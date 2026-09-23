package singleflight_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JonasBorgesLM/cistern/internal/singleflight"
)

const long = time.Minute

// waitFor polls until cond holds, failing the test after a generous bound.
// Polling a condition is deterministic; sleeping for a guessed duration is not.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestDoReturnsTheResult(t *testing.T) {
	var g singleflight.Group[string]
	v, err := g.Do(context.Background(), "k", long, func(context.Context) (string, error) { return "v", nil })
	if err != nil || v != "v" {
		t.Fatalf("Do = %q, %v; want %q, nil", v, err, "v")
	}
}

// RF-05: N concurrent callers of a key trigger exactly one call.
// Negative control: verified failing with the join branch removed from Do.
func TestConcurrentCallersShareOneCall(t *testing.T) {
	const n = 50
	var g singleflight.Group[int]
	var calls atomic.Int32
	release := make(chan struct{})
	fn := func(context.Context) (int, error) {
		calls.Add(1)
		<-release
		return 7, nil
	}

	results := make(chan int, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := g.Do(context.Background(), "k", long, fn)
			if err != nil {
				t.Errorf("Do: %v", err)
			}
			results <- v
		}()
	}
	waitFor(t, "all callers to join", func() bool { return g.Waiting("k") == n })
	close(release)
	wg.Wait()
	close(results)

	if got := calls.Load(); got != 1 {
		t.Fatalf("fn called %d times for %d concurrent callers, want 1", got, n)
	}
	for v := range results {
		if v != 7 {
			t.Fatalf("a caller got %d, want 7", v)
		}
	}
}

func TestDifferentKeysDoNotShare(t *testing.T) {
	var g singleflight.Group[string]
	for _, k := range []string{"a", "b"} {
		v, err := g.Do(context.Background(), k, long, func(context.Context) (string, error) { return k, nil })
		if err != nil || v != k {
			t.Fatalf("Do(%q) = %q, %v", k, v, err)
		}
	}
}

// A flight is not a cache: once it completes, the next call runs fn again,
// whether the flight succeeded or failed.
func TestCompletedFlightIsNotRemembered(t *testing.T) {
	var g singleflight.Group[int]
	var calls atomic.Int32
	errLoad := errors.New("source down")
	fn := func(context.Context) (int, error) {
		if calls.Add(1) == 1 {
			return 0, errLoad
		}
		return 2, nil
	}

	if _, err := g.Do(context.Background(), "k", long, fn); !errors.Is(err, errLoad) {
		t.Fatalf("first Do: err = %v, want the load error", err)
	}
	if v, err := g.Do(context.Background(), "k", long, fn); err != nil || v != 2 {
		t.Fatalf("second Do = %d, %v; want 2, nil", v, err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("fn called %d times, want 2", got)
	}
}

// RF-02 / ADR-0007: a caller leaving does not cancel the load for the others.
// Negative control: verified failing with fn run on the leader's context.
func TestLeaderCancellationDoesNotCancelTheLoad(t *testing.T) {
	var g singleflight.Group[string]
	release := make(chan struct{})
	loadCtxErr := make(chan error, 1)
	fn := func(ctx context.Context) (string, error) {
		<-release
		loadCtxErr <- ctx.Err()
		return "v", nil
	}

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderDone := make(chan error, 1)
	go func() {
		_, err := g.Do(leaderCtx, "k", long, fn)
		leaderDone <- err
	}()
	waitFor(t, "the leader to start the flight", func() bool { return g.Waiting("k") == 1 })

	followerDone := make(chan string, 1)
	go func() {
		v, err := g.Do(context.Background(), "k", long, fn)
		if err != nil {
			t.Errorf("follower Do: %v", err)
		}
		followerDone <- v
	}()
	waitFor(t, "the follower to join", func() bool { return g.Waiting("k") == 2 })

	cancelLeader()
	if err := <-leaderDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("leader Do: err = %v, want context.Canceled", err)
	}

	close(release)
	if err := <-loadCtxErr; err != nil {
		t.Fatalf("the load's context was %v after the leader left, want still live", err)
	}
	if v := <-followerDone; v != "v" {
		t.Fatalf("follower got %q, want %q", v, "v")
	}
}

// ADR-0007: a loader that never returns cannot take the key down. Its waiters
// get a timeout, and the next caller starts a fresh flight instead of joining
// the expired one.
// Negative control: verified failing with the expired-flight check removed.
func TestHungLoadDoesNotBlockTheNextCaller(t *testing.T) {
	var g singleflight.Group[string]
	hang := make(chan struct{})
	defer close(hang)

	_, err := g.Do(context.Background(), "k", 20*time.Millisecond, func(context.Context) (string, error) {
		<-hang // ignores its context on purpose
		return "never", nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Do on a hung load: err = %v, want context.DeadlineExceeded", err)
	}

	v, err := g.Do(context.Background(), "k", long, func(context.Context) (string, error) { return "fresh", nil })
	if err != nil || v != "fresh" {
		t.Fatalf("Do after the timeout = %q, %v; want a fresh flight's result", v, err)
	}
}

// ADR-0007: a panicking load never deadlocks a waiter; every waiting caller
// panics with the original value.
func TestPanicReachesEveryWaitingCaller(t *testing.T) {
	const n = 5
	var g singleflight.Group[int]
	release := make(chan struct{})
	fn := func(context.Context) (int, error) {
		<-release
		panic("loader bug")
	}

	recovered := make(chan any, n)
	for range n {
		go func() {
			defer func() { recovered <- recover() }()
			_, _ = g.Do(context.Background(), "k", long, fn)
		}()
	}
	waitFor(t, "all callers to join", func() bool { return g.Waiting("k") == n })
	close(release)

	for range n {
		select {
		case r := <-recovered:
			var pe *singleflight.PanicError
			err, isErr := r.(error)
			if !isErr || !errors.As(err, &pe) || pe.Value != "loader bug" || len(pe.Stack) == 0 {
				t.Fatalf("recovered %#v, want a *PanicError carrying the value and a stack", r)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a waiting caller deadlocked on a panicking load")
		}
	}
}
