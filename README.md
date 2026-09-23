# cistern

A Go library for two-level cache-aside caching — an in-process L1 and a shared
Redis L2 — with load coalescing, negative caching, and invalidation by key, by
tag and by event.

The name is the reservoir inside the fortress: you drink from it instead of
going to the river, and it keeps serving when the river is slow or under siege.

> **Status: unreleased (phase C7 next).** The typed `Cache[K,V]` over an
> in-process L1 and a Redis L2 — `Get`/`GetOrLoad`/`Set`/`Delete`, backfill,
> load coalescing, negative caching, tag invalidation, a cross-instance Bus,
> the versioned envelope, key validation, value limits, `NoCache` — and the
> `cisterntest` conformance suites exist; hooks and the examples module do not
> yet — see [Roadmap](#roadmap). No version is tagged, and the API may still
> change.

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

## Planned properties

| Property | How |
| --- | --- |
| No stampede | Per-key load coalescing (singleflight) plus TTL jitter (RF-04, RF-05) |
| Redis down ≠ app down | Fail-open reads with short, independent L2 timeouts and a `Guard` hook for circuit breaking (RNF-02, RNF-03, RNF-04) |
| No cross-user leakage | Mandatory namespace and owner-scoped keys, with an explicit cross-user test (RS-01, RS-02) |
| L2 data is untrusted | Size-limited, version-checked envelope; a bad entry is a miss, never a panic (RS-06) |
| Invalidation without `SCAN` | Tag generation keys, plus a Bus that evicts other instances' L1 (RF-10, RF-11) |
| Secrets are never cached | `NoCache` marker refused by `Set` (RS-04) |
| No dependencies in the core | Standard library only; Redis lives in a separate module (RNF-01, [ADR-0001](docs/adr/0001-module-structure-and-dependency-policy.md)) |

### And what it will not do

- **Be a source of truth.** Nothing may depend on the cache for correctness.
- **Close the cache-aside race.** A reader can write back a value that was
  invalidated while it was loading; the window is bounded by TTL, not closed.
- **Protect you from a key without the owner in it.** cistern forces a
  namespace; your `KeyFunc` has to encode the user.
- **HTTP caching, write-behind, Redis Cluster** — out of scope for v0.x.

Full residual analysis: [`docs/THREAT-MODEL.md`](docs/THREAT-MODEL.md) §7.

## Design shape

```
cistern/             core — Cache[K,V], options, GetOrLoad, tags   (stdlib only)
├── memory/          L1: LRU with entry and byte limits, TTL
├── codec/           Codec interface + JSON
├── envelope/        versioned storage format
├── bus/             Bus interface + in-process implementation
├── cisterntest/     conformance suite for Store and Bus
└── internal/        singleflight

cistern/redisstore/  separate module — Redis L2 + Pub/Sub Bus
cistern/examples/    not published — task-api decorator, bastion, crier
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
| **C7** Hooks & examples | Hooks, `examples` module, README |
| **C8** Validation & release | sapper scenarios, clean-session audit, `v0.1.0` |

In parallel from C7, the `task-api` track: **T0** baseline with sapper → **T1**
endpoint choice → **T2** `CachedTaskRepository` → **T3** re-measure and
release.

## Ecosystem

| Library | Concern |
| --- | --- |
| [`moat`](https://github.com/JonasBorgesLM/moat) | HTTP security middleware |
| [`crier`](https://github.com/JonasBorgesLM/crier) | Log control and export |
| [`cairn`](https://github.com/JonasBorgesLM/cairn) | Short links |
| **cistern** | Two-level caching |

## License

[MIT](LICENSE).
