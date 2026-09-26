package server

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/router"
	"github.com/xantiagoma/xtandard-image/internal/dialect/wsrv"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/obs/admin"
)

// newWsrvHarness is newHarness with the real router and wsrv dialect at the
// root "?url=" rule, so tests exercise wsrv URLs end to end.
func newWsrvHarness(t *testing.T, mutate func(*harness)) *harness {
	t.Helper()
	h := newHarness(t, mutate)
	p, err := wsrv.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := router.New(&router.Options{
		Policy:  router.PolicyAllow,
		RootURL: &router.Mount{Name: "wsrv/?url", Dialect: wsrv.Name},
	}, map[string]dialect.Dialect{wsrv.Name: p})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(&h.cfg, &Deps{
		Router: rt, Resolver: h.res, Fetcher: h.f, Admitter: h.a, Prober: h.pr, Processor: h.p, Sniffer: h.sniffer,
		Allocator: func(n int) (fetch.Buffer, error) { return fetch.NewGoBuffer(n, nil), nil },
		Metrics:   h.m,
		Logger:    h.logger,
		Readiness: admin.NewReadiness(),
	})
	if err != nil {
		t.Fatal(err)
	}
	h.srv = srv
	return h
}

// TestWsrvDefaultRedirect covers wsrv's default= (and the deprecated
// errorredirect=): a 302 when the source fails.
func TestWsrvDefaultRedirect(t *testing.T) {
	t.Parallel()
	notFound := errs.New(errs.KindNotFound, "origin 404")
	tests := []struct {
		name     string
		target   string
		method   string
		fetchErr error
		deny     map[string]bool
		fallback bool // XI_FALLBACK_ALLOW_REQUEST_URL
		status   int
		location string
	}{
		{
			name: "default url on 404", target: "/?url=origin/a.jpg&w=64&default=origin/placeholder.png",
			fetchErr: notFound, status: http.StatusFound, location: "https://origin/placeholder.png",
		},
		{
			name: "errorredirect on 404", target: "/?url=origin/a.jpg&errorredirect=https://origin/404.png",
			fetchErr: notFound, status: http.StatusFound, location: "https://origin/404.png",
		},
		{
			name: "default=1 redirects to the source", target: "/?url=origin/a.jpg&default=1",
			fetchErr: notFound, status: http.StatusFound, location: "https://origin/a.jpg",
		},
		{
			name: "head", target: "/?url=origin/a.jpg&default=1", method: http.MethodHead,
			fetchErr: notFound, status: http.StatusFound, location: "https://origin/a.jpg",
		},
		{
			name: "upstream error", target: "/?url=origin/a.jpg&default=1",
			fetchErr: errs.New(errs.KindUpstreamError, "origin 500"), status: http.StatusFound, location: "https://origin/a.jpg",
		},
		{
			name: "target not allowlisted", target: "/?url=origin/a.jpg&default=evil.example/x.png",
			fetchErr: notFound, deny: map[string]bool{"https://evil.example/x.png": true}, status: http.StatusNotFound,
		},
		{
			name: "success is not redirected", target: "/?url=origin/a.jpg&default=origin/placeholder.png",
			status: http.StatusOK,
		},
		{
			name: "no default", target: "/?url=origin/a.jpg", fetchErr: notFound, status: http.StatusNotFound,
		},
		{
			// Overload is never masked (fallbackEligible).
			name: "overload is not redirected", target: "/?url=origin/a.jpg&default=1",
			fetchErr: errs.New(errs.KindOverloaded, "busy"), status: http.StatusTooManyRequests,
		},
		{
			// A denied redirect target falls through to the fallback image,
			// which is refused by the same allowlist.
			name: "denied target, fallback allowed but denied", target: "/?url=origin/a.jpg&default=evil.example/x.png",
			fetchErr: notFound, deny: map[string]bool{"https://evil.example/x.png": true}, fallback: true, status: http.StatusNotFound,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newWsrvHarness(t, func(h *harness) {
				h.f.err = tc.fetchErr
				h.res.deny = tc.deny
				h.cfg.Fallback.AllowRequestURL = tc.fallback
			})
			method := tc.method
			if method == "" {
				method = http.MethodGet
			}
			rec := h.do(t, method, tc.target, nil)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d; body %q", rec.Code, tc.status, rec.Body.String())
			}
			if got := rec.Header().Get("Location"); got != tc.location {
				t.Errorf("Location = %q, want %q", got, tc.location)
			}
			if tc.status == http.StatusFound {
				if got := rec.Header().Get("Cache-Control"); got != "public, max-age=10" {
					t.Errorf("Cache-Control = %q", got)
				}
				if method == http.MethodHead && rec.Body.Len() != 0 {
					t.Errorf("HEAD body %q", rec.Body.String())
				}
			}
		})
	}
}

// TestWsrvMaxAge checks that maxage sets the Cache-Control max-age,
// capped by XI_CACHE_HEADERS_TTL.
func TestWsrvMaxAge(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		query string
		ttl   time.Duration
		want  string
	}{
		{"one day", "&maxage=1d", 365 * 24 * time.Hour, "public, max-age=86400, stale-while-revalidate=8640"},
		{"31 days", "&maxage=31d", 365 * 24 * time.Hour, "public, max-age=2678400, stale-while-revalidate=267840"},
		{"capped by config", "&maxage=1y", 48 * time.Hour, "public, max-age=172800, stale-while-revalidate=17280"},
		{"no maxage", "", 48 * time.Hour, "public, max-age=172800, stale-while-revalidate=17280"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newWsrvHarness(t, func(h *harness) { h.cfg.Cache.TTL = tc.ttl })
			rec := h.do(t, http.MethodGet, "/?url=origin/a.jpg&w=64"+tc.query, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Cache-Control"); got != tc.want {
				t.Errorf("Cache-Control = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestWsrvBase64 checks encoding=base64: a data URL as text/plain.
func TestWsrvBase64(t *testing.T) {
	t.Parallel()
	h := newWsrvHarness(t, nil)
	rec := h.do(t, http.MethodGet, "/?url=origin/a.jpg&w=64&output=jpg&encoding=base64", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
	}
	want := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpegOut)
	if got := rec.Body.String(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	hdr := rec.Header()
	if got := hdr.Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Errorf("Content-Type = %q", got)
	}
	if got := hdr.Get("Content-Length"); got != strconv.Itoa(len(want)) {
		t.Errorf("Content-Length = %q, want %d", got, len(want))
	}
	if got := hdr.Get("Content-Disposition"); got != "" {
		t.Errorf("Content-Disposition = %q", got)
	}
	binary := h.do(t, http.MethodGet, "/?url=origin/a.jpg&w=64&output=jpg", nil)
	if binary.Header().Get("ETag") == hdr.Get("ETag") {
		t.Error("data URL and binary responses share an ETag")
	}
}
