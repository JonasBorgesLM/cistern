package cistern

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/JonasBorgesLM/cistern/bus"
	"github.com/JonasBorgesLM/cistern/codec"
	"github.com/JonasBorgesLM/cistern/internal/envelope"
	"github.com/JonasBorgesLM/cistern/internal/singleflight"
)

// namespacePattern is the namespace alphabet of ADR-0008: it cannot contain
// the key separator, so every key component before the consumer key is
// unambiguous.
var namespacePattern = regexp.MustCompile(`^[a-z0-9._-]{1,64}$`)

// Loader fetches a value from the source of truth. It returns ErrNotFound,
// possibly wrapped, when the value does not exist.
//
// The context it receives carries the values of the caller that started the
// load but not its cancellation, and is bounded by the load timeout
// (ADR-0007).
type Loader[V any] func(ctx context.Context) (V, error)

// Cache is a typed cache-aside cache over one or two levels.
//
// A Cache is safe for concurrent use. It never holds a value object: both
// levels store encoded bytes, and every hit decodes a fresh copy, so a caller
// that mutates what it received cannot change what the next caller receives
// (ADR-0014).
type Cache[K comparable, V any] struct {
	prefix      string
	key         func(K) string
	l1, l2      Store
	ttl         time.Duration
	l1TTL       time.Duration
	negTTL      time.Duration // zero: negative caching off
	jitter      float64
	loadTimeout time.Duration
	maxValue    int
	hashKeys    bool
	codec       codec.Codec
	codecID     string
	now         func() time.Time
	rand        func() float64
	flights     singleflight.Group[[]byte]
	hooks       Hooks
	hookKeys    bool

	// Tag invalidation (ADR-0005); tagsFn is nil for an untagged cache.
	tagsFn    func(K) []string
	auth      TagStore      // the level that keeps generations
	gens      *genCache     // with two levels only
	genTTL    time.Duration // counters outlive the entries that record them
	genPrefix string

	// Cross-instance invalidation (ADR-0006); bus is nil without WithBus.
	namespace   string
	bus         bus.Bus
	unsubscribe func()
}

// New returns a Cache whose entries live under namespace.
//
// The namespace names a domain, such as "tasks", and must match
// [a-z0-9._-]{1,64}; there is no cache without one (RS-01). key turns a K into
// the consumer key. When entries belong to a user or tenant, key must put the
// owner in it — "user:42:list" — because cistern cannot tell a key that omits
// the owner from one that does not need it (RS-02, ADR-0008).
//
// At least one of WithL1 and WithL2, and WithTTL, are required. New returns an
// error wrapping ErrInvalidConfig for any invalid configuration.
func New[K comparable, V any](namespace string, key func(K) string, opts ...Option) (*Cache[K, V], error) {
	cfg := config{codec: codec.JSON, loadTimeout: DefaultLoadTimeout, maxValue: DefaultMaxValueBytes}
	for _, opt := range opts {
		opt(&cfg)
	}
	if err := validate(namespace, key != nil, &cfg); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidConfig, err)
	}
	if uncacheableType[V]() {
		return nil, fmt.Errorf("%w: the value type %T implements NoCache", ErrUncacheable, *new(V))
	}
	l1TTL := cfg.ttl
	if cfg.l1TTLSet {
		l1TTL = cfg.l1TTL
	}
	var tagsFn func(K) []string
	var auth TagStore
	if cfg.tagsSet {
		var err error
		if tagsFn, auth, err = tagging[K](&cfg); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidConfig, err)
		}
	}
	c := &Cache[K, V]{
		prefix:      "cistern:v1:" + namespace + ":-:",
		key:         key,
		l1:          cfg.l1,
		l2:          cfg.l2,
		ttl:         cfg.ttl,
		l1TTL:       l1TTL,
		negTTL:      cfg.negTTL,
		jitter:      cfg.jitter,
		loadTimeout: cfg.loadTimeout,
		maxValue:    cfg.maxValue,
		hashKeys:    cfg.hashKeys,
		codec:       cfg.codec,
		codecID:     cfg.codec.ID(),
		now:         time.Now,
		rand:        rand.Float64, // #nosec G404 -- jitter spreads expiry; a predictable draw is harmless
		tagsFn:      tagsFn,
		auth:        auth,
		genTTL:      2 * cfg.ttl,
		genPrefix:   "cistern:v1:" + namespace + ":g:",
		namespace:   namespace,
		bus:         cfg.bus,
		hooks:       cfg.hooks,
		hookKeys:    cfg.hookKeys,
	}
	if tagsFn != nil && cfg.l1 != nil && cfg.l2 != nil {
		c.gens = newGenCache(l1TTL, func() time.Time { return c.now() })
	}
	if c.bus != nil {
		unsubscribe, err := c.bus.Subscribe(c.onEvent)
		if err != nil {
			return nil, fmt.Errorf("%w: subscribing to the bus: %w", ErrInvalidConfig, err)
		}
		c.unsubscribe = unsubscribe
	}
	return c, nil
}

