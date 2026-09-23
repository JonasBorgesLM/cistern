package cistern

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/JonasBorgesLM/cistern/bus"
)

// eventTimeout bounds the local work an incoming event causes.
const eventTimeout = time.Second

// Close stops the cache listening to its Bus. It is safe to call more than
// once, and on a cache without a Bus.
func (c *Cache[K, V]) Close() {
	if c.unsubscribe != nil {
		c.unsubscribe()
	}
}

// publish announces an invalidation to other instances, best-effort
// (ADR-0006). Without a Bus it does nothing.
func (c *Cache[K, V]) publish(ctx context.Context, kind bus.Kind, name string) error {
	if c.bus == nil {
		return nil
	}
	if err := c.bus.Publish(ctx, bus.Event{Namespace: c.namespace, Kind: kind, Name: name}); err != nil {
		return fmt.Errorf("cistern: publishing invalidation: %w", err)
	}
	return nil
}

// eventKey returns the consumer key a key event names, and whether the name is
// a physical key this cache could have stored (ADR-0008). The name is
// untrusted (RS-07), so its key is validated like a consumer key before it
// reaches L1 or a hook, where a control character would inject into a log
// line (RS-03, #119). A hashed key has no consumer key to report: "".
func (c *Cache[K, V]) eventKey(name string) (key string, ok bool) {
	rest, ok := strings.CutPrefix(name, c.prefix)
	if !ok {
		return "", false
	}
	if key, ok := strings.CutPrefix(rest, "k:"); ok {
		return key, len(key) <= MaxKeyBytes && validateKey(key) == nil
	}
	if sum, ok := strings.CutPrefix(rest, "h:"); ok {
		_, err := hex.DecodeString(sum)
		return "", len(sum) == 2*sha256.Size && err == nil
	}
	return "", false
}

// onEvent applies another instance's invalidation to this one's local state
// (RF-12). Events are untrusted (RS-07): one for another namespace, naming a
// key outside this cache, or carrying an invalid tag is ignored. Receiving an
// event twice, or one's own, is harmless.
func (c *Cache[K, V]) onEvent(e bus.Event) {
	if e.Namespace != c.namespace {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), eventTimeout)
	defer cancel()
	switch e.Kind {
	case bus.KindKey:
		if c.l1 == nil {
			return
		}
		key, ok := c.eventKey(e.Name)
		if !ok {
			return
		}
		if err := c.l1.Delete(ctx, e.Name); err != nil {
			c.onError(ctx, key, OpEvent, LevelL1, err) // best-effort: the L1 TTL still bounds staleness
			return
		}
		c.onInvalidate(ctx, key, "", true)
	case bus.KindTag:
		if c.tagsFn == nil {
			return
		}
		gk, err := c.genKey(e.Name)
		if err != nil {
			return
		}
		switch {
		case c.gens != nil:
			c.gens.drop(gk)
		case c.l2 == nil:
			// L1 is authoritative here: each replica keeps its own
			// generations, so the event must advance this one's.
			if err := c.auth.Bump(ctx, []string{gk}, c.genTTL); err != nil {
				c.onError(ctx, "", OpEvent, LevelL1, err) // best-effort, as above
				return
			}
		}
		c.onInvalidate(ctx, "", e.Name, true)
	}
}
