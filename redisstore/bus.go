package redisstore

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/redis/go-redis/v9"

	"github.com/JonasBorgesLM/cistern"
	"github.com/JonasBorgesLM/cistern/bus"
)

// Channel is the Pub/Sub channel invalidation events travel on. Events carry
// their namespace, so every cache in a deployment shares it; the ACL grants
// it through &cistern:*.
const Channel = "cistern:v1:bus"

// Bus is a bus.Bus over Redis Pub/Sub (RF-11, ADR-0006). It is best-effort,
// as Pub/Sub is: a subscriber that is reconnecting misses events, which costs
// at most one L1 TTL of staleness (RNF-11).
type Bus struct {
	client *redis.Client
}

// NewBus returns a Bus that uses client, which stays the caller's. Like New,
// it refuses a client without ContextTimeoutEnabled (ADR-0012).
func NewBus(client *redis.Client) (*Bus, error) {
	switch {
	case client == nil:
		return nil, fmt.Errorf("%w: redisstore: client is nil", cistern.ErrInvalidConfig)
	case !client.Options().ContextTimeoutEnabled:
		return nil, fmt.Errorf("%w: redisstore: the client must set ContextTimeoutEnabled", cistern.ErrInvalidConfig)
	}
	return &Bus{client: client}, nil
}

// Publish sends e, bounded by DefaultTimeout.
func (b *Bus) Publish(ctx context.Context, e bus.Event) error {
	data, err := bus.Encode(e)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()
	if err := b.client.Publish(ctx, Channel, data).Err(); err != nil {
		return fmt.Errorf("redisstore: publish: %w", err)
	}
	return nil
}

// Subscribe starts delivering events to h. It waits a bounded time for Redis
// to confirm the subscription, so that when Redis is up nothing published
// after Subscribe returns is missed. When Redis is unreachable it does not
// fail: go-redis keeps reconnecting and resubscribing, and delivery starts
// once Redis is back — events published in between are missed, the
// best-effort cost ADR-0006 accepts. Failing instead would make cistern.New
// fail during an outage, turning a Redis outage into a startup outage
// (ADR-0002, #117). Messages are untrusted (RS-07): a malformed one is
// dropped and the subscription goes on.
func (b *Bus) Subscribe(h bus.Handler) (func(), error) {
	if h == nil {
		return nil, errors.New("redisstore: nil handler")
	}
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout*10)
	defer cancel()
	ps := b.client.Subscribe(ctx, Channel)
	_, _ = ps.Receive(ctx) //nolint:errcheck // Redis unreachable is not an error here: ps.Channel keeps reconnecting and resubscribing
	messages := ps.Channel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for m := range messages {
			e, err := bus.Decode([]byte(m.Payload))
			if err != nil {
				continue
			}
			h(e)
		}
	}()
	// Once, not a flag: Cache.Close may be called concurrently (#121), and a
	// second call must also return only after delivery has stopped.
	var once sync.Once
	return func() {
		once.Do(func() {
			err := ps.Close()
			<-done
			if err != nil {
				return // unsubscribe has no error result; the connection is released either way
			}
		})
	}, nil
}
