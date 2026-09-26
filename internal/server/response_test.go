package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/source"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// testNow is the fixed clock of the response tests.
var testNow = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

const defaultCC = "public, max-age=31536000, stale-while-revalidate=3153600"

func withRequest(f func(*transform.Request)) func(*harness) {
	return func(h *harness) { h.router.mutate = f }
}

func explicitJPEG(r *transform.Request) { r.Output.Format = transform.FormatJPEG }

// TestResponseHeaders covers every row of ARCHITECTURE §9.1. absent lists
// headers that must not be set.
func TestResponseHeaders(t *testing.T) {
	t.Parallel()
	lastMod := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	yes := true

	tests := []struct {
		name   string
		mutate func(*harness)
		header http.Header
		want   map[string]string
		absent []string
	}{
		{
			name: "defaults",
			want: map[string]string{
				"Cache-Control":          defaultCC,
				"Content-Type":           "image/jpeg",
				"Content-Length":         "12",
				"X-Content-Type-Options": "nosniff",
				"Content-Disposition":    `inline; filename="a.jpg"`,
				"Vary":                   "Accept",
			},
			absent: []string{"Last-Modified", "Content-DPR", "Content-Security-Policy", "Link", "Cache-Tag", "X-Xi-Origin-Content-Length"},
		},
		{
			name:   "cache-control/immutable option",
			mutate: withRequest(func(r *transform.Request) { r.Delivery.Immutable = true }),
			want:   map[string]string{"Cache-Control": defaultCC + ", immutable"},
		},
		{
			name: "cache-control/signed with cache buster is immutable",
			mutate: func(h *harness) {
				h.router.sig = sign.Verified
				h.router.mutate = func(r *transform.Request) { r.Delivery.CacheBuster = "v7" }
			},
			want: map[string]string{"Cache-Control": defaultCC + ", immutable"},
		},
		{
			name: "cache-control/signed with hashsum is immutable",
			mutate: func(h *harness) {
				h.router.sig = sign.Trusted
				h.router.mutate = func(r *transform.Request) {
					r.Source.Hashsum = &transform.Hashsum{Algorithm: transform.HashSHA256, Digest: strings.Repeat("0", 64)}
				}
			},
			want: map[string]string{"Cache-Control": defaultCC + ", immutable"},
		},
		{
			name:   "cache-control/unsigned cache buster is not immutable",
			mutate: withRequest(func(r *transform.Request) { r.Delivery.CacheBuster = "v7" }),
			want:   map[string]string{"Cache-Control": defaultCC},
		},
		{
			name:   "cache-control/configured ttl",
			mutate: func(h *harness) { h.cfg.Cache.TTL = time.Hour },
			want:   map[string]string{"Cache-Control": "public, max-age=3600, stale-while-revalidate=360"},
		},
		{
			name:   "cache-control/zero ttl",
			mutate: func(h *harness) { h.cfg.Cache.TTL = 0 },
			want:   map[string]string{"Cache-Control": "no-cache"},
		},
		{
			name:   "cache-control/request ttl clamped by config",
			mutate: withRequest(func(r *transform.Request) { r.Delivery.TTL = 60 * time.Second }),
			want:   map[string]string{"Cache-Control": "public, max-age=60, stale-while-revalidate=6"},
		},
		{
			name: "cache-control/request ttl above config",
			mutate: func(h *harness) {
				h.cfg.Cache.TTL = time.Minute
				h.router.mutate = func(r *transform.Request) { r.Delivery.TTL = time.Hour }
			},
			want: map[string]string{"Cache-Control": "public, max-age=60, stale-while-revalidate=6"},
		},
		{
			name: "cache-control/expires shortens and drops swr and immutable",
			mutate: withRequest(func(r *transform.Request) {
				r.Delivery.Expires = testNow.Add(90 * time.Second)
				r.Delivery.Immutable = true
			}),
			want: map[string]string{"Cache-Control": "public, max-age=90"},
		},
		{
			name: "cache-control/passthrough source cache-control",
			mutate: func(h *harness) {
				h.cfg.Cache.Passthrough = true
				h.f.obj.CacheControl = "public, max-age=300"
				h.f.obj.Expires = testNow.Add(time.Hour)
			},
			want: map[string]string{"Cache-Control": "public, max-age=300"},
		},
		{
			name: "cache-control/passthrough source expires",
			mutate: func(h *harness) {
				h.cfg.Cache.Passthrough = true
				h.f.obj.Expires = testNow.Add(2 * time.Minute)
			},
			want: map[string]string{"Cache-Control": "public, max-age=120"},
		},
		{
			name:   "cache-control/passthrough without source headers",
			mutate: func(h *harness) { h.cfg.Cache.Passthrough = true },
			want:   map[string]string{"Cache-Control": defaultCC},
		},
		{
			name: "cache-control/request ttl beats passthrough",
			mutate: func(h *harness) {
				h.cfg.Cache.Passthrough = true
				h.f.obj.CacheControl = "public, max-age=300"
				h.router.mutate = func(r *transform.Request) { r.Delivery.TTL = 30 * time.Second }
			},
			want: map[string]string{"Cache-Control": "public, max-age=30, stale-while-revalidate=3"},
		},
		{
			name:   "last-modified/from source",
			mutate: func(h *harness) { h.f.obj.LastModified = lastMod },
			want:   map[string]string{"Last-Modified": "Fri, 02 Jan 2026 03:04:05 GMT"},
		},
		{
			name:   "vary/none when format is explicit",
			mutate: withRequest(explicitJPEG),
			absent: []string{"Vary"},
		},
		{
			name:   "vary/client hints and content-dpr",
			mutate: func(h *harness) { h.cfg.Negotiate.EnableClientHints = true },
			header: http.Header{"Sec-Ch-Dpr": {"2"}},
			want: map[string]string{
				// Width is fixed by the URL, so only DPR and Save-Data were consulted.
				"Vary":        "Accept, Sec-CH-DPR, DPR, Save-Data, ECT", //nolint:misspell // ECT is the connection-type hint header
				"Content-DPR": "2",
			},
		},
		{
			name: "vary/url dpr is not a hint",
			mutate: withRequest(func(r *transform.Request) {
				explicitJPEG(r)
				r.Pipelines[0].Resize.DPR = 2
			}),
			absent: []string{"Vary", "Content-DPR"},
		},
		{
			name:   "content-disposition/filename option",
			mutate: withRequest(func(r *transform.Request) { r.Delivery.Filename = "holiday" }),
			want:   map[string]string{"Content-Disposition": `inline; filename="holiday.jpg"`},
		},
		{
			name:   "content-disposition/attachment",
			mutate: withRequest(func(r *transform.Request) { r.Delivery.Attachment = &yes }),
			want:   map[string]string{"Content-Disposition": `attachment; filename="a.jpg"`},
		},
		{
			name: "content-disposition/new extension",
			mutate: func(h *harness) {
				h.router.mutate = func(r *transform.Request) { r.Output.Format = transform.FormatWebP }
				h.p.outFormat, h.p.outType = transform.FormatWebP, "image/webp"
			},
			want: map[string]string{"Content-Disposition": `inline; filename="a.webp"`, "Content-Type": "image/webp"},
		},
		{
			name: "svg output/csp",
			mutate: func(h *harness) {
				h.router.mutate = func(r *transform.Request) { r.Output.Format = transform.FormatSVG }
				h.p.outFormat, h.p.outType = transform.FormatSVG, "image/svg+xml"
			},
			want: map[string]string{
				"Content-Type":            "image/svg+xml",
				"Content-Security-Policy": svgCSP,
				"X-Content-Type-Options":  "nosniff",
				"Content-Disposition":     `inline; filename="a.svg"`,
			},
		},
		{
			name:   "link/canonical",
			mutate: func(h *harness) { h.cfg.CanonicalHeader = true },
			want:   map[string]string{"Link": `<http://origin/a.jpg>; rel="canonical"`},
		},
		{
			name: "cache tags",
			mutate: withRequest(func(r *transform.Request) {
				r.Delivery.CacheTags = []string{"product-1", "bad,tag\r\n", "\t"}
			}),
			want: map[string]string{"Cache-Tag": "product-1,badtag"},
		},
		{
			name: "debug headers",
			mutate: func(h *harness) {
				h.cfg.DebugHeaders = true
				h.router.mutate = func(r *transform.Request) {
					r.Ignored = []transform.Ignored{{Option: "gif_options", Reason: "x"}, {Option: "a,b\r\n", Reason: "y"}}
				}
			},
			want: map[string]string{
				"X-Xi-Origin-Content-Length": "9",
				"X-Xi-Origin-Width":          "640",
				"X-Xi-Origin-Height":         "480",
				"X-Xi-Result-Width":          "64",
				"X-Xi-Result-Height":         "48",
				"X-Xi-Ignored":               "gif_options, ab",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, tc.mutate)
			h.srv.now = func() time.Time { return testNow }
			rec := h.do(t, http.MethodGet, okPath, tc.header)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
			}
			if etag := rec.Header().Get("ETag"); !strings.HasPrefix(etag, `"xi1-`) || len(etag) != len(`"xi1-`)+etagLen+1 {
				t.Errorf("ETag = %q", etag)
			}
			for k, v := range tc.want {
				if got := rec.Header().Get(k); got != v {
					t.Errorf("%s = %q, want %q", k, got, v)
				}
			}
			for _, k := range tc.absent {
				if v, ok := rec.Header()[http.CanonicalHeaderKey(k)]; ok {
					t.Errorf("%s = %q, want absent", k, v)
				}
			}
			h.checkReleased(t)
		})
	}
}

