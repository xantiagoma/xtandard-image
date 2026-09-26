package fetch

import (
	"bytes"
	"context"
	"io"
	"maps"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

// fetchFunc is a fake backend: n is the 1-based call number.
type fetchFunc func(ctx context.Context, n int, key source.Key, opt source.FetchOptions) (*source.Object, error)

// fakeSource counts calls and records the options of each.
type fakeSource struct {
	fn    fetchFunc
	calls atomic.Int64

	mu   sync.Mutex
	opts []source.FetchOptions
}

func (s *fakeSource) Name() string { return "fake" }
func (s *fakeSource) Type() string { return source.TypeHTTP }

func (s *fakeSource) Fetch(ctx context.Context, key source.Key, opt source.FetchOptions) (*source.Object, error) {
	n := int(s.calls.Add(1))
	s.mu.Lock()
	s.opts = append(s.opts, opt)
	s.mu.Unlock()
	return s.fn(ctx, n, key, opt)
}

func (s *fakeSource) lastOpts() source.FetchOptions {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opts[len(s.opts)-1]
}

// fakeSources is a one-source registry that records redirect checks.
type fakeSources struct {
	src       *fakeSource
	redirects atomic.Int64
}

func (r *fakeSources) Source(name string) (source.Source, bool) {
	if name != "fake" {
		return nil, false
	}
	return r.src, true
}

func (r *fakeSources) ResolveRedirect(from source.Key, _ string) (source.Key, error) {
	r.redirects.Add(1)
	return from, nil
}

// countingAlloc is an Allocator that counts allocations and frees.
type countingAlloc struct {
	allocs atomic.Int64
	frees  atomic.Int64
}

func (a *countingAlloc) alloc(n int) (Buffer, error) {
	a.allocs.Add(1)
	return NewGoBuffer(n, func() { a.frees.Add(1) }), nil
}

func (a *countingAlloc) live() int64 { return a.allocs.Load() - a.frees.Load() }

// recorder is a race-safe Metrics fake.
type recorder struct {
	mu        sync.Mutex
	cache     map[CacheResult]int
	outcomes  []string
	coalesced int
	retries   int
}

func (r *recorder) ObserveFetch(_, outcome string, _ time.Duration, _ int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outcomes = append(r.outcomes, outcome)
}

func (r *recorder) OnCache(c CacheResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache == nil {
		r.cache = make(map[CacheResult]int)
	}
	r.cache[c]++
}

func (r *recorder) OnCoalesced() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.coalesced++
}

func (r *recorder) OnRetry(string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.retries++
}

func (r *recorder) snapshot() (cache map[CacheResult]int, coalesced, retries int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return maps.Clone(r.cache), r.coalesced, r.retries
}

type harness struct {
	f     *Fetcher
	src   *fakeSource
	reg   *fakeSources
	alloc *countingAlloc
	rec   *recorder
}

func newHarness(t *testing.T, cfg Config, fn fetchFunc) *harness {
	t.Helper()
	h := &harness{src: &fakeSource{fn: fn}, alloc: &countingAlloc{}, rec: &recorder{}}
	h.reg = &fakeSources{src: h.src}
	f, err := New(h.reg, h.alloc.alloc, cfg, WithMetrics(h.rec))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.f = f
	return h
}

func testKey(path string) source.Key {
	return source.Key{Source: "fake", Path: "https://img.example.com/" + path}
}

// object returns a 200 with a declared length.
func object(data []byte, etag string) *source.Object {
	return &source.Object{
		Body:        io.NopCloser(bytes.NewReader(data)),
		Size:        int64(len(data)),
		ContentType: "image/jpeg",
		ETag:        etag,
	}
}

// serve returns a fetchFunc that always serves data.
func serve(data []byte) fetchFunc {
	return func(context.Context, int, source.Key, source.FetchOptions) (*source.Object, error) {
		return object(data, ""), nil
	}
}

// gated returns a fetchFunc that blocks until gate closes (or ctx ends)
// and then serves data. It reports the backend context's error on ctxErr.
func gated(gate <-chan struct{}, data []byte, ctxErr *atomic.Value) fetchFunc {
	return func(ctx context.Context, _ int, _ source.Key, _ source.FetchOptions) (*source.Object, error) {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if ctxErr != nil {
			ctxErr.Store(errBox{ctx.Err()})
		}
		return object(data, `"v1"`), nil
	}
}

type errBox struct{ err error }

func payload(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i * 7)
	}
	return b
}

func wantKind(t *testing.T, err error, want errs.Kind) {
	t.Helper()
	if err == nil {
		t.Fatalf("err = nil, want kind %s", want)
	}
	if got := errs.KindOf(err); got != want {
		t.Fatalf("KindOf(%v) = %s, want %s", err, got, want)
	}
}

func mustFetch(t *testing.T, f *Fetcher, key source.Key) *Result {
	t.Helper()
	r, err := f.Fetch(t.Context(), key, source.FetchOptions{})
	if err != nil {
		t.Fatalf("Fetch(%s): %v", key.Path, err)
	}
	return r
}
