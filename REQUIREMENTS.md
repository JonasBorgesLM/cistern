# cistern — Requirements

**Version:** 0.1
**Status:** baseline for implementation — no code written yet
**Module:** `github.com/JonasBorgesLM/cistern`
**Ecosystem:** moat · crier · cairn · bastion · sapper · gateway-auth · task-api

This document is binding. Every `RF-`, `RS-` and `RNF-` identifier below is
referenced from commit messages, ADRs and tests. A requirement without a test
that fails when the protection is removed counts as unimplemented — the rule
is inherited from `moat`, `crier` and `cairn`.

> `cistern` — the reservoir inside the fortress: you drink from it instead of
> going to the river (the source of truth), and it keeps serving when the
> source is slow or under siege.

---

## 1. Context

`task-api` (Handler → Service → Repository, bearer token, single-instance
Kubernetes deployment) already integrates `moat`, `crier` and `cairn`. The goal
is to add caching to heavy read endpoints **without touching the Service
layer**, while producing a library reusable by `gateway-auth` (which may scale
horizontally) and by future projects.

Engineering premise: **measure before caching.** Integration into `task-api`
only happens after a baseline with `sapper` (phase T0), and the gain is
demonstrated against the same scenario.

## 2. Decisions already closed

| # | Decision | Origin |
| --- | --- | --- |
| D1 | Two-level architecture (L1 memory + L2 Redis) from the MVP, each level optional | Initial discussion |
| D2 | Stale-while-revalidate is **out of the MVP**; the envelope format is already built to support it | Initial discussion |
| D3 | Tag invalidation via *generation keys* is in the MVP | Initial discussion |
| D4 | Name: **cistern** | Initial discussion |
| D5 | Cache-aside is the default pattern; write-through is post-MVP via an interface implemented by the consumer | Initial discussion |
| D6 | Failure policy is **fail-open** (the opposite of `moat`'s rate limiter) | Initial discussion |
| D7 | Multi-module: dependency-free core + separate `redisstore` (same pattern as `moat`/`crier`) | Ecosystem convention |

## 3. Goals and non-goals

**Goals**
- Generic, typed API, easy to plug in as a Repository Decorator.
- Correctness under concurrency: no stampede, no leakage between users,
  bounded and documented staleness.
- Graceful degradation: Redis being down never takes the application down,
  only removes the performance gain.
- Observability without coupling (hooks), integrable with `crier`.
- Validatable under load by `sapper`.

**Non-goals (v0.x)**
- Not a database and not a source of truth; nothing must depend on the cache
  for correctness.
- No Redis Cluster support in the MVP (see §11). Target: standalone/Sentinel
  Redis.
- No HTTP caching (ETag/`Cache-Control`); authenticated responses keep
  `no-store` via `moat`.
- No write-behind; no stale-while-revalidate in the MVP; no binary codecs in
  the MVP.
- Never cache secrets, tokens or sessions.

## 4. Glossary

- **L1**: in-process memory cache, LRU with an entry and byte limit.
- **L2**: shared Redis cache.
- **Loader**: consumer-supplied function that fetches the value from the
  source of truth.
- **Scope/namespace**: mandatory prefix that isolates keys by domain and by
  owner (e.g. user).
- **Tag**: logical group of keys that can be invalidated at once (e.g. "lists
  of user 123").
- **Generation key**: per-tag counter embedded in the physical key;
  incrementing it invalidates the whole group without scanning keys.
- **Envelope**: the serialized storage format (version, codec, metadata,
  payload).
- **Bus**: invalidation-event channel between instances.

## 5. Functional requirements (FR)

| ID | Requirement | Priority |
| --- | --- | --- |
| **RF-01** | Generic typed cache `Cache[K, V]` with `Get`, `Set`, `Delete` and `GetOrLoad(ctx, key, loader)` | MVP |
| **RF-02** | Every operation takes a `context.Context` and honours cancellation/deadline | MVP |
| **RF-03** | Configurable TTL per cache (default) and per key (override) | MVP |
| **RF-04** | Configurable TTL jitter (percentage) to avoid mass expiry (avalanche) | MVP |
| **RF-05** | Load coalescing (singleflight): N concurrent calls for the same missing key trigger a single `loader` call | MVP |
| **RF-06** | Optional negative caching: when the loader returns the `ErrNotFound` sentinel, the absence is cached with its own short TTL | MVP |
| **RF-07** | Level composition: L1 only, L2 only, or L1+L2 (read L1 → L2 → loader; write populates both) | MVP |
| **RF-08** | L1 TTL must be ≤ L2 TTL (validated in the constructor) | MVP |
| **RF-09** | Key invalidation (`Delete`) propagating to L1 and L2 | MVP |
| **RF-10** | Tag invalidation via generation keys (`InvalidateTag`), without `SCAN`/`KEYS` | MVP |
| **RF-11** | `Bus` interface for invalidation events; in-process implementation in the core and Redis Pub/Sub in `redisstore` | MVP |
| **RF-12** | On receiving a Bus event, an instance removes the key/tag from its own L1 | MVP |
| **RF-13** | Pluggable codec (Strategy); JSON as the default | MVP |
| **RF-14** | `Store` interface implementable by third parties + an exported conformance suite (`cisterntest`) | MVP |
| **RF-15** | Observability hooks (hit, miss, load, coalesced, error, evict, invalidate) | MVP |
| **RF-16** | Configuration via Functional Options, validated in the constructor (error, never panic) | MVP |
| **RF-17** | Stale-while-revalidate (serve an expired value within a window and reload in the background) | Post-MVP |
| **RF-18** | Write-through via a `Writer` interface implemented by the consumer | Post-MVP |
| **RF-19** | Optional binary codecs (e.g. msgpack) in a separate module | Post-MVP |

## 6. Security requirements (SR)

| ID | Requirement |
| --- | --- |
| **RS-01** | **Mandatory scope**: the constructor requires a non-empty namespace; physical keys are always prefixed. There is no "scopeless" cache. |
| **RS-02** | **Isolation by owner**: `KeyFunc` with a documented tenant/user scope as the standard pattern; the `task-api` example builds `user:{id}:…`. Explicit test attempting to read another user's data (must fail). |
| **RS-03** | **Key validation**: length limit and rejection of control characters (avoids ambiguous collisions and log injection). Long keys may be hashed (SHA-256) opt-in. |
| **RS-04** | **Never cache secrets**: a marker interface `NoCache` — types implementing it are rejected by `Set` with an error. Document that `moat`'s `secret.Value` must not be cached, with a test in the examples module. |
| **RS-05** | **Value size limit** (bytes) on `Set` and on decode. |
| **RS-06** | **L2 data is untrusted**: decode with a size limit, envelope version/codec validation, and no codec that instantiates arbitrary types. An invalid envelope is a miss plus an error hook, never a panic. |
| **RS-07** | **The Bus carries invalidation only, never values.** Worst case of a forged message from someone with Redis access: extra misses (documented in the threat model). |
| **RS-08** | **Privacy in hooks**: hooks receive the namespace and the operation by default, not the full key (which may contain user ids). The full key is available only via an explicit option. |
| **RS-09** | **Redis connection**: configurable AUTH/ACL and TLS support; the README documents an ACL user with the minimum commands required. |
| **RS-10** | **`maxmemory-policy`** documented: for a cache, `allkeys-lru` is correct (explicit contrast with `moat`, where the same policy caused a bypass). Require a separate instance from the rate limiter: the policy is instance-wide, so a separate logical DB does not isolate eviction (#114). |
| **RS-11** | **Threat model** in `docs/THREAT-MODEL.md`: assets, considered attackers (malicious authenticated user, Redis access, DoS via non-existent keys), mitigations and accepted risks. |

## 7. Non-functional requirements (NFR)

| ID | Requirement |
| --- | --- |
| **RNF-01** | Core with **zero external dependencies**; Go 1.24 as the core's minimum version. `redisstore`/`examples` may require more because their dependencies impose it (documented as an imposition, not a choice). |
| **RNF-02** | **Fail-open**: an L2 failure/timeout degrades to L1/loader; a read operation never fails because of the cache. Cache errors are reported via a hook. |
| **RNF-03** | Short, configurable timeouts on L2 operations, independent of the request deadline. |
| **RNF-04** | `Guard` extension point in `redisstore` to wrap Redis calls (circuit breaker); integration with `bastion` demonstrated in the examples, with no dependency in the core. |
| **RNF-05** | Safe for concurrent use; `-race` tests mandatory in CI. |
| **RNF-06** | Hot-path benchmarks (L1 hit, L2 hit, miss with loader, coalescing) versioned; relevant regressions documented in the release notes. |
| **RNF-07** | ≥ 85% coverage in the core; L2 integration tests with **testcontainers** (real Redis, not miniredis). |
| **RNF-08** | An `ExampleXxx` in **every** public package (a lesson from `realip` in `moat`). |
| **RNF-09** | CI: build, `-race`, lint, `govulncheck`, integration tests; GitHub private vulnerability reporting enabled. |
| **RNF-10** | Releases with signed (SSH) tags, semver, release notes generated from the real API diff, a GitHub Release per tag. |
| **RNF-11** | Maximum documented staleness: in a multi-replica deployment, bounded by the L1 TTL in the worst case (a lost Bus event). |

## 8. Architecture

### 8.1 Modules

```
github.com/JonasBorgesLM/cistern              (core, Go 1.24, zero deps)
github.com/JonasBorgesLM/cistern/redisstore   (L2 + Bus Pub/Sub, depends on go-redis)
github.com/JonasBorgesLM/cistern/examples     (task-api decorator, bastion, crier, integration tests)
```

### 8.2 Packages (core)

| Package | Responsibility |
| --- | --- |
| `cistern` | `Cache[K,V]`, options, `GetOrLoad`, L1+L2 composition, coalescing, negative caching, tags |
| `cistern/memory` | L1 store: LRU with an entry and byte limit, TTL, eviction |
| `cistern/codec` | `Codec` interface + JSON |
| `cistern/envelope` | Versioned format (internal, or under `internal/`) |
| `cistern/bus` | `Bus` interface + in-process implementation |
| `cistern/cisterntest` | Conformance suite for `Store` and `Bus` implementations |

Coalescing (singleflight) is a self-contained implementation under
`internal/`, to keep the core dependency-free.

### 8.3 Read flow (`GetOrLoad`)

1. Validate the key and build the physical key: `namespace` + tag generations
   + the consumer's key. *(Resolved differently by ADR-0005: generations are
   recorded in the envelope and checked on read, not encoded in the key.)*
2. L1 hit → return.
3. L2 hit (through `Guard` + timeout) → populate L1 → return.
4. L2 error/timeout → error hook, treated as a miss (fail-open).
5. Miss → coalesce by physical key → call `loader` → write L2 and L1 (TTL with
   jitter) → return.
6. `loader` returns `ErrNotFound` and negative caching is enabled → write an
   absence marker with a short TTL.

### 8.4 Write/invalidation flow (cache-aside)

1. The consumer writes to the source of truth.
2. The consumer calls `Delete(key)` and/or `InvalidateTag(tag)`.
3. `InvalidateTag` increments the tag's generation in L2 (`INCR`) and publishes
   an event on the Bus.
4. Instances remove the affected entries from L1 on receiving the event.

**Known race condition** (the classic cache-aside one): a reader loads a stale
value, a writer invalidates, the reader writes the stale value afterwards.
MVP mitigation: a short TTL on entries subject to writes, plus documentation.
*Delayed double delete* is evaluated as a post-MVP option. Recorded in an ADR.

### 8.5 Generation keys

- Each tag's generation is stored in L2; reads fetch the generations via
  `MGET` alongside the value (pipelined) to avoid extra round trips. *(ADR-0005
  settles the tension with §8.3 in favour of this: generations are checked
  against the envelope.)*
- In L1, the generation is cached with a short TTL and updated by Bus events.
- Old generation keys expire naturally through TTL — nothing is scanned.
- Consequence: incompatible with Redis Cluster without hash tags (the reason
  for the non-goal in §3).

### 8.6 Envelope

Fields: format version, codec id, logical expiry, an absence flag (negative
cache), payload. The logical expiry, kept separate from the physical TTL, is
what allows stale-while-revalidate to be enabled later without breaking the
format (D2).

## 9. Design patterns

| Pattern | Where |
| --- | --- |
| Decorator | `CachedTaskRepository` wrapping the real Repository in `task-api`; `Guard` wrapping Redis calls |
| Strategy | `Store`, `Codec`, `Bus`, eviction policy |
| Observer / Pub-Sub | Cross-instance invalidation; observability hooks |
| Singleflight | Load coalescing |
| Functional Options | Configuration with constructor validation |
| Generics | Typed `Cache[K, V]` |
| Null Object | No-op hooks and `Guard` by default |

## 10. Ecosystem integration

| Project | Relation |
| --- | --- |
| **task-api** | Main consumer, via a Repository Decorator; Service untouched; user-scoped keys; every write invalidates the owner's list tag |
| **bastion** | Circuit breaker + timeout on Redis calls via `Guard` (example, no dependency in the core) |
| **crier** | Observability hooks exported as logs/metrics; example in the `examples` module |
| **moat** | Same multi-module and release pattern; `secret.Value` is never cached (RS-04); `no-store` stays on authenticated HTTP responses |
| **cairn** | Shares the Redis infrastructure; needs a separate instance (its store requires `noeviction`) and its own ACL user |
| **gateway-auth** | Future multi-replica consumer — the reason L2 + Bus exist from the MVP |
| **sapper** | Validation harness: baseline, stampede, dead Redis, slow Redis |
| **security-scanner** | Runs against `task-api` after integration, focused on cross-user leakage |

## 11. Risks

| Risk | Impact | Mitigation |
| --- | --- | --- |
| Cross-user leakage from a badly built key | High | RS-01, RS-02, explicit test, scanner |
| Stale data after a write (cache-aside race) | Medium | Short TTL, tag invalidation, documented in an ADR |
| Stampede on a hot key | Medium | Coalescing (RF-05) + jitter (RF-04) |
| Redis unavailability adding latency | Medium | Fail-open + short timeout + `bastion` |
| Unbounded L1 growth | Medium | Entry and byte limit with LRU |
| L1+L2 complexity for a single-replica consumer | Low | Levels are optional; `task-api` can start with L1 only |
| `redisstore` dependencies bringing in CVEs | Low | `govulncheck` in CI, the same policy already used in `moat` |

## 12. ADRs to record

| ADR | Title |
| --- | --- |
| 0001 | Multi-module structure and dependency-free core |
| 0002 | Fail-open in the cache vs. fail-closed in `moat`'s rate limiter |
| 0003 | Cache-aside as the default; write-through deferred |
| 0004 | L1+L2 composition with L1 TTL ≤ L2 TTL |
| 0005 | Tag invalidation via generation keys (no `SCAN`/`KEYS`) |
| 0006 | Bus is best-effort and carries invalidation only |
| 0007 | Self-contained singleflight under `internal/` |
| 0008 | Mandatory key scope |
| 0009 | Versioned envelope prepared for stale-while-revalidate |
| 0010 | LRU eviction is safe here (contrast with `moat`'s M-1) |
| 0011 | Cache-aside race: accepted risk and mitigations |
| 0012 | Redis Cluster out of the initial scope |
| 0013 | Minimum Go version per module |

Workflow rule: ADRs are never rewritten. Changes come in as an amendment or a
supersession.

## 13. Development phases

| Phase | Delivery | Exit criterion |
| --- | --- | --- |
| **C0** | Bootstrap: repo, multi-module, CI (race, lint, govulncheck), ADR templates, `SECURITY.md` skeleton, vulnerability reporting enabled | CI green on an empty repo; ADR-0001 and ADR-0013 accepted |
| **C1** | Public API (`Cache[K,V]`, options, sentinel errors) + `memory` (LRU, TTL, jitter, limits) | Tests + Examples; L1 hit benchmark |
| **C2** | `GetOrLoad`, coalescing, negative caching | Test proving 1 load for N concurrent callers; `-race` |
| **C3** | Codec, envelope, key validation, value limit, `NoCache` | Tests for a malformed envelope without a panic |
| **C4** | `redisstore` L2, fail-open, timeouts, `Guard`, testcontainers | Test with Redis killed mid-run: reads keep working |
| **C5** | L1+L2 composition | `cisterntest` suite passing for memory and redisstore |
| **C6** | Tags/generation keys + Bus (in-process and Pub/Sub) | Tag invalidation propagated between two instances in a test |
| **C7** | Hooks + `examples` module (task-api decorator, bastion, crier) + README | Every public package has an Example; README with a summarized threat model |
| **C8** | Validation and release: sapper scenarios, audit **in a clean session**, v0.1.0 with signed tags | Sapper report attached; audit findings resolved or accepted in an ADR |

**task-api integration track (parallel from C7 onward)**

| Phase | Delivery |
| --- | --- |
| **T0** | Baseline with `sapper` on the candidate endpoints (before any cache code) |
| **T1** | Endpoint selection based on the baseline; ADR in `task-api` |
| **T2** | `CachedTaskRepository` (Decorator), per-user scope, tag invalidation on write |
| **T3** | Run the same sapper scenarios + scanner; compare with T0; new semver version of `task-api` |

## 14. Validation scenarios with sapper

1. **Baseline vs. cache**: same load, p50/p95/p99 and error rate, with and
   without the cache.
2. **Stampede**: expire a hot key under high load; assert the number of loader
   calls is ≈ 1 per window.
3. **Dead Redis**: kill Redis during the load; assert the error-rate SLO holds
   (fail-open) and the `bastion` breaker opens.
4. **Slow Redis**: inject latency into Redis; assert the short timeout keeps
   degradation within the SLO.
5. **Penetration**: load with non-existent ids; assert negative caching
   protects the source.
6. **Isolation**: user A trying to obtain user B's cached data; assert zero
   leakage.

## 15. MVP (v0.1.0) acceptance criteria

- All FRs marked MVP implemented with tests and Examples.
- RS-01 through RS-11 satisfied and reflected in `SECURITY.md`.
- Sapper scenarios 1–6 run and their results recorded.
- Audit performed in a clean session, by someone who did not write the code
  (the item that was left unmet in `moat`).
- CI green on the tagged commit, `govulncheck` clean on the build, signed tags
  and GitHub Releases created.

## 16. Open questions (to decide during C0–C1)

- ~~Exact physical key format (separator, opt-in hashing threshold).~~
  Decided in ADR-0008.
- ~~Final public API naming (a synonym-free review, as done in `moat`).~~
  Decided in #13.
- Which `task-api` endpoint goes first: depends on the T0 result.
