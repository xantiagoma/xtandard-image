// Package cache is the pluggable store behind the result cache and,
// later, the shared original cache (docs/ARCHITECTURE.md §9.2, ADR 0004).
//
// A Store keeps Entries (a refcounted body plus a small header subset)
// under a namespace and a key. Backends live in subpackages (memory, disk,
// s3cache, redis); this package holds the interface, the refcounted
// Buffer, result-key derivation (key.go), the persisted entry metadata the
// out-of-process backends share (meta.go), the Tiered composition
// (tiered.go) and the non-blocking write queue (async.go).
//
// Cache failures are never fatal: callers log them and treat the lookup as a
// miss (ARCHITECTURE §2 stage 8, ADR 0004 "errors degrade to misses").
package cache

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"
)

// Namespace separates the kinds of data sharing one Store. A backend
// stores an entry under ns + key (so "res/" + "v1/<hex>").
type Namespace string

// Namespaces in use.
const (
	// NSResult holds encoded responses (server result cache, WP-52).
	NSResult Namespace = "res/"
	// NSOrigin holds original bytes when fetch's in-memory LRU is backed
	// by a shared Store (XI_CACHE_ORIGIN_STORES, ARCHITECTURE §7).
	NSOrigin Namespace = "orig/"
)

// Errors callers branch on.
var (
	// ErrClosed is returned by a Store after Close.
	ErrClosed = errors.New("cache: store closed")
	// ErrDropped is returned by AsyncStore.Set when the write queue is
	// full and the write was dropped (counted in
	// xi_cache_writes_dropped_total).
	ErrDropped = errors.New("cache: write queue full, write dropped")
)

// Buffer is a reference-counted, read-only block of bytes. It has the
// shape of fetch.Buffer and server.Blob, so a vips blob, a fetched
// original or a Go slice (NewBuffer) can be stored without copying.
//
// Retain adds a reference, Release drops one; the Release that drops the
// last reference frees the memory. Implementations are safe for concurrent
// Retain and Release. Bytes is valid until the final Release and must not
// be written.
type Buffer interface {
	Bytes() []byte
	Retain()
	Release()
}

// Entry is one cached response.
//
// Ownership: an Entry returned by Store.Get holds one reference to Body
// owned by the caller, who must call Release exactly once. Store.Set takes
// its own reference (or copies the bytes) and never consumes the
// caller's. Header is shared and read-only on both sides.
type Entry struct {
	// Body is the response body. A hit from a memory tier returns the
	// stored buffer itself (no copy).
	Body Buffer
	// Status is the HTTP status of the stored response (200 for results).
	Status int
	// Header is the stored header subset: Content-Type, ETag,
	// Last-Modified, Vary, Content-DPR and X-Xi-* (the server's own
	// metadata, internal/server/cache.go). Request-dependent headers
	// (Cache-Control, Content-Disposition, Cache-Tag, Link) are recomputed
	// per request and never stored.
	Header http.Header
	// StoredAt is when the entry was first written.
	StoredAt time.Time
	// ExpiresAt is when the entry stops being served; zero means it only
	// leaves by eviction. Stores set it from the Set ttl; Tiered uses it
	// to fill faster tiers with the remaining lifetime.
	ExpiresAt time.Time
}

// Release drops the caller's reference to Body (no-op without a body).
func (e *Entry) Release() {
	if e.Body != nil {
		e.Body.Release()
	}
}

// Size is the byte charge of the entry in a bounded store: body, header
// names and values, and a fixed per-entry overhead.
func (e *Entry) Size() int64 {
	const overhead = 128
	n := int64(overhead)
	if e.Body != nil {
		n += int64(len(e.Body.Bytes()))
	}
	for k, vs := range e.Header {
		n += int64(len(k))
		for _, v := range vs {
			n += int64(len(v))
		}
	}
	return n
}

