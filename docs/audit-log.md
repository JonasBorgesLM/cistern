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
