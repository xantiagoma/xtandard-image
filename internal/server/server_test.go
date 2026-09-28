package server

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/router"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/limiter"
	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/obs/admin"
	"github.com/xantiagoma/xtandard-image/internal/obs/logging"
	"github.com/xantiagoma/xtandard-image/internal/obs/metrics"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/source"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// ---- fakes ------------------------------------------------------------------

type fakeRouter struct {
	err     error
	mutate  func(*transform.Request)
	dialect string
	sig     sign.Result
	policy  router.Policy // the mount's unsigned policy
}

func (f *fakeRouter) Route(*http.Request) (*dialect.Result, *router.Mount, error) {
	m := &router.Mount{Dialect: f.dialect, Policy: f.policy}
	if f.err != nil {
		return nil, m, f.err
	}
	req := &transform.Request{
		Source:    transform.SourceSpec{Raw: "http://origin/a.jpg"},
		Pipelines: []transform.Pipeline{{Resize: transform.Resize{Width: transform.Dim{Px: 64}}}},
	}
	if f.mutate != nil {
		f.mutate(req)
	}
	return &dialect.Result{Request: req, SourceString: req.Source.Raw, Signature: f.sig}, m, nil
}

type fakeResolver struct {
	err  error
	deny map[string]bool // source strings refused with KindForbidden
}

func (f *fakeResolver) Resolve(spec transform.SourceSpec) (source.Key, error) {
	if f.err != nil {
		return source.Key{}, f.err
	}
	if f.deny[spec.Raw] {
		return source.Key{}, errs.New(errs.KindForbidden, "source: not allowed")
	}
	return source.Key{Source: "origin", Path: spec.Raw}, nil
}

type fakeFetcher struct {
	data  []byte
	obj   source.Object
	err   error
	fn    func(ctx context.Context) error
	freed atomic.Int32
	calls atomic.Int32
	// other answers every path but the request's (fallback images);
	// nil: same as the request's.
	other *fakeReply

	mu    sync.Mutex
	paths []string
}

type fakeReply struct {
	data []byte
	err  error
}

func (f *fakeFetcher) Fetch(ctx context.Context, key source.Key, _ source.FetchOptions) (*fetch.Result, error) {
	f.mu.Lock()
	f.paths = append(f.paths, key.Path)
	f.mu.Unlock()
	if f.fn != nil {
		if err := f.fn(ctx); err != nil {
			return nil, err
		}
	}
	data, err := f.data, f.err
	if f.other != nil && key.Path != "http://origin/a.jpg" {
		data, err = f.other.data, f.other.err
	}
	if err != nil {
		return nil, err
	}
	f.calls.Add(1)
	b := fetch.NewGoBuffer(len(data), func() { f.freed.Add(1) })
	copy(b.Bytes(), data)
	return &fetch.Result{Buffer: b, Object: f.obj}, nil
}

func (f *fakeFetcher) fetched() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.paths)
}

type fakeAdmitter struct {
	err      error
	acquired atomic.Int32
	released atomic.Int32
}

func (a *fakeAdmitter) Acquire(context.Context, int64) (func(), error) {
	if a.err != nil {
		return nil, a.err
	}
	a.acquired.Add(1)
	return func() { a.released.Add(1) }, nil
}

type fakeProber struct {
	meta SourceMeta
	err  error
	// errFor fails only for the given source bytes (the request's, not
	// the fallback's).
	errFor []byte
	calls  atomic.Int32
	mu     sync.Mutex
	seen   []byte
	// password is the last ProbeLimits.Password (copied).
	password []byte
}

func (p *fakeProber) Probe(_ context.Context, src fetch.Buffer, lim ProbeLimits) (SourceMeta, error) {
	p.calls.Add(1)
	p.mu.Lock()
	p.seen = bytes.Clone(src.Bytes())
	p.password = bytes.Clone(lim.Password)
	p.mu.Unlock()
	if p.errFor != nil && !bytes.Equal(p.errFor, src.Bytes()) {
		return p.meta, nil
	}
	return p.meta, p.err
}

