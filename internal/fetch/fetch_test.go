package fetch

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

const cachedTTL = time.Minute

func cachedCfg() Config {
	return Config{CacheBytes: 1 << 20, CacheTTL: cachedTTL, NegativeTTL: 5 * time.Second, Retry: true}
}

// TestCoalescing is the WP-18 done criterion: ten concurrent requests (ten
// sizes of one original) hit the origin once and share one buffer.
func TestCoalescing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		cfg      Config
		wantRefs int64 // after all ten results arrive
	}{
		{name: "no cache", cfg: Config{}, wantRefs: 10},
		{name: "with cache", cfg: cachedCfg(), wantRefs: 11},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				data := payload(4096)
				gate := make(chan struct{})
				h := newHarness(t, tc.cfg, gated(gate, data, nil))
				key := testKey("a.jpg")

				const n = 10
				results := make([]*Result, n)
				var wg sync.WaitGroup
				for i := range n {
					wg.Go(func() {
						r, err := h.f.Fetch(t.Context(), key, source.FetchOptions{})
						if err != nil {
							t.Errorf("Fetch %d: %v", i, err)
							return
						}
						results[i] = r
					})
				}
				synctest.Wait() // all ten are waiting on one flight
				close(gate)
				wg.Wait()
				if t.Failed() {
					return
				}

				if got := h.src.calls.Load(); got != 1 {
					t.Fatalf("backend calls = %d, want 1", got)
				}
				if got := h.alloc.allocs.Load(); got != 1 {
					t.Fatalf("allocations = %d, want 1", got)
				}
				for i, r := range results {
					if r.Buffer != results[0].Buffer {
						t.Fatalf("result %d has a different buffer", i)
					}
					if !bytes.Equal(r.Bytes(), data) {
						t.Fatalf("result %d bytes differ", i)
					}
				}
				if gb, ok := results[0].Buffer.(*GoBuffer); !ok || gb.Refs() != tc.wantRefs {
					t.Fatalf("buffer %T: refs want %d", results[0].Buffer, tc.wantRefs)
				}
				if got := h.alloc.live(); got != 1 {
					t.Fatalf("live buffers = %d, want 1", got)
				}
				cache, coalesced, _ := h.rec.snapshot()
				if coalesced != n-1 || cache[CacheMiss] != n {
					t.Fatalf("coalesced = %d, misses = %d; want %d, %d", coalesced, cache[CacheMiss], n-1, n)
				}
				for _, r := range results {
					r.Release()
				}
				h.f.Purge()
				if got := h.alloc.live(); got != 0 {
					t.Fatalf("live buffers = %d, want 0", got)
				}
			})
		})
	}
}

// TestLastReleaseFrees proves only the final reference frees the buffer
// through the allocator.
func TestLastReleaseFrees(t *testing.T) {
	t.Parallel()
	h := newHarness(t, cachedCfg(), serve(payload(100)))
	key := testKey("a.jpg")

	r1 := mustFetch(t, h.f, key)
	r2 := mustFetch(t, h.f, key) // cache hit: same buffer, new reference
	if !r2.FromCache || r1.Buffer != r2.Buffer {
		t.Fatalf("second fetch: FromCache = %v, same buffer = %v; want true, true", r2.FromCache, r1.Buffer == r2.Buffer)
	}
	steps := []struct {
		name      string
		do        func()
		wantFrees int64
	}{
		{"release first", r1.Release, 0},
		{"release first again (no-op)", r1.Release, 0},
		{"release second", r2.Release, 0},
		{"purge cache (last reference)", h.f.Purge, 1},
	}
	for _, s := range steps {
		s.do()
		if got := h.alloc.frees.Load(); got != s.wantFrees {
			t.Fatalf("after %s: frees = %d, want %d", s.name, got, s.wantFrees)
		}
	}
	if r1.Buffer.Bytes() != nil {
		t.Fatal("freed GoBuffer still exposes bytes")
	}
}

