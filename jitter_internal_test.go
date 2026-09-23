package cistern

import (
	"context"
	"testing"
	"time"
)

// ttlStore records the TTL of the last Set.
type ttlStore struct{ ttl time.Duration }

func (*ttlStore) Get(context.Context, string) (value []byte, ok bool, err error) {
	return nil, false, nil
}

func (s *ttlStore) Set(_ context.Context, _ string, _ []byte, ttl time.Duration) error {
	s.ttl = ttl
	return nil
}

func (*ttlStore) Delete(context.Context, string) error { return nil }

// RF-04: jitter only shortens a TTL, by at most the configured fraction, and
// the same draw applies to both levels so L1 never outlives L2 (ADR-0004).
// An internal test, so the draw can be fixed without exporting a hook.
func TestJitterShortensTTLWithinBounds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		draw   float64
		wantL1 time.Duration
		wantL2 time.Duration
	}{
		{"no reduction", 0, 10 * time.Second, 100 * time.Second},
		{"half the maximum reduction", 0.5, 9 * time.Second, 90 * time.Second},
		{"just under the maximum", 0.999999, 8*time.Second + 2*time.Microsecond, 80*time.Second + 20*time.Microsecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l1, l2 := &ttlStore{}, &ttlStore{}
			c, err := New[string, string]("tasks", func(k string) string { return k },
				WithL1(l1), WithL2(l2), WithTTL(100*time.Second), WithL1TTL(10*time.Second), WithJitter(0.2))
			if err != nil {
				t.Fatal(err)
			}
			c.rand = func() float64 { return tc.draw }
			if err := c.Set(context.Background(), "k", "v"); err != nil {
				t.Fatal(err)
			}
			for level, got := range map[string][2]time.Duration{"L1": {l1.ttl, tc.wantL1}, "L2": {l2.ttl, tc.wantL2}} {
				if d := got[0] - got[1]; d < -time.Millisecond || d > time.Millisecond {
					t.Errorf("%s TTL = %v, want about %v", level, got[0], got[1])
				}
			}
			if l1.ttl > l2.ttl {
				t.Errorf("L1 TTL %v outlives L2 TTL %v", l1.ttl, l2.ttl)
			}
		})
	}
}
