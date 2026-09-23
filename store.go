package cistern

import (
	"context"
	"time"
)

// Store is one cache level: in-process (the memory package) or shared (the
// redisstore module). Both levels hold encoded bytes behind this one
// interface (ADR-0014), so any implementation can be checked against the
// same conformance suite (RF-14).
//
// Keys are physical keys already built and validated by Cache (ADR-0008).
// Implementations must be safe for concurrent use, must honour ctx, must not
// retain the value slice passed to Set, and must not let a caller's changes to
// a slice returned by Get reach the stored entry.
type Store interface {
	// Get returns the value stored under key. ok is false when there is no
	// live entry; that is not an error. An error means the store could not
	// answer.
	Get(ctx context.Context, key string) (value []byte, ok bool, err error)

	// Set stores value under key, replacing any existing entry, for ttl.
	// ttl must be positive.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error

	// Delete removes the entry under key. Deleting a missing key is not an
	// error.
	Delete(ctx context.Context, key string) error
}