func TestTimeout(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		retry       bool
		wantCalls   int64
		wantElapsed [2]time.Duration // inclusive range
	}{
		{name: "no retry", retry: false, wantCalls: 1, wantElapsed: [2]time.Duration{2 * time.Second, 2 * time.Second}},
		{name: "retry once", retry: true, wantCalls: 2, wantElapsed: [2]time.Duration{4*time.Second + 50*time.Millisecond, 4*time.Second + 150*time.Millisecond}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				hang := func(ctx context.Context, _ int, _ source.Key, _ source.FetchOptions) (*source.Object, error) {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				h := newHarness(t, Config{Timeout: 2 * time.Second, Retry: tc.retry, RetryBackoff: 100 * time.Millisecond}, hang)
				start := time.Now()
				_, err := h.f.Fetch(t.Context(), testKey("slow.jpg"), source.FetchOptions{})
				elapsed := time.Since(start)
				wantKind(t, err, errs.KindUpstreamTimeout)
				if got := h.src.calls.Load(); got != tc.wantCalls {
					t.Fatalf("calls = %d, want %d", got, tc.wantCalls)
				}
				if elapsed < tc.wantElapsed[0] || elapsed > tc.wantElapsed[1] {
					t.Fatalf("elapsed = %s, want within %v", elapsed, tc.wantElapsed)
				}
			})
		})
	}
}

// TestTimeoutWhileReadingBody covers a slow body rather than slow headers.
func TestTimeoutWhileReadingBody(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		slow := func(ctx context.Context, _ int, _ source.Key, _ source.FetchOptions) (*source.Object, error) {
			return &source.Object{Body: io.NopCloser(readerFunc(func([]byte) (int, error) {
				<-ctx.Done()
				return 0, ctx.Err()
			})), Size: -1}, nil
		}
		h := newHarness(t, Config{Timeout: time.Second}, slow)
		_, err := h.f.Fetch(t.Context(), testKey("slow.jpg"), source.FetchOptions{})
		wantKind(t, err, errs.KindUpstreamTimeout)
		if got := h.alloc.live(); got != 0 {
			t.Fatalf("live buffers = %d, want 0", got)
		}
	})
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

func TestRetry(t *testing.T) {
	t.Parallel()
	upstream := errs.New(errs.KindUpstreamError, "origin 503")
	failFirst := func(first error) fetchFunc {
		return func(_ context.Context, n int, _ source.Key, _ source.FetchOptions) (*source.Object, error) {
			if n == 1 {
				return nil, first
			}
			return object(payload(10), ""), nil
		}
	}
	always := func(err error) fetchFunc {
		return func(context.Context, int, source.Key, source.FetchOptions) (*source.Object, error) {
			return nil, err
		}
	}
	for _, tc := range []struct {
		name        string
		retry       bool
		fn          fetchFunc
		wantCalls   int64
		wantKind    errs.Kind // zero (KindUnknown) means success
		wantRetries int
	}{
		{"5xx then ok", true, failFirst(upstream), 2, errs.KindUnknown, 1},
		{"untyped error then ok", true, failFirst(errors.New("connection reset")), 2, errs.KindUnknown, 1},
		{"5xx twice", true, always(upstream), 2, errs.KindUpstreamError, 1},
		{"untyped error twice", true, always(errors.New("connection refused")), 2, errs.KindUpstreamError, 1},
		{"not found is not retried", true, always(errs.New(errs.KindNotFound, "404")), 1, errs.KindNotFound, 0},
		{"forbidden is not retried", true, always(errs.New(errs.KindForbidden, "403")), 1, errs.KindForbidden, 0},
		{"retry disabled", false, failFirst(upstream), 1, errs.KindUpstreamError, 0},
		{"truncated body then ok", true, func(_ context.Context, n int, _ source.Key, _ source.FetchOptions) (*source.Object, error) {
			o := object(payload(10), "")
			if n == 1 {
				o.Size = 20
			}
			return o, nil
		}, 2, errs.KindUnknown, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newHarness(t, Config{Retry: tc.retry}, tc.fn)
				start := time.Now()
				r, err := h.f.Fetch(t.Context(), testKey("x.jpg"), source.FetchOptions{})
				if tc.wantKind == errs.KindUnknown {
					if err != nil {
						t.Fatalf("Fetch: %v", err)
					}
					r.Release()
				} else {
					wantKind(t, err, tc.wantKind)
				}
				if got := h.src.calls.Load(); got != tc.wantCalls {
					t.Fatalf("calls = %d, want %d", got, tc.wantCalls)
				}
				if _, _, retries := h.rec.snapshot(); retries != tc.wantRetries {
					t.Fatalf("retries = %d, want %d", retries, tc.wantRetries)
				}
				if tc.wantRetries == 1 {
					// Default backoff 100ms jittered to [50ms, 150ms].
					if d := time.Since(start); d < 50*time.Millisecond || d > 150*time.Millisecond {
						t.Fatalf("backoff = %s, want within [50ms, 150ms]", d)
					}
				}
				if got := h.alloc.live(); got != 0 {
					t.Fatalf("live buffers = %d, want 0", got)
				}
			})
		})
	}
}