// tagging checks WithTags against the key type and finds the level that will
// keep generations: L2 if there is one, else L1 (ADR-0005).
func tagging[K comparable](cfg *config) (func(K) []string, TagStore, error) {
	fn, ok := cfg.tagsFn.(func(K) []string)
	if !ok {
		return nil, nil, fmt.Errorf("WithTags was given a %T, not a func of this cache's key type", cfg.tagsFn)
	}
	if fn == nil {
		return nil, nil, errors.New("WithTags was given a nil function")
	}
	authority := cfg.l1
	if cfg.l2 != nil {
		authority = cfg.l2
	}
	auth, ok := authority.(TagStore)
	if !ok {
		return nil, nil, fmt.Errorf("WithTags needs the authoritative level (%T) to implement TagStore", authority)
	}
	return fn, auth, nil
}

func validate(namespace string, hasKey bool, cfg *config) error {
	switch {
	case !namespacePattern.MatchString(namespace):
		return fmt.Errorf("namespace %q must match %s", namespace, namespacePattern)
	case !hasKey:
		return errors.New("key function is nil")
	case !cfg.l1Set && !cfg.l2Set:
		return errors.New("no level: use WithL1, WithL2 or both")
	case cfg.l1Set && cfg.l1 == nil:
		return errors.New("WithL1 was given a nil Store")
	case cfg.l2Set && cfg.l2 == nil:
		return errors.New("WithL2 was given a nil Store")
	case cfg.ttl <= 0:
		return fmt.Errorf("TTL must be set with WithTTL and positive, got %v", cfg.ttl)
	case cfg.l1TTLSet && !cfg.l1Set:
		return errors.New("WithL1TTL requires WithL1")
	case cfg.l1TTLSet && cfg.l1TTL <= 0:
		return fmt.Errorf("L1 TTL must be positive, got %v", cfg.l1TTL)
	case cfg.l1TTLSet && cfg.l1TTL > cfg.ttl:
		return fmt.Errorf("L1 TTL %v exceeds TTL %v (RF-08)", cfg.l1TTL, cfg.ttl)
	case cfg.negTTLSet && (cfg.negTTL <= 0 || cfg.negTTL > cfg.ttl):
		return fmt.Errorf("negative TTL must be positive and no longer than TTL %v, got %v", cfg.ttl, cfg.negTTL)
	case cfg.loadTimeoutSet && cfg.loadTimeout <= 0:
		return fmt.Errorf("load timeout must be positive, got %v", cfg.loadTimeout)
	case math.IsNaN(cfg.jitter) || cfg.jitter < 0 || cfg.jitter >= 1:
		return fmt.Errorf("jitter must be in [0, 1), got %v", cfg.jitter)
	case cfg.maxValue <= 0:
		return fmt.Errorf("max value bytes must be positive, got %d", cfg.maxValue)
	case cfg.busSet && cfg.bus == nil:
		return errors.New("WithBus was given a nil Bus")
	case cfg.codec == nil:
		return errors.New("codec is nil")
	case len(cfg.codec.ID()) < 1 || len(cfg.codec.ID()) > 32:
		return fmt.Errorf("codec id %q must be 1 to 32 bytes (ADR-0009)", cfg.codec.ID())
	}
	return nil
}

// Get returns the cached value for k, reading L1 before L2. ok reports a hit;
// a miss is not an error. A remembered absence (negative caching) returns
// ErrNotFound.
//
// Reads fail open (ADR-0002): a level that errors, or an entry that does not
// decode, is treated as a miss. Apart from ErrNotFound, Get returns an error
// only for an invalid key (ErrInvalidKey) or when ctx is done.
func (c *Cache[K, V]) Get(ctx context.Context, k K) (value V, ok bool, err error) {
	if ctx.Err() != nil {
		return value, false, ctx.Err()
	}
	s, err := c.slotFor(k)
	if err != nil {
		return value, false, err
	}
	value, ok, _, err = c.read(ctx, s)
	return value, ok, err
}

