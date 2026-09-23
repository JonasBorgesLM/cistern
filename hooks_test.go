package cistern_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/JonasBorgesLM/cistern"
	"github.com/JonasBorgesLM/cistern/bus"
	"github.com/JonasBorgesLM/cistern/internal/envelope"
)

// events records every hook call.
type events struct {
	mu        sync.Mutex
	hits      []cistern.HitEvent
	misses    []cistern.MissEvent
	loads     []cistern.LoadEvent
	errs      []cistern.ErrorEvent
	invalids  []cistern.InvalidateEvent
	coalesced []cistern.CoalescedEvent
}

func (e *events) hooks() cistern.Hooks {
	return cistern.Hooks{
		OnHit: func(_ context.Context, ev cistern.HitEvent) { e.mu.Lock(); e.hits = append(e.hits, ev); e.mu.Unlock() },
		OnMiss: func(_ context.Context, ev cistern.MissEvent) {
			e.mu.Lock()
			e.misses = append(e.misses, ev)
			e.mu.Unlock()
		},
		OnLoad: func(_ context.Context, ev cistern.LoadEvent) {
			e.mu.Lock()
			e.loads = append(e.loads, ev)
			e.mu.Unlock()
		},
		OnError: func(_ context.Context, ev cistern.ErrorEvent) {
			e.mu.Lock()
			e.errs = append(e.errs, ev)
			e.mu.Unlock()
		},
		OnInvalidate: func(_ context.Context, ev cistern.InvalidateEvent) {
			e.mu.Lock()
			e.invalids = append(e.invalids, ev)
			e.mu.Unlock()
		},
		OnCoalesced: func(_ context.Context, ev cistern.CoalescedEvent) {
			e.mu.Lock()
			e.coalesced = append(e.coalesced, ev)
			e.mu.Unlock()
		},
	}
}

func TestHitAndMissEventsCarryTheLevel(t *testing.T) {
	ctx := context.Background()
	var ev events
	c, _, l2 := twoLevels(t, cistern.WithHooks(ev.hooks()))
	plant(t, l2, envelope.Entry{Codec: "json", Expires: time.Now().Add(time.Minute), Payload: []byte(`"v"`)})

	if _, ok, _ := c.Get(ctx, "k"); !ok {
		t.Fatal("miss")
	}
	if _, ok, _ := c.Get(ctx, "k"); !ok {
		t.Fatal("miss")
	}
	if _, ok, _ := c.Get(ctx, "absent"); ok {
		t.Fatal("hit")
	}
	if len(ev.hits) != 2 || ev.hits[0].Level != cistern.LevelL2 || ev.hits[1].Level != cistern.LevelL1 {
		t.Fatalf("hits = %+v, want an L2 hit then an L1 hit (the backfill)", ev.hits)
	}
	if len(ev.misses) != 1 || ev.misses[0].Namespace != "tasks" {
		t.Fatalf("misses = %+v, want one in namespace tasks", ev.misses)
	}
}

// RS-08: keys usually embed a user id and hooks feed logs, so events carry the
// namespace but not the key unless the consumer asks. Every event kind is
// fired, because each hook builds its event at its own call site (#118).
// Negative control: verified failing with the key passed straight through at
// each of the six call sites in hooks.go, one at a time.
func TestHookEventsWithholdTheKeyByDefault(t *testing.T) {
	const key = "user:42:list:1"
	fire := func(opts ...cistern.Option) *events {
		ctx := context.Background()
		var ev events
		c, _, l2 := twoLevels(t, append([]cistern.Option{cistern.WithHooks(ev.hooks())}, opts...)...)

		release, done := blockedLoad(t, c, key, "v") // miss, then load
		joined := getOrLoadAsync(c, key, "v")        // coalesced
		for deadline := time.Now().Add(2 * time.Second); ; {
			l2.mu.Lock()
			gets := l2.gets
			l2.mu.Unlock()
			if gets >= 2 || time.Now().After(deadline) {
				break
			}
			time.Sleep(time.Millisecond)
		}
		time.Sleep(20 * time.Millisecond) // from its L2 miss to joining the flight
		close(release)
		<-done
		<-joined

		_, _, _ = c.Get(ctx, key) // hit
		_ = c.Delete(ctx, key)    // invalidate
		l2.mu.Lock()
		l2.failGet = errStore
		l2.mu.Unlock()
		_, _, _ = c.Get(ctx, key) // swallowed error
		return &ev
	}
	keys := func(ev *events) map[string][]string {
		ev.mu.Lock()
		defer ev.mu.Unlock()
		got := map[string][]string{}
		for _, e := range ev.misses {
			got["miss"] = append(got["miss"], e.Key)
		}
		for _, e := range ev.hits {
			got["hit"] = append(got["hit"], e.Key)
		}
		for _, e := range ev.loads {
			got["load"] = append(got["load"], e.Key)
		}
		for _, e := range ev.coalesced {
			got["coalesced"] = append(got["coalesced"], e.Key)
		}
		for _, e := range ev.errs {
			got["error"] = append(got["error"], e.Key)
		}
		for _, e := range ev.invalids {
			got["invalidate"] = append(got["invalidate"], e.Key)
		}
		return got
	}
	kinds := []string{"miss", "hit", "load", "coalesced", "error", "invalidate"}

	quiet, loud := keys(fire()), keys(fire(cistern.WithHookKeys()))
	for _, kind := range kinds {
		if len(quiet[kind]) == 0 || len(loud[kind]) == 0 {
			t.Fatalf("no %s event fired (quiet %v, loud %v); the test proves nothing about it", kind, quiet[kind], loud[kind])
		}
		for _, k := range quiet[kind] {
			if k != "" {
				t.Errorf("%s event carried the key %q without WithHookKeys", kind, k)
			}
		}
		for _, k := range loud[kind] {
			if k != key {
				t.Errorf("with WithHookKeys, %s event carried %q, want %q", kind, k, key)
			}
		}
	}
}

