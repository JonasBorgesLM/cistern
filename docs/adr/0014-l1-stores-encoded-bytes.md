# ADR-0014: L1 stores encoded bytes behind the same Store as L2

## Status
Accepted

## Context
An in-process L1 can hold either the caller's value `V` itself or its encoded
bytes. The choice was not made in `REQUIREMENTS.md`, but two requirements lean
on it:

- **RF-14** and C5's exit criterion ask for one `Store` interface with one
  conformance suite (`cisterntest`) that both `memory` and `redisstore` pass.
  L2 can only hold bytes, so one interface means bytes.
- **§8.2** gives L1 a **byte** limit alongside the entry limit. The size of an
  arbitrary `V` in memory is not knowable from Go without encoding it, so a byte
  limit over values is an estimate at best.

Holding `V` directly is the fastest possible L1 hit — no decode. Its cost is
aliasing: every caller that hits the same entry receives the *same* object. A
caller that appends to a returned slice or writes to a returned map changes the
cached value for every later reader, and nothing in the type system prevents
it. "Do not mutate what the cache returns" is a rule that cannot be checked.

Three options were weighed:

| Option | L1 hit | Isolation between callers | One `Store` + `cisterntest` for L1 | Byte limit exact |
| --- | --- | --- | --- | --- |
| Encoded bytes | decode per hit | yes — each hit decodes a fresh copy | yes | yes |
| `V` directly | no decode | no — shared object | no | no |
| `V` + optional clone function | no decode | only if the consumer configures it | no | no |

## Decision
**L1 stores encoded bytes, behind the same `Store` interface as L2.** Every hit
decodes, so every caller receives its own copy and a mutation by one caller can
never reach another. `memory` and `redisstore` implement the same interface and
run the same `cisterntest` suite. The L1 byte limit is the length of what is
stored, exactly.

The bytes are what the codec produces (RF-13); from C3 onward they are the
envelope (ADR-0009, planned). The exact `Store` signature is fixed with its
issue (#17).

## Consequences
- **An L1 hit costs one decode.** The L1 hit benchmark (RNF-06, #20) records
  what that is for a representative value, so the trade is measured rather than
  assumed.
- `Cache[K,V]` cannot store anything without a codec, so the `Codec` interface
  and its JSON default move from C3 to C1. The envelope, key validation and
  value limits stay in C3.
- A consumer with an immutable `V` pays for isolation it does not need. That is
  the price of a default that is safe for every `V`.

## Open
**Reopening criterion:** a consumer measures the L1 decode as a material share
of request latency on a hot path (sapper, not a microbenchmark). The proposal
must say how isolation is kept for mutable `V` — for example a required clone
function — and must be a new ADR that supersedes this one.