// GetOrLoad returns the cached value for k, or loads it with load, stores it
// and returns it. Concurrent calls for the same missing key share one load
// (RF-05); each caller still decodes its own copy.
//
// Like Get, it fails open: a cache level that errors is a miss, and a value
// that cannot be stored is still returned. A loader error is returned as is and
// not cached, except ErrNotFound when negative caching is enabled
// (WithNegativeTTL), which is remembered and returned as ErrNotFound until it
// expires. A load that exceeds the load timeout returns an error wrapping
// context.DeadlineExceeded.
func (c *Cache[K, V]) GetOrLoad(ctx context.Context, k K, load Loader[V], opts ...EntryOption) (V, error) {
	var zero V
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if load == nil {
		return zero, fmt.Errorf("%w: loader is nil", ErrInvalidConfig)
	}
	e, optErr := entryOptions(c.ttl, opts)
	if optErr != nil {
		return zero, optErr
	}
	s, keyErr := c.slotFor(k)
	if keyErr != nil {
		return zero, keyErr
	}
	v, ok, gens, readErr := c.read(ctx, s)
	if ok || readErr != nil {
		return v, readErr
	}
	// A tagged entry must record the generations read before the load, so an
	// invalidation during the load leaves it stale (ADR-0011). If they could
	// not be read, the value is still returned, but not cached.
	cacheable := c.tagsFn == nil || gens != nil

	var led atomic.Bool
	payload, err := c.flights.Do(ctx, flightKey(s.pk, c.tagsFn != nil, gens), c.loadTimeout, func(loadCtx context.Context) ([]byte, error) {
		led.Store(true)
		start := time.Now()
		v, err := load(loadCtx)
		c.onLoad(loadCtx, s.key, time.Since(start), err)
		if err != nil {
			if cacheable && c.negTTL > 0 && errors.Is(err, ErrNotFound) {
				c.populate(loadCtx, s, envelope.Entry{Absent: true, Gens: gens}, c.negTTL)
			}
			return nil, err
		}
		if uncacheable(v) {
			return nil, fmt.Errorf("%w: the loader returned a %T", ErrUncacheable, v)
		}
		payload, err := c.codec.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("cistern: encoding value: %w", err)
		}
		if cacheable && len(payload) <= c.maxValue {
			c.populate(loadCtx, s, envelope.Entry{Payload: payload, Gens: gens}, e.ttl)
		}
		return payload, nil
	})
	if !led.Load() {
		c.onCoalesced(ctx, s.key)
	}
	if err != nil {
		return zero, err
	}
	return c.decode(payload)
}

// Set stores v under k in every configured level, L2 first. Unlike a read, it
// fails loud (ADR-0002): an error from any level is returned, so a caller that
// asked to store a value knows when it was not stored.
//
// After writing the source of truth, prefer Delete to Set: two concurrent Sets
// can reach the cache in the opposite order from the source, while two
// Deletes can only cost a miss (ADR-0003).
func (c *Cache[K, V]) Set(ctx context.Context, k K, v V, opts ...EntryOption) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e, err := entryOptions(c.ttl, opts)
	if err != nil {
		return err
	}
	s, err := c.slotFor(k)
	if err != nil {
		return err
	}
	if uncacheable(v) {
		return fmt.Errorf("%w: %T", ErrUncacheable, v)
	}
	payload, err := c.codec.Marshal(v)
	if err != nil {
		return fmt.Errorf("cistern: encoding value: %w", err)
	}
	if len(payload) > c.maxValue {
		return fmt.Errorf("%w: %d bytes encoded, limit %d", ErrValueTooLarge, len(payload), c.maxValue)
	}
	var gens []uint64
	if c.tagsFn != nil {
		if _, _, gens, err = c.auth.GetTagged(ctx, s.pk, s.genKeys, c.genTTL); err != nil {
			return fmt.Errorf("cistern: reading tag generations: %w", err)
		}
	}
	return c.write(ctx, s.pk, envelope.Entry{Payload: payload, Gens: gens}, e.ttl)
}

// Delete removes k from every configured level. L1 is always evicted, and an
// error from L2 is returned: a failed invalidation that went unreported would
// leave stale data on every replica until it expired (ADR-0002).
func (c *Cache[K, V]) Delete(ctx context.Context, k K) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, err := c.slotFor(k)
	if err != nil {
		return err
	}
	var errs []error
	if c.l1 != nil {
		errs = append(errs, c.l1.Delete(ctx, s.pk))
	}
	if c.l2 != nil {
		errs = append(errs, c.l2.Delete(ctx, s.pk))
	}
	errs = append(errs, c.publish(ctx, bus.KindKey, s.pk))
	err = errors.Join(errs...)
	if err == nil {
		c.onInvalidate(ctx, s.key, "", false)
	}
	return err
}

// read looks a slot up. For a tagged cache it also returns the tags' current
// generations (see readTagged); for an untagged one, nil.
func (c *Cache[K, V]) read(ctx context.Context, s slot) (value V, ok bool, gens []uint64, err error) {
	if c.tagsFn != nil {
		return c.readTagged(ctx, s)
	}
	value, ok, err = c.readPlain(ctx, s)
	return value, ok, nil, err
}

