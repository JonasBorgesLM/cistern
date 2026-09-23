# Audit brief — cistern v0.1

This is the brief for the pre-release audit that `REQUIREMENTS.md` §15 makes
a release criterion: **performed in a clean session, by someone who did not
write the code.** It was the item left undone in `moat`. Issue #68.

If you are the auditor: you have none of the context the code was written in,
and that is the point. Read this brief, then form your own view from the
requirements, the code and the tests. **Trust nothing you have not checked** —
including this brief, every "Negative control: verified failing …" comment in
the tests, every PR description and every claim in the README and ADRs. The
author made mistakes that only surfaced when something was run; assume there
are more.

---

## 1. What you are auditing

A Go cache library: an in-process L1 (`memory`), a Redis L2 (`redisstore`,
separate module), load coalescing, negative caching, tag invalidation through
generation counters, a cross-instance Bus, hooks.

| Read first | For |
| --- | --- |
| [`REQUIREMENTS.md`](../REQUIREMENTS.md) | `RF-`, `RS-`, `RNF-` ids — the contract |
| [`docs/THREAT-MODEL.md`](THREAT-MODEL.md) | threats T-01 … T-14 and the residuals claimed |
| [`docs/adr/`](adr/README.md) | the decisions and their reasoning |
| [`SECURITY.md`](../SECURITY.md), [`redisstore/README.md`](../redisstore/README.md) | what operators are told |

In scope: the core module, `redisstore`, `.github/workflows/` and
`.github/scripts/`. The `examples` module is not released, but two of its
tests are the evidence for RS-02 and RS-04 — audit those two claims.

## 2. What to do

### 2.1 A negative control for every security requirement

For each of **RS-01 through RS-11**: find the code that implements it and the
test that guards it, then **remove the protection, run the test, watch it
fail, restore, and confirm `git diff` is empty.** A test that passes with its
protection removed is a finding. A requirement with no test is a finding. If a
requirement is documentation only (RS-09, RS-10, RS-11), check that what is
documented is accurate and, where the repository claims it is tested, that it
is.

Record every row, run or not, in `docs/audit-log.md`, in this shape (the same
as `cairn`'s audit log):

| RS | Requirement | Test | Control |
| --- | --- | --- | --- |
| RS-01 | … | `TestXxx` (file) | what you removed, what failed, "Restored." |

### 2.2 The functional contract

Every `RF-` marked MVP in `REQUIREMENTS.md` §5 is implemented, tested, and has
an Example in its package (§15). Say which are not.

### 2.3 The threat model

For each threat T-01 … T-14: is the mitigation real in the code, and is the
residual stated honestly? A residual described as smaller than it is, is a
finding. Look in particular at what an attacker who can **write to Redis** or
**publish on the Bus** can do (T-05, T-06), and at cross-user isolation
(T-01).

### 2.4 Failure behaviour

ADR-0002 says reads fail open and invalidations fail loud. Check every read
path (`Get`, `GetOrLoad`, the tagged path, L2→L1 backfill, Bus events) and
every invalidation path (`Delete`, `InvalidateTag`, `Set`). A read that can
return a cache error to the caller, or an invalidation that can fail silently,
is a finding.

### 2.5 Concurrency

`go test -race` is in CI, but races that the tests never provoke are not
caught by it. Read the shared state: the singleflight group, the local
generation copy, the `memory` store and its eviction reporting, Bus
subscriptions, `Close`.

### 2.6 Untrusted input

L2 bytes (the envelope) and Bus messages are attacker-controllable. Run the
fuzz targets for longer than CI does:

```bash
go test -run '^$' -fuzz FuzzDecode -fuzztime 5m ./internal/envelope/
go test -run '^$' -fuzz FuzzDecode -fuzztime 5m ./bus/
```

### 2.7 Supply chain and release

`go.mod` of each module, `govulncheck`, pinned tool versions in the
workflows, and the guards in `release.yml`: would it refuse a release it
should refuse — an unsigned tag, a satellite requiring an unreleased core, a
core that gained a dependency? You cannot push a tag to test it; reason from
the workflow and run its scripts.

### 2.8 Areas that already had defects

Scrutinise these more, not less — each was wrong once:

- `internal/singleflight`: joining an expired flight;
- `redisstore`: where the per-call timeout sits relative to the `Guard` (#103);
- `release.yml`: its pseudo-version and signer checks;
- the ADR-0005 local copy of generations, whose own expiry was found not to be
  load-bearing.

## 3. Rules

- **Do not change production code during the audit** except to run a control,
  and restore it before the next step.
- **A finding** goes into `docs/audit-log.md` and into a GitHub issue labelled
  `audit`, with a severity — CRITICAL, HIGH, MEDIUM or LOW — and the evidence:
  file, line, and the concrete path that reaches the problem. Do not report
  speculation; if you cannot show the path, say it is a question, not a
  finding.
- **Say what you did not cover.** A scope note at the end of the log.
- Deliver the log as a pull request to `develop`, following
  [`CONTRIBUTING.md`](../CONTRIBUTING.md). Do not fix findings in the same PR:
  each fix is its own PR, reviewed as usual, or the risk is accepted in an ADR
  (`REQUIREMENTS.md` §13, C8).

## 4. Commands

```bash
go work init . ./redisstore ./examples && go work edit -go=1.25.0

go test -race ./...                                   # core, at the root
(cd redisstore && go test -race ./... && go test -tags=integration -race ./...)   # needs Docker
(cd examples && go test -race ./...)
golangci-lint run ./...
gosec -tests -exclude-generated ./...
govulncheck ./...
./.github/scripts/check-docs.sh
```

The Claude Code commands `/security-review` and `/code-review` are useful
second passes; they do not replace the negative controls in §2.1.
