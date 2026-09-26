package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/limiter"
	"github.com/xantiagoma/xtandard-image/internal/security/svg"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// fakeSniffer recognises the magic bytes the tests use (codec.Sniff is the
// production implementation; app adapts it).
type fakeSniffer struct{}

func (fakeSniffer) Sniff(b []byte) (Sniffed, bool) {
	switch {
	case bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}):
		return Sniffed{transform.FormatJPEG, "image/jpeg", "jpg"}, true
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return Sniffed{transform.FormatPNG, "image/png", "png"}, true
	case bytes.HasPrefix(b, []byte("GIF89a")):
		return Sniffed{transform.FormatGIF, "image/gif", "gif"}, true
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return Sniffed{transform.FormatWebP, "image/webp", "webp"}, true
	case bytes.HasPrefix(b, []byte("\x00\x00\x00\x0cjP  \r\n\x87\n")):
		return Sniffed{transform.FormatUnset, "image/jp2", "jp2"}, true
	case bytes.HasPrefix(b, []byte("%PDF-")):
		return Sniffed{transform.FormatUnset, "application/pdf", "pdf"}, true
	case svg.IsSVG(b):
		return Sniffed{transform.FormatSVG, "image/svg+xml", "svg"}, true
	}
	return Sniffed{}, false
}

var (
	pngSource  = []byte("\x89PNG\r\n\x1a\n-png-source")
	gifSource  = []byte("GIF89a-gif-source")
	webpSource = []byte("RIFF\x10\x00\x00\x00WEBPVP8 -webp")
	jp2Source  = []byte("\x00\x00\x00\x0cjP  \r\n\x87\n-jp2")
	svgSource  = []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><script>alert(1)</script><rect width="10" height="10"/></svg>`)
	// fallbackSource is the fallback image's bytes (a PNG, so the fakes
	// can tell it from the request's JPEG).
	fallbackSource = pngSource
)

const fallbackRef = "local:///fallback.png"

func rawRequest(r *transform.Request) { r.Kind = transform.KindRaw }

// TestRaw streams the source: no admission, no probe, no processing, and
// the Content-Type comes from the sniffed bytes, not the origin header.
func TestRaw(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		data     []byte
		wantType string
		wantCD   string
	}{
		{"png", pngSource, "image/png", `inline; filename="a.png"`},
		{"jpeg", jpegSource, "image/jpeg", `inline; filename="a.jpg"`},
		{"jpeg 2000 has no output format", jp2Source, "image/jp2", `inline; filename="a.jp2"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, func(h *harness) {
				h.router.mutate = rawRequest
				h.f.data = tc.data
				h.f.obj.ContentType = "text/html" // never trusted
				h.cfg.Fallback.Source = fallbackRef
			})
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				rec := h.do(t, method, okPath, nil)
				if rec.Code != http.StatusOK {
					t.Fatalf("%s: status %d, body %q", method, rec.Code, rec.Body.String())
				}
				hdr := rec.Header()
				if got := hdr.Get("Content-Type"); got != tc.wantType {
					t.Errorf("%s: Content-Type = %q, want %q", method, got, tc.wantType)
				}
				if got := hdr.Get("Content-Disposition"); got != tc.wantCD {
					t.Errorf("%s: Content-Disposition = %q, want %q", method, got, tc.wantCD)
				}
				if hdr.Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(hdr.Get("ETag"), `"xi1-`) {
					t.Errorf("%s: missing standard headers: %v", method, hdr)
				}
				if method == http.MethodGet && !bytes.Equal(rec.Body.Bytes(), tc.data) {
					t.Errorf("body = %q, want the source bytes", rec.Body.Bytes())
				}
			}
			if n := h.a.acquired.Load(); n != 0 {
				t.Errorf("admission acquired %d times, want 0", n)
			}
			if h.p.calls != 0 || h.pr.calls.Load() != 0 {
				t.Errorf("processor called %d, prober called %d times; want 0", h.p.calls, h.pr.calls.Load())
			}
			h.checkReleased(t)
			want := []string{"route", "resolve", "fetch", "respond"}
			if diff := cmp.Diff(want, h.fm.stages[:4]); diff != "" {
				t.Errorf("stages (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRawSVGIsSanitised(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(h *harness) {
		h.router.mutate = rawRequest
		h.f.data = svgSource
	})
	rec := h.do(t, http.MethodGet, okPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "image/svg+xml" {
		t.Errorf("Content-Type = %q", got)
	}
	if body := rec.Body.String(); strings.Contains(body, "script") || !strings.Contains(body, "<rect") {
		t.Errorf("body not sanitised: %q", body)
	}
	if h.allocs.Load() != 1 {
		t.Errorf("sanitised buffers allocated %d, want 1", h.allocs.Load())
	}
	h.checkReleased(t)
}

func TestRawFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*harness)
		want   int
	}{
		{"not an image", func(h *harness) { h.f.data = []byte("%PDF-1.7") }, 422},
		{"unrecognised", func(h *harness) { h.f.data = []byte("hello") }, 422},
		{"not allowlisted", func(h *harness) { h.res.err = errs.New(errs.KindForbidden, "no") }, 403},
		{"svg malformed", func(h *harness) { h.f.data = []byte(`<svg xmlns="http://www.w3.org/2000/svg"><g>`) }, 422},
		// raw never falls back.
		{"fetch error, fallback configured", func(h *harness) {
			h.f.err = errs.New(errs.KindNotFound, "origin 404")
			h.f.other = &fakeReply{data: fallbackSource}
			h.cfg.Fallback.Source = fallbackRef
		}, 404},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, func(h *harness) {
				h.router.mutate = rawRequest
				tc.mutate(h)
			})
			rec := h.do(t, http.MethodGet, okPath, nil)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d (body %q)", rec.Code, tc.want, rec.Body.String())
			}
			if h.a.acquired.Load() != 0 {
				t.Error("admission acquired")
			}
			h.checkReleased(t)
		})
	}
}

// TestSkipProcessing covers XI_FORMATS_SKIP_PROCESSING and the request's
// skip_processing per sniffed format.
func TestSkipProcessing(t *testing.T) {
	t.Parallel()
	type list = []transform.Format
	tests := []struct {
		name        string
		data        []byte
		config, url list
		passthrough bool
		wantType    string
	}{
		{name: "jpeg config", data: jpegSource, config: list{transform.FormatJPEG}, passthrough: true, wantType: "image/jpeg"},
		{name: "png config", data: pngSource, config: list{transform.FormatPNG}, passthrough: true, wantType: "image/png"},
		{name: "gif url", data: gifSource, url: list{transform.FormatGIF}, passthrough: true, wantType: "image/gif"},
		{name: "webp url", data: webpSource, url: list{transform.FormatWebP}, passthrough: true, wantType: "image/webp"},
		{name: "svg config sanitised", data: svgSource, config: list{transform.FormatSVG}, passthrough: true, wantType: "image/svg+xml"},
		{name: "jpeg not listed", data: jpegSource, config: list{transform.FormatPNG}, url: list{transform.FormatGIF}, wantType: "image/jpeg"},
		{name: "nothing listed", data: pngSource, wantType: "image/jpeg"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, func(h *harness) {
				h.f.data = tc.data
				h.f.obj.ContentType = "application/octet-stream"
				h.cfg.Negotiate.SkipProcessing = tc.config
				h.router.mutate = func(r *transform.Request) { r.Delivery.SkipProcessing = tc.url }
			})
			rec := h.do(t, http.MethodGet, okPath, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); got != tc.wantType {
				t.Errorf("Content-Type = %q, want %q", got, tc.wantType)
			}
			processed := h.p.calls == 1
			if processed == tc.passthrough {
				t.Errorf("processor calls = %d, passthrough %v", h.p.calls, tc.passthrough)
			}
			if tc.passthrough {
				if h.a.acquired.Load() != 0 || h.pr.calls.Load() != 0 {
					t.Errorf("passthrough took admission %d / probed %d times", h.a.acquired.Load(), h.pr.calls.Load())
				}
				body := rec.Body.String()
				if tc.data[0] == '<' {
					if strings.Contains(body, "script") {
						t.Errorf("svg not sanitised: %q", body)
					}
				} else if body != string(tc.data) {
					t.Errorf("body = %q, want the source", body)
				}
			}
			h.checkReleased(t)
		})
	}
}