type fakeBlob struct {
	b        []byte
	released atomic.Int32
}

func (f *fakeBlob) Bytes() []byte { return f.b }
func (f *fakeBlob) Release()      { f.released.Add(1) }

type fakeProcessor struct {
	out    *fakeBlob
	err    error
	errFor []byte // err only for these source bytes; nil: always
	fn     func(ctx context.Context) error
	// outFormat and outType override the JPEG output when set.
	outFormat transform.Format
	outType   string
	outBest   string // ProcessOutput.Best
	outCodec  string // ProcessOutput.VideoCodec

	mu       sync.Mutex
	calls    int
	source   []byte
	req      *transform.Request
	format   transform.Format
	decision negotiate.Decision
	delta    int              // ProcessInput.QualityDelta
	marks    []WatermarkInput // ProcessInput.Watermarks, Source bytes in markBytes
	markData [][]byte
	meta     SourceMeta // ProcessInput.Meta
	password []byte     // ProcessInput.Password (copied)
}

func (p *fakeProcessor) Process(ctx context.Context, in *ProcessInput) (*ProcessOutput, error) {
	p.mu.Lock()
	p.calls++
	p.source = bytes.Clone(in.Source.Bytes())
	p.req = in.Request
	p.format = in.Decision.Format
	p.decision = in.Decision
	p.delta = in.QualityDelta
	p.meta = in.Meta
	p.password = bytes.Clone(in.Password)
	p.marks = slices.Clone(in.Watermarks)
	p.markData = nil
	for _, m := range in.Watermarks {
		p.markData = append(p.markData, bytes.Clone(m.Source.Bytes()))
	}
	p.mu.Unlock()
	if p.fn != nil {
		if err := p.fn(ctx); err != nil {
			return nil, err
		}
	}
	if p.err != nil && (p.errFor == nil || bytes.Equal(p.errFor, in.Source.Bytes())) {
		return nil, p.err
	}
	o := &ProcessOutput{Body: p.out, ContentType: "image/jpeg", Format: transform.FormatJPEG, Width: 64, Height: 48, Quality: 80}
	if p.outFormat != transform.FormatUnset {
		o.Format, o.ContentType = p.outFormat, p.outType
	}
	o.Best = p.outBest
	o.VideoCodec = p.outCodec
	return o, nil
}

type fakeMetrics struct {
	panics   atomic.Int32
	requests atomic.Int32
	lastCode atomic.Int64
	mu       sync.Mutex
	stages   []string
}

func (m *fakeMetrics) ObserveRequest(_ string, status int, _ string, _ time.Duration) {
	m.requests.Add(1)
	m.lastCode.Store(int64(status))
}
func (m *fakeMetrics) RequestStarted() func()  { return func() {} }
func (m *fakeMetrics) IncPanic()               { m.panics.Add(1) }
func (m *fakeMetrics) ObserveRawDecode(string) {}
func (m *fakeMetrics) ObserveStage(stage string, _ time.Duration) {
	m.mu.Lock()
	m.stages = append(m.stages, stage)
	m.mu.Unlock()
}

// ---- harness ----------------------------------------------------------------

var (
	jpegSource = []byte{0xFF, 0xD8, 0xFF, 0xE0, 's', 'r', 'c', 0xFF, 0xD9}
	jpegOut    = []byte{0xFF, 0xD8, 0xFF, 0xE0, 'o', 'u', 't', 'p', 'u', 't', 0xFF, 0xD9}
)

type harness struct {
	cfg     Config
	router  *fakeRouter
	res     *fakeResolver
	f       *fakeFetcher
	a       *fakeAdmitter
	pr      *fakeProber
	p       *fakeProcessor
	sniffer Sniffer
	m       Metrics
	fm      *fakeMetrics
	logger  *slog.Logger
	allocs  atomic.Int32
	allocFr atomic.Int32
	cache   cache.Store // result cache; nil: off
	srv     *Server
}

