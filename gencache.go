package cistern

import (
	"sync"
	"time"
)

// maxCachedGens bounds the local copy of generations. Clearing it when full
// costs round trips, never correctness.
const maxCachedGens = 10_000

// genCache is a replica's short-lived copy of tag generations read from L2,
// which lets an L1 hit be checked without a round trip (ADR-0005). An entry
// lives at most ttl — the L1 TTL, RNF-11's staleness bound — and is dropped
// as soon as the tag is invalidated locally or by a Bus event.
//
// With a conforming L1 the bound already holds without this expiry, since
// every L1 entry expires within the L1 TTL; mutation testing confirms it is
// not load-bearing. It is kept as a second, independent bound for an L1 store
// whose own expiry is coarse.
type genCache struct {
	mu     sync.Mutex
	ttl    time.Duration
	now    func() time.Time
	m      map[string]cachedGen
	drops  map[string]uint64 // per key: how many times it was dropped
	clears uint64            // how many times the whole copy was cleared
}

// genEpoch is a snapshot of when a read began, relative to drops and clears.
// A read may only store what it fetched if nothing was dropped in between
// (#113): otherwise it would restore generations an invalidation retired.
type genEpoch struct {
	clears uint64
	drops  []uint64
}

type cachedGen struct {
	gen     uint64
	expires time.Time
}

func newGenCache(ttl time.Duration, now func() time.Time) *genCache {
	return &genCache{ttl: ttl, now: now, m: make(map[string]cachedGen), drops: make(map[string]uint64)}
}

// epoch snapshots genKeys before a read fetches their generations.
func (g *genCache) epoch(genKeys []string) genEpoch {
	g.mu.Lock()
	defer g.mu.Unlock()
	e := genEpoch{clears: g.clears, drops: make([]uint64, len(genKeys))}
	for i, k := range genKeys {
		e.drops[i] = g.drops[k]
	}
	return e
}

// get returns the generations of all genKeys, or false if any is missing or
// has expired.
func (g *genCache) get(genKeys []string) ([]uint64, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	gens := make([]uint64, len(genKeys))
	for i, k := range genKeys {
		c, ok := g.m[k]
		if !ok || !now.Before(c.expires) {
			return nil, false
		}
		gens[i] = c.gen
	}
	return gens, true
}

// put stores what a read fetched, unless a key was dropped (or the copy
// cleared) since that read took its epoch — its generations may be ones an
// invalidation has already retired.
func (g *genCache) put(genKeys []string, gens []uint64, since genEpoch) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.clears != since.clears {
		return
	}
	for i, k := range genKeys {
		if g.drops[k] != since.drops[i] {
			return
		}
	}
	if len(g.m)+len(genKeys) > maxCachedGens || len(g.drops) > maxCachedGens {
		g.clear()
	}
	expires := g.now().Add(g.ttl)
	for i, k := range genKeys {
		g.m[k] = cachedGen{gen: gens[i], expires: expires}
	}
}

// drop forgets genKey's generation, after a local or Bus invalidation. Bus
// events are untrusted and may name any number of tags, so the drop counters
// are bounded here as well as in put: a replica that never reads a tag would
// otherwise grow them without limit (#135).
func (g *genCache) drop(genKey string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.m, genKey)
	if _, seen := g.drops[genKey]; !seen && len(g.drops) >= maxCachedGens {
		g.clear()
	}
	g.drops[genKey]++
}

// clear empties the copy. Counting the clear is what keeps it safe: it resets
// the drop counters, so every epoch taken before it must be refused (#113).
// Callers hold mu.
func (g *genCache) clear() {
	clear(g.m)
	clear(g.drops)
	g.clears++
}
