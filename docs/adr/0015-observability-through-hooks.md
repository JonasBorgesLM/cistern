# ADR-0015: Observability through hooks, with keys withheld by default

## Status
Accepted

## Context
RF-15 asks for hooks on hit, miss, load, coalesced, error, evict and
invalidate. Fail-open (ADR-0002) makes them more than a nicety: every error on
the read path is swallowed, so without a hook a Redis outage is invisible
except as load on the source of truth.

Two sibling libraries already answered the shape question. `cairn`'s ADR-0016
chose a struct of optional callbacks over an OpenTelemetry dependency, because
`crier` owns transport in this ecosystem; `bastion` does the same and runs
hooks so that a panicking one cannot break the call it observes.

RS-08 adds a constraint of its own: a consumer key usually embeds a user id
(ADR-0008), and hooks are routinely wired straight into logs.

## Decision
A `Hooks` struct of nil-able function fields, set with `WithHooks`:

| Field | Fires when |
| --- | --- |
| `OnHit` | a read is served, with the level (L1 or L2) |
| `OnMiss` | a read finds nothing usable |
| `OnLoad` | a loader returns, with its duration and error |
| `OnCoalesced` | a caller is served by another caller's load (RF-05) |
| `OnError` | an error is swallowed by fail-open: the operation, the level, the error |
| `OnInvalidate` | a key or tag is invalidated, locally or by a Bus event |

- **Keys are withheld by default (RS-08).** Every event carries the namespace;
  the consumer key is filled in only with `WithHookKeys()`. Tags are not keys
  and are always present on invalidation events — they name groups, and a
  consumer who puts a user id in a tag has chosen to.
- **Hooks are synchronous and must not block**: there is no goroutine pool to
  absorb a slow one, and a hook that blocks is a read that blocks.
- **A panicking hook is recovered and ignored.** A logger with a bug must not
  turn a cache hit into a crash; this is `bastion`'s rule.
- **Errors that are returned are not also hooked.** `Set`, `Delete` and
  `InvalidateTag` fail loud (ADR-0002); the caller already has the error.
- **Eviction is observed where it happens.** Only an L1 knows when it evicts,
  so `memory.WithOnEvict` reports it; a Redis L2 evicts on its own terms, which
  Redis's own metrics report.

## Consequences
- No observability dependency in the core (RNF-01). Exporting to `crier` or to
  metrics is a few lines in the host; the `examples` module shows it.
- An unset hook costs a nil check; events are built only when a hook is set.
- Hook authors see errors from untrusted L2 data (RS-06); those errors never
  contain the key, so logging them does not bypass RS-08.

## Open
None.