func newHarness(tb testing.TB, mutate func(*harness)) *harness {
	tb.Helper()
	h := &harness{
		cfg:     DefaultConfig(),
		router:  &fakeRouter{dialect: "imgproxy", policy: router.PolicyAllow},
		res:     &fakeResolver{},
		f:       &fakeFetcher{data: jpegSource},
		a:       &fakeAdmitter{},
		pr:      &fakeProber{meta: SourceMeta{Format: transform.FormatJPEG, Width: 640, Height: 480, Frames: 1}},
		p:       &fakeProcessor{out: &fakeBlob{b: jpegOut}},
		sniffer: fakeSniffer{},
		fm:      &fakeMetrics{},
		logger:  slog.New(slog.DiscardHandler),
	}
	h.m = h.fm
	if mutate != nil {
		mutate(h)
	}
	srv, err := New(&h.cfg, &Deps{
		Router: h.router, Resolver: h.res, Fetcher: h.f, Admitter: h.a, Prober: h.pr, Processor: h.p, Sniffer: h.sniffer,
		Allocator: func(n int) (fetch.Buffer, error) {
			h.allocs.Add(1)
			return fetch.NewGoBuffer(n, func() { h.allocFr.Add(1) }), nil
		},
		Metrics:     h.m,
		Logger:      h.logger,
		Readiness:   admin.NewReadiness(),
		ResultCache: h.cache,
	})
	if err != nil {
		tb.Fatal(err)
	}
	h.srv = srv
	return h
}

func (h *harness) do(tb testing.TB, method, target string, hdr http.Header) *httptest.ResponseRecorder {
	tb.Helper()
	req := httptest.NewRequestWithContext(tb.Context(), method, target, http.NoBody)
	maps.Copy(req.Header, hdr)
	rec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, req)
	return rec
}

// checkReleased asserts every reference taken during a request was dropped.
func (h *harness) checkReleased(t *testing.T) {
	t.Helper()
	if n := h.f.calls.Load(); n > 0 && h.f.freed.Load() != n {
		t.Errorf("source buffers freed %d of %d", h.f.freed.Load(), n)
	}
	if h.a.acquired.Load() != h.a.released.Load() {
		t.Errorf("admission: acquired %d, released %d", h.a.acquired.Load(), h.a.released.Load())
	}
	if h.allocs.Load() != h.allocFr.Load() {
		t.Errorf("sanitised buffers: allocated %d, freed %d", h.allocs.Load(), h.allocFr.Load())
	}
}

const okPath = "/insecure/rs:fit:64:64/plain/http://origin/a.jpg"

// ---- tests ------------------------------------------------------------------

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
	if got := rec.Header().Get("Content-Length"); got != "12" {
		t.Errorf("Content-Length = %q, want 12", got)
	}
	if diff := cmp.Diff(jpegOut, rec.Body.Bytes()); diff != "" {
		t.Errorf("body (-want +got):\n%s", diff)
	}
	etag := rec.Header().Get("ETag")
	if !strings.HasPrefix(etag, `"xi1-`) || len(etag) != len(`"xi1-`)+etagLen+1 {
		t.Errorf("ETag = %q, want \"xi1-<22 chars>\"", etag)
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("missing X-Request-Id")
	}
	if h.p.req.Pipelines[0].Resize.Type != transform.ResizeFit || h.p.req.Pipelines[0].Resize.DPR != 1 {
		t.Errorf("processor got a request without defaults: %+v", h.p.req.Pipelines[0].Resize)
	}
	if h.p.format != transform.FormatJPEG {
		t.Errorf("negotiated %v, want jpeg (no Accept)", h.p.format)
	}
	if h.p.out.released.Load() != 1 {
		t.Errorf("output released %d times, want 1", h.p.out.released.Load())
	}
	h.checkReleased(t)
	want := []string{"route", "normalize", "resolve", "fetch", "admission", "probe", "negotiate", "process", "respond"}
	if diff := cmp.Diff(want, h.fm.stages); diff != "" {
		t.Errorf("stages (-want +got):\n%s", diff)
	}
	if h.fm.requests.Load() != 1 || h.fm.lastCode.Load() != http.StatusOK {
		t.Errorf("metrics: %d requests, last status %d", h.fm.requests.Load(), h.fm.lastCode.Load())
	}

	// Deterministic: the same request and source give the same ETag; a new
	// source validator changes it.
	if again := h.do(t, http.MethodGet, okPath, nil).Header().Get("ETag"); again != etag {
		t.Errorf("ETag not stable: %q then %q", etag, again)
	}
	h.f.obj.ETag = `"v2"`
	if changed := h.do(t, http.MethodGet, okPath, nil).Header().Get("ETag"); changed == etag {
		t.Error("ETag did not change with the source ETag")
	}
}

