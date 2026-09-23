package crierhooks_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/JonasBorgesLM/crier/core"

	"github.com/JonasBorgesLM/cistern"
	"github.com/JonasBorgesLM/cistern/examples/crierhooks"
)

type collector struct {
	mu      sync.Mutex
	records []core.LogRecord
}

func (c *collector) Export(_ context.Context, batch []core.LogRecord) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, batch...)
	return nil
}

func (*collector) Shutdown(context.Context) error { return nil }

// downStore is an L2 that cannot answer.
type downStore struct{}

var errDown = errors.New("dial tcp: connection refused")

func (downStore) Get(context.Context, string) (value []byte, ok bool, err error) {
	return nil, false, errDown
}
func (downStore) Set(context.Context, string, []byte, time.Duration) error { return errDown }
func (downStore) Delete(context.Context, string) error                     { return errDown }

// RF-15: a swallowed L2 failure reaches crier as a warning — without the key,
// which usually embeds a user id (RS-08).
func TestSwallowedErrorsBecomeWarnings(t *testing.T) {
	sink := &collector{}
	c, err := core.New(core.Options{ServiceName: "task-api", Exporters: map[string]core.Exporter{"test": sink}, BatchWindow: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	cache, err := cistern.New[string, string]("tasks", func(k string) string { return k },
		cistern.WithL2(downStore{}), cistern.WithTTL(time.Minute), cistern.WithHooks(crierhooks.Hooks(c)))
	if err != nil {
		t.Fatal(err)
	}

	if _, ok, err := cache.Get(context.Background(), "user:42:list:1"); ok || err != nil {
		t.Fatalf("Get = %v, %v; want a plain miss (fail-open)", ok, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.records) != 1 {
		t.Fatalf("crier received %d records, want 1", len(sink.records))
	}
	r := sink.records[0]
	if r.Severity != core.SeverityWarn || r.Attributes["cistern.op"] != "read" || r.Attributes["cistern.level"] != "L2" || r.Attributes["cistern.namespace"] != "tasks" {
		t.Fatalf("record = %+v", r)
	}
	if _, leaked := r.Attributes["cistern.key"]; leaked {
		t.Fatal("the key reached the log without WithHookKeys")
	}
}
