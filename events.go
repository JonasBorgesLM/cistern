package cistern

import (
	"context"
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
		if c.l1 == nil || !strings.HasPrefix(e.Name, c.prefix) {
			return
		}
		if err := c.l1.Delete(ctx, e.Name); err != nil {
			return // best-effort: the L1 TTL still bounds staleness (RNF-11)
		}
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
				return // best-effort, as above
			}
		}
	}
}
