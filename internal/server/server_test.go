package server

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/limiter"
	"github.com/xantiagoma/xtandard-image/internal/obs/admin"
)

// fakeBody records its release.
type fakeBody struct {
	b        []byte
	released atomic.Int32
}

func (f *fakeBody) Bytes() []byte { return f.b }
func (f *fakeBody) Release()      { f.released.Add(1) }

type fakeFetcher struct {
	mu   sync.Mutex
	urls []string
	body *fakeBody
	err  error
}

func (f *fakeFetcher) Fetch(_ context.Context, rawURL string) (Body, error) {
	f.mu.Lock()
	f.urls = append(f.urls, rawURL)
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.body, nil
}

type fakeAdmitter struct {
	err      error
	released atomic.Int32
}

func (a *fakeAdmitter) Acquire(context.Context, int64) (func(), error) {
	if a.err != nil {
		return nil, a.err
	}
	return func() { a.released.Add(1) }, nil
}

type thumbCall struct{ W, H int }

type fakeProcessor struct {
	mu    sync.Mutex
	calls []thumbCall
	out   *fakeBody
	err   error
	fn    func(ctx context.Context) error // optional behaviour hook
}

func (p *fakeProcessor) Thumbnail(ctx context.Context, _ Body, w, h int) (*Result, error) {
	p.mu.Lock()
	p.calls = append(p.calls, thumbCall{W: w, H: h})
	p.mu.Unlock()
	if p.fn != nil {
		if err := p.fn(ctx); err != nil {
			return nil, err
		}
	}
	if p.err != nil {
		return nil, p.err
	}
	return &Result{Body: p.out, ContentType: "image/jpeg", Format: "jpeg", Width: 64, Height: 48}, nil
}

type fakeMetrics struct {
	panics   atomic.Int32
	requests atomic.Int32
	lastCode atomic.Int64
}

func (m *fakeMetrics) ObserveRequest(_ string, status int, _ string, _ time.Duration) {
	m.requests.Add(1)
	m.lastCode.Store(int64(status))
}
func (m *fakeMetrics) RequestStarted() func() { return func() {} }
func (m *fakeMetrics) IncPanic()              { m.panics.Add(1) }

var jpegBytes = []byte{0xFF, 0xD8, 0xFF, 0xE0, 'f', 'a', 'k', 'e', 0xFF, 0xD9}

type harness struct {
	srv *Server
	f   *fakeFetcher
	a   *fakeAdmitter
	p   *fakeProcessor
	m   *fakeMetrics
	src *fakeBody
	out *fakeBody
	cfg Config
}

func newHarness(t *testing.T, mutate func(*harness)) *harness {
	t.Helper()
	h := &harness{
		src: &fakeBody{b: []byte("source")},
		out: &fakeBody{b: jpegBytes},
		a:   &fakeAdmitter{},
		m:   &fakeMetrics{},
		cfg: DefaultConfig(),
	}
	h.cfg.DevMode = true
	h.f = &fakeFetcher{body: h.src}
	h.p = &fakeProcessor{out: h.out}
	if mutate != nil {
		mutate(h)
	}
	srv, err := New(h.cfg, &Deps{
		Fetcher: h.f, Admitter: h.a, Processor: h.p, Metrics: h.m,
		Logger:    slog.New(slog.DiscardHandler),
		Readiness: admin.NewReadiness(),
	})
	if err != nil {
		t.Fatal(err)
	}
	h.srv = srv
	return h
}

func (h *harness) do(t *testing.T, method, target string, hdr http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, http.NoBody)
	maps.Copy(req.Header, hdr)
	rec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, req)
	return rec
}

const okPath = "/insecure/rs:fit:64:64/plain/http://origin/a.jpg"

func TestImageSuccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)

	rec := h.do(t, http.MethodGet, okPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Content-Length"); got != "10" {
		t.Errorf("Content-Length = %q, want 10", got)
	}
	if diff := cmp.Diff(jpegBytes, rec.Body.Bytes()); diff != "" {
		t.Errorf("body (-want +got):\n%s", diff)
	}
	etag := rec.Header().Get("ETag")
	if !strings.HasPrefix(etag, `"`) || len(etag) < 3 {
		t.Errorf("ETag = %q, want a quoted strong tag", etag)
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("missing X-Request-Id")
	}
	if diff := cmp.Diff([]string{"http://origin/a.jpg"}, h.f.urls); diff != "" {
		t.Errorf("fetched urls (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]thumbCall{{W: 64, H: 64}}, h.p.calls); diff != "" {
		t.Errorf("processor calls (-want +got):\n%s", diff)
	}
	if h.src.released.Load() != 1 || h.out.released.Load() != 1 || h.a.released.Load() != 1 {
		t.Errorf("releases: source %d, output %d, admission %d; want 1 each",
			h.src.released.Load(), h.out.released.Load(), h.a.released.Load())
	}
	if h.m.requests.Load() != 1 || h.m.lastCode.Load() != http.StatusOK {
		t.Errorf("metrics: %d requests, last status %d", h.m.requests.Load(), h.m.lastCode.Load())
	}

	// Same bytes → same ETag → 304 without a body.
	rec = h.do(t, http.MethodGet, okPath, http.Header{"If-None-Match": {`"x", ` + etag}})
	if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Errorf("conditional GET: status %d, %d body bytes; want 304, 0", rec.Code, rec.Body.Len())
	}
}

func TestImageHead(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)

	rec := h.do(t, http.MethodHead, "/unsafe/w:100/plain/https://origin/b.png", nil)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "10" {
		t.Errorf("HEAD: status %d, %d body bytes, Content-Length %q", rec.Code, rec.Body.Len(), rec.Header().Get("Content-Length"))
	}
	if diff := cmp.Diff([]thumbCall{{W: 100, H: 0}}, h.p.calls); diff != "" {
		t.Errorf("processor calls (-want +got):\n%s", diff)
	}
}

func TestImageErrors(t *testing.T) {
	t.Parallel()

	overload := &limiter.OverloadError{
		Reason: limiter.ReasonQueueFull, RetryAfter: 3 * time.Second,
		Err: errs.New(errs.KindOverloaded, "limiter: queue full"),
	}
	tests := []struct {
		name       string
		method     string
		target     string
		mutate     func(*harness)
		wantStatus int
		wantBody   string // exact public body
		wantHeader map[string]string
	}{
		{name: "not dev mode", target: okPath, mutate: func(h *harness) { h.cfg.DevMode = false }, wantStatus: 403, wantBody: "forbidden\n"},
		{name: "unknown options", target: "/insecure/crop:10:10/plain/http://origin/a.jpg", wantStatus: 400, wantBody: "invalid url\n"},
		{name: "signed url", target: "/abc123/rs:fit:1:1/plain/http://origin/a.jpg", wantStatus: 403, wantBody: "invalid signature\n"},
		{name: "relative source", target: "/insecure/w:10/plain/origin/a.jpg", wantStatus: 400},
		{name: "not found", target: okPath, mutate: func(h *harness) { h.f.err = errs.New(errs.KindNotFound, "origin said 404") }, wantStatus: 404},
		{name: "upstream error", target: okPath, mutate: func(h *harness) { h.f.err = errs.New(errs.KindUpstreamError, "dial tcp 10.0.0.1:80: refused") }, wantStatus: 502, wantBody: "upstream error\n"},
		{name: "ssrf block", target: okPath, mutate: func(h *harness) { h.f.err = errs.New(errs.KindForbidden, "ssrf: 127.0.0.1 blocked") }, wantStatus: 403, wantBody: "forbidden\n"},
		{name: "too large", target: okPath, mutate: func(h *harness) { h.f.err = errs.New(errs.KindSourceTooLarge, "over") }, wantStatus: 422},
		{name: "queue full", target: okPath, mutate: func(h *harness) { h.a.err = overload }, wantStatus: 429, wantHeader: map[string]string{"Retry-After": "3", "Cache-Control": "no-store"}},
		{name: "corrupt", target: okPath, mutate: func(h *harness) { h.p.err = errs.New(errs.KindCorrupt, "VipsJpeg: premature end") }, wantStatus: 422, wantBody: "image cannot be decoded\n"},
		{name: "unsupported", target: okPath, mutate: func(h *harness) { h.p.err = errs.New(errs.KindUnsupported, "no loader") }, wantStatus: 422},
		{name: "plain error hides text", target: okPath, mutate: func(h *harness) { h.p.err = errors.New("secret internal detail") }, wantStatus: 500, wantBody: "internal server error\n"},
		{name: "method", method: http.MethodPost, target: okPath, wantStatus: 405, wantHeader: map[string]string{"Allow": "GET, HEAD"}},
		{name: "url too long", target: "/insecure/w:1/plain/http://origin/" + strings.Repeat("a", maxURLLength), wantStatus: 414},
		{name: "panic", target: okPath, mutate: func(h *harness) {
			h.p.fn = func(context.Context) error { panic("boom") }
		}, wantStatus: 500, wantBody: "internal server error\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, tc.mutate)
			method := tc.method
			if method == "" {
				method = http.MethodGet
			}
			rec := h.do(t, method, tc.target, nil)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantBody != "" && rec.Body.String() != tc.wantBody {
				t.Errorf("body = %q, want %q", rec.Body.String(), tc.wantBody)
			}
			for k, v := range tc.wantHeader {
				if got := rec.Header().Get(k); got != v {
					t.Errorf("%s = %q, want %q", k, got, v)
				}
			}
			if ct := rec.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
				t.Errorf("error Content-Type = %q", ct)
			}
			if tc.name == "panic" && h.m.panics.Load() != 1 {
				t.Errorf("panics counted = %d, want 1", h.m.panics.Load())
			}
			if h.src.released.Load() > 1 || h.a.released.Load() > 1 {
				t.Errorf("double release: source %d, admission %d", h.src.released.Load(), h.a.released.Load())
			}
		})
	}
}

func TestRequestTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, func(h *harness) {
			h.cfg.RequestTimeout = 2 * time.Second
			h.p.fn = func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			}
		})
		rec := h.do(t, http.MethodGet, okPath, nil)
		if rec.Code != http.StatusGatewayTimeout {
			t.Errorf("status = %d, want 504", rec.Code)
		}
	})
}

func TestClientGoneIsNotWritten(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(h *harness) {
		h.p.err = errs.Wrap(errs.KindCanceled, context.Canceled)
	})
	rec := h.do(t, http.MethodGet, okPath, nil)
	if rec.Body.Len() != 0 || rec.Header().Get("Content-Type") != "" {
		t.Errorf("canceled request wrote %q with Content-Type %q", rec.Body.String(), rec.Header().Get("Content-Type"))
	}
	if h.m.lastCode.Load() != errs.StatusClientClosedRequest {
		t.Errorf("recorded status = %d, want 499", h.m.lastCode.Load())
	}
}

func TestFixedRoutes(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)

	if rec := h.do(t, http.MethodGet, "/healthz", nil); rec.Code != http.StatusOK {
		t.Errorf("/healthz = %d", rec.Code)
	}
	if rec := h.do(t, http.MethodGet, "/readyz", nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/readyz before ready = %d, want 503", rec.Code)
	}
	if h.m.requests.Load() != 0 {
		t.Errorf("fixed routes counted as image requests: %d", h.m.requests.Load())
	}
}

func TestHTTPServerTimeouts(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)
	s := h.srv.HTTPServer(t.Context())
	if s.ReadHeaderTimeout <= 0 || s.ReadTimeout <= 0 || s.WriteTimeout <= 0 || s.IdleTimeout <= 0 || s.MaxHeaderBytes <= 0 {
		t.Errorf("unset timeout in %+v", s)
	}
	if s.BaseContext == nil || s.ErrorLog == nil {
		t.Error("BaseContext and ErrorLog must be set")
	}
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
	var zero Config
	err := zero.Validate()
	if err == nil {
		t.Fatal("zero config valid")
	}
	for _, want := range []string{"request timeout", "read header timeout", "max header bytes"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