// TestNotModified proves a matching conditional request is answered 304
// without running the processor, and that every permit and buffer is
// released. With an explicit format the 304 happens before admission and
// probe.
func TestNotModified(t *testing.T) {
	t.Parallel()
	lastMod := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	// The ETag a plain GET returns for each setup.
	etagOf := func(t *testing.T, mutate func(*harness)) string {
		t.Helper()
		return newHarness(t, mutate).do(t, http.MethodGet, okPath, nil).Header().Get("ETag")
	}
	fixed := withRequest(explicitJPEG)
	withLM := func(h *harness) { h.f.obj.LastModified = lastMod }

	tests := []struct {
		name      string
		mutate    func(*harness)
		header    func(etag string) http.Header
		etagSetup func(*harness)
		want      int
		admitted  bool // admission and probe ran
	}{
		{
			name: "if-none-match/explicit format skips admission", mutate: fixed, etagSetup: fixed,
			header: func(e string) http.Header { return http.Header{"If-None-Match": {e}} },
			want:   http.StatusNotModified,
		},
		{
			name:   "if-none-match/negotiated after probe",
			header: func(e string) http.Header { return http.Header{"If-None-Match": {e}} },
			want:   http.StatusNotModified, admitted: true,
		},
		{
			name: "if-none-match/weak and listed", mutate: fixed, etagSetup: fixed,
			header: func(e string) http.Header { return http.Header{"If-None-Match": {`"other", W/` + e}} },
			want:   http.StatusNotModified,
		},
		{
			name: "if-none-match/star", mutate: fixed,
			header: func(string) http.Header { return http.Header{"If-None-Match": {"*"}} },
			want:   http.StatusNotModified,
		},
		{
			name: "if-none-match/mismatch", mutate: fixed,
			header: func(string) http.Header { return http.Header{"If-None-Match": {`"xi1-nope"`}} },
			want:   http.StatusOK, admitted: true,
		},
		{
			name: "if-modified-since/not modified", mutate: func(h *harness) { fixed(h); withLM(h) },
			header: func(string) http.Header {
				return http.Header{"If-Modified-Since": {lastMod.Format(http.TimeFormat)}}
			},
			want: http.StatusNotModified,
		},
		{
			name: "if-modified-since/modified", mutate: func(h *harness) { fixed(h); withLM(h) },
			header: func(string) http.Header {
				return http.Header{"If-Modified-Since": {lastMod.Add(-time.Hour).Format(http.TimeFormat)}}
			},
			want: http.StatusOK, admitted: true,
		},
		{
			name: "if-modified-since/unknown last-modified", mutate: fixed,
			header: func(string) http.Header {
				return http.Header{"If-Modified-Since": {testNow.Format(http.TimeFormat)}}
			},
			want: http.StatusOK, admitted: true,
		},
		{
			name: "if-none-match wins over if-modified-since", mutate: func(h *harness) { fixed(h); withLM(h) },
			header: func(string) http.Header {
				return http.Header{"If-None-Match": {`"xi1-nope"`}, "If-Modified-Since": {lastMod.Format(http.TimeFormat)}}
			},
			want: http.StatusOK, admitted: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := etagOf(t, tc.etagSetup)
			h := newHarness(t, tc.mutate)
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				rec := h.do(t, method, okPath, tc.header(e))
				if rec.Code != tc.want {
					t.Fatalf("%s: status = %d, want %d", method, rec.Code, tc.want)
				}
				if tc.want != http.StatusNotModified {
					continue
				}
				if rec.Body.Len() != 0 {
					t.Errorf("%s: 304 has a %d-byte body", method, rec.Body.Len())
				}
				for _, k := range []string{"ETag", "Cache-Control"} {
					if rec.Header().Get(k) == "" {
						t.Errorf("%s: 304 lacks %s", method, k)
					}
				}
				for _, k := range []string{"Content-Type", "Content-Length", "Content-Disposition"} {
					if v := rec.Header().Get(k); v != "" {
						t.Errorf("%s: 304 has %s = %q", method, k, v)
					}
				}
			}
			if tc.want == http.StatusNotModified && h.p.calls != 0 {
				t.Errorf("processor ran %d times on a 304 path", h.p.calls)
			}
			if got := h.a.acquired.Load() > 0; got != tc.admitted {
				t.Errorf("admission acquired = %v, want %v", got, tc.admitted)
			}
			if got := h.pr.calls.Load() > 0; got != tc.admitted {
				t.Errorf("probe ran = %v, want %v", got, tc.admitted)
			}
			h.checkReleased(t)
		})
	}
}

