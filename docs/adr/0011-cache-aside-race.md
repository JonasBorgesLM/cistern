# ADR-0011: The cache-aside race — what is closed, and what is accepted

## Status
Accepted

## Context
The classic race of cache-aside (T-13, `REQUIREMENTS.md` §8.4):

1. A reader misses and starts loading from the source.
2. A writer updates the source and invalidates the cache.
3. The reader finishes and stores the value it loaded in step 1 — the one the
   writer just replaced.

The cache now serves stale data until the entry expires, and nothing reports
it.

## Decision
**For tag invalidation, the race is closed.** ADR-0005 records in each entry
the tag generations observed by the read that missed, before the load. A bump
in step 2 makes the entry written in step 3 stale on arrival: the next read
compares generations and misses. Consumers who need the race closed invalidate
by tag — the `task-api` decorator invalidates its owner's list tag on every
write.

**For `Delete` of an untagged key, the race is accepted**, with two
mitigations:

- a short TTL on entries that are written to, which bounds how long a stale
  value can survive;
- coalescing (RF-05), which narrows the window to one load per key at a time.

**Delayed double delete** — deleting again a short time after the write — was
considered and deferred: it narrows the window without closing it, needs a
timer per write in the host, and the tag mechanism already closes the race for
those who need it.

## Consequences
- The documentation recommends tags for entries that are written to, and says
  why.
- An untagged key written concurrently with a `Delete` may be stale for up to
  its TTL. That is the residual of T-13 in `docs/THREAT-MODEL.md`.

## Open
**Reopening criterion:** a consumer cannot express its invalidation as tags and
measures stale reads from this race — then delayed double delete, or a
versioned `Delete`, is reconsidered with its cost in writes.
