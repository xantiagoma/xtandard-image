package fetch

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
	"github.com/xantiagoma/xtandard-image/internal/spool"
)

// testSpooler is app's adapter of *spool.Spool.
type testSpooler struct{ s *spool.Spool }

func (a testSpooler) Spills(n int64) bool        { return a.s.Spills(n) }
func (a testSpooler) Threshold() int64           { return a.s.Threshold() }
func (a testSpooler) SetReclaimer(f func() bool) { a.s.SetReclaimer(f) }

func (a testSpooler) Create(reserve int64) (SpoolWriter, error) {
	w, err := a.s.Create(spool.KindInput, reserve)
	if err != nil {
		return nil, err
	}
	return testWriter{w}, nil
}

type testWriter struct{ *spool.Writer }

func (w testWriter) Finish(n int64) (Buffer, error) {
	b, err := w.Writer.Finish(n)
	if err != nil {
		return nil, err
	}
	return b, nil
}

const testThreshold = 1000

// newLarge is newHarness with a spool of threshold testThreshold and
// budget maxBytes (0: none), sharing spooled originals for ttl.
func newLarge(t *testing.T, cfg Config, ttl time.Duration, maxBytes int64, fn fetchFunc) (*harness, *spool.Spool) {
	t.Helper()
	sp, err := spool.New(spool.Config{Dir: filepath.Join(t.TempDir(), "spool"), Threshold: testThreshold, MaxBytes: maxBytes})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{src: &fakeSource{fn: fn}, alloc: &countingAlloc{}, rec: &recorder{}}
	h.reg = &fakeSources{src: h.src}
	f, err := New(h.reg, h.alloc.alloc, cfg, WithMetrics(h.rec), WithSpool(testSpooler{sp}, ttl))
	if err != nil {
		t.Fatal(err)
	}
	h.f = f
	return h, sp
}

// spoolFiles is the number of open spool files.
func spoolFiles(sp *spool.Spool) int {
	_, n := sp.Usage()
	return n
}

// unknownLength serves data without a declared length.
func unknownLength(data []byte) fetchFunc {
	return func(context.Context, int, source.Key, source.FetchOptions) (*source.Object, error) {
		return &source.Object{Body: io.NopCloser(bytes.NewReader(data)), Size: -1}, nil
	}
}

// declared serves data claiming size bytes.
func declared(data []byte, size int64) fetchFunc {
	return func(context.Context, int, source.Key, source.FetchOptions) (*source.Object, error) {
		return &source.Object{Body: io.NopCloser(bytes.NewReader(data)), Size: size}, nil
	}
}

// TestFetchLargeThreshold: what goes to a file, what stays in memory, and
// that a failed read leaves no file behind.
func TestFetchLargeThreshold(t *testing.T) {
	t.Parallel()
	small, large, huge := payload(testThreshold), payload(testThreshold+1), payload(5000)
	tests := []struct {
		name     string
		fn       fetchFunc
		want     []byte
		wantFile bool
		kind     errs.Kind
	}{
		{"declared, at the threshold", serve(small), small, false, 0},
		{"declared, over it", serve(large), large, true, 0},
		{"unknown length, small", unknownLength(small), small, false, 0},
		{"unknown length, large", unknownLength(huge), huge, true, 0},
		{"declared over the limit", serve(payload(5001)), nil, false, errs.KindSourceTooLarge},
		{"unknown length over the limit", unknownLength(payload(5001)), nil, false, errs.KindSourceTooLarge},
		{"body shorter than declared", declared(huge[:3000], 5000), nil, false, errs.KindUpstreamError},
		{"body longer than declared", declared(huge, 3000), nil, false, errs.KindUpstreamError},
		{"read error mid-body", func(context.Context, int, source.Key, source.FetchOptions) (*source.Object, error) {
			return &source.Object{Body: io.NopCloser(io.MultiReader(bytes.NewReader(huge), errReader{})), Size: -1}, nil
		}, nil, false, errs.KindUpstreamError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h, sp := newLarge(t, Config{MaxBytes: 5000}, 0, 0, tc.fn)
			r, err := h.f.FetchLarge(t.Context(), testKey("v.mp4"), source.FetchOptions{})
			if tc.kind != 0 {
				wantKind(t, err, tc.kind)
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if got := cache.IsFileBody(r.Buffer); got != tc.wantFile {
					t.Errorf("file body %v, want %v", got, tc.wantFile)
				}
				got, _ := io.ReadAll(cache.BodyReader(r.Buffer))
				if !bytes.Equal(got, tc.want) || r.Object.Size != int64(len(tc.want)) {
					t.Errorf("read %d bytes (size %d), want %d", len(got), r.Object.Size, len(tc.want))
				}
				r.Release()
			}
			if n := spoolFiles(sp); n != 0 {
				t.Errorf("%d spool files left", n)
			}
			if h.alloc.live() != 0 && tc.wantFile {
				t.Errorf("%d memory buffers left", h.alloc.live())
			}
		})
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

// TestFetchLargeShares: one download serves concurrent and later callers
// (the segments of a stream) from one spool file; Fetch never sees it.
func TestFetchLargeShares(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) { testFetchLargeShares(t) })
}