// TestStageFailures maps a failure at every lifecycle stage to its status
// (ARCHITECTURE §2 and §12). Case names are "stage/failure".
func TestStageFailures(t *testing.T) {
	t.Parallel()

	queueFull := &limiter.OverloadError{
		Reason: limiter.ReasonQueueFull, RetryAfter: 3 * time.Second,
		Err: errs.New(errs.KindOverloaded, "limiter: queue full"),
	}
	waitTimeout := &limiter.OverloadError{
		Reason: limiter.ReasonQueueTimeout, RetryAfter: 7 * time.Second,
		Err: errs.New(errs.KindUnavailable, "limiter: no worker within 5s"),
	}
	route := func(err error) func(*harness) { return func(h *harness) { h.router.err = err } }
	req := func(fn func(*transform.Request)) func(*harness) { return func(h *harness) { h.router.mutate = fn } }
	fetchErr := func(err error) func(*harness) { return func(h *harness) { h.f.err = err } }
	probeErr := func(err error) func(*harness) { return func(h *harness) { h.pr.err = err } }
	meta := func(m SourceMeta) func(*harness) { return func(h *harness) { h.pr.meta = m } }
	procErr := func(err error) func(*harness) { return func(h *harness) { h.p.err = err } }

	tests := []struct {
		name       string
		method     string
		target     string
		header     http.Header
		mutate     func(*harness)
		wantStatus int
		wantBody   string // exact public body when set
		wantHeader map[string]string
	}{
		{name: "ingress/method", method: http.MethodPost, wantStatus: 405, wantHeader: map[string]string{"Allow": "GET, HEAD"}},
		{name: "ingress/url too long", target: "/insecure/w:1/plain/http://origin/" + strings.Repeat("a", maxURLLength), wantStatus: 414},
		{name: "ingress/outside path prefix", target: "/other/x", mutate: func(h *harness) { h.cfg.PathPrefix = "/img" }, wantStatus: 404},
		{name: "route/no route", mutate: route(&errs.Error{Kind: errs.KindNotFound, Public: "not found", Err: router.ErrNoRoute}), wantStatus: 404, wantBody: "not found\n"},
		{name: "parse/malformed", mutate: route(dialect.InvalidURL("missing source")), wantStatus: 400, wantBody: "invalid url: missing source\n"},
		{name: "parse/bad argument", mutate: route(dialect.InvalidArgument("width", "abc", "an integer")), wantStatus: 400, wantBody: "invalid width: expected an integer\n"},
		{name: "authenticate/bad signature", mutate: route(errs.New(errs.KindUnauthorized, "sign: mismatch")), wantStatus: 403, wantBody: "invalid signature\n"},
		{name: "authenticate/expired", mutate: route(dialect.Expired()), wantStatus: 404},
		{name: "authenticate/unsigned policy", mutate: route(&errs.Error{Kind: errs.KindForbidden, Public: "unsigned request not allowed", Err: router.ErrUnsignedNotAllowed}), wantStatus: 403},
		{name: "route/info not implemented", mutate: req(func(r *transform.Request) { r.Kind = transform.KindInfo }), wantStatus: 501},
		{name: "route/raw not an image", mutate: func(h *harness) {
			h.router.mutate = func(r *transform.Request) { r.Kind = transform.KindRaw }
			h.f.data = []byte("%PDF-1.7")
		}, wantStatus: 422},
		{name: "normalize/validation", mutate: req(func(r *transform.Request) { r.Output.Quality = 101 }), wantStatus: 400},
		{name: "normalize/result dimension", mutate: req(func(r *transform.Request) { r.Pipelines[0].Resize.Width.Px = 9000 }), wantStatus: 422},
		{name: "resolve/not allowed", mutate: func(h *harness) { h.res.err = errs.New(errs.KindForbidden, "source: no match for 10.0.0.1") }, wantStatus: 403, wantBody: "forbidden\n"},
		{name: "resolve/bad source", mutate: func(h *harness) { h.res.err = errs.New(errs.KindInvalidURL, "source: bad ref") }, wantStatus: 400},
		{name: "fetch/not found", mutate: fetchErr(errs.New(errs.KindNotFound, "origin 404")), wantStatus: 404},
		{name: "fetch/upstream error", mutate: fetchErr(errs.New(errs.KindUpstreamError, "dial tcp 10.0.0.1:80: refused")), wantStatus: 502, wantBody: "upstream error\n"},
		{name: "fetch/upstream timeout", mutate: fetchErr(errs.New(errs.KindUpstreamTimeout, "attempt timeout")), wantStatus: 504},
		{name: "fetch/too large", mutate: fetchErr(errs.New(errs.KindSourceTooLarge, "over")), wantStatus: 422},
		{name: "fetch/ssrf", mutate: fetchErr(errs.New(errs.KindForbidden, "ssrf: 127.0.0.1 blocked")), wantStatus: 403, wantBody: "forbidden\n"},
		{name: "admission/queue full", mutate: func(h *harness) { h.a.err = queueFull }, wantStatus: 429, wantHeader: map[string]string{"Retry-After": "3", "Cache-Control": "no-store"}},
		{name: "admission/wait timeout", mutate: func(h *harness) { h.a.err = waitTimeout }, wantStatus: 503, wantHeader: map[string]string{"Retry-After": "7"}},
		{name: "admission/memory budget", mutate: func(h *harness) { h.a.err = errs.New(errs.KindLimitExceeded, "limiter: too big") }, wantStatus: 422},
		{name: "probe/unsupported", mutate: probeErr(errs.New(errs.KindUnsupported, "no loader")), wantStatus: 422, wantBody: "unsupported image format\n"},
		{name: "probe/corrupt", mutate: probeErr(errs.New(errs.KindCorrupt, "bad header")), wantStatus: 422},
		{name: "probe/prober limit", mutate: probeErr(errs.New(errs.KindLimitExceeded, "too many pixels")), wantStatus: 422},
		{name: "probe/pixel limit", mutate: meta(SourceMeta{Format: transform.FormatJPEG, Width: 10000, Height: 10000, Frames: 1}), wantStatus: 422},
		{name: "probe/frame limit", mutate: meta(SourceMeta{Format: transform.FormatGIF, Width: 10, Height: 10, Frames: 300, IsAnimated: true}), wantStatus: 422},
		{name: "probe/svg malformed", mutate: func(h *harness) { h.f.data = []byte(`<svg xmlns="http://www.w3.org/2000/svg"><g>`) }, wantStatus: 422},
		{name: "probe/svg missed by sniffer", mutate: meta(SourceMeta{Format: transform.FormatSVG, Width: 10, Height: 10, Frames: 1}), wantStatus: 422},
		{name: "negotiate/strict accept", header: http.Header{"Accept": {"text/html"}}, mutate: func(h *harness) { h.cfg.Negotiate.StrictAccept = true }, wantStatus: 406},
		{name: "process/corrupt", mutate: procErr(errs.New(errs.KindCorrupt, "VipsJpeg: premature end")), wantStatus: 422, wantBody: "image cannot be decoded\n"},
		{name: "process/limit", mutate: procErr(errs.New(errs.KindLimitExceeded, "result too big")), wantStatus: 422},
		{name: "process/internal hides text", mutate: procErr(errors.New("secret internal detail")), wantStatus: 500, wantBody: "internal server error\n"},
		{name: "process/panic", mutate: func(h *harness) { h.p.fn = func(context.Context) error { panic("boom") } }, wantStatus: 500, wantBody: "internal server error\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, tc.mutate)
			method, target := tc.method, tc.target
			if method == "" {
				method = http.MethodGet
			}
			if target == "" {
				target = okPath
			}
			rec := h.do(t, method, target, tc.header)
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
			if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "10.0.0.1") {
				t.Errorf("internal text leaked: %q", rec.Body.String())
			}
			if h.fm.lastCode.Load() != int64(tc.wantStatus) {
				t.Errorf("metric status = %d, want %d", h.fm.lastCode.Load(), tc.wantStatus)
			}
			h.checkReleased(t)
		})
	}
}

func TestCompatImgproxyStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		err          error
		plain, compa int
	}{
		{"invalid url", dialect.InvalidURL("bad"), 400, 404},
		{"validation", nil, 400, 404},
		{"forbidden source", errs.New(errs.KindForbidden, "no match"), 403, 404},
		{"bad signature stays", errs.New(errs.KindUnauthorized, "mismatch"), 403, 403},
		{"corrupt stays", errs.New(errs.KindCorrupt, "x"), 422, 422},
	}
	for _, tc := range tests {
		for _, compat := range []bool{false, true} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				h := newHarness(t, func(h *harness) {
					h.cfg.CompatImgproxyStatus = compat
					switch {
					case tc.err == nil:
						h.router.mutate = func(r *transform.Request) { r.Output.Quality = 101 }
					case errs.KindOf(tc.err) == errs.KindCorrupt:
						h.p.err = tc.err
					case errs.KindOf(tc.err) == errs.KindForbidden:
						h.res.err = tc.err
					default:
						h.router.err = tc.err
					}
				})
				want := tc.plain
				if compat {
					want = tc.compa
				}
				rec := h.do(t, http.MethodGet, okPath, nil)
				if rec.Code != want {
					t.Errorf("compat=%v: status %d, want %d", compat, rec.Code, want)
				}
				if compat && want == 404 && rec.Body.String() != "not found\n" {
					t.Errorf("compat body = %q", rec.Body.String())
				}
			})
		}
	}
}

