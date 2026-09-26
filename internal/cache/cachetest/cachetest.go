// Package cachetest is the conformance suite every cache.Store backend
// runs (unit tests for memory-free backends like disk, integration tests
// for redis and s3cache), so the backends agree on the contract of
// cache.Store: misses, round trips, overwrite, namespaces, TTL, deletion,
// buffer ownership and Close.
package cachetest

import (
	"bytes"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/cache"
)

// Clock is a settable clock for stores that take a Now option.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock returns a clock at a fixed instant.
func NewClock() *Clock {
	return &Clock{now: time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)}
}

// Now returns the clock's time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// Factory returns a new, empty store whose TTL checks use now. Stores
// sharing a server (redis, s3) must isolate each call, for example with a
// unique key prefix. The suite closes the store.
type Factory func(t *testing.T, now func() time.Time) cache.Store

// Body is a tracked cache.Buffer for ownership checks.
type Body struct {
	cache.Buffer
	freed *atomic.Bool
}

// NewBody returns a buffer over b holding one reference and reporting
// its final Release.
func NewBody(b []byte) Body {
	freed := new(atomic.Bool)
	return Body{Buffer: cache.NewBuffer(b, func() { freed.Store(true) }), freed: freed}
}

// Freed reports whether the last reference was released.
func (b Body) Freed() bool { return b.freed.Load() }

// Set stores body under ns+key and drops the caller's reference.
func Set(t *testing.T, s cache.Store, ns cache.Namespace, key string, body []byte, h http.Header, ttl time.Duration) {
	t.Helper()
	b := cache.NewBuffer(body, nil)
	defer b.Release()
	if err := s.Set(t.Context(), ns, key, &cache.Entry{Body: b, Status: http.StatusOK, Header: h}, ttl); err != nil {
		t.Fatalf("Set %s%s: %v", ns, key, err)
	}
}

// Get returns the body under ns+key (nil, false on a miss).
func Get(t *testing.T, s cache.Store, ns cache.Namespace, key string) (cache.Entry, []byte, bool) {
	t.Helper()
	e, ok, err := s.Get(t.Context(), ns, key)
	if err != nil {
		t.Fatalf("Get %s%s: %v", ns, key, err)
	}
	if !ok {
		return cache.Entry{}, nil, false
	}
	body := bytes.Clone(e.Body.Bytes())
	e.Release()
	return e, body, true
}

