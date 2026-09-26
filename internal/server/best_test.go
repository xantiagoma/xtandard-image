package server

import (
	"net/http"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

const bestSummary = "photo c=7.40 colors=7470; avif q60=5942, webp q80=5100*, jpeg q80=12182"

func bestHarness(tb testing.TB, cached bool) *harness {
	tb.Helper()
	mutate := func(h *harness) {
		h.cfg.DebugHeaders = true
		h.router.mutate = func(r *transform.Request) { r.Output.Format = transform.FormatBest }
		h.p.outFormat, h.p.outType, h.p.outBest = transform.FormatWebP, "image/webp", bestSummary
	}
	if cached {
		h, _ := newCachedHarness(tb, mutate)
		return h
	}
	return newHarness(tb, mutate)
}

// TestBestFormat: a format=best response carries the encoder's choice
// (Content-Type, X-Xi-Best), varies on Accept, hands the processor the
// candidate lists, and gets an ETag that follows the candidate set rather
// than only the first candidate.
func TestBestFormat(t *testing.T) {
	t.Parallel()
	h := bestHarness(t, false)
	chrome := http.Header{"Accept": {"image/avif,image/webp,image/*,*/*;q=0.8"}}
	rec := h.do(t, http.MethodGet, okPath, chrome)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	for name, want := range map[string]string{
		"Content-Type": "image/webp",
		"Vary":         "Accept",
		"X-Xi-Best":    bestSummary,
	} {
		if got := rec.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	h.p.mu.Lock()
	d := h.p.decision
	h.p.mu.Unlock()
	if d.Reason != negotiate.ReasonBest || len(d.BestCandidates(negotiate.ContentPhoto, 0)) == 0 {
		t.Errorf("processor got decision %+v, want best candidates", d)
	}

	again := h.do(t, http.MethodGet, okPath, chrome).Header().Get("ETag")
	// AVIF first as for chrome, but no WebP: another candidate set.
	avifOnly := h.do(t, http.MethodGet, okPath, http.Header{"Accept": {"image/avif,*/*"}}).Header().Get("ETag")
	if etag := rec.Header().Get("ETag"); etag != again || etag == avifOnly {
		t.Errorf("ETags chrome %q, again %q, avif-only %q: want equal, equal, different", etag, again, avifOnly)
	}
	h.checkReleased(t)
}

// TestBestFormatCacheHit: a result-cache hit replays the chosen format and
// the X-Xi-Best summary.
func TestBestFormatCacheHit(t *testing.T) {
	t.Parallel()
	h := bestHarness(t, true)
	accept := http.Header{"Accept": {"image/webp,*/*"}}
	first := h.do(t, http.MethodGet, okPath, accept)
	if first.Code != http.StatusOK {
		t.Fatalf("status %d", first.Code)
	}
	store := h.cache
	if w, ok := store.(interface{ Wait() }); ok {
		w.Wait()
	}
	hit := h.do(t, http.MethodGet, okPath, accept)
	if got := hit.Header().Get(hdrXiCache); got != cacheHit {
		t.Fatalf("X-Xi-Cache = %q, want a hit", got)
	}
	for _, name := range []string{"Content-Type", "Vary", "X-Xi-Best", "ETag"} {
		if got, want := hit.Header().Get(name), first.Header().Get(name); got != want {
			t.Errorf("hit %s = %q, want %q", name, got, want)
		}
	}
	if h.p.calls != 1 {
		t.Errorf("processor ran %d times", h.p.calls)
	}
}
