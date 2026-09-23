// Package singleflight coalesces concurrent loads of the same key into one
// call (RF-05), with the semantics ADR-0007 records: the load runs detached
// from any single caller, is bounded by a timeout after which the key is free
// again, and a panic reaches every caller still waiting.
package singleflight

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"time"
)

// Group coalesces calls by key. The zero value is ready to use.
type Group[T any] struct {
	mu    sync.Mutex
	calls map[string]*call[T]
}

type call[T any] struct {
	done    chan struct{}   // closed when fn has returned or panicked
	expired <-chan struct{} // closed when the load's context ends
	val     T
	err     error
	panic   *PanicError
	waiting int
}

// PanicError is what a waiting caller panics with when the load panicked.
type PanicError struct {
	Value any    // the value the load panicked with
	Stack []byte // the stack of the load goroutine at the panic
}

func (p *PanicError) Error() string {
	return fmt.Sprintf("singleflight: load panicked: %v\n\n%s", p.Value, p.Stack)
}

// Do returns the result of fn for key, calling it at most once among
// concurrent callers of the same key.
//
// fn runs in its own goroutine on a context detached from ctx's cancellation
// but carrying its values, bounded by timeout. Each caller returns when fn
// does, when its own ctx is done, or when the load times out, whichever comes
// first; a caller returning early does not cancel fn. Once the load times
// out, the next caller starts a fresh load even if fn has not returned.
func (g *Group[T]) Do(ctx context.Context, key string, timeout time.Duration, fn func(context.Context) (T, error)) (T, error) {
	g.mu.Lock()
	if g.calls == nil {
		g.calls = make(map[string]*call[T])
	}
	c, inFlight := g.calls[key]
	// A flight whose load has timed out is replaced rather than joined, even
	// if its loader never returns: joining would fail at once, forever.
	if inFlight && closed(c.expired) {
		inFlight = false
	}
	if !inFlight {
		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		c = &call[T]{done: make(chan struct{}), expired: loadCtx.Done()}
		g.calls[key] = c
		go g.run(key, c, loadCtx, cancel, fn)
	}
	c.waiting++
	g.mu.Unlock()

	defer func() {
		g.mu.Lock()
		c.waiting--
		g.mu.Unlock()
	}()

	var zero T
	select {
	case <-c.done:
		return c.result()
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-c.expired:
		// The load's context also ends when fn returns, and done is closed
		// before that, so prefer a result that is already there.
		select {
		case <-c.done:
			return c.result()
		default:
			return zero, fmt.Errorf("singleflight: load timed out after %v: %w", timeout, context.DeadlineExceeded)
		}
	}
}

// Waiting reports how many callers are currently waiting on the load of key.
// It exists so tests can know that callers have joined a flight without
// guessing with sleeps.
func (g *Group[T]) Waiting(key string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if c, ok := g.calls[key]; ok {
		return c.waiting
	}
	return 0
}

func (g *Group[T]) run(key string, c *call[T], ctx context.Context, cancel context.CancelFunc, fn func(context.Context) (T, error)) {
	// Deferred in reverse: done is closed first, then the key is forgotten,
	// then the context is cancelled — so a waiter woken by the cancellation
	// always finds the result already in place.
	defer cancel()
	defer g.forget(key, c)
	defer func() {
		if r := recover(); r != nil {
			c.panic = &PanicError{Value: r, Stack: debug.Stack()}
		}
		close(c.done)
	}()
	c.val, c.err = fn(ctx)
}

// forget removes c from the group if it is still the flight for key.
func (g *Group[T]) forget(key string, c *call[T]) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.calls[key] == c {
		delete(g.calls, key)
	}
}

func closed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func (c *call[T]) result() (T, error) {
	if c.panic != nil {
		panic(c.panic)
	}
	return c.val, c.err
}
