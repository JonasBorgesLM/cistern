package cistern_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JonasBorgesLM/cistern"
	"github.com/JonasBorgesLM/cistern/memory"
)

// pausingL2 is a TagStore whose GetTagged, when armed, reads its answer and
// then waits before returning it — the window #113 is about.
type pausingL2 struct {
	*memory.Store
	armed  atomic.Bool
	paused chan struct{}
	resume chan struct{}
}

func (p *pausingL2) GetTagged(ctx context.Context, key string, genKeys []string, ttl time.Duration) (v []byte, ok bool, gens []uint64, err error) {
	v, ok, gens, err = p.Store.GetTagged(ctx, key, genKeys, ttl)
	if p.armed.CompareAndSwap(true, false) {
		close(p.paused)
		<-p.resume
	}
	return v, ok, gens, err
}

// #113: a read that fetched generations before InvalidateTag must not put
// them back into the local copy after the invalidation dropped it; otherwise
// the instance that invalidated keeps serving its retired L1 copy.
// Negative control: verified failing with the local copy accepting any put.
func TestInFlightReadCannotRestoreRetiredGenerations(t *testing.T) {
	ctx := context.Background()
	shared, err := memory.New()
	if err != nil {
		t.Fatal(err)
	}
	l2 := &pausingL2{Store: shared, paused: make(chan struct{}), resume: make(chan struct{})}
	c := taggedCache(t, newL1(t), cistern.WithL2(l2), cistern.WithL1TTL(time.Minute))

	mustSetTagged(t, c, "user:42:list:1", "retired")
	mustHit(t, c, "user:42:list:1", "retired") // L1 copy and local generations now held

	l2.armed.Store(true)
	reader := make(chan struct{})
	go func() {
		defer close(reader)
		_, _, _ = c.Get(ctx, "user:42:list:2") // same tag, not in L1: reads generations from L2
	}()
	<-l2.paused // the reader holds the pre-invalidation generations
	if err := c.InvalidateTag(ctx, "user:42:lists"); err != nil {
		t.Fatal(err)
	}
	close(l2.resume)
	<-reader

	mustMiss(t, c, "user:42:list:1")
}
