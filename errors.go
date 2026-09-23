package cistern

import "errors"

var (
	// ErrNotFound reports that a value does not exist in the source of truth.
	//
	// A loader returns it (possibly wrapped) to say the key has no value; with
	// negative caching enabled, cistern remembers that absence for a short TTL
	// instead of calling the loader again (RF-06). A read that finds a
	// remembered absence returns it. A plain cache miss is not ErrNotFound.
	ErrNotFound = errors.New("cistern: not found")

	// ErrInvalidConfig reports a cache that cannot be built as configured —
	// for example an empty namespace (RS-01) or an L1 TTL longer than the L2
	// TTL (RF-08). Constructors return it wrapped with the reason; they never
	// panic (RF-16).
	ErrInvalidConfig = errors.New("cistern: invalid configuration")

	// ErrValueTooLarge reports a value whose encoding exceeds the cache's
	// value limit (WithMaxValueBytes, RS-05). Set returns it; GetOrLoad
	// returns the loaded value without caching it instead.
	ErrValueTooLarge = errors.New("cistern: value too large")
)
