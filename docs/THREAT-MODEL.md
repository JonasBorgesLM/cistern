# cistern — Threat Model

**Version:** 0.1
**Scope:** the `cistern` library and the cached values it holds.
**Companion to:** [`../REQUIREMENTS.md`](../REQUIREMENTS.md) — every mitigation
below names the `RS-` it is discharged by, and every `RS-` appears at least
once.

A threat model that lists only threats it defeats is marketing. §7 lists what
this design does **not** stop, and why.

---

## 1. What is being protected

| Asset | Why it matters | Exposure |
| --- | --- | --- |
| **Cached values** | May carry another tenant's data if a key is built wrong; the whole point of the library is to serve them fast | Stored in L1 (process memory) and L2 (Redis) |
| **Physical cache keys** | Encode namespace, tenant/owner and tag generation; a collision here is a cross-tenant read | Sent to Redis; may appear in hooks/logs |
| **Tag generation counters** | Control invalidation of an entire group of keys at once | Stored in L2; incrementable by anything with Redis access |
| **The Bus channel** | Coordinates L1 eviction across instances | Redis Pub/Sub; readable/writable by anything with Redis access |
| **Availability of reads** | A cache outage must never become an application outage | Depends on fail-open (RNF-02) and `Guard` timeouts (RNF-03/RNF-04) |
| **The source of truth** | The cache exists to protect it from load; a broken negative-cache or coalescing path removes that protection under attack | Depends on RF-05/RF-06 |

The asymmetry worth naming: **a cache miss is always safe, a cache hit on the
wrong data is never safe.** Every mitigation below is biased toward "fail to a
miss" over "fail to a stale or foreign value."

## 2. Actors and their capabilities

| Actor | Assumed capability | Assumed *not* to have |
| --- | --- | --- |
| **Authenticated application user** | Can trigger reads/writes through the host application within their own account | Direct access to Redis or to another user's account |
| **Malicious authenticated user** | The above, used adversarially: crafted ids, high-frequency requests for keys they should not see | Ability to bypass the host's authentication/authorization |
| **Redis-adjacent attacker** | Can reach the Redis instance (misconfigured network, shared instance, compromised neighbour) | Ability to modify the host application's code |
| **Curious insider** | Read access to application logs/metrics where hooks report | Read access to Redis |
| **Network observer** | Sees traffic between the host and Redis if TLS is not enabled | Plaintext under TLS (RS-09) |

`cistern` assumes the host has already authenticated and authorized the
caller before a `Cache[K,V]` method is invoked. The library has no identity
model of its own; every isolation guarantee below depends on the host's
`KeyFunc` actually encoding the caller's scope (RS-02).

## 3. Trust boundaries

```
        ┌────────────────────── host application ───────────────────┐
        │  Authenticated user → Service → Repository (cache-aside)  │
        └───────────────────────────┬─────────────────────────────┘
                                     │ Cache[K,V] (Go API, in-process)
        ╔════════════════════════════▼════════════════════════════╗
        ║  cistern core — key validation, L1, coalescing, codec    ║  ← RS-01..RS-08
        ╚════════════════════════════╤════════════════════════════╝
                                      │ Store / Bus (network)
        ┌─────────────────────────── ▼ ──────────────────────────┐
        │  Redis (L2 + Pub/Sub) — shared, reachable by anything   │  ← RS-06, RS-07, RS-09, RS-10
        │  with network access and credentials                    │
        └──────────────────────────────────────────────────────────┘
```

The boundary that matters most is the one between the Go API (trusted,
in-process) and Redis (untrusted transport and untrusted storage, per RS-06).
Nothing that comes back from Redis is trusted without validation.

## 4. Threats and mitigations

Each threat is `T-nn`, with its actor, impact and the requirements that
discharge it. "Residual" means what is left after the mitigation.

### T-01 — Cross-user read through a badly built key

**Actor:** malicious authenticated user. **Impact:** the highest in this
system — one user is served another user's cached data, and nothing in the
response looks wrong.

