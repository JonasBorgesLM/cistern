package cistern_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JonasBorgesLM/cistern"
	"github.com/JonasBorgesLM/cistern/bus"
)

// replicas returns two caches sharing an L2 and a Bus, each with its own L1
// and an L1 TTL long enough that only the Bus can explain a prompt miss.
func replicas(t *testing.T, b bus.Bus, extra ...cistern.Option) (a, c *cistern.Cache[string, string]) {
	t.Helper()
	shared := newL1(t)
	opts := append([]cistern.Option{cistern.WithL2(shared), cistern.WithL1TTL(time.Minute), cistern.WithBus(b)}, extra...)
	a = taggedCache(t, newL1(t), opts...)
	c = taggedCache(t, newL1(t), opts...)
	t.Cleanup(func() { a.Close(); c.Close() })
	return a, c
}

// RF-12: a Delete on one replica evicts the key from the others' L1 at once.
// Negative control: verified failing with Delete not publishing.
func TestBusDeleteEvictsOtherReplicasL1(t *testing.T) {
	a, b := replicas(t, bus.NewLocal())
	mustSetTagged(t, a, "user:42:list:1", "v")
	mustHit(t, b, "user:42:list:1", "v")
	if err := a.Delete(context.Background(), "user:42:list:1"); err != nil {
		t.Fatal(err)
	}
	mustMiss(t, b, "user:42:list:1")
}

// RF-11/RF-12: a tag invalidation reaches the other replicas at once, instead
// of after their L1 TTL.
// Negative control: verified failing with tag events ignored.
func TestBusTagEventReachesOtherReplicasAtOnce(t *testing.T) {
	a, b := replicas(t, bus.NewLocal())
	mustSetTagged(t, a, "user:42:list:1", "v")
	mustHit(t, b, "user:42:list:1", "v")
	if err := a.InvalidateTag(context.Background(), "user:42:lists"); err != nil {
		t.Fatal(err)
	}
	mustMiss(t, b, "user:42:list:1")
}

// ADR-0006: with only an L1 each, replicas keep their own generations; the
// Bus is what makes an invalidation on one retire entries on the others.
// Negative control: verified failing with tag events not bumping an
// L1-authoritative cache.
func TestBusCarriesTagInvalidationBetweenL1OnlyReplicas(t *testing.T) {
	b := bus.NewLocal()
	a := taggedCache(t, newL1(t), cistern.WithBus(b))
	c := taggedCache(t, newL1(t), cistern.WithBus(b))
	t.Cleanup(func() { a.Close(); c.Close() })
	mustSetTagged(t, c, "user:42:list:1", "v")
	if err := a.InvalidateTag(context.Background(), "user:42:lists"); err != nil {
		t.Fatal(err)
	}
	mustMiss(t, c, "user:42:list:1")
}

// ADR-0006: events are scoped by namespace; caches sharing a Bus do not
// invalidate each other's entries.
func TestBusIgnoresOtherNamespaces(t *testing.T) {
	b := bus.NewLocal()
	tasks := taggedCache(t, newL1(t), cistern.WithBus(b))
	notes, err := cistern.New[string, string]("notes", stringKey, cistern.WithL1(newL1(t)), cistern.WithTTL(time.Minute),
		cistern.WithTags(ownerTag), cistern.WithBus(b))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tasks.Close(); notes.Close() })
	mustSetTagged(t, tasks, "user:42:list:1", "v")
	if err := notes.InvalidateTag(context.Background(), "user:42:lists"); err != nil {
		t.Fatal(err)
	}
	mustHit(t, tasks, "user:42:list:1", "v")
}

