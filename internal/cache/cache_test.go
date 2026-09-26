package cache

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// countingBuffer returns a Go-memory Buffer whose final Release increments
// freed (the leak check of every test here).
func countingBuffer(b []byte, freed *atomic.Int32) Buffer {
	return NewBuffer(b, func() { freed.Add(1) })
}

func TestBufferRefcount(t *testing.T) {
	t.Parallel()
	var freed atomic.Int32
	b := countingBuffer([]byte("abc"), &freed)
	b.Retain()
	b.Release()
	if freed.Load() != 0 || string(b.Bytes()) != "abc" {
		t.Fatalf("freed early: %d %q", freed.Load(), b.Bytes())
	}
	b.Release()
	if freed.Load() != 1 || b.Bytes() != nil {
		t.Fatalf("after final release: freed %d, bytes %q", freed.Load(), b.Bytes())
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("extra Release did not panic")
			}
		}()
		b.Release()
	}()
}

func TestEntrySize(t *testing.T) {
	t.Parallel()
	e := Entry{Body: NewBuffer(make([]byte, 1000), nil), Header: http.Header{"Etag": {`"x"`}}}
	if got, want := e.Size(), int64(128+1000+len("Etag")+3); got != want {
		t.Errorf("Size = %d, want %d", got, want)
	}
	var empty Entry
	empty.Release() // no body: no-op
}

// mapStore is a minimal Store for Tiered and AsyncStore tests. set, when
// non-nil, runs before every Set (blocking or failing it).
type mapStore struct {
	mu      sync.Mutex
	m       map[string]Entry
	getErr  error
	set     func(ctx context.Context) error
	sets    atomic.Int32
	closed  atomic.Bool
	deletes atomic.Int32
}

func newMapStore() *mapStore { return &mapStore{m: map[string]Entry{}} }

func (s *mapStore) Get(_ context.Context, ns Namespace, key string) (Entry, bool, error) {
	if s.getErr != nil {
		return Entry{}, false, s.getErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[string(ns)+key]
	if !ok || (!e.ExpiresAt.IsZero() && !time.Now().Before(e.ExpiresAt)) {
		return Entry{}, false, nil
	}
	e.Body.Retain()
	return e, true, nil
}

func (s *mapStore) Set(ctx context.Context, ns Namespace, key string, in *Entry, ttl time.Duration) error {
	e := *in
	if s.set != nil {
		if err := s.set(ctx); err != nil {
			return err
		}
	}
	s.sets.Add(1)
	now := time.Now()
	if e.StoredAt.IsZero() {
		e.StoredAt = now
	}
	e.ExpiresAt = Expiry(now, ttl)
	e.Body.Retain()
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.m[string(ns)+key]; ok {
		old.Release()
	}
	s.m[string(ns)+key] = e
	return nil
}

func (s *mapStore) Delete(_ context.Context, ns Namespace, key string) error {
	s.deletes.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.m[string(ns)+key]; ok {
		old.Release()
		delete(s.m, string(ns)+key)
	}
	return nil
}

func (s *mapStore) Close() error {
	s.closed.Store(true)
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, e := range s.m {
		e.Release()
		delete(s.m, k)
	}
	return nil
}

type countMetrics struct {
	NopMetrics
	dropped atomic.Int32
}

func (m *countMetrics) IncCacheWriteDropped() { m.dropped.Add(1) }

// TestAsyncDropsWhenFull fills the write queue with blocked writes: the
// next Set returns ErrDropped at once, is counted, and takes no reference;
// once unblocked every write lands and every reference is dropped.
func TestAsyncDropsWhenFull(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		inner := newMapStore()
		unblock := make(chan struct{})
		inner.set = func(ctx context.Context) error {
			select {
			case <-unblock:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		m := &countMetrics{}
		a, err := NewAsync(inner, AsyncOptions{Queue: 2, Metrics: m})
		if err != nil {
			t.Fatal(err)
		}
		var freed atomic.Int32
		ctx := t.Context()
		for i, key := range []string{"a", "b", "c", "d"} {
			body := countingBuffer([]byte(key), &freed)
			err := a.Set(ctx, NSResult, key, &Entry{Body: body, Status: http.StatusOK}, time.Minute)
			body.Release() // the caller's reference: the queue holds its own
			switch {
			case i < 2 && err != nil:
				t.Fatalf("Set %s: %v", key, err)
			case i >= 2 && !errors.Is(err, ErrDropped):
				t.Fatalf("Set %s = %v, want ErrDropped", key, err)
			}
		}
		synctest.Wait() // both writers are blocked in the inner store
		if got := m.dropped.Load(); got != 2 || a.Dropped() != 2 {
			t.Errorf("dropped metric %d, Dropped() %d, want 2", got, a.Dropped())
		}
		if got := freed.Load(); got != 2 {
			t.Errorf("freed %d buffers while two writes are queued, want the 2 dropped ones", got)
		}
		close(unblock)
		a.Wait()
		if got := inner.sets.Load(); got != 2 {
			t.Errorf("inner sets = %d, want 2", got)
		}
		// A freed slot accepts writes again.
		body := countingBuffer([]byte("e"), &freed)
		if err := a.Set(ctx, NSResult, "e", &Entry{Body: body}, 0); err != nil {
			t.Errorf("Set after drain: %v", err)
		}
		body.Release()
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		if !inner.closed.Load() {
			t.Error("Close did not close the wrapped store")
		}
		if got := freed.Load(); got != 5 {
			t.Errorf("freed %d of 5 buffers after Close", got)
		}
		if err := a.Set(ctx, NSResult, "f", &Entry{Body: NewBuffer(nil, nil)}, 0); !errors.Is(err, ErrClosed) {
			t.Errorf("Set after Close = %v, want ErrClosed", err)
		}
		if err := a.Close(); err != nil {
			t.Errorf("second Close = %v", err)
		}
	})
}

// TestAsyncWriteOutlivesRequest: the request context ends right after Set;
// the write still runs (context.WithoutCancel) and is bounded by the write
// timeout.
func TestAsyncWriteOutlivesRequest(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		inner := newMapStore()
		var errs []error
		var mu sync.Mutex
		inner.set = func(ctx context.Context) error {
			<-ctx.Done() // never unblocked: only the timeout ends it
			return ctx.Err()
		}
		a, err := NewAsync(inner, AsyncOptions{Timeout: 5 * time.Second, OnError: func(_ Namespace, _ string, err error) {
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
		}})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		if err := a.Set(ctx, NSResult, "k", &Entry{Body: NewBuffer([]byte("x"), nil)}, 0); err != nil {
			t.Fatal(err)
		}
		cancel()
		start := time.Now()
		a.Wait()
		if d := time.Since(start); d != 5*time.Second {
			t.Errorf("write ended after %s, want the 5s write timeout", d)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(errs) != 1 || !errors.Is(errs[0], context.DeadlineExceeded) {
			t.Errorf("OnError got %v, want one deadline exceeded", errs)
		}
		_ = a.Close()
	})
}