// TestWaiterCancellation proves a caller that gives up does not abort the
// shared fetch other callers (and the cache) still want.
func TestWaiterCancellation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		keepOther    bool // a second caller stays
		cancelLeader bool
	}{
		{name: "leader cancels, follower stays", keepOther: true, cancelLeader: true},
		{name: "follower cancels, leader stays", keepOther: true, cancelLeader: false},
		{name: "everyone cancels", keepOther: false, cancelLeader: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				data := payload(256)
				gate := make(chan struct{})
				var backendErr atomic.Value
				h := newHarness(t, cachedCfg(), gated(gate, data, &backendErr))
				key := testKey("a.jpg")

				cancelCtx, cancel := context.WithCancel(t.Context())
				defer cancel()
				ctxs := []context.Context{cancelCtx, t.Context()}
				if !tc.cancelLeader {
					ctxs[0], ctxs[1] = ctxs[1], ctxs[0]
				}
				if !tc.keepOther {
					ctxs = ctxs[:1]
				}
				type out struct {
					r   *Result
					err error
				}
				outs := make([]chan out, len(ctxs))
				for i, ctx := range ctxs {
					outs[i] = make(chan out, 1)
					go func() {
						r, err := h.f.Fetch(ctx, key, source.FetchOptions{})
						outs[i] <- out{r, err}
					}()
					synctest.Wait() // join in order: ctxs[0] leads
				}

				cancel()
				synctest.Wait()
				canceledIdx := 0
				if !tc.cancelLeader {
					canceledIdx = 1
				}
				got := <-outs[canceledIdx]
				wantKind(t, got.err, errs.KindCanceled)

				close(gate)
				synctest.Wait()
				if e, _ := backendErr.Load().(errBox); e.err != nil {
					t.Fatalf("backend context ended: %v", e.err)
				}
				if tc.keepOther {
					o := <-outs[1-canceledIdx]
					if o.err != nil {
						t.Fatalf("surviving caller: %v", o.err)
					}
					if !bytes.Equal(o.r.Bytes(), data) {
						t.Fatal("surviving caller got wrong bytes")
					}
					o.r.Release()
				}

				// The abandoned fetch still filled the cache.
				r := mustFetch(t, h.f, key)
				if !r.FromCache {
					t.Fatal("FromCache = false after the shared fetch completed")
				}
				r.Release()
				if got := h.src.calls.Load(); got != 1 {
					t.Fatalf("calls = %d, want 1", got)
				}
				h.f.Purge()
				if got := h.alloc.live(); got != 0 {
					t.Fatalf("live buffers = %d, want 0", got)
				}
			})
		})
	}
}