func testFetchLargeShares(t *testing.T) {
	data := payload(4000)
	gate := make(chan struct{})
	h, sp := newLarge(t, Config{}, time.Minute, 0, gated(gate, data, nil))
	const n = 8
	results := make([]*Result, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			r, err := h.f.FetchLarge(t.Context(), testKey("v.mp4"), source.FetchOptions{})
			if err != nil {
				t.Error(err)
				return
			}
			results[i] = r
		})
	}
	synctest.Wait() // all eight wait on one flight
	close(gate)
	wg.Wait()
	later, err := h.f.FetchLarge(t.Context(), testKey("v.mp4"), source.FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	results = append(results, later)
	for _, r := range results {
		if r == nil || r.Buffer != results[0].Buffer {
			t.Fatal("callers got different buffers")
		}
	}
	if !later.FromCache {
		t.Error("the later call did not come from the shared file")
	}
	if c := h.src.calls.Load(); c != 1 {
		t.Errorf("%d origin fetches, want 1", c)
	}
	if got, _, _ := h.rec.snapshot(); got[CacheSpoolHit] != 1 {
		t.Errorf("cache results %v", got)
	}
	// Fetch (images) does not share the spool file.
	img := mustFetch(t, h.f, testKey("v.mp4"))
	if cache.IsFileBody(img.Buffer) {
		t.Error("Fetch returned a spool file")
	}
	img.Release()
	for _, r := range results {
		r.Release()
	}
	if spoolFiles(sp) != 1 || h.f.SpooledUsage() != 1 {
		t.Errorf("%d spool files, %d kept; want the shared one", spoolFiles(sp), h.f.SpooledUsage())
	}
	h.f.Purge()
	if spoolFiles(sp) != 0 {
		t.Errorf("%d spool files after Purge", spoolFiles(sp))
	}
}

