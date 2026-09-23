package envelope_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/JonasBorgesLM/cistern/internal/envelope"
)

const limit = 1 << 10

var expires = time.Unix(1_700_000_000, 123)

func mustEncode(t testing.TB, e envelope.Entry) []byte {
	t.Helper()
	data, err := envelope.Encode(e)
	if err != nil {
		t.Fatalf("Encode(%+v): %v", e, err)
	}
	return data
}

func TestRoundTrip(t *testing.T) {
	for name, in := range map[string]envelope.Entry{
		"value":             {Codec: "json", Expires: expires, Payload: []byte(`{"id":7}`)},
		"absence":           {Codec: "json", Expires: expires, Absent: true},
		"value with tags":   {Codec: "json", Expires: expires, Gens: []uint64{7, 1 << 61}, Payload: []byte(`{"id":7}`)},
		"absence with tags": {Codec: "json", Expires: expires, Gens: []uint64{3}, Absent: true},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := envelope.Decode(mustEncode(t, in), limit)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if out.Absent != in.Absent || out.Codec != in.Codec || !out.Expires.Equal(in.Expires) ||
				!bytes.Equal(out.Payload, in.Payload) || !slices.Equal(out.Gens, in.Gens) {
				t.Fatalf("round trip changed the entry: got %+v, want %+v", out, in)
			}
		})
	}
}

// The layout is a storage format other versions will read: pin it byte for
// byte, not only through a round trip that would agree with any layout.
func TestLayoutIsTheOneADR0009Records(t *testing.T) {
	got := mustEncode(t, envelope.Entry{Codec: "json", Expires: expires, Gens: []uint64{9, 10}, Payload: []byte("{}")})
	want := []byte{2, 0}
	want = binary.BigEndian.AppendUint64(want, uint64(expires.UnixNano()))
	want = append(want, 4)
	want = append(want, "json"...)
	want = append(want, 2)
	want = binary.BigEndian.AppendUint64(want, 9)
	want = binary.BigEndian.AppendUint64(want, 10)
	want = append(want, "{}"...)
	if !bytes.Equal(got, want) {
		t.Fatalf("Encode = % x\nwant     % x", got, want)
	}
	absent := mustEncode(t, envelope.Entry{Codec: "json", Expires: expires, Absent: true})
	if absent[1] != 1 {
		t.Fatalf("absence flags byte = %d, want 1", absent[1])
	}
}

func TestEncodeRejectsInvalidEntries(t *testing.T) {
	for name, e := range map[string]envelope.Entry{
		"empty codec id":         {Codec: "", Expires: expires, Payload: []byte("{}")},
		"codec id too long":      {Codec: string(make([]byte, 33)), Expires: expires, Payload: []byte("{}")},
		"zero expiry":            {Codec: "json", Payload: []byte("{}")},
		"expiry before epoch":    {Codec: "json", Expires: time.Unix(-1, 0), Payload: []byte("{}")},
		"value without payload":  {Codec: "json", Expires: expires},
		"absence with a payload": {Codec: "json", Expires: expires, Absent: true, Payload: []byte("{}")},
		"more than 8 tags":       {Codec: "json", Expires: expires, Gens: make([]uint64, 9), Payload: []byte("{}")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := envelope.Encode(e); err == nil {
				t.Fatal("Encode = nil error, want an error")
			}
		})
	}
}

// RS-06: L2 bytes are untrusted. Every malformation is rejected with
// ErrMalformed — never accepted, never a panic.
func TestDecodeRejectsMalformedData(t *testing.T) {
	good := mustEncode(t, envelope.Entry{Codec: "json", Expires: expires, Payload: []byte("{}")})
	mutate := func(f func([]byte) []byte) []byte { return f(append([]byte(nil), good...)) }

	for name, data := range map[string][]byte{
		"empty":                  {},
		"shorter than header":    good[:10],
		"codec id cut short":     good[:12],
		"no tag count":           good[:15],
		"tag count above 8":      mutate(func(b []byte) []byte { b[15] = 9; return b }),
		"generations cut short":  mutate(func(b []byte) []byte { b[15] = 1; return b[:20] }),
		"unknown version":        mutate(func(b []byte) []byte { b[0] = 3; return b }),
		"version 1 (pre-tags)":   mutate(func(b []byte) []byte { b[0] = 1; return b }),
		"version zero":           mutate(func(b []byte) []byte { b[0] = 0; return b }),
		"unknown flag":           mutate(func(b []byte) []byte { b[1] = 0b10; return b }),
		"zero expiry":            mutate(func(b []byte) []byte { clear(b[2:10]); return b }),
		"negative expiry":        mutate(func(b []byte) []byte { b[2] = 0x80; return b }),
		"zero codec length":      mutate(func(b []byte) []byte { b[10] = 0; return b }),
		"codec length too large": mutate(func(b []byte) []byte { b[10] = 33; return b }),
		"value without payload":  mutate(func(b []byte) []byte { return b[:len(b)-2] }),
		"value with a tag and no payload": mutate(func(b []byte) []byte {
			b[15] = 1
			return append(b[:16], 0, 0, 0, 0, 0, 0, 0, 7)
		}),
		"absence with payload":   mutate(func(b []byte) []byte { b[1] = 1; return b }),
		"payload over the limit": append(mutate(func(b []byte) []byte { return b[:len(b)-2] }), make([]byte, limit+1)...),
	} {
		t.Run(name, func(t *testing.T) {
			if e, err := envelope.Decode(data, limit); !errors.Is(err, envelope.ErrMalformed) {
				t.Fatalf("Decode = %+v, %v; want ErrMalformed", e, err)
			}
		})
	}
}

func TestPayloadAtTheLimitIsAccepted(t *testing.T) {
	data := mustEncode(t, envelope.Entry{Codec: "json", Expires: expires, Payload: make([]byte, limit)})
	if _, err := envelope.Decode(data, limit); err != nil {
		t.Fatalf("Decode of a payload exactly at the limit: %v", err)
	}
}

// RS-06, #34: arbitrary bytes never panic the decoder, and whatever it accepts
// is exactly what Encode would have produced.
func FuzzDecode(f *testing.F) {
	f.Add(mustEncode(f, envelope.Entry{Codec: "json", Expires: expires, Payload: []byte(`{"id":7}`)}))
	f.Add(mustEncode(f, envelope.Entry{Codec: "json", Expires: expires, Absent: true}))
	f.Add(mustEncode(f, envelope.Entry{Codec: "json", Expires: expires, Gens: []uint64{1, 2, 3}, Payload: []byte(`"v"`)}))
	f.Add([]byte{})
	f.Add([]byte("v{\"legacy\":true}"))
	f.Fuzz(func(t *testing.T, data []byte) {
		e, err := envelope.Decode(data, limit)
		if err != nil {
			if !errors.Is(err, envelope.ErrMalformed) {
				t.Fatalf("Decode error %v does not wrap ErrMalformed", err)
			}
			return
		}
		again, err := envelope.Encode(e)
		if err != nil {
			t.Fatalf("Encode of a decoded entry: %v", err)
		}
		if !bytes.Equal(again, data) {
			t.Fatalf("accepted bytes are not canonical:\n in  % x\n out % x", data, again)
		}
	})
}
