package cisterntest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/JonasBorgesLM/cistern/bus"
)

// RunBus runs the Bus conformance suite (RF-11, ADR-0006). newBus returns two
// handles on one medium — two instances of a network Bus, or the same Local
// twice — so delivery between instances is what is tested. A Bus must be
// ready to receive once Subscribe returns.
func RunBus(t *testing.T, newBus func(t *testing.T) (a, b bus.Bus)) {
	t.Helper()
	for _, tc := range []struct {
		name string
		run  func(*testing.T, bus.Bus, bus.Bus)
	}{
		{"DeliversAcrossInstances", deliversAcrossInstances},
		{"DeliversToEverySubscriber", deliversToEverySubscriber},
		{"UnsubscribeStopsDelivery", unsubscribeStopsDelivery},
		{"CancelledPublishIsAnError", cancelledPublishIsAnError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := newBus(t)
			tc.run(t, a, b)
		})
	}
}

var (
	tagEvent = bus.Event{Namespace: "cisterntest", Kind: bus.KindTag, Name: "user:42:lists"}
	keyEvent = bus.Event{Namespace: "cisterntest", Kind: bus.KindKey, Name: keyA}
)

// collector records events delivered to a handler.
type collector struct {
	mu     sync.Mutex
	events []bus.Event
	got    chan struct{}
}

func newCollector() *collector { return &collector{got: make(chan struct{}, 64)} }

func (c *collector) handle(e bus.Event) {
	c.mu.Lock()
	c.events = append(c.events, e)
	c.mu.Unlock()
	c.got <- struct{}{}
}

func (c *collector) waitFor(t *testing.T, n int) []bus.Event {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for range n {
		select {
		case <-c.got:
		case <-deadline:
			t.Fatalf("waited 5s for %d events", n)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]bus.Event(nil), c.events...)
}

func subscribe(t *testing.T, b bus.Bus, h bus.Handler) func() {
	t.Helper()
	unsubscribe, err := b.Subscribe(h)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(unsubscribe)
	return unsubscribe
}

func publish(t *testing.T, b bus.Bus, e bus.Event) {
	t.Helper()
	if err := b.Publish(context.Background(), e); err != nil {
		t.Fatalf("Publish: %v", err)
	}
}

// Events arrive unchanged, whatever their kind.
func deliversAcrossInstances(t *testing.T, a, b bus.Bus) {
	c := newCollector()
	subscribe(t, b, c.handle)
	publish(t, a, tagEvent)
	publish(t, a, keyEvent)
	got := c.waitFor(t, 2)
	seen := map[bus.Event]bool{}
	for _, e := range got {
		seen[e] = true
	}
	if !seen[tagEvent] || !seen[keyEvent] {
		t.Fatalf("received %+v, want %+v and %+v", got, tagEvent, keyEvent)
	}
}

func deliversToEverySubscriber(t *testing.T, a, b bus.Bus) {
	one, two := newCollector(), newCollector()
	subscribe(t, b, one.handle)
	subscribe(t, b, two.handle)
	publish(t, a, tagEvent)
	one.waitFor(t, 1)
	two.waitFor(t, 1)
}

// After unsubscribing, a handler gets nothing. Proven by delivering a later
// event to a handler that stayed, so the test does not rely on a timeout.
func unsubscribeStopsDelivery(t *testing.T, a, b bus.Bus) {
	gone, stayed := newCollector(), newCollector()
	unsubscribe := subscribe(t, b, gone.handle)
	subscribe(t, b, stayed.handle)
	unsubscribe()
	unsubscribe()
	publish(t, a, tagEvent)
	publish(t, a, keyEvent)
	stayed.waitFor(t, 2)
	gone.mu.Lock()
	defer gone.mu.Unlock()
	if len(gone.events) != 0 {
		t.Fatalf("an unsubscribed handler received %+v", gone.events)
	}
}

func cancelledPublishIsAnError(t *testing.T, a, _ bus.Bus) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.Publish(ctx, tagEvent); err == nil {
		t.Fatal("Publish with a cancelled context = nil error")
	}
}
