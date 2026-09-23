# ADR-0001: Multi-module structure and dependency-free core

## Status
Accepted

## Context
`cistern` needs a Redis-backed L2, and the obvious client is `go-redis`.
Putting it in the root module would put it in the dependency graph of every
consumer, including those who run L1-only or bring their own `Store` — the
same problem `moat` solved by splitting out `redisstore` and `crier` solved
with one module per exporter (RF-14, RNF-01).

Unlike `cairn`, which took a first-party dependency on `moat` for
`secret.Value` (`cairn` ADR-0007), `cistern`'s core has no first-party
dependency to take: `NoCache` (RS-04) is a marker interface the core defines
itself, and the documentation naming `moat/secret.Value` as an example of a
type that must not be cached lives in the `examples` module, not in the core.
That makes "zero dependencies" true as a fact for `cistern`'s core, not just a
narrower true statement the way it is for `cairn`.

## Decision
Three modules, one published as a library pair and one illustrative only:

| Path | Module | Dependencies |
| --- | --- | --- |
| `.` | `github.com/JonasBorgesLM/cistern` | none (RNF-01) |
| `redisstore/` | `github.com/JonasBorgesLM/cistern/redisstore` | go-redis; testcontainers (test only) |
| `examples/` | not a published module | task-api decorator, `bastion`, `crier`, integration tests |

The dependency policy is: **the core module admits no dependencies at all**,
first-party or third-party. CI's `dependency-policy` job enforces this by
failing on any non-empty `require` block in the core `go.mod` — a policy that
is not checked is a preference.

`memory/`, `codec/`, `envelope/`, `bus/` and `cisterntest/` are packages of the
core module, not modules. They add no dependencies beyond the standard
library, so a separate module would buy nothing and cost a tag.

Go floor: **1.24** in the core (ADR-0013), matching `moat` and `cairn` core.
`redisstore` and `examples` declare whatever their dependencies impose, and
the `go.mod` records that as an imposition, not an endorsement — the wording
`cairn`'s ADR-0001 also uses.

## Consequences
- A consumer with their own `Store` never sees `go-redis`.
- `examples` is never `go get`-able as a dependency and is excluded from the
  module's release process (see `RELEASING.md`); it exists to be read and run
  from a clone, not imported.
- Two tag prefixes to maintain once `redisstore` ships: `vX.Y.Z` and
  `redisstore/vX.Y.Z`. A green build in one module says nothing about the
  other; every command in CI and in `CLAUDE.md` runs per module.
- A local `go.work` is used for development and is not committed, following
  `moat` and `cairn`.
- Because the core takes no dependency at all, there is no equivalent of
  `cairn`'s ADR-0007 reopening criterion to track here.

## Open
Whether `examples` should later be split so that just the `task-api` decorator
pattern is copy-pasteable without pulling in the `bastion`/`crier` wiring.
Not decided now; revisit if C7 makes the module unwieldy.
