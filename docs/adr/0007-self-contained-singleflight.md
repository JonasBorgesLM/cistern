# ADR-0007: Self-contained singleflight under internal/

## Status
Accepted

## Context
RF-05 requires that N concurrent calls for the same missing key trigger one
loader call; without it, a hot key that expires sends every waiting request to
the source of truth at once (T-10). The standard answer in Go is
`golang.org/x/sync/singleflight`. The core admits no dependencies at all
(RNF-01, ADR-0001), so that answer is not available — and three properties
this cache needs are not what `x/sync` provides anyway:

1. **Callers must not share a value.** `x/sync` hands every caller the same
   result object. ADR-0014 made every hit decode a fresh copy so that a caller
   mutating what it received cannot reach another caller; a coalesced load
   that shared one `V` would reopen exactly that hole for the N callers of a
   stampede.
2. **One caller's cancellation must not cancel the load for the others**
   (RF-02). If the loader ran on the first caller's context, that caller
   giving up would fail every other waiter.
3. **A loader that never returns must not take the key down with it.** With
   coalescing, every new caller joins the in-flight load. If that load hangs,
   the key is unavailable to everyone, permanently, until the process restarts.

## Decision
A small generic group in `internal/singleflight`, keyed by the **physical key**
(ADR-0008), so namespace and tag generations are part of what is coalesced.

- **The shared result is encoded bytes.** The flight loads, encodes and stores;
  each caller decodes its own copy, as on any other hit (ADR-0014).
- **The load runs detached.** It runs in its own goroutine, on
  `context.WithoutCancel` of the first caller's context — so request-scoped
  values such as trace ids survive — bounded by a **load timeout** (option
  `WithLoadTimeout`, default 10 s). Each caller waits for the result or for its
  own context, whichever comes first; leaving does not cancel the load.
- **A flight whose load timeout has expired is never joined**, even if the
  loader ignores its context and never returns: the next caller replaces it
  and starts a fresh load. A loader that ignores its context can therefore
  leak one goroutine per timeout window (and, if its key is never requested
  again, one map entry); that is bounded, and it is the loader's bug, not a
  stuck key.
- **A panicking loader is recovered in the flight goroutine and re-panicked in
  every caller still waiting**, with the original value and stack. That is
  what each caller would have seen calling the loader itself, and it never
  deadlocks a waiter. If every caller has already left, the panic is dropped:
  there is nobody left to deliver it to, and crashing the process from a
  background goroutine would be worse.
- A load **error** is delivered to every waiting caller and is not cached,
  except `ErrNotFound` under negative caching (RF-06).

## Consequences
- Coalescing is **per process**. N replicas can still issue N loads for the
  same key; that is T-10's residual, and L2 narrows it because the first
  replica's result becomes a hit for the others.
- A coalesced stampede of N callers costs one load and N decodes. The decodes
  are the price of isolation, already measured by the L1 hit benchmark.
- `WithLoadTimeout` joins the options approved in #13. A read can now fail with
  a load timeout; that is a failure of the loader, bounded so it cannot become
  a failure of the key.
- Testing coalescing deterministically needs to observe how many callers have
  joined a flight. That observation stays inside the module (an internal test
  hook), not in the public API.

## Open
None.

## Amendment (audit finding #112)
The decision says flights are keyed by the physical key "so namespace and tag
generations are part of what is coalesced". That stopped being true when
ADR-0005 moved generations out of the key and into the envelope, and the
audit showed the consequence: a caller joining a flight was served its value
without its own generations being compared — another owner's data when the
owner was only in the tag, and a pre-invalidation value to a read issued after
`InvalidateTag` returned.

For a tagged cache, a flight is now keyed by the physical key **and the tag
generations the caller read**. Callers that saw different generations never
share a load; a caller whose generations could not be read shares only with
others in the same position, and its load is not cached.
