//go:build integration

package redisstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/JonasBorgesLM/cistern"
	"github.com/JonasBorgesLM/cistern/bus"
	"github.com/JonasBorgesLM/cistern/cisterntest"
	"github.com/JonasBorgesLM/cistern/memory"
	"github.com/JonasBorgesLM/cistern/redisstore"
)

// RF-14, ADR-0005: redisstore keeps generations as every TagStore must,
// including the T-14 case, against a real Redis.
func TestTagStoreConformance(t *testing.T) {
	r := startRedis(t)
	cisterntest.RunTagStore(t, func(t *testing.T) cistern.TagStore {
		if err := r.raw.FlushDB(context.Background()).Err(); err != nil {
			t.Fatalf("FLUSHDB: %v", err)
		}
		return r.storeFor(t, nil)
	})
}

func (r *testRedis) busFor(t testing.TB, opts *redis.Options) *redisstore.Bus {
	t.Helper()
	if opts == nil {
		opts = &redis.Options{}
	}
	opts.Addr = r.addr
	opts.ContextTimeoutEnabled = true
	c := redis.NewClient(opts)
	t.Cleanup(func() { _ = c.Close() })
	b, err := redisstore.NewBus(c)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// RF-11: the Pub/Sub Bus passes the Bus suite, with each handle on its own
// connection as two replicas would be.
func TestBusConformance(t *testing.T) {
	r := startRedis(t)
	cisterntest.RunBus(t, func(t *testing.T) (bus.Bus, bus.Bus) {
		return r.busFor(t, nil), r.busFor(t, nil)
	})
}

func ownerTag(k string) []string { return []string{k[:len("user:42")] + ":lists"} }

func replica(t *testing.T, r *testRedis) *cistern.Cache[string, string] {
	t.Helper()
	l1, err := memory.New()
	if err != nil {
		t.Fatal(err)
	}
	c, err := cistern.New[string, string]("tasks", func(k string) string { return k },
		cistern.WithL1(l1), cistern.WithL2(r.storeFor(t, nil)), cistern.WithBus(r.busFor(t, nil)),
		cistern.WithTTL(time.Hour), cistern.WithL1TTL(time.Minute), cistern.WithTags(ownerTag))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The C6 exit criterion (REQUIREMENTS.md §13): a tag invalidated on one
// instance is propagated to another through Redis — generations in L2,
// events on Pub/Sub — long before the other's one-minute L1 TTL.
func TestTagInvalidationPropagatesBetweenTwoInstances(t *testing.T) {
	r := startRedis(t)
	a, b := replica(t, r), replica(t, r)
	ctx := context.Background()

	if err := a.Set(ctx, "user:42:list:1", "before"); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := b.Get(ctx, "user:42:list:1"); err != nil || !ok || v != "before" {
		t.Fatalf("b.Get = %q, %v, %v", v, ok, err)
	}
	if err := a.InvalidateTag(ctx, "user:42:lists"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "b to stop serving the invalidated entry", func() bool {
		_, ok, err := b.Get(ctx, "user:42:list:1")
		return err == nil && !ok
	})

	// And a Delete, the key-level invalidation, the same way (RF-12).
	if err := a.Set(ctx, "user:43:list:1", "v"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := b.Get(ctx, "user:43:list:1"); !ok {
		t.Fatal("b missed a fresh entry")
	}
	if err := a.Delete(ctx, "user:43:list:1"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "b to drop the deleted key", func() bool {
		_, ok, err := b.Get(ctx, "user:43:list:1")
		return err == nil && !ok
	})
}

// RS-07: whatever is published on the channel, a subscriber survives it and
// keeps delivering well-formed events.
func TestMalformedMessagesDoNotStopTheBus(t *testing.T) {
	r := startRedis(t)
	sub := r.busFor(t, nil)
	got := make(chan bus.Event, 4)
	unsubscribe, err := sub.Subscribe(func(e bus.Event) { got <- e })
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	ctx := context.Background()
	for _, junk := range []string{"", "not json", `{"v":1,"k":"value","ns":"tasks","n":"x","value":"secret"}`, string(make([]byte, 5000))} {
		if err := r.raw.Publish(ctx, redisstore.Channel, junk).Err(); err != nil {
			t.Fatal(err)
		}
	}
	want := bus.Event{Namespace: "tasks", Kind: bus.KindTag, Name: "user:42:lists"}
	if err := r.busFor(t, nil).Publish(ctx, want); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-got:
		if e != want {
			t.Fatalf("delivered %+v, want only the well-formed event %+v", e, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the bus stopped delivering after malformed messages")
	}
}
