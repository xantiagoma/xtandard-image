package server

import (
	"context"
	"crypto/md5" //nolint:gosec // G501: the test checks the md5 hashsum imgproxy reports
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
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
	"github.com/xantiagoma/xtandard-image/internal/imageinfo"
	"github.com/xantiagoma/xtandard-image/internal/limiter"
	"github.com/xantiagoma/xtandard-image/internal/obs/admin"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

type fakeInspector struct {
	info  *SourceInfo
	err   error
	calls atomic.Int32
	// px is the pixel request of the last call; pixels answers it
	// (cannedPixels when nil).
	px     atomic.Pointer[imageinfo.Options]
	pixels *imageinfo.Result
	// noPixels ignores px (a broken inspector).
	noPixels bool
	// det is the detection request of the last call; dets answers it.
	det  atomic.Pointer[plan.DetectionNeeds]
	dets *plan.Detections
}

func (f *fakeInspector) Inspect(_ context.Context, _ fetch.Buffer, _ ProbeLimits, px *imageinfo.Options, det plan.DetectionNeeds) (*SourceInfo, error) {
	f.calls.Add(1)
	f.px.Store(px)
	f.det.Store(&det)
	if f.err != nil {
		return nil, f.err
	}
	if f.info == nil || ((!px.Any() || f.noPixels) && !det.Any()) {
		return f.info, nil
	}
	si := *f.info
	if det.Any() {
		si.Detections = f.dets
	}
	if !px.Any() || f.noPixels {
		return &si, nil
	}
	si.Pixels = f.pixels
	if si.Pixels == nil {
		si.Pixels = cannedPixels(px)
	}
	return &si, nil
}

// cannedPixels answers every field px asks for with fixed values.
func cannedPixels(px *imageinfo.Options) *imageinfo.Result {
	r := &imageinfo.Result{}
	if px.Palette > 0 {
		r.Palette = []imageinfo.Color{{R: 189, G: 178, B: 169, A: 255}, {R: 83, G: 79, B: 67, A: 255}}
	}
	if px.Average {
		r.Average = &imageinfo.Color{R: 139, G: 132, B: 121, A: 255}
	}
	if px.Dominant {
		r.Dominant = &imageinfo.DominantColors{Vibrant: &imageinfo.RGB{R: 172, G: 146, B: 83}, DarkMuted: &imageinfo.RGB{R: 75, G: 70, B: 57}}
	}
	if px.BlurhashX > 0 {
		r.Blurhash = "LLH-}fox0fRQ%Do}9as9_3%2M{S2"
	}
	if px.Thumbhash {
		r.Thumbhash = []byte{0x0b, 0xe7, 0x81, 0x0c, 0x80}
	}
	if px.PerceptualHash {
		h := uint64(0x9797306d694b4e94)
		r.PerceptualHash = &h
	}
	return r
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
			o.VideoMeta, o.Classify, o.ObjectsPos = true, true, true
		}), want: `{}`},
		{name: "detect objects without detections", set: only(func(o *transform.InfoOptions) {
			o.DetectObjects = true
		}), want: `{"objects":[]}`},
		{
			name: "palette", set: only(func(o *transform.InfoOptions) { o.Palette = 2 }),
			want: `{"palette":[{"R":189,"G":178,"B":169,"A":255},{"R":83,"G":79,"B":67,"A":255}]}`,
		},
		{
			name: "average", set: only(func(o *transform.InfoOptions) { o.Average = &transform.InfoAverage{IgnoreTransparent: true} }),
			want: `{"average":{"R":139,"G":132,"B":121,"A":255}}`,
		},
		{
			name: "dominant colours", set: only(func(o *transform.InfoOptions) { o.DominantColors = &transform.InfoDominantColors{} }),
			want: `{"dominant_colors":{"dark_muted":{"R":75,"G":70,"B":57},"vibrant":{"R":172,"G":146,"B":83}}}`,
		},
		{
			name: "blurhash", set: only(func(o *transform.InfoOptions) { o.Blurhash = &transform.InfoBlurhash{XComponents: 4, YComponents: 3} }),
			want: `{"blurhash":"LLH-}fox0fRQ%Do}9as9_3%2M{S2"}`,
		},
		{
			name: "thumbhash upper-case hex", set: only(func(o *transform.InfoOptions) { o.Thumbhash = true }),
			want: `{"thumb_hash":"0BE7810C80"}`,
		},
		{
			name: "perceptual hash", set: only(func(o *transform.InfoOptions) { o.PHash = true }),
			want: `{"perceptual_hash":"9797306d694b4e94"}`,
		},
		{
			// Hashes of jpegSource, the 9 fetched bytes.
			name: "hashsums", set: only(func(o *transform.InfoOptions) {
				o.CalcHashsums = []transform.HashAlgorithm{transform.HashSHA256, transform.HashMD5}
			}),
			want: `{"hashsums":{"md5":"` + md5Hex(jpegSource) + `","sha256":"` + sha256Hex(jpegSource) + `"}}`,
		},
		{
			// 4000x3000 stored with orientation 6: the crop is placed on
			// the upright 3000x4000 image; 1500x1000 at the centre.
			name: "crop centre", set: only(func(o *transform.InfoOptions) {
				o.Crop = &transform.Crop{Width: transform.Dim{Px: 1500}, Height: transform.Dim{Px: 1000}}
			}),
			want: `{"crop":{"left":0.25,"top":0.375,"width":0.5,"height":0.25}}`,
		},
		{
			name: "crop south-east, relative size", set: only(func(o *transform.InfoOptions) {
				o.Crop = &transform.Crop{Width: transform.Dim{Rel: 0.5}, Height: transform.Dim{Rel: 0.5}, Gravity: transform.Gravity{Type: transform.GravitySouthEast}}
			}),
			want: `{"crop":{"left":0.5,"top":0.5,"width":0.5,"height":0.5}}`,
		},
		{
			name: "crop larger than the image", set: only(func(o *transform.InfoOptions) {
				o.Crop = &transform.Crop{Width: transform.Dim{Px: 9000}, Height: transform.Dim{Px: 9000}}
			}),
			want: `{"crop":{"left":0,"top":0,"width":1,"height":1}}`,
		},
		{
			name: "crop 0:0 adds nothing", set: only(func(o *transform.InfoOptions) { o.Crop = &transform.Crop{} }),
			want: `{}`,
		},
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

