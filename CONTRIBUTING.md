# Contributing to cistern

## Status

`cistern` is pre-implementation. Work is tracked on the
[project board](https://github.com/users/JonasBorgesLM/projects/6),
grouped into phases C0 through C8 plus the `task-api` integration track T0–T3
([`REQUIREMENTS.md`](REQUIREMENTS.md) §13).

---

## Git flow

### `develop` and `main`

**`develop` is where work lands. `main` is what has been released.**

- Every pull request targets `develop`. Nothing targets `main` directly.
- At each release, `develop` merges into `main` and the tags are cut there
  (see [`RELEASING.md`](RELEASING.md)).
- **Until the first tag exists**, `main` tracks `develop`: there is no released
  state for it to hold yet, and a default branch showing an empty project is
  worse than one showing unreleased work. Both branches are protected, so this
  is a `develop` → `main` pull request, merged once `CI OK` is green.
- Release commits are made on `main`, and **`main` is merged back into
  `develop` when the release finishes** — otherwise the branches diverge in
  the exact files the release changed.

### Branches

```
feat/<short-name>        feat/memory-lru
fix/<short-name>         fix/negative-cache-ttl
docs/adr-<nnnn>          docs/adr-0005
ci/<short-name>          ci/pin-gosec
```

Branch from `develop`. One branch, one subject.

### Stacked pull requests

While a phase branch is open, the next phase's branch may be based on it so its
PR shows only its own diff. **Retarget the dependent PR to `develop` before
deleting the base branch** — deleting it first closes the dependent PR, and a
closed PR whose base is gone can be neither retargeted nor reopened.

```bash
gh pr merge 12 --merge
gh pr edit 13 --base develop           # retarget FIRST
git push origin --delete feat/c1-public-api
```

### Merging

- Squash a branch whose intermediate commits are noise; use a merge commit for
  one whose history is worth keeping. Either way the resulting subject is a
  Conventional Commit, which CI checks on the PR title as well as the commits.
- Delete the branch after merge, unless another PR is based on it.

---

## Commits

**[Conventional Commits](https://www.conventionalcommits.org)**, enforced by
the `commits` job in CI on every commit in a pull request and on the PR title.

```
<type>(<scope>)!: <subject>

<body: why, not what>

Refs RS-02
Closes #12
```

| | |
| --- | --- |
| **Types** | `feat` `fix` `docs` `test` `refactor` `perf` `build` `ci` `chore` `revert` |
| **Scopes** | `cistern` `memory` `codec` `envelope` `bus` `cisterntest` `redisstore` `examples` `adr` `docs` `deps` `ci` `security` |
| **Subject** | imperative, ≤ 72 characters, no trailing period |
| **Breaking** | `!` before the colon **and** a `BREAKING CHANGE:` footer |

```
feat(memory): evict by byte limit as well as entry count
fix(redisstore): treat a decode failure as a miss
docs(adr): record the generation-key decision
ci: pin gosec to an exact version
```

**Explain why in the body** and cite the requirement or ADR. **One subject per
commit, staged explicitly** — no `git add -A` when the tree holds more than one
subject.

---

## Documentation is part of the change

Enforced mechanically:

1. **Exported identifiers carry doc comments** (`revive`'s `exported` rule in
   [`.golangci.yml`](.golangci.yml)).
2. **Every cited requirement and threat id exists** —
   [`check-docs.sh`](.github/scripts/check-docs.sh). Cross-project citations
   are written qualified (`moat/RNF-01`), because unqualified they would be
   checked against cistern's numbering.
3. **Every relative link between documents resolves.**
4. **ADRs are indexed** in [`docs/adr/README.md`](docs/adr/README.md).

```bash
./.github/scripts/check-docs.sh
```

### ADRs are never rewritten

A change of mind is **a new ADR that supersedes the old one**, or an
`## Amendment` section appended to it. CI enforces the mechanical version: an
existing ADR may gain lines and never lose them. A genuine typo fix is the one
exception — label the PR `adr-typo`.

Write the ADR **before** the code. A question listed as open in
[`docs/adr/README.md`](docs/adr/README.md) must not be resolved silently by an
implementation.

---

## Before you write code

1. Find the requirement id (`RF-`, `RS-`, `RNF-`) your change implements. If
   there is none, the change needs a requirement first.
2. If the change is structural, write the ADR first.
3. If it touches an `RS-`, plan the negative control before the test.

## Per-module commands

Multi-module repository (ADR-0001). A green build in one module says nothing
about the other.

```bash
go work init . ./redisstore          # once; go.work is not committed

for m in . redisstore; do
  (cd "$m" && go build ./... && go vet ./... && go test -race ./...)
done

golangci-lint run ./...
gosec -tests -exclude-generated ./...
govulncheck ./...
./.github/scripts/check-docs.sh
```

`go.work` is deliberately not committed: it changes how modules resolve for
everyone who clones the repository, and it would make the
`satellite-resolution` CI job prove nothing.

## Testing rules

- Table-driven, `t.Run` subtests, asserting the specific behaviour.
- Every public package has `ExampleXxx` functions (RNF-08).
- Concurrency is tested under `-race` (RNF-05); coalescing is tested by
  counting loader calls, not by timing.
- L2 integration tests use testcontainers against a real Redis (RNF-07).
- **Every `RS-` test carries a negative control**, noted above the test:

  ```go
  // RS-01: a cache without a namespace must be rejected.
  // Negative control: verified failing with the namespace check removed.
  ```

- **A negative assertion is satisfied by a tool that never ran.** Assert the
  expected answer, not the absence of a wrong one.

## Reviewing

1. Which requirement does this implement, and does the code match its wording?
2. Which threat does it touch, and is the residual still what
   [`docs/THREAT-MODEL.md`](docs/THREAT-MODEL.md) says?
3. Has the `RS-` test been **seen to fail**?
4. Does it break an invariant in [`CLAUDE.md`](CLAUDE.md)?
5. Does it add a dependency to the core module? (The answer is no.)

## Reporting a vulnerability

See [`SECURITY.md`](SECURITY.md). Do not open a public issue.
