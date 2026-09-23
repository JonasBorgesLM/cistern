// Package cistern is a two-level cache-aside library: an in-process L1 and an
// optional shared L2, with load coalescing, negative caching, and invalidation
// by key, by tag and by event.
//
// Its requirements are in REQUIREMENTS.md, its threat model in
// docs/THREAT-MODEL.md, and its decisions in docs/adr.
//
// A cache is never a source of truth. Every read path is fail-open: when L2 is
// unavailable, cistern degrades to L1 and the loader rather than failing the
// read (RNF-02).
package cistern
