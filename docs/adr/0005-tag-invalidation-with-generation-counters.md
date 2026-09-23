# ADR-0005: Tag invalidation with generation counters checked on read

## Status
Accepted

## Context
RF-10 asks for invalidation of a whole group of keys — "every list of user 42"
— without `SCAN` or `KEYS`, which are O(keyspace) and block Redis. The
standard answer is a *generation counter* per tag: bumping it retires every
entry written under an older generation.

`REQUIREMENTS.md` described that in two ways that cannot both hold: §8.3 puts
the generation *inside the physical key*, and §8.5 reads the generations "via
`MGET` together with the value, in one round trip". With the generation in the
key, the value's key is only known after the generation is read — two round
trips, not one — and every bump orphans the old entries in Redis until their
TTL.

Two further constraints were already on the record:

- **ADR-0010's exception (T-14).** A counter evicted by `allkeys-lru` and
  recreated from a small value goes *backwards*, and entries retired by an
  earlier bump become current again. This ADR may not be accepted without
  answering that.
- **The cache-aside race (T-13).** A reader that loads an old value while a
  writer invalidates can store the old value after the invalidation.

The choice of where generations live and how tags are declared was made with
the project owner; the options not taken are recorded below.

## Decision
### Generations live in the envelope, and are checked on read
The physical key does not change: its generations component is always `-`
(ADR-0008, amended). Each entry's envelope records the generation of each of
its tags **at the time the entry's value was read from the source** (ADR-0009,
amended to version 2). A read fetches the value *and* the current generation of
its tags — in one round trip where the store can pipeline it (`MGET` in
Redis) — and an entry whose recorded generations differ from the current ones
is a miss. `InvalidateTag` bumps the counter; nothing is scanned or deleted.

### Tags are a function of the key
`WithTags(func(K) []string)` is given at `New`, beside the key function. Reads
and writes derive a key's tags from the same function, so a reader can never
check different tags than the writer recorded. Tags are validated like keys
(ADR-0008): non-empty, UTF-8, no control characters, at most 256 bytes; at most
8 per entry, sorted and de-duplicated.

### Counters
- A counter is stored under `cistern:v1:<namespace>:g:<tag>` — its fourth
  component `g` can never be an entry's `-`, and the ACL's `~cistern:*` covers
  it.
- **A missing counter is created with an unpredictable value** in
  [1, 2⁶²), drawn from `crypto/rand`, never from zero or one. That is the
  answer to T-14: an evicted or expired counter is recreated at a value no
  entry recorded, so every entry of that tag becomes a miss — never a
  resurrection. Bumping increments; 2⁶² leaves room for more bumps than will
  ever happen.
- Counters carry a TTL of twice the cache TTL, refreshed on every bump, so
  tags that are no longer used do not accumulate in Redis. An expired counter
  is the same as an evicted one: safe, and costs misses.
- The counters are kept by the **authoritative level**: L2 when there is one,
  else L1. A `Store` that can hold them implements `TagStore`; `New` refuses
  `WithTags` when the authoritative level does not.

### Entries are written with the generations read before the load
`GetOrLoad` records the generations observed by the read that missed, not the
ones current after the load. If the tag is bumped while the loader runs, the
entry is born stale and the next read misses. For tag invalidation, that closes
the cache-aside race of T-13 (ADR-0011). An explicit `Set` records the
generations current when it runs.

### Two levels: a short-lived local copy of the generations
Checking every L1 hit against L2 would make L1 pointless. With both levels, a
`Cache` keeps the generations it last read from L2 for at most the L1 TTL, and
an L1 hit is valid when its recorded generations match that copy. The copy is
dropped immediately on a Bus event for the tag (ADR-0006) and on the
instance's own `InvalidateTag`. A lost event therefore leaves a replica stale
for at most one L1 TTL — RNF-11's bound, unchanged. The copy is bounded; when
full it is cleared, which costs round trips, never correctness.

## Options not taken
- **Generation in the physical key** (as §8.3 and ADR-0008 anticipated): two
  round trips on every read that does not have the generation locally, and
  orphaned entries in Redis after every bump.
- **`Tags(...)` per call** (as approved in #13): every read and write would
  have to repeat the writer's tags, and a single call site that forgot would
  silently check nothing.

## Consequences
- `Store` implementations that want to serve tagged caches implement two more
  methods; `cisterntest` gains a suite for them, including the T-14 case (a
  counter deleted and recreated must not repeat).
- Invalidating a tag is one round trip and O(1) in the number of entries it
  retires; retired entries expire on their own TTL.
- An entry with tags costs 8 bytes per tag in its envelope.
- An untagged cache pays nothing: no counters are read.

## Open
None.