// Test304KeepsVaryAndLastModified checks a 304 carries the headers a cache
// needs to update its stored response (RFC 9110 §15.4.5).
func Test304KeepsVaryAndLastModified(t *testing.T) {
	t.Parallel()
	lastMod := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	h := newHarness(t, func(h *harness) { h.f.obj.LastModified = lastMod })
	e := h.do(t, http.MethodGet, okPath, nil).Header().Get("ETag")
	rec := h.do(t, http.MethodGet, okPath, http.Header{"If-None-Match": {e}})
	if rec.Code != http.StatusNotModified {
		t.Fatalf("status = %d", rec.Code)
	}
	want := map[string]string{"ETag": e, "Vary": "Accept", "Last-Modified": "Fri, 02 Jan 2026 03:04:05 GMT", "Cache-Control": defaultCC}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestHead(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)
	get := h.do(t, http.MethodGet, okPath, nil)
	h.p.calls = 0

	rec := h.do(t, http.MethodHead, okPath, nil)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("HEAD: status %d, %d body bytes", rec.Code, rec.Body.Len())
	}
	if h.p.calls != 0 {
		t.Errorf("HEAD ran the processor %d times", h.p.calls)
	}
	if v := rec.Header().Get("Content-Length"); v != "" {
		t.Errorf("HEAD Content-Length = %q, want omitted (unknown before encoding)", v)
	}
	for _, k := range []string{"ETag", "Cache-Control", "Content-Type", "Content-Disposition", "Vary", "X-Content-Type-Options"} {
		if got, want := rec.Header().Get(k), get.Header().Get(k); got != want {
			t.Errorf("HEAD %s = %q, GET has %q", k, got, want)
		}
	}
	h.checkReleased(t)
}

