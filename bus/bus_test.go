package bus_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/JonasBorgesLM/cistern/bus"
)

var tagEvent = bus.Event{Namespace: "tasks", Kind: bus.KindTag, Name: "user:42:lists"}

func TestLocalDeliversToEverySubscriber(t *testing.T) {
	b := bus.NewLocal()
	var mu sync.Mutex
	var got []string
	for _, who := range []string{"a", "b"} {
		if _, err := b.Subscribe(func(e bus.Event) {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, who+":"+e.Name)
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Publish(context.Background(), tagEvent); err != nil {
		t.Fatal(err)
	}
	slices.Sort(got) // delivery order between subscribers is not a contract
	if strings.Join(got, ",") != "a:user:42:lists,b:user:42:lists" {
		t.Fatalf("delivered %v, want both subscribers once", got)
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	b := bus.NewLocal()
	var n int
	unsubscribe, err := b.Subscribe(func(bus.Event) { n++ })
	if err != nil {
		t.Fatal(err)
	}
	unsubscribe()
	unsubscribe() // idempotent
	if err := b.Publish(context.Background(), tagEvent); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("an unsubscribed handler ran %d times", n)
	}
}

func TestPublishHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := bus.NewLocal().Publish(ctx, tagEvent); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	for _, e := range []bus.Event{
		tagEvent,
		{Namespace: "tasks", Kind: bus.KindKey, Name: "cistern:v1:tasks:-:k:user:42:list:1"},
	} {
		data, err := bus.Encode(e)
		if err != nil {
			t.Fatal(err)
		}
		got, err := bus.Decode(data)
		if err != nil || got != e {
			t.Fatalf("Decode(Encode(%+v)) = %+v, %v", e, got, err)
		}
	}
}

// RS-07: a message from the network is untrusted. Anything that is not a
// well-formed invalidation event is rejected, never acted on or panicked on.
func TestDecodeRejectsMalformedMessages(t *testing.T) {
	for name, data := range map[string]string{
		"empty":             ``,
		"not JSON":          `hello`,
		"unknown version":   `{"v":2,"k":"tag","ns":"tasks","n":"x"}`,
		"unknown kind":      `{"v":1,"k":"value","ns":"tasks","n":"x"}`,
		"carries a value":   `{"v":1,"k":"tag","ns":"tasks","n":"x","value":"secret"}`,
		"bad namespace":     `{"v":1,"k":"tag","ns":"Tasks:x","n":"x"}`,
		"empty name":        `{"v":1,"k":"tag","ns":"tasks","n":""}`,
		"control character": `{"v":1,"k":"tag","ns":"tasks","n":"a\nb"}`,
		"trailing data":     `{"v":1,"k":"tag","ns":"tasks","n":"x"} {}`,
		"too large":         `{"v":1,"k":"tag","ns":"tasks","n":"` + strings.Repeat("x", 2000) + `"}`,
		"missing fields":    `{"v":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			if e, err := bus.Decode([]byte(data)); !errors.Is(err, bus.ErrMalformed) {
				t.Fatalf("Decode = %+v, %v; want ErrMalformed", e, err)
			}
		})
	}
}

func TestEncodeRejectsInvalidEvents(t *testing.T) {
	for name, e := range map[string]bus.Event{
		"no kind":      {Namespace: "tasks", Name: "x"},
		"no namespace": {Kind: bus.KindTag, Name: "x"},
		"no name":      {Namespace: "tasks", Kind: bus.KindTag},
	} {
		if _, err := bus.Encode(e); err == nil {
			t.Errorf("%s: Encode = nil error", name)
		}
	}
}

func FuzzDecode(f *testing.F) {
	good, _ := bus.Encode(tagEvent)
	f.Add(good)
	f.Add([]byte(`{"v":1,"k":"key","ns":"tasks","n":"cistern:v1:tasks:-:k:a"}`))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		e, err := bus.Decode(data)
		if err != nil {
			if !errors.Is(err, bus.ErrMalformed) {
				t.Fatalf("error %v does not wrap ErrMalformed", err)
			}
			return
		}
		if _, err := bus.Encode(e); err != nil {
			t.Fatalf("Decode accepted an event Encode refuses: %+v", e)
		}
	})
}