// Store is a cache backend (memory, disk, s3cache, redis) or a composition
// of them (Tiered, AsyncStore). Implementations are safe for concurrent
// use and honour ctx for I/O.
type Store interface {
	// Get returns the entry under ns+key. ok is false on a miss
	// (including an expired entry); err reports a backend failure, which
	// callers treat as a miss. On a hit the caller owns one reference to
	// Entry.Body and must Release it.
	Get(ctx context.Context, ns Namespace, key string) (e Entry, ok bool, err error)
	// Set stores e under ns+key for ttl (ttl ≤ 0: no expiry, bounded by
	// eviction only), replacing any previous entry. The store takes its
	// own reference to e.Body or copies it; the caller keeps its own. The
	// store copies *e and never keeps or modifies e itself. StoredAt is
	// filled in when zero; ExpiresAt is derived from ttl.
	Set(ctx context.Context, ns Namespace, key string, e *Entry, ttl time.Duration) error
	// Delete removes ns+key; deleting a missing key is not an error.
	Delete(ctx context.Context, ns Namespace, key string) error
	// Close releases every held entry and resource. Later calls return
	// ErrClosed; Close itself is idempotent.
	Close() error
}

// Reasons of Metrics.ObserveCacheSkip (the "reason" label of
// xi_cache_skipped_total).
const (
	// SkipTooLarge: Set did not store an entry over the store's size
	// limit (the memory budget, redis' value limit, s3's metadata limit).
	SkipTooLarge = "too_large"
	// SkipCorrupt: Get found an entry it could not decode (truncated
	// file, checksum mismatch, foreign object) and served a miss.
	SkipCorrupt = "corrupt"
)

// Metrics receives cache observations; *metrics.Metrics implements it
// (xi_cache_requests_total, xi_cache_evictions_total, xi_cache_bytes,
// xi_cache_entries, xi_cache_writes_dropped_total, xi_cache_skipped_total).
// store is the tier name (memory, disk, redis, s3), a bounded label.
type Metrics interface {
	ObserveCache(store string, hit bool)
	ObserveCacheEvictions(store string, n int)
	SetCacheSize(store string, bytes int64, entries int)
	IncCacheWriteDropped()
	// ObserveCacheSkip counts one entry skipped for reason (SkipTooLarge,
	// SkipCorrupt).
	ObserveCacheSkip(store, reason string)
}

// NopMetrics discards every observation.
type NopMetrics struct{}

// ObserveCache implements Metrics.
func (NopMetrics) ObserveCache(string, bool) {}

// ObserveCacheEvictions implements Metrics.
func (NopMetrics) ObserveCacheEvictions(string, int) {}

// SetCacheSize implements Metrics.
func (NopMetrics) SetCacheSize(string, int64, int) {}

// IncCacheWriteDropped implements Metrics.
func (NopMetrics) IncCacheWriteDropped() {}

// ObserveCacheSkip implements Metrics.
func (NopMetrics) ObserveCacheSkip(string, string) {}

// refBuffer is the Buffer returned by NewBuffer.
type refBuffer struct {
	b    []byte
	free func()
	refs atomic.Int64
}

// NewBuffer returns a Buffer over b holding one reference. free, if
// non-nil, runs once when the last reference is released: pass the
// owner's Release to adopt a blob without copying it (a vips output blob
// becomes a cached body). b must not be written afterwards.
func NewBuffer(b []byte, free func()) Buffer {
	r := &refBuffer{b: b, free: free}
	r.refs.Store(1)
	return r
}

func (r *refBuffer) Bytes() []byte { return r.b }

// Retain panics on a released buffer: that is a use-after-free bug.
func (r *refBuffer) Retain() {
	if r.refs.Add(1) <= 1 {
		panic("cache: Retain on a released buffer")
	}
}

// Release panics when called more often than retained.
func (r *refBuffer) Release() {
	switch n := r.refs.Add(-1); {
	case n == 0:
		r.b = nil
		if r.free != nil {
			r.free()
		}
	case n < 0:
		panic("cache: buffer released too often")
	}
}

// Expiry returns the ExpiresAt of an entry stored at now for ttl: zero
// (never) when ttl ≤ 0. Backends use it so every tier agrees.
func Expiry(now time.Time, ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return now.Add(ttl)
}
