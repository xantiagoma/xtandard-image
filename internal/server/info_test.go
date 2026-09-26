package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/limiter"
	"github.com/xantiagoma/xtandard-image/internal/obs/admin"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

type fakeInspector struct {
	info  *SourceInfo
	err   error
	calls atomic.Int32
}

func (f *fakeInspector) Inspect(_ context.Context, _ fetch.Buffer, _ ProbeLimits) (*SourceInfo, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	return f.info, nil
}

// photoInfo is a 4000x3000 JPEG stored rotated (orientation 6) with EXIF
// in IFD0, the EXIF IFD and the thumbnail IFD, IPTC with Photoshop
// resolution, and XMP.
func photoInfo() *SourceInfo {
	return &SourceInfo{
		Meta:     SourceMeta{Format: transform.FormatJPEG, Width: 4000, Height: 3000, Frames: 1, Orientation: 6},
		Format:   "jpeg",
		MIMEType: "image/jpeg",
		Pages:    1, Bands: 3, SampleFormat: "uchar", Colorspace: "srgb",
		EXIF: []EXIFEntry{
			{Name: "Model", Title: "Model", Value: "NIKON D810"},
			{Name: "XResolution", Title: "X-Resolution", Value: "300"},
			{Name: "DateTimeOriginal", Title: "Date and Time (Original)", Value: "2016:09:11 22:15:03"},
			{Name: "XResolution", Title: "X-Resolution", Value: "72"}, // thumbnail IFD: IFD0 wins
			{Name: "Vendor", Value: "raw"},                            // no title: canonical name
		},
		IPTC:      map[string]any{"Copyright Notice": "Daily Bugle", "Keywords": []any{"a", "b"}},
		Photoshop: map[string]any{"resolution": map[string]any{"XResolution": 240}},
		XMP:       map[string]map[string]any{"dc": {"format": "image/jpeg"}},
	}
}

// infoRequest turns the fake router's request into an info request with
// imgproxy's default options changed by set.
func infoRequest(set func(*transform.InfoOptions)) func(*transform.Request) {
	return func(r *transform.Request) {
		r.Kind = transform.KindInfo
		r.Pipelines = make([]transform.Pipeline, 1)
		r.Info = &transform.InfoOptions{Size: true, Format: true, Dimensions: true, EXIF: true, IPTC: true, XMP: true, VideoMeta: true}
		if set != nil {
			set(r.Info)
		}
	}
}

