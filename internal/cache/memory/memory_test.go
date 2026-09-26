package memory

import (
	"errors"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
	"unsafe"

	"github.com/xantiagoma/xtandard-image/internal/cache"
)

// tracker is a Go-memory allocator that counts buffers allocated and freed
// (the leak check).
type tracker struct{ allocs, frees atomic.Int32 }

func (tr *tracker) buf(n int) cache.Buffer {
	tr.allocs.Add(1)
	return cache.NewBuffer(make([]byte, n), func() { tr.frees.Add(1) })
}

func (tr *tracker) check(t *testing.T) {
	t.Helper()
	if a, f := tr.allocs.Load(), tr.frees.Load(); a != f {
		t.Errorf("leak: %d buffers allocated, %d freed", a, f)
	}
}

type metricsRec struct {
	cache.NopMetrics
	mu        sync.Mutex
	hits      int
	misses    int
	evictions int
	bytes     int64
	entries   int
}

func (m *metricsRec) ObserveCache(_ string, hit bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if hit {
		m.hits++
	} else {
		m.misses++
	}
}

func (m *metricsRec) ObserveCacheEvictions(_ string, n int) {
	m.mu.Lock()
	m.evictions += n
	m.mu.Unlock()
}

func (m *metricsRec) SetCacheSize(_ string, b int64, n int) {
	m.mu.Lock()
	m.bytes, m.entries = b, n
	m.mu.Unlock()
}

// set stores a body of n bytes under key and drops the caller's reference.
func set(t *testing.T, s *Store, tr *tracker, key string, n int, ttl time.Duration) {
	t.Helper()
	b := tr.buf(n)
	defer b.Release()
	if err := s.Set(t.Context(), cache.NSResult, key, &cache.Entry{Body: b, Status: http.StatusOK}, ttl); err != nil {
		t.Fatalf("Set %s: %v", key, err)
	}
}

func has(t *testing.T, s *Store, key string) bool {
	t.Helper()
	e, ok, err := s.Get(t.Context(), cache.NSResult, key)
	if err != nil {
		t.Fatalf("Get %s: %v", key, err)
	}
	e.Release()
	return ok
}

// TestHitSharesBody: a hit returns the stored bytes themselves (no copy),
// with a reference owned by the caller.
func TestHitSharesBody(t *testing.T) {
	t.Parallel()
	tr := &tracker{}
	m := &metricsRec{}
	s, err := New(Options{MaxBytes: 1 << 20, Metrics: m})
	if err != nil {
		t.Fatal(err)
	}
	body := tr.buf(100)
	hdr := http.Header{"Etag": {`"a"`}}
	if err := s.Set(t.Context(), cache.NSResult, "k", &cache.Entry{Body: body, Status: http.StatusOK, Header: hdr}, time.Minute); err != nil {
		t.Fatal(err)
	}
	hdr.Set("ETag", `"mutated"`) // the store cloned the header map

	e, ok, err := s.Get(t.Context(), cache.NSResult, "k")
	if err != nil || !ok {
		t.Fatalf("Get = %v, %v", ok, err)
	}
	if unsafe.SliceData(e.Body.Bytes()) != unsafe.SliceData(body.Bytes()) {
		t.Error("hit copied the body")
	}
	if got := e.Header.Get("ETag"); got != `"a"` {
		t.Errorf("stored header = %q", got)
	}
	if e.Status != http.StatusOK || e.StoredAt.IsZero() || e.ExpiresAt.Sub(e.StoredAt) != time.Minute {
		t.Errorf("entry meta: status %d stored %v expires %v", e.Status, e.StoredAt, e.ExpiresAt)
	}
	body.Release() // caller's original reference
	_ = s.Close()  // store's reference
	if tr.frees.Load() != 0 {
		t.Fatal("body freed while a reader still holds it")
	}
	e.Release()
	tr.check(t)
	if _, ok, _ := s.Get(t.Context(), cache.NSResult, "missing"); ok {
		t.Error("hit after close")
	}
	if m.hits != 1 {
		t.Errorf("hits = %d", m.hits)
	}
}

// TestTTLExpiry runs on the synctest clock.
func TestTTLExpiry(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		tr := &tracker{}
		s, err := New(Options{MaxBytes: 1 << 20})
		if err != nil {
			t.Fatal(err)
		}
		set(t, s, tr, "short", 10, 10*time.Second)
		set(t, s, tr, "forever", 10, 0)
		synctest.Sleep(10*time.Second - time.Nanosecond)
		if !has(t, s, "short") {
			t.Fatal("expired early")
		}
		synctest.Sleep(time.Nanosecond)
		if has(t, s, "short") {
			t.Fatal("served at its expiry")
		}
		if tr.frees.Load() != 1 {
			t.Errorf("expired entry not released: %d frees", tr.frees.Load())
		}
		synctest.Sleep(365 * 24 * time.Hour)
		if !has(t, s, "forever") {
			t.Error("ttl 0 entry expired")
		}
		if _, n := s.Usage(); n != 1 {
			t.Errorf("entries = %d, want 1", n)
		}
		_ = s.Close()
		tr.check(t)
	})
}