func TestExpires(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		expires time.Time
		want    int
		wantCC  string
	}{
		{name: "past", expires: testNow.Add(-time.Second), want: http.StatusNotFound, wantCC: "no-store"},
		{name: "exactly now", expires: testNow, want: http.StatusNotFound, wantCC: "no-store"},
		{name: "future", expires: testNow.Add(time.Hour), want: http.StatusOK, wantCC: "public, max-age=3600"},
		{name: "future beyond ttl", expires: testNow.Add(2 * 365 * 24 * time.Hour), want: http.StatusOK, wantCC: "public, max-age=31536000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, withRequest(func(r *transform.Request) { r.Delivery.Expires = tc.expires }))
			h.srv.now = func() time.Time { return testNow }
			rec := h.do(t, http.MethodGet, okPath, nil)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if got := rec.Header().Get("Cache-Control"); got != tc.wantCC {
				t.Errorf("Cache-Control = %q, want %q", got, tc.wantCC)
			}
			if tc.want == http.StatusNotFound && h.f.calls.Load() != 0 {
				t.Error("expired URL fetched the source")
			}
			h.checkReleased(t)
		})
	}
}

// TestCacheBusterInETag: the cache buster changes the key (and ETag) but
// nothing the processor sees besides the request field itself.
func TestCacheBusterInETag(t *testing.T) {
	t.Parallel()
	a := newHarness(t, nil).do(t, http.MethodGet, okPath, nil).Header().Get("ETag")
	b := newHarness(t, withRequest(func(r *transform.Request) { r.Delivery.CacheBuster = "v2" })).
		do(t, http.MethodGet, okPath, nil).Header().Get("ETag")
	if a == b {
		t.Errorf("cache buster did not change the ETag %q", a)
	}
}

