package redisstore_test

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/JonasBorgesLM/cistern"
	"github.com/JonasBorgesLM/cistern/bus"
	"github.com/JonasBorgesLM/cistern/redisstore"
)

var (
	_ cistern.TagStore = (*redisstore.Store)(nil)
	_ bus.Bus          = (*redisstore.Bus)(nil)
)

// blackhole accepts connections and never answers: the slowest Redis there
// is. It counts connections so a test can prove none was made.
func blackhole(t *testing.T) (addr string, conns *atomic.Int32) {
	t.Helper()
	ln, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	conns = new(atomic.Int32)
	var open []net.Conn
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns.Add(1)
			open = append(open, c)
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-done
		for _, c := range open {
			_ = c.Close()
		}
	})
	return ln.Addr().String(), conns
}

func client(t *testing.T, addr string, contextTimeouts bool) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{
		Addr:                  addr,
		ContextTimeoutEnabled: contextTimeouts,
		Protocol:              2,
		DisableIdentity:       true,
		MaxRetries:            -1,
	})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestNewValidatesItsConfiguration(t *testing.T) {
	addr, _ := blackhole(t)
	ok := client(t, addr, true)
	for name, build := range map[string]func() (*redisstore.Store, error){
		"nil client": func() (*redisstore.Store, error) { return redisstore.New(nil) },
		// ADR-0012: without ContextTimeoutEnabled the per-call timeout would
		// silently not exist.
		// Negative control: verified failing with the check removed.
		"client ignoring context deadlines": func() (*redisstore.Store, error) {
			return redisstore.New(client(t, addr, false))
		},
		"zero timeout":     func() (*redisstore.Store, error) { return redisstore.New(ok, redisstore.WithTimeout(0)) },
		"negative timeout": func() (*redisstore.Store, error) { return redisstore.New(ok, redisstore.WithTimeout(-time.Second)) },
		"nil guard":        func() (*redisstore.Store, error) { return redisstore.New(ok, redisstore.WithGuard(nil)) },
	} {
		t.Run(name, func(t *testing.T) {
			s, err := build()
			if !errors.Is(err, cistern.ErrInvalidConfig) || s != nil {
				t.Fatalf("New = %v, %v; want nil, ErrInvalidConfig", s, err)
			}
		})
	}
}

// RNF-03, T-12: a slow Redis costs a read the store's timeout, not the
// client's read timeout and not the request's whole budget.
// Negative control: verified failing with the per-call timeout removed.
func TestSlowRedisIsBoundedByTheTimeout(t *testing.T) {
	addr, _ := blackhole(t)
	s, err := redisstore.New(client(t, addr, true), redisstore.WithTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	for name, op := range map[string]func(context.Context) error{
		"Get":    func(ctx context.Context) error { _, _, err := s.Get(ctx, "k"); return err },
		"Set":    func(ctx context.Context) error { return s.Set(ctx, "k", []byte("v"), time.Minute) },
		"Delete": func(ctx context.Context) error { return s.Delete(ctx, "k") },
	} {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			err := op(context.Background())
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Fatalf("returned after %v, want about the 50ms timeout", elapsed)
			}
			// go-redis reports either the context's deadline or the socket's
			// i/o timeout; which one is not the contract, the bound above is.
			if err == nil {
				t.Fatal("a call to a Redis that never answers returned no error")
			}
		})
	}
}

type refusingGuard struct{ calls atomic.Int32 }

var errOpen = errors.New("breaker open")

func (g *refusingGuard) Do(context.Context, func(context.Context) error) error {
	g.calls.Add(1)
	return errOpen
}

