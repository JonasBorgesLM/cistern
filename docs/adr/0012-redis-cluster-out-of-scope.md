# ADR-0012: Redis Cluster out of the initial scope

## Status
Accepted

## Context
`REQUIREMENTS.md` §3 lists Redis Cluster as a non-goal for v0.x, and §8.5 says
why: tag invalidation reads an entry's value together with the generation
counters of its tags in one round trip (`MGET`, pipelined), and under Cluster
those keys live in different hash slots unless the key format forces them
together with a hash tag. ADR-0008 fixed the physical key format without a hash
tag, so a multi-key read across an entry and its generations is a cross-slot
error on a cluster.

A non-goal written only in a document is one a consumer discovers in
production. The question for `redisstore`, now that it is being written, is
where the boundary is enforced.

## Decision
**`redisstore` targets standalone Redis and Sentinel, and says so in its
types.** `redisstore.New` takes a `*redis.Client` — what `redis.NewClient` and
`redis.NewFailoverClient` (Sentinel) return — not `redis.UniversalClient` or
`redis.Cmdable`, which a `*redis.ClusterClient` also satisfies. Passing a
cluster client is a compile error rather than a cross-slot error under load.

Taking the consumer's configured client, rather than an address, also means
every connection setting — AUTH and ACL username/password, TLS, pool size,
dial and read timeouts — is go-redis's own option, not a subset re-exposed by
this module (RS-09). The consumer owns the client's lifecycle; `redisstore`
never closes it.

**`New` refuses a client without `ContextTimeoutEnabled`.** go-redis v9 does
not apply a context's deadline to the socket unless that option is set; it
falls back to the client's read timeout. Measured against a server that
accepts and never answers: a 50 ms deadline returned after 5 s without the
option and after 50 ms with it. `redisstore`'s per-call timeout (RNF-03) is a
context deadline, so without the option it would silently not exist, and a
slow Redis would cost every read seconds instead of milliseconds. Refusing the
client at construction turns that into an error the consumer sees once.

## Consequences
- A deployment on Redis Cluster cannot use `redisstore` as shipped. A
  third-party `Store` over a cluster client is possible, but tag invalidation
  across slots will fail, and `cisterntest` (C5) will say so.
- A managed Redis that is a cluster behind a proxy presenting one endpoint
  (some cloud offerings) passes the type check and fails at runtime on
  multi-key commands. The README says to check before choosing one.
- Moving to Cluster is a key-schema change (hash tags, e.g. `{namespace}`),
  therefore `v2` of the physical key (ADR-0008): every replica starts cold on
  it (RELEASING.md).

## Open
**Reopening criterion:** a consumer needs horizontal sharding of the cache
itself — measured, not anticipated — and the proposal states the hash-tag
layout, what happens to tag invalidation spanning namespaces, and the
migration.
