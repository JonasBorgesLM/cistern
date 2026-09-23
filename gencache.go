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
	mu  sync.Mutex
	ttl time.Duration
	now func() time.Time
	m   map[string]cachedGen
}

type cachedGen struct {
	gen     uint64
	expires time.Time
}

func newGenCache(ttl time.Duration, now func() time.Time) *genCache {
	return &genCache{ttl: ttl, now: now, m: make(map[string]cachedGen)}
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

func (g *genCache) put(genKeys []string, gens []uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.m)+len(genKeys) > maxCachedGens {
		clear(g.m)
	}
	expires := g.now().Add(g.ttl)
	for i, k := range genKeys {
		g.m[k] = cachedGen{gen: gens[i], expires: expires}
	}
}

func (g *genCache) drop(genKey string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.m, genKey)
}