// RNF-04: every Redis call goes through the Guard, so an open breaker keeps
// the store from touching Redis at all.
// Negative control: verified failing with Get calling Redis directly.
func TestGuardWrapsEveryCall(t *testing.T) {
	addr, conns := blackhole(t)
	g := &refusingGuard{}
	s, err := redisstore.New(client(t, addr, true), redisstore.WithGuard(g))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if _, _, err := s.Get(ctx, "k"); !errors.Is(err, errOpen) {
		t.Errorf("Get: err = %v, want the guard's error", err)
	}
	if err := s.Set(ctx, "k", []byte("v"), time.Minute); !errors.Is(err, errOpen) {
		t.Errorf("Set: err = %v, want the guard's error", err)
	}
	if err := s.Delete(ctx, "k"); !errors.Is(err, errOpen) {
		t.Errorf("Delete: err = %v, want the guard's error", err)
	}
	if _, _, _, err := s.GetTagged(ctx, "k", []string{"g"}, time.Minute); !errors.Is(err, errOpen) {
		t.Errorf("GetTagged: err = %v, want the guard's error", err)
	}
	if err := s.Bump(ctx, []string{"g"}, time.Minute); !errors.Is(err, errOpen) {
		t.Errorf("Bump: err = %v, want the guard's error", err)
	}
	if g.calls.Load() != 5 {
		t.Errorf("guard saw %d calls, want 5", g.calls.Load())
	}
	if conns.Load() != 0 {
		t.Errorf("Redis received %d connections behind an open guard, want 0", conns.Load())
	}
}

// callerCtxGuard records, after each call, whether the context it was given
// had ended, and what the call returned.
type callerCtxGuard struct {
	guardCtxDone atomic.Bool
	opFailed     atomic.Bool
}

func (g *callerCtxGuard) Do(ctx context.Context, op func(context.Context) error) error {
	err := op(ctx)
	g.guardCtxDone.Store(ctx.Err() != nil)
	g.opFailed.Store(err != nil)
	return err
}

// RNF-03 with RNF-04: the per-call timeout is a failure of the dependency, not
// a cancellation by the caller, so a Guard must see it as the operation's
// error while its own context is still live. A breaker such as bastion treats
// a call whose context has ended as neither success nor failure; given the
// timeout on its context, it would never open on a slow Redis.
// Negative control: verified failing with the timeout on the Guard's context.
func TestTimeoutIsTheOperationsNotTheCallers(t *testing.T) {
	addr, _ := blackhole(t)
	g := &callerCtxGuard{}
	s, err := redisstore.New(client(t, addr, true), redisstore.WithTimeout(30*time.Millisecond), redisstore.WithGuard(g))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Get(context.Background(), "k"); err == nil {
		t.Fatal("Get against a Redis that never answers returned no error")
	}
	if !g.opFailed.Load() {
		t.Fatal("the operation the Guard ran did not fail")
	}
	if g.guardCtxDone.Load() {
		t.Fatal("the Guard's own context had ended: a breaker would not count this timeout")
	}
}

func TestSetRejectsNonPositiveTTLWithoutCallingRedis(t *testing.T) {
	addr, conns := blackhole(t)
	s, err := redisstore.New(client(t, addr, true))
	if err != nil {
		t.Fatal(err)
	}
	for _, ttl := range []time.Duration{0, -time.Second} {
		if err := s.Set(context.Background(), "k", []byte("v"), ttl); err == nil {
			t.Errorf("Set with ttl %v = nil error", ttl)
		}
	}
	if conns.Load() != 0 {
		t.Fatalf("Redis received %d connections for rejected Sets", conns.Load())
	}
}

func TestNewBusValidatesItsClient(t *testing.T) {
	addr, _ := blackhole(t)
	if b, err := redisstore.NewBus(nil); !errors.Is(err, cistern.ErrInvalidConfig) || b != nil {
		t.Errorf("NewBus(nil) = %v, %v", b, err)
	}
	if b, err := redisstore.NewBus(client(t, addr, false)); !errors.Is(err, cistern.ErrInvalidConfig) || b != nil {
		t.Errorf("NewBus without ContextTimeoutEnabled = %v, %v", b, err)
	}
}
