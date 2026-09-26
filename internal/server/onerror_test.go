package server

import (
	"net/http"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

func onErrorRedirect(r *transform.Request) { r.Delivery.OnErrorRedirect = true }

// TestOnErrorRedirect: a request with Delivery.OnErrorRedirect (Cloudflare
// onerror=redirect) whose source fails is answered with a 302 to the source
// URL, ahead of the fallback image; other failures and non-http sources
// take the normal path. wsrv default= is covered by TestWsrvDefaultRedirect.
func TestOnErrorRedirect(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		method   string
		mutate   func(*harness)
		want     int
		location string
	}{
		{"source 404", http.MethodGet, func(h *harness) {
			h.router.mutate = onErrorRedirect
			h.f.err = errs.New(errs.KindNotFound, "origin 404")
		}, http.StatusFound, "http://origin/a.jpg"},
		{"head", http.MethodHead, func(h *harness) {
			h.router.mutate = onErrorRedirect
			h.f.err = errs.New(errs.KindNotFound, "origin 404")
		}, http.StatusFound, "http://origin/a.jpg"},
		{"upstream error", http.MethodGet, func(h *harness) {
			h.router.mutate = onErrorRedirect
			h.f.err = errs.New(errs.KindUpstreamError, "origin 500")
		}, http.StatusFound, "http://origin/a.jpg"},
		{"corrupt source", http.MethodGet, func(h *harness) {
			h.router.mutate = onErrorRedirect
			h.pr.err, h.pr.errFor = errs.New(errs.KindCorrupt, "bad image"), jpegSource
		}, http.StatusFound, "http://origin/a.jpg"},
		{"beats the fallback image", http.MethodGet, func(h *harness) {
			h.router.mutate = onErrorRedirect
			h.f.err = errs.New(errs.KindNotFound, "origin 404")
			h.f.other = &fakeReply{data: fallbackSource}
			h.cfg.Fallback = Fallback{Source: fallbackRef}
		}, http.StatusFound, "http://origin/a.jpg"},
		{"flag off", http.MethodGet, func(h *harness) {
			h.f.err = errs.New(errs.KindNotFound, "origin 404")
		}, http.StatusNotFound, ""},
		{"not eligible", http.MethodGet, func(h *harness) {
			h.router.mutate = func(r *transform.Request) { onErrorRedirect(r); r.Output.Quality = 101 }
			h.f.err = errs.New(errs.KindNotFound, "origin 404")
		}, http.StatusBadRequest, ""},
		{"source refused", http.MethodGet, func(h *harness) {
			h.router.mutate = onErrorRedirect
			h.res.deny = map[string]bool{"http://origin/a.jpg": true}
		}, http.StatusForbidden, ""},
		{"non-http source", http.MethodGet, func(h *harness) {
			h.router.mutate = func(r *transform.Request) { onErrorRedirect(r); r.Source.Raw = "uploads/a.jpg" }
			h.f.err = errs.New(errs.KindNotFound, "object missing")
		}, http.StatusNotFound, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, tc.mutate)
			rec := h.do(t, tc.method, okPath, nil)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tc.want, rec.Body.String())
			}
			if got := rec.Header().Get("Location"); got != tc.location {
				t.Errorf("Location = %q, want %q", got, tc.location)
			}
			if tc.location != "" {
				// The redirect is cached like the error it replaces
				// (XI_CACHE_HEADERS_ERROR_TTL, 10s in the harness).
				if got := rec.Header().Get("Cache-Control"); got != "public, max-age=10" {
					t.Errorf("Cache-Control = %q, want %q", got, "public, max-age=10")
				}
				if h.fetchedFallback() {
					t.Error("fallback fetched although the request redirected")
				}
			}
			h.checkReleased(t)
		})
	}
}
