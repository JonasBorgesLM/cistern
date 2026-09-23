# ADR-0003: Cache-aside as the default; write-through deferred

## Status
Accepted

## Context
There are three ways a cache can relate to its source of truth:

| Pattern | Who writes the source | Who updates the cache |
| --- | --- | --- |
| Cache-aside | the consumer | the consumer, after writing (invalidate or set) |
| Write-through | the cache, synchronously | the cache |
| Write-behind | the cache, asynchronously | the cache |

Write-behind is a non-goal (`REQUIREMENTS.md` §3): it can lose writes that the
caller was told succeeded. Write-through makes the cache a participant in the
consumer's write path — its transactions, its error handling, its retries —
which a library that knows nothing about the consumer's storage cannot do well.

`task-api`'s shape settles the default: the goal is to cache reads **without
touching the Service layer**, which means a Repository Decorator that reads
through the cache and invalidates on writes (§1, §10). That is cache-aside.

## Decision
**Cache-aside is the only write pattern in the MVP.**

- `cistern` never writes to the source of truth. `GetOrLoad` takes the loader
  per call; the library owns no connection to the consumer's storage.
- On a write, the consumer writes the source first and then **invalidates**
  (`Delete` / `InvalidateTag`), rather than `Set`ting the new value. Two
  writers that each `Set` can land in the cache in the opposite order from the
  source; two writers that each invalidate cannot leave a wrong value behind,
  only a miss. `Set` exists for values the consumer derives itself, and the
  documentation says so.
- Write-through is deferred to RF-18 as a consumer-implemented `Writer`
  interface, so the consumer's write semantics stay in the consumer's code.

## Consequences
- Every write site must invalidate. Forgetting one is a stale-data bug the
  library cannot detect. The Decorator pattern exists to keep those sites in
  one place per repository; `examples` shows it (C7) and `task-api` uses it (T2).
- The classic cache-aside race remains: a reader can write back a value loaded
  before a concurrent invalidation (T-13). Its mitigations and the accepted
  residual are ADR-0011's.
- Invalidation errors reach the caller (ADR-0002), because the consumer is the
  only party that knows a write just happened.

## Open
**Reopening criterion for write-through (RF-18):** a consumer shows, with a
sapper measurement, that the read-after-write miss on a hot key costs more than
the complexity of a `Writer` — and the proposal states how a failed cache write
after a successful source write is reported.