// readPlain looks pk up in L1, then L2. A level that errors, or holds an entry
// that is malformed, from another codec, past its logical expiry or not
// decodable, is skipped (ADR-0002, ADR-0009). A usable L2 entry is copied
// into L1.
func (c *Cache[K, V]) readPlain(ctx context.Context, s slot) (value V, ok bool, err error) {
	for _, lv := range []struct {
		store Store
		level Level
	}{{c.l1, LevelL1}, {c.l2, LevelL2}} {
		if lv.store == nil {
			continue
		}
		data, hit, err := lv.store.Get(ctx, s.pk)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return value, false, ctxErr
		}
		if err != nil {
			c.onError(ctx, s.key, OpRead, lv.level, err)
			continue
		}
		if !hit {
			continue
		}
		e, usable := c.checked(ctx, s.key, lv.level, data)
		if !usable {
			continue
		}
		v, found, err := c.served(ctx, s.key, lv.level, e)
		if err != nil && !errors.Is(err, ErrNotFound) {
			continue
		}
		if lv.level == LevelL2 && c.l1 != nil {
			c.backfill(ctx, s, data, e.Expires)
		}
		return v, found, err
	}
	c.onMiss(ctx, s.key)
	return value, false, nil
}

// backfill copies an entry read from L2 into L1, for no longer than the L1
// TTL and never beyond the L2 entry's own expiry (ADR-0004).
func (c *Cache[K, V]) backfill(ctx context.Context, s slot, data []byte, expires time.Time) {
	ttl := min(c.l1TTL, expires.Sub(c.now()))
	if ttl <= 0 {
		return
	}
	if err := c.l1.Set(ctx, s.pk, data, ttl); err != nil {
		c.onError(ctx, s.key, OpBackfill, LevelL1, err) // reads fail open (ADR-0002)
	}
}

// write stores an entry in every configured level, L2 first, with one jitter
// draw for both so L1 never outlives L2 (ADR-0004). The envelope's logical
// expiry is the longest physical TTL used (ADR-0009).
func (c *Cache[K, V]) write(ctx context.Context, pk string, entry envelope.Entry, ttl time.Duration) error {
	scale := 1 - c.jitter*c.rand()
	longest := scaled(ttl, scale)
	entry.Codec = c.codecID
	entry.Expires = c.now().Add(longest)
	data, err := envelope.Encode(entry)
	if err != nil {
		return fmt.Errorf("cistern: %w", err)
	}
	var errs []error
	if c.l2 != nil {
		errs = append(errs, c.l2.Set(ctx, pk, data, longest))
	}
	if c.l1 != nil {
		errs = append(errs, c.l1.Set(ctx, pk, data, scaled(min(ttl, c.l1TTL), scale)))
	}
	return errors.Join(errs...)
}

// populate stores an entry on behalf of a read. Its failure is not the
// reader's to handle: reads fail open (ADR-0002).
func (c *Cache[K, V]) populate(ctx context.Context, s slot, entry envelope.Entry, ttl time.Duration) {
	if err := c.write(ctx, s.pk, entry, ttl); err != nil {
		c.onError(ctx, s.key, OpPopulate, 0, err) // reads fail open (ADR-0002)
	}
}

// decode returns a fresh V from an encoded payload.
func (c *Cache[K, V]) decode(payload []byte) (V, error) {
	var v V
	if err := c.codec.Unmarshal(payload, &v); err != nil {
		return v, fmt.Errorf("cistern: decoding value: %w", err)
	}
	return v, nil
}

func entryOptions(ttl time.Duration, opts []EntryOption) (entryConfig, error) {
	e := entryConfig{ttl: ttl}
	for _, opt := range opts {
		opt(&e)
	}
	if e.ttl <= 0 {
		return e, fmt.Errorf("%w: entry TTL must be positive, got %v", ErrInvalidConfig, e.ttl)
	}
	return e, nil
}

// flightKey is what concurrent loads are coalesced by. For a tagged cache it
// includes the tag generations the caller read: a caller that saw different
// generations — another owner's tag, or a tag invalidated since — must not be
// served a load that recorded others (#112, ADR-0007 as amended).
func flightKey(pk string, tagged bool, gens []uint64) string {
	if !tagged {
		return pk
	}
	if gens == nil {
		return pk + "\x00?" // generations unknown: never shares with a load that knew them
	}
	var b strings.Builder
	b.WriteString(pk)
	b.WriteByte(0)
	for i, g := range gens {
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(strconv.FormatUint(g, 10))
	}
	return b.String()
}

// scaled returns d*f, at least one nanosecond so a store never sees a
// non-positive TTL.
func scaled(d time.Duration, f float64) time.Duration {
	return max(time.Duration(float64(d)*f), 1)
}
