package cistern

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/JonasBorgesLM/cistern/bus"
	"github.com/JonasBorgesLM/cistern/internal/envelope"
)

// slot is where a key lives: its physical key and, in a tagged cache, the keys
// of its tags' generation counters (sorted, de-duplicated).
type slot struct {
	key     string // the consumer key, for hook events
	pk      string
	genKeys []string
}

func (c *Cache[K, V]) slotFor(k K) (slot, error) {
	pk, err := c.physicalKey(k)
	if err != nil {
		return slot{}, err
	}
	if c.tagsFn == nil {
		return slot{key: c.key(k), pk: pk}, nil
	}
	tags := slices.Clone(c.tagsFn(k))
	slices.Sort(tags)
	tags = slices.Compact(tags)
	if len(tags) > envelope.MaxTags {
		return slot{}, fmt.Errorf("%w: %d tags exceeds %d", ErrInvalidKey, len(tags), envelope.MaxTags)
	}
	genKeys := make([]string, len(tags))
	for i, tag := range tags {
		if genKeys[i], err = c.genKey(tag); err != nil {
			return slot{}, err
		}
	}
	return slot{key: c.key(k), pk: pk, genKeys: genKeys}, nil
}

// genKey validates tag like a consumer key and returns its counter's key,
// cistern:v1:<namespace>:g:<tag> (ADR-0008 as amended by ADR-0005).
func (c *Cache[K, V]) genKey(tag string) (string, error) {
	if err := validateKey(tag); err != nil {
		return "", fmt.Errorf("tag: %w", err)
	}
	if len(tag) > MaxKeyBytes {
		return "", fmt.Errorf("%w: tag of %d bytes exceeds %d", ErrInvalidKey, len(tag), MaxKeyBytes)
	}
	return c.genPrefix + tag, nil
}

// InvalidateTag retires every entry carrying tag, in every replica, without
// scanning (RF-10, ADR-0005): it advances the tag's generation counter, and
// entries that recorded an older generation read as misses from then on.
//
// Like Delete, it fails loud (ADR-0002). It returns ErrInvalidConfig on a
// cache built without WithTags and ErrInvalidKey for an invalid tag.
func (c *Cache[K, V]) InvalidateTag(ctx context.Context, tag string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.tagsFn == nil {
		return fmt.Errorf("%w: InvalidateTag on a cache built without WithTags", ErrInvalidConfig)
	}
	gk, err := c.genKey(tag)
	if err != nil {
		return err
	}
	bumpErr := c.auth.Bump(ctx, []string{gk}, c.genTTL)
	if c.gens != nil {
		c.gens.drop(gk)
	}
	if bumpErr != nil {
		bumpErr = fmt.Errorf("cistern: invalidating tag: %w", bumpErr)
	}
	err = errors.Join(bumpErr, c.publish(ctx, bus.KindTag, tag))
	if err == nil {
		c.onInvalidate(ctx, "", tag, false)
	}
	return err
}

// readTagged reads a tagged entry: usable only when the generations it
// recorded are its tags' current ones. It also returns those current
// generations — what a write after a miss must record (ADR-0005, ADR-0011) —
// or nil if they could not be read, in which case nothing should be cached.
func (c *Cache[K, V]) readTagged(ctx context.Context, s slot) (value V, ok bool, gens []uint64, err error) {
	// With two levels, an L1 hit is checked against the local copy of the
	// generations, without a round trip.
	var local envelope.Entry
	var haveLocal bool
	if c.gens != nil {
		data, hit, getErr := c.l1.Get(ctx, s.pk)
		if ctx.Err() != nil {
			return value, false, nil, ctx.Err()
		}
		switch {
		case getErr != nil:
			c.onError(ctx, s.key, OpRead, LevelL1, getErr)
		case hit:
			local, haveLocal = c.checked(ctx, s.key, LevelL1, data)
		}
		if cached, fresh := c.gens.get(s.genKeys); haveLocal && fresh && slices.Equal(local.Gens, cached) {
			if v, found, err := c.served(ctx, s.key, LevelL1, local); err == nil || errors.Is(err, ErrNotFound) {
				return v, found, cached, err
			}
		}
	}

	authLevel := LevelL1
	if c.l2 != nil {
		authLevel = LevelL2
	}
	var since genEpoch
	if c.gens != nil {
		since = c.gens.epoch(s.genKeys)
	}
	data, hit, current, getErr := c.auth.GetTagged(ctx, s.pk, s.genKeys, c.genTTL)
	if ctx.Err() != nil {
		return value, false, nil, ctx.Err()
	}
	if getErr != nil {
		// A miss with no generations to record, so nothing will be cached.
		c.onError(ctx, s.key, OpRead, authLevel, getErr)
		c.onMiss(ctx, s.key)
		return value, false, nil, nil // reads fail open (ADR-0002)
	}
	if c.gens != nil {
		c.gens.put(s.genKeys, current, since)
	}
	if haveLocal && slices.Equal(local.Gens, current) {
		if v, found, err := c.served(ctx, s.key, LevelL1, local); err == nil || errors.Is(err, ErrNotFound) {
			return v, found, current, err
		}
	}
	if hit {
		if e, ok := c.checked(ctx, s.key, authLevel, data); ok && slices.Equal(e.Gens, current) {
			if v, found, err := c.served(ctx, s.key, authLevel, e); err == nil || errors.Is(err, ErrNotFound) {
				if c.gens != nil {
					c.backfill(ctx, s, data, e.Expires)
				}
				return v, found, current, err
			}
		}
	}
	c.onMiss(ctx, s.key)
	return value, false, current, nil
}

// checked decodes data read from level as an unexpired entry written with
// this cache's codec. Anything else is unusable; all but expiry is reported
// as a decode error (RS-06).
func (c *Cache[K, V]) checked(ctx context.Context, key string, level Level, data []byte) (envelope.Entry, bool) {
	e, err := envelope.Decode(data, c.maxValue)
	if err == nil && e.Codec != c.codecID {
		err = fmt.Errorf("cistern: entry written with codec %q, not %q", e.Codec, c.codecID)
	}
	if err != nil {
		c.onError(ctx, key, OpDecode, level, err)
		return envelope.Entry{}, false
	}
	if !c.now().Before(e.Expires) {
		return envelope.Entry{}, false
	}
	return e, true
}

// served returns the value an entry holds, or ErrNotFound for a remembered
// absence, and reports the hit; a payload that does not decode is reported
// and returned as an error.
func (c *Cache[K, V]) served(ctx context.Context, key string, level Level, e envelope.Entry) (value V, ok bool, err error) {
	if e.Absent {
		c.onHit(ctx, key, level, true)
		return value, false, ErrNotFound
	}
	v, err := c.decode(e.Payload)
	if err != nil {
		c.onError(ctx, key, OpDecode, level, err)
		return value, false, err
	}
	c.onHit(ctx, key, level, false)
	return v, true, nil
}
