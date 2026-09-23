package cistern_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JonasBorgesLM/cistern"
)

type sessionToken struct{ Value string }

func (sessionToken) NoCache() {}

type apiKey struct{ Value string }

func (*apiKey) NoCache() {}

// RS-04, T-03: a type marked NoCache cannot be the value type of a cache.
// Negative control: verified failing with the check in New removed.
func TestNewRefusesNoCacheValueTypes(t *testing.T) {
	opts := []cistern.Option{cistern.WithL1(newRecorder()), cistern.WithTTL(time.Minute)}
	if _, err := cistern.New[string, sessionToken]("tasks", stringKey, opts...); !errors.Is(err, cistern.ErrUncacheable) {
		t.Errorf("value receiver: err = %v, want ErrUncacheable", err)
	}
	if _, err := cistern.New[string, apiKey]("tasks", stringKey, opts...); !errors.Is(err, cistern.ErrUncacheable) {
		t.Errorf("pointer receiver: err = %v, want ErrUncacheable", err)
	}
	if _, err := cistern.New[string, *apiKey]("tasks", stringKey, opts...); !errors.Is(err, cistern.ErrUncacheable) {
		t.Errorf("pointer value type: err = %v, want ErrUncacheable", err)
	}
}

// RS-04: behind an interface value type, the check happens per value.
// Negative control: verified failing with the per-value check removed.
func TestNoCacheValuesAreRefusedBehindAnInterface(t *testing.T) {
	ctx := context.Background()
	l2 := newRecorder()
	c, err := cistern.New[string, any]("tasks", stringKey, cistern.WithL2(l2), cistern.WithTTL(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Set(ctx, "k", sessionToken{Value: "secret"}); !errors.Is(err, cistern.ErrUncacheable) {
		t.Errorf("Set: err = %v, want ErrUncacheable", err)
	}
	var calls atomic.Int32
	load := func(context.Context) (any, error) {
		calls.Add(1)
		return &apiKey{Value: "secret"}, nil
	}
	if _, err := c.GetOrLoad(ctx, "k", load); !errors.Is(err, cistern.ErrUncacheable) {
		t.Errorf("GetOrLoad: err = %v, want ErrUncacheable", err)
	}
	if len(l2.keys()) != 0 {
		t.Fatal("a NoCache value reached the store")
	}

	if err := c.Set(ctx, "k", "an ordinary value"); err != nil {
		t.Fatalf("Set of an ordinary value: %v", err)
	}
}
