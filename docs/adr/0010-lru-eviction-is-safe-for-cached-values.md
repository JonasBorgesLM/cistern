# ADR-0010: LRU eviction is safe for cached values — with one exception

## Status
Accepted

## Context
`moat`'s audit finding **M-1** is the reason this needs writing down.
`ratelimit.MemoryStore` evicted its least recently used bucket when over its
cap, and that was a rate-limit bypass: a throttled client cannot make requests,
so its empty bucket is the least recently used *by construction*, and deleting
it hands the client a full burst. "Recency is anti-correlated with safety
here." The same reasoning is why `moat`'s and `cairn`'s Redis stores require
`maxmemory-policy noeviction`.

`cistern` recommends the opposite — LRU in L1 and `allkeys-lru` in Redis
(RS-10) — and a reader who knows M-1 should be able to see why that is not the
same mistake.

## Decision
**Evicting a cached value is always safe, so LRU is the eviction policy for
L1, and `allkeys-lru` is the recommended Redis policy for L2.**

The safety argument is that a cached value carries no authority. Losing one
produces a miss, and a miss is answered from the source of truth: the worst
case is latency, never a wrong answer or a granted permission. Recency is
*positively* correlated with value here — the entries read most recently are
the ones most likely to be read again — so LRU evicts the cheapest entries to
lose. The same holds for negative-cache markers (RF-06): evicting one costs one
extra loader call.

L1 evicts on both limits it enforces, entry count and bytes (§8.2); a value
larger than the L1 byte limit is not stored in L1 at all, and is still returned
to the caller.

### The exception: tag generation counters

One kind of key in L2 **does** carry authority: the per-tag generation counter
that makes tag invalidation work (RF-10, §8.5). A physical key embeds the
generation it was written under; incrementing the counter makes every older
entry unreachable. If Redis evicts the counter and it is recreated from a
small starting value, the generation goes **backwards**, and entries written
under an earlier generation — the ones an invalidation retired — become
reachable again. That is M-1's shape exactly: losing the key grants the stale
data back.

So the claim above holds for cached values only. **A missing generation
counter must never be recreated at a value that could have been used before.**
How — a starting value drawn so it cannot repeat, counters kept out of
`allkeys-lru`'s reach, or both — is ADR-0005's to decide (C6), and ADR-0005
must not be accepted without answering it.

## Consequences
- L1 needs no cleverness beyond LRU with entry and byte limits.
- `SECURITY.md` and the `redisstore` README recommend `allkeys-lru` for the
  cache database and say plainly that it must not be shared with `moat`'s rate
  limiter or `cairn`'s link store, which require `noeviction` (RS-10).
- The generation-counter hazard is recorded as threat T-14 in
  `docs/THREAT-MODEL.md`, discharged by ADR-0005.
- `cisterntest` should include a case that deletes a generation counter
  mid-test and asserts that previously invalidated entries stay unreachable.

## Open
None here; the open part belongs to ADR-0005.