// newInfoHarness is newHarness with an info request and the inspector
// wired in.
func newInfoHarness(t *testing.T, insp *fakeInspector, mutate func(*harness)) *harness {
	t.Helper()
	h := newHarness(t, func(h *harness) {
		h.router.mutate = infoRequest(nil)
		if mutate != nil {
			mutate(h)
		}
	})
	var in Inspector
	if insp != nil {
		in = insp
	}
	srv, err := New(&h.cfg, &Deps{
		Router: h.router, Resolver: h.res, Fetcher: h.f, Admitter: h.a, Prober: h.pr, Processor: h.p, Sniffer: fakeSniffer{},
		Inspector: in,
		Allocator: func(n int) (fetch.Buffer, error) {
			h.allocs.Add(1)
			return fetch.NewGoBuffer(n, func() { h.allocFr.Add(1) }), nil
		},
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

const infoPath = "/info/insecure/plain/http://origin/a.jpg"

// TestInfoOptionFlags checks that every info option includes or excludes
// exactly its imgproxy fields.
func TestInfoOptionFlags(t *testing.T) {
	t.Parallel()
	none := func(o *transform.InfoOptions) { *o = transform.InfoOptions{} }
	only := func(set func(*transform.InfoOptions)) func(*transform.InfoOptions) {
		return func(o *transform.InfoOptions) { none(o); set(o) }
	}
	bare := func(si *SourceInfo) { si.EXIF, si.IPTC, si.Photoshop, si.XMP = nil, nil, nil, nil }
	tests := []struct {
		name   string
		set    func(*transform.InfoOptions)
		source func(*SourceInfo)
		want   string
	}{
		{name: "imgproxy defaults", want: `{"size":9,"format":"jpeg","mime_type":"image/jpeg","width":3000,"height":4000,"orientation":6,` +
			`"exif":{"Date and Time (Original)":"2016:09:11 22:15:03","Model":"NIKON D810","Vendor":"raw","X-Resolution":"300"},` +
			`"iptc":{"Copyright Notice":"Daily Bugle","Keywords":["a","b"]},"photoshop":{"resolution":{"XResolution":240}},` +
			`"xmp":{"dc":{"format":"image/jpeg"}}}`},
		{name: "everything off", set: none, want: `{}`},
		{name: "size", set: only(func(o *transform.InfoOptions) { o.Size = true }), want: `{"size":9}`},
		{name: "format", set: only(func(o *transform.InfoOptions) { o.Format = true }), want: `{"format":"jpeg","mime_type":"image/jpeg"}`},
		{name: "dimensions oriented", set: only(func(o *transform.InfoOptions) { o.Dimensions = true }), want: `{"width":3000,"height":4000,"orientation":6}`},
		{
			name: "dimensions upright", set: only(func(o *transform.InfoOptions) { o.Dimensions = true }),
			source: func(si *SourceInfo) { si.Meta.Orientation = 0 }, want: `{"width":4000,"height":3000,"orientation":1}`,
		},
		{
			name: "exif", set: only(func(o *transform.InfoOptions) { o.EXIF = true }),
			want: `{"exif":{"Date and Time (Original)":"2016:09:11 22:15:03","Model":"NIKON D810","Vendor":"raw","X-Resolution":"300"}}`,
		},
		{
			name: "iptc", set: only(func(o *transform.InfoOptions) { o.IPTC = true }),
			want: `{"iptc":{"Copyright Notice":"Daily Bugle","Keywords":["a","b"]},"photoshop":{"resolution":{"XResolution":240}}}`,
		},
		{name: "xmp", set: only(func(o *transform.InfoOptions) { o.XMP = true }), want: `{"xmp":{"dc":{"format":"image/jpeg"}}}`},
		{
			name: "requested metadata absent", source: bare,
			want: `{"size":9,"format":"jpeg","mime_type":"image/jpeg","width":3000,"height":4000,"orientation":6,"exif":{},"iptc":{},"xmp":{}}`,
		},
		{name: "header options", set: only(func(o *transform.InfoOptions) {
			o.Colorspace, o.Bands, o.SampleFormat, o.PagesNumber, o.Alpha = true, true, true, true, true
		}), want: `{"colorspace":"srgb","bands":3,"sample_format":"uchar","pages_number":1,"alpha":false}`},
		{
			name: "animation frames", set: only(func(o *transform.InfoOptions) { o.PagesNumber, o.Alpha = true, true }),
			source: func(si *SourceInfo) {
				si.Meta = SourceMeta{Format: transform.FormatGIF, Width: 400, Height: 300, Frames: 6, IsAnimated: true, HasAlpha: true}
				si.Pages = 6
			},
			want: `{"pages_number":6,"alpha":true}`,
		},
		{name: "not implemented options add nothing", set: only(func(o *transform.InfoOptions) {
			o.VideoMeta, o.DetectObjects, o.Classify, o.Thumbhash, o.PHash, o.ObjectsPos = true, true, true, true, true, true
			o.Palette, o.Average, o.Blurhash = 16, &transform.InfoAverage{}, &transform.InfoBlurhash{XComponents: 4, YComponents: 3}
			o.DominantColors, o.Crop = &transform.InfoDominantColors{}, &transform.Crop{Width: transform.Dim{Px: 10}, Height: transform.Dim{Px: 10}}
			o.CalcHashsums = []transform.HashAlgorithm{transform.HashSHA256}
		}), want: `{}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			si := photoInfo()
			if tc.source != nil {
				tc.source(si)
			}
			h := newInfoHarness(t, &fakeInspector{info: si}, func(h *harness) { h.router.mutate = infoRequest(tc.set) })
			rec := h.do(t, http.MethodGet, infoPath, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
			}
			if diff := cmp.Diff(canonicalJSON(t, tc.want), canonicalJSON(t, rec.Body.String())); diff != "" {
				t.Errorf("body (-want +got):\n%s", diff)
			}
			h.checkReleased(t)
		})
	}
}

// canonicalJSON re-encodes s so key order does not matter.
func canonicalJSON(t *testing.T, s string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("invalid JSON %q: %v", s, err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInfoResponse(t *testing.T) {
	t.Parallel()
	insp := &fakeInspector{info: photoInfo()}
	h := newInfoHarness(t, insp, func(h *harness) {
		h.cfg.Info = InfoConfig{CacheTTL: time.Hour, DebugHeaders: true}
		h.router.mutate = func(r *transform.Request) {
			infoRequest(func(o *transform.InfoOptions) { o.Blurhash = &transform.InfoBlurhash{XComponents: 4, YComponents: 3} })(r)
			r.Ignored = []transform.Ignored{{Option: "bh", Reason: "takes effect in M2"}, {Option: "do", Reason: "takes effect in M3"}}
		}
	})

	rec := h.do(t, http.MethodGet, infoPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
	etag := rec.Header().Get("ETag")
	for k, want := range map[string]string{
		"Content-Type":           "application/json",
		"Cache-Control":          "max-age=3600, public",
		"X-Content-Type-Options": "nosniff",
		"X-Xi-Ignored":           "bh (takes effect in M2), do (takes effect in M3)",
		"Content-Length":         strconv.Itoa(rec.Body.Len()),
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if !strings.HasPrefix(etag, `"xi1-`) {
		t.Errorf("ETag = %q", etag)
	}
	want := []string{"route", "normalize", "resolve", "fetch", "admission", "probe", "respond"}
	if diff := cmp.Diff(want, h.fm.stages); diff != "" {
		t.Errorf("stages (-want +got):\n%s", diff)
	}
	if h.p.calls != 0 || h.a.acquired.Load() != 1 {
		t.Errorf("processor calls %d, admissions %d; want 0 and 1", h.p.calls, h.a.acquired.Load())
	}

	// Stable ETag, 304 on If-None-Match (strong and weak), HEAD.
	if again := h.do(t, http.MethodGet, infoPath, nil).Header().Get("ETag"); again != etag {
		t.Errorf("ETag not stable: %q then %q", etag, again)
	}
	for _, inm := range []string{etag, "W/" + etag, `"other", ` + etag, "*"} {
		if rec := h.do(t, http.MethodGet, infoPath, http.Header{"If-None-Match": {inm}}); rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
			t.Errorf("If-None-Match %s: status %d, %d bytes", inm, rec.Code, rec.Body.Len())
		}
	}
	if rec := h.do(t, http.MethodGet, infoPath, http.Header{"If-None-Match": {`"other"`}}); rec.Code != http.StatusOK {
		t.Errorf("non-matching If-None-Match: status %d", rec.Code)
	}
	if rec := h.do(t, http.MethodHead, infoPath, nil); rec.Code != http.StatusOK || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") == "" {
		t.Errorf("HEAD: status %d, %d bytes, Content-Length %q", rec.Code, rec.Body.Len(), rec.Header().Get("Content-Length"))
	}

	// A new source validator, or different options, change the ETag.
	h.f.obj.ETag = `"v2"`
	if changed := h.do(t, http.MethodGet, infoPath, nil).Header().Get("ETag"); changed == etag {
		t.Error("ETag did not change with the source ETag")
	}
	h.checkReleased(t)

	// The ETag differs from the processing ETag of the same source and
	// differs between option sets.
	other := newInfoHarness(t, insp, func(h *harness) { h.router.mutate = infoRequest(func(o *transform.InfoOptions) { o.EXIF = false }) })
	if got := other.do(t, http.MethodGet, infoPath, nil).Header().Get("ETag"); got == etag {
		t.Error("different info options share an ETag")
	}
}

func TestInfoDebugHeaderOff(t *testing.T) {
	t.Parallel()
	h := newInfoHarness(t, &fakeInspector{info: photoInfo()}, func(h *harness) {
		h.cfg.Info = InfoConfig{}
		h.router.mutate = func(r *transform.Request) {
			infoRequest(nil)(r)
			r.Ignored = []transform.Ignored{{Option: "bh", Reason: "takes effect in M2"}}
		}
	})
	rec := h.do(t, http.MethodGet, infoPath, nil)
	if rec.Code != http.StatusOK || rec.Header().Get("X-Xi-Ignored") != "" || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("status %d, X-Xi-Ignored %q, Cache-Control %q", rec.Code, rec.Header().Get("X-Xi-Ignored"), rec.Header().Get("Cache-Control"))
	}
}

// TestInfoErrors maps failures of an info request to the same statuses as
// processing requests.
func TestInfoErrors(t *testing.T) {
	t.Parallel()
	route := func(err error) func(*harness) { return func(h *harness) { h.router.err = err } }
	fetchErr := func(err error) func(*harness) { return func(h *harness) { h.f.err = err } }
	inspErr := func(err error) *fakeInspector { return &fakeInspector{err: err} }
	withMeta := func(m SourceMeta) *fakeInspector {
		si := photoInfo()
		si.Meta = m
		return &fakeInspector{info: si}
	}
	tests := []struct {
		name       string
		insp       *fakeInspector // defaults to one answering photoInfo()
		noInsp     bool
		mutate     func(*harness)
		wantStatus int
		wantBody   string
	}{
		{name: "no inspector", noInsp: true, wantStatus: 501},
		{name: "bad signature", mutate: route(errs.New(errs.KindUnauthorized, "sign: mismatch")), wantStatus: 403, wantBody: "invalid signature\n"},
		{name: "expired", mutate: route(dialect.Expired()), wantStatus: 404},
		{name: "unsigned not allowed", mutate: route(&errs.Error{Kind: errs.KindForbidden, Public: "unsigned request not allowed"}), wantStatus: 403},
		{name: "malformed", mutate: route(dialect.InvalidURL("missing source")), wantStatus: 400},
		{name: "compat 404", mutate: func(h *harness) { h.cfg.CompatImgproxyStatus = true; h.router.err = dialect.InvalidURL("x") }, wantStatus: 404},
		{name: "validation", mutate: func(h *harness) {
			h.router.mutate = infoRequest(func(o *transform.InfoOptions) { o.Palette = 1000 })
		}, wantStatus: 400},
		{name: "source not allowed", mutate: func(h *harness) { h.res.err = errs.New(errs.KindForbidden, "no match 10.0.0.1") }, wantStatus: 403, wantBody: "forbidden\n"},
		{name: "origin 404", mutate: fetchErr(errs.New(errs.KindNotFound, "origin 404")), wantStatus: 404},
		{name: "origin error", mutate: fetchErr(errs.New(errs.KindUpstreamError, "refused")), wantStatus: 502},
		{name: "origin timeout", mutate: fetchErr(errs.New(errs.KindUpstreamTimeout, "slow")), wantStatus: 504},
		{name: "source too large", mutate: fetchErr(errs.New(errs.KindSourceTooLarge, "big")), wantStatus: 422},
		{name: "queue full", mutate: func(h *harness) {
			h.a.err = &limiter.OverloadError{Reason: limiter.ReasonQueueFull, RetryAfter: time.Second, Err: errs.New(errs.KindOverloaded, "full")}
		}, wantStatus: 429},
		{name: "unsupported", insp: inspErr(errs.New(errs.KindUnsupported, "no loader")), wantStatus: 422, wantBody: "unsupported image format\n"},
		{name: "corrupt", insp: inspErr(errs.New(errs.KindCorrupt, "bad header")), wantStatus: 422},
		{name: "inspector limit", insp: inspErr(errs.New(errs.KindLimitExceeded, "pixels")), wantStatus: 422},
		{name: "internal hides text", insp: inspErr(errs.New(errs.KindInternal, "secret detail")), wantStatus: 500, wantBody: "internal server error\n"},
		{name: "pixel limit rechecked", insp: withMeta(SourceMeta{Format: transform.FormatJPEG, Width: 10000, Height: 10000, Frames: 1}), wantStatus: 422},
		{name: "svg missed by sniffer", insp: withMeta(SourceMeta{Format: transform.FormatSVG, Width: 10, Height: 10, Frames: 1}), wantStatus: 422},
		{name: "svg malformed", mutate: func(h *harness) { h.f.data = []byte(`<svg xmlns="http://www.w3.org/2000/svg"><g>`) }, wantStatus: 422},
		{name: "nil info", insp: &fakeInspector{}, wantStatus: 500},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			insp := tc.insp
			if insp == nil && !tc.noInsp {
				insp = &fakeInspector{info: photoInfo()}
			}
			h := newInfoHarness(t, insp, tc.mutate)
			rec := h.do(t, http.MethodGet, infoPath, nil)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status %d, want %d (body %q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantBody != "" && rec.Body.String() != tc.wantBody {
				t.Errorf("body %q, want %q", rec.Body.String(), tc.wantBody)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
				t.Errorf("error Content-Type %q", ct)
			}
			if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "10.0.0.1") {
				t.Errorf("internal text leaked: %q", rec.Body.String())
			}
			h.checkReleased(t)
		})
	}
}

func TestIgnoredHeaderBounded(t *testing.T) {
	t.Parallel()
	list := make([]transform.Ignored, 200)
	for i := range list {
		list[i] = transform.Ignored{Option: strings.Repeat("x", 20), Reason: "unknown option"}
	}
	if got := ignoredHeader(list); len(got) != maxIgnoredHeader {
		t.Errorf("len = %d, want %d", len(got), maxIgnoredHeader)
	}
}
