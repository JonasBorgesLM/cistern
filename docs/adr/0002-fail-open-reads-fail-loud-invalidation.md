# ADR-0002: Fail-open reads, fail-loud invalidation

## Status
Accepted

## Context
`moat`'s rate limiter **fails closed**: when its store errors, the request is
rejected, and that is the default "so the safer behavior is what you get by
forgetting to decide" (`moat` `doc/DESIGN.md` §4.1). For a rate limiter that is
right — a store outage that let every request through would turn the control
off exactly when someone is probing it.

`cistern` sits in the opposite position. The cache holds no authority: the
source of truth is always there behind it, and every value the cache could
serve can be loaded again. Failing a read because Redis is down would turn a
performance dependency into an availability dependency — the application would
go down with its cache (RNF-02, T-12). D6 already fixed the direction; this ADR
fixes where the line falls, because "fail-open" applied to *every* operation
would be wrong in one place.

That place is invalidation. After a consumer writes the source of truth, it
calls `Delete` or `InvalidateTag`. If that call fails silently, the cache keeps
serving the old value to every replica until its TTL runs out, and nobody knows.
Failing open there is not availability; it is a silent correctness loss.

## Decision
**Reads fail open.** In `Get` and `GetOrLoad`, an L2 error, an L2 timeout, a
`Guard` refusal or an envelope that fails validation (RS-06) is treated as a
miss: the read proceeds to L1 or to the loader, and the cache error is reported
through the error hook, never returned. This is not configurable — there is no
situation in which refusing a read is safer than answering it from the source.

**Populating writes fail open.** When a loaded value cannot be written to L2,
the value is still returned to the caller and still written to L1; the failure
goes to the error hook.

**Invalidation fails loud.** `Delete` and `InvalidateTag` always evict from the
local L1, and then return an error if L2 (or the Bus publish) failed. The
consumer just changed the source of truth and is the only party that can
retry, log or alert; the library must not decide for it that staleness is
acceptable.

**Explicit `Set` fails loud** for the same reason: a caller that asked to store
a value is told when it was not stored.

What is not a cache failure is never swallowed:

- a **loader error** is returned to the caller unchanged, and is not cached
  (negative caching applies only to `ErrNotFound`, RF-06);
- a **cancelled or expired caller context** returns the context's error
  (RF-02);
- a **validation error** on the caller's own input — an invalid key (RS-03), an
  oversized value (RS-05), a `NoCache` type (RS-04) — is returned, because it
  is a bug at the call site, not an outage.

L2 calls run under their own short timeout, independent of the caller's
deadline (RNF-03): the effective deadline is the earlier of the two, so a slow
Redis costs at most that timeout per read, not the whole request budget.

## Consequences
- A Redis outage moves the full read load onto the source of truth. Whether the
  source survives that is a property of the deployment, measured by sapper
  scenario 3; `Guard` (RNF-04) keeps each read from paying the timeout while
  the breaker is open.
- Hooks are the only channel for cache errors on the read path. A consumer that
  wires no error hook will not notice a Redis outage except as load on the
  source — `examples` must show the `crier` wiring (RF-15).
- Consumers must handle an error from `Delete`/`InvalidateTag`. The
  `task-api` decorator (T2) decides what to do with it — at minimum log it,
  since the write to the source has already succeeded.
- Unlike `moat`, there is no `WithFailureMode`. Adding one later is a new ADR.

## Open
Whether a failed invalidation should also trigger a local "distrust window"
(skip L1 for that tag for one L1 TTL). Not in the MVP; revisit with ADR-0011.