// TestSkipProcessingAfterProbe: the format is only known once probed (the
// sniffer maps it to no output format), so the permit is already held; the
// source is still streamed unmodified.
func TestSkipProcessingAfterProbe(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(h *harness) {
		h.cfg.Negotiate.SkipProcessing = []transform.Format{transform.FormatPNG}
		h.f.data = pngSource
		h.pr.meta = SourceMeta{Format: transform.FormatPNG, Width: 10, Height: 10, Frames: 1}
		h.sniffer = sniffAs{Sniffed{transform.FormatUnset, "image/png", "png"}}
	})
	rec := h.do(t, http.MethodGet, okPath, nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || rec.Body.String() != string(pngSource) {
		t.Fatalf("status %d, type %q, body %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	if h.p.calls != 0 || h.a.acquired.Load() != 1 {
		t.Errorf("processor calls %d, admissions %d; want 0, 1", h.p.calls, h.a.acquired.Load())
	}
	h.checkReleased(t)
}

type sniffAs struct{ s Sniffed }

func (f sniffAs) Sniff([]byte) (Sniffed, bool) { return f.s, true }

// TestFallback serves the fallback image, processed with the same request,
// for every eligible failure kind, and the original error otherwise.
func TestFallback(t *testing.T) {
	t.Parallel()
	fetchErr := func(k errs.Kind) func(*harness) {
		return func(h *harness) { h.f.err = errs.New(k, "origin failed") }
	}
	probeErr := func(k errs.Kind) func(*harness) {
		return func(h *harness) { h.pr.err, h.pr.errFor = errs.New(k, "probe failed"), jpegSource }
	}
	procErr := func(k errs.Kind) func(*harness) {
		return func(h *harness) { h.p.err, h.p.errFor = errs.New(k, "process failed"), jpegSource }
	}
	tests := []struct {
		name     string
		mutate   func(*harness)
		fallback bool
		want     int // status without a fallback
	}{
		{"fetch/not found", fetchErr(errs.KindNotFound), true, 404},
		{"fetch/forbidden by origin", fetchErr(errs.KindForbidden), true, 403},
		{"fetch/upstream error", fetchErr(errs.KindUpstreamError), true, 502},
		{"fetch/upstream timeout", fetchErr(errs.KindUpstreamTimeout), true, 504},
		{"fetch/too large", fetchErr(errs.KindSourceTooLarge), true, 422},
		{"probe/unsupported", probeErr(errs.KindUnsupported), true, 422},
		{"probe/corrupt", probeErr(errs.KindCorrupt), true, 422},
		{"probe/limit", probeErr(errs.KindLimitExceeded), true, 422},
		{"process/corrupt", procErr(errs.KindCorrupt), true, 422},
		{"normalize/validation", func(h *harness) { h.router.mutate = func(r *transform.Request) { r.Output.Quality = 101 } }, false, 400},
		{"resolve/not allowlisted", func(h *harness) { h.res.deny = map[string]bool{"http://origin/a.jpg": true} }, false, 403},
		{"admission/overloaded", func(h *harness) {
			h.a.err = &limiter.OverloadError{Reason: limiter.ReasonQueueFull, Err: errs.New(errs.KindOverloaded, "full")}
		}, false, 429},
		{"process/internal", procErr(errs.KindInternal), false, 500},
		{"negotiate/not acceptable", func(h *harness) { h.cfg.Negotiate.StrictAccept = true }, false, 406},
	}
	for _, tc := range tests {
		for _, cfg := range []struct {
			name   string
			status int
			want   int
		}{{"default status", 0, 200}, {"status 404", 404, 404}} {
			t.Run(tc.name+"/"+cfg.name, func(t *testing.T) {
				t.Parallel()
				h := newHarness(t, func(h *harness) {
					h.f.other = &fakeReply{data: fallbackSource}
					h.cfg.Fallback = Fallback{Source: fallbackRef, Status: cfg.status, TTL: 90e9}
					tc.mutate(h)
				})
				var hdr http.Header
				if tc.name == "negotiate/not acceptable" {
					hdr = http.Header{"Accept": {"text/html"}}
				}
				rec := h.do(t, http.MethodGet, okPath, hdr)
				if !tc.fallback {
					if rec.Code != tc.want {
						t.Errorf("status = %d, want the original %d", rec.Code, tc.want)
					}
					if got := h.fetchedFallback(); got {
						t.Error("fallback fetched for an ineligible failure")
					}
					h.checkReleased(t)
					return
				}
				if rec.Code != cfg.want {
					t.Fatalf("status = %d, want %d (body %q)", rec.Code, cfg.want, rec.Body.String())
				}
				if got := rec.Header().Get("Cache-Control"); got != "public, max-age=90" {
					t.Errorf("Cache-Control = %q", got)
				}
				if got := rec.Header().Get("Content-Type"); got != "image/jpeg" {
					t.Errorf("Content-Type = %q", got)
				}
				if rec.Header().Get(fallbackHeader) != "" {
					t.Error("debug header sent with debug headers off")
				}
				if !bytes.Equal(rec.Body.Bytes(), jpegOut) {
					t.Errorf("body = %q, want the processed fallback", rec.Body.Bytes())
				}
				if !bytes.Equal(h.p.source, fallbackSource) || h.p.req.Pipelines[0].Resize.Width.Px != 64 {
					t.Errorf("processor got source %q / request %+v; want the fallback with the same request", h.p.source, h.p.req.Pipelines[0].Resize)
				}
				h.checkReleased(t)
			})
		}
	}
}

func (h *harness) fetchedFallback() bool {
	for _, p := range h.f.fetched() {
		if p != "http://origin/a.jpg" {
			return true
		}
	}
	return false
}

func TestFallbackHeaders(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(h *harness) {
		h.f.err = errs.New(errs.KindNotFound, "origin 404")
		h.f.other = &fakeReply{data: fallbackSource}
		h.cfg.Fallback = Fallback{Source: fallbackRef}
		h.cfg.DebugHeaders = true
	})
	rec := h.do(t, http.MethodGet, okPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control with TTL 0 = %q, want no-store", got)
	}
	if got := rec.Header().Get(fallbackHeader); got != "1" {
		t.Errorf("%s = %q, want 1", fallbackHeader, got)
	}
	if h.fm.lastCode.Load() != http.StatusOK {
		t.Errorf("metric status = %d", h.fm.lastCode.Load())
	}
}

