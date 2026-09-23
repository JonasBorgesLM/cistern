// Package bus carries invalidation events between cache instances (RF-11,
// ADR-0006). It is best-effort: a lost event costs at most one L1 TTL of
// staleness on the replica that missed it (RNF-11), never correctness. Events
// carry invalidation only — a key or a tag — never a value (RS-07).
package bus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Kind says what an Event invalidates.
type Kind uint8

// The kinds of invalidation.
const (
	KindKey Kind = iota + 1 // Name is a physical key
	KindTag                 // Name is a tag
)

// Event is one invalidation. Receivers only drop local state for it, so
// receiving an event twice, or one's own, is harmless.
type Event struct {
	Namespace string
	Kind      Kind
	Name      string
}

// Handler receives events. It must not block for long: a Bus may deliver
// synchronously.
type Handler func(Event)

// Bus publishes invalidation events and delivers them to subscribers.
// Implementations must be safe for concurrent use.
type Bus interface {
	// Publish sends e to every subscriber, best-effort.
	Publish(ctx context.Context, e Event) error
	// Subscribe registers h and returns a function that unregisters it; the
	// function may be called more than once, concurrently. An unreachable
	// medium is not an error: a cache subscribes in New, and an outage must
	// not stop it from starting (ADR-0006) — delivery begins when the medium
	// is back.
	Subscribe(h Handler) (unsubscribe func(), err error)
}

// Local is an in-process Bus: every Publish is delivered synchronously to
// every current subscriber. It connects several caches in one process, and
// stands in for a network Bus in tests.
type Local struct {
	mu   sync.RWMutex
	next int
	subs map[int]Handler
}

// NewLocal returns an empty Local bus.
func NewLocal() *Local { return &Local{subs: make(map[int]Handler)} }

// Publish delivers e to every subscriber.
func (l *Local) Publish(ctx context.Context, e Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.mu.RLock()
	handlers := make([]Handler, 0, len(l.subs))
	for _, h := range l.subs {
		handlers = append(handlers, h)
	}
	l.mu.RUnlock()
	for _, h := range handlers {
		h(e)
	}
	return nil
}

// Subscribe registers h.
func (l *Local) Subscribe(h Handler) (func(), error) {
	if h == nil {
		return nil, errors.New("bus: nil handler")
	}
	l.mu.Lock()
	id := l.next
	l.next++
	l.subs[id] = h
	l.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			delete(l.subs, id)
			l.mu.Unlock()
		})
	}, nil
}

// ErrMalformed reports a message that is not a well-formed invalidation event.
var ErrMalformed = errors.New("bus: malformed event")

// MaxMessageBytes bounds an encoded event.
const MaxMessageBytes = 1024

var namespacePattern = regexp.MustCompile(`^[a-z0-9._-]{1,64}$`)

type wire struct {
	V  int    `json:"v"`
	K  string `json:"k"`
	NS string `json:"ns"`
	N  string `json:"n"`
}

var kindNames = map[Kind]string{KindKey: "key", KindTag: "tag"}

// Encode returns e as a network message, for Bus implementations that cross
// process boundaries.
func Encode(e Event) ([]byte, error) {
	if err := check(e); err != nil {
		return nil, fmt.Errorf("bus: cannot encode: %w", err)
	}
	return json.Marshal(wire{V: 1, K: kindNames[e.Kind], NS: e.Namespace, N: e.Name})
}

// Decode parses a network message. Messages are untrusted (RS-07): anything
// but exactly one well-formed event within MaxMessageBytes is ErrMalformed.
func Decode(data []byte) (Event, error) {
	if len(data) > MaxMessageBytes {
		return Event{}, fmt.Errorf("%w: %d bytes exceeds %d", ErrMalformed, len(data), MaxMessageBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var w wire
	if err := dec.Decode(&w); err != nil {
		return Event{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Event{}, fmt.Errorf("%w: trailing data", ErrMalformed)
	}
	if w.V != 1 {
		return Event{}, fmt.Errorf("%w: unknown version %d", ErrMalformed, w.V)
	}
	e := Event{Namespace: w.NS, Name: w.N}
	switch w.K {
	case "key":
		e.Kind = KindKey
	case "tag":
		e.Kind = KindTag
	default:
		return Event{}, fmt.Errorf("%w: unknown kind %q", ErrMalformed, w.K)
	}
	if err := check(e); err != nil {
		return Event{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	return e, nil
}

func check(e Event) error {
	if _, ok := kindNames[e.Kind]; !ok {
		return fmt.Errorf("unknown kind %d", e.Kind)
	}
	if !namespacePattern.MatchString(e.Namespace) {
		return fmt.Errorf("namespace %q must match %s", e.Namespace, namespacePattern)
	}
	if e.Name == "" || !utf8.ValidString(e.Name) {
		return errors.New("name must be non-empty UTF-8")
	}
	for _, r := range e.Name {
		if unicode.IsControl(r) {
			return fmt.Errorf("name contains control character %U", r)
		}
	}
	return nil
}
