# ADR-0009: Versioned envelope prepared for stale-while-revalidate

## Status
Accepted

## Context
Every entry cistern stores, in either level, is read back later by code that
must not trust it: L2 data can be corrupted, written by an older or newer
version of this library, written by a cache configured with another codec, or
written by anyone with access to Redis (RS-06, T-05). Until now an entry was
one tag byte (`v` value, `n` absence) followed by the codec's output — enough
for C2, but it carries no version, no codec identity and no expiry of its own.

Three later needs depend on what the stored bytes say about themselves:

- **Stale-while-revalidate** (RF-17, post-MVP) serves an entry past its
  freshness while reloading it. That needs a *logical* expiry inside the entry,
  separate from the store's *physical* TTL (D2).
- **L1 backfill** (ADR-0004) must clamp an L1 copy to the L2 entry's remaining
  lifetime, which the store does not report.
- **Format changes** must be recognisable, so an entry from another version is
  a miss rather than a misread.

## Decision
Every entry is an envelope with a fixed header:

| Offset | Size | Field |
| --- | --- | --- |
| 0 | 1 | format version, `1` |
| 1 | 1 | flags; bit 0 = absence (negative cache). Other bits must be zero |
| 2 | 8 | logical expiry, Unix nanoseconds, big-endian, positive |
| 10 | 1 | codec id length *n*, 1–32 |
| 11 | *n* | codec id, the `Codec.ID()` that produced the payload |
| 11+*n* | rest | payload: the encoded value; **empty if and only if** absence |

The format lives in `internal/envelope`: it is a storage detail, not API.

**Decoding is validation (RS-06).** An envelope is rejected — and the read is a
miss, never an error to the caller and never a panic (ADR-0002) — when it is
shorter than its header, has an unknown version, sets an unknown flag, has a
non-positive expiry, a codec id of invalid length, a payload inconsistent with
its absence flag, or a payload larger than the cache's value limit (RS-05).

**The codec is never chosen by the data.** A cache decodes only envelopes whose
codec id equals its own codec's id; any other id is a miss. That is what "no
codec that instantiates arbitrary types" means at the storage layer: the bytes
cannot pick how they are decoded.

**Expiry.** For now the logical expiry equals the physical TTL given to the
store, and an entry past its logical expiry is a miss even if a store still
returns it. Stale-while-revalidate will later give the physical TTL a margin
beyond the logical expiry, without changing this format.

**The value limit** (`WithMaxValueBytes`, default 1 MiB) bounds the payload on
the way in — an explicit `Set` over the limit fails with `ErrValueTooLarge`,
and a loaded value over it is returned but not cached — and on the way out, as
above.

## Consequences
- Each entry costs 11 bytes plus the codec id (4 for `json`) of header.
- An entry written before this format (the C2 tag byte) or by any future
  version is a miss: a cold start, not a failure (RELEASING.md).
- Changing the codec of a deployed cache turns every existing entry into a
  miss rather than a decode error.
- The version byte is the one place a future format change is declared; a
  version 2 reader may choose to read version 1, never the reverse.

## Open
None. Stale-while-revalidate itself is RF-17, post-MVP.
