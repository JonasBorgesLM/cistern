# ADR-0013: Minimum Go version per module

## Status
Accepted

## Context
A library's `go` directive is a compatibility promise about who may import it:
raising it strands consumers without patching anything for them. `moat` and
`cairn` both hold their core floor at Go 1.24 and treat raising it as a
decision that needs its own reasoning, not a routine bump. `cistern` has the
same shape of promise to make, and CI (`ci.yml`) needs a concrete version to
build and test against before any code exists.

`redisstore` and `examples` are not bound by the same promise: their floor is
whatever `go-redis`, `testcontainers-go` and (for `examples`) `bastion`/`crier`
actually require, and recording that as a deliberate choice would misstate it.

## Decision
- **Core (`.`): Go 1.24.** This is the version RNF-01 promises importers, and
  the version CI builds and tests the core against.
- **`redisstore/` and `examples/`: whatever their dependencies impose**,
  recorded in each module's `go.mod` as an imposition rather than a choice —
  the same wording `cairn`'s ADR-0001 uses for the same situation.
- `govulncheck` additionally runs the core against the newest supported Go
  toolchain (`GO_SUPPORTED_VERSION` in `ci.yml`), not the floor: a library
  verified only at its floor is scanned against a standard library that may no
  longer receive fixes, which reports findings no change to this repository
  can resolve.
- Raising the core's floor below 1.24 is not considered; raising it above 1.24
  requires a superseding ADR naming the concrete language feature or security
  fix that makes 1.24 non-viable.

## Consequences
- CI's `build` job sets up each module from the `go` directive in its own
  `go.mod` (`go-version-file`) with `GOTOOLCHAIN=local`, so a floor violation
  fails instead of silently downloading a newer toolchain. A dependency bump
  that raises a satellite's floor is therefore visible in that module's diff,
  not inherited from a shared pin.
- The core builds **without** a workspace: it requires nothing, and a
  workspace would make it borrow the highest floor among the satellites.
- `lint` runs at the toolchain golangci-lint was built with, and `govulncheck`
  at the newest stable one. Neither is a floor check, and neither claims to be.
- A `go.work` used for local development may specify a newer toolchain version
  than any individual module's floor (`go.work`'s own `go` line, not
  committed — ADR-0001); that is a development convenience and not a promise
  to consumers.

## Open
None. This ADR is narrow by design; it exists to satisfy C0's exit criterion
and to give CI a floor to build against, not to anticipate every future
version decision.
