package cistern

import (
	"time"

	"github.com/JonasBorgesLM/cistern/bus"
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
	maxValue       int
	hashKeys       bool
	tagsFn         any // func(K) []string, checked against K in New
	tagsSet        bool
	bus            bus.Bus
	busSet         bool
	hooks          Hooks
	hookKeys       bool
}

// Defaults used when the corresponding option is not given.
const (
	DefaultLoadTimeout   = 10 * time.Second
	DefaultMaxValueBytes = 1 << 20
)

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

// WithMaxValueBytes bounds the encoded size of a value (RS-05). It must be
// positive and defaults to DefaultMaxValueBytes. Set fails with
// ErrValueTooLarge over it; GetOrLoad returns an oversized loaded value
// without caching it; a stored entry over it is read as a miss (ADR-0009).
func WithMaxValueBytes(n int) Option {
	return func(c *config) { c.maxValue = n }
}

// WithKeyHashing stores a consumer key longer than MaxKeyBytes as its SHA-256
// instead of refusing it (RS-03, ADR-0008). Keys within the limit are never
// hashed, and every key is still checked for control characters and UTF-8.
func WithKeyHashing() Option {
	return func(c *config) { c.hashKeys = true }
}

// WithTags declares how a key's tags are derived, which enables InvalidateTag
// (RF-10, ADR-0005): every entry records the generation of its tags, and a tag
// invalidated since reads as a miss. Reads and writes derive tags from the
// same function, so they can never disagree. At most 8 tags per key, each
// valid like a key; order and duplicates do not matter.
//
// The cache's authoritative level — L2 if there is one, else L1 — must
// implement TagStore; New checks it.
func WithTags[K comparable](tags func(K) []string) Option {
	return func(c *config) { c.tagsFn, c.tagsSet = tags, true }
}

// WithBus connects the cache to other instances through b (RF-11, ADR-0006):
// Delete and InvalidateTag publish an event, and events from other instances
// evict the local L1 copy of a key, or retire a tag locally, at once instead
// of after the L1 TTL. The cache subscribes in New; call Close to stop.
//
// The Bus is best-effort; a lost event costs at most one L1 TTL of staleness
// (RNF-11). A publish failure is returned by the invalidation that caused it.
func WithBus(b bus.Bus) Option {
	return func(c *config) { c.bus, c.busSet = b, true }
}

// WithHooks sets the hooks the cache reports to (RF-15, ADR-0015).
func WithHooks(h Hooks) Option {
	return func(c *config) { c.hooks = h }
}

// WithHookKeys puts the consumer key in hook events. Without it events carry
// only the namespace, because keys usually embed a user id and hooks usually
// feed logs (RS-08).
func WithHookKeys() Option {
	return func(c *config) { c.hookKeys = true }
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
