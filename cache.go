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
)

// namespacePattern is the namespace alphabet of ADR-0008: it cannot contain
// the key separator, so every key component before the consumer key is
// unambiguous.
var namespacePattern = regexp.MustCompile(`^[a-z0-9._-]{1,64}$`)

// Cache is a typed cache-aside cache over one or two levels.
//
// A Cache is safe for concurrent use. It never holds a value object: both
// levels store encoded bytes, and every hit decodes a fresh copy, so a caller
// that mutates what it received cannot change what the next caller receives
// (ADR-0014).
type Cache[K comparable, V any] struct {
	prefix string
	key    func(K) string
	l1, l2 Store
	ttl    time.Duration
	l1TTL  time.Duration
	jitter float64
	codec  codec.Codec
	rand   func() float64
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
	cfg := config{codec: codec.JSON}
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
		prefix: "cistern:v1:" + namespace + ":-:k:",
		key:    key,
		l1:     cfg.l1,
		l2:     cfg.l2,
		ttl:    cfg.ttl,
		l1TTL:  l1TTL,
		jitter: cfg.jitter,
		codec:  cfg.codec,
		rand:   rand.Float64, // #nosec G404 -- jitter spreads expiry; a predictable draw is harmless
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
	case math.IsNaN(cfg.jitter) || cfg.jitter < 0 || cfg.jitter >= 1:
		return fmt.Errorf("jitter must be in [0, 1), got %v", cfg.jitter)
	case cfg.codec == nil:
		return errors.New("codec is nil")
	}
	return nil
}

// Get returns the cached value for k, reading L1 before L2. ok reports a hit;
// a miss is not an error.
//
// Reads fail open (ADR-0002): a level that errors, or an entry that does not
// decode, is treated as a miss. Get returns an error only when ctx is done.
func (c *Cache[K, V]) Get(ctx context.Context, k K) (value V, ok bool, err error) {
	var zero V
	if err := ctx.Err(); err != nil {
		return zero, false, err
	}
	pk := c.prefix + c.key(k)
	for _, s := range []Store{c.l1, c.l2} {
		if s == nil {
			continue
		}
		data, hit, err := s.Get(ctx, pk)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return zero, false, ctxErr
		}
		if err != nil || !hit {
			continue
		}
		var v V
		if err := c.codec.Unmarshal(data, &v); err != nil {
			continue
		}
		return v, true, nil
	}
	return zero, false, nil
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
	e := entryConfig{ttl: c.ttl}
	for _, opt := range opts {
		opt(&e)
	}
	if e.ttl <= 0 {
		return fmt.Errorf("%w: entry TTL must be positive, got %v", ErrInvalidConfig, e.ttl)
	}
	data, err := c.codec.Marshal(v)
	if err != nil {
		return fmt.Errorf("cistern: encoding value: %w", err)
	}

	scale := 1 - c.jitter*c.rand()
	l2TTL := scaled(e.ttl, scale)
	l1TTL := scaled(min(e.ttl, c.l1TTL), scale)

	pk := c.prefix + c.key(k)
	var errs []error
	if c.l2 != nil {
		errs = append(errs, c.l2.Set(ctx, pk, data, l2TTL))
	}
	if c.l1 != nil {
		errs = append(errs, c.l1.Set(ctx, pk, data, l1TTL))
	}
	return errors.Join(errs...)
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

// scaled returns d*f, at least one nanosecond so a store never sees a
// non-positive TTL.
func scaled(d time.Duration, f float64) time.Duration {
	return max(time.Duration(float64(d)*f), 1)
}
