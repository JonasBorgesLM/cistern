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
	"encoding/binary"
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
	value, ok = s.getLocked(key)
	return value, ok, nil
}

// GetTagged returns the value under key and the generation of each counter in
// genKeys, creating a missing counter at an unpredictable value (ADR-0005).
// Counters live in the same LRU as values: one evicted under memory pressure
// comes back unpredictable, which is what keeps it from repeating (T-14).
func (s *Store) GetTagged(ctx context.Context, key string, genKeys []string, genTTL time.Duration) (value []byte, ok bool, gens []uint64, err error) {
	if ctx.Err() != nil {
		return nil, false, nil, ctx.Err()
	}
	if genTTL <= 0 {
		return nil, false, nil, errors.New("memory: counter ttl must be positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok = s.getLocked(key)
	gens = make([]uint64, len(genKeys))
	for i, gk := range genKeys {
		if gens[i], err = s.counterLocked(gk, genTTL); err != nil {
			return nil, false, nil, err
		}
	}
	return value, ok, gens, nil
}

// Bump advances each counter in genKeys, creating a missing one first, and
// sets its TTL to genTTL.
func (s *Store) Bump(ctx context.Context, genKeys []string, genTTL time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if genTTL <= 0 {
		return errors.New("memory: counter ttl must be positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, gk := range genKeys {
		g, err := s.counterLocked(gk, genTTL)
		if err != nil {
			return err
		}
		s.setLocked(&entry{key: gk, value: binary.BigEndian.AppendUint64(nil, g+1), expires: s.now().Add(genTTL)})
	}
	return nil
}

// counterLocked returns the live counter under gk, or creates one. The caller
// holds s.mu.
func (s *Store) counterLocked(gk string, ttl time.Duration) (uint64, error) {
	if v, ok := s.getLocked(gk); ok && len(v) == 8 {
		return binary.BigEndian.Uint64(v), nil
	}
	g, err := cistern.NewGeneration()
	if err != nil {
		return 0, err
	}
	s.setLocked(&entry{key: gk, value: binary.BigEndian.AppendUint64(nil, g), expires: s.now().Add(ttl)})
	return g, nil
}

// getLocked returns a copy of the live value under key. The caller holds s.mu.
func (s *Store) getLocked(key string) ([]byte, bool) {
	e, found := s.items[key]
	if !found {
		return nil, false
	}
	if !s.now().Before(e.expires) {
		s.remove(e)
		return nil, false
	}
	s.unlink(e)
	s.pushFront(e)
	return clone(e.value), true
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
	s.setLocked(e)
	return nil
}

// setLocked stores e, replacing any entry under its key and evicting least
// recently used entries until both limits hold. The caller holds s.mu.
func (s *Store) setLocked(e *entry) {
	if old, found := s.items[e.key]; found {
		s.remove(old)
	}
	if e.size() > s.maxBytes {
		return
	}
	for s.count >= s.maxEntries || s.size+e.size() > s.maxBytes {
		s.remove(s.head.prev)
	}
	s.items[e.key] = e
	s.pushFront(e)
	s.count++
	s.size += e.size()
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