func TestErrorCacheControl(t *testing.T) {
	t.Parallel()
	errorTTL := "public, max-age=10"
	tests := []struct {
		kind errs.Kind
		want string
	}{
		{errs.KindInvalidURL, "no-store"},
		{errs.KindUnauthorized, "no-store"},
		{errs.KindForbidden, "no-store"},
		{errs.KindExpired, "no-store"},
		{errs.KindNotFound, errorTTL},
		{errs.KindSourceTooLarge, errorTTL},
		{errs.KindLimitExceeded, errorTTL},
		{errs.KindUnsupported, errorTTL},
		{errs.KindCorrupt, errorTTL},
		{errs.KindHashMismatch, "no-store"},
		{errs.KindNotAcceptable, "no-store"},
		{errs.KindUpstreamError, errorTTL},
		{errs.KindUpstreamTimeout, errorTTL},
		{errs.KindOverloaded, "no-store"},
		{errs.KindUnavailable, "no-store"},
		{errs.KindTimeout, "no-store"},
		{errs.KindInternal, "no-store"},
	}
	for _, tc := range tests {
		t.Run(tc.kind.String(), func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, func(h *harness) { h.f.err = errs.New(tc.kind, "boom") })
			rec := h.do(t, http.MethodGet, okPath, nil)
			if got := rec.Header().Get("Cache-Control"); got != tc.want {
				t.Errorf("Cache-Control = %q, want %q (status %d)", got, tc.want, rec.Code)
			}
		})
	}

	t.Run("zero error ttl", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, func(h *harness) {
			h.cfg.Cache.ErrorTTL = 0
			h.f.err = errs.New(errs.KindNotFound, "boom")
		})
		if got := h.do(t, http.MethodGet, okPath, nil).Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("Cache-Control = %q", got)
		}
	})
	t.Run("not implemented", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, withRequest(func(r *transform.Request) { r.Kind = transform.KindRaw }))
		rec := h.do(t, http.MethodGet, okPath, nil)
		if rec.Code != http.StatusNotImplemented || rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("status %d, Cache-Control %q", rec.Code, rec.Header().Get("Cache-Control"))
		}
	})
}

