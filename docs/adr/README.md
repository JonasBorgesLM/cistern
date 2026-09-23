# Architecture Decision Records

Every structural decision in `cistern` is recorded here. **An ADR is never
edited to reflect a later change of mind.** It is amended in place with a
section naming what superseded which part, so the reasoning that was current
at the time stays readable. The convention is inherited from `crier` and
`cairn`.

## Index

| ADR | Title | Status | Amended by |
| --- | --- | --- | --- |
| [0001](0001-module-structure-and-dependency-policy.md) | Multi-module structure and dependency-free core | Accepted | — |
| [0013](0013-minimum-go-version-per-module.md) | Minimum Go version per module | Accepted | — |

ADR-0001 and ADR-0013 are the two decisions gated by phase C0
(`REQUIREMENTS.md` §13): the module layout and the Go version floor have to
exist before there is any code to build with either.

## Planned

Numbered and titled in `REQUIREMENTS.md` §12 so the numbering stays stable
when each is written — before the code of the phase that needs it, not now.

| ADR | Title | Written during |
| --- | --- | --- |
| 0002 | Fail-open in the cache vs. fail-closed in `moat`'s rate limiter | C1 |
| 0003 | Cache-aside as the default; write-through deferred | C1 |
| 0004 | L1+L2 composition with L1 TTL ≤ L2 TTL | C1 |
| 0005 | Tag invalidation via generation keys (no `SCAN`/`KEYS`) | C6 |
| 0006 | Bus is best-effort and carries invalidation only | C6 |
| 0007 | Self-contained singleflight under `internal/` | C2 |
| 0008 | Mandatory key scope | C1 |
| 0009 | Versioned envelope prepared for stale-while-revalidate | C3 |
| 0010 | LRU eviction is safe here (contrast with `moat`'s M-1) | C1 |
| 0011 | Cache-aside race: accepted risk and mitigations | C6 |
| 0012 | Redis Cluster out of the initial scope | C4 |

## Open questions

Listed here rather than left implicit, because an undecided question that
looks decided is the one that gets implemented by accident. From
`REQUIREMENTS.md` §16:

1. **Exact physical key format** — separator, and the length threshold above
   which hashing (RS-03) kicks in opt-in. Decided by C0–C1.
2. **Final public API naming** — a synonym-free review, as done in `moat`.
   Decided by C1.
3. **Which `task-api` endpoint goes first** — depends on the T0 baseline
   result, not decidable now.

None of these blocks C0.
