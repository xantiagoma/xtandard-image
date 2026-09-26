package server

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/cache/memory"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// newCachedHarness is newHarness with a memory result cache behind an
// AsyncStore and debug headers on. The store is closed at cleanup; tests
// that check releases close it themselves first (Close is idempotent).
func newCachedHarness(tb testing.TB, mutate func(*harness)) (*harness, *cache.AsyncStore) {
	tb.Helper()
	mem, err := memory.New(memory.Options{MaxBytes: 1 << 20})
	if err != nil {
		tb.Fatal(err)
	}
	store, err := cache.NewAsync(mem, cache.AsyncOptions{})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = store.Close() })
	h := newHarness(tb, func(h *harness) {
		h.cache = store
		h.cfg.DebugHeaders = true
		h.cfg.ResultCache = ResultCacheConfig{TTL: time.Hour, Build: "test"}
		if mutate != nil {
			mutate(h)
		}
	})
	return h, store
}

// comparableHeader drops the headers that legitimately differ between a
// miss and a hit.
func comparableHeader(h http.Header) http.Header {
	c := h.Clone()
	c.Del(hdrXiCache)
	c.Del("X-Request-ID")
	return c
}

// TestResultCacheHit: the second identical request is served from the
// cache without a fetch, admission, probe or processing, with the same
// status, headers and body; conditional and HEAD requests are answered
// from the entry; every buffer is released once the cache lets go.
func TestResultCacheHit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*harness)
		accept string
		want   map[string]string // headers the miss (and so the hit) must carry
	}{
		{name: "negotiated", accept: "image/webp,*/*"},
		{name: "fixed format", mutate: func(h *harness) {
			h.router.mutate = func(r *transform.Request) { r.Output.Format = transform.FormatJPEG }
		}},
		{name: "client hints", accept: "*/*", mutate: func(h *harness) {
			h.cfg.Negotiate.EnableClientHints = true
		}, want: map[string]string{"Content-Dpr": "2", "Vary": "Accept, Sec-CH-DPR, Save-Data, ECT"}}, //nolint:misspell // ECT is a hint header
		{name: "skip processing passthrough", mutate: func(h *harness) {
			h.router.mutate = func(r *transform.Request) { r.Delivery.SkipProcessing = []transform.Format{transform.FormatJPEG} }
		}, want: map[string]string{"Content-Type": "image/jpeg", "X-Xi-Result-Width": ""}},
		{name: "source passthrough cache control", mutate: func(h *harness) {
			h.cfg.Cache.Passthrough = true
			h.f.obj.CacheControl = "public, max-age=600"
			h.f.obj.LastModified = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		}, want: map[string]string{"Cache-Control": "public, max-age=600", "Last-Modified": "Fri, 02 Jan 2026 03:04:05 GMT"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h, store := newCachedHarness(t, tc.mutate)
			hdr := http.Header{}
			if tc.accept != "" {
				hdr.Set("Accept", tc.accept)
			}
			hdr.Set("Sec-Ch-Dpr", "2")

			miss := h.do(t, http.MethodGet, okPath, hdr)
			if miss.Code != http.StatusOK || miss.Header().Get(hdrXiCache) != cacheMiss {
				t.Fatalf("first: %d %q %q", miss.Code, miss.Header().Get(hdrXiCache), miss.Body.String())
			}
			for k, v := range tc.want {
				if got := miss.Header().Get(k); got != v {
					t.Errorf("miss %s = %q, want %q", k, got, v)
				}
			}
			store.Wait()
			hit := h.do(t, http.MethodGet, okPath, hdr)
			if hit.Code != http.StatusOK || hit.Header().Get(hdrXiCache) != cacheHit {
				t.Fatalf("second: %d %q", hit.Code, hit.Header().Get(hdrXiCache))
			}
			if diff := cmp.Diff(comparableHeader(miss.Header()), comparableHeader(hit.Header())); diff != "" {
				t.Errorf("hit headers differ (-miss +hit):\n%s", diff)
			}
			if diff := cmp.Diff(miss.Body.Bytes(), hit.Body.Bytes()); diff != "" {
				t.Errorf("hit body differs (-miss +hit):\n%s", diff)
			}
			if got := len(h.f.fetched()); got != 1 {
				t.Errorf("source fetched %d times, want 1", got)
			}
			if h.a.acquired.Load() > 1 || h.pr.calls.Load() > 1 {
				t.Errorf("hit was admitted (%d) or probed (%d)", h.a.acquired.Load(), h.pr.calls.Load())
			}

			// 304 from the entry: same validators, no body.
			cond := hdr.Clone()
			cond.Set("If-None-Match", miss.Header().Get("ETag"))
			nm := h.do(t, http.MethodGet, okPath, cond)
			if nm.Code != http.StatusNotModified || nm.Body.Len() != 0 || nm.Header().Get("ETag") != miss.Header().Get("ETag") {
				t.Errorf("conditional hit: %d etag %q body %d bytes", nm.Code, nm.Header().Get("ETag"), nm.Body.Len())
			}
			if lm := miss.Header().Get("Last-Modified"); lm != "" {
				ims := hdr.Clone()
				ims.Set("If-Modified-Since", lm)
				if rec := h.do(t, http.MethodGet, okPath, ims); rec.Code != http.StatusNotModified {
					t.Errorf("If-Modified-Since hit: %d", rec.Code)
				}
			}
			// HEAD from the entry: GET's headers, no body.
			head := h.do(t, http.MethodHead, okPath, hdr)
			if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get(hdrXiCache) != cacheHit ||
				head.Header().Get("Content-Length") != miss.Header().Get("Content-Length") {
				t.Errorf("HEAD hit: %d %q len %q", head.Code, head.Header().Get(hdrXiCache), head.Header().Get("Content-Length"))
			}
			if got := len(h.f.fetched()); got != 1 {
				t.Errorf("source fetched %d times after 304 and HEAD hits, want 1", got)
			}

			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			h.checkReleased(t)
			if got := h.p.out.released.Load(); int(got) != h.p.calls {
				t.Errorf("output blob released %d times for %d outputs", got, h.p.calls)
			}
		})
	}
}

