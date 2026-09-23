# redisstore

cistern's Redis-backed L2 `Store`: `github.com/JonasBorgesLM/cistern/redisstore`.

A separate module, so go-redis never enters the dependency graph of a consumer
that runs L1-only or brings its own store
([ADR-0001](../docs/adr/0001-module-structure-and-dependency-policy.md)).

```go
client := redis.NewClient(&redis.Options{
	Addr:                  "redis:6379",
	Username:              "cistern",
	Password:              os.Getenv("CISTERN_REDIS_PASSWORD"),
	TLSConfig:             &tls.Config{MinVersion: tls.VersionTLS12},
	ContextTimeoutEnabled: true, // required, see below
})
l2, err := redisstore.New(client, redisstore.WithTimeout(50*time.Millisecond))
```

## What it requires of the client

- **`ContextTimeoutEnabled: true`.** Without it go-redis ignores a context's
  deadline on the socket; measured, a 50 ms deadline returned after 5 s.
  `redisstore`'s per-call timeout (RNF-03) is a context deadline, so `New`
  refuses such a client rather than letting the timeout silently not exist
  ([ADR-0012](../docs/adr/0012-redis-cluster-out-of-scope.md)).
- **A `*redis.Client`** — standalone, or Sentinel through
  `redis.NewFailoverClient`. Redis Cluster is out of scope and a
  `*redis.ClusterClient` does not compile here (ADR-0012). A managed "cluster
  behind one endpoint" passes the type check and fails on multi-key commands:
  check before choosing one.
- The client stays yours: `redisstore` never closes it.

## Failure behaviour

Every call is bounded by `WithTimeout` (default 100 ms), whichever comes first
with the caller's deadline, and runs through an optional `Guard` — a circuit
breaker such as `bastion` refuses calls while open and Redis is not touched at
all (RNF-04). The store reports errors; cistern turns them into misses on the
read path and returns them from `Set` and `Delete`
([ADR-0002](../docs/adr/0002-fail-open-reads-fail-loud-invalidation.md)). With
Redis down, reads are served by the loader, each costing at most the timeout.

## Operating Redis for it

**A dedicated ACL user with the minimum it needs** (RS-09). This is the exact
user the integration suite creates and proves sufficient, and proves refused
outside cistern's keys and for administrative commands:

```
ACL SETUSER cistern on >s3cret-for-tests resetkeys ~cistern:* resetchannels -@all +get +set +del
```

Use your own password, not the one above. The Pub/Sub invalidation bus (C6)
will add its commands and channel to this line.

**TLS** outside local development, through the client's `TLSConfig`. The
integration suite does not exercise TLS; that is go-redis's code path, not
this module's.

**`maxmemory-policy allkeys-lru`** (RS-10). For a cache this is the right
policy: losing a cached value costs a miss, never a wrong answer
([ADR-0010](../docs/adr/0010-lru-eviction-is-safe-for-cached-values.md)). It is
the deliberate opposite of what `moat`'s rate limiter and `cairn`'s link store
require (`noeviction`), so **never share an instance or logical database with
them**: their keys would be evicted under cistern's memory pressure. One
exception is recorded for later — tag generation counters must not be allowed
to go backwards when evicted (ADR-0010, ADR-0005).

## Tests

```bash
go test ./...                       # unit tests, no Docker
go test -tags=integration ./...     # real Redis through testcontainers
```