func TestRequestTimeout(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*harness)
	}{
		{"processor returns ctx error", func(h *harness) {
			h.p.fn = func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
		}},
		{"processor returns bare cause", func(h *harness) {
			h.p.fn = func(ctx context.Context) error { <-ctx.Done(); return context.Cause(ctx) }
		}},
		{"fetch waits for deadline", func(h *harness) {
			h.f.fn = func(ctx context.Context) error {
				<-ctx.Done()
				return errs.Wrap(errs.KindCanceled, ctx.Err())
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newHarness(t, func(h *harness) {
					h.cfg.RequestTimeout = 2 * time.Second
					tc.mutate(h)
				})
				rec := h.do(t, http.MethodGet, okPath, nil)
				if rec.Code != http.StatusGatewayTimeout || rec.Body.String() != "processing timeout\n" {
					t.Errorf("status = %d body %q, want 504", rec.Code, rec.Body.String())
				}
				h.checkReleased(t)
			})
		})
	}
}

func TestClientGoneIsNotWritten(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)
	ctx, cancel := context.WithCancel(t.Context())
	h.p.fn = func(context.Context) error { cancel(); return context.Canceled }
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, okPath, http.NoBody)
	rec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, req)
	if rec.Body.Len() != 0 || rec.Header().Get("Content-Type") != "" {
		t.Errorf("canceled request wrote %q with Content-Type %q", rec.Body.String(), rec.Header().Get("Content-Type"))
	}
	if h.fm.lastCode.Load() != errs.StatusClientClosedRequest {
		t.Errorf("recorded status = %d, want 499", h.fm.lastCode.Load())
	}
}

