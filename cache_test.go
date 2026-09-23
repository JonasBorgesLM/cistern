package cistern_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JonasBorgesLM/cistern"
	"github.com/JonasBorgesLM/cistern/memory"
)

// recorder is a Store that remembers what it was asked and can be told to fail.
type recorder struct {
	mu      sync.Mutex
	data    map[string][]byte
	ttls    map[string]time.Duration
	gets    int
	failGet error
	failSet error
	failDel error
}

func newRecorder() *recorder {
	return &recorder{data: map[string][]byte{}, ttls: map[string]time.Duration{}}
}

func (r *recorder) Get(_ context.Context, key string) (value []byte, ok bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gets++
	if r.failGet != nil {
		return nil, false, r.failGet
	}
	v, found := r.data[key]
	return append([]byte(nil), v...), found, nil
}

func (r *recorder) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failSet != nil {
		return r.failSet
	}
	r.data[key] = append([]byte(nil), value...)
	r.ttls[key] = ttl
	return nil
}

func (r *recorder) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failDel != nil {
		return r.failDel
	}
	delete(r.data, key)
	return nil
}

func (r *recorder) keys() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.data))
	for k := range r.data {
		out = append(out, k)
	}
	return out
}

var errStore = errors.New("store unavailable")

func stringKey(k string) string { return k }

