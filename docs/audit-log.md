# Pre-release audit — cistern v0.1

Issue #68, following [`audit-brief.md`](audit-brief.md). Run on 2026-09-23
against `develop` at `95d8e02`.

**Who ran it.** This audit was done by Claude Code (Claude Opus 5.5) in a new
session with none of the context the code was written in: no conversation
history and no notes, only the repository. That meets the brief's "clean
session" requirement. It is **not** an independent human audit, and the code
may have been written by the same model family in earlier sessions. Read the
line in [`SECURITY.md`](../SECURITY.md) that says the library "has not been
reviewed by an independent security auditor" as still true.

**Method.** Every negative control below was run in this session. The
protection was removed or neutralised, the named tests were run and watched
failing, then the file was restored and `git diff --quiet` confirmed a clean
tree before the next step. The "Negative control: verified failing …" comments
already in the tests were read, but they were not accepted as evidence. Two
controls failed to *build* on the first attempt: the control-character check
in RS-03 and the local-copy expiry in §2.8. A build failure is not a test
failure, so both were re-run with the check neutralised instead of deleted, and
only the re-run is recorded. Proofs of concept were temporary `zz_audit_*`
test files, deleted after running. None of them is in this PR.

**Environment.** macOS arm64, Go 1.27.1 for build and test (the modules
declare 1.24 and 1.25; the floors themselves were not exercised locally),
golangci-lint v2.12.2 run with the `go1.26.5` toolchain as CI pins, gosec,
govulncheck, Docker 27.5.1, `redis:7.4-alpine` (the image the suite pins).

## Summary of findings

| # | Severity | Finding | Issue |
| --- | --- | --- | --- |
| A-01 | **HIGH** | Coalesced `GetOrLoad` callers skip the tag-generation check. The owner-in-tag barrier of T-01 fails under concurrency, and a read issued after `InvalidateTag` can return the pre-invalidation value | #112 |
| A-02 | MEDIUM | The local generation copy is re-populated after `InvalidateTag` drops it. The invalidating instance then serves retired entries for up to one L1 TTL | #113 |
| A-03 | MEDIUM | `maxmemory-policy` is instance-wide, but T-09, §5 and RS-10 recommend a separate *logical DB* | #114 |
| A-04 | MEDIUM | `release.yml` checks the tag signature against `allowed_signers` taken from the tagged commit, and does not require the commit to be on `main` | #115 |
| A-05 | MEDIUM | A `replace` directive bypasses the "satellite requires a released core" guard | #116 |
| A-06 | MEDIUM | `New` with a Redis Bus fails, as `ErrInvalidConfig`, while Redis is down, so a Redis outage blocks startup (T-12) | #117 |
| A-07 | LOW | The RS-08 test passes with `OnLoad`, `OnCoalesced` or (in core) `OnError` leaking the key | #118 |
| A-08 | LOW | Bus key events are not validated by the cache (ADR-0006), and the tag-event validation has no test that fails | #119 |
| A-09 | LOW | The RS-09 tests accept a wider ACL than the one documented: channel widening, and permissions appended to the README line | #120 |
| A-10 | LOW | Data race in `redisstore.Bus`'s unsubscribe when `Close` is called concurrently | #121 |
| A-11 | LOW | MVP RFs with no Example: `GetOrLoad`, `TTL`, `WithNegativeTTL`, tags, `WithBus` | #122 |

No finding is fixed in this PR (brief §3). Each one needs its own PR, or an ADR
that accepts the risk (`REQUIREMENTS.md` §13, C8).

## 2.1 Negative controls, RS-01 … RS-11

