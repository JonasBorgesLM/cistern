module github.com/JonasBorgesLM/cistern

// Go 1.24 is the floor promised to importers (RNF-01, ADR-0013), not the
// newest available. It moves only when something concrete makes it
// non-viable, and that reasoning is written as a superseding ADR before this
// line changes.
//
// This module has no `require` block, and it never gains one: the core
// admits no dependencies at all, first-party or third-party (ADR-0001). The
// `dependency-policy` job in .github/workflows/ci.yml enforces that
// mechanically.
go 1.24
