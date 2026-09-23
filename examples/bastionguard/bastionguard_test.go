package bastionguard_test

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JonasBorgesLM/bastion"
	"github.com/redis/go-redis/v9"

	"github.com/JonasBorgesLM/cistern"
	"github.com/JonasBorgesLM/cistern/examples/bastionguard"
	"github.com/JonasBorgesLM/cistern/redisstore"
)

// unreachable returns an address that accepts connections and never answers,
// counting the connections it gets.
func unreachable(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	ln, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var conns atomic.Int32
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
	return ln.Addr().String(), &conns
}

// RNF-04, sapper scenario 3 in miniature: once the breaker opens, reads stop
// paying Redis's timeout — they are refused before any connection — and the
// cache keeps serving from the loader (ADR-0002).
// Negative control: verified failing with the Guard calling Redis directly.
func TestOpenBreakerKeepsReadsOffRedis(t *testing.T) {
	addr, conns := unreachable(t)
	client := redis.NewClient(&redis.Options{Addr: addr, ContextTimeoutEnabled: true, MaxRetries: -1, Protocol: 2, DisableIdentity: true})
	t.Cleanup(func() { _ = client.Close() })
	breaker, err := bastion.New("redis-l2", bastion.WithFailureThreshold(3), bastion.WithOpenTimeout(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	l2, err := redisstore.New(client, redisstore.WithTimeout(30*time.Millisecond), redisstore.WithGuard(bastionguard.Guard{Breaker: breaker}))
	if err != nil {
		t.Fatal(err)
	}
	var refused atomic.Int32
	cache, err := cistern.New[string, string]("tasks", func(k string) string { return k },
		cistern.WithL2(l2), cistern.WithTTL(time.Minute),
		cistern.WithHooks(cistern.Hooks{OnError: func(_ context.Context, e cistern.ErrorEvent) {
			if errors.Is(e.Err, bastion.ErrOpenState) {
				refused.Add(1)
			}
		}}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	load := func(context.Context) (string, error) { return "from source", nil }

	for range 3 { // enough failures to trip the breaker
		if v, err := cache.GetOrLoad(ctx, "k", load); err != nil || v != "from source" {
			t.Fatalf("GetOrLoad = %q, %v", v, err)
		}
	}
	if breaker.State() != bastion.StateOpen {
		t.Fatalf("breaker is %v after repeated timeouts, want open", breaker.State())
	}
	before := conns.Load()
	start := time.Now()
	for range 20 {
		if v, err := cache.GetOrLoad(ctx, "k", load); err != nil || v != "from source" {
			t.Fatalf("GetOrLoad with the breaker open = %q, %v", v, err)
		}
	}
	if elapsed := time.Since(start); elapsed > 20*30*time.Millisecond/2 {
		t.Fatalf("20 reads with the breaker open took %v; they should not wait on Redis", elapsed)
	}
	if conns.Load() != before {
		t.Fatalf("Redis received %d new connections with the breaker open", conns.Load()-before)
	}
	if refused.Load() == 0 {
		t.Fatal("no read reported bastion.ErrOpenState through OnError")
	}
}