// TestFetchLargeTTL: a spooled original is reused without the origin for
// the TTL, revalidated (If-None-Match, 304) after it while in use, and
// dropped after a TTL without use.
func TestFetchLargeTTL(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		data := payload(3000)
		var inm []string
		var mu sync.Mutex
		h, sp := newLarge(t, Config{}, time.Minute, 0, func(_ context.Context, _ int, _ source.Key, opt source.FetchOptions) (*source.Object, error) {
			mu.Lock()
			inm = append(inm, opt.IfNoneMatch)
			mu.Unlock()
			if opt.IfNoneMatch == `"v1"` {
				return &source.Object{NotModified: true}, nil
			}
			return object(data, `"v1"`), nil
		})
		fetchOnce := func() {
			t.Helper()
			r, err := h.f.FetchLarge(t.Context(), testKey("v.mp4"), source.FetchOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !cache.IsFileBody(r.Buffer) {
				t.Fatal("not spooled")
			}
			r.Release()
		}
		fetchOnce()
		time.Sleep(40 * time.Second)
		fetchOnce() // fresh: no origin request
		time.Sleep(40 * time.Second)
		fetchOnce() // stale but used 40 s ago: revalidated
		synctest.Wait()
		mu.Lock()
		got := append([]string(nil), inm...)
		mu.Unlock()
		if len(got) != 2 || got[0] != "" || got[1] != `"v1"` {
			t.Fatalf("origin requests (If-None-Match) %q, want an unconditional and a 304", got)
		}
		if c, _, _ := h.rec.snapshot(); c[CacheRevalidated] != 1 || c[CacheSpoolHit] != 1 {
			t.Errorf("cache results %v", c)
		}
		time.Sleep(59 * time.Second)
		synctest.Wait()
		if spoolFiles(sp) != 1 {
			t.Fatal("dropped before a TTL without use")
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if spoolFiles(sp) != 0 || h.f.SpooledUsage() != 0 {
			t.Errorf("kept after a TTL without use: %d files", spoolFiles(sp))
		}
	})
}

// TestFetchLargeBudget: a source that does not fit the spool budget is a
// 503 after the kept originals are dropped; dropping them makes room.
func TestFetchLargeBudget(t *testing.T) {
	t.Parallel()
	h, sp := newLarge(t, Config{}, time.Hour, 6000, serve(payload(4000)))
	a, err := h.f.FetchLarge(t.Context(), testKey("a.mp4"), source.FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// a is in use: the kept copy cannot be freed, b does not fit.
	_, err = h.f.FetchLarge(t.Context(), testKey("b.mp4"), source.FetchOptions{})
	wantKind(t, err, errs.KindUnavailable)
	if _, ok := errors.AsType[*spool.BudgetError](err); !ok {
		t.Errorf("no BudgetError in %v", err)
	}
	a.Release()
	// a is only kept now (the failed attempt already dropped it).
	b, err := h.f.FetchLarge(t.Context(), testKey("b.mp4"), source.FetchOptions{})
	if err != nil {
		t.Fatalf("after a was released: %v", err)
	}
	b.Release()
	// b is kept; c reclaims it.
	c, err := h.f.FetchLarge(t.Context(), testKey("c.mp4"), source.FetchOptions{})
	if err != nil {
		t.Fatalf("reclaiming b: %v", err)
	}
	c.Release()
	h.f.Purge()
	if b, f := sp.Usage(); b != 0 || f != 0 {
		t.Errorf("spool holds %d bytes in %d files", b, f)
	}
}

// TestFetchLargeCancel: a caller that leaves does not leak the download's
// file; with no sharing the file is gone once the download ends.
func TestFetchLargeCancel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		h, sp := newLarge(t, Config{}, 0, 0, gated(gate, payload(4000), nil))
		ctx, cancel := context.WithCancel(t.Context())
		errc := make(chan error, 1)
		go func() {
			_, err := h.f.FetchLarge(ctx, testKey("v.mp4"), source.FetchOptions{})
			errc <- err
		}()
		synctest.Wait() // the download waits on the gate
		cancel()
		if err := <-errc; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled caller: %v", err)
		}
		close(gate)
		synctest.Wait() // the detached download ends and drops its file
		if n := spoolFiles(sp); n != 0 {
			t.Errorf("%d spool files after the cancelled fetch", n)
		}
	})
}

// TestFetchLargeWithoutSpool: without WithSpool, FetchLarge is Fetch.
func TestFetchLargeWithoutSpool(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{}, serve(payload(5000)))
	r, err := h.f.FetchLarge(t.Context(), testKey("v.mp4"), source.FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release()
	if cache.IsFileBody(r.Buffer) {
		t.Error("spooled without a spool")
	}
}