// TestByteBoundedEviction: least recently used entries go first, the
// charge never exceeds MaxBytes, evictions are counted and released.
func TestByteBoundedEviction(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		tr := &tracker{}
		m := &metricsRec{}
		const body = 1000
		one := (&cache.Entry{Body: cache.NewBuffer(make([]byte, body), nil)}).Size() + int64(len(cache.NSResult)+1)
		s, err := New(Options{MaxBytes: 3 * one, Metrics: m})
		if err != nil {
			t.Fatal(err)
		}
		set(t, s, tr, "a", body, time.Hour)
		set(t, s, tr, "b", body, time.Hour)
		set(t, s, tr, "c", body, time.Hour)
		synctest.Sleep(time.Second)
		if !has(t, s, "a") { // a becomes most recently used; b is now the oldest
			t.Fatal("a missing")
		}
		set(t, s, tr, "d", body, time.Hour)
		for key, want := range map[string]bool{"a": true, "b": false, "c": true, "d": true} {
			if got := has(t, s, key); got != want {
				t.Errorf("has(%s) = %v, want %v", key, got, want)
			}
		}
		used, n := s.Usage()
		if used > 3*one || n != 3 {
			t.Errorf("usage %d bytes / %d entries, budget %d", used, n, 3*one)
		}
		m.mu.Lock()
		if m.evictions != 1 || m.entries != 3 || m.bytes != used {
			t.Errorf("metrics: evictions %d entries %d bytes %d", m.evictions, m.entries, m.bytes)
		}
		m.mu.Unlock()
		if tr.frees.Load() != 1 {
			t.Errorf("evicted entry not released: %d frees", tr.frees.Load())
		}

		// Larger than the whole budget: skipped, nothing evicted.
		set(t, s, tr, "huge", int(4*one), time.Hour)
		if has(t, s, "huge") {
			t.Error("oversized entry stored")
		}
		if _, n := s.Usage(); n != 3 {
			t.Errorf("oversized entry evicted others: %d entries", n)
		}
		// Replacing a key releases the previous body.
		set(t, s, tr, "a", body, time.Hour)
		if tr.frees.Load() != 3 {
			t.Errorf("frees after replace = %d, want 3", tr.frees.Load())
		}
		if err := s.Delete(t.Context(), cache.NSResult, "a"); err != nil {
			t.Fatal(err)
		}
		if has(t, s, "a") {
			t.Error("deleted entry served")
		}
		_ = s.Close()
		tr.check(t)
		if err := s.Set(t.Context(), cache.NSResult, "x", &cache.Entry{Body: cache.NewBuffer(nil, nil)}, 0); !errors.Is(err, cache.ErrClosed) {
			t.Errorf("Set after Close = %v", err)
		}
		if err := s.Delete(t.Context(), cache.NSResult, "x"); !errors.Is(err, cache.ErrClosed) {
			t.Errorf("Delete after Close = %v", err)
		}
	})
}

func TestNamespacesAreSeparate(t *testing.T) {
	t.Parallel()
	tr := &tracker{}
	s, err := New(Options{MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	set(t, s, tr, "k", 1, 0)
	e, ok, _ := s.Get(t.Context(), cache.NSOrigin, "k")
	if ok {
		e.Release()
		t.Error("orig/k served res/k")
	}
	if _, err := New(Options{}); err == nil {
		t.Error("New accepted a zero budget")
	}
	if err := s.Set(t.Context(), cache.NSResult, "nil", &cache.Entry{}, 0); err == nil {
		t.Error("Set accepted an entry without body")
	}
	_ = s.Close()
	tr.check(t)
}

// TestConcurrentLeakFree hammers one small store from many goroutines
// (run with -race); afterwards every buffer is released exactly once.
func TestConcurrentLeakFree(t *testing.T) {
	t.Parallel()
	tr := &tracker{}
	s, err := New(Options{MaxBytes: 16 << 10})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 500 {
				key := strconv.Itoa((g*7 + i) % 40)
				switch i % 4 {
				case 0:
					b := tr.buf(512 + i%700)
					_ = s.Set(ctx, cache.NSResult, key, &cache.Entry{Body: b}, time.Minute)
					b.Release()
				case 3:
					_ = s.Delete(ctx, cache.NSResult, key)
				default:
					if e, ok, _ := s.Get(ctx, cache.NSResult, key); ok {
						_ = e.Body.Bytes()[0]
						e.Release()
					}
				}
			}
		})
	}
	wg.Wait()
	if used, _ := s.Usage(); used > 16<<10 {
		t.Errorf("usage %d over budget", used)
	}
	_ = s.Close()
	tr.check(t)
}

func BenchmarkGet(b *testing.B) {
	s, err := New(Options{MaxBytes: 64 << 20})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	body := cache.NewBuffer(make([]byte, 64<<10), nil)
	hdr := http.Header{"Content-Type": {"image/webp"}, "Etag": {`"xi1-abcdefghijklmnopqrstuv"`}}
	key := "v1/40e51a65fe4e60e198920fdceb6c56f6f61ed6ffd861a92c712207dbc119f36e"
	if err := s.Set(b.Context(), cache.NSResult, key, &cache.Entry{Body: body, Header: hdr}, time.Hour); err != nil {
		b.Fatal(err)
	}
	b.Run("hit", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			e, ok, _ := s.Get(b.Context(), cache.NSResult, key)
			if !ok {
				b.Fatal("miss")
			}
			e.Release()
		}
	})
	b.Run("miss", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, ok, _ := s.Get(b.Context(), cache.NSResult, "v1/absent"); ok {
				b.Fatal("hit")
			}
		}
	})
}
