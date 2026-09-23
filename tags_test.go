package cistern_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JonasBorgesLM/cistern"
	"github.com/JonasBorgesLM/cistern/memory"
)

// ownerTag derives a key's tag from its owner: "user:42:list:1" is tagged
// "user:42:lists", the tag task-api invalidates on every write.
func ownerTag(k string) []string {
	parts := strings.SplitN(k, ":", 3)
	return []string{parts[0] + ":" + parts[1] + ":lists"}
}

func taggedCache(t *testing.T, l1 cistern.Store, opts ...cistern.Option) *cistern.Cache[string, string] {
	t.Helper()
	base := []cistern.Option{cistern.WithTTL(time.Minute), cistern.WithTags(ownerTag)}
	if l1 != nil {
		base = append(base, cistern.WithL1(l1))
	}
	c, err := cistern.New[string, string]("tasks", stringKey, append(base, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func mustHit(t *testing.T, c *cistern.Cache[string, string], key, want string) {
	t.Helper()
	if v, ok, err := c.Get(context.Background(), key); err != nil || !ok || v != want {
		t.Fatalf("Get(%q) = %q, %v, %v; want %q", key, v, ok, err, want)
	}
}

func mustMiss(t *testing.T, c *cistern.Cache[string, string], key string) {
	t.Helper()
	if v, ok, err := c.Get(context.Background(), key); err != nil || ok {
		t.Fatalf("Get(%q) = %q, %v, %v; want a miss", key, v, ok, err)
	}
}

func mustSetTagged(t *testing.T, c *cistern.Cache[string, string], key, value string) {
	t.Helper()
	if err := c.Set(context.Background(), key, value); err != nil {
		t.Fatalf("Set(%q): %v", key, err)
	}
}

// RF-10: invalidating a tag retires every entry carrying it, and only those.
// Negative control: verified failing with generations not compared on read.
func TestInvalidateTagRetiresItsEntries(t *testing.T) {
	c := taggedCache(t, newL1(t))
	mustSetTagged(t, c, "user:42:list:1", "a")
	mustSetTagged(t, c, "user:42:list:2", "b")
	mustSetTagged(t, c, "user:43:list:1", "c")

	if err := c.InvalidateTag(context.Background(), "user:42:lists"); err != nil {
		t.Fatalf("InvalidateTag: %v", err)
	}
	mustMiss(t, c, "user:42:list:1")
	mustMiss(t, c, "user:42:list:2")
	mustHit(t, c, "user:43:list:1", "c")
}

// ADR-0005 with ADR-0011: an entry records the generations read before its
// load, so an invalidation during the load leaves it stale on arrival.
// Negative control: verified failing with generations read after the load.
func TestEntryLoadedDuringAnInvalidationIsBornStale(t *testing.T) {
	ctx := context.Background()
	c := taggedCache(t, newL1(t))
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := c.GetOrLoad(ctx, "user:42:list:1", func(context.Context) (string, error) {
			close(started)
			<-release
			return "old value", nil
		})
		done <- err
	}()
	<-started
	if err := c.InvalidateTag(ctx, "user:42:lists"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mustMiss(t, c, "user:42:list:1")
}

// T-14 end to end: a counter that vanishes (evicted, expired) comes back at a
// value no entry recorded, so nothing it retired is ever served again.
// Negative control: verified failing with memory creating counters at 1.
func TestVanishedCounterNeverResurrectsARetiredEntry(t *testing.T) {
	ctx := context.Background()
	l1 := newL1(t)
	c := taggedCache(t, l1)
	mustSetTagged(t, c, "user:42:list:1", "retired")
	if err := c.InvalidateTag(ctx, "user:42:lists"); err != nil {
		t.Fatal(err)
	}
	mustSetTagged(t, c, "user:42:list:2", "current")

	if err := l1.Delete(ctx, "cistern:v1:tasks:g:user:42:lists"); err != nil {
		t.Fatal(err) // stands in for eviction
	}
	mustMiss(t, c, "user:42:list:1")
	mustMiss(t, c, "user:42:list:2")
}

// RF-06 with RF-10: a remembered absence is retired by its tag too — the item
// may have just been created.
func TestTaggedAbsenceIsInvalidated(t *testing.T) {
	ctx := context.Background()
	c := taggedCache(t, newL1(t), cistern.WithNegativeTTL(30*time.Second))
	var calls atomic.Int32
	load := func(context.Context) (string, error) {
		if calls.Add(1) == 1 {
			return "", cistern.ErrNotFound
		}
		return "created", nil
	}
	if _, err := c.GetOrLoad(ctx, "user:42:list:9", load); !errors.Is(err, cistern.ErrNotFound) {
		t.Fatalf("first GetOrLoad: %v", err)
	}
	if err := c.InvalidateTag(ctx, "user:42:lists"); err != nil {
		t.Fatal(err)
	}
	if v, err := c.GetOrLoad(ctx, "user:42:list:9", load); err != nil || v != "created" {
		t.Fatalf("GetOrLoad after the invalidation = %q, %v; want the loader to run again", v, err)
	}
}

// Tags are sorted and de-duplicated, so a tag function that returns them in
// any order still matches what was recorded.
func TestTagOrderDoesNotMatter(t *testing.T) {
	var n atomic.Int32
	shuffled := func(string) []string {
		if n.Add(1)%2 == 0 {
			return []string{"b", "a", "a"}
		}
		return []string{"a", "b"}
	}
	c, err := cistern.New[string, string]("tasks", stringKey, cistern.WithL1(newL1(t)), cistern.WithTTL(time.Minute), cistern.WithTags(shuffled))
	if err != nil {
		t.Fatal(err)
	}
	mustSetTagged(t, c, "k", "v")
	for range 3 {
		mustHit(t, c, "k", "v")
	}
}

// Two replicas sharing L2, each with its own L1 (ADR-0005). The replica that
// invalidates sees it at once; the other within its L1 TTL — RNF-11's bound
// when no Bus event reaches it.
// Negative control: verified failing with the local generation copy never
// expiring.
func TestInvalidationReachesAnotherReplicaWithinTheL1TTL(t *testing.T) {
	shared := newL1(t) // stands in for Redis
	opts := []cistern.Option{cistern.WithL2(shared), cistern.WithL1TTL(100 * time.Millisecond)}
	a := taggedCache(t, newL1(t), opts...)
	b := taggedCache(t, newL1(t), opts...)

	mustSetTagged(t, a, "user:42:list:1", "v")
	mustHit(t, a, "user:42:list:1", "v") // a's generation copy now holds the tag
	mustHit(t, b, "user:42:list:1", "v") // and so do b's L1 and copy

	if err := a.InvalidateTag(context.Background(), "user:42:lists"); err != nil {
		t.Fatal(err)
	}
	// The replica that invalidated drops its own copy: no delay at all.
	// Negative control: verified failing with InvalidateTag keeping the copy.
	mustMiss(t, a, "user:42:list:1")

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok, _ := b.Get(context.Background(), "user:42:list:1"); !ok {
			// Having read the new generation, b must not fall back on its
			// old L1 copy on the next read.
			mustMiss(t, b, "user:42:list:1")
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the other replica still served the retired entry long after its L1 TTL")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ADR-0005: once a replica has read a tag's new generation — here through
// another key of the same tag — it stops serving every older copy at once,
// long before their L1 TTL.
// Negative control: verified failing with the L1 check ignoring generations.
func TestReplicaThatLearnsTheNewGenerationStopsServingOldCopies(t *testing.T) {
	shared := newL1(t)
	opts := []cistern.Option{cistern.WithL2(shared), cistern.WithL1TTL(time.Minute)}
	a := taggedCache(t, newL1(t), opts...)
	b := taggedCache(t, newL1(t), opts...)

	mustSetTagged(t, a, "user:42:list:1", "v1")
	mustHit(t, b, "user:42:list:1", "v1") // b holds it in L1 for up to a minute
	if err := a.InvalidateTag(context.Background(), "user:42:lists"); err != nil {
		t.Fatal(err)
	}
	mustSetTagged(t, a, "user:42:list:2", "v2")
	mustHit(t, b, "user:42:list:2", "v2") // b reads the new generation from L2
	mustMiss(t, b, "user:42:list:1")
}

type failingBump struct{ *memory.Store }

func (failingBump) Bump(context.Context, []string, time.Duration) error { return errStore }

// ADR-0002: invalidation fails loud.
func TestInvalidateTagFailsLoud(t *testing.T) {
	c := taggedCache(t, failingBump{newL1(t)})
	if err := c.InvalidateTag(context.Background(), "user:42:lists"); !errors.Is(err, errStore) {
		t.Fatalf("InvalidateTag: err = %v, want the store's error", err)
	}
}

func TestTagConfigurationAndValidation(t *testing.T) {
	ctx := context.Background()

	t.Run("authoritative level must keep generations", func(t *testing.T) {
		_, err := cistern.New[string, string]("tasks", stringKey, cistern.WithL1(newRecorder()), cistern.WithTTL(time.Minute), cistern.WithTags(ownerTag))
		if !errors.Is(err, cistern.ErrInvalidConfig) {
			t.Fatalf("New: err = %v, want ErrInvalidConfig", err)
		}
	})
	t.Run("nil tag function", func(t *testing.T) {
		_, err := cistern.New[string, string]("tasks", stringKey, cistern.WithL1(newL1(t)), cistern.WithTTL(time.Minute), cistern.WithTags[string](nil))
		if !errors.Is(err, cistern.ErrInvalidConfig) {
			t.Fatalf("New: err = %v, want ErrInvalidConfig", err)
		}
	})
	t.Run("invalid tags are refused like keys", func(t *testing.T) {
		for name, tags := range map[string][]string{
			"control character": {"user:42\nlists"},
			"empty":             {""},
			"more than 8":       {"a", "b", "c", "d", "e", "f", "g", "h", "i"},
		} {
			c, err := cistern.New[string, string]("tasks", stringKey, cistern.WithL1(newL1(t)), cistern.WithTTL(time.Minute),
				cistern.WithTags(func(string) []string { return tags }))
			if err != nil {
				t.Fatal(err)
			}
			if err := c.Set(ctx, "k", "v"); !errors.Is(err, cistern.ErrInvalidKey) {
				t.Errorf("%s: Set err = %v, want ErrInvalidKey", name, err)
			}
			if _, _, err := c.Get(ctx, "k"); !errors.Is(err, cistern.ErrInvalidKey) {
				t.Errorf("%s: Get err = %v, want ErrInvalidKey", name, err)
			}
		}
	})
	t.Run("InvalidateTag validates its tag", func(t *testing.T) {
		c := taggedCache(t, newL1(t))
		if err := c.InvalidateTag(ctx, "a\x00b"); !errors.Is(err, cistern.ErrInvalidKey) {
			t.Fatalf("err = %v, want ErrInvalidKey", err)
		}
	})
	t.Run("InvalidateTag without WithTags", func(t *testing.T) {
		c := newCache(t, cistern.WithL1(newL1(t)), cistern.WithTTL(time.Minute))
		if err := c.InvalidateTag(ctx, "user:42:lists"); !errors.Is(err, cistern.ErrInvalidConfig) {
			t.Fatalf("err = %v, want ErrInvalidConfig", err)
		}
	})
}
