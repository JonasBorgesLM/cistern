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
| [0002](0002-fail-open-reads-fail-loud-invalidation.md) | Fail-open reads, fail-loud invalidation | Accepted | — |
| [0003](0003-cache-aside-default-write-through-deferred.md) | Cache-aside as the default; write-through deferred | Accepted — reopening criterion recorded | — |
| [0004](0004-l1-l2-composition-and-ttl-ordering.md) | L1+L2 composition with L1 TTL ≤ L2 TTL | Accepted | — |
| [0005](0005-tag-invalidation-with-generation-counters.md) | Tag invalidation with generation counters checked on read | Accepted | — |
| [0006](0006-best-effort-invalidation-bus.md) | The Bus is best-effort and carries invalidation only | Accepted | — |
| [0007](0007-self-contained-singleflight.md) | Self-contained singleflight under `internal/` | Accepted | — |
| [0008](0008-mandatory-scope-and-physical-key-format.md) | Mandatory scope and the physical key format | Accepted | [0005](0005-tag-invalidation-with-generation-counters.md) |
| [0009](0009-versioned-envelope.md) | Versioned envelope prepared for stale-while-revalidate | Accepted | [0005](0005-tag-invalidation-with-generation-counters.md) |
| [0010](0010-lru-eviction-is-safe-for-cached-values.md) | LRU eviction is safe for cached values — with one exception | Accepted | [0005](0005-tag-invalidation-with-generation-counters.md) |
| [0011](0011-cache-aside-race.md) | The cache-aside race — what is closed, and what is accepted | Accepted — reopening criterion recorded | — |
| [0012](0012-redis-cluster-out-of-scope.md) | Redis Cluster out of the initial scope | Accepted — reopening criterion recorded | — |
| [0013](0013-minimum-go-version-per-module.md) | Minimum Go version per module | Accepted | — |
| [0014](0014-l1-stores-encoded-bytes.md) | L1 stores encoded bytes behind the same Store as L2 | Accepted — reopening criterion recorded | — |
| [0015](0015-observability-through-hooks.md) | Observability through hooks, with keys withheld by default | Accepted | — |

ADR-0001 and ADR-0013 are the two decisions gated by phase C0
(`REQUIREMENTS.md` §13): the module layout and the Go version floor have to
exist before there is any code to build with either. ADR-0002, 0003, 0004, 0008 and
0010 open C1. ADR-0010 is the one to read with care: it argues that LRU is safe
here despite `moat`'s M-1, and names the one key in L2 for which that is false.
ADR-0014 was not in the original plan: it came out of the C1 API review (#13).

## Planned

Numbered and titled in `REQUIREMENTS.md` §12 so the numbering stays stable
when each is written — before the code of the phase that needs it, not now.

| ADR | Title | Written during |
| --- | --- | --- |

## Open questions

Listed here rather than left implicit, because an undecided question that
looks decided is the one that gets implemented by accident. From
`REQUIREMENTS.md` §16:

1. **Which `task-api` endpoint goes first** — depends on the T0 baseline
   result, not decidable now.

None of these blocks C0.