func TestRevalidation(t *testing.T) {
	t.Parallel()
	lastMod := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		etag     string
		lastMod  time.Time
		wantINM  string
		wantIMS  time.Time
		validate bool // a conditional request is expected after expiry
	}{
		{name: "etag", etag: `"v1"`, wantINM: `"v1"`, validate: true},
		{name: "last-modified", lastMod: lastMod, wantIMS: lastMod, validate: true},
		{name: "no validators", validate: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				v1, v2 := payload(100), payload(120)
				var changed atomic.Bool
				fn := func(_ context.Context, _ int, _ source.Key, opt source.FetchOptions) (*source.Object, error) {
					conditional := opt.IfNoneMatch != "" || !opt.IfModifiedSince.IsZero()
					if conditional && !changed.Load() {
						return &source.Object{NotModified: true, Size: -1}, nil
					}
					data := v1
					if changed.Load() {
						data = v2
					}
					o := object(data, tc.etag)
					o.LastModified = tc.lastMod
					return o, nil
				}
				h := newHarness(t, cachedCfg(), fn)
				key := testKey("a.jpg")

				first := mustFetch(t, h.f, key)
				first.Release()
				synctest.Sleep(cachedTTL + time.Second)

				r := mustFetch(t, h.f, key)
				opt := h.src.lastOpts()
				if got := h.src.calls.Load(); got != 2 {
					t.Fatalf("calls after expiry = %d, want 2", got)
				}
				if diff := cmp.Diff([]any{tc.wantINM, tc.wantIMS}, []any{opt.IfNoneMatch, opt.IfModifiedSince}); diff != "" {
					t.Fatalf("conditional headers (-want +got):\n%s", diff)
				}
				if r.FromCache != tc.validate {
					t.Fatalf("FromCache = %v, want %v", r.FromCache, tc.validate)
				}
				if tc.validate && r.Buffer != first.Buffer {
					t.Fatal("304 did not reuse the cached buffer")
				}
				r.Release()
				cache, _, _ := h.rec.snapshot()
				if wantReval := map[bool]int{true: 1}[tc.validate]; cache[CacheRevalidated] != wantReval {
					t.Fatalf("revalidated = %d, want %d", cache[CacheRevalidated], wantReval)
				}

				// The TTL was refreshed: no backend call inside it.
				r = mustFetch(t, h.f, key)
				r.Release()
				if got := h.src.calls.Load(); got != 2 {
					t.Fatalf("calls within refreshed TTL = %d, want 2", got)
				}

				// Origin changed: the conditional request returns new bytes.
				changed.Store(true)
				synctest.Sleep(cachedTTL + time.Second)
				r = mustFetch(t, h.f, key)
				if r.FromCache || !bytes.Equal(r.Bytes(), v2) {
					t.Fatalf("after change: FromCache = %v, len = %d; want false, %d", r.FromCache, len(r.Bytes()), len(v2))
				}
				r.Release()
				if got := h.alloc.live(); got != 1 { // only the new cached original
					t.Fatalf("live buffers = %d, want 1", got)
				}
				h.f.Purge()
			})
		})
	}
}

func TestLRUEvictionByBytes(t *testing.T) {
	t.Parallel()
	calls := map[string]*atomic.Int64{"a": {}, "b": {}, "c": {}, "big": {}}
	fn := func(_ context.Context, _ int, key source.Key, _ source.FetchOptions) (*source.Object, error) {
		name := key.Path[len("https://img.example.com/"):]
		calls[name].Add(1)
		if name == "big" {
			return object(payload(300), ""), nil
		}
		return object(payload(100), ""), nil
	}
	h := newHarness(t, Config{CacheBytes: 250, CacheTTL: time.Hour}, fn)
	get := func(name string) {
		t.Helper()
		mustFetch(t, h.f, testKey(name)).Release()
	}

	get("a")
	get("b")
	get("a") // hit; a becomes most recently used
	get("c") // 300 > 250: evicts b, the least recently used
	if bytes, entries := h.f.CacheUsage(); bytes != 200 || entries != 2 {
		t.Fatalf("usage = %d bytes, %d entries; want 200, 2", bytes, entries)
	}
	if got := h.alloc.live(); got != 2 {
		t.Fatalf("live buffers = %d, want 2 (evicted b freed)", got)
	}
	get("a")
	get("c")
	get("b")
	get("big") // larger than the whole budget: served, never cached
	get("big")
	got := map[string]int64{}
	for k, v := range calls {
		got[k] = v.Load()
	}
	if diff := cmp.Diff(map[string]int64{"a": 1, "b": 2, "c": 1, "big": 2}, got); diff != "" {
		t.Fatalf("backend calls (-want +got):\n%s", diff)
	}
	h.f.Purge()
	if got := h.alloc.live(); got != 0 {
		t.Fatalf("live buffers = %d, want 0", got)
	}
}

