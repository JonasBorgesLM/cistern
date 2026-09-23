package cisterntest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/JonasBorgesLM/cistern"
)

const (
	genA = "cistern:v1:cisterntest:g:user:42:lists"
	genB = "cistern:v1:cisterntest:g:user:43:lists"
)

// RunTagStore runs the TagStore conformance suite (ADR-0005), which includes
// the T-14 case: a counter that disappears must never come back at a value it
// held. newStore is called once per subtest and must return an empty store.
// A TagStore must also pass RunStore.
func RunTagStore(t *testing.T, newStore func(t *testing.T) cistern.TagStore) {
	t.Helper()
	for _, tc := range []struct {
		name string
		run  func(*testing.T, cistern.TagStore)
	}{
		{"MissingCounterIsCreatedUnpredictably", missingCounterIsCreatedUnpredictably},
		{"GenerationsAreStable", generationsAreStable},
		{"ValueAndGenerationsTogether", valueAndGenerationsTogether},
		{"MissingValueStillReturnsGenerations", missingValueStillReturnsGenerations},
		{"BumpAdvances", bumpAdvances},
		{"BumpOfAMissingCounterIsUnpredictable", bumpOfAMissingCounterIsUnpredictable},
		{"VanishedCounterNeverRepeats", vanishedCounterNeverRepeats},
		{"CountersExpire", countersExpire},
		{"NonPositiveCounterTTLIsRejected", nonPositiveCounterTTLIsRejected},
		{"CancelledContextIsAnError", taggedCancelledContextIsAnError},
		{"ConcurrentBumpsAreAtomic", concurrentBumpsAreAtomic},
	} {
		t.Run(tc.name, func(t *testing.T) { tc.run(t, newStore(t)) })
	}
}

func gens(t *testing.T, s cistern.TagStore, genKeys ...string) []uint64 {
	t.Helper()
	_, _, g, err := s.GetTagged(context.Background(), keyA, genKeys, time.Minute)
	if err != nil {
		t.Fatalf("GetTagged: %v", err)
	}
	if len(g) != len(genKeys) {
		t.Fatalf("GetTagged returned %d generations for %d counters", len(g), len(genKeys))
	}
	return g
}

func bump(t *testing.T, s cistern.TagStore, genKeys ...string) {
	t.Helper()
	if err := s.Bump(context.Background(), genKeys, time.Minute); err != nil {
		t.Fatalf("Bump: %v", err)
	}
}

func inRange(g uint64) bool { return g >= 1 && g < 1<<62 }

// A counter created at 0 or 1 is how an evicted counter repeats (T-14).
func missingCounterIsCreatedUnpredictably(t *testing.T, s cistern.TagStore) {
	g := gens(t, s, genA, genB)
	for i, v := range g {
		if !inRange(v) || v < 1<<20 {
			t.Fatalf("new counter %d = %d, want an unpredictable value in [1, 2^62)", i, v)
		}
	}
	if g[0] == g[1] {
		t.Fatalf("two new counters got the same generation %d", g[0])
	}
}

func generationsAreStable(t *testing.T, s cistern.TagStore) {
	first := gens(t, s, genA)[0]
	if again := gens(t, s, genA)[0]; again != first {
		t.Fatalf("an untouched counter moved from %d to %d", first, again)
	}
}

func valueAndGenerationsTogether(t *testing.T, s cistern.TagStore) {
	set(t, s, keyA, []byte("value"), time.Minute)
	v, ok, g, err := s.GetTagged(context.Background(), keyA, []string{genA}, time.Minute)
	if err != nil || !ok || string(v) != "value" || len(g) != 1 {
		t.Fatalf("GetTagged = %q, %v, %v, %v; want the value and one generation", v, ok, g, err)
	}
}

func missingValueStillReturnsGenerations(t *testing.T, s cistern.TagStore) {
	_, ok, g, err := s.GetTagged(context.Background(), keyA, []string{genA}, time.Minute)
	if err != nil || ok || len(g) != 1 || !inRange(g[0]) {
		t.Fatalf("GetTagged of a missing value = %v, %v, %v; want a miss with a generation", ok, g, err)
	}
}

func bumpAdvances(t *testing.T, s cistern.TagStore) {
	before := gens(t, s, genA, genB)
	bump(t, s, genA)
	after := gens(t, s, genA, genB)
	if after[0] == before[0] {
		t.Fatalf("Bump left the counter at %d", after[0])
	}
	if after[1] != before[1] {
		t.Fatal("Bump of one counter moved another")
	}
}

func bumpOfAMissingCounterIsUnpredictable(t *testing.T, s cistern.TagStore) {
	bump(t, s, genA)
	if g := gens(t, s, genA)[0]; !inRange(g) || g < 1<<20 {
		t.Fatalf("a counter created by Bump = %d, want an unpredictable value", g)
	}
}

// T-14: deleting a counter stands in for eviction or expiry. Whatever comes
// back must be a value the counter never held, or an entry that recorded it
// would be served again.
func vanishedCounterNeverRepeats(t *testing.T, s cistern.TagStore) {
	held := map[uint64]bool{gens(t, s, genA)[0]: true}
	bump(t, s, genA)
	held[gens(t, s, genA)[0]] = true
	if err := s.Delete(context.Background(), genA); err != nil {
		t.Fatalf("Delete of the counter: %v", err)
	}
	if g := gens(t, s, genA)[0]; held[g] {
		t.Fatalf("a vanished counter came back at %d, a value it held before", g)
	}
}

func countersExpire(t *testing.T, s cistern.TagStore) {
	_, _, g, err := s.GetTagged(context.Background(), keyA, []string{genA}, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, _, now, err := s.GetTagged(context.Background(), keyA, []string{genA}, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if now[0] != g[0] {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("a counter with a 100ms TTL was still there after 5s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func nonPositiveCounterTTLIsRejected(t *testing.T, s cistern.TagStore) {
	ctx := context.Background()
	if _, _, _, err := s.GetTagged(ctx, keyA, []string{genA}, 0); err == nil {
		t.Error("GetTagged with a zero counter TTL = nil error")
	}
	if err := s.Bump(ctx, []string{genA}, 0); err == nil {
		t.Error("Bump with a zero counter TTL = nil error")
	}
}

func taggedCancelledContextIsAnError(t *testing.T, s cistern.TagStore) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := s.GetTagged(ctx, keyA, []string{genA}, time.Minute); err == nil {
		t.Error("GetTagged with a cancelled context = nil error")
	}
	if err := s.Bump(ctx, []string{genA}, time.Minute); err == nil {
		t.Error("Bump with a cancelled context = nil error")
	}
}

// Bumps from many replicas at once must all count: a lost increment is an
// invalidation that did not happen.
func concurrentBumpsAreAtomic(t *testing.T, s cistern.TagStore) {
	const goroutines, each = 8, 25
	start := gens(t, s, genA)[0]
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range each {
				if err := s.Bump(context.Background(), []string{genA}, time.Minute); err != nil {
					t.Errorf("Bump: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if got, want := gens(t, s, genA)[0], start+goroutines*each; got != want {
		t.Fatalf("after %d concurrent bumps the counter is %d, want %d", goroutines*each, got, want)
	}
}
