package fetch

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/cache/memory"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

// process is one simulated replica: its own Fetcher (own LRU, own
// allocator) over a backend shared with the other replicas.
func process(t *testing.T, src *fakeSource, store cache.Store, cfg Config) (*Fetcher, *countingAlloc, *recorder) {
	t.Helper()
	alloc, rec := &countingAlloc{}, &recorder{}
	f, err := New(&fakeSources{src: src}, alloc.alloc, cfg, WithMetrics(rec), WithStore(store, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return f, alloc, rec
}

func sharedStore(t *testing.T) *memory.Store {
	t.Helper()
	s, err := memory.New(memory.Options{MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() }) // idempotent
	return s
}

// TestStoreSharedAcrossProcesses: the second process's fetch is served by
// the shared store without reaching the origin, with the origin metadata.
func TestStoreSharedAcrossProcesses(t *testing.T) {
	t.Parallel()
	data := payload(4096)
	src := &fakeSource{fn: func(context.Context, int, source.Key, source.FetchOptions) (*source.Object, error) {
		o := object(data, `"etag-1"`)
		o.LastModified = time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
		o.CacheControl = "public, max-age=60"
		return o, nil
	}}
	store := sharedStore(t)
	cfg := Config{CacheBytes: 1 << 20, CacheTTL: time.Minute}
	key := testKey("shared.jpg")

	a, allocA, _ := process(t, src, store, cfg)
	r := mustFetch(t, a, key)
	if r.FromCache {
		t.Error("first fetch claims the cache")
	}
	first := r.Object
	r.Release()

	b, allocB, recB := process(t, src, store, cfg)
	r = mustFetch(t, b, key)
	if n := src.calls.Load(); n != 1 {
		t.Errorf("origin called %d times, want 1", n)
	}
	if !r.FromCache || !bytes.Equal(r.Bytes(), data) {
		t.Errorf("second process: FromCache=%v, %d bytes", r.FromCache, len(r.Bytes()))
	}
	if r.Object.ContentType != first.ContentType || r.Object.ETag != first.ETag ||
		!r.Object.LastModified.Equal(first.LastModified) || r.Object.CacheControl != first.CacheControl || r.Object.Size != first.Size {
		t.Errorf("metadata %+v, want %+v", r.Object, first)
	}
	r.Release()
	if c, _, _ := recB.snapshot(); c[CacheStoreHit] != 1 {
		t.Errorf("store hits %d, want 1", c[CacheStoreHit])
	}

	// The store hit entered b's LRU: the next fetch does not ask the store.
	if err := store.Delete(t.Context(), cache.NSOrigin, storeKey(cacheKey(key))); err != nil {
		t.Fatal(err)
	}
	r = mustFetch(t, b, key)
	r.Release()
	if c, _, _ := recB.snapshot(); c[CacheHit] != 1 {
		t.Errorf("LRU hits %d, want 1", c[CacheHit])
	}

	a.Purge()
	b.Purge()
	if a, b := allocA.live(), allocB.live(); a != 0 || b != 0 {
		t.Errorf("live buffers after purge: %d, %d", a, b)
	}
}

type failingStore struct{ cache.Store }

func (failingStore) Get(context.Context, cache.Namespace, string) (cache.Entry, bool, error) {
	return cache.Entry{}, false, errors.New("store down")
}

func TestStoreFailureAndLimits(t *testing.T) {
	t.Parallel()
	data := payload(2048)
	src := &fakeSource{fn: func(context.Context, int, source.Key, source.FetchOptions) (*source.Object, error) {
		return object(data, ""), nil
	}}

	t.Run("failing store downloads", func(t *testing.T) {
		t.Parallel()
		src := &fakeSource{fn: src.fn}
		f, _, _ := process(t, src, failingStore{sharedStore(t)}, Config{})
		mustFetch(t, f, testKey("x.jpg")).Release()
		if n := src.calls.Load(); n != 1 {
			t.Errorf("origin called %d times, want 1", n)
		}
	})

	t.Run("stored original over this process's limit", func(t *testing.T) {
		t.Parallel()
		src := &fakeSource{fn: src.fn}
		store := sharedStore(t)
		big, _, _ := process(t, src, store, Config{})
		mustFetch(t, big, testKey("x.jpg")).Release()
		small, _, _ := process(t, src, store, Config{MaxBytes: 1024})
		_, err := small.Fetch(t.Context(), testKey("x.jpg"), source.FetchOptions{})
		if err == nil {
			t.Fatal("original over MaxBytes served from the store")
		}
		if n := src.calls.Load(); n != 2 {
			t.Errorf("origin called %d times, want 2 (the store entry was skipped)", n)
		}
	})

	t.Run("disabled without ttl", func(t *testing.T) {
		t.Parallel()
		f, err := New(&fakeSources{src: src}, GoAllocator, Config{}, WithStore(sharedStore(t), 0))
		if err != nil {
			t.Fatal(err)
		}
		if f.store != nil {
			t.Error("store enabled with ttl 0")
		}
	})
}
