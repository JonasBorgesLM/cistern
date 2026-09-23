//go:build integration

package redisstore_test

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/JonasBorgesLM/cistern/bus"
	"github.com/JonasBorgesLM/cistern/redisstore"
)

// proxy forwards a local port to target once opened; until then nothing
// listens on the port, so connections are refused.
type proxy struct {
	addr   string
	target string
	mu     sync.Mutex
	ln     net.Listener
	conns  []net.Conn
}

func newProxy(t *testing.T, target string) *proxy {
	t.Helper()
	ln, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &proxy{addr: ln.Addr().String(), target: target}
	_ = ln.Close()
	t.Cleanup(p.close)
	return p
}

func (p *proxy) open(t *testing.T) {
	t.Helper()
	ln, err := new(net.ListenConfig).Listen(context.Background(), "tcp", p.addr)
	if err != nil {
		t.Fatalf("reopening %s: %v", p.addr, err)
	}
	p.mu.Lock()
	p.ln = ln
	p.mu.Unlock()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			up, err := new(net.Dialer).DialContext(context.Background(), "tcp", p.target)
			if err != nil {
				_ = c.Close()
				continue
			}
			p.mu.Lock()
			p.conns = append(p.conns, c, up)
			p.mu.Unlock()
			go func() { _, _ = io.Copy(up, c) }()
			go func() { _, _ = io.Copy(c, up) }()
		}
	}()
}

func (p *proxy) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ln != nil {
		_ = p.ln.Close()
	}
	for _, c := range p.conns {
		_ = c.Close()
	}
}

// #117: a subscription made while Redis is unreachable starts delivering once
// Redis is reachable — go-redis reconnects and resubscribes — so a replica
// that started during an outage does not stay deaf after it.
// Negative control: verified failing with Subscribe returning the dial error.
func TestSubscriptionMadeDuringAnOutageDeliversOnceRedisIsBack(t *testing.T) {
	r := startRedis(t)
	p := newProxy(t, r.addr)

	c := redis.NewClient(&redis.Options{Addr: p.addr, ContextTimeoutEnabled: true, MaxRetries: -1})
	t.Cleanup(func() { _ = c.Close() })
	b, err := redisstore.NewBus(c)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan bus.Event, 16)
	unsubscribe, err := b.Subscribe(func(e bus.Event) { got <- e })
	if err != nil {
		t.Fatalf("Subscribe while Redis is unreachable: %v", err)
	}
	defer unsubscribe()

	p.open(t)
	publisher := r.busFor(t, nil)
	want := bus.Event{Namespace: "tasks", Kind: bus.KindTag, Name: "user:42:lists"}
	deadline := time.After(15 * time.Second)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case e := <-got:
			if e != want {
				t.Fatalf("received %+v", e)
			}
			return
		case <-tick.C:
			_ = publisher.Publish(context.Background(), want) // until the resubscription is in place
		case <-deadline:
			t.Fatal("no event delivered 15s after Redis became reachable")
		}
	}
}