func TestPanicCountsMetric(t *testing.T) {
	t.Parallel()
	m, err := metrics.New(metrics.Options{})
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, func(h *harness) {
		h.m = m
		h.p.fn = func(context.Context) error { panic("boom") }
	})
	if rec := h.do(t, http.MethodGet, okPath, nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	var got float64 = -1
	for _, f := range families {
		if f.GetName() == "xi_panics_total" {
			got = f.GetMetric()[0].GetCounter().GetValue()
		}
	}
	if got != 1 {
		t.Errorf("xi_panics_total = %v, want 1", got)
	}
	h.checkReleased(t)
}

func TestRequestIDInLogs(t *testing.T) {
	t.Parallel()
	var buf syncBuffer
	h := newHarness(t, func(h *harness) {
		h.logger = logging.New(&buf, logging.Options{Level: slog.LevelDebug})
		h.f.err = errs.New(errs.KindNotFound, "origin 404")
	})
	rec := h.do(t, http.MethodGet, okPath, http.Header{"X-Request-Id": {"req-abc.123"}})
	if got := rec.Header().Get("X-Request-ID"); got != "req-abc.123" {
		t.Errorf("X-Request-ID = %q", got)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("want an error line and an access line, got %q", buf.String())
	}
	for _, l := range lines {
		if !strings.Contains(l, `"request_id":"req-abc.123"`) {
			t.Errorf("log line without request id: %s", l)
		}
	}
	if !strings.Contains(buf.String(), `"msg":"request served"`) || !strings.Contains(buf.String(), `"msg":"request failed"`) {
		t.Errorf("missing access or error line: %s", buf.String())
	}
}

func TestSVGSanitisedBeforeProbe(t *testing.T) {
	t.Parallel()
	const doc = `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">` +
		`<script>alert(1)</script><rect width="10" height="10" onload="evil()"/></svg>`
	h := newHarness(t, func(h *harness) {
		h.f.data = []byte(doc)
		h.pr.meta = SourceMeta{Format: transform.FormatSVG, Width: 10, Height: 10, Frames: 1}
	})
	rec := h.do(t, http.MethodGet, okPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body %q", rec.Code, rec.Body.String())
	}
	for name, got := range map[string][]byte{"prober": h.pr.seen, "processor": h.p.source} {
		s := string(got)
		if strings.Contains(s, "script") || strings.Contains(s, "onload") || !strings.Contains(s, "<rect") {
			t.Errorf("%s saw unsanitised svg: %s", name, s)
		}
	}
	if h.allocs.Load() != 1 {
		t.Errorf("sanitised copies allocated = %d, want 1", h.allocs.Load())
	}
	h.checkReleased(t)
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
	if h.fm.requests.Load() != 0 {
		t.Errorf("fixed routes counted as image requests: %d", h.fm.requests.Load())
	}
}

func TestPathPrefix(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(h *harness) { h.cfg.PathPrefix = "/img/" })
	if rec := h.do(t, http.MethodGet, "/img"+okPath, nil); rec.Code != http.StatusOK {
		t.Errorf("prefixed path = %d", rec.Code)
	}
}

func TestLimitOverrides(t *testing.T) {
	t.Parallel()
	s := &Server{cfg: DefaultConfig()}
	o := &transform.LimitOverrides{MaxSrcResolution: 100, MaxSrcFileSize: 1 << 20, MaxResultDimension: 100}
	got := s.limits(o)
	if got.probe.MaxPixels != 50_000_000 || got.maxSrcBytes != 1<<20 || got.maxResultDimension != 100 {
		t.Errorf("lowering only: %+v", got)
	}
	s.cfg.Limits.AllowRaising = true
	if got := s.limits(o); got.probe.MaxPixels != 100_000_000 {
		t.Errorf("raising: %+v", got)
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
	def := DefaultConfig()
	if err := def.Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
	var zero Config
	err := zero.Validate()
	if err == nil {
		t.Fatal("zero config valid")
	}
	for _, want := range []string{"request timeout", "read header timeout", "max header bytes", "preferred"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// syncBuffer is a bytes.Buffer safe for the logger's concurrent writes.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