// Run runs the conformance suite against stores from newStore.
func Run(t *testing.T, newStore Factory) {
	t.Helper()
	open := func(t *testing.T) (cache.Store, *Clock) {
		t.Helper()
		clock := NewClock()
		s := newStore(t, clock.Now)
		t.Cleanup(func() { _ = s.Close() }) // Close is idempotent; errors are checked by the close case
		return s, clock
	}
	header := http.Header{"Content-Type": {"image/webp"}, "Etag": {`"xi1-abc"`}, "X-Xi-Format": {"webp"}}

	t.Run("miss", func(t *testing.T) {
		t.Parallel()
		s, _ := open(t)
		if _, _, ok := Get(t, s, cache.NSResult, "v1/absent"); ok {
			t.Error("hit on an empty store")
		}
	})

	t.Run("round trip", func(t *testing.T) {
		t.Parallel()
		s, clock := open(t)
		body := bytes.Repeat([]byte("xtandard"), 1000)
		stored := clock.Now().Add(-time.Second)
		b := NewBody(body)
		in := &cache.Entry{Body: b, Status: http.StatusOK, Header: header, StoredAt: stored}
		if err := s.Set(t.Context(), cache.NSResult, "v1/rt", in, time.Hour); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if b.Freed() {
			t.Fatal("Set consumed the caller's reference")
		}
		b.Release()
		e, got, ok := Get(t, s, cache.NSResult, "v1/rt")
		if !ok {
			t.Fatal("miss after Set")
		}
		if !bytes.Equal(got, body) {
			t.Errorf("body: %d bytes, want %d", len(got), len(body))
		}
		if e.Status != http.StatusOK {
			t.Errorf("status %d", e.Status)
		}
		if diff := cmp.Diff(header, e.Header); diff != "" {
			t.Errorf("header (-want +got):\n%s", diff)
		}
		if !e.StoredAt.Equal(stored) {
			t.Errorf("StoredAt %s, want %s", e.StoredAt, stored)
		}
		if want := clock.Now().Add(time.Hour); !e.ExpiresAt.Equal(want) {
			t.Errorf("ExpiresAt %s, want %s", e.ExpiresAt, want)
		}
	})

	t.Run("overwrite", func(t *testing.T) {
		t.Parallel()
		s, _ := open(t)
		Set(t, s, cache.NSResult, "v1/ow", []byte("first"), header, time.Hour)
		Set(t, s, cache.NSResult, "v1/ow", []byte("second"), header, time.Hour)
		if _, got, ok := Get(t, s, cache.NSResult, "v1/ow"); !ok || string(got) != "second" {
			t.Errorf("got %q %v, want second", got, ok)
		}
	})

	t.Run("namespaces", func(t *testing.T) {
		t.Parallel()
		s, _ := open(t)
		Set(t, s, cache.NSResult, "v1/ns", []byte("result"), header, time.Hour)
		Set(t, s, cache.NSOrigin, "v1/ns", []byte("origin"), nil, time.Hour)
		if _, got, ok := Get(t, s, cache.NSResult, "v1/ns"); !ok || string(got) != "result" {
			t.Errorf("res/: %q %v", got, ok)
		}
		e, got, ok := Get(t, s, cache.NSOrigin, "v1/ns")
		if !ok || string(got) != "origin" {
			t.Errorf("orig/: %q %v", got, ok)
		}
		if len(e.Header) != 0 {
			t.Errorf("orig/ header %v, want none", e.Header)
		}
	})

	t.Run("expiry", func(t *testing.T) {
		t.Parallel()
		s, clock := open(t)
		Set(t, s, cache.NSResult, "v1/ttl", []byte("short"), header, time.Minute)
		Set(t, s, cache.NSResult, "v1/forever", []byte("long"), header, 0)
		clock.Advance(59 * time.Second)
		if _, _, ok := Get(t, s, cache.NSResult, "v1/ttl"); !ok {
			t.Error("miss before the TTL")
		}
		clock.Advance(2 * time.Second)
		if _, _, ok := Get(t, s, cache.NSResult, "v1/ttl"); ok {
			t.Error("hit after the TTL")
		}
		clock.Advance(1000 * time.Hour)
		e, _, ok := Get(t, s, cache.NSResult, "v1/forever")
		if !ok {
			t.Fatal("ttl 0 entry expired")
		}
		if !e.ExpiresAt.IsZero() {
			t.Errorf("ttl 0 ExpiresAt %s, want zero", e.ExpiresAt)
		}
	})

	t.Run("delete", func(t *testing.T) {
		t.Parallel()
		s, _ := open(t)
		Set(t, s, cache.NSResult, "v1/del", []byte("gone"), header, time.Hour)
		if err := s.Delete(t.Context(), cache.NSResult, "v1/del"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, _, ok := Get(t, s, cache.NSResult, "v1/del"); ok {
			t.Error("hit after Delete")
		}
		if err := s.Delete(t.Context(), cache.NSResult, "v1/never"); err != nil {
			t.Errorf("Delete of a missing key: %v", err)
		}
	})

	t.Run("close", func(t *testing.T) {
		t.Parallel()
		s, _ := open(t)
		Set(t, s, cache.NSResult, "v1/c", []byte("x"), header, time.Hour)
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if err := s.Close(); err != nil {
			t.Errorf("second Close: %v", err)
		}
		if _, _, err := s.Get(t.Context(), cache.NSResult, "v1/c"); !errors.Is(err, cache.ErrClosed) {
			t.Errorf("Get after Close: %v, want ErrClosed", err)
		}
		b := cache.NewBuffer([]byte("y"), nil)
		defer b.Release()
		if err := s.Set(t.Context(), cache.NSResult, "v1/c", &cache.Entry{Body: b, Status: http.StatusOK}, time.Hour); !errors.Is(err, cache.ErrClosed) {
			t.Errorf("Set after Close: %v, want ErrClosed", err)
		}
	})
}