func md5Hex(b []byte) string {
	s := md5.Sum(b) //nolint:gosec // G401: see the import
	return hex.EncodeToString(s[:])
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// TestInfoPixelOptions checks what the server asks the inspector for:
// nothing without pixel options (so no decode), exactly the requested
// fields otherwise, and a 500 when the inspector ignores the request.
func TestInfoPixelOptions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		set  func(*transform.InfoOptions)
		want *imageinfo.Options
	}{
		{name: "defaults decode nothing"},
		{name: "hashsums and crop decode nothing", set: func(o *transform.InfoOptions) {
			o.CalcHashsums = []transform.HashAlgorithm{transform.HashSHA1}
			o.Crop = &transform.Crop{Width: transform.Dim{Px: 10}}
		}},
		{
			name: "every pixel field",
			set: func(o *transform.InfoOptions) {
				o.Palette, o.Thumbhash, o.PHash = 8, true, true
				o.Average = &transform.InfoAverage{IgnoreTransparent: true}
				o.DominantColors = &transform.InfoDominantColors{BuildMissed: true}
				o.Blurhash = &transform.InfoBlurhash{XComponents: 5, YComponents: 2}
			},
			want: &imageinfo.Options{
				Palette: 8, Average: true, AverageIgnoreTransparent: true, Dominant: true, DominantBuildMissed: true,
				BlurhashX: 5, BlurhashY: 2, Thumbhash: true, PerceptualHash: true,
			},
		},
		{
			name: "average keeping transparent pixels",
			set:  func(o *transform.InfoOptions) { o.Average = &transform.InfoAverage{} },
			want: &imageinfo.Options{Average: true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			insp := &fakeInspector{info: photoInfo()}
			h := newInfoHarness(t, insp, func(h *harness) { h.router.mutate = infoRequest(tc.set) })
			if rec := h.do(t, http.MethodGet, infoPath, nil); rec.Code != http.StatusOK {
				t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
			}
			if diff := cmp.Diff(tc.want, insp.px.Load()); diff != "" {
				t.Errorf("pixel options (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("inspector without pixels", func(t *testing.T) {
		t.Parallel()
		insp := &fakeInspector{info: photoInfo(), noPixels: true}
		h := newInfoHarness(t, insp, func(h *harness) {
			h.router.mutate = infoRequest(func(o *transform.InfoOptions) { o.Thumbhash = true })
		})
		if rec := h.do(t, http.MethodGet, infoPath, nil); rec.Code != http.StatusInternalServerError {
			t.Errorf("status %d, want 500", rec.Code)
		}
		h.checkReleased(t)
	})
}

// TestInfoDetectObjects: detect_objects reports the inspector's detections
// in imgproxy Pro's shape (plus landmarks), filtered by class; a crop with
// object gravity is placed on them; the inspector is asked for exactly the
// models the options need.
func TestInfoDetectObjects(t *testing.T) {
	t.Parallel()
	dets := &plan.Detections{
		Faces: []plan.Detected{{
			Class: "face", Score: 0.9321, Box: transform.Area{Left: 0.1, Top: 0.2, Width: 0.1, Height: 0.15},
			Landmarks: [][2]float64{{0.12, 0.25}, {0.17, 0.25}, {0.15, 0.28}, {0.13, 0.31}, {0.17, 0.31}},
		}},
		Objects: []plan.Detected{{Class: "dog", Score: 0.87654321, Box: transform.Area{Left: 0.5, Top: 0.5, Width: 0.25, Height: 0.25}}},
	}
	face := `{"class_id":0,"class_name":"face","confidence":0.9321,"left":0.1,"top":0.2,"width":0.1,"height":0.15,` +
		`"landmarks":[[0.12,0.25],[0.17,0.25],[0.15,0.28],[0.13,0.31],[0.17,0.31]]}`
	dog := `{"class_id":16,"class_name":"dog","confidence":0.876543,"left":0.5,"top":0.5,"width":0.25,"height":0.25}`
	tests := []struct {
		name string
		set  func(*transform.InfoOptions)
		op   *transform.ObjectsPosition
		need plan.DetectionNeeds
		want string
	}{
		{
			"all", func(o *transform.InfoOptions) { o.DetectObjects = true },
			nil,
			plan.DetectionNeeds{Faces: true, Objects: true},
			`{"objects":[` + face + `,` + dog + `]}`,
		},
		{
			"faces only", func(o *transform.InfoOptions) { o.DetectObjects, o.DetectClasses = true, []string{"faces"} },
			nil,
			plan.DetectionNeeds{Faces: true},
			`{"objects":[` + face + `]}`,
		},
		{
			"animals", func(o *transform.InfoOptions) { o.DetectObjects, o.DetectClasses = true, []string{"animal"} },
			nil,
			plan.DetectionNeeds{Objects: true},
			`{"objects":[` + dog + `]}`,
		},
		// Upright 3000x4000: the dog's centre is at 1875, 2500; a centred
		// 1000x1000 crop starts at 1375, 2000.
		{"crop on the dog", func(o *transform.InfoOptions) {
			o.Crop = &transform.Crop{
				Width: transform.Dim{Px: 1000}, Height: transform.Dim{Px: 1000},
				Gravity: transform.Gravity{Type: transform.GravityObject, Classes: []string{"dog"}},
			}
		}, nil, plan.DetectionNeeds{Objects: true}, `{"crop":{"left":0.458333,"top":0.5,"width":0.333333,"height":0.25}}`},
		{
			"crop on the dog, north-west", func(o *transform.InfoOptions) {
				o.Crop = &transform.Crop{
					Width: transform.Dim{Px: 1000}, Height: transform.Dim{Px: 1000},
					Gravity: transform.Gravity{Type: transform.GravityObject, Classes: []string{"dog"}},
				}
			}, &transform.ObjectsPosition{Type: transform.GravityNorthWest},
			plan.DetectionNeeds{Objects: true},
			`{"crop":{"left":0.5,"top":0.5,"width":0.333333,"height":0.25}}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			insp := &fakeInspector{info: photoInfo(), dets: dets}
			h := newInfoHarness(t, insp, func(h *harness) {
				h.router.mutate = func(r *transform.Request) {
					infoRequest(func(o *transform.InfoOptions) { *o = transform.InfoOptions{}; tc.set(o) })(r)
					r.Pipelines[0].Gravity.ObjectsPosition = tc.op
				}
			})
			rec := h.do(t, http.MethodGet, infoPath, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
			}
			if diff := cmp.Diff(canonicalJSON(t, tc.want), canonicalJSON(t, roundJSON(t, rec.Body.String()))); diff != "" {
				t.Errorf("body (-want +got):\n%s", diff)
			}
			if got := *insp.det.Load(); got != tc.need {
				t.Errorf("inspector asked for %+v, want %+v", got, tc.need)
			}
			h.checkReleased(t)
		})
	}
}

// roundJSON rounds the numbers of a JSON document to 6 decimals.
func roundJSON(t *testing.T, s string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	var walk func(v any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case float64:
			return math.Round(x*1e6) / 1e6
		case []any:
			for i := range x {
				x[i] = walk(x[i])
			}
		case map[string]any:
			for k := range x {
				x[k] = walk(x[k])
			}
		}
		return v
	}
	b, err := json.Marshal(walk(v))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
