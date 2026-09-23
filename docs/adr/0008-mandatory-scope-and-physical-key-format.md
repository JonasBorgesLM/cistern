# ADR-0008: Mandatory scope and the physical key format

## Status
Accepted

## Context
The largest risk in this library is a cross-user read through a badly built key
(T-01, `REQUIREMENTS.md` §11). Two requirements address it at the key level:
every key is scoped by a mandatory namespace (RS-01), and the owner is encoded
by the consumer's key function following a documented pattern (RS-02). RS-03
adds validation — a length limit, no control characters, opt-in hashing of long
keys — so that two logical keys can never collide and a key can never inject
into a log line.

What was left open (§16, question 1) is the concrete layout: the separator,
the order of the components, and the hashing threshold. It has to be fixed
before `Cache[K,V]` exists, because every level (L1, L2) and the Bus address
entries by it, and because once `redisstore` ships it becomes the format that
deployed data is written in.

Two alternatives were weighed and rejected:

- **Length-prefixed components** (`cistern:v1:5:tasks:…`) are unambiguous for
  any character, but unreadable in `redis-cli` — and reading keys is how an
  operator debugs a cache.
- **Always hashing the consumer key** keeps user ids out of Redis and gives a
  uniform length, but makes every key opaque to debugging and puts a SHA-256 on
  every access for a property RS-03 only asks for opt-in.

A second question was whether the owner should be a mandatory argument of every
operation (`Get(ctx, owner, key)`, with an explicit sentinel for shared caches)
instead of a convention inside the key. It would close more of T-01 at the API,
at the cost of weight on every call and of caches that are not per user. RS-02
chose the convention; this ADR keeps it.

## Decision
### Layout

```
cistern:v1:<namespace>:<generations>:<kind>:<key>
```

| Component | Content |
| --- | --- |
| `cistern` | Fixed prefix. Separates cistern's keys from anything else in the database |
| `v1` | Key schema version |
| `<namespace>` | Required (RS-01). `[a-z0-9._-]`, 1–64 bytes, validated in the constructor |
| `<generations>` | `-` when the entry has no tags; otherwise the tag generations. The encoding is ADR-0005's, constrained here to contain no `:` |
| `<kind>` | `k` when `<key>` is the consumer key verbatim, `h` when it is a hash |
| `<key>` | The consumer key, or its SHA-256 in lowercase hex |

The separator is `:`. **The consumer key is always the last component**, and
every component before it is of fixed count and cannot contain `:`, so a `:`
inside the consumer key is unambiguous. That is what lets the documented owner
pattern — `user:42:list` — be written naturally.

The same physical key addresses the entry in L1, in L2 and in Bus messages.

### Validation (RS-03)

The consumer key must be non-empty, valid UTF-8, contain no control characters
(Unicode category Cc, which covers C0, DEL and C1), and be at most **256
bytes**. A key over the limit is rejected with an error — unless the cache was
built with the opt-in `WithKeyHashing` option, in which case that key is stored
as `h:<sha256 hex>` instead. Keys within the limit are never hashed. Namespace
and generations stay in clear either way, so tag invalidation still applies to
hashed keys.

Validation errors are the caller's bug, not an outage, so they are returned
(ADR-0002).

### Owner scope (RS-02)

The owner stays inside the consumer key, produced by the cache's key function.
The documented pattern puts it first — `user:{id}:…`, `tenant:{id}:…` — and the
`examples` module carries an explicit test in which one user tries to read
another's entry and must miss. The namespace names a **domain** (`tasks`,
`sessions-meta`), not an owner: owner ids are not restricted to the namespace
alphabet and would otherwise have to be escaped.

## Consequences
- An operator can read keys in `redis-cli` and see the namespace, the owner and
  whether an entry is tag-scoped.
- User ids appear in clear in Redis keys unless the key is hashed. That exposure
  is to whoever can already read Redis — who can read the values too (T-05's
  residual, `docs/THREAT-MODEL.md` §7) — so it adds little; hooks still never
  receive the full key by default (RS-08).
- T-01's residual is unchanged and stays the largest in the design: a key
  function that omits the owner is a leak the library cannot see.
- The `v1` schema freezes with `redisstore/v0.1.0`. Changing it later is not a
  data migration — this is a cache — but every replica starts cold on the new
  prefix, and replicas on different versions stop sharing entries during a
  rollout. Both load the source of truth; a schema change needs a release note.
- A key over 256 bytes fails at the call site in development, which is where a
  consumer should discover it, rather than being silently hashed.

## Open
Whether to offer hashing of **all** keys (not only long ones) so that user ids
never reach Redis in clear. Not in the MVP: nobody has asked for it, and it
would be one option on top of the existing hash path.

## Amendment (ADR-0005)
Tag generations are recorded in the envelope and checked on read, not encoded
in the key. The `<generations>` component of an entry's key is therefore always
`-`; it stays in the layout so the `v1` key schema does not change. Generation
counters live under `cistern:v1:<namespace>:g:<tag>`, whose fourth component
`g` can never be an entry's `-`. Tags are validated like consumer keys.