func TestNegativeCache(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		negTTL    time.Duration
		wantCalls []int64 // after fetch 1, fetch 2, fetch 3 (after the ttl)
	}{
		{name: "enabled", negTTL: 5 * time.Second, wantCalls: []int64{1, 1, 2}},
		{name: "disabled", negTTL: 0, wantCalls: []int64{1, 2, 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				notFound := func(context.Context, int, source.Key, source.FetchOptions) (*source.Object, error) {
					return nil, errs.New(errs.KindNotFound, "origin 404")
				}
				cfg := cachedCfg()
				cfg.NegativeTTL = tc.negTTL
				h := newHarness(t, cfg, notFound)
				got := make([]int64, 0, 3)
				for i := range 3 {
					if i == 2 {
						synctest.Sleep(6 * time.Second)
					}
					_, err := h.f.Fetch(t.Context(), testKey("missing.jpg"), source.FetchOptions{})
					wantKind(t, err, errs.KindNotFound)
					got = append(got, h.src.calls.Load())
				}
				if diff := cmp.Diff(tc.wantCalls, got); diff != "" {
					t.Fatalf("calls (-want +got):\n%s", diff)
				}
			})
		})
	}
}

func TestLimits(t *testing.T) {
	t.Parallel()
	const maxBytes = 1000
	unknown := func(data []byte) fetchFunc {
		return func(context.Context, int, source.Key, source.FetchOptions) (*source.Object, error) {
			return &source.Object{Body: io.NopCloser(iotest.HalfReader(bytes.NewReader(data))), Size: -1}, nil
		}
	}
	for _, tc := range []struct {
		name      string
		maxBytes  int64 // Config.MaxBytes
		callerMax int64 // FetchOptions.MaxBytes
		fn        fetchFunc
		wantLen   int
		wantKind  errs.Kind // zero (KindUnknown) means success
	}{
		{name: "declared length at limit", maxBytes: maxBytes, fn: serve(payload(maxBytes)), wantLen: maxBytes},
		{name: "declared length over limit", maxBytes: maxBytes, fn: serve(payload(maxBytes + 1)), wantKind: errs.KindSourceTooLarge},
		{name: "unknown length at limit", maxBytes: maxBytes, fn: unknown(payload(maxBytes)), wantLen: maxBytes},
		{name: "unknown length over limit", maxBytes: maxBytes, fn: unknown(payload(maxBytes + 1)), wantKind: errs.KindSourceTooLarge},
		{name: "unknown length grows past first chunk", maxBytes: 1 << 20, fn: unknown(payload(200 << 10)), wantLen: 200 << 10},
		{name: "unknown length fills a chunk exactly", maxBytes: 1 << 20, fn: unknown(payload(initialChunk)), wantLen: initialChunk},
		{name: "caller limit below config", maxBytes: maxBytes, callerMax: 10, fn: serve(payload(11)), wantKind: errs.KindSourceTooLarge},
		{name: "empty body", maxBytes: maxBytes, fn: unknown(nil), wantKind: errs.KindCorrupt},
		{name: "body longer than declared", maxBytes: maxBytes, fn: func(context.Context, int, source.Key, source.FetchOptions) (*source.Object, error) {
			o := object(payload(20), "")
			o.Size = 10
			return o, nil
		}, wantKind: errs.KindUpstreamError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, Config{MaxBytes: tc.maxBytes}, tc.fn)
			r, err := h.f.Fetch(t.Context(), testKey("x.jpg"), source.FetchOptions{MaxBytes: tc.callerMax})
			if tc.wantKind != errs.KindUnknown {
				wantKind(t, err, tc.wantKind)
			} else {
				if err != nil {
					t.Fatalf("Fetch: %v", err)
				}
				if !bytes.Equal(r.Bytes(), payload(tc.wantLen)) {
					t.Fatalf("len = %d, want %d with matching content", len(r.Bytes()), tc.wantLen)
				}
				if r.Object.Size != int64(tc.wantLen) || r.Object.Body != nil {
					t.Fatalf("Object = %+v, want Size %d and nil Body", r.Object, tc.wantLen)
				}
				r.Release()
			}
			if got := h.alloc.live(); got != 0 {
				t.Fatalf("live buffers = %d, want 0", got)
			}
		})
	}
}