// TestFallbackNested: when the fallback image fails too, the original
// error is returned and nothing falls back a second time.
func TestFallbackNested(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*harness)
		want   int
	}{
		{"fallback fetch fails", func(h *harness) {
			h.f.err = errs.New(errs.KindUpstreamError, "origin 500")
			h.f.other = &fakeReply{err: errs.New(errs.KindNotFound, "fallback 404")}
		}, 502},
		{"fallback corrupt", func(h *harness) {
			h.f.err = errs.New(errs.KindNotFound, "origin 404")
			h.f.other = &fakeReply{data: fallbackSource}
			h.pr.err = errs.New(errs.KindCorrupt, "bad")
		}, 404},
		{"same image for both", func(h *harness) {
			h.pr.err = errs.New(errs.KindCorrupt, "bad") // every source is corrupt
		}, 422},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, func(h *harness) {
				h.cfg.Fallback = Fallback{Source: fallbackRef, Status: 200}
				tc.mutate(h)
			})
			rec := h.do(t, http.MethodGet, okPath, nil)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want the original %d", rec.Code, tc.want)
			}
			if n := len(h.f.fetched()); n != 2 {
				t.Errorf("%d fetches, want 2 (request + one fallback)", n)
			}
			h.checkReleased(t)
		})
	}
}