| RS | Requirement | Test | Control |
| --- | --- | --- | --- |
| RS-01 | Mandatory scope | `TestNewValidatesConfiguration` (cache_test.go); `TestNamespacesDoNotShareEntries`, `TestPhysicalKeyLayout` (cache_test.go) | (a) The `namespacePattern` case removed from `validate`. Failed: the empty, separator, uppercase and too-long subtests. Restored. (b) The namespace removed from the physical-key prefix. Failed: `TestNamespacesDoNotShareEntries`, `TestPhysicalKeyLayout` and 9 tests that hard-code the key. Restored. |
| RS-02 | Isolation by owner | `TestOneUserNeverSeesAnothersCachedList` (examples/taskapi/taskapi_test.go) | (a) Owner removed from the key only: **passes**, as the test comment claims, because the tag barrier holds for sequential reads. (b) Owner removed from key and tag: failed, "user 43 received [private to 42]". Restored. **A-01 (#112):** with the owner in the tag only, a *concurrent* request crosses users through a coalesced load, a path this test does not exercise. |
| RS-03 | Key validation | `TestInvalidKeysAreRefusedOnEveryOperation`, `TestKeyHashing`, `TestKeyHashingDoesNotBypassTheOtherChecks` (keys_test.go); `TestTagConfigurationAndValidation` (tags_test.go) | Control characters (`false && unicode.IsControl`): failed on NUL, newline, DEL and NEL, plus the hashing-bypass test and two tag subtests. UTF-8 check removed: failed on invalid UTF-8. Length limit bypassed: failed on the >256 B case and `TestKeyHashing`. Empty check removed: failed on empty. All restored. |
| RS-04 | Never cache secrets | `TestNewRefusesNoCacheValueTypes`, `TestNoCacheValuesAreRefusedBehindAnInterface` (nocache_test.go); `TestMarkedSecretsCannotBeCached` (examples/secrets) | Check in `New` disabled: failed on value receiver, pointer receiver and pointer type, and the examples test failed. Per-value check in `Set` disabled: failed. Per-value check in `GetOrLoad` disabled: failed ("a NoCache value reached the store"). `viaPointer` ignored: failed on pointer receiver. All restored. |
| RS-05 | Value size limit | `TestValueLimit` (envelope_integration_test.go); `TestDecodeRejectsMalformedData` (internal/envelope) | Limit disabled on `Set`: failed on `Set_over_the_limit`. Disabled on `GetOrLoad` population: failed, "an oversized loaded value reached the store". Disabled on decode: the oversized-entry subtest and the envelope `payload_over_the_limit` subtest failed. All restored. |
| RS-06 | L2 data is untrusted | `TestEntryFromAnotherCodecIsAMiss`, `TestMalformedOrForeignEntriesAreAMiss`, `TestUndecodableL2EntryIsNotBackfilled` (core); `TestDecodeRejectsMalformedData`, `FuzzDecode` (internal/envelope) | Codec-id check disabled: failed. Codec-length bound removed: **panic**, slice bounds `[15:12]`. Generations bound removed: **panic**, index out of range. Version check removed: 3 subtests failed. Flags check removed: failed. A decode error returned instead of a miss: 2 tests failed. All restored. Fuzzing: §2.6. |
| RS-07 | Bus carries invalidation only | `TestDecodeRejectsMalformedMessages` (bus); `TestBusIgnoresEventsItCannotTrust`, `TestBusIgnoresOtherNamespaces` (core); `TestMalformedMessagesDoNotStopTheBus` (redisstore, integration) | `DisallowUnknownFields` off: failed on `carries_a_value`. Key-prefix check in `onEvent` off: failed. Namespace check off: failed. Control-character check in `bus.check` off: failed. The subscriber loop made to `return` on a malformed message: failed, "the bus stopped delivering". All restored. **Tag validation in `onEvent` replaced by a raw prefix: whole core suite passes.** This is A-08 (#119). |
| RS-08 | Privacy in hooks | `TestHookEventsWithholdTheKeyByDefault` (hooks_test.go) | `hookKey` made to always return the key: failed. **`onLoad` or `onCoalesced` bypassing `hookKey`: all tests pass. `onError` bypassing it: only `examples/crierhooks` fails.** This is A-07 (#118). All restored. `memory.EvictEvent` has no key field, so there is nothing to remove. |
| RS-09 | AUTH/ACL, TLS, minimum ACL documented | `TestMinimalACLIsSufficient` (redisstore, integration); `TestREADMEDocumentsTheTestedACL` (redisstore) | Removing `+incr`, `+mget`, `+pexpire` or `+subscribe`: failed, with `NOPERM` each time. `~*`: failed (a `SET` outside the prefix was allowed). `+@all`: failed (`FLUSHALL` allowed). README narrowed (`+incr` removed, `~*`, `allkeys` inserted): failed. **`&*` and a permission appended to the README line: both pass.** This is A-09 (#120). All restored. TLS is not tested, and the README says so. |
| RS-10 | `maxmemory-policy` documented | Documentation. The T-14 safety claim it relies on is tested by `TestVanishedCounterNeverResurrectsARetiredEntry` (tags_test.go) and `cisterntest.RunTagStore` | Memory counters created at a fixed `1`: the core test and memory conformance failed. `redisstore` fresh generation fixed at `1`: 4 conformance subtests failed (integration). Both restored. **The documentation is inaccurate:** a logical DB does not isolate the policy. This is A-03 (#114), proven against a real Redis. |
| RS-11 | Threat model | Documentation | Present, and it covers assets, actors, mitigations and accepted risks. `check-docs.sh` confirms every `T-` citation resolves. Accuracy is reviewed in §2.3. Residuals understated for T-01, T-09, T-12 and T-13. |

## 2.2 Functional contract

Every MVP `RF-` (RF-01 … RF-16) is implemented, and each has a test that
exercises it. Checked by reading the tests, not by counting id citations:
RF-02 in `cache_test.go` and `getorload_test.go`, RF-03 through `TTL(…)` in
`getorload_test.go` and `cache_test.go`, RF-09 in
`TestDeleteEvictsBothLevelsAndFailsLoud`, RF-13 in `codec`, RF-16 in the
`New` validation tables. The core's statement coverage is 90.7%, against a
floor of 85% (`coverage-floor.sh`, `-coverpkg=./...`).

§15 also asks for Examples. RNF-08 (one per public package) holds. The core
package has **no Example** for `GetOrLoad` (RF-01, RF-05), `TTL` (RF-03),
`WithNegativeTTL` (RF-06), `WithTags`/`InvalidateTag` (RF-10) or `WithBus`
(RF-11/RF-12): A-11 (#122). RF-07 and RF-08 are shown by `redisstore`'s
`ExampleNew`.

## 2.3 Threat model

| Threat | Mitigation real in code? | Residual stated honestly? |
| --- | --- | --- |
| T-01 | Yes for the key (RS-01, RS-02). | **No.** "Either one alone keeps users apart" fails for coalesced callers: A-01 (#112). |
| T-02 | Yes. The kinds `k:`/`h:` and the `-`/`g` components keep keys, hashes and counters apart. | Yes for keys. Bus key events bypass the cache's validation: A-08 (#119). |
| T-03 | Yes (RS-04). | Yes. See the observations for containers. |
| T-04 | Yes (RS-05 on the way in and out, L1 limits). | Yes. See the observations for the decode-side limit. |
| T-05 | Yes. Fuzzed (§2.6), the codec is never chosen by the data, and a malformed entry is a miss plus a hook. | Yes. A Redis writer can poison values and counters (setting a counter back resurrects retired entries), and §7 says Redis write access equals control over what is served. |
| T-06 | Yes. Events carry no value, and receivers only drop state. | Yes. A forged key event can delete any L1 entry under the namespace prefix, and a forged tag event bumps counters in L1-only mode. Both cost only misses. |
| T-07 | Yes (RS-08). | Yes. The tests are incomplete: A-07 (#118). |
| T-08 | Delegated to the client, as documented. | Yes. TLS is untested, and the README says so. |
| T-09 | Documentation. | **No.** The "logical DB" wording reintroduces the risk: A-03 (#114). |
| T-10 | Yes, per process. | Yes. |
| T-11 | Yes (RF-06). | Yes. |
| T-12 | Yes on the read path (§2.4). | **No.** A Redis outage at startup fails `New` when a Bus is configured: A-06 (#117). |
| T-13 | Yes for stored entries. | **No.** A reader can join a pre-invalidation load (A-01, #112), and the local copy can be re-populated after the drop (A-02, #113). |
| T-14 | Yes. The controls in RS-10 fail with fixed initial values. | Yes. |

**Redis writers and Bus publishers (T-05, T-06).** Checked by reading the code
and by the fuzzing in §2.6. A malformed envelope never panics and never reaches
the caller as an error. A forged Bus message can only drop local state or
advance a local counter. Nothing an attacker writes to Redis or publishes can
make a *read* return an error, and nothing published can make a replica serve a
value it did not already hold.

## 2.4 Failure behaviour

| Path | Behaviour | Verdict |
| --- | --- | --- |
| `Get`: L1 error, L2 error, malformed entry, undecodable payload | Hook plus miss (`readPlain`, `checked`, `served`). Only ctx, key and `ErrNotFound` errors are returned | fail-open ✓ |
| `GetOrLoad` | As `Get`. If the tag read fails, the value is loaded and returned but not cached. A failed population is hooked | fail-open ✓ |
| Tagged read | A `GetTagged` error is hooked and treated as a miss with no generations, so nothing is cached | fail-open ✓ |
| L2→L1 backfill | Its failure is hooked, and the TTL is clamped to the L2 entry's expiry | fail-open ✓ |
| Bus event | The local delete or bump error is hooked | fail-open ✓ |
| `Delete` | L1 is always evicted, and the L1, L2 and publish errors are joined and returned | fail-loud ✓ |
| `InvalidateTag` | The local copy is always dropped, and bump and publish errors are joined and returned | fail-loud ✓ (but see A-02) |
| `Set` | L2 first, then L1, with errors joined | fail-loud ✓ |

No read path was found that returns a cache error to its caller, and no
invalidation path that fails silently. Construction is where an outage turns
into an error: A-06 (#117).

## 2.5 Concurrency

- **Singleflight.** An expired flight is never joined (see §2.8). Panics reach
  the waiters. The shared payload is decoded per caller. But the flight key
  omits generations: A-01 (#112).
- **Local generation copy.** Its mutex is sound, but its *protocol* is not:
  `put` can reinstate generations older than a `drop` that happened after
  they were read (A-02, #113).
- **`memory`.** One mutex. Evictions are queued under the lock and reported
  after it is released. The `setLocked` eviction loop always terminates,
  because `maxEntries > 0` and an entry larger than `maxBytes` is refused
  first. Nothing found.
- **Bus subscriptions and `Close`.** `bus.Local` is sound (`sync.Once`, and
  handlers are copied under the lock). `redisstore.Bus`'s unsubscribe races
  under concurrent `Close`: A-10 (#121).
- `go test -race` is green for all three modules on `develop`, and for the
  integration suite.

## 2.6 Untrusted input: fuzzing

| Target | Duration | Executions | Result |
| --- | --- | --- | --- |
| `internal/envelope` `FuzzDecode` | 5 min | 78,949,637 | PASS, no crasher |
| `bus` `FuzzDecode` | 5 min | 35,477,855 | PASS, no crasher |

Both targets also check that whatever `Decode` accepts, `Encode` would have
produced. The envelope target checks byte-for-byte canonical form.

## 2.7 Supply chain and release

| Check | Result |
| --- | --- |
| `go.mod` core | No `require`. The release and CI guards were exercised: a real `require` (with `go.sum`) is refused, and an unresolvable one is refused as an error. A `tool` line without a `require` is not listed, but `go mod tidy` would add the `require`, which is then refused |
| `go.mod` redisstore | The core is pinned at a pseudo-version, which is expected before the first core tag. `released-version.sh` refuses the current pin and all three pseudo-version forms, and accepts `v0.1.0` and `v0.1.0-rc.1`. **A `replace` bypasses it:** A-05 (#116) |
| govulncheck | "No vulnerabilities found" in core, redisstore and examples |
| golangci-lint, gosec | 0 issues in all three modules (lint with `go1.26.5`, as CI pins it) |
| `check-docs.sh` | Passes |
| Tool pins | govulncheck, gosec, golangci-lint and gorelease are pinned to exact versions. Actions are pinned to major tags, not SHAs (see the observations) |
| Signed-tag gate | Refuses an unsigned tag and a tag signed by a key missing from `allowed_signers`. **But the file is read from the tagged commit, and nothing requires `main`:** A-04 (#115) |
| Core gained a dependency | Refused (see above) |

The release workflow was reasoned about and its shell run locally. It was not
run on GitHub.

## 2.8 Areas that already had defects

| Area | Control | Result |
| --- | --- | --- |
| `internal/singleflight`, joining an expired flight | `closed(c.expired)` check disabled | `TestHungLoadDoesNotBlockTheNextCaller` failed. Restored |
| `redisstore`, timeout relative to the `Guard` (#103) | Timeout moved outside `guard.Do` | `TestTimeoutIsTheOperationsNotTheCallers` and `examples/bastionguard` `TestOpenBreakerKeepsReadsOffRedis` failed. Restored |
| `release.yml`, pseudo-version and signer checks | Scripts and steps run locally | The checks work on their inputs, and both can be bypassed from outside those inputs: A-04 (#115), A-05 (#116) |
| ADR-0005 local copy, "expiry not load-bearing" | Expiry check neutralised (`false &&`) | The core suite passes, as the `gencache.go` comment says. The copy has a different, load-bearing flaw: A-02 (#113) |

## Observations: not findings

These were checked, and are recorded so nobody repeats the work. None has a
concrete harmful path, so none is filed as an issue.

- **`NoCache` and containers.** `Cache[string, []secrets.Credential]` is built
  even though `Credential` is marked, because a slice does not implement the
  interface. T-03's residual ("a type that does not implement `NoCache`")
  literally covers this. Naming containers explicitly in the `NoCache` godoc
  would save a consumer the surprise.
- **The decode-side limit bounds decoding, not transfer.** A planted oversized
  entry is read in full from Redis before `envelope.Decode` rejects it. The
  next `GetOrLoad` overwrites it, so there is no amplification unless the
  attacker keeps rewriting it, and that attacker already controls T-05.
- **The ACL denies `PING` and `CLIENT SETINFO`.** go-redis's Pub/Sub health
  check fills `ACL LOG` with `NOPERM` entries for idle subscribers. The
  subscription keeps working: same client id, 449/449 events delivered in 10 s
  (details in #120).
- **Workflow expressions in shell.** `release.yml` interpolates
  `${{ github.ref_name }}` into `run:` scripts. Git accepts tag names containing
  `'` and `;` (`git check-ref-format "refs/tags/v1';id;'"` succeeds), so a tag
  name can inject shell. Only someone who can already push tags can do this,
  and the job token grants nothing beyond that, so no escalation was shown.
  Passing it through `env:` is the conventional hardening.
- **Actions pinned by major tag.** The ci.yml header says "every tool is pinned
  to an exact version". Tools are. `actions/*` and
  `golangci/golangci-lint-action` are major tags, including in the release
  job, which holds `contents: write`.

## Scope note: what this audit did not cover

- **sapper scenarios 1–6** (§14) and any load or latency behaviour: #67.
- **TLS** to Redis, and **Sentinel** (`NewFailoverClient`). Neither was
  exercised.
- **The release workflow on GitHub.** Reasoned about and run locally only. In
  particular, whether `actions/checkout@v7` preserves the *annotated* tag
  object that `git verify-tag` needs was not verified.
- **The Go floors** (1.24 core, 1.25 redisstore). Tests ran on 1.27.1.
- **Benchmarks** (RNF-06) and **RNF-11** under a real multi-replica deployment
  with lost Pub/Sub messages.
- **The `examples` module** beyond the RS-02 and RS-04 evidence the brief asks
  for.
- **`/security-review` and `/code-review`.** The brief suggests them as second
  passes, and they were not run.
- **Repository settings** (branch protection, tag rulesets, private
  vulnerability reporting). Not inspected. RELEASING.md lists the last as a
  release checklist item.

---

## Re-audit (2026-09-23)

Second pass, also in a clean session with none of the context either the code
or the original audit was written in: no conversation history, no notes,
only the repository, `docs/audit-brief.md` and this log. Run against
`develop` at `57736ae` (the commit the original audit's findings, #112–#122,
were all fixed and merged onto — PRs #124–#133). The rules, format and
commands are still the brief's; this section adds the parts the instructions
for this pass asked for on top of it.

**Who ran it.** Claude Code (Claude Sonnet 5), clean session, same
disclosure as before: not an independent human audit, and the code may have
been written by the same model family. The line in `SECURITY.md` about no
independent security auditor is still true.

**Method.** Part 1 re-verified every one of #112–#122: read the issue and the
PR that closed it, ran the issue's original reproduction against current
`develop` (must be blocked), redid every negative control the PR or its
comments claim (`git diff --quiet` confirmed before and after each one), and
looked for regressions and for narrower windows the original fix might have
missed. Proofs of concept were temporary `zz_audit_*` files, deleted after
running; none is in this PR. Part 2 covers what the first audit's scope note
listed as not done: the pinned Go floors, `/security-review` and
`/code-review`, longer fuzzing, and TLS/Sentinel.

**Environment.** macOS arm64, Go 1.27.1 for the general suite;
`GOWORK=off GOTOOLCHAIN=go1.24.0` for the core's own floor and
`GOWORK=off GOTOOLCHAIN=go1.25.0` for redisstore's (§10 of this section).
golangci-lint v2.12.2 refuses to typecheck under Go 1.27.1's standard library
(`math/rand/v2`: "method must have no type parameters") — an environment
incompatibility, not a project defect — so it was run with
`GOTOOLCHAIN=go1.26.5`, the version CI actually pins, as the first audit also
did. gosec, govulncheck, Docker 27.5.1, `redis:7.4-alpine`.

### 1. Verification of #112–#122

| # | Repro blocked? | Controls redone? | Regression? | Notes |
| --- | --- | --- | --- | --- |
| #112 | Yes — `TestConcurrentCallsWithDifferentTagsDoNotShareALoad`, `TestReadAfterInvalidateTagDoesNotJoinAnOlderLoad` pass | Yes — `flightKey` reverted to pre-fix (`pk` alone); both tests fail with a caller receiving another owner's/older value. Restored | No, on the path the fix covers. **But see #134**: the fix left the `GetTagged`-error path open to the same cross-owner join | Deep dive below |
| #113 | Yes — `TestInFlightReadCannotRestoreRetiredGenerations` passes | Yes — `genCache.put`'s epoch/clears/drops check disabled; fails with a retired entry served as a hit. Restored | No | Read `gencache.go` in full for #135, below |
| #114 | N/A (documentation) | N/A | No — "separate instance" and the logical-DB-is-not-enough statement are consistent across `THREAT-MODEL.md`, `REQUIREMENTS.md`, `SECURITY.md`, `README.md`, `redisstore/README.md` and ADR-0010's amendment | — |
| #115 | Yes | Yes, both halves independently, in a scratch worktree: (a) a tag on a commit not reachable from `origin/main` is refused by the ancestry check alone; (b) the same tag's signature fails verification against `origin/main`'s `allowed_signers` alone (the attacker's key is not in it). Both reproduce the issue's exact PoC | No — `no-replace.sh` still passes on the real `go.mod`, `check-docs.sh` passes | RELEASING.md's stated limit ("a tag whose commit also rewrites the workflow skips these checks... a tag ruleset is the control") is honest, but **see #136**: that control does not exist yet |
| #116 | Yes — reproduced the exact PoC (`require` at `v0.1.0` + `replace => ../`) in a scratch worktree; `no-replace.sh` refuses it | Yes — same PoC | No — wired into both `release.yml` and CI's `satellite-resolution` job | — |
| #117 | Yes — `New`/`Subscribe` no longer error when Redis is unreachable | N/A (the fix is a removed error path, not a guard to disable) | **Yes, on the shutdown path — see #137** | Deep dive below |
| #118 | Yes — `TestHookEventsWithholdTheKeyByDefault` fires and checks all six kinds | Yes — `onLoad`, `onCoalesced` and `onError` each reverted to pass `key` directly, one at a time; the test catches all three. Restored | No | The 20ms-sleep synchronisation was stress-tested, see below; empirically robust but structurally worth hardening (Observations) |
| #119 | Yes | Yes (already covered by `TestBusIgnoresEventsItCannotTrust`'s control table) | No | Extra probes below confirm hashed-key and boundary handling |
| #120 | Yes (RS-09 refuses a wide ACL) | Could not be isolated in the file as written, but **was isolated** by reordering the checks in a scratch copy — see below | No | Test-hygiene observation, not a finding |
| #121 | Yes — `TestBusConformance/UnsubscribeStopsDelivery` passes under `-race`, unit and integration | Yes — `sync.Once` reverted to an unsynchronised `bool`; `-race` catches the data race immediately, both for `bus.Local`'s conformance run and redisstore's. Restored | No | — |
| #122 | N/A (documentation/release criterion) | N/A | No | `ExampleCache_GetOrLoad`, `ExampleCache_InvalidateTag` and `ExampleWithBus` each genuinely exercise the RF they claim (traced against `cache.go`/`tags.go`/`bus/bus.go`), and all pass as runnable examples |

**#112/#113 deep dive.** Read `gencache.go`, `tags.go`, `events.go` and
`cache.go` in full, not only the tests. The epoch/drops/clears protocol in
`genCache` (`gencache.go:30-98`) is sound for the interleaving #113 named, and
`flightKey`'s inclusion of tag generations (`cache.go:465-476`) is sound for
the interleaving #112 named. Pushing on the *unnamed* interleavings found one
real gap: when `readTagged`'s `GetTagged` call fails, `flightKey` collapses
every caller onto a fixed sentinel (`pk+"\x00?"`) regardless of owner, so two
owners whose reads fail concurrently can still join one flight and cross —
**#134 (HIGH)**. Also read `genCache.drop` (called both by `InvalidateTag`
and, untrusted, by `onEvent`'s `KindTag` handling) against `put`'s bound and
found it has none — **#135 (MEDIUM)**.

**#117 deep dive**, against the brief's specific questions:

- *How long does `New` block when Redis drops packets rather than refusing
  the connection?* Tested against `10.255.255.1:6379` (silently dropped,
  unlike `127.0.0.1:<closed>` which refuses instantly): `Subscribe` returned
  in 1.00s, matching the documented `DefaultTimeout*10` bound. Correct.
- *Does anything else still assume "ready when `Subscribe` returns"?*
  `cache.go:134-138` is the only call site; nothing else depends on Bus
  readiness at construction.
- *Does reconnection work after a mid-run outage, not just at startup?*
  `integration_reconnect_test.go` has exactly one test, and it covers an
  outage that predates `Subscribe` (a startup outage). Wrote a temporary test
  that subscribes while Redis is reachable, publishes and confirms delivery,
  closes the path to Redis for 500ms, reopens it, and confirms delivery
  resumes within 20s: **it does** (21.8s total, most of it container
  start-up). This is a real behaviour gap in test *coverage*, not in
  behaviour — worth a permanent regression test, not a finding.
- *Does anything leak if `Close` is never called?* Not probed directly this
  pass (no new evidence beyond the original audit's read of `bus.Local`'s
  `sync.Once` and `redisstore.Bus`'s handlers). Follow-up: **the inverse
  question, what happens when `Close` *is* called during an outage, turned up
  #137 (MEDIUM)** — `unsub()` (`Cache.Close`'s path) blocked 10.1s at
  go-redis's default `DialTimeout` and 1m40s at an explicit 50s one, against
  `Subscribe`'s own correctly-bounded 1.0s.

**#118 stress test.** `TestHookEventsWithholdTheKeyByDefault`'s 20ms sleep
bridges an inherently racy window (the joining caller's L2 miss to it
registering in the flight) with a fixed constant rather than the deterministic
`flights.Waiting(pk) == n` polling `coalesce_internal_test.go`'s `joinAll`
helper uses for the same purpose elsewhere in this package. Ran it 700 times
total: 100x at `GOMAXPROCS=1`, 300x under artificial CPU load (`yes` on every
core), 300x at `GOMAXPROCS=2` under that same load. **Zero failures.** It
cannot "pass without proving anything" — a lost race would make the join fail
and fall back to an independent load, which the test's own "no %s event
fired" assertion would catch as a hard failure, not a silent pass. So the
worst case is flakiness, and none was reproduced despite deliberately trying.
Recorded as an observation, not a finding, per the brief's own rule: no
failure shown, no finding filed.

**#119 extra probes**, per the brief: a legitimate hashed-key event
(`WithKeyHashing`, real cross-replica `Delete`) reports an **empty** `Key` in
the `OnInvalidate` hook even with `WithHookKeys()` — confirmed with a
dedicated test (`eventKey`'s "" for the `h:` branch holds in practice, not
only in the comment). `MaxKeyBytes` boundary (`len(key) <= MaxKeyBytes`) is
consistent between `validateKey`'s own limit and `eventKey`'s check, no
off-by-one. A name matching the prefix but neither `k:` nor `h:`, and a name
from a different namespace that merely looks like a prefix match, are both
already covered by `TestBusIgnoresEventsItCannotTrust` and were re-verified
by reading `strings.CutPrefix`'s exact-match semantics against ADR-0008's key
format (fixed-count components cannot contain the separator, so no partial-
prefix collision is possible).

**#120 isolation.** The brief asked to get the SUBSCRIBE-outside-`cistern:*`
assertion (`integration_test.go:226-230`) to fail on its own, or say why not.
Root cause: it cannot fail on its own **in the file as written**, because the
PUBLISH-outside-channels check three lines above it (`:223`) calls
`t.Fatalf` first, and Redis ACL's `&pattern` gates PUBLISH and SUBSCRIBE
channels with the same primitive — there is no way to loosen one without the
other, so any control that would break the SUBSCRIBE check also breaks the
PUBLISH check, which always reports first. **Isolated it anyway**: in a
scratch copy, with the two checks reordered (SUBSCRIBE first) and the
existing `&cistern:*` → `&*` control applied, the SUBSCRIBE assertion fails
on its own: `SUBSCRIBE outside cistern's channels: err = <nil>, want NOPERM`.
The security property holds; the test file's ordering is what made it look
unverifiable. Not a finding (the ACL is correct); a test-hygiene observation.

### 2. Coverage the first audit's scope note left open

**Pinned Go floors** (`GOWORK=off`, no silent toolchain upgrade, ADR-0013).
The first audit ran everything on 1.27.1 without exercising either floor.

```
core:       GOTOOLCHAIN=go1.24.0 go build/vet/test -race ./...   -> all green
redisstore: GOTOOLCHAIN=go1.25.0 go build/vet/test -race ./...   -> all green
redisstore: GOTOOLCHAIN=go1.25.0 go test -tags=integration -race -> all green
```

Both floors build, vet and test clean, including the redisstore integration
suite against real Redis at its own floor. `examples` also rebuilt and
retested clean under 1.27.1 (its floor is not separately pinned).

**`/security-review` and `/code-review`.** `/security-review` is diff-based
and, invoked from this environment, resolved against a different repository
in the same session with an unrelated two-file documentation diff; that
output is not applicable to this audit and was discarded rather than forced.
`/code-review high` with an explicit path target reviewed the tip commit's
diff (`1e11af6`, the #122 Examples) and found nothing — consistent with this
session's own reading of the same commit. Neither tool substitutes for the
manual read of `cache.go`, `tags.go`, `gencache.go`, `events.go`, `hooks.go`,
`redisstore/bus.go`, `redisstore/redisstore.go`, `release.yml` and the
`.github/scripts/*.sh` scripts this session did directly, which is where
#134, #135 and #137 came from.

**Fuzzing, 5 minutes each** (brief §2.6), longer than CI's run:

| Target | Duration | Executions | New interesting corpus entries | Result |
| --- | --- | --- | --- | --- |
| `internal/envelope` `FuzzDecode` | 5m | 24,434,091 | 0 (stable at 30) | PASS, no crasher |
| `bus` `FuzzDecode` | 5m | 10,414,402 | 63 (30 → 743, no new *failures*) | PASS, no crasher |

No new files under `testdata/fuzz/`: nothing found rose to the level of a
persisted regression corpus entry (that only happens on a failure). `git
status` after both runs showed no fuzz-related changes.

**TLS.** Not exercised by the first audit or by CI (`redisstore/README.md`
says so: TLS is go-redis's code path, not `redisstore`'s). This pass built a
Redis container that accepts **only** TLS (self-signed CA, SAN for
`127.0.0.1`, `--tls-auth-clients no`, no plaintext port at all) and ran
`Store.Set`/`Get` and `Bus.Subscribe`/`Publish` against it through a
`*redis.Client` with `TLSConfig` set. Both worked end to end. A plaintext
client against the same address was confirmed to fail, so the positive result
is not a fallback artifact. This is not a claim that `cistern` implements
TLS — it doesn't, deliberately (ADR-0001) — only that the documented "bring
your own `TLSConfig`" story actually works.

**Sentinel.** Also not exercised before. Stood up a real `redis-server` plus
`redis-sentinel` (`sentinel monitor`, Docker, host-published ports so
`SENTINEL get-master-addr-by-name` resolves to something the test process can
actually reach — Sentinel's advertised address is otherwise the container's
internal IP, unreachable from the host on Docker Desktop). Built a client
with `redis.NewFailoverClient` (the type `redisstore.New`/`NewBus` already
accept, since it returns the same `*redis.Client`) and ran the same
Set/Get/Subscribe/Publish sequence through it. All passed. A live failover
(killing the master mid-test and confirming Sentinel promotes a replica and
the client follows) was not attempted — this repository has no replica
topology configured for it, and building one is closer to the sapper
scenarios below than to this probe.

**sapper scenarios 1–6.** Still out of scope, and re-confirmed why: issue
#67 states they run "against task-api after T2" — the `task-api` integration
track has not started (§13 of `REQUIREMENTS.md`; T0–T3 are listed as future
work). There is no `task-api` deployment in this repository to run them
against yet. This is unchanged from the first audit's scope note.

### 3. Summary of new findings

| # | Severity | Finding | Issue |
| --- | --- | --- | --- |
| RE-01 | **HIGH** | A tagged cache's `GetTagged` failure collapses `flightKey` onto a fixed sentinel regardless of owner, so two owners whose reads fail concurrently can still join one flight and cross (T-01, the same class #112 fixed for the normal-read path) | #134 |
| RE-02 | MEDIUM | `genCache.drop` has no cardinality bound, unlike `put`; untrusted Bus tag events can grow it without limit until the next unrelated successful tagged read wipes it (T-06) | #135 |
| RE-03 | LOW | No GitHub tag ruleset exists; #115's named compensating control is undeployed and absent from the pre-release checklist (unlike the vulnerability-reporting item next to it) | #136 |
| RE-04 | MEDIUM | `Cache.Close` blocks for roughly 2x the Redis client's `DialTimeout` when Redis is unreachable at the moment `Close` is called — the shutdown-side mirror of #117 (T-12) | #137 |

No finding is fixed in this PR (brief §3). Each needs its own PR, or an ADR
that accepts the risk (`REQUIREMENTS.md` §13, C8), same as the first pass.

## Observations (re-audit): not findings

- **#118's fixed-sleep synchronisation** (`hooks_test.go:103`) survived 700
  stress runs (100x `GOMAXPROCS=1`, 300x under CPU load, 300x
  `GOMAXPROCS=2` under CPU load) with zero flakes, and a lost race would fail
  loudly rather than pass silently. Still worth switching to the deterministic
  `flights.Waiting`-based pattern `joinAll` already uses elsewhere in this
  package, so this doesn't need re-deriving next time.
- **#120's SUBSCRIBE-outside-`cistern:*` assertion** cannot fail on its own in
  `integration_test.go` as written (the PUBLISH check three lines above it
  always reports first, and Redis ACL has no primitive to loosen one channel
  operation without the other). Isolated by reordering in a scratch copy: it
  does fail correctly on its own. Worth reordering the two checks (or adding
  a dedicated sub-test) so this doesn't need re-deriving next audit either.
- **golangci-lint v2.12.2 cannot typecheck under Go 1.27.1's standard
  library** (`crypto/internal/randutil` vs. `math/rand/v2`) — an environment
  version mismatch between the locally installed Go and the lint tool, not a
  project defect. Confirmed clean (0 issues, both modules) once pinned to
  `GOTOOLCHAIN=go1.26.5`, the version CI actually uses.
- **gosec, govulncheck**: 0 issues / no vulnerabilities in core, redisstore
  and examples, current toolchain.
- **Core statement coverage**: 90.6% (`-coverpkg=./...`), against the 85%
  floor — consistent with the first audit's 90.7%, no regression.

## Scope note: what this re-audit did not cover

- **A live Sentinel failover** (killing the master and watching the client
  follow a promotion). The static Sentinel-routing path was proven; the
  dynamic failover path was not, for the reason given above.
- **The release workflow actually running on GitHub** (`actions/checkout@v7`
  and the annotated-tag question) — still reasoned about and run locally
  only, same as the first audit.
- **Benchmarks (RNF-06) and RNF-11** under a real multi-replica deployment.
  Not attempted this pass either.
- **Whether `#134`'s and `#135`'s fixes, once written, reopen anything** —
  by construction, since they are not fixed in this PR.
- **A full independent pass over `examples`** beyond what #122's verification
  and the RS-02/RS-04 evidence already touch.

---

## Re-audit 2 (2026-09-23)

Third pass, again in a clean session with none of the context the code, the
first audit or the re-audit were written in: no conversation history, no
notes, only the repository, `docs/audit-brief.md` and this log. Run against
`develop` at `57fe0c7` (`main` synced to the same tree at `2fea54f`, PR
#144) — the commit the re-audit's four findings, #134–#137, were fixed onto
(PRs #139–#142), plus the unmerged-until-now follow-up to #120 (PR #143,
`test/acl-subscribe-independent`).

**Who ran it.** Claude Code (Claude Sonnet 5), clean session, same
disclosure as both prior passes: not an independent human audit, and the
code — including the fixes this pass verifies — may have been written by the
same model family. The `SECURITY.md` line about no independent security
auditor is still true, now three times over.

**Scope, per this pass's instructions.** Not a repeat of the first two
passes. Specifically:

1. The re-audit's four findings and their fixes (#134–#137): reproduce the
   original issue against current `develop` (must be blocked), redo every
   negative control the closing PR claims, and push on the exact follow-up
   questions this pass's brief posed for each one.
2. The #120 follow-up (PR #143): confirm both ACL assertions now fail
   together, and that switching `t.Fatalf` to `t.Errorf` didn't let the test
   run code afterward that assumes the ACL is correct.
3. The GitHub tag ruleset from #136: confirm it exists, covers exactly the
   right two patterns, is enforced, and reason about who can bypass it.
4. Interaction between #112/#113 (round 1) and #134/#135 (round 2), which
   touch the same `cache.go`/`gencache.go` machinery: `-race -count=20` on
   the race-focused test files.

**Method.** Same as both prior passes: every negative control was actually
run, watched failing, then restored with `git diff --quiet` confirmed clean
before the next step. Proofs of concept were temporary `zz_audit_*` files,
deleted after running; none is in this PR. Two of them uncovered real,
reproducible new questions (#134's residual and #135's `clear()` scope) —
both are written up below with the evidence that survived, not just the
hypothesis that prompted them.

**Environment.** macOS arm64, Go 1.27.1 for the general suite. `gh api`
(authenticated as the repository owner) for the ruleset check — this is
configuration the git history cannot show. Docker 27.5.1, `redis:7.4-alpine`.
golangci-lint v2.12.2 via `GOTOOLCHAIN=go1.26.5` (see the first re-audit's
note on why: it cannot typecheck under 1.27.1's standard library). gosec,
govulncheck.

### 1. Verification of the re-audit's findings (#134–#137)

| # | Repro blocked? | Controls redone? | Anything new? |
| --- | --- | --- | --- |
| #134 | Yes — `TestCallersWithUnreadableGenerationsDoNotShareAcrossTags`, `TestCallersWithUnreadableGenerationsStillShareWithinTheirTags` pass | Yes, both: (a) `flightKey`'s unknown-generations branch reverted to the pre-#134 fixed marker (`pk+"\x00?"`) — fails the cross-owner test, user 43 got user 42's value; restored. (b) Reverted to a per-caller-unique key (the option ADR-0007's amendment names and rejects) — fails the same-tags-still-share test (`loads = 2, coalesced = 0`, want `1, 1`); restored | No. See the deep dive below for the two specific questions this pass asked (owner-cross, and the `\x00` join) |
| #135 | Yes — `TestGenCacheDropsAreBounded`, `TestGenCacheClearByDropsRefusesOlderReads` pass | Yes, both: (a) the bound check removed from `drop` — 30,000 drops leave 30,000 counters uncapped; restored. (b) `clear()`'s `g.clears++` removed — a read whose tag was dropped mid-read restores the retired generation (`stored generation [1]`); restored | **Yes — see #145 below.** The bound itself holds; the mechanism it reuses to enforce it (`clear()`) has a cost the original finding didn't quantify |
| #136 | N/A (a repository setting, not code) | N/A | Confirmed live — see §2 below |
| #137 | Yes — `TestUnsubscribeIsBoundedWhileRedisIsUnreachable` passes (unsubscribe returns in ~4s against a Dialer that hangs 10s, well under Subscribe's own bound) | Yes — the bounded `select`/goroutine wrapper reverted to a synchronous unbounded wait; fails with `unsubscribe took 18.104382625s`; restored | No new finding. See the goroutine-lifecycle deep dive below |

**#134 deep dive**, against this pass's specific questions:

- *Does keying by `genKeys` close the cross-owner join?* Yes, and the
  negative control above proves it: reverting to the pre-#134 fixed marker
  reopens exactly the original cross-owner leak. With `genKeys` in the key,
  two owners with different tags get different flight keys whenever their
  `GetTagged` reads fail, by construction.
- *Two tags sharing a physical-key prefix but different `genKeys` — same
  question, restated.* Covered by the same reasoning: `pk` is the same
  string prefix either way, but the *joined* `genKeys` suffix differs
  whenever the tags differ, so the full `flightKey` string differs. Two
  different owners never collapse to the same key unless their tag sets are
  identical — the same "key function and tag function both omit the owner"
  residual T-01 already documents (`docs/THREAT-MODEL.md` §7), not a new one.
- *Can the `\x00` join between `genKeys` elements collide with a tag that
  already contains `\x00`?* Checked, not assumed. Wrote a throwaway program
  calling `unicode.IsControl(rune(0))` directly: it returns `true` — NUL is
  U+0000, in the Unicode `Cc` (control) category. `validateKey` (`keys.go`)
  rejects every control character by that exact check, and `genKey`
  (`tags.go`) runs it on every tag before building the counter's key. Since
  no `genKeys[i]` can contain the join separator, joining them with it is
  unambiguous (a delimiter that cannot appear inside any joined element
  can always be split back out): there is no pair of distinct tag sets whose
  joined form collides. Confirmed with `unicode.IsControl`, not inferred from
  the comment in `cache.go` that makes the same claim.

**#135 deep dive: the forced-`clear()` question.** The bound `#135` added to
`drop` is real and holds (control (a) above). But `drop`'s bound and `put`'s
bound share one mechanism, `clear()`, which empties **both** `g.drops` (the
counters, what needed bounding) **and** `g.m` (every tag's valid, freshly
cached generation — what a real tagged read populates to skip an L2 round
trip). Before `#135`, the only way to trigger `clear()` was a burst of
*successful* tagged reads through `put`, which is self-limiting. `#135` gave
`drop()` — fed by untrusted Bus `KindTag` events — the same trigger, on a
path that needs zero successful reads. Reproduced: populate one real,
legitimate generation via `put`, then `drop` exactly `maxCachedGens`
(10,000) distinct forged tag names the replica never asked about — the real
entry is gone. **This is filed as #145 (MEDIUM)**, distinct from the
already-accepted T-06 residual ("a flood of forged invalidations degrades
hit rate to zero") because the cost is fixed (10,000 events) while the
damage scales with however large the real cache is — a strictly better
ratio for the attacker than the baseline "one event, one evicted tag" the
existing residual language describes.

**#137 deep dive: does the spawned goroutine leak?** `unsub()`'s bounded wait
(control (a) above, and the un-reverted behavior) still spawns a goroutine
that calls `ps.Close()` and waits `<-done` even after the caller-visible
`select` times out — exactly as ADR-0006's amendment says: "the redial ends
on its own, bounded by the dial timeout." Verified, not just read: called
`Subscribe`+`unsub()` five times in a row against a blackholed address (each
`~1-2s`, matching the bound), then watched `runtime.NumGoroutine()`: it
spiked to 11 (from a baseline of 2) right after the five calls, then
settled back to 2 within 30 seconds — no accumulation with a normally
configured client (default `DialTimeout`). Pushed further: with a custom
`Dialer` that blocks forever and ignores its `context.Context` entirely
(never selects on `ctx.Done()`), the spawned goroutine — and in fact
`Subscribe` itself — hangs permanently, confirming the residual the fixing
PR's own description names ("a go-redis Dialer that never connects: no
limit at all"). This is not filed as a new finding: it is an already
disclosed limitation of Go's `context` contract itself (a function that
never checks `ctx.Done()` cannot be bounded by a caller's timeout, by
construction, regardless of what `redisstore` does on its side), triggered
only by a Dialer so broken it also breaks `#117`'s bound the same way — not
a `redisstore`-specific gap.

### 2. The #120 follow-up (PR #143)

Reproduced PR #143's own claimed verification: with `minimalACL`'s channel
pattern widened from `&cistern:*` to `&*` (the exact control from the first
re-audit), both assertions now report in the same run:

```
integration_test.go:228: PUBLISH outside cistern's channels: err = <nil>, want NOPERM
integration_test.go:233: SUBSCRIBE outside cistern's channels: err = <nil>, want NOPERM
```

(A first pass at reproducing this only matched the `PUBLISH` line with an
overly narrow `grep` pattern — worth recording so the false alarm doesn't
get rediscovered: the full, un-filtered output shows both lines every time.)
Restored, and the real, documented ACL still passes
`TestMinimalACLIsSufficient` end to end.

Read the function after both assertions for code that would misbehave
running past a failed ACL check now that they're `t.Errorf` instead of
`t.Fatalf`: there is none. Lines 227–234 (both `Errorf` calls) are the last
statements in the test function; nothing after them uses `s` or `limited`.
The `t.Fatalf`→`t.Errorf` change cannot let the test read a stale or
partially-set-up client, because there is nothing left to read.

### 3. The GitHub tag ruleset (#136)

```
$ gh api repos/JonasBorgesLM/cistern/rulesets
[{"id":23903936,"name":"Release tags","target":"tag","enforcement":"active", ...}]
```

Full detail via `gh api repos/JonasBorgesLM/cistern/rulesets/23903936` and
`gh ruleset view`:

- `conditions.ref_name.include` is exactly `["refs/tags/v*",
  "refs/tags/redisstore/v*"]`, `exclude: []` — the two patterns #136 asked
  for, no more and no fewer.
- `enforcement: "active"` — not `evaluate` (dry-run) or `disabled`.
- `rules`: `creation`, `update`, `deletion` — all three, matching
  RELEASING.md's new checklist item (PR #142).
- `bypass_actors`: one entry, `{"actor_type": "RepositoryRole", "actor_id":
  5, "bypass_mode": "always"}`.

**What "actor_id: 5" means could not be confirmed from documentation.**
Checked the REST API reference for rulesets, the "Creating rulesets for a
repository" guide, and `gh ruleset view`'s own output (which prints the raw
`RepositoryRole (ID: 5)` without resolving a name) — none of them document
the numeric-ID-to-role-name mapping. This is a genuine gap in what a
read-only check can confirm, stated as a question, not asserted as a fact.

What *is* confirmed: `gh api repos/JonasBorgesLM/cistern --jq
'.permissions'` for the authenticated owner returns `admin: true`, and
`current_user_can_bypass` on the ruleset is `"always"` — so whatever role ID
5 names, it includes the owner. The repository has exactly one collaborator
today (`gh api .../collaborators`), the owner, with every permission bit
set. Per this pass's own instruction ("você pode simular isso raciocinando
sobre a configuração retornada pela API, já que não pode criar um segundo
usuário"): reasoning only, no second account was created to test the
boundary. Two things are true regardless of what role 5 turns out to be:

1. **Today, the ambiguity has no practical effect.** With one collaborator
   who is trivially an admin, no one below whatever tier ID 5 represents
   exists to test the boundary against, and the repository being public
   (`private: false`) doesn't change this — GitHub does not allow a
   non-collaborator to push anything to someone else's repository,
   ruleset or not.
2. **It would matter the day a second collaborator is added below Admin.**
   If ID 5 turns out to mean "Maintain" rather than "Admin" (both are
   plausible without confirmation), a future Maintain-level collaborator
   could push release tags directly, which is a wider bypass than #136's
   fix intended ("restricting who can create `v*` tags").

This is recorded as an open question worth resolving before adding any
collaborator, not as a finding: there is no concrete path today, per the
brief's own rule that a claim without a reachable path is a question.

**Would a non-admin's tag push actually be blocked?** Reasoned from the
returned configuration, as instructed (no second account exists to test
directly): `enforcement: "active"` and `rules: [creation, ...]` on a `tag`
target ruleset mean GitHub rejects the ref-creation operation itself for any
pusher who is not a bypass actor. Today that question is moot for the
reason above (nobody but the owner has any repository access at all), but
once someone with write-or-above access exists, the ruleset — not repository
permissions — becomes the actual gate for tag pushes specifically.

### 4. Should the ruleset be checked automatically?

This pass's brief asked directly: is the ruleset's absence-from-CI a gap,
and should something fail automatically if it's ever removed by mistake
before the first release? Checked rather than guessed: fetched GitHub's own
workflow-syntax reference for the exact set of permission keys grantable to
the default `GITHUB_TOKEN` via a workflow's `permissions:` block. The full
list (`actions`, `attestations`, `checks`, `contents`, `deployments`,
`discussions`, `id-token`, `issues`, `packages`, `pages`, `pull-requests`,
`security-events`, `statuses`, `vulnerability-alerts`, and others) **does
not include `administration`**, which is what reading repository rulesets
requires. `gh api repos/.../rulesets`, run with the default token a
workflow gets, would be refused regardless of what the workflow's
`permissions:` block declares.

So an automated check is not free: it needs a separate credential (a
personal access token or a GitHub App installation token with
`administration: read`) stored as a repository secret, which is a new
credential to manage — a small but real blast radius increase — for a check
that already has a manual, documented command
(`RELEASING.md`, PR #142) run once per release. Recommendation: not worth
automating *yet*, on cost/benefit grounds, but worth being explicit that
"documented" and "automatically verified" are different guarantees — this
audit log is now the second and third place that's had to reconfirm the
ruleset exists by hand.

### 5. Interaction between rounds 1 and 2's fixes

`-race -count=20` on every test in `races_test.go` (`#112`/`#134`'s tests
together) and `gencache_race_test.go` (`#113`): 100 total test runs (5 tests
× 20), zero failures, zero races reported. Also ran the whole core suite at
`-race -count=5` (35 more runs across every package) — all green. No new
interaction between #112, #113, #134 and #135's fixes was found.

### Summary of new findings

| # | Severity | Finding | Issue |
| --- | --- | --- | --- |
| RE2-01 | MEDIUM | `genCache.drop`'s bound-triggered `clear()` evicts every cached tag's generation, not just the flooded ones, for a fixed attacker cost — an amplification `#135`'s fix inherited from `put`'s pre-existing "wipe both maps" design, newly reachable from untrusted Bus events | #145 |

No finding is fixed in this PR, same rule as both prior passes.

## Observations (re-audit 2): not findings

- **The ruleset's `bypass_actors` role-ID semantics** (§3) could not be
  confirmed from any documentation this session could reach. Worth
  resolving — by asking GitHub support, or by testing with a second,
  lower-privileged collaborator once one exists — before that ambiguity has
  a practical target to matter against.
- **Automated ruleset verification** (§4) is not free: it needs a new
  `administration`-scoped credential the default `GITHUB_TOKEN` cannot
  provide. Documented as a deliberate non-decision, not an oversight.
- **`#137`'s spawned goroutine with a `context`-ignoring custom `Dialer`**
  hangs permanently, confirmed directly. This is a property of Go's
  `context` contract (nothing can bound a function that never checks
  `ctx.Done()`), not a `redisstore`-specific defect, and the fixing PR
  already disclosed it in passing.

## Scope note: what this re-audit did not cover

- **A full re-run of rounds 1 and 2's scope.** By instruction: this pass
  only covers what neither prior pass audited yet (the four fixes, the
  ruleset, the #120 follow-up, and the cross-fix interaction).
- **Creating a second GitHub collaborator** to empirically resolve the
  `bypass_actors` role-ID question in §3. Reasoned about instead, per this
  pass's own instruction.
- **Everything both prior passes already listed as out of scope** (sapper,
  a live Sentinel failover, the release workflow actually running on
  GitHub, benchmarks/RNF-11 under a real multi-replica deployment) — still
  true, and not repeated here.
