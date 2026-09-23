# cistern

A Go library for two-level cache-aside caching — an in-process L1 and a shared
Redis L2 — with load coalescing, negative caching, and invalidation by key, by
tag and by event.

The name is the reservoir inside the fortress: you drink from it instead of
going to the river, and it keeps serving when the river is slow or under siege.

> **Status: [`v0.1.0`](https://github.com/JonasBorgesLM/cistern/releases/tag/v0.1.0)
> and [`redisstore/v0.1.0`](https://github.com/JonasBorgesLM/cistern/releases/tag/redisstore/v0.1.0)
> are released** — signed, audited across three clean-session passes
> ([`docs/audit-log.md`](docs/audit-log.md)), `go get`-able. What's left of C8
> is validating the `task-api` integration under sapper — see
> [Roadmap](#roadmap).

---

## Why this exists

A cache that makes the happy path faster is easy. The work is in what happens
around it: a hot key expiring under load and sending every request to the
database at once; a key built without the user id serving one user's data to
another; a Redis outage turning a performance optimization into an
application outage; a decoded value from Redis that nobody validated.

`cistern` takes positions on those and **writes down the reasoning**:

- [`REQUIREMENTS.md`](REQUIREMENTS.md) — traceable `RF-`/`RS-`/`RNF-` ids,
  architecture, phases
- [`docs/THREAT-MODEL.md`](docs/THREAT-MODEL.md) — fourteen threats, their
  mitigations, and **what is left over**
- [`docs/adr/`](docs/adr/README.md) — the decision record
- [`CONTRIBUTING.md`](CONTRIBUTING.md) — git flow, commit convention, and the
  documentation rules CI enforces
- [`RELEASING.md`](RELEASING.md) — why the tag order is not optional
- [`SECURITY.md`](SECURITY.md) — reporting, and how to operate Redis for it

## Quick start

```go
client := redis.NewClient(&redis.Options{
	Addr:                  "redis:6379",
	ContextTimeoutEnabled: true, // required: see redisstore/README.md
})
l2, _ := redisstore.New(client, redisstore.WithTimeout(50*time.Millisecond))
b, _ := redisstore.NewBus(client)
l1, _ := memory.New(memory.WithMaxEntries(10_000))

// The owner goes in the key and in the tag (RS-02, ADR-0005).
lists, err := cistern.New[int, []Task]("tasks",
	func(owner int) string { return fmt.Sprintf("user:%d:lists", owner) },
	cistern.WithL1(l1), cistern.WithL2(l2), cistern.WithBus(b),
	cistern.WithTTL(5*time.Minute), cistern.WithL1TTL(5*time.Second),
	cistern.WithTags(func(owner int) []string { return []string{fmt.Sprintf("user:%d:lists", owner)} }),
	cistern.WithNegativeTTL(10*time.Second),
	cistern.WithHooks(hooks), // see examples/crierhooks
)
defer lists.Close()

// Reads: concurrent misses share one load; Redis down means the loader
// answers, never an error.
tasks, err := lists.GetOrLoad(ctx, 42, func(ctx context.Context) ([]Task, error) {
	return repo.ListByOwner(ctx, 42)
})

// Writes: change the source of truth, then invalidate — every replica.
err = lists.InvalidateTag(ctx, "user:42:lists")
```

[`examples/`](examples) holds runnable versions: the task-api Repository
Decorator, a bastion circuit breaker around Redis, hooks exported to crier, and
how to keep moat secrets out of the cache.

## Properties

| Property | How |
| --- | --- |
| No stampede | Per-key load coalescing plus TTL jitter; a hung loader cannot take a key down (RF-04, RF-05, [ADR-0007](docs/adr/0007-self-contained-singleflight.md)) |
| Redis down ≠ app down | Reads fail open, bounded by a short per-call timeout and an optional circuit breaker; invalidation fails loud (RNF-02, RNF-03, RNF-04, [ADR-0002](docs/adr/0002-fail-open-reads-fail-loud-invalidation.md)) |
| No cross-user leakage | Mandatory namespace; owner in the key and in the tag, each enough on its own (RS-01, RS-02, [ADR-0008](docs/adr/0008-mandatory-scope-and-physical-key-format.md)) |
| L2 data is untrusted | Versioned, size-limited envelope; the data never picks its codec; a bad entry is a miss, never a panic (RS-06, [ADR-0009](docs/adr/0009-versioned-envelope.md)) |
| Invalidation without `SCAN` | Tag generation counters checked on read, one round trip; an evicted counter can never resurrect an entry (RF-10, [ADR-0005](docs/adr/0005-tag-invalidation-with-generation-counters.md)) |
| Replicas agree quickly | A Bus drops other replicas' copies at once; a lost event costs at most one L1 TTL (RF-11, RNF-11, [ADR-0006](docs/adr/0006-best-effort-invalidation-bus.md)) |
| Callers never share a value | Both levels hold bytes; every hit decodes a fresh copy ([ADR-0014](docs/adr/0014-l1-stores-encoded-bytes.md)) |
| Secrets are never cached | A type marked `NoCache` cannot be a cache's value type (RS-04) |
| Observable without leaking | Hooks for hits, misses, loads and every swallowed error; keys withheld unless asked (RF-15, RS-08, [ADR-0015](docs/adr/0015-observability-through-hooks.md)) |
| No dependencies in the core | Standard library only; Redis lives in a separate module (RNF-01, [ADR-0001](docs/adr/0001-module-structure-and-dependency-policy.md)) |

### And what it will not do

- **Be a source of truth.** Nothing may depend on the cache for correctness.
- **Close the cache-aside race for untagged keys.** Tag invalidation closes it;
  an untagged `Delete` racing a load can leave a stale value for up to its TTL
  ([ADR-0011](docs/adr/0011-cache-aside-race.md)).
- **Protect you from a key and a tag that both omit the owner.** cistern forces
  a namespace; your key and tag functions have to encode the user.
- **Protect you from whoever can write your Redis.** Values are validated, not
  signed.
- **HTTP caching, write-behind, Redis Cluster** — out of scope for v0.x.

## Operating it

- **Redis**: a dedicated ACL user with the tested minimum, TLS outside
  development, `maxmemory-policy allkeys-lru`, and never an instance shared
  with moat's rate limiter or cairn's link store (a logical database is not
  enough) —
  [`redisstore/README.md`](redisstore/README.md) has the exact lines.
- **Staleness between replicas** is at most the L1 TTL when a Bus event is
  lost; keep it short (seconds).
- **Wire `OnError`.** Fail-open means a Redis outage returns no errors to your
  code; the hook is where it shows.

## Security model in brief

The full model is [`docs/THREAT-MODEL.md`](docs/THREAT-MODEL.md).

| Threat | Mitigation | What is left |
| --- | --- | --- |
| One user served another's data (T-01) | Mandatory namespace; owner in key and tag | A consumer omitting the owner from both |
| Hostile or corrupt L2 entry (T-05) | Envelope validation, codec pinned by the cache, fuzzed decoder | Anyone who can write Redis can serve plausible values |
| Forged Bus event (T-06) | Events carry invalidation only | Forced misses |
| Keys in logs (T-07) | Hooks withhold keys by default | A consumer who opts in |
| Stampede, penetration (T-10, T-11) | Coalescing, jitter, negative caching | Coalescing is per process |
| Redis outage (T-12) | Fail-open, per-call timeout, circuit breaker | The source carries the load |
| Stale write-back (T-13) | Generations recorded before the load | Untagged keys, up to their TTL |
| Evicted generation counter (T-14) | Recreated at an unpredictable value | None known |

## Design shape

```
cistern/             core — Cache[K,V], options, GetOrLoad, tags, hooks  (stdlib only)
├── memory/          L1: LRU over bytes, entry and byte limits, TTL, eviction hook
├── codec/           Codec interface + JSON
├── bus/             Bus interface, in-process bus, event codec
├── cisterntest/     conformance suites for Store, TagStore and Bus
└── internal/        envelope, singleflight

cistern/redisstore/  separate module — Redis L2, tag generations, Pub/Sub Bus
cistern/examples/    not published — task-api decorator, bastion, crier, moat secrets
```

## Roadmap

| Phase | Delivery |
| --- | --- |
| **C0** Bootstrap | Repository, modules, CI, conventions, ADR-0001 and ADR-0013 *(done)* |
| **C1** Public API + memory | `Cache[K,V]`, options, sentinel errors, codec, LRU L1 with TTL and jitter *(done)* |
| **C2** Loading | `GetOrLoad`, coalescing, negative caching *(done)* |
| **C3** Encoding & validation | Envelope, key validation, value limit, `NoCache` *(done)* |
| **C4** redisstore | Redis L2, fail-open, timeouts, `Guard`, testcontainers *(done)* |
| **C5** Composition | L1+L2, `cisterntest` passing for both stores *(done)* |
| **C6** Invalidation | Tag generation counters, in-process and Pub/Sub Bus *(done)* |
| **C7** Hooks & examples | Hooks, `examples` module, README *(done)* |
| **C8** Validation & release | audit **in three clean sessions** *(done)*, `v0.1.0` and `redisstore/v0.1.0` **released** *(done)*; sapper scenarios against `task-api` still pending T2 (below) |

In parallel from C7, the `task-api` track: **T0** baseline with sapper *(done —
see `#70`)* → **T1** endpoint choice → **T2** `CachedTaskRepository` → **T3**
re-measure and release. `#67`'s sapper scenarios (stampede, dead/slow Redis,
penetration, isolation) run against `task-api` after T2, so they close with T3,
not independently.

## Ecosystem

| Library | Concern |
| --- | --- |
| [`moat`](https://github.com/JonasBorgesLM/moat) | HTTP security middleware |
| [`crier`](https://github.com/JonasBorgesLM/crier) | Log control and export |
| [`cairn`](https://github.com/JonasBorgesLM/cairn) | Short links |
| [`bastion`](https://github.com/JonasBorgesLM/bastion) | Circuit breaking |
| **cistern** | Two-level caching |

## License

[MIT](LICENSE).