// TestFallbackRequestURL covers fallback_image_url: honoured only with
// XI_FALLBACK_ALLOW_REQUEST_URL and only when the allowlist accepts it.
func TestFallbackRequestURL(t *testing.T) {
	t.Parallel()
	const reqURL = "https://cdn/404.png"
	tests := []struct {
		name       string
		allow      bool
		global     string
		deny       bool
		wantStatus int
		wantFetch  string // fallback path fetched; "" = none
	}{
		{name: "allowed", allow: true, global: fallbackRef, wantStatus: 200, wantFetch: reqURL},
		{name: "allowed without global", allow: true, wantStatus: 200, wantFetch: reqURL},
		{name: "disabled uses global", global: fallbackRef, wantStatus: 200, wantFetch: fallbackRef},
		{name: "disabled without global", wantStatus: 404},
		{name: "not allowlisted uses global", allow: true, deny: true, global: fallbackRef, wantStatus: 200, wantFetch: fallbackRef},
		{name: "not allowlisted without global", allow: true, deny: true, wantStatus: 404},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, func(h *harness) {
				h.f.err = errs.New(errs.KindNotFound, "origin 404")
				h.f.other = &fakeReply{data: fallbackSource}
				h.cfg.Fallback = Fallback{Source: tc.global, AllowRequestURL: tc.allow}
				if tc.deny {
					h.res.deny = map[string]bool{reqURL: true}
				}
				h.router.mutate = func(r *transform.Request) { r.Delivery.Fallback = &transform.SourceSpec{Raw: reqURL} }
			})
			rec := h.do(t, http.MethodGet, okPath, nil)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			want := []string{"http://origin/a.jpg"}
			if tc.wantFetch != "" {
				want = append(want, tc.wantFetch)
			}
			if diff := cmp.Diff(want, h.f.fetched()); diff != "" {
				t.Errorf("fetched (-want +got):\n%s", diff)
			}
			h.checkReleased(t)
		})
	}
}

func TestFallbackSourceValidatedAtStartup(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.Fallback.Source = "http://evil/x.png"
	_, err := New(&cfg, &Deps{
		Router: &fakeRouter{}, Resolver: &fakeResolver{deny: map[string]bool{"http://evil/x.png": true}},
		Fetcher: &fakeFetcher{}, Admitter: &fakeAdmitter{}, Prober: &fakeProber{}, Processor: &fakeProcessor{},
		Sniffer: fakeSniffer{}, Allocator: fetch.GoAllocator, Logger: slog.New(slog.DiscardHandler),
	})
	if errs.KindOf(err) != errs.KindForbidden {
		t.Errorf("New = %v, want a forbidden fallback source", err)
	}

	cfg.Fallback = Fallback{Status: 99}
	if err := cfg.Validate(); err == nil {
		t.Error("fallback status 99 accepted")
	}
}

func TestContentDispositionResponse(t *testing.T) {
	t.Parallel()
	yes := true
	h := newHarness(t, func(h *harness) {
		h.router.mutate = func(r *transform.Request) {
			r.Delivery.Filename = "holiday photo"
			r.Delivery.Attachment = &yes
		}
	})
	rec := h.do(t, http.MethodGet, okPath, nil)
	if got, want := rec.Header().Get("Content-Disposition"), `attachment; filename="holiday photo.jpg"`; got != want {
		t.Errorf("Content-Disposition = %q, want %q", got, want)
	}
}

