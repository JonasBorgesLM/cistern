// Package memory is cistern's in-process L1 Store: an LRU over encoded bytes,
// bounded by an entry count and a byte size, with a TTL per entry.
//
// Evicting an entry only ever produces a miss, which is answered from the
// source of truth, so least-recently-used is a safe eviction order here
// (ADR-0010). Entries hold bytes rather than values so that no two callers
// ever share an object (ADR-0014).
package memory

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/JonasBorgesLM/cistern"
)

// Defaults used when an Option does not set a limit. Both are bounded because
// an L1 must never grow without limit (REQUIREMENTS.md §11).
const (
	DefaultMaxEntries       = 10_000
	DefaultMaxBytes   int64 = 64 << 20
)

// Option configures a Store.
type Option func(*config)

type config struct {
	maxEntries int
	maxBytes   int64
}

// WithMaxEntries sets the maximum number of entries. It must be positive.
func WithMaxEntries(n int) Option { return func(c *config) { c.maxEntries = n } }

// WithMaxBytes sets the maximum total size of the entries, where an entry
// costs len(key) + len(value) bytes. It must be positive.
func WithMaxBytes(n int64) Option { return func(c *config) { c.maxBytes = n } }

// Store is an in-memory cistern.Store. It is safe for concurrent use.
type Store struct {
	mu         sync.Mutex
	maxEntries int
	maxBytes   int64
	size       int64
	count      int
	head       entry // sentinel: head.next is most recently used, head.prev least
	items      map[string]*entry
	now        func() time.Time
}

type entry struct {
	key        string
	value      []byte
	expires    time.Time
	prev, next *entry
}

func (e *entry) size() int64 { return int64(len(e.key) + len(e.value)) }

// New returns an empty Store. It returns an error wrapping
// cistern.ErrInvalidConfig when a limit is not positive.
func New(opts ...Option) (*Store, error) {
	cfg := config{maxEntries: DefaultMaxEntries, maxBytes: DefaultMaxBytes}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.maxEntries <= 0 {
		return nil, fmt.Errorf("%w: memory: max entries must be positive, got %d", cistern.ErrInvalidConfig, cfg.maxEntries)
	}
	if cfg.maxBytes <= 0 {
		return nil, fmt.Errorf("%w: memory: max bytes must be positive, got %d", cistern.ErrInvalidConfig, cfg.maxBytes)
	}
	s := &Store{
		maxEntries: cfg.maxEntries,
		maxBytes:   cfg.maxBytes,
		items:      make(map[string]*entry),
		now:        time.Now,
	}
	s.head.next, s.head.prev = &s.head, &s.head
	return s, nil
}

// Get returns a copy of the live value stored under key.
func (s *Store) Get(ctx context.Context, key string) (value []byte, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	e, found := s.items[key]
	if !found {
		return nil, false, nil
	}
	if !s.now().Before(e.expires) {
		s.remove(e)
		return nil, false, nil
	}
	s.unlink(e)
	s.pushFront(e)
	return clone(e.value), true, nil
}

// Set stores a copy of value under key for ttl, evicting least recently used
// entries until both limits hold. A value too large to fit even in an empty
// store is not stored, and any older value under key is removed, so the store
// never keeps serving what the caller just replaced.
func (s *Store) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ttl <= 0 {
		return errors.New("memory: ttl must be positive")
	}
	e := &entry{key: key, value: clone(value), expires: s.now().Add(ttl)}

	s.mu.Lock()
	defer s.mu.Unlock()

	if old, found := s.items[key]; found {
		s.remove(old)
	}
	if e.size() > s.maxBytes {
		return nil
	}
	for s.count >= s.maxEntries || s.size+e.size() > s.maxBytes {
		s.remove(s.head.prev)
	}
	s.items[key] = e
	s.pushFront(e)
	s.count++
	s.size += e.size()
	return nil
}

// Delete removes the entry under key, if any.
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if e, found := s.items[key]; found {
		s.remove(e)
	}
	return nil
}

// remove drops e from the store. The caller holds s.mu.
func (s *Store) remove(e *entry) {
	s.unlink(e)
	delete(s.items, e.key)
	s.count--
	s.size -= e.size()
}

func (s *Store) unlink(e *entry) {
	e.prev.next, e.next.prev = e.next, e.prev
	e.prev, e.next = nil, nil
}

func (s *Store) pushFront(e *entry) {
	e.prev, e.next = &s.head, s.head.next
	s.head.next.prev = e
	s.head.next = e
}

func clone(b []byte) []byte { return append([]byte(nil), b...) }
