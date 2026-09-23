package cistern

import (
	"fmt"
	"testing"
	"time"
)

// #135, T-06: tag events are untrusted and name any tag they like. Drops
// alone, with no tagged read on the replica, must not grow the local copy
// past its bound.
// Negative control: verified failing with drop not bounded.
func TestGenCacheDropsAreBounded(t *testing.T) {
	g := newGenCache(time.Minute, time.Now)
	for i := range 3 * maxCachedGens {
		g.drop(fmt.Sprintf("cistern:v1:t:g:tag:%d", i))
	}
	if len(g.drops) > maxCachedGens || len(g.m) > maxCachedGens {
		t.Fatalf("after %d drops: %d drop counters, %d generations; bound is %d", 3*maxCachedGens, len(g.drops), len(g.m), maxCachedGens)
	}
}

// #113 through the bound of #135: clearing the drop counters resets them, so
// the clear must also invalidate every epoch taken before it. Otherwise a read
// whose tag was dropped mid-read sees its counter back at the value it
// recorded and stores a retired generation.
// Negative control: verified failing with drop clearing without counting the
// clear.
func TestGenCacheClearByDropsRefusesOlderReads(t *testing.T) {
	g := newGenCache(time.Minute, time.Now)
	const tag = "cistern:v1:t:g:user:42:lists"
	since := g.epoch([]string{tag}) // a read begins
	g.drop(tag)                     // the tag is invalidated during it
	for i := range maxCachedGens + 1 {
		g.drop(fmt.Sprintf("cistern:v1:t:g:flood:%d", i))
	}
	g.put([]string{tag}, []uint64{1}, since) // the read tries to store what it fetched
	if gens, ok := g.get([]string{tag}); ok {
		t.Fatalf("a read that began before the tag was dropped stored generation %v", gens)
	}
}
