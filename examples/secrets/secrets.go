// Package secrets shows how to keep secrets out of a cache (RS-04, T-03).
//
// moat's secret.Value redacts itself through every encoding path, JSON
// included, so a struct holding one does not leak into a cache. It does not
// work either: the entry is written with "[REDACTED]" in place of the secret,
// which does not decode back into a secret.Value, so every read misses and
// reports a decode error. The cache does useless work, quietly. The fix is to
// never cache it: mark the type NoCache and cistern refuses it at
// construction.
package secrets

import "github.com/JonasBorgesLM/moat/secret"

// Credential holds a secret and is marked NoCache, so no cistern.Cache can be
// built for it.
type Credential struct {
	Service string       `json:"service"`
	Token   secret.Value `json:"token"`
}

// NoCache marks Credential as never cacheable (cistern.NoCache).
func (Credential) NoCache() {}
