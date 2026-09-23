package cistern

// NoCache marks a type whose values must never be cached (RS-04): secrets,
// tokens, sessions. A cache whose value type implements it — directly or
// through a pointer — cannot be built, and Set and GetOrLoad refuse such
// values behind an interface value type, with ErrUncacheable.
//
// The mark is a guard rail, not a classifier: a secret inside a type that
// does not implement NoCache is not detected (docs/THREAT-MODEL.md, T-03).
type NoCache interface {
	NoCache()
}

// uncacheableType reports whether V, or *V, implements NoCache.
func uncacheableType[V any]() bool {
	var zero V
	_, direct := any(zero).(NoCache)
	_, viaPointer := any(&zero).(NoCache)
	return direct || viaPointer
}

// uncacheable reports whether the dynamic value v implements NoCache.
func uncacheable(v any) bool {
	_, ok := v.(NoCache)
	return ok
}