func TestTieredReadFillAndWriteThrough(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fast, slow := newMapStore(), newMapStore()
		tiered, err := NewTiered(fast, slow)
		if err != nil {
			t.Fatal(err)
		}
		ctx := t.Context()
		var freed atomic.Int32

		// Only the slow tier has the entry: the hit fills the fast tier
		// with the remaining lifetime.
		body := countingBuffer([]byte("slow"), &freed)
		if err := slow.Set(ctx, NSResult, "k", &Entry{Body: body, Status: http.StatusOK}, time.Minute); err != nil {
			t.Fatal(err)
		}
		body.Release()
		synctest.Sleep(20 * time.Second)
		e, ok, err := tiered.Get(ctx, NSResult, "k")
		if err != nil || !ok || string(e.Body.Bytes()) != "slow" {
			t.Fatalf("Get = %v %v %v", ok, err, e.Body)
		}
		e.Release()
		fe, ok, _ := fast.Get(ctx, NSResult, "k")
		if !ok {
			t.Fatal("fast tier not filled")
		}
		if got, want := fe.ExpiresAt, e.ExpiresAt; !got.Equal(want) {
			t.Errorf("filled ExpiresAt %v, want the slow tier's %v", got, want)
		}
		fe.Release()
		synctest.Sleep(41 * time.Second) // past the original minute
		if _, ok, _ := tiered.Get(ctx, NSResult, "k"); ok {
			t.Error("entry served past its lifetime")
		}

		// Set writes through; Delete removes everywhere.
		body = countingBuffer([]byte("new"), &freed)
		if err := tiered.Set(ctx, NSResult, "n", &Entry{Body: body}, 0); err != nil {
			t.Fatal(err)
		}
		body.Release()
		if fast.sets.Load() != 2 || slow.sets.Load() != 2 {
			t.Errorf("sets fast %d slow %d, want 2 each", fast.sets.Load(), slow.sets.Load())
		}
		if err := tiered.Delete(ctx, NSResult, "n"); err != nil {
			t.Fatal(err)
		}
		if err := tiered.Close(); err != nil {
			t.Fatal(err)
		}
		if got := freed.Load(); got != 2 {
			t.Errorf("freed %d of 2 buffers", got)
		}
	})
}

func TestTieredErrorIsMiss(t *testing.T) {
	t.Parallel()
	broken, good := newMapStore(), newMapStore()
	broken.getErr = errors.New("redis down")
	tiered, err := NewTiered(broken, good)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if _, ok, err := tiered.Get(ctx, NSResult, "k"); ok || err == nil {
		t.Errorf("miss everywhere = %v, %v; want miss with the tier error", ok, err)
	}
	if err := good.Set(ctx, NSResult, "k", &Entry{Body: NewBuffer([]byte("x"), nil)}, 0); err != nil {
		t.Fatal(err)
	}
	e, ok, err := tiered.Get(ctx, NSResult, "k")
	if !ok || err != nil {
		t.Fatalf("hit behind a failing tier = %v, %v", ok, err)
	}
	e.Release()
	if _, err := NewTiered(); err == nil {
		t.Error("NewTiered() accepted no tiers")
	}
	if _, err := NewTiered(nil); err == nil {
		t.Error("NewTiered(nil) accepted a nil tier")
	}
}
