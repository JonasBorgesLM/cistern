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
	pk      string
	genKeys []string
}

func (c *Cache[K, V]) slotFor(k K) (slot, error) {
	pk, err := c.physicalKey(k)
	if err != nil {
		return slot{}, err
	}
	if c.tagsFn == nil {
		return slot{pk: pk}, nil
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
	return slot{pk: pk, genKeys: genKeys}, nil
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
	return errors.Join(bumpErr, c.publish(ctx, bus.KindTag, tag))
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
		if getErr == nil && hit {
			local, haveLocal = c.usable(data)
		}
		if cached, fresh := c.gens.get(s.genKeys); haveLocal && fresh && slices.Equal(local.Gens, cached) {
			if v, found, err := c.entryValue(local); err == nil || errors.Is(err, ErrNotFound) {
				return v, found, cached, err
			}
		}
	}

	data, hit, current, getErr := c.auth.GetTagged(ctx, s.pk, s.genKeys, c.genTTL)
	if ctx.Err() != nil {
		return value, false, nil, ctx.Err()
	}
	if getErr != nil {
		// A miss with no generations to record, so nothing will be cached.
		return value, false, nil, nil //nolint:nilerr // reads fail open (ADR-0002)
	}
	if c.gens != nil {
		c.gens.put(s.genKeys, current)
	}
	if haveLocal && slices.Equal(local.Gens, current) {
		if v, found, err := c.entryValue(local); err == nil || errors.Is(err, ErrNotFound) {
			return v, found, current, err
		}
	}
	if hit {
		if e, usable := c.usable(data); usable && slices.Equal(e.Gens, current) {
			if v, found, err := c.entryValue(e); err == nil || errors.Is(err, ErrNotFound) {
				if c.gens != nil {
					c.backfill(ctx, s.pk, data, e.Expires)
				}
				return v, found, current, err
			}
		}
	}
	return value, false, current, nil
}

// usable decodes data as an unexpired entry written with this cache's codec.
func (c *Cache[K, V]) usable(data []byte) (envelope.Entry, bool) {
	e, err := envelope.Decode(data, c.maxValue)
	if err != nil || e.Codec != c.codecID || !c.now().Before(e.Expires) {
		return envelope.Entry{}, false
	}
	return e, true
}

// entryValue returns the value an entry holds, ErrNotFound for a remembered
// absence, or a decode error.
func (c *Cache[K, V]) entryValue(e envelope.Entry) (value V, ok bool, err error) {
	if e.Absent {
		return value, false, ErrNotFound
	}
	v, err := c.decode(e.Payload)
	if err != nil {
		return value, false, err
	}
	return v, true, nil
}
