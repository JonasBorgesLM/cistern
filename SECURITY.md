# Security Policy

## This library has not been independently audited

**`cistern` has not been reviewed by an independent security auditor**, and at
this stage it has no released code to audit. Its reasoning is written down in
[`REQUIREMENTS.md`](REQUIREMENTS.md), [`docs/THREAT-MODEL.md`](docs/THREAT-MODEL.md)
and [`docs/adr/`](docs/adr/README.md) so that it can be checked rather than
trusted. An audit in a clean session is a release criterion for `v0.1.0`
(`REQUIREMENTS.md` §15).

## Supported versions

Pre-release. No version is supported yet.

| Module | Supported |
| --- | --- |
| `github.com/JonasBorgesLM/cistern` (core) | latest `v0.x` tag, once one exists |
| `github.com/JonasBorgesLM/cistern/redisstore` | latest `redisstore/v0.x` tag, once one exists |
| `examples/` | not supported; illustrative code, not a released artifact |

## Reporting a vulnerability

Use GitHub's **private vulnerability reporting**:
[Security → Report a vulnerability](https://github.com/JonasBorgesLM/cistern/security/advisories/new).
Do not open a public issue.

Please include the affected module and version (or commit), a description of
the impact, and a reproduction if you have one.

## Security model in brief

The full model is [`docs/THREAT-MODEL.md`](docs/THREAT-MODEL.md). The points an
operator must know:

- **A cache is not a source of truth, and never a place for secrets.** Types
  implementing `NoCache` are refused (RS-04); `moat`'s `secret.Value` must never
  be cached.
- **Isolation between users depends on your key.** cistern requires a namespace
  (RS-01); your `KeyFunc` must encode the owner (RS-02). A key that omits the
  owner is a cross-user leak no library can detect.
- **Anything that can write your Redis controls what the cache serves** until
  expiry. Values are not signed or encrypted at rest.
- **Reads fail open** (RNF-02): a Redis outage removes the performance gain,
  never the application.

## Operating Redis for cistern

- **Authentication and TLS** (RS-09): use the ACL user restricted to the
  commands and keys `redisstore` needs — the exact, tested line is in
  [`redisstore/README.md`](redisstore/README.md) — and enable TLS outside local
  development.
- **`maxmemory-policy allkeys-lru`** (RS-10). This is correct *for a cache* and
  is the deliberate opposite of what `moat`'s rate limiter and `cairn`'s link
  store require (`noeviction`). Do not share an instance between cistern and
  either of them. A separate logical database is not enough: `maxmemory` and
  its policy are instance-wide, so `allkeys-lru` evicts their keys too.
- **Standalone or Sentinel only** — Redis Cluster is out of scope for the MVP
  (`REQUIREMENTS.md` §3).
