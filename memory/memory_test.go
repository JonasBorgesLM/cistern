package memory_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/JonasBorgesLM/cistern"
	"github.com/JonasBorgesLM/cistern/cisterntest"
	"github.com/JonasBorgesLM/cistern/memory"
)

var _ cistern.Store = (*memory.Store)(nil)

const hour = time.Hour

func newStore(t *testing.T, opts ...memory.Option) *memory.Store {
	t.Helper()
	s, err := memory.New(opts...)
	if err != nil {
		t.Fatalf("memory.New: %v", err)
	}
	return s
}

func mustSet(t *testing.T, s *memory.Store, key, value string, ttl time.Duration) {
	t.Helper()
	if err := s.Set(context.Background(), key, []byte(value), ttl); err != nil {
		t.Fatalf("Set(%q): %v", key, err)
	}
}

func get(t *testing.T, s *memory.Store, key string) (string, bool) {
	t.Helper()
	v, ok, err := s.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	return string(v), ok
}

func wantValue(t *testing.T, s *memory.Store, key, want string) {
	t.Helper()
	got, ok := get(t, s, key)
	if !ok || got != want {
		t.Fatalf("Get(%q) = %q, %v; want %q, true", key, got, ok, want)
	}
}

func wantMiss(t *testing.T, s *memory.Store, key string) {
	t.Helper()
	if got, ok := get(t, s, key); ok {
		t.Fatalf("Get(%q) = %q, true; want a miss", key, got)
	}
}

func TestSetThenGet(t *testing.T) {
	s := newStore(t)
	mustSet(t, s, "a", "1", hour)
	wantValue(t, s, "a", "1")
	wantMiss(t, s, "never-set")
}

func TestSetReplacesExistingValue(t *testing.T) {
	s := newStore(t)
	mustSet(t, s, "a", "old", hour)
	mustSet(t, s, "a", "new", hour)
	wantValue(t, s, "a", "new")
}

func TestDelete(t *testing.T) {
	s := newStore(t)
	mustSet(t, s, "a", "1", hour)
	if err := s.Delete(context.Background(), "a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	wantMiss(t, s, "a")
	if err := s.Delete(context.Background(), "a"); err != nil {
		t.Fatalf("Delete of a missing key: %v, want nil", err)
	}
}

// ADR-0014: each caller gets its own bytes, so a mutation by one caller can
// never reach the entry another caller reads.
func TestCallersNeverShareBytesWithTheStore(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	in := []byte("original")
	if err := s.Set(ctx, "a", in, hour); err != nil {
		t.Fatal(err)
	}
	copy(in, "XXXXXXXX")
	wantValue(t, s, "a", "original")

	out, _, err := s.Get(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	copy(out, "YYYYYYYY")
	wantValue(t, s, "a", "original")
}

func TestSetRejectsNonPositiveTTL(t *testing.T) {
	s := newStore(t)
	for _, ttl := range []time.Duration{0, -time.Second} {
		if err := s.Set(context.Background(), "a", []byte("1"), ttl); err == nil {
			t.Errorf("Set with ttl %v = nil error, want an error", ttl)
		}
	}
	wantMiss(t, s, "a")
}

func TestEntryLimitEvictsLeastRecentlyUsed(t *testing.T) {
	s := newStore(t, memory.WithMaxEntries(2))
	mustSet(t, s, "a", "1", hour)
	mustSet(t, s, "b", "2", hour)
	wantValue(t, s, "a", "1") // a is now more recently used than b
	mustSet(t, s, "c", "3", hour)

	wantMiss(t, s, "b")
	wantValue(t, s, "a", "1")
	wantValue(t, s, "c", "3")
}

// An entry costs len(key) + len(value) bytes.
func TestByteLimitEvictsLeastRecentlyUsedUntilItFits(t *testing.T) {
	s := newStore(t, memory.WithMaxBytes(12))
	mustSet(t, s, "a", "11111", hour) // 6 bytes
	mustSet(t, s, "b", "22222", hour) // 6 bytes, total 12
	mustSet(t, s, "c", "33", hour)    // 3 bytes: a must go

	wantMiss(t, s, "a")
	wantValue(t, s, "b", "22222")
	wantValue(t, s, "c", "33")

	mustSet(t, s, "d", "4444444444", hour) // 11 bytes: b and c must go
	wantMiss(t, s, "b")
	wantMiss(t, s, "c")
	wantValue(t, s, "d", "4444444444")
}

// ADR-0010: a value larger than the whole byte limit is not stored. It must
// also not leave an older value behind under the same key, or the cache would
// keep serving what the caller just replaced.
func TestOversizedValueIsNotStoredAndDropsTheOldOne(t *testing.T) {
	s := newStore(t, memory.WithMaxBytes(10))
	mustSet(t, s, "a", "old", hour)  // 4 bytes
	mustSet(t, s, "b", "keep", hour) // 5 bytes, total 9

	if err := s.Set(context.Background(), "a", []byte("far too large"), hour); err != nil {
		t.Fatalf("Set of an oversized value: %v, want nil (it is skipped, not an error)", err)
	}
	wantMiss(t, s, "a")
	wantValue(t, s, "b", "keep")
}

func TestOperationsHonourCancelledContext(t *testing.T) {
	s := newStore(t)
	mustSet(t, s, "a", "1", hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := s.Get(ctx, "a"); !errors.Is(err, context.Canceled) {
		t.Errorf("Get: err = %v, want context.Canceled", err)
	}
	if err := s.Set(ctx, "b", []byte("2"), hour); !errors.Is(err, context.Canceled) {
		t.Errorf("Set: err = %v, want context.Canceled", err)
	}
	if err := s.Delete(ctx, "a"); !errors.Is(err, context.Canceled) {
		t.Errorf("Delete: err = %v, want context.Canceled", err)
	}
	wantMiss(t, s, "b")
	wantValue(t, s, "a", "1")
}

func TestNewRejectsNonPositiveLimits(t *testing.T) {
	for name, opt := range map[string]memory.Option{
		"zero entries":   memory.WithMaxEntries(0),
		"negative bytes": memory.WithMaxBytes(-1),
		"zero bytes":     memory.WithMaxBytes(0),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := memory.New(opt); !errors.Is(err, cistern.ErrInvalidConfig) {
				t.Fatalf("New: err = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestConcurrentUse(t *testing.T) {
	s := newStore(t, memory.WithMaxEntries(64))
	ctx := context.Background()
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 500 {
				key := fmt.Sprintf("k%d", (g*i)%100)
				_ = s.Set(ctx, key, []byte(key), hour)
				if v, ok, _ := s.Get(ctx, key); ok && !bytes.Equal(v, []byte(key)) {
					t.Errorf("Get(%q) returned %q", key, v)
				}
				if i%7 == 0 {
					_ = s.Delete(ctx, key)
				}
			}
		}()
	}
	wg.Wait()
}

// RF-14: memory passes the conformance suite every Store must pass.
func TestConformance(t *testing.T) {
	cisterntest.RunStore(t, func(t *testing.T) cistern.Store { return newStore(t) })
}

// ADR-0005: memory keeps tag generations when it is a cache's only level.
func TestTagStoreConformance(t *testing.T) {
	cisterntest.RunTagStore(t, func(t *testing.T) cistern.TagStore { return newStore(t) })
}
