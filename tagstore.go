package cistern

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"time"
)

// TagStore is a Store that also keeps tag generation counters, which tag
// invalidation needs from the cache's authoritative level: L2 when there is
// one, else L1 (RF-10, ADR-0005).
//
// Counters are addressed by keys the Cache builds. A missing counter — never
// written, expired or evicted — must be created at an unpredictable value in
// [1, 2^62) (NewGeneration), never at a small or fixed one: an evicted counter
// that restarted low could repeat a generation an old entry recorded, and
// resurrect it (T-14).
type TagStore interface {
	Store

	// GetTagged returns the value stored under key, as Get does, together with
	// the current generation of each counter in genKeys, in order. A missing
	// counter is created, with genTTL. Where the store allows, value and
	// generations are read in one round trip.
	GetTagged(ctx context.Context, key string, genKeys []string, genTTL time.Duration) (value []byte, ok bool, gens []uint64, err error)

	// Bump advances each counter in genKeys to a value it has never held,
	// creating a missing one as GetTagged does, and sets its TTL to genTTL.
	Bump(ctx context.Context, genKeys []string, genTTL time.Duration) error
}

// NewGeneration returns an unpredictable generation in [1, 2^62), the value a
// TagStore gives a counter it creates (ADR-0005). The range leaves room for
// more increments than will ever happen.
func NewGeneration() (uint64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, fmt.Errorf("cistern: drawing a generation: %w", err)
	}
	return binary.BigEndian.Uint64(b[:])%(1<<62-1) + 1, nil
}