// RS-07: an event is untrusted input. One naming a key outside the cache, a
// key this cache could not have stored, or an invalid tag, is ignored — no
// panic, no effect, and nothing reaches the hooks.
//
// Negative control: verified failing with the key-prefix check removed, with
// the key-event validation removed, and with the tag validation removed.
func TestBusIgnoresEventsItCannotTrust(t *testing.T) {
	b := bus.NewLocal()
	shared := newL1(t) // one L1 store under two namespaces
	var ev events
	c := taggedCache(t, shared, cistern.WithBus(b), cistern.WithHooks(ev.hooks()), cistern.WithHookKeys())
	notes, err := cistern.New[string, string]("notes", stringKey, cistern.WithL1(shared), cistern.WithTTL(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	mustSetTagged(t, c, "user:42:list:1", "v")
	mustSetTagged(t, notes, "n", "note")
	for _, e := range []bus.Event{
		// Claims to be about "tasks" but names another namespace's key.
		{Namespace: "tasks", Kind: bus.KindKey, Name: "cistern:v1:notes:-:k:n"},
		{Namespace: "tasks", Kind: bus.KindKey, Name: "moat:ratelimit:1.2.3.4"},
		// This cache's prefix, but not a key it could have stored (#119): a
		// control character would reach the host's logs through the hooks.
		{Namespace: "tasks", Kind: bus.KindKey, Name: "cistern:v1:tasks:-:k:x\nlevel=admin msg=forged"},
		{Namespace: "tasks", Kind: bus.KindKey, Name: "cistern:v1:tasks:-:k:"},
		{Namespace: "tasks", Kind: bus.KindKey, Name: "cistern:v1:tasks:-:x:n"},
		{Namespace: "tasks", Kind: bus.KindKey, Name: "cistern:v1:tasks:-:h:not-a-hash"},
		{Namespace: "tasks", Kind: bus.KindTag, Name: "a\x00b"},
		{Namespace: "tasks", Kind: bus.Kind(99), Name: "user:42:lists"},
	} {
		if err := b.Publish(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	mustHit(t, c, "user:42:list:1", "v")
	mustHit(t, notes, "n", "note")
	ev.mu.Lock()
	defer ev.mu.Unlock()
	if len(ev.invalids) != 0 || len(ev.errs) != 0 {
		t.Fatalf("untrusted events reached the hooks: invalidations %+v, errors %+v", ev.invalids, ev.errs)
	}
}

// ADR-0008: a key longer than MaxKeyBytes is stored under its hash, and an
// event naming it must still evict it from the other replicas — validating
// event names (#119) must not reject hashed keys.
func TestBusDeleteEvictsAHashedKeyFromOtherReplicas(t *testing.T) {
	long := "user:42:" + strings.Repeat("x", cistern.MaxKeyBytes)
	a, b := replicas(t, bus.NewLocal(), cistern.WithKeyHashing())
	mustSetTagged(t, a, long, "v")
	mustHit(t, b, long, "v")
	if err := a.Delete(context.Background(), long); err != nil {
		t.Fatal(err)
	}
	mustMiss(t, b, long)
}

type failingBus struct{ *bus.Local }

func (failingBus) Publish(context.Context, bus.Event) error { return errStore }

// ADR-0002/ADR-0006: a publish that fails is reported with the invalidation;
// the local effects still happen.
func TestPublishFailureFailsLoud(t *testing.T) {
	ctx := context.Background()
	c := taggedCache(t, newL1(t), cistern.WithBus(failingBus{bus.NewLocal()}))
	t.Cleanup(c.Close)
	mustSetTagged(t, c, "user:42:list:1", "v")

	if err := c.InvalidateTag(ctx, "user:42:lists"); !errors.Is(err, errStore) {
		t.Fatalf("InvalidateTag: err = %v, want the publish error", err)
	}
	mustMiss(t, c, "user:42:list:1")

	mustSetTagged(t, c, "user:42:list:2", "v")
	if err := c.Delete(ctx, "user:42:list:2"); !errors.Is(err, errStore) {
		t.Fatalf("Delete: err = %v, want the publish error", err)
	}
	mustMiss(t, c, "user:42:list:2")
}

// Close ends the subscription; calling it again, or on a cache without a Bus,
// is harmless.
func TestCloseUnsubscribes(t *testing.T) {
	a, b := replicas(t, bus.NewLocal())
	mustSetTagged(t, a, "user:42:list:1", "v")
	mustHit(t, b, "user:42:list:1", "v")
	b.Close()
	b.Close()
	if err := a.Delete(context.Background(), "user:42:list:1"); err != nil {
		t.Fatal(err)
	}
	mustHit(t, b, "user:42:list:1", "v") // no longer listening: its L1 copy stands

	newCache(t, cistern.WithL1(newL1(t)), cistern.WithTTL(time.Minute)).Close()
}

func TestWithBusValidation(t *testing.T) {
	_, err := cistern.New[string, string]("tasks", stringKey, cistern.WithL1(newL1(t)), cistern.WithTTL(time.Minute), cistern.WithBus(nil))
	if !errors.Is(err, cistern.ErrInvalidConfig) {
		t.Fatalf("WithBus(nil): err = %v, want ErrInvalidConfig", err)
	}
}