// ADR-0002 made read errors silent to the caller; the hook is where they go.
// Negative control: verified failing with the read error not reported.
func TestSwallowedErrorsReachOnError(t *testing.T) {
	ctx := context.Background()
	var ev events
	c, l1, l2 := twoLevels(t, cistern.WithHooks(ev.hooks()))
	l2.failGet = errStore
	_, _, _ = c.Get(ctx, "k")
	if len(ev.errs) != 1 || ev.errs[0].Op != cistern.OpRead || ev.errs[0].Level != cistern.LevelL2 || !errors.Is(ev.errs[0].Err, errStore) {
		t.Fatalf("errors = %+v, want one read error on L2", ev.errs)
	}

	l2.failGet = nil
	l2.data[pk] = []byte("not an envelope")
	_, _, _ = c.Get(ctx, "k")
	if last := ev.errs[len(ev.errs)-1]; last.Op != cistern.OpDecode || last.Level != cistern.LevelL2 {
		t.Fatalf("last error = %+v, want a decode error on L2 (RS-06)", last)
	}

	l1.failSet = errStore
	l2.failSet = errStore
	_, _ = c.GetOrLoad(ctx, "fresh", func(context.Context) (string, error) { return "v", nil })
	var populate bool
	for _, e := range ev.errs {
		populate = populate || e.Op == cistern.OpPopulate
	}
	if !populate {
		t.Fatalf("errors = %+v, want the failed population reported", ev.errs)
	}
}

func TestLoadEventsCarryDurationAndOutcome(t *testing.T) {
	ctx := context.Background()
	var ev events
	c := newCache(t, cistern.WithL1(newL1(t)), cistern.WithTTL(time.Minute), cistern.WithHooks(ev.hooks()))
	_, _ = c.GetOrLoad(ctx, "a", func(context.Context) (string, error) { time.Sleep(5 * time.Millisecond); return "v", nil })
	_, _ = c.GetOrLoad(ctx, "b", func(context.Context) (string, error) { return "", errStore })
	if len(ev.loads) != 2 || ev.loads[0].Duration < 5*time.Millisecond || ev.loads[0].Err != nil || !errors.Is(ev.loads[1].Err, errStore) {
		t.Fatalf("loads = %+v", ev.loads)
	}
}

func TestInvalidateEventsLocalAndRemote(t *testing.T) {
	ctx := context.Background()
	var local, remote events
	b := bus.NewLocal()
	a := taggedCache(t, newL1(t), cistern.WithBus(b), cistern.WithHooks(local.hooks()))
	o := taggedCache(t, newL1(t), cistern.WithBus(b), cistern.WithHooks(remote.hooks()))
	t.Cleanup(func() { a.Close(); o.Close() })

	if err := a.InvalidateTag(ctx, "user:42:lists"); err != nil {
		t.Fatal(err)
	}
	if err := a.Delete(ctx, "user:42:list:1"); err != nil {
		t.Fatal(err)
	}
	var sawLocalTag bool
	for _, e := range local.invalids {
		sawLocalTag = sawLocalTag || (!e.Remote && e.Tag == "user:42:lists")
	}
	if !sawLocalTag {
		t.Fatalf("local invalidations = %+v, want the local tag invalidation", local.invalids)
	}
	var sawRemoteTag, sawRemoteKey bool
	for _, e := range remote.invalids {
		sawRemoteTag = sawRemoteTag || (e.Remote && e.Tag == "user:42:lists")
		sawRemoteKey = sawRemoteKey || (e.Remote && e.Tag == "")
	}
	if !sawRemoteTag || !sawRemoteKey {
		t.Fatalf("remote invalidations = %+v, want a remote tag and a remote key", remote.invalids)
	}
}

// ADR-0015: a panicking hook must not turn a read into a crash.
// Negative control: verified failing with hooks not recovered.
func TestPanickingHookDoesNotBreakTheRead(t *testing.T) {
	boom := cistern.Hooks{OnMiss: func(context.Context, cistern.MissEvent) { panic("logger bug") }}
	c := newCache(t, cistern.WithL1(newL1(t)), cistern.WithTTL(time.Minute), cistern.WithHooks(boom))
	if _, ok, err := c.Get(context.Background(), "k"); ok || err != nil {
		t.Fatalf("Get = %v, %v; want a plain miss despite the hook", ok, err)
	}
}
