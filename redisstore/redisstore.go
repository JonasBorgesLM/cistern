package redisstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/JonasBorgesLM/cistern"
)

// DefaultTimeout bounds each Redis call when WithTimeout is not given.
const DefaultTimeout = 100 * time.Millisecond

// Guard wraps every call the Store makes to Redis (RNF-04). A circuit breaker
// implements it by refusing to run op while open; the Store returns that
// error without touching Redis, and cistern's fail-open read path turns it
// into a miss (ADR-0002).
//
// op's context already carries the Store's per-call timeout. A Guard must
// call op at most once and return its error unchanged, or its own.
type Guard interface {
	Do(ctx context.Context, op func(context.Context) error) error
}

type passGuard struct{}

// Do runs op.
func (passGuard) Do(ctx context.Context, op func(context.Context) error) error { return op(ctx) }

// Option configures a Store.
type Option func(*Store)

// WithTimeout bounds each Redis call, independently of the caller's deadline
// (RNF-03): a call ends at whichever comes first. It must be positive and
// defaults to DefaultTimeout.
func WithTimeout(d time.Duration) Option { return func(s *Store) { s.timeout = d } }

// WithGuard wraps every Redis call in g. It defaults to a guard that always
// runs the call.
func WithGuard(g Guard) Option { return func(s *Store) { s.guard = g } }

// Store is a cistern.Store over one Redis, standalone or behind Sentinel
// (ADR-0012). It is safe for concurrent use.
type Store struct {
	client  *redis.Client
	timeout time.Duration
	guard   Guard
}

// New returns a Store that uses client. The client stays the caller's: its
// options carry AUTH, ACL and TLS (RS-09), and New never closes it.
//
// client must have ContextTimeoutEnabled set. Without it go-redis ignores a
// context's deadline on the socket, and the per-call timeout would silently
// not exist (ADR-0012). New returns an error wrapping cistern.ErrInvalidConfig
// for that, a nil client, a non-positive timeout or a nil Guard.
func New(client *redis.Client, opts ...Option) (*Store, error) {
	s := &Store{client: client, timeout: DefaultTimeout, guard: passGuard{}}
	for _, opt := range opts {
		opt(s)
	}
	switch {
	case client == nil:
		return nil, fmt.Errorf("%w: redisstore: client is nil", cistern.ErrInvalidConfig)
	case !client.Options().ContextTimeoutEnabled:
		return nil, fmt.Errorf("%w: redisstore: the client must set ContextTimeoutEnabled, or per-call timeouts are ignored", cistern.ErrInvalidConfig)
	case s.timeout <= 0:
		return nil, fmt.Errorf("%w: redisstore: timeout must be positive, got %v", cistern.ErrInvalidConfig, s.timeout)
	case s.guard == nil:
		return nil, fmt.Errorf("%w: redisstore: guard is nil", cistern.ErrInvalidConfig)
	}
	return s, nil
}

// Get returns the value stored under key. A missing key is (nil, false, nil).
func (s *Store) Get(ctx context.Context, key string) (value []byte, ok bool, err error) {
	err = s.call(ctx, func(ctx context.Context) error {
		v, getErr := s.client.Get(ctx, key).Bytes()
		switch {
		case errors.Is(getErr, redis.Nil):
			return nil
		case getErr != nil:
			return getErr
		}
		value, ok = v, true
		return nil
	})
	if err != nil {
		return nil, false, fmt.Errorf("redisstore: get: %w", err)
	}
	return value, ok, nil
}

// Set stores value under key for ttl, which must be positive. A TTL below one
// millisecond is stored as one millisecond, Redis's resolution.
func (s *Store) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if ttl <= 0 {
		return fmt.Errorf("redisstore: ttl must be positive, got %v", ttl)
	}
	// go-redis rounds a sub-millisecond TTL up by itself, but logs a warning on
	// every such call; jitter can produce one, so round here instead.
	ttl = max(ttl, time.Millisecond)
	if err := s.call(ctx, func(ctx context.Context) error {
		return s.client.Set(ctx, key, value, ttl).Err()
	}); err != nil {
		return fmt.Errorf("redisstore: set: %w", err)
	}
	return nil
}

// Delete removes key. Deleting a missing key is not an error.
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := s.call(ctx, func(ctx context.Context) error {
		return s.client.Del(ctx, key).Err()
	}); err != nil {
		return fmt.Errorf("redisstore: delete: %w", err)
	}
	return nil
}

// call runs op through the Guard under the per-call timeout.
func (s *Store) call(ctx context.Context, op func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	return s.guard.Do(ctx, op)
}
