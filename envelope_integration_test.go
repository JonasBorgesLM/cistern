package cistern_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JonasBorgesLM/cistern"
	"github.com/JonasBorgesLM/cistern/internal/envelope"
)

const pk = "cistern:v1:tasks:-:k:k"

func plant(t *testing.T, r *recorder, e envelope.Entry) {
	t.Helper()
	data, err := envelope.Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Set(context.Background(), pk, data, time.Hour); err != nil {
		t.Fatal(err)
	}
}

func wantMiss(t *testing.T, c *cistern.Cache[string, string]) {
	t.Helper()
	if v, ok, err := c.Get(context.Background(), "k"); err != nil || ok {
		t.Fatalf("Get = %q, %v, %v; want zero, false, nil", v, ok, err)
	}
}

// ADR-0009: what reaches a store is an envelope naming its codec and expiry.
func TestStoredEntryIsAnEnvelope(t *testing.T) {
	l2 := newRecorder()
	c := newCache(t, cistern.WithL2(l2), cistern.WithTTL(time.Minute))
	before := time.Now()
	if err := c.Set(context.Background(), "k", "v"); err != nil {
		t.Fatal(err)
	}
	e, err := envelope.Decode(l2.data[pk], 1<<20)
	if err != nil {
		t.Fatalf("stored bytes are not an envelope: %v", err)
	}
	if e.Absent || e.Codec != "json" || string(e.Payload) != `"v"` {
		t.Fatalf("stored envelope = %+v", e)
	}
	if e.Expires.Before(before.Add(time.Minute)) || e.Expires.After(time.Now().Add(time.Minute)) {
		t.Fatalf("logical expiry %v is not the TTL from now", e.Expires)
	}
}

// RS-06: the data never picks its codec.
// Negative control: verified failing with the codec id check removed.
func TestEntryFromAnotherCodecIsAMiss(t *testing.T) {
	l2 := newRecorder()
	c := newCache(t, cistern.WithL2(l2), cistern.WithTTL(time.Minute))
	plant(t, l2, envelope.Entry{Codec: "gob", Expires: time.Now().Add(time.Hour), Payload: []byte(`"v"`)})
	wantMiss(t, c)
}

// ADR-0009: past its logical expiry an entry is a miss, whatever the store
// still returns.
// Negative control: verified failing with the expiry check removed.
func TestEntryPastItsLogicalExpiryIsAMiss(t *testing.T) {
	l2 := newRecorder()
	c := newCache(t, cistern.WithL2(l2), cistern.WithTTL(time.Minute))
	plant(t, l2, envelope.Entry{Codec: "json", Expires: time.Now().Add(-time.Second), Payload: []byte(`"v"`)})
	wantMiss(t, c)
}

func TestMalformedOrForeignEntriesAreAMiss(t *testing.T) {
	for name, data := range map[string][]byte{
		"pre-envelope tag format": []byte(`v"v"`),
		"not an envelope":         []byte("{not json"),
		"payload not decodable":   mustEnvelope(t, envelope.Entry{Codec: "json", Expires: time.Now().Add(time.Hour), Payload: []byte("{bad")}),
	} {
		t.Run(name, func(t *testing.T) {
			l2 := newRecorder()
			c := newCache(t, cistern.WithL2(l2), cistern.WithTTL(time.Minute))
			l2.data[pk] = data
			wantMiss(t, c)
		})
	}
}

func mustEnvelope(t *testing.T, e envelope.Entry) []byte {
	t.Helper()
	data, err := envelope.Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// RS-05, T-04: the value limit holds on the way in and on the way out.
// Negative control: verified failing with the Set-side limit removed.
func TestValueLimit(t *testing.T) {
	ctx := context.Background()
	big := strings.Repeat("x", 64)

	t.Run("Set over the limit fails loud", func(t *testing.T) {
		l2 := newRecorder()
		c := newCache(t, cistern.WithL2(l2), cistern.WithTTL(time.Minute), cistern.WithMaxValueBytes(32))
		if err := c.Set(ctx, "k", big); !errors.Is(err, cistern.ErrValueTooLarge) {
			t.Fatalf("Set: err = %v, want ErrValueTooLarge", err)
		}
		if len(l2.keys()) != 0 {
			t.Fatal("an oversized value reached the store")
		}
	})

	t.Run("a loaded value over the limit is returned but not cached", func(t *testing.T) {
		l2 := newRecorder()
		c := newCache(t, cistern.WithL2(l2), cistern.WithTTL(time.Minute), cistern.WithMaxValueBytes(32))
		var calls atomic.Int32
		v, err := c.GetOrLoad(ctx, "k", countingLoader(big, &calls))
		if err != nil || v != big {
			t.Fatalf("GetOrLoad = %d bytes, %v; want the value", len(v), err)
		}
		if len(l2.keys()) != 0 {
			t.Fatal("an oversized loaded value reached the store")
		}
	})

	t.Run("an oversized stored entry is a miss", func(t *testing.T) {
		l2 := newRecorder()
		c := newCache(t, cistern.WithL2(l2), cistern.WithTTL(time.Minute), cistern.WithMaxValueBytes(32))
		plant(t, l2, envelope.Entry{Codec: "json", Expires: time.Now().Add(time.Hour), Payload: []byte(`"` + big + `"`)})
		wantMiss(t, c)
	})
}

func TestNewValidatesTheValueLimit(t *testing.T) {
	for _, n := range []int{0, -1} {
		_, err := cistern.New[string, string]("tasks", stringKey, cistern.WithL1(newRecorder()), cistern.WithTTL(time.Minute), cistern.WithMaxValueBytes(n))
		if !errors.Is(err, cistern.ErrInvalidConfig) {
			t.Errorf("WithMaxValueBytes(%d): err = %v, want ErrInvalidConfig", n, err)
		}
	}
}