**Mitigation:** RS-01 (mandatory non-empty namespace, keys always prefixed),
RS-02 (owner-scoped `KeyFunc` as the documented pattern, plus an explicit test
trying to read another user's data).

**Residual:** a consumer `KeyFunc` that omits the owner defeats this; see §7.

### T-02 — Key injection and ambiguous collisions

**Actor:** malicious authenticated user supplying key material. **Impact:** log
injection through control characters, or two logical keys mapping to one
physical key.

**Mitigation:** RS-03 (length limit, control-character rejection, opt-in
SHA-256 hashing of long keys).

**Residual:** none known in the format itself: every component before the
consumer key is fixed in count and cannot contain the separator, so a `:`
inside the key is unambiguous (ADR-0008). Keys are in clear in Redis unless
hashed, an exposure only to whoever can already read the values.

### T-03 — A secret is cached

**Actor:** none — a consumer mistake. **Impact:** a token, session or secret
persists in L1/L2 past its intended lifetime, readable by anyone with Redis
access.

**Mitigation:** RS-04 (`NoCache` marker interface rejected by `Set`; `moat`'s
`secret.Value` documented as never cacheable, with a test in `examples`).

**Residual:** a secret carried in a type that does not implement `NoCache`
passes through. The marker is a guard rail, not a classifier.

### T-04 — Oversized values

**Actor:** malicious user or accident. **Impact:** L1 memory exhaustion or L2
bandwidth saturation.

**Mitigation:** RS-05 (value size limit on `Set` and on decode), plus L1's
entry and byte limit (§8.2 of the requirements).

**Residual:** none known beyond choosing a limit that fits the workload.

### T-05 — Hostile or corrupted envelope from L2

**Actor:** Redis-adjacent attacker, or plain corruption. **Impact:** a panic in
the host process, or a decode that instantiates attacker-chosen types.

**Mitigation:** RS-06 (L2 data is untrusted: size-limited decode,
version/codec validation, no codec that instantiates arbitrary types; an
invalid envelope is a miss plus an error hook, never a panic).

**Residual:** a *well-formed* forged envelope carrying a plausible value is
indistinguishable from a real one. Anyone who can write Redis can poison the
cache; see §7.

### T-06 — Forged Bus message

**Actor:** Redis-adjacent attacker. **Impact:** bounded — extra L1 misses.

**Mitigation:** RS-07 (the Bus carries invalidation only, never values).

**Residual:** a flood of forged invalidations degrades hit rate to zero, which
is a performance loss, not a correctness or confidentiality loss.

### T-07 — Keys leaking into logs and metrics

**Actor:** curious insider. **Impact:** user ids embedded in keys reach log
indexes with long retention.

**Mitigation:** RS-08 (hooks receive namespace and operation by default; the
full key only through an explicit option).

**Residual:** a consumer that opts into full keys owns that decision.

### T-08 — Unauthenticated or intercepted Redis traffic

**Actor:** network observer, Redis-adjacent attacker. **Impact:** cached values
read in transit, or Redis used without credentials.

**Mitigation:** RS-09 (configurable AUTH/ACL and TLS; a minimum-command ACL user
documented in the README).

**Residual:** enabling them is the operator's job; the library cannot require
TLS on a local development Redis.

### T-09 — Wrong eviction policy or a shared Redis database

**Actor:** none — a misconfiguration. **Impact:** with `noeviction` the cache
fills Redis and writes fail; sharing a database with `moat`'s rate limiter or
`cairn`'s link store lets cache pressure evict their data.

**Mitigation:** RS-10 (`allkeys-lru` documented as correct *for cached values*, ADR-0010 —
the explicit contrast with `moat`, where the same policy caused a bypass; a
separate instance/logical DB recommended).

**Residual:** documentation only; `redisstore` does not verify the policy at
startup in the MVP.

### T-10 — Stampede on a hot key

**Actor:** load, adversarial or organic. **Impact:** a hot key expires and
every waiting request hits the source of truth at once.

**Mitigation:** RF-05 (singleflight coalescing per physical key), RF-04 (TTL
jitter against mass expiry). Validated by sapper scenario 2.

**Residual:** coalescing is per process; N replicas can still issue N loads.

### T-11 — Penetration with non-existent ids

**Actor:** malicious authenticated user. **Impact:** every request misses and
loads the source of truth — a denial-of-service lever against the origin.

**Mitigation:** RF-06 (optional negative caching with its own short TTL).
Validated by sapper scenario 5.

**Residual:** random ids that never repeat are not helped by negative caching;
rate limiting (`moat`) is the complement.

### T-12 — Redis outage becoming an application outage

**Actor:** none — an infrastructure failure. **Impact:** a cache that was meant
to be an optimization takes the read path down, or adds latency to every
request.

**Mitigation:** RNF-02 (fail-open to L1/loader), RNF-03 (short L2 timeouts
independent of the request deadline), RNF-04 (`Guard` circuit breaker, with
`bastion` in the examples). Validated by sapper scenarios 3 and 4.

**Residual:** while Redis is down, the source of truth carries the full load.

### T-13 — Cache-aside race writes a stale value back

**Actor:** none — concurrency. **Impact:** a reader loads an old value, a
writer invalidates, and the reader stores the old value afterwards; users see
stale data until expiry.

**Mitigation:** for tag invalidation the race is closed — entries record the
tag generations read before the load, so a bump during the load makes the
entry stale on arrival (RF-10, ADR-0005, ADR-0011). For an untagged `Delete`:
short TTLs on write-exposed entries and coalescing (RF-05).

**Residual:** an untagged key written concurrently with a `Delete` may be
stale for up to its TTL (ADR-0011). See §7.

### T-14 — Evicted generation counter resurrects invalidated entries

**Actor:** none — Redis memory pressure under `allkeys-lru`, or anyone who can
delete a key. **Impact:** a tag's generation counter is evicted and recreated
at a value used before; entries retired by an earlier `InvalidateTag` become
reachable again and are served as current.

**Mitigation:** RF-10 as constrained by ADR-0010: a missing counter is
recreated at an unpredictable value in [1, 2⁶²), so its tag's entries become
misses rather than current (ADR-0005). The conformance suite checks it for
every `TagStore`.

**Residual:** none known; the cost of an evicted counter is misses.

### Coverage

| Threat | Discharged by |
| --- | --- |
| T-01 Cross-user read | RS-01, RS-02 |
| T-02 Key injection | RS-03 |
| T-03 Secret cached | RS-04 |
| T-04 Oversized values | RS-05 |
| T-05 Hostile envelope | RS-06 |
| T-06 Forged Bus message | RS-07 |
| T-07 Keys in logs | RS-08 |
| T-08 Redis transport | RS-09 |
| T-09 Eviction policy | RS-10 |
| T-10 Stampede | RF-04, RF-05 |
| T-11 Penetration | RF-06 |
| T-12 Redis outage | RNF-02, RNF-03, RNF-04 |
| T-13 Cache-aside race | RF-10, RNF-11 |
| T-14 Generation counter evicted | RF-10 (ADR-0005, ADR-0010) |

RS-11 is discharged by this document itself.

## 5. Assumptions the host must satisfy

`cistern` cannot verify these; they are documented preconditions rather than
enforced controls:

- The host authenticates and authorizes callers **before** invoking
  `Cache[K,V]`. cistern has no identity model.
- `KeyFunc` implementations actually encode the caller's tenant/owner scope
  (RS-02). A `KeyFunc` that ignores the owner defeats T-01 regardless of what
  the library does.
- Redis is reachable only from the host application's network segment, with
  AUTH/ACL and TLS configured per RS-09.
- `maxmemory-policy` is set to `allkeys-lru` on the Redis instance/database
  used for caching, and that instance/database is not shared with `moat`'s
  rate limiter or `cairn`'s link store (RS-10).

## 6. Non-goals that are also security decisions

Restated from `REQUIREMENTS.md` §3 because a threat model that omits them
would look more complete than it is:

- **No Redis Cluster support in the MVP.** Generation keys are not hash-tagged
  (§8.5 of `REQUIREMENTS.md`), so cross-slot operations are out of scope.
  Running `cistern` against a cluster without hash tags is a misuse the
  library does not detect.
- **No HTTP-level caching.** Authenticated responses keep `no-store` via
  `moat`; `cistern` never becomes an excuse to relax that.
- **No write-behind.** A crash between a cache write and a source write cannot
  lose a source-of-truth write, because there is no such path.

## 7. What this design does not stop

- **A `KeyFunc` that omits the owner scope.** T-01's mitigation is structural
  encouragement (RS-01, RS-02) and a test against the documented pattern, not
  a guarantee that every consumer implements `KeyFunc` correctly. This is the
  single largest residual risk in the design.
- **A privileged Redis-adjacent attacker reading cached values directly.**
  RS-06 stops a *malformed* envelope from being decoded into something
  dangerous; it does not encrypt data at rest. Anyone who can read Redis's
  memory or persistence file can read every cached value in plaintext (or
  whatever the codec produces). Encryption at rest is the operator's
  responsibility, not this library's.
- **Cache poisoning by anyone who can write Redis.** A well-formed envelope
  carrying a plausible value passes RS-06 by design (T-05). Values are not
  signed in the MVP; Redis write access is therefore equivalent to control
  over what the cache serves until expiry.
- **DNS- or network-level compromise between the host and Redis**, beyond what
  TLS (RS-09) already covers.
- **A cache-aside race on untagged keys** (T-13). Tag invalidation closes it;
  an untagged `Delete` racing a load can leave a stale value for up to its TTL
  (ADR-0011).
