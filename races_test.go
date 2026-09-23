package cistern_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JonasBorgesLM/cistern"
	"github.com/JonasBorgesLM/cistern/memory"
)

// blockedLoad starts a GetOrLoad whose loader returns value only once release
// is closed, and waits until that loader is running.
func blockedLoad[K comparable](t *testing.T, c *cistern.Cache[K, string], k K, value string) (release chan struct{}, done chan string) {
	t.Helper()
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan string, 1)
	go func() {
		v, err := c.GetOrLoad(context.Background(), k, func(context.Context) (string, error) {
			close(started)
			<-release
			return value, nil
		})
		if err != nil {
			t.Errorf("blocked GetOrLoad: %v", err)
		}
		done <- v
	}()
	<-started
	return release, done
}

// answer waits for a result that must not depend on another caller's load;
// if it has not come after 2s, it releases that load and fails.
func answer(t *testing.T, got chan string, release chan struct{}) string {
	t.Helper()
	select {
	case v := <-got:
		return v
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatalf("the call waited on another caller's load; it got %q once that load finished", <-got)
		return ""
	}
}

func getOrLoadAsync[K comparable](c *cistern.Cache[K, string], k K, value string) chan string {
	out := make(chan string, 1)
	go func() {
		v, _ := c.GetOrLoad(context.Background(), k, func(context.Context) (string, error) { return value, nil })
		out <- v
	}()
	return out
}

// #112, T-01: with the owner in the tag but not in the key, a concurrent call
// for another owner must not be served the first owner's in-flight load.
// Negative control: verified failing with flights keyed by the physical key
// alone.
func TestConcurrentCallsWithDifferentTagsDoNotShareALoad(t *testing.T) {
	l1, err := memory.New()
	if err != nil {
		t.Fatal(err)
	}
	c, err := cistern.New[int, string]("tasks", func(int) string { return "lists" },
		cistern.WithL1(l1), cistern.WithTTL(time.Minute),
		cistern.WithTags(func(owner int) []string { return []string{fmt.Sprintf("user:%d:lists", owner)} }))
	if err != nil {
		t.Fatal(err)
	}
	release, done42 := blockedLoad(t, c, 42, "private to 42")
	if v := answer(t, getOrLoadAsync(c, 43, "private to 43"), release); v != "private to 43" {
		t.Fatalf("user 43 received %q", v)
	}
	close(release)
	<-done42
}

// #112, T-13: a read issued after InvalidateTag has returned must not be served
// by a load that started before it.
// Negative control: verified failing with flights keyed by the physical key
// alone.
func TestReadAfterInvalidateTagDoesNotJoinAnOlderLoad(t *testing.T) {
	c := taggedCache(t, newL1(t))
	release, doneOld := blockedLoad(t, c, "user:42:list:1", "old")
	if err := c.InvalidateTag(context.Background(), "user:42:lists"); err != nil {
		t.Fatal(err)
	}
	if v := answer(t, getOrLoadAsync(c, "user:42:list:1", "new"), release); v != "new" {
		t.Fatalf("read after InvalidateTag returned %q, want %q", v, "new")
	}
	close(release)
	<-doneOld
}

// failingGens is an L1 whose generations can never be read, as when the
// authoritative level times out.
type failingGens struct{ *memory.Store }

func (failingGens) GetTagged(context.Context, string, []string, time.Duration) (value []byte, ok bool, gens []uint64, err error) {
	return nil, false, nil, errStore
}

// #134, T-01: callers whose generations could not be read share a load only
// with callers of the same tags. Keyed by a fixed marker instead, another
// owner joined the first owner's load and was returned its value.
// Negative control: verified failing with every unknown-generations caller
// keyed by the same marker.
func TestCallersWithUnreadableGenerationsDoNotShareAcrossTags(t *testing.T) {
	l1, err := memory.New()
	if err != nil {
		t.Fatal(err)
	}
	c, err := cistern.New[int, string]("tasks", func(int) string { return "lists" },
		cistern.WithL1(failingGens{l1}), cistern.WithTTL(time.Minute),
		cistern.WithTags(func(owner int) []string { return []string{fmt.Sprintf("user:%d:lists", owner)} }))
	if err != nil {
		t.Fatal(err)
	}
	release, done42 := blockedLoad(t, c, 42, "private to 42")
	if v := answer(t, getOrLoadAsync(c, 43, "private to 43"), release); v != "private to 43" {
		t.Fatalf("user 43 received %q", v)
	}
	close(release)
	<-done42
}

// #134, T-10: during an L2 outage the source carries the load, so callers of
// the same tags whose generations could not be read still share one load.
// Negative control: verified failing with a flight key unique to each caller.
func TestCallersWithUnreadableGenerationsStillShareWithinTheirTags(t *testing.T) {
	l1, err := memory.New()
	if err != nil {
		t.Fatal(err)
	}
	var loads, coalesced atomic.Int32
	c, err := cistern.New[int, string]("tasks", func(int) string { return "lists" },
		cistern.WithL1(failingGens{l1}), cistern.WithTTL(time.Minute),
		cistern.WithTags(func(owner int) []string { return []string{fmt.Sprintf("user:%d:lists", owner)} }),
		cistern.WithHooks(cistern.Hooks{
			OnLoad:      func(context.Context, cistern.LoadEvent) { loads.Add(1) },
			OnCoalesced: func(context.Context, cistern.CoalescedEvent) { coalesced.Add(1) },
		}))
	if err != nil {
		t.Fatal(err)
	}
	release, done := blockedLoad(t, c, 42, "v")
	joined := getOrLoadAsync(c, 42, "v")
	time.Sleep(50 * time.Millisecond) // for the second caller to reach the flight; if it has not, coalesced is 0
	close(release)
	<-done
	<-joined
	if loads.Load() != 1 || coalesced.Load() != 1 {
		t.Fatalf("loads = %d, coalesced = %d; want 1 and 1", loads.Load(), coalesced.Load())
	}
}
