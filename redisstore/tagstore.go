package redisstore

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/JonasBorgesLM/cistern"
)

// GetTagged returns the value under key and the current generation of each
// counter in genKeys in one round trip (RF-10, ADR-0005): a pipeline of
// SET NX for every counter — creating a missing one at an unpredictable value
// (cistern.NewGeneration) — then one MGET of the value and the counters.
func (s *Store) GetTagged(ctx context.Context, key string, genKeys []string, genTTL time.Duration) (value []byte, ok bool, gens []uint64, err error) {
	if genTTL <= 0 {
		return nil, false, nil, fmt.Errorf("redisstore: counter ttl must be positive, got %v", genTTL)
	}
	fresh, err := newGenerations(len(genKeys))
	if err != nil {
		return nil, false, nil, err
	}
	err = s.call(ctx, func(ctx context.Context) error {
		pipe := s.client.Pipeline()
		for i, gk := range genKeys {
			pipe.SetNX(ctx, gk, fresh[i], max(genTTL, time.Millisecond))
		}
		mget := pipe.MGet(ctx, append([]string{key}, genKeys...)...)
		if _, execErr := pipe.Exec(ctx); execErr != nil && !errors.Is(execErr, redis.Nil) {
			return execErr
		}
		vals := mget.Val()
		if s, isString := vals[0].(string); isString {
			value, ok = []byte(s), true
		}
		gens = make([]uint64, len(genKeys))
		for i, v := range vals[1:] {
			str, isString := v.(string)
			if !isString {
				return fmt.Errorf("counter %q vanished between SET NX and MGET", genKeys[i])
			}
			if gens[i], err = strconv.ParseUint(str, 10, 64); err != nil {
				return fmt.Errorf("counter %q holds %q: %w", genKeys[i], str, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, false, nil, fmt.Errorf("redisstore: get tagged: %w", err)
	}
	return value, ok, gens, nil
}

// Bump advances each counter in genKeys with INCR, after SET NX has created a
// missing one at an unpredictable value, and sets its TTL, in one round trip.
// INCR is atomic, so concurrent bumps from many replicas all count.
func (s *Store) Bump(ctx context.Context, genKeys []string, genTTL time.Duration) error {
	if genTTL <= 0 {
		return fmt.Errorf("redisstore: counter ttl must be positive, got %v", genTTL)
	}
	fresh, err := newGenerations(len(genKeys))
	if err != nil {
		return err
	}
	if err := s.call(ctx, func(ctx context.Context) error {
		pipe := s.client.Pipeline()
		for i, gk := range genKeys {
			pipe.SetNX(ctx, gk, fresh[i], max(genTTL, time.Millisecond))
			pipe.Incr(ctx, gk)
			pipe.PExpire(ctx, gk, max(genTTL, time.Millisecond))
		}
		_, err := pipe.Exec(ctx)
		return err
	}); err != nil {
		return fmt.Errorf("redisstore: bump: %w", err)
	}
	return nil
}

func newGenerations(n int) ([]string, error) {
	out := make([]string, n)
	for i := range out {
		g, err := cistern.NewGeneration()
		if err != nil {
			return nil, err
		}
		out[i] = strconv.FormatUint(g, 10)
	}
	return out, nil
}
