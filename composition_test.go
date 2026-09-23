package cistern_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/JonasBorgesLM/cistern"
	"github.com/JonasBorgesLM/cistern/internal/envelope"
)

func twoLevels(t *testing.T, opts ...cistern.Option) (c *cistern.Cache[string, string], l1, l2 *recorder) {
	t.Helper()
	l1, l2 = newRecorder(), newRecorder()
	c = newCache(t, append([]cistern.Option{cistern.WithL1(l1), cistern.WithL2(l2), cistern.WithTTL(time.Minute)}, opts...)...)
	return c, l1, l2
}

// RF-07: an L2 hit fills L1, so the next read does not leave the process.
// Negative control: verified failing with the backfill removed.
func TestL2HitBackfillsL1(t *testing.T) {
	ctx := context.Background()
	c, l1, l2 := twoLevels(t)
	plant(t, l2, envelope.Entry{Codec: "json", Expires: time.Now().Add(time.Minute), Payload: []byte(`"from L2"`)})

	if v, ok, err := c.Get(ctx, "k"); err != nil || !ok || v != "from L2" {
		t.Fatalf("first Get = %q, %v, %v", v, ok, err)
	}
	l2.failGet = errStore // from here on, only L1 can answer
	if v, ok, err := c.Get(ctx, "k"); err != nil || !ok || v != "from L2" {
		t.Fatalf("Get after the backfill = %q, %v, %v; want it served by L1", v, ok, err)
	}
	if len(l1.keys()) != 1 {
		t.Fatalf("L1 holds %d entries, want the backfilled one", len(l1.keys()))
	}
}

// ADR-0004: a backfilled L1 copy never outlives the L2 entry it came from —
// its TTL is the smaller of the L1 TTL and what the L2 entry has left.
// Negative control: verified failing with the clamp to the L2 expiry removed.
func TestBackfillNeverOutlivesTheL2Entry(t *testing.T) {
	ctx := context.Background()

	t.Run("L2 entry expires first", func(t *testing.T) {
		c, l1, l2 := twoLevels(t, cistern.WithL1TTL(30*time.Second))
		plant(t, l2, envelope.Entry{Codec: "json", Expires: time.Now().Add(2 * time.Second), Payload: []byte(`"v"`)})
		if _, ok, _ := c.Get(ctx, "k"); !ok {
			t.Fatal("miss")
		}
		if got := l1.ttls[pk]; got <= 0 || got > 2*time.Second {
			t.Fatalf("backfilled L1 TTL = %v, want at most the 2s the L2 entry has left", got)
		}
	})

	t.Run("L1 TTL is shorter", func(t *testing.T) {
		c, l1, l2 := twoLevels(t, cistern.WithL1TTL(5*time.Second))
		plant(t, l2, envelope.Entry{Codec: "json", Expires: time.Now().Add(time.Hour), Payload: []byte(`"v"`)})
		if _, ok, _ := c.Get(ctx, "k"); !ok {
			t.Fatal("miss")
		}
		if got := l1.ttls[pk]; got != 5*time.Second {
			t.Fatalf("backfilled L1 TTL = %v, want the 5s L1 TTL", got)
		}
	})
}

// RF-06 with RF-07: a remembered absence found in L2 is remembered in L1 too.
func TestAbsenceIsBackfilled(t *testing.T) {
	ctx := context.Background()
	c, _, l2 := twoLevels(t)
	plant(t, l2, envelope.Entry{Codec: "json", Expires: time.Now().Add(time.Minute), Absent: true})

	if _, _, err := c.Get(ctx, "k"); !errors.Is(err, cistern.ErrNotFound) {
		t.Fatalf("first Get: err = %v, want ErrNotFound", err)
	}
	l2.failGet = errStore
	if _, _, err := c.Get(ctx, "k"); !errors.Is(err, cistern.ErrNotFound) {
		t.Fatalf("Get after the backfill: err = %v, want ErrNotFound from L1", err)
	}
}

// ADR-0002: a failed backfill is not the reader's problem.
func TestFailedBackfillStillReturnsTheValue(t *testing.T) {
	c, l1, l2 := twoLevels(t)
	plant(t, l2, envelope.Entry{Codec: "json", Expires: time.Now().Add(time.Minute), Payload: []byte(`"v"`)})
	l1.failSet = errStore
	if v, ok, err := c.Get(context.Background(), "k"); err != nil || !ok || v != "v" {
		t.Fatalf("Get = %q, %v, %v; want the L2 value", v, ok, err)
	}
}

// GetOrLoad reads through the same path: an L2 hit is served and backfilled
// without calling the loader.
func TestGetOrLoadServesAndBackfillsAnL2Hit(t *testing.T) {
	c, l1, l2 := twoLevels(t)
	plant(t, l2, envelope.Entry{Codec: "json", Expires: time.Now().Add(time.Minute), Payload: []byte(`"v"`)})
	load := func(context.Context) (string, error) {
		t.Fatal("the loader ran on an L2 hit")
		return "", nil
	}
	if v, err := c.GetOrLoad(context.Background(), "k", load); err != nil || v != "v" {
		t.Fatalf("GetOrLoad = %q, %v", v, err)
	}
	if len(l1.keys()) != 1 {
		t.Fatal("the L2 hit was not backfilled into L1")
	}
}

// Only what decodes is copied: an unusable L2 entry must not be multiplied
// into L1.
// Negative control: verified failing with the backfill done before decoding.
func TestUndecodableL2EntryIsNotBackfilled(t *testing.T) {
	c, l1, l2 := twoLevels(t)
	plant(t, l2, envelope.Entry{Codec: "json", Expires: time.Now().Add(time.Minute), Payload: []byte("{bad")})
	if _, ok, err := c.Get(context.Background(), "k"); ok || err != nil {
		t.Fatalf("Get = %v, %v; want a miss", ok, err)
	}
	if len(l1.keys()) != 0 {
		t.Fatal("an undecodable L2 entry was copied into L1")
	}
}
