# CLAUDE.md

Guidance for Claude Code when working in this repository.

The general engineering rules are in `~/.claude/CLAUDE.md` and already loaded.
**This file carries only what is true of cistern**, and where it repeats a
global rule it is because this repository makes it stricter.

## What cistern is

A Go library for two-level cache-aside caching: an in-process L1 (LRU) and an
optional shared L2 (Redis), with load coalescing, negative caching, and
invalidation by key, tag (generation keys) and event (Bus). It is **not** a
source of truth, not an HTTP cache, and never caches secrets.

Requirements live in [`REQUIREMENTS.md`](REQUIREMENTS.md) and are cited by id
(`RF-05`, `RS-02`, `RNF-02`…). Threats live in
[`docs/THREAT-MODEL.md`](docs/THREAT-MODEL.md) as `T-nn`. Decisions live in
[`docs/adr/`](docs/adr/README.md).

## Current phase

**C2 done; C3 (encoding & validation) next.** `Cache[K,V]` (`Get`,
`GetOrLoad`, `Set`, `Delete`, options), `internal/singleflight`, `memory`,
`codec` and the `Store` interface exist. Nothing is released. Work
is tracked on the [project board](https://github.com/users/JonasBorgesLM/projects/6), grouped
C0–C8 plus the task-api track T0–T3 (`REQUIREMENTS.md` §13). Do not write
implementation code without an issue that says to, and write the phase's
planned ADR (`docs/adr/README.md`, "Planned") before its code.

## Repository layout

**Multi-module** (ADR-0001).

| Path | Module | Notes |
| --- | --- | --- |
| `.` | core | **No `require` at all** — CI's `dependency-policy` enforces it |
| `redisstore/` | Redis L2 + Pub/Sub Bus | go-redis; testcontainers (test only). Arrives in C4 |
| `examples/` | not published | task-api decorator, bastion, crier. Arrives in C7 |

`memory/`, `codec/`, `envelope/`, `bus/`, `cisterntest/` and `internal/` will be
packages of the core module, not modules.

## Invariants — do not break these

- **The core imports nothing outside the standard library** (RNF-01), and never
  a submodule. Singleflight is our own, under `internal/` (ADR-0007, planned).
- **Reads are fail-open** (RNF-02). An L2 error or timeout is a miss plus an
  error hook, never an error returned to the caller of a read.
- **No scopeless cache** (RS-01). A constructor without a namespace is an error.
- **L2 data is untrusted** (RS-06). Decoding never panics and never
  instantiates arbitrary types.
- **Hooks never receive the full key by default** (RS-08).
- **Constructors return errors, never panic** (RF-16).
- **L1 TTL ≤ L2 TTL**, validated at construction (RF-08).

## Commands

Per module — a green build in one says nothing about the other.

```bash
go work init . ./redisstore          # once; go.work is not committed

for m in . redisstore; do
  (cd "$m" && go build ./... && go vet ./... && go test -race ./...)
done

golangci-lint run ./...                # at the root and in redisstore/
gosec -tests -exclude-generated ./...
govulncheck ./...
./.github/scripts/check-docs.sh
```

## Conventions

- Git flow, commit format and scopes: [`CONTRIBUTING.md`](CONTRIBUTING.md).
  PRs target `develop`, never `main`.
- Every exported identifier has a doc comment; every public package has an
  `ExampleXxx` (RNF-08).
- Every `RS-` test carries a negative control: remove the protection, watch it
  fail, restore it, note it in a comment above the test.
- L2 integration tests use testcontainers against a real Redis, never
  miniredis (RNF-07).
- ADRs are never rewritten — amend or supersede. CI enforces it.
