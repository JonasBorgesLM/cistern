package cistern

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"regexp"
	"time"

	"github.com/JonasBorgesLM/cistern/codec"
	"github.com/JonasBorgesLM/cistern/internal/singleflight"
)

// namespacePattern is the namespace alphabet of ADR-0008: it cannot contain
// the key separator, so every key component before the consumer key is
// unambiguous.
var namespacePattern = regexp.MustCompile(`^[a-z0-9._-]{1,64}$`)

// Every stored entry starts with one byte saying what it holds, so a
// remembered absence can never be mistaken for an encoded value.
const (
	tagValue  byte = 'v'
	tagAbsent byte = 'n'
)

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
	codec       codec.Codec
	rand        func() float64
	flights     singleflight.Group[[]byte]
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
	cfg := config{codec: codec.JSON, loadTimeout: DefaultLoadTimeout}
	for _, opt := range opts {
		opt(&cfg)
	}
	if err := validate(namespace, key != nil, &cfg); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidConfig, err)
	}
	l1TTL := cfg.ttl
	if cfg.l1TTLSet {
		l1TTL = cfg.l1TTL
	}
	return &Cache[K, V]{
		prefix:      "cistern:v1:" + namespace + ":-:k:",
		key:         key,
		l1:          cfg.l1,
		l2:          cfg.l2,
		ttl:         cfg.ttl,
		l1TTL:       l1TTL,
		negTTL:      cfg.negTTL,
		jitter:      cfg.jitter,
		loadTimeout: cfg.loadTimeout,
		codec:       cfg.codec,
		rand:        rand.Float64, // #nosec G404 -- jitter spreads expiry; a predictable draw is harmless
	}, nil
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
	case cfg.codec == nil:
		return errors.New("codec is nil")
	}
	return nil
}

// Get returns the cached value for k, reading L1 before L2. ok reports a hit;
// a miss is not an error. A remembered absence (negative caching) returns
// ErrNotFound.
//
// Reads fail open (ADR-0002): a level that errors, or an entry that does not
// decode, is treated as a miss. Apart from ErrNotFound, Get returns an error
// only when ctx is done.
func (c *Cache[K, V]) Get(ctx context.Context, k K) (value V, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return value, false, err
	}
	return c.read(ctx, c.prefix+c.key(k))
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
	pk := c.prefix + c.key(k)
	if v, ok, readErr := c.read(ctx, pk); ok || readErr != nil {
		return v, readErr
	}

	data, err := c.flights.Do(ctx, pk, c.loadTimeout, func(loadCtx context.Context) ([]byte, error) {
		v, err := load(loadCtx)
		if err != nil {
			if c.negTTL > 0 && errors.Is(err, ErrNotFound) {
				c.populate(loadCtx, pk, []byte{tagAbsent}, c.negTTL)
			}
			return nil, err
		}
		data, err := c.encode(v)
		if err != nil {
			return nil, err
		}
		c.populate(loadCtx, pk, data, e.ttl)
		return data, nil
	})
	if err != nil {
		return zero, err
	}
	return c.decode(data)
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
	data, err := c.encode(v)
	if err != nil {
		return err
	}
	return c.write(ctx, c.prefix+c.key(k), data, e.ttl)
}

// Delete removes k from every configured level. L1 is always evicted, and an
// error from L2 is returned: a failed invalidation that went unreported would
// leave stale data on every replica until it expired (ADR-0002).
func (c *Cache[K, V]) Delete(ctx context.Context, k K) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	pk := c.prefix + c.key(k)
	var errs []error
	if c.l1 != nil {
		errs = append(errs, c.l1.Delete(ctx, pk))
	}
	if c.l2 != nil {
		errs = append(errs, c.l2.Delete(ctx, pk))
	}
	return errors.Join(errs...)
}

// read looks pk up in L1, then L2. A level that errors, or holds an entry that
// does not decode, is skipped (ADR-0002).
func (c *Cache[K, V]) read(ctx context.Context, pk string) (value V, ok bool, err error) {
	for _, s := range []Store{c.l1, c.l2} {
		if s == nil {
			continue
		}
		data, hit, err := s.Get(ctx, pk)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return value, false, ctxErr
		}
		if err != nil || !hit || len(data) == 0 {
			continue
		}
		switch data[0] {
		case tagAbsent:
			return value, false, ErrNotFound
		case tagValue:
			if v, err := c.decode(data); err == nil {
				return v, true, nil
			}
		}
	}
	return value, false, nil
}

// write stores an entry in every configured level, L2 first, with one jitter
// draw for both so L1 never outlives L2 (ADR-0004).
func (c *Cache[K, V]) write(ctx context.Context, pk string, data []byte, ttl time.Duration) error {
	scale := 1 - c.jitter*c.rand()
	var errs []error
	if c.l2 != nil {
		errs = append(errs, c.l2.Set(ctx, pk, data, scaled(ttl, scale)))
	}
	if c.l1 != nil {
		errs = append(errs, c.l1.Set(ctx, pk, data, scaled(min(ttl, c.l1TTL), scale)))
	}
	return errors.Join(errs...)
}

// populate stores an entry on behalf of a read. Its failure is not the
// reader's to handle: reads fail open (ADR-0002).
func (c *Cache[K, V]) populate(ctx context.Context, pk string, data []byte, ttl time.Duration) {
	if err := c.write(ctx, pk, data, ttl); err != nil {
		return // reads fail open (ADR-0002)
	}
}

// encode returns v as a value entry.
func (c *Cache[K, V]) encode(v V) ([]byte, error) {
	data, err := c.codec.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("cistern: encoding value: %w", err)
	}
	return append([]byte{tagValue}, data...), nil
}

// decode returns a fresh V from a value entry.
func (c *Cache[K, V]) decode(entry []byte) (V, error) {
	var v V
	if err := c.codec.Unmarshal(entry[1:], &v); err != nil {
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

// scaled returns d*f, at least one nanosecond so a store never sees a
// non-positive TTL.
func scaled(d time.Duration, f float64) time.Duration {
	return max(time.Duration(float64(d)*f), 1)
}
