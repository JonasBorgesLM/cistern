package cistern

import (
	"time"

	"github.com/JonasBorgesLM/cistern/codec"
)

// Option configures a Cache. Options are validated together by New, which
// returns an error wrapping ErrInvalidConfig rather than panicking (RF-16).
type Option func(*config)

type config struct {
	l1, l2         Store
	l1Set, l2Set   bool
	ttl            time.Duration
	l1TTL          time.Duration
	l1TTLSet       bool
	jitter         float64
	codec          codec.Codec
	negTTL         time.Duration
	negTTLSet      bool
	loadTimeout    time.Duration
	loadTimeoutSet bool
}

// DefaultLoadTimeout bounds a load when WithLoadTimeout is not given.
const DefaultLoadTimeout = 10 * time.Second

// WithL1 sets the in-process level, typically a memory.Store.
func WithL1(s Store) Option {
	return func(c *config) { c.l1, c.l1Set = s, true }
}

// WithL2 sets the shared level, typically a redisstore.Store.
func WithL2(s Store) Option {
	return func(c *config) { c.l2, c.l2Set = s, true }
}

// WithTTL sets how long an entry lives, unless a call overrides it with TTL.
// It is required and must be positive. With both levels configured it is the
// L2 TTL.
func WithTTL(d time.Duration) Option {
	return func(c *config) { c.ttl = d }
}

// WithL1TTL caps how long an entry lives in L1. It must be positive and no
// longer than the TTL (RF-08), and requires WithL1. It defaults to the TTL.
//
// With more than one replica, it is also the worst-case staleness: a replica
// that misses an invalidation serves its copy until its L1 entry expires
// (ADR-0004, RNF-11).
func WithL1TTL(d time.Duration) Option {
	return func(c *config) { c.l1TTL, c.l1TTLSet = d, true }
}

// WithJitter shortens each entry's TTL by a random fraction of up to f, so
// entries written together do not expire together (RF-04). f must be in
// [0, 1). Jitter never lengthens a TTL, and the same draw applies to both
// levels, so L1 still never outlives L2.
func WithJitter(f float64) Option {
	return func(c *config) { c.jitter = f }
}

// WithCodec sets how values are encoded. It defaults to codec.JSON.
func WithCodec(cd codec.Codec) Option {
	return func(c *config) { c.codec = cd }
}

// WithNegativeTTL enables negative caching (RF-06): when a loader returns
// ErrNotFound, the absence is remembered for d instead of calling the loader
// again on every request for the missing key (T-11). d must be positive and no
// longer than the TTL; the L1 copy of an absence also obeys the L1 TTL.
func WithNegativeTTL(d time.Duration) Option {
	return func(c *config) { c.negTTL, c.negTTLSet = d, true }
}

// WithLoadTimeout bounds how long a load may run (ADR-0007). It must be
// positive and defaults to DefaultLoadTimeout. When it expires, the callers
// waiting on the load get an error wrapping context.DeadlineExceeded and the
// next caller starts a fresh load, even if the loader never returns.
func WithLoadTimeout(d time.Duration) Option {
	return func(c *config) { c.loadTimeout, c.loadTimeoutSet = d, true }
}

// EntryOption configures a single call.
type EntryOption func(*entryConfig)

type entryConfig struct {
	ttl time.Duration
}

// TTL overrides the cache's TTL for one entry. It must be positive. With both
// levels configured it sets the L2 TTL; the L1 TTL is the smaller of it and
// the configured L1 TTL (ADR-0004). In GetOrLoad, the options of the call that
// starts a load are the ones that apply to what it stores.
func TTL(d time.Duration) EntryOption {
	return func(e *entryConfig) { e.ttl = d }
}