func newCache(t *testing.T, opts ...cistern.Option) *cistern.Cache[string, string] {
	t.Helper()
	c, err := cistern.New[string, string]("tasks", stringKey, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func newL1(t *testing.T) *memory.Store {
	t.Helper()
	s, err := memory.New()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestNewValidatesConfiguration(t *testing.T) {
	l1, l2 := newRecorder(), newRecorder()
	valid := []cistern.Option{cistern.WithL1(l1), cistern.WithL2(l2), cistern.WithTTL(time.Minute)}

	cases := []struct {
		name      string
		namespace string
		key       func(string) string
		opts      []cistern.Option
	}{
		// RS-01: there is no scopeless cache.
		// Negative control: verified failing with the namespace check removed.
		{"empty namespace", "", stringKey, valid},
		{"namespace with separator", "tasks:v2", stringKey, valid},
		{"namespace with uppercase", "Tasks", stringKey, valid},
		{"namespace too long", strings.Repeat("a", 65), stringKey, valid},
		{"nil key function", "tasks", nil, valid},
		{"no level", "tasks", stringKey, []cistern.Option{cistern.WithTTL(time.Minute)}},
		{"nil L1", "tasks", stringKey, []cistern.Option{cistern.WithL1(nil), cistern.WithTTL(time.Minute)}},
		{"nil L2", "tasks", stringKey, []cistern.Option{cistern.WithL2(nil), cistern.WithTTL(time.Minute)}},
		{"no TTL", "tasks", stringKey, []cistern.Option{cistern.WithL1(l1)}},
		{"negative TTL", "tasks", stringKey, []cistern.Option{cistern.WithL1(l1), cistern.WithTTL(-time.Second)}},
		// RF-08: L1 TTL must not exceed L2 TTL.
		// Negative control: verified failing with the ordering check removed.
		{"L1 TTL above TTL", "tasks", stringKey, append(valid, cistern.WithL1TTL(2*time.Minute))},
		{"zero L1 TTL", "tasks", stringKey, append(valid, cistern.WithL1TTL(0))},
		{"L1 TTL without L1", "tasks", stringKey, []cistern.Option{cistern.WithL2(l2), cistern.WithTTL(time.Minute), cistern.WithL1TTL(time.Second)}},
		{"negative jitter", "tasks", stringKey, append(valid, cistern.WithJitter(-0.1))},
		{"jitter of one", "tasks", stringKey, append(valid, cistern.WithJitter(1))},
		{"NaN jitter", "tasks", stringKey, append(valid, cistern.WithJitter(math.NaN()))},
		{"nil codec", "tasks", stringKey, append(valid, cistern.WithCodec(nil))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := cistern.New[string, string](tc.namespace, tc.key, tc.opts...)
			if !errors.Is(err, cistern.ErrInvalidConfig) {
				t.Fatalf("New: err = %v, want ErrInvalidConfig", err)
			}
			if c != nil {
				t.Fatal("New returned a cache alongside an error")
			}
		})
	}

	for _, ns := range []string{"tasks", "a", "sessions-meta", "v1.lists_by_owner", strings.Repeat("a", 64)} {
		if _, err := cistern.New[string, string](ns, stringKey, valid...); err != nil {
			t.Errorf("New(%q): %v, want nil", ns, err)
		}
	}
}

func TestSetThenGet(t *testing.T) {
	c := newCache(t, cistern.WithL1(newL1(t)), cistern.WithTTL(time.Minute))
	ctx := context.Background()

	if err := c.Set(ctx, "user:42:list", "tasks of 42"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	v, ok, err := c.Get(ctx, "user:42:list")
	if err != nil || !ok || v != "tasks of 42" {
		t.Fatalf("Get = %q, %v, %v; want %q, true, nil", v, ok, err, "tasks of 42")
	}

	v, ok, err = c.Get(ctx, "user:43:list")
	if err != nil || ok || v != "" {
		t.Fatalf("Get of a missing key = %q, %v, %v; want zero, false, nil", v, ok, err)
	}
}

// ADR-0008: cistern:v1:<namespace>:<generations>:<kind>:<key>.
func TestPhysicalKeyLayout(t *testing.T) {
	l2 := newRecorder()
	c := newCache(t, cistern.WithL2(l2), cistern.WithTTL(time.Minute))
	if err := c.Set(context.Background(), "user:42:list", "x"); err != nil {
		t.Fatal(err)
	}
	keys := l2.keys()
	want := "cistern:v1:tasks:-:k:user:42:list"
	if len(keys) != 1 || keys[0] != want {
		t.Fatalf("stored keys = %q, want [%q]", keys, want)
	}
}

// RS-01: the namespace isolates caches that share a store.
func TestNamespacesDoNotShareEntries(t *testing.T) {
	shared := newL1(t)
	ctx := context.Background()
	tasks := newCache(t, cistern.WithL1(shared), cistern.WithTTL(time.Minute))
	notes, err := cistern.New[string, string]("notes", stringKey, cistern.WithL1(shared), cistern.WithTTL(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	if err := tasks.Set(ctx, "user:42", "a task"); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := notes.Get(ctx, "user:42"); err != nil || ok {
		t.Fatalf("notes.Get = %q, %v, %v; want a miss", v, ok, err)
	}
}

func TestTTLsReachTheStores(t *testing.T) {
	ctx := context.Background()

	t.Run("default TTL, L1 TTL defaults to it", func(t *testing.T) {
		l1, l2 := newRecorder(), newRecorder()
		c := newCache(t, cistern.WithL1(l1), cistern.WithL2(l2), cistern.WithTTL(time.Minute))
		if err := c.Set(ctx, "k", "v"); err != nil {
			t.Fatal(err)
		}
		wantTTL(t, l1, time.Minute)
		wantTTL(t, l2, time.Minute)
	})

	t.Run("shorter L1 TTL", func(t *testing.T) {
		l1, l2 := newRecorder(), newRecorder()
		c := newCache(t, cistern.WithL1(l1), cistern.WithL2(l2), cistern.WithTTL(time.Minute), cistern.WithL1TTL(5*time.Second))
		if err := c.Set(ctx, "k", "v"); err != nil {
			t.Fatal(err)
		}
		wantTTL(t, l1, 5*time.Second)
		wantTTL(t, l2, time.Minute)
	})

	// ADR-0004: a per-key override sets the L2 TTL; L1 gets the smaller of the
	// override and the L1 TTL.
	t.Run("per-key override", func(t *testing.T) {
		l1, l2 := newRecorder(), newRecorder()
		c := newCache(t, cistern.WithL1(l1), cistern.WithL2(l2), cistern.WithTTL(time.Minute), cistern.WithL1TTL(5*time.Second))
		if err := c.Set(ctx, "long", "v", cistern.TTL(time.Hour)); err != nil {
			t.Fatal(err)
		}
		wantTTL(t, l1, 5*time.Second)
		wantTTL(t, l2, time.Hour)

		l1, l2 = newRecorder(), newRecorder()
		c = newCache(t, cistern.WithL1(l1), cistern.WithL2(l2), cistern.WithTTL(time.Minute), cistern.WithL1TTL(5*time.Second))
		if err := c.Set(ctx, "short", "v", cistern.TTL(2*time.Second)); err != nil {
			t.Fatal(err)
		}
		wantTTL(t, l1, 2*time.Second)
		wantTTL(t, l2, 2*time.Second)
	})

	t.Run("non-positive per-key TTL is rejected", func(t *testing.T) {
		l2 := newRecorder()
		c := newCache(t, cistern.WithL2(l2), cistern.WithTTL(time.Minute))
		if err := c.Set(ctx, "k", "v", cistern.TTL(0)); !errors.Is(err, cistern.ErrInvalidConfig) {
			t.Fatalf("Set with TTL(0): err = %v, want ErrInvalidConfig", err)
		}
		if len(l2.keys()) != 0 {
			t.Fatal("a rejected Set still wrote to the store")
		}
	})
}

func TestGetReadsL1BeforeL2(t *testing.T) {
	ctx := context.Background()
	l1, l2 := newRecorder(), newRecorder()
	c := newCache(t, cistern.WithL1(l1), cistern.WithL2(l2), cistern.WithTTL(time.Minute))
	if err := c.Set(ctx, "k", "v"); err != nil {
		t.Fatal(err)
	}

	if v, ok, err := c.Get(ctx, "k"); err != nil || !ok || v != "v" {
		t.Fatalf("Get = %q, %v, %v", v, ok, err)
	}
	if l2.gets != 0 {
		t.Fatalf("an L1 hit consulted L2 %d times", l2.gets)
	}

	if err := l1.Delete(ctx, "cistern:v1:tasks:-:k:k"); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := c.Get(ctx, "k"); err != nil || !ok || v != "v" {
		t.Fatalf("Get after an L1 miss = %q, %v, %v; want the L2 value", v, ok, err)
	}
}

// ADR-0002: a read never fails because of the cache.
func TestGetFailsOpenOnStoreErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("failing L2 is a miss", func(t *testing.T) {
		l2 := newRecorder()
		l2.failGet = errStore
		c := newCache(t, cistern.WithL2(l2), cistern.WithTTL(time.Minute))
		if v, ok, err := c.Get(ctx, "k"); err != nil || ok {
			t.Fatalf("Get = %q, %v, %v; want zero, false, nil", v, ok, err)
		}
	})

	t.Run("failing L1 falls through to L2", func(t *testing.T) {
		l1, l2 := newRecorder(), newRecorder()
		c := newCache(t, cistern.WithL1(l1), cistern.WithL2(l2), cistern.WithTTL(time.Minute))
		if err := c.Set(ctx, "k", "v"); err != nil {
			t.Fatal(err)
		}
		l1.failGet = errStore
		if v, ok, err := c.Get(ctx, "k"); err != nil || !ok || v != "v" {
			t.Fatalf("Get = %q, %v, %v; want the L2 value", v, ok, err)
		}
	})
}

// RS-06 (the full envelope checks arrive in C3): bytes that do not decode are
// a miss, never an error or a panic.
func TestUndecodableEntryIsAMiss(t *testing.T) {
	ctx := context.Background()
	l2 := newRecorder()
	c := newCache(t, cistern.WithL2(l2), cistern.WithTTL(time.Minute))
	if err := l2.Set(ctx, "cistern:v1:tasks:-:k:k", []byte("{not json"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := c.Get(ctx, "k"); err != nil || ok {
		t.Fatalf("Get = %q, %v, %v; want zero, false, nil", v, ok, err)
	}
}

// ADR-0002: an explicit Set is told when its value was not stored.
func TestSetFailsLoud(t *testing.T) {
	l2 := newRecorder()
	l2.failSet = errStore
	c := newCache(t, cistern.WithL2(l2), cistern.WithTTL(time.Minute))
	if err := c.Set(context.Background(), "k", "v"); !errors.Is(err, errStore) {
		t.Fatalf("Set: err = %v, want the store error", err)
	}
}

func TestSetReturnsEncodingErrors(t *testing.T) {
	c, err := cistern.New[string, any]("tasks", stringKey, cistern.WithL1(newRecorder()), cistern.WithTTL(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Set(context.Background(), "k", make(chan int)); err == nil {
		t.Fatal("Set of an unencodable value = nil error")
	}
}

// ADR-0002: invalidation always evicts L1, and reports an L2 failure.
func TestDeleteEvictsBothLevelsAndFailsLoud(t *testing.T) {
	ctx := context.Background()
	l1, l2 := newRecorder(), newRecorder()
	c := newCache(t, cistern.WithL1(l1), cistern.WithL2(l2), cistern.WithTTL(time.Minute))
	if err := c.Set(ctx, "k", "v"); err != nil {
		t.Fatal(err)
	}

	l2.failDel = errStore
	if err := c.Delete(ctx, "k"); !errors.Is(err, errStore) {
		t.Fatalf("Delete: err = %v, want the L2 error", err)
	}
	if len(l1.keys()) != 0 {
		t.Fatal("Delete left the entry in L1 because L2 failed")
	}

	l2.failDel = nil
	if err := c.Delete(ctx, "k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(l2.keys()) != 0 {
		t.Fatal("Delete left the entry in L2")
	}
}

func TestCancelledContextIsReturned(t *testing.T) {
	c := newCache(t, cistern.WithL1(newL1(t)), cistern.WithTTL(time.Minute))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := c.Get(ctx, "k"); !errors.Is(err, context.Canceled) {
		t.Errorf("Get: err = %v, want context.Canceled", err)
	}
	if err := c.Set(ctx, "k", "v"); !errors.Is(err, context.Canceled) {
		t.Errorf("Set: err = %v, want context.Canceled", err)
	}
	if err := c.Delete(ctx, "k"); !errors.Is(err, context.Canceled) {
		t.Errorf("Delete: err = %v, want context.Canceled", err)
	}
}

// ADR-0014: a caller that mutates what it got cannot change what the next
// caller gets.
func TestCallerMutationDoesNotReachTheCache(t *testing.T) {
	ctx := context.Background()
	c, err := cistern.New[string, []int]("tasks", stringKey, cistern.WithL1(newL1(t)), cistern.WithTTL(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Set(ctx, "k", []int{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	first, _, _ := c.Get(ctx, "k")
	first[0] = 99

	second, ok, err := c.Get(ctx, "k")
	if err != nil || !ok || second[0] != 1 {
		t.Fatalf("second Get = %v, %v, %v; want [1 2 3]", second, ok, err)
	}
}

func wantTTL(t *testing.T, r *recorder, want time.Duration) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, got := range r.ttls {
		if got != want {
			t.Fatalf("stored TTL = %v, want %v", got, want)
		}
		return
	}
	t.Fatal("nothing was stored")
}
