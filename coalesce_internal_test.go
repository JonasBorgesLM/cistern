package cistern

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type nopStore struct{}

func (nopStore) Get(context.Context, string) (value []byte, ok bool, err error) {
	return nil, false, nil
}
func (nopStore) Set(context.Context, string, []byte, time.Duration) error { return nil }
func (nopStore) Delete(context.Context, string) error                     { return nil }

// joinAll starts n GetOrLoad callers on key and returns once all of them have
// joined one flight, so no caller can be served by a later cache hit instead.
func joinAll[V any](t *testing.T, c *Cache[string, V], key string, n int, load Loader[V]) (results chan V) {
	t.Helper()
	results = make(chan V, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := c.GetOrLoad(context.Background(), key, load)
			if err != nil {
				t.Errorf("GetOrLoad: %v", err)
			}
			results <- v
		}()
	}
	pk, err := c.physicalKey(key)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for c.flights.Waiting(pk) != n {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d callers joined the flight", c.flights.Waiting(pk), n)
		}
		time.Sleep(time.Millisecond)
	}
	t.Cleanup(wg.Wait)
	return results
}

// RF-05, T-10: N concurrent callers of a missing key cost one load.
// Negative control: verified failing with GetOrLoad calling the loader
// directly instead of through the flight group.
func TestOneLoadForNConcurrentCallers(t *testing.T) {
	const n = 64
	c, err := New[string, string]("tasks", func(k string) string { return k }, WithL2(nopStore{}), WithTTL(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	release := make(chan struct{})
	results := joinAll(t, c, "hot", n, func(context.Context) (string, error) {
		calls.Add(1)
		<-release
		return "v", nil
	})
	close(release)

	for range n {
		if v := <-results; v != "v" {
			t.Fatalf("a caller got %q", v)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("loader called %d times for %d concurrent callers, want 1", got, n)
	}
}

// ADR-0007 with ADR-0014: coalesced callers each decode their own copy, so one
// caller mutating its result cannot reach another's.
// Negative control: verified failing with the flight sharing the decoded value.
func TestCoalescedCallersDoNotShareAValue(t *testing.T) {
	const n = 8
	c, err := New[string, []int]("tasks", func(k string) string { return k }, WithL2(nopStore{}), WithTTL(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	results := joinAll(t, c, "hot", n, func(context.Context) ([]int, error) {
		<-release
		return []int{1, 2, 3}, nil
	})
	close(release)

	got := make([][]int, 0, n)
	for range n {
		got = append(got, <-results)
	}
	got[0][0] = 99
	for i, v := range got[1:] {
		if v[0] != 1 {
			t.Fatalf("caller %d sees %v after caller 0 mutated its own result", i+1, v)
		}
	}
}

// RF-15: one load event for the caller that led, one coalesced event for each
// caller served by it.
func TestCoalescedCallersAreReported(t *testing.T) {
	const n = 6
	var mu sync.Mutex
	var loads, coalesced int
	hooks := Hooks{
		OnLoad:      func(context.Context, LoadEvent) { mu.Lock(); loads++; mu.Unlock() },
		OnCoalesced: func(context.Context, CoalescedEvent) { mu.Lock(); coalesced++; mu.Unlock() },
	}
	c, err := New[string, string]("tasks", func(k string) string { return k }, WithL2(nopStore{}), WithTTL(time.Minute), WithHooks(hooks))
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	results := joinAll(t, c, "hot", n, func(context.Context) (string, error) {
		<-release
		return "v", nil
	})
	close(release)
	for range n {
		<-results
	}
	mu.Lock()
	defer mu.Unlock()
	if loads != 1 || coalesced != n-1 {
		t.Fatalf("loads = %d, coalesced = %d; want 1 and %d", loads, coalesced, n-1)
	}
}
