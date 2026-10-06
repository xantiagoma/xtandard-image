// Package memory is the in-process cache.Store: a byte-bounded LRU with a
// TTL per entry (docs/ARCHITECTURE.md §9.2, XI_CACHE_RESULT_MEMORY_BYTES).
//
// It holds one reference to each stored body, so a hit hands out the stored
// buffer itself (Retain, no copy) and an evicted entry is freed once the
// last in-flight reader releases it. Expired entries are dropped lazily by
// Get and by LRU pressure; there is no background goroutine, so the store
// needs no lifecycle beyond Close and is deterministic under testing/synctest
// (time.Now is the clock).
package memory

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/cache"
)

// Name is the default store label in metrics.
const Name = "memory"

// Options configures New.
type Options struct {
	// MaxBytes bounds the total charge (cache.Entry.Size plus the key) of
	// all entries; it must be > 0. An entry larger than MaxBytes is not
	// stored.
	MaxBytes int64
	// Name labels the metrics; "" uses Name.
	Name string
	// Metrics receives lookups, evictions and size; nil discards them.
	Metrics cache.Metrics
}

// Store is the in-memory LRU. It is safe for concurrent use.
type Store struct {
	name    string
	metrics cache.Metrics
	max     int64

	mu     sync.Mutex
	used   int64
	ll     list.List // front = most recently used; values are *item
	items  map[string]*list.Element
	closed bool
}

type item struct {
	key   string // ns + key
	entry cache.Entry
	size  int64
}

// New returns an empty store.
func New(opts Options) (*Store, error) {
	if opts.MaxBytes <= 0 {
		return nil, fmt.Errorf("memory cache: max bytes must be > 0, got %d", opts.MaxBytes)
	}
	s := &Store{name: opts.Name, metrics: opts.Metrics, max: opts.MaxBytes, items: make(map[string]*list.Element)}
	if s.name == "" {
		s.name = Name
	}
	if s.metrics == nil {
		s.metrics = cache.NopMetrics{}
	}
	return s, nil
}

// Get implements cache.Store. A hit retains the stored body for the caller
// and moves the entry to the front; an expired entry is removed.
func (s *Store) Get(_ context.Context, ns cache.Namespace, key string) (cache.Entry, bool, error) {
	var drop cache.Entry
	defer drop.Release()

	now := time.Now()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return cache.Entry{}, false, cache.ErrClosed
	}
	if el, ok := s.items[string(ns)+key]; ok {
		it := itemOf(el)
		if exp := it.entry.ExpiresAt; exp.IsZero() || now.Before(exp) {
			s.ll.MoveToFront(el)
			it.entry.Body.Retain()
			e := it.entry
			s.mu.Unlock()
			s.metrics.ObserveCache(s.name, true)
			return e, true, nil
		}
		drop = s.removeLocked(el)
		s.reportSizeLocked()
	}
	s.mu.Unlock()
	s.metrics.ObserveCache(s.name, false)
	return cache.Entry{}, false, nil
}

// Set implements cache.Store. The store retains e.Body and clones the
// header map, so the caller may release or reuse both. Entries larger than
// the whole budget are skipped (not an error); least recently used entries
// are evicted until the new one fits.
func (s *Store) Set(_ context.Context, ns cache.Namespace, key string, in *cache.Entry, ttl time.Duration) error {
	if in.Body == nil {
		return errors.New("memory cache: entry without body")
	}
	if cache.IsFileBody(in.Body) {
		// A body spooled to a file (a large video, WP-121b) belongs in the
		// disk or S3 tier: holding it here would pin the file and its
		// spool budget, and mapping it would bring it into memory.
		s.metrics.ObserveCacheSkip(s.name, cache.SkipTooLarge)
		return nil
	}
	e := *in
	now := time.Now()
	if e.StoredAt.IsZero() {
		e.StoredAt = now
	}
	e.ExpiresAt = cache.Expiry(now, ttl)
	e.Header = e.Header.Clone()
	full := string(ns) + key
	it := &item{key: full, entry: e}
	it.size = e.Size() + int64(len(full))
	if it.size > s.max {
		s.metrics.ObserveCacheSkip(s.name, cache.SkipTooLarge)
		return nil
	}

	var drop []cache.Entry
	defer func() {
		for i := range drop {
			drop[i].Release()
		}
	}()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return cache.ErrClosed
	}
	e.Body.Retain()
	if el, ok := s.items[full]; ok {
		drop = append(drop, s.removeLocked(el))
	}
	s.items[full] = s.ll.PushFront(it)
	s.used += it.size
	evicted := 0
	for s.used > s.max {
		drop = append(drop, s.removeLocked(s.ll.Back()))
		evicted++
	}
	if evicted > 0 {
		s.metrics.ObserveCacheEvictions(s.name, evicted)
	}
	s.reportSizeLocked()
	return nil
}

// Delete implements cache.Store.
func (s *Store) Delete(_ context.Context, ns cache.Namespace, key string) error {
	var drop cache.Entry
	defer drop.Release()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return cache.ErrClosed
	}
	if el, ok := s.items[string(ns)+key]; ok {
		drop = s.removeLocked(el)
		s.reportSizeLocked()
	}
	return nil
}

// Close implements cache.Store: it drops every entry's reference (buffers
// still held by readers are freed when they release them).
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	drop := make([]cache.Entry, 0, s.ll.Len())
	for s.ll.Len() > 0 {
		drop = append(drop, s.removeLocked(s.ll.Back()))
	}
	s.reportSizeLocked()
	s.mu.Unlock()
	for i := range drop {
		drop[i].Release()
	}
	return nil
}

// Usage reports the bytes charged and the number of entries.
func (s *Store) Usage() (bytes int64, entries int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.used, s.ll.Len()
}

// removeLocked unlinks el and returns its entry for the caller to release
// outside the lock.
func (s *Store) removeLocked(el *list.Element) cache.Entry {
	it := itemOf(el)
	s.ll.Remove(el)
	delete(s.items, it.key)
	s.used -= it.size
	return it.entry
}

func (s *Store) reportSizeLocked() { s.metrics.SetCacheSize(s.name, s.used, s.ll.Len()) }

// itemOf returns the item stored in el; every element holds one.
func itemOf(el *list.Element) *item {
	it, _ := el.Value.(*item)
	return it
}
