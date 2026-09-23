# ADR-0006: The Bus is best-effort and carries invalidation only

## Status
Accepted

## Context
With more than one replica, each has its own L1. A `Delete` or `InvalidateTag`
on one replica reaches L2 and that replica's L1, but not the others' (RF-12).
Without a signal, the others keep serving their copies until the L1 TTL — the
RNF-11 bound, acceptable as a worst case but not as the normal case.

A delivery guarantee would need an acknowledged, persistent queue: a new
dependency and a new failure mode in the core, for a cache whose correctness
never depends on the signal arriving.

## Decision
A `Bus` (package `bus`) publishes and subscribes to invalidation events:

```go
type Event struct {
	Namespace string
	Kind      Kind   // KindKey or KindTag
	Name      string // a physical key, or a tag
}
```

- **It carries invalidation only, never values (RS-07).** The worst a forged
  or replayed event can do is cause misses.
- **It is best-effort.** `Delete` and `InvalidateTag` publish after writing the
  authoritative level; a publish failure is returned with the rest of the
  invalidation error (ADR-0002), but nothing retries it. A lost event costs at
  most one L1 TTL of staleness on the replicas that missed it (RNF-11).
- **Receivers only drop local state**: the L1 entry for a key event, the local
  copy of a tag's generation for a tag event (ADR-0005). Receiving an event is
  idempotent; a replica receiving its own events is harmless.
- **Events are scoped by namespace.** A cache ignores events for other
  namespaces, so several caches can share one Bus.
- `bus.NewLocal()` is the in-process implementation (for tests, and for
  several caches in one process); `redisstore` provides one over Redis Pub/Sub.
  A cache subscribes in `New` and unsubscribes in `Close`.
- **Received data is untrusted.** A Bus implementation decoding messages from
  the network rejects malformed ones without panicking; a cache validates an
  event's name like a key before acting on it.

## Consequences
- `Cache` gains `Close` to end its subscription. A cache without a Bus needs no
  `Close`, and calling it is harmless.
- Redis Pub/Sub delivers only to connected subscribers: a replica that is
  reconnecting misses events and relies on the L1 TTL bound. Documented, not
  mitigated.
- Anyone who can publish on the channel can force misses (T-06); the ACL user
  (RS-09) is the control, and the channel is under the `cistern` prefix.

## Open
None.

## Amendment (audit finding #117)
"A cache subscribes in `New`" had a consequence the decision did not weigh:
the Redis Bus refused to subscribe while Redis was unreachable, so `New`
failed with `ErrInvalidConfig` and a replica could not start during an outage
— a Redis outage turned into an application outage, against ADR-0002.

The Redis Bus now waits a bounded time for the subscription to be confirmed
and, if Redis does not answer, returns without an error: go-redis keeps
reconnecting and resubscribing, and delivery starts once Redis is back. Events
published before then are missed, which is the reconnecting-subscriber case
the Consequences already accept, bounded by one L1 TTL. `Subscribe` still
fails on a configuration error such as a nil handler.

## Amendment (re-audit finding #137)
The #117 amendment bounded subscribing while Redis is unreachable but not
unsubscribing, which `Close` does on every shutdown: go-redis's `Close` waits
for a background redial that only the client's dial timeout ends, about twice
that timeout in practice (10s at go-redis's default).

Unsubscribing now waits the same bound as subscribing and then returns;
the redial ends on its own, bounded by the dial timeout. The cost: an event
that arrives in that window can still reach the handler once, which an
invalidation tolerates.