// TestContentDispositionExt covers the extension-taking variant used for
// unprocessed bodies (WP-34c). Sanitisation follows the WP-34b rules of
// contentDisposition: a filename option keeps only its last path segment and
// control characters are dropped.
func TestContentDispositionExt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		src, name, ext string
		attachment     bool
		want           string
	}{
		{"http://origin/dir/cat.png?x=1#f", "", ".webp", false, `inline; filename="cat.webp"`},
		{"a/b/photo.final.jpeg", "", ".jpg", true, `attachment; filename="photo.final.jpg"`},
		{"http://origin/dir/", "", ".png", false, `inline; filename="image.png"`},
		{"", "", "", false, `inline; filename="image"`},
		{"dir/scan.psd", "", ".psd", false, `inline; filename="scan.psd"`},
		{"http://origin/x.jpg", "report", ".avif", false, `inline; filename="report.avif"`},
		{"http://origin/x.jpg", `a"b\c/d`, ".jpg", false, `inline; filename="d.jpg"`},
		{"http://origin/x.jpg", "a\r\nSet-Cookie: x", ".jpg", false, `inline; filename="aSet-Cookie: x.jpg"`},
		{"http://origin/caf%C3%A9.jpg", "", ".jpg", false, `inline; filename="caf_.jpg"; filename*=UTF-8''caf%C3%A9.jpg`},
		{"http://origin/x.jpg", "日本", ".png", false, `inline; filename="__.png"; filename*=UTF-8''%E6%97%A5%E6%9C%AC.png`},
		{"http://origin/x.jpg", strings.Repeat("a", 300), "", false, `inline; filename="` + strings.Repeat("a", maxFilenameBytes) + `"`},
	}
	for _, tc := range tests {
		if got := contentDispositionExt(tc.name, tc.src, tc.ext, tc.attachment); got != tc.want {
			t.Errorf("contentDispositionExt(%q, %q, %q, %v) = %q, want %q", tc.name, tc.src, tc.ext, tc.attachment, got, tc.want)
		}
	}
}

// TestRawConditional: the WP-34b 304 rules apply to raw streaming; the
// ETag is known without processing and a match sends no body.
func TestRawConditional(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(h *harness) {
		h.router.mutate = rawRequest
		h.f.data = pngSource
	})
	first := h.do(t, http.MethodGet, okPath, nil)
	tag := first.Header().Get("ETag")
	if first.Code != http.StatusOK || !strings.HasPrefix(tag, `"xi1-`) {
		t.Fatalf("status %d, ETag %q", first.Code, tag)
	}
	rec := h.do(t, http.MethodGet, okPath, http.Header{"If-None-Match": {tag}})
	if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Errorf("revalidation: status %d, %d body bytes; want 304 and none", rec.Code, rec.Body.Len())
	}
	if rec.Header().Get("ETag") != tag || rec.Header().Get("Content-Type") != "" {
		t.Errorf("304 headers = %v", rec.Header())
	}
	if h.a.acquired.Load() != 0 || h.pr.calls.Load() != 0 || h.p.calls != 0 {
		t.Error("raw revalidation took admission, probed or processed")
	}
	h.checkReleased(t)
}

// TestFallbackIgnoresConditional: a fallback is always a full response
// with the fallback status and TTL, never a 304 that would refresh a
// cached fallback with the success lifetime. The WP-34b ETag and Vary stay.
func TestFallbackIgnoresConditional(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(h *harness) {
		h.f.err = errs.New(errs.KindNotFound, "origin 404")
		h.f.other = &fakeReply{data: fallbackSource}
		h.cfg.Fallback = Fallback{Source: fallbackRef, Status: http.StatusNotFound, TTL: 90e9}
	})
	accept := http.Header{"Accept": {"image/webp,image/avif,*/*"}}
	first := h.do(t, http.MethodGet, okPath, accept)
	tag := first.Header().Get("ETag")
	if first.Code != http.StatusNotFound || tag == "" {
		t.Fatalf("status %d, ETag %q", first.Code, tag)
	}
	if !strings.Contains(first.Header().Get("Vary"), "Accept") {
		t.Errorf("Vary = %q, want the negotiation's Accept", first.Header().Get("Vary"))
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		hdr := http.Header{"If-None-Match": {tag}, "Accept": accept["Accept"]}
		rec := h.do(t, method, okPath, hdr)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s revalidation: status %d, want the fallback's 404", method, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != "public, max-age=90" {
			t.Errorf("%s: Cache-Control = %q", method, got)
		}
		if rec.Header().Get("ETag") != tag {
			t.Errorf("%s: ETag = %q, want %q", method, rec.Header().Get("ETag"), tag)
		}
	}
	h.checkReleased(t)
}
