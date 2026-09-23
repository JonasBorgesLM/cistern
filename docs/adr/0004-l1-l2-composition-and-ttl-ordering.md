# ADR-0004: L1+L2 composition with L1 TTL ≤ L2 TTL

## Status
Accepted

## Context
D1 fixed two optional levels from the MVP: an in-process L1 and a shared Redis
L2. `task-api` runs a single replica and can start with L1 alone;
`gateway-auth` may scale horizontally and needs L2 plus the Bus (§10). The
composition rules decide what "consistent" means across replicas, so they need
to be stated before `Cache[K,V]` exists (RF-07, RF-08, RNF-11).

In a multi-replica deployment, L2 is the shared view and each L1 is a private
copy. A replica's L1 can only become stale relative to L2 in two ways: its entry
outlives the L2 entry it was copied from, or it misses a Bus invalidation. The
first is avoidable by construction; the second is not (the Bus is best-effort,
ADR-0006 planned).

## Decision
**Levels.** A cache is configured with L1 only, L2 only, or both. At least one
is required; a cache with neither is a construction error (RF-16).

**Read order.** L1 → L2 → loader (RF-07). An L2 hit backfills L1. A loaded
value is written to L2, then L1.

**TTL ordering.** When both levels are configured, the L1 TTL must be less than
or equal to the L2 TTL, validated in the constructor (RF-08). And an L1 entry
**never outlives the L2 entry it came from**: on backfill, its expiry is the
earlier of `now + L1 TTL` and the logical expiry carried in the L2 envelope. That
second rule is what makes the ordering hold per entry, including under jitter
(RF-04) and per-key TTL overrides (RF-03), where comparing the two configured
durations alone would not.

**Per-key TTL.** A per-key override sets the L2 TTL for that entry; the L1 TTL
for it is the smaller of the override and the configured L1 TTL.

**Invalidation.** `Delete` evicts L1, deletes from L2 and publishes on the Bus
(RF-09, RF-12); errors follow ADR-0002.

## Consequences
- **The worst-case staleness is one L1 TTL** (RNF-11): a replica that misses a
  Bus event serves its old copy until its L1 entry expires, and never longer.
  The L1 TTL is therefore the consistency knob, and multi-replica deployments
  should keep it short (seconds, not minutes).
- With L1 only, there is no cross-replica consistency at all; that is fine for
  a single replica and documented as unsafe for more than one.
- With L2 only, every hit is a network round trip; that is the mode for
  consumers who cannot tolerate L1 staleness.
- An L1 backfill needs the L2 entry's logical expiry, so the envelope must carry
  it — one of the reasons for ADR-0009 (planned).

## Open
None.
