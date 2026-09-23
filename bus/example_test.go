package bus_test

import (
	"context"
	"fmt"

	"github.com/JonasBorgesLM/cistern/bus"
)

// Several caches in one process can share a Local bus; across processes,
// redisstore.NewBus carries the same events over Redis Pub/Sub. Events name
// what to invalidate and never carry a value (RS-07).
func ExampleNewLocal() {
	b := bus.NewLocal()
	unsubscribe, err := b.Subscribe(func(e bus.Event) {
		fmt.Printf("invalidate %s in %s\n", e.Name, e.Namespace)
	})
	if err != nil {
		panic(err)
	}
	defer unsubscribe()

	_ = b.Publish(context.Background(), bus.Event{Namespace: "tasks", Kind: bus.KindTag, Name: "user:42:lists"})
	// Output: invalidate user:42:lists in tasks
}