func TestErrorJSON(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(h *harness) { h.f.err = errs.New(errs.KindNotFound, "origin 404 at 10.0.0.1") })
	rec := h.do(t, http.MethodGet, okPath, http.Header{"Accept": {"text/html;q=0.9, application/json"}, "X-Request-Id": {"req-123"}})
	if rec.Code != http.StatusNotFound || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, Content-Type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var got struct {
		Error struct {
			Kind      string `json:"kind"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Error.Kind != errs.KindNotFound.String() {
		t.Errorf("kind = %q", got.Error.Kind)
	}
	if got.Error.RequestID != "req-123" || strings.Contains(got.Error.Message, "10.0.0.1") {
		t.Errorf("body = %+v", got.Error)
	}
	if !strings.Contains(rec.Header().Get("Vary"), "Accept") {
		t.Errorf("Vary = %q", rec.Header().Get("Vary"))
	}
}

func TestContentDisposition(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		filename string
		src      string
		format   transform.Format
		attach   bool
		want     string
	}{
		{name: "source basename", src: "http://origin/dir/photo.png", format: transform.FormatWebP, want: `inline; filename="photo.webp"`},
		{name: "source query stripped", src: "http://origin/p/cat.jpg?token=secret#frag", format: transform.FormatJPEG, want: `inline; filename="cat.jpg"`},
		{name: "source percent-decoded", src: "http://origin/caf%C3%A9.jpg", format: transform.FormatJPEG, want: `inline; filename="caf_.jpg"; filename*=UTF-8''caf%C3%A9.jpg`},
		{name: "object key", src: "bucket/prefix/a.b.tiff", format: transform.FormatPNG, want: `inline; filename="a.b.png"`},
		{name: "no basename", src: "http://origin/", format: transform.FormatJPEG, want: `inline; filename="image.jpg"`},
		{name: "attachment", src: "x.jpg", format: transform.FormatAVIF, attach: true, want: `attachment; filename="x.avif"`},
		{name: "option wins", filename: "report", src: "x.jpg", format: transform.FormatJPEG, want: `inline; filename="report.jpg"`},
		{name: "option keeps matching extension", filename: "report.JPEG", format: transform.FormatJPEG, want: `inline; filename="report.jpg"`},
		{name: "option other extension kept", filename: "report.v2", format: transform.FormatPNG, want: `inline; filename="report.v2.png"`},
		{name: "path separators", filename: `../../etc/passwd`, format: transform.FormatJPEG, want: `inline; filename="passwd.jpg"`},
		{name: "backslash separators", filename: `C:\Users\x\evil`, format: transform.FormatJPEG, want: `inline; filename="evil.jpg"`},
		{name: "leading dots", filename: `...hidden`, format: transform.FormatJPEG, want: `inline; filename="hidden.jpg"`},
		{name: "quotes", filename: `a"b\c`, format: transform.FormatJPEG, want: `inline; filename="c.jpg"`},
		{name: "quote in name", filename: `say "hi"`, format: transform.FormatJPEG, want: `inline; filename="say _hi_.jpg"; filename*=UTF-8''say%20%22hi%22.jpg`},
		{name: "control characters", filename: "a\r\nContent-Type: x\x00", format: transform.FormatJPEG, want: `inline; filename="aContent-Type: x.jpg"`},
		{name: "non-ascii", filename: "Straße 東京", format: transform.FormatJPEG, want: `inline; filename="Stra_e __.jpg"; filename*=UTF-8''Stra%C3%9Fe%20%E6%9D%B1%E4%BA%AC.jpg`},
		{name: "percent", filename: "100%", format: transform.FormatJPEG, want: `inline; filename="100_.jpg"; filename*=UTF-8''100%25.jpg`},
		{name: "invalid utf-8 dropped", filename: "a\xffb", format: transform.FormatJPEG, want: `inline; filename="ab.jpg"`},
		{name: "only junk", filename: "/../", format: transform.FormatGIF, want: `inline; filename="image.gif"`},
		{name: "long name capped", filename: strings.Repeat("x", 300), format: transform.FormatJPEG, want: `inline; filename="` + strings.Repeat("x", maxFilenameBytes) + `.jpg"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := contentDisposition(tc.filename, tc.src, tc.format, tc.attach); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

// TestETagGolden pins the ETag formula of ARCHITECTURE §9.1. The expected
// values were computed independently from the documented encoding
// (F(x) = len ":" x over canonicalKey, format, qualityDelta, validator,
// buster). A change here invalidates every client and CDN cache: bump the
// "xi1-" version instead.
func TestETagGolden(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "source etag",
			got:  etag("k", transform.FormatJPEG, 0, &source.Object{ETag: `"v1"`, LastModified: testNow}, []byte("src"), "b"),
			want: `"xi1-MHrPqgPDQrhMR_xEU56dsy"`,
		},
		{
			name: "last-modified",
			got:  etag("k", transform.FormatWebP, -15, &source.Object{LastModified: time.Date(1994, 11, 6, 8, 49, 37, 0, time.UTC)}, []byte("src"), ""),
			want: `"xi1-FDKG3kAB54mvcSjrXFbBRX"`,
		},
		{
			name: "content hash",
			got:  etag("k", transform.FormatAVIF, 0, &source.Object{}, []byte("src"), ""),
			want: `"xi1-UREQ83lW55N8zJa8oy2oey"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tc.want, tc.got); diff != "" {
				t.Errorf("ETag (-want +got):\n%s", diff)
			}
		})
	}
}

func TestETagListMatch(t *testing.T) {
	t.Parallel()
	const e = `"xi1-abc"`
	tests := []struct {
		list string
		want bool
	}{
		{`"xi1-abc"`, true},
		{`W/"xi1-abc"`, true},
		{`"a", "b" ,"xi1-abc"`, true},
		{`*`, true},
		{`"xi1-abd"`, false},
		{`xi1-abc`, false},
		{`"xi1-abc`, false},
		{``, false},
		{`"a,b", "xi1-abc"`, true},
	}
	for _, tc := range tests {
		if got := etagListMatch(tc.list, e); got != tc.want {
			t.Errorf("etagListMatch(%q) = %v, want %v", tc.list, got, tc.want)
		}
	}
}

func FuzzETagListMatch(f *testing.F) {
	for _, s := range []string{`"xi1-abc"`, `W/"x", *`, `"a,b"`, `W/`, `"`, ``} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, list string) {
		if etagListMatch(list, `"xi1-abc"`) && !strings.Contains(list, `"xi1-abc"`) && !strings.Contains(list, "*") {
			t.Errorf("matched %q without the tag or *", list)
		}
	})
}

func TestConfigValidateCache(t *testing.T) {
	t.Parallel()
	c := DefaultConfig()
	c.Cache.TTL = -time.Second
	c.CacheTagsHeader = "Bad Header"
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "cache ttls") || !strings.Contains(err.Error(), "cache tags header") {
		t.Errorf("Validate() = %v", err)
	}
}
