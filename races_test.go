package cistern_test

import (
	"context"
	"fmt"
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
