package cistern_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JonasBorgesLM/cistern"
)

// countingLoader returns v and counts its calls.
func countingLoader[V any](v V, calls *atomic.Int32) cistern.Loader[V] {
	return func(context.Context) (V, error) {
		calls.Add(1)
		return v, nil
	}
}

func TestGetOrLoadLoadsOnMissAndServesTheHit(t *testing.T) {
	ctx := context.Background()
	c := newCache(t, cistern.WithL1(newL1(t)), cistern.WithTTL(time.Minute))
	var calls atomic.Int32
	load := countingLoader("from source", &calls)

	for range 3 {
		v, err := c.GetOrLoad(ctx, "k", load)
		if err != nil || v != "from source" {
			t.Fatalf("GetOrLoad = %q, %v", v, err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("loader called %d times, want 1", got)
	}
	if v, ok, err := c.Get(ctx, "k"); err != nil || !ok || v != "from source" {
		t.Fatalf("Get after GetOrLoad = %q, %v, %v; want the loaded value", v, ok, err)
	}
}

func TestGetOrLoadDoesNotCacheLoaderErrors(t *testing.T) {
	ctx := context.Background()
	c := newCache(t, cistern.WithL1(newL1(t)), cistern.WithTTL(time.Minute))
	errSource := errors.New("source down")
	var calls atomic.Int32
	load := func(context.Context) (string, error) {
		calls.Add(1)
		return "", errSource
	}

	for range 2 {
		if _, err := c.GetOrLoad(ctx, "k", load); !errors.Is(err, errSource) {
			t.Fatalf("GetOrLoad: err = %v, want the loader's error", err)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("loader called %d times, want 2: an error must not be cached", got)
	}
}

func TestErrNotFoundIsNotCachedWithoutNegativeCaching(t *testing.T) {
	ctx := context.Background()
	c := newCache(t, cistern.WithL1(newL1(t)), cistern.WithTTL(time.Minute))
	var calls atomic.Int32
	load := func(context.Context) (string, error) {
		calls.Add(1)
		return "", cistern.ErrNotFound
	}

	for range 2 {
		if _, err := c.GetOrLoad(ctx, "k", load); !errors.Is(err, cistern.ErrNotFound) {
			t.Fatalf("GetOrLoad: err = %v, want ErrNotFound", err)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("loader called %d times, want 2", got)
	}
}

// RF-06, T-11: a remembered absence protects the source from repeated misses.
// Negative control: verified failing with the absence write removed.
func TestNegativeCachingRemembersAbsence(t *testing.T) {
	ctx := context.Background()
	c := newCache(t, cistern.WithL1(newL1(t)), cistern.WithTTL(time.Minute), cistern.WithNegativeTTL(5*time.Second))
	var calls atomic.Int32
	errMissing := fmt.Errorf("task 7: %w", cistern.ErrNotFound)
	load := func(context.Context) (string, error) {
		calls.Add(1)
		return "", errMissing
	}

	if _, err := c.GetOrLoad(ctx, "task:7", load); !errors.Is(err, errMissing) {
		t.Fatalf("first GetOrLoad: err = %v, want the loader's error", err)
	}
	if _, err := c.GetOrLoad(ctx, "task:7", load); !errors.Is(err, cistern.ErrNotFound) {
		t.Fatalf("second GetOrLoad: err = %v, want ErrNotFound", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("loader called %d times, want 1", got)
	}
	if v, ok, err := c.Get(ctx, "task:7"); !errors.Is(err, cistern.ErrNotFound) || ok {
		t.Fatalf("Get of a remembered absence = %q, %v, %v; want ErrNotFound", v, ok, err)
	}
}

// ADR-0004 applied to absences: L2 keeps the negative TTL, L1 the smaller of
// it and the L1 TTL.
func TestNegativeTTLReachesTheStores(t *testing.T) {
	l1, l2 := newRecorder(), newRecorder()
	c := newCache(t, cistern.WithL1(l1), cistern.WithL2(l2), cistern.WithTTL(time.Minute),
		cistern.WithL1TTL(2*time.Second), cistern.WithNegativeTTL(5*time.Second))
	_, _ = c.GetOrLoad(context.Background(), "k", func(context.Context) (string, error) { return "", cistern.ErrNotFound })
	wantTTL(t, l1, 2*time.Second)
	wantTTL(t, l2, 5*time.Second)
}

func TestGetOrLoadAppliesTheEntryTTL(t *testing.T) {
	l2 := newRecorder()
	c := newCache(t, cistern.WithL2(l2), cistern.WithTTL(time.Minute))
	var calls atomic.Int32
	if _, err := c.GetOrLoad(context.Background(), "k", countingLoader("v", &calls), cistern.TTL(time.Hour)); err != nil {
		t.Fatal(err)
	}
	wantTTL(t, l2, time.Hour)
}

// ADR-0002: a read never fails because of the cache.
func TestGetOrLoadFailsOpen(t *testing.T) {
	ctx := context.Background()

	t.Run("failing read falls through to the loader", func(t *testing.T) {
		l2 := newRecorder()
		l2.failGet = errStore
		c := newCache(t, cistern.WithL2(l2), cistern.WithTTL(time.Minute))
		var calls atomic.Int32
		if v, err := c.GetOrLoad(ctx, "k", countingLoader("v", &calls)); err != nil || v != "v" {
			t.Fatalf("GetOrLoad = %q, %v; want the loaded value", v, err)
		}
	})

	t.Run("failing population still returns the value", func(t *testing.T) {
		l2 := newRecorder()
		l2.failSet = errStore
		c := newCache(t, cistern.WithL2(l2), cistern.WithTTL(time.Minute))
		var calls atomic.Int32
		if v, err := c.GetOrLoad(ctx, "k", countingLoader("v", &calls)); err != nil || v != "v" {
			t.Fatalf("GetOrLoad = %q, %v; want the loaded value", v, err)
		}
	})
}

// ADR-0007: the load keeps the caller's context values but not its
// cancellation, so a caller leaving does not stop the cache from being filled.
func TestLoadOutlivesTheCallerThatStartedIt(t *testing.T) {
	type traceKey struct{}
	c := newCache(t, cistern.WithL1(newL1(t)), cistern.WithTTL(time.Minute))
	started, release := make(chan struct{}), make(chan struct{})
	seen := make(chan any, 1)
	load := func(ctx context.Context) (string, error) {
		close(started)
		<-release
		seen <- ctx.Value(traceKey{})
		return "v", ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), traceKey{}, "trace-1"))
	done := make(chan error, 1)
	go func() {
		_, err := c.GetOrLoad(ctx, "k", load)
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("GetOrLoad after its caller left: err = %v, want context.Canceled", err)
	}

	close(release)
	if got := <-seen; got != "trace-1" {
		t.Fatalf("loader saw trace value %v, want %q", got, "trace-1")
	}
	waitForHit(t, c, "k", "v")
}

func TestGetOrLoadTimesOutAHungLoader(t *testing.T) {
	c := newCache(t, cistern.WithL1(newL1(t)), cistern.WithTTL(time.Minute), cistern.WithLoadTimeout(20*time.Millisecond))
	hang := make(chan struct{})
	defer close(hang)
	_, err := c.GetOrLoad(context.Background(), "k", func(context.Context) (string, error) {
		<-hang
		return "never", nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("GetOrLoad on a hung loader: err = %v, want context.DeadlineExceeded", err)
	}
}

func TestGetOrLoadRejectsBadCalls(t *testing.T) {
	ctx := context.Background()
	c := newCache(t, cistern.WithL1(newL1(t)), cistern.WithTTL(time.Minute))
	var calls atomic.Int32

	if _, err := c.GetOrLoad(ctx, "k", nil); !errors.Is(err, cistern.ErrInvalidConfig) {
		t.Errorf("nil loader: err = %v, want ErrInvalidConfig", err)
	}
	if _, err := c.GetOrLoad(ctx, "k", countingLoader("v", &calls), cistern.TTL(0)); !errors.Is(err, cistern.ErrInvalidConfig) {
		t.Errorf("TTL(0): err = %v, want ErrInvalidConfig", err)
	}
	if calls.Load() != 0 {
		t.Error("a rejected call still ran the loader")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.GetOrLoad(cancelled, "k", countingLoader("v", &calls)); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled ctx: err = %v, want context.Canceled", err)
	}
}

func TestNewValidatesLoadingOptions(t *testing.T) {
	base := []cistern.Option{cistern.WithL1(newRecorder()), cistern.WithTTL(time.Minute)}
	for name, opt := range map[string]cistern.Option{
		"zero negative TTL":      cistern.WithNegativeTTL(0),
		"negative TTL above TTL": cistern.WithNegativeTTL(2 * time.Minute),
		"zero load timeout":      cistern.WithLoadTimeout(0),
		"negative load timeout":  cistern.WithLoadTimeout(-time.Second),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := cistern.New[string, string]("tasks", stringKey, append(base, opt)...); !errors.Is(err, cistern.ErrInvalidConfig) {
				t.Fatalf("New: err = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func waitForHit(t *testing.T, c *cistern.Cache[string, string], key, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if v, ok, err := c.Get(context.Background(), key); err == nil && ok && v == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the cache was never filled with %q for %q", want, key)
		}
		time.Sleep(time.Millisecond)
	}
}
