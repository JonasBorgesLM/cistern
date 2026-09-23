package codec_test

import (
	"reflect"
	"testing"

	"github.com/JonasBorgesLM/cistern/codec"
)

type task struct {
	ID    int      `json:"id"`
	Title string   `json:"title"`
	Tags  []string `json:"tags"`
}

func TestJSONRoundTrip(t *testing.T) {
	in := task{ID: 7, Title: "write ADR-0005", Tags: []string{"c6", "adr"}}

	data, err := codec.JSON.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out task
	if err := codec.JSON.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip changed the value: got %+v, want %+v", out, in)
	}
}

func TestJSONRejectsMalformedInput(t *testing.T) {
	for name, data := range map[string][]byte{
		"truncated":        []byte(`{"id":7,"title":`),
		"trailing garbage": []byte(`{"id":7} {}`),
		"wrong type":       []byte(`{"id":"seven"}`),
		"empty":            {},
	} {
		t.Run(name, func(t *testing.T) {
			var out task
			if err := codec.JSON.Unmarshal(data, &out); err == nil {
				t.Fatalf("Unmarshal(%q) = nil error, want an error", data)
			}
		})
	}
}

func TestJSONMarshalErrorIsReturned(t *testing.T) {
	if _, err := codec.JSON.Marshal(make(chan int)); err == nil {
		t.Fatal("Marshal(chan) = nil error, want an error")
	}
}

func TestJSONID(t *testing.T) {
	// The id is written into stored data (the envelope, ADR-0009), so it is a
	// format promise: changing it orphans every entry already in L2.
	if got := codec.JSON.ID(); got != "json" {
		t.Fatalf("ID() = %q, want %q", got, "json")
	}
}
