package cistern

import (
	"context"
	"time"
)

// Level identifies a cache level in hook events.
type Level uint8

// The levels. The zero Level means an event is not tied to one level.
const (
	LevelL1 Level = iota + 1
	LevelL2
)

// String returns "L1", "L2", or "" for the zero Level.
func (l Level) String() string {
	switch l {
	case LevelL1:
		return "L1"
	case LevelL2:
		return "L2"
	}
	return ""
}

// Op names the operation whose error a fail-open read swallowed.
type Op string

// The operations reported through OnError.
const (
	OpRead     Op = "read"     // a level could not answer
	OpDecode   Op = "decode"   // an entry was malformed, from another codec, or did not decode (RS-06)
	OpPopulate Op = "populate" // a loaded value could not be stored
	OpBackfill Op = "backfill" // an L2 entry could not be copied into L1
	OpEvent    Op = "event"    // a Bus event could not be applied locally
)

// Hooks observe a cache (RF-15, ADR-0015). Every field may be nil.
//
// Hooks are called synchronously and must not block: a hook that blocks is a
// read that blocks. A hook that panics is recovered and ignored. Events carry
// the namespace; the consumer key only with WithHookKeys, because keys usually
// embed a user id and hooks usually feed logs (RS-08).
type Hooks struct {
	// OnHit fires when a read is served, with the level that served it.
	OnHit func(ctx context.Context, e HitEvent)
	// OnMiss fires when a read finds nothing usable.
	OnMiss func(ctx context.Context, e MissEvent)
	// OnLoad fires when a loader returns.
	OnLoad func(ctx context.Context, e LoadEvent)
	// OnCoalesced fires for a caller served by another caller's load (RF-05).
	OnCoalesced func(ctx context.Context, e CoalescedEvent)
	// OnError fires for an error that fail-open swallowed (ADR-0002). Errors
	// that are returned to the caller are not reported here.
	OnError func(ctx context.Context, e ErrorEvent)
	// OnInvalidate fires when a key or tag is invalidated, by this instance
	// or, with a Bus, by another.
	OnInvalidate func(ctx context.Context, e InvalidateEvent)
}

// HitEvent describes a served read. Absent reports a remembered absence.
type HitEvent struct {
	Namespace string
	Key       string // empty unless WithHookKeys
	Level     Level
	Absent    bool
}

// MissEvent describes a read that found nothing usable.
type MissEvent struct {
	Namespace string
	Key       string // empty unless WithHookKeys
}

// LoadEvent describes a loader call.
type LoadEvent struct {
	Namespace string
	Key       string // empty unless WithHookKeys
	Duration  time.Duration
	Err       error
}

// CoalescedEvent describes a caller served by another caller's load.
type CoalescedEvent struct {
	Namespace string
	Key       string // empty unless WithHookKeys
}

// ErrorEvent describes a swallowed error. Level is zero when the error is not
// tied to one level.
type ErrorEvent struct {
	Namespace string
	Key       string // empty unless WithHookKeys
	Op        Op
	Level     Level
	Err       error
}

// InvalidateEvent describes an invalidation: of a key (Tag is empty) or of a
// tag. Remote reports one that arrived from another instance over the Bus.
// Tags are always present — they name groups, not entries.
type InvalidateEvent struct {
	Namespace string
	Key       string // empty unless WithHookKeys, for tag invalidations, and for a Remote one of a hashed key (only its hash travels)
	Tag       string
	Remote    bool
}

// safely runs a hook, recovering a panic: a logger with a bug must not break
// the cache operation it observes (ADR-0015).
func safely(fn func()) {
	defer func() { recover() }() //nolint:errcheck // the panic is deliberately dropped (ADR-0015)
	fn()
}

func (c *Cache[K, V]) hookKey(key string) string {
	if c.hookKeys {
		return key
	}
	return ""
}

func (c *Cache[K, V]) onHit(ctx context.Context, key string, level Level, absent bool) {
	if h := c.hooks.OnHit; h != nil {
		safely(func() { h(ctx, HitEvent{Namespace: c.namespace, Key: c.hookKey(key), Level: level, Absent: absent}) })
	}
}

func (c *Cache[K, V]) onMiss(ctx context.Context, key string) {
	if h := c.hooks.OnMiss; h != nil {
		safely(func() { h(ctx, MissEvent{Namespace: c.namespace, Key: c.hookKey(key)}) })
	}
}

func (c *Cache[K, V]) onLoad(ctx context.Context, key string, d time.Duration, err error) {
	if h := c.hooks.OnLoad; h != nil {
		safely(func() { h(ctx, LoadEvent{Namespace: c.namespace, Key: c.hookKey(key), Duration: d, Err: err}) })
	}
}

func (c *Cache[K, V]) onCoalesced(ctx context.Context, key string) {
	if h := c.hooks.OnCoalesced; h != nil {
		safely(func() { h(ctx, CoalescedEvent{Namespace: c.namespace, Key: c.hookKey(key)}) })
	}
}

func (c *Cache[K, V]) onError(ctx context.Context, key string, op Op, level Level, err error) {
	if h := c.hooks.OnError; h != nil {
		safely(func() {
			h(ctx, ErrorEvent{Namespace: c.namespace, Key: c.hookKey(key), Op: op, Level: level, Err: err})
		})
	}
}

func (c *Cache[K, V]) onInvalidate(ctx context.Context, key, tag string, remote bool) {
	if h := c.hooks.OnInvalidate; h != nil {
		safely(func() {
			h(ctx, InvalidateEvent{Namespace: c.namespace, Key: c.hookKey(key), Tag: tag, Remote: remote})
		})
	}
}