// TestResultCacheNotStored lists responses that are never cached: the
// second request is produced again (a second fetch).
func TestResultCacheNotStored(t *testing.T) {
	t.Parallel()
	notFound := errs.New(errs.KindNotFound, "source: not found")
	tests := []struct {
		name      string
		mutate    func(*harness)
		method    string // first request; the second is a GET
		wantCache string // X-Xi-Cache of the second request
		wantCode  int    // status of the second request
	}{
		{name: "bypass_cache", wantCache: cacheBypass, mutate: func(h *harness) {
			h.cfg.ResultCache.AllowBypass = true
			h.router.mutate = func(r *transform.Request) { r.Delivery.BypassCache = true }
		}},
		{name: "source no-store", wantCache: cacheMiss, mutate: func(h *harness) {
			h.cfg.Cache.Passthrough = true
			h.f.obj.CacheControl = "no-store"
		}},
		{name: "source private", wantCache: cacheMiss, mutate: func(h *harness) {
			h.cfg.Cache.Passthrough = true
			h.f.obj.CacheControl = "private, max-age=60"
		}},
		{name: "head", method: http.MethodHead, wantCache: cacheMiss},
		{name: "error", wantCache: cacheMiss, wantCode: http.StatusNotFound, mutate: func(h *harness) {
			h.f.err = notFound
		}},
		{name: "fallback", wantCache: cacheMiss, mutate: func(h *harness) {
			h.f.err = notFound
			h.f.other = &fakeReply{data: jpegSource}
			h.cfg.Fallback.Source = fallbackRef
		}},
		{name: "raw", wantCache: "", mutate: func(h *harness) { h.router.mutate = rawRequest }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h, store := newCachedHarness(t, tc.mutate)
			first := h.do(t, cmpOr(tc.method, http.MethodGet), okPath, nil)
			if first.Code >= 500 {
				t.Fatalf("first: %d", first.Code)
			}
			store.Wait()
			second := h.do(t, http.MethodGet, okPath, nil)
			if want := cmpOr(tc.wantCode, http.StatusOK); second.Code != want {
				t.Errorf("second: status %d, want %d", second.Code, want)
			}
			if got := second.Header().Get(hdrXiCache); got != tc.wantCache {
				t.Errorf("second: X-Xi-Cache %q, want %q", got, tc.wantCache)
			}
			if got := len(h.f.fetched()); got < 2 {
				t.Errorf("source fetched %d times, want a second fetch", got)
			}
			_ = store.Close()
			h.checkReleased(t)
		})
	}
}

// TestResultCacheBypassNeedsConfig: bypass_cache is ignored without
// XI_CACHE_ALLOW_BYPASS.
func TestResultCacheBypassNeedsConfig(t *testing.T) {
	t.Parallel()
	h, store := newCachedHarness(t, func(h *harness) {
		h.router.mutate = func(r *transform.Request) { r.Delivery.BypassCache = true }
	})
	h.do(t, http.MethodGet, okPath, nil)
	store.Wait()
	if got := h.do(t, http.MethodGet, okPath, nil).Header().Get(hdrXiCache); got != cacheHit {
		t.Errorf("X-Xi-Cache = %q, want HIT", got)
	}
}