// TestDeclaredTooLargeIsNotRead checks Content-Length is enforced before
// any byte is read or allocated.
func TestDeclaredTooLargeIsNotRead(t *testing.T) {
	t.Parallel()
	var reads atomic.Int64
	fn := func(context.Context, int, source.Key, source.FetchOptions) (*source.Object, error) {
		return &source.Object{Body: io.NopCloser(countingReader{&reads}), Size: 1 << 40}, nil
	}
	h := newHarness(t, Config{}, fn)
	_, err := h.f.Fetch(t.Context(), testKey("huge.jpg"), source.FetchOptions{})
	wantKind(t, err, errs.KindSourceTooLarge)
	if reads.Load() != 0 || h.alloc.allocs.Load() != 0 {
		t.Fatalf("reads = %d, allocs = %d; want 0, 0", reads.Load(), h.alloc.allocs.Load())
	}
}

type countingReader struct{ n *atomic.Int64 }

func (r countingReader) Read(p []byte) (int, error) {
	r.n.Add(1)
	return len(p), nil
}

func TestFetchErrors(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{}, serve(payload(10)))

	_, err := h.f.Fetch(t.Context(), source.Key{Source: "nope", Path: "x"}, source.FetchOptions{})
	wantKind(t, err, errs.KindForbidden)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = h.f.Fetch(ctx, testKey("x.jpg"), source.FetchOptions{})
	wantKind(t, err, errs.KindCanceled)
	if got := h.src.calls.Load(); got != 0 {
		t.Fatalf("calls = %d, want 0", got)
	}

	unexpected304 := newHarness(t, Config{}, func(context.Context, int, source.Key, source.FetchOptions) (*source.Object, error) {
		return &source.Object{NotModified: true}, nil
	})
	_, err = unexpected304.f.Fetch(t.Context(), testKey("x.jpg"), source.FetchOptions{})
	wantKind(t, err, errs.KindUpstreamError)

	if _, err := New(nil, GoAllocator, Config{}); err == nil {
		t.Fatal("New(nil sources) succeeded")
	}
	if _, err := New(h.reg, GoAllocator, Config{CacheTTL: -1}); err == nil {
		t.Fatal("New(negative ttl) succeeded")
	}
}

// TestRedirectBinding checks a nil CheckRedirect is bound to the
// registry's policy for the key, and an explicit one is kept.
func TestRedirectBinding(t *testing.T) {
	t.Parallel()
	fn := func(_ context.Context, _ int, _ source.Key, opt source.FetchOptions) (*source.Object, error) {
		if opt.CheckRedirect == nil {
			return nil, errs.New(errs.KindInternal, "no redirect policy")
		}
		if err := opt.CheckRedirect("https://img.example.com/moved.jpg"); err != nil {
			return nil, err
		}
		return object(payload(10), ""), nil
	}
	h := newHarness(t, Config{}, fn)
	mustFetch(t, h.f, testKey("a.jpg")).Release()
	if got := h.reg.redirects.Load(); got != 1 {
		t.Fatalf("registry redirect checks = %d, want 1", got)
	}

	var own atomic.Int64
	r, err := h.f.Fetch(t.Context(), testKey("b.jpg"), source.FetchOptions{CheckRedirect: func(string) error {
		own.Add(1)
		return nil
	}})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	r.Release()
	if own.Load() != 1 || h.reg.redirects.Load() != 1 {
		t.Fatalf("own checks = %d, registry checks = %d; want 1, 1", own.Load(), h.reg.redirects.Load())
	}
}

func TestCacheResultString(t *testing.T) {
	t.Parallel()
	got := []string{CacheMiss.String(), CacheHit.String(), CacheNegativeHit.String(), CacheRevalidated.String(), CacheResult(9).String()}
	want := []string{"miss", "hit", "negative_hit", "revalidated", "CacheResult(9)"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("String() (-want +got):\n%s", diff)
	}
}