// TestResultCacheKeyedByAccept: a negotiated format is keyed by the Accept
// weights (equivalent headers share an entry); a URL-fixed format ignores
// Accept.
func TestResultCacheKeyedByAccept(t *testing.T) {
	t.Parallel()
	h, store := newCachedHarness(t, nil)
	get := func(accept string) string {
		rec := h.do(t, http.MethodGet, okPath, http.Header{"Accept": {accept}})
		store.Wait()
		return rec.Header().Get(hdrXiCache)
	}
	for i, tc := range []struct{ accept, want string }{
		{"image/webp,*/*", cacheMiss},
		{"*/*, image/webp;q=1", cacheHit},
		{"image/avif,image/webp,*/*", cacheMiss},
		{"*/*", cacheMiss},
		{"", cacheHit}, // no Accept = accept everything
	} {
		if got := get(tc.accept); got != tc.want {
			t.Errorf("request %d (Accept %q): %q, want %q", i, tc.accept, got, tc.want)
		}
	}

	fixed, fstore := newCachedHarness(t, func(h *harness) {
		h.router.mutate = func(r *transform.Request) { r.Output.Format = transform.FormatJPEG }
	})
	fixed.do(t, http.MethodGet, okPath, http.Header{"Accept": {"image/avif"}})
	fstore.Wait()
	if got := fixed.do(t, http.MethodGet, okPath, http.Header{"Accept": {"text/html"}}).Header().Get(hdrXiCache); got != cacheHit {
		t.Errorf("fixed format with another Accept: %q, want HIT", got)
	}
}

// TestResultCacheTTL: entries follow XI_CACHE_RESULT_TTL shortened by the
// request's ttl, on the synctest clock.
func TestResultCacheTTL(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, store := newCachedHarness(t, func(h *harness) {
			h.router.mutate = func(r *transform.Request) { r.Delivery.TTL = time.Minute }
		})
		h.do(t, http.MethodGet, okPath, nil)
		store.Wait()
		synctest.Sleep(time.Minute - time.Second)
		if got := h.do(t, http.MethodGet, okPath, nil).Header().Get(hdrXiCache); got != cacheHit {
			t.Fatalf("within ttl: %q", got)
		}
		synctest.Sleep(time.Second)
		if got := h.do(t, http.MethodGet, okPath, nil).Header().Get(hdrXiCache); got != cacheMiss {
			t.Errorf("after the request ttl: %q, want MISS", got)
		}
		store.Wait()
		_ = store.Close()
	})
}

// failingStore fails every operation.
type failingStore struct{}

var errStoreDown = errors.New("store down")

func (failingStore) Get(context.Context, cache.Namespace, string) (cache.Entry, bool, error) {
	return cache.Entry{}, false, errStoreDown
}

func (failingStore) Set(context.Context, cache.Namespace, string, *cache.Entry, time.Duration) error {
	return errStoreDown
}
func (failingStore) Delete(context.Context, cache.Namespace, string) error { return errStoreDown }
func (failingStore) Close() error                                          { return nil }

// TestResultCacheErrorsDegrade: a failing store never fails a request.
func TestResultCacheErrorsDegrade(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(h *harness) {
		h.cache = failingStore{}
		h.cfg.DebugHeaders = true
		h.cfg.ResultCache.TTL = time.Hour
	})
	for range 2 {
		rec := h.do(t, http.MethodGet, okPath, nil)
		if rec.Code != http.StatusOK || rec.Header().Get(hdrXiCache) != cacheMiss {
			t.Fatalf("status %d, X-Xi-Cache %q", rec.Code, rec.Header().Get(hdrXiCache))
		}
	}
	h.checkReleased(t)
}

// TestResultCacheOffNoHeader: without a store there is no X-Xi-Cache.
func TestResultCacheOffNoHeader(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(h *harness) { h.cfg.DebugHeaders = true })
	if got := h.do(t, http.MethodGet, okPath, nil).Header().Get(hdrXiCache); got != "" {
		t.Errorf("X-Xi-Cache = %q with the cache off", got)
	}
}

func TestStorable(t *testing.T) {
	t.Parallel()
	for cc, want := range map[string]bool{
		"":                                  true,
		"public, max-age=60":                true,
		"no-cache":                          true,
		"no-store":                          false,
		"public, No-Store":                  false,
		"private":                           false,
		`private="Set-Cookie", max-age=600`: false,
	} {
		if got := storable(cc); got != want {
			t.Errorf("storable(%q) = %v, want %v", cc, got, want)
		}
	}
}

func cmpOr[T comparable](v, def T) T {
	var zero T
	if v == zero {
		return def
	}
	return v
}

// BenchmarkResultCache compares a full miss (fake fetch, probe and
// processing) with a memory hit through the whole handler.
func BenchmarkResultCache(b *testing.B) {
	b.Run("miss", func(b *testing.B) {
		h := newHarness(b, nil)
		b.ReportAllocs()
		for b.Loop() {
			if rec := h.do(b, http.MethodGet, okPath, nil); rec.Code != http.StatusOK {
				b.Fatal(rec.Code)
			}
		}
	})
	b.Run("hit", func(b *testing.B) {
		h, store := newCachedHarness(b, func(h *harness) { h.cfg.DebugHeaders = false })
		h.do(b, http.MethodGet, okPath, nil)
		store.Wait()
		b.ReportAllocs()
		for b.Loop() {
			if rec := h.do(b, http.MethodGet, okPath, nil); rec.Code != http.StatusOK {
				b.Fatal(rec.Code)
			}
		}
	})
}
