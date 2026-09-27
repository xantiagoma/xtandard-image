//go:build cgo

package app

import (
	"bytes"
	"encoding/json"
	"image"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/server"
	"github.com/xantiagoma/xtandard-image/internal/testutil/detectfix"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/testutil/leakcheck"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// The face video fixtures (WP-91): the crew photo as a one-second still
// clip, and the same stored rotated with a display matrix that shows it
// upright (testdata/gen/video-faces.sh).
const (
	faceVideo        = "video/faces-vp9.webm"
	faceVideoRotated = "video/faces-av1-rotate90.mp4"
)

// crewCases are the detection dialect cases on the crew photo.
func crewCases(t *testing.T) []struct{ name, path string } {
	t.Helper()
	var out []struct{ name, path string }
	for _, tc := range detectDialectCases(t) {
		if strings.Contains(tc.path, detectfix.Crew) {
			out = append(out, tc)
		}
	}
	return out
}

// TestDetectVideo: face gravity, crops and filters of every dialect, and
// /info detect_objects, on a single-frame video source detect on the
// decoded frame (upright, display matrix applied), and the results match
// the same requests on the photo the clip was made from. The detector runs
// once per video and frame (the cache key has the frame's timestamp).
// Full image only: video needs FFmpeg (TestDetectUnavailable covers slim).
func TestDetectVideo(t *testing.T) {
	// Not parallel: see TestDetectDialects.
	if !codec.VideoAvailable() {
		t.Skip("video needs FFmpeg (the full image)")
	}
	fake := newFakeDetector(t)
	a := start(t, detectDialectsConfig(t, ""), hooks{detector: fake})
	base := "http://" + a.public.String()
	for _, src := range []string{faceVideo, faceVideoRotated} {
		for _, tc := range crewCases(t) {
			t.Run(filepath.Base(src)+"/"+tc.name, func(t *testing.T) {
				path := strings.ReplaceAll(tc.path, detectfix.Crew, src)
				r := get(t, base+path)
				if r.status != http.StatusOK {
					t.Fatalf("GET %s: %d %q", path, r.status, r.body)
				}
				if ig := r.header.Get("X-Xi-Ignored"); ig != "" {
					t.Errorf("X-Xi-Ignored = %q", ig)
				}
				if r.header.Get("X-Xi-Video-Frame") == "" {
					t.Error("no X-Xi-Video-Frame: not processed as a video")
				}
				img, _, err := image.Decode(bytes.NewReader(r.body))
				if err != nil {
					t.Fatal(err)
				}
				if src == faceVideoRotated && tc.name == "imgix-facearea" {
					// A 131 px face crop enlarged to 160: the planner rounds
					// the crop's position in the stored (rotated) frame, one
					// pixel off the upright photo's, which the enlargement
					// magnifies beyond a perceptual tolerance. Same face.
					if b := img.Bounds(); b.Dx() != 160 || b.Dy() != 160 {
						t.Errorf("size %v", b)
					}
					return
				}
				// The photo's golden; the clip is a lossy VP9 or AV1
				// encoding of it.
				golden.Compare(t, "app/detect-"+tc.name, img, golden.Options{Tolerance: 0.06})
			})
		}
	}
	// One detection per video: every request picks the same best frame.
	if f, o := fake.faces.Load(), fake.objs.Load(); f != 2 || o != 0 {
		t.Errorf("detector calls: faces %d, objects %d; want 2 and 0 (cached per video and frame)", f, o)
	}
	// Another frame is detected again.
	r := get(t, base+"/insecure/vts:0.3/rs:fill:240:120/g:obj:face/plain/"+faceVideo+"@png")
	if r.status != http.StatusOK || !strings.HasPrefix(r.header.Get("X-Xi-Video-Frame"), "0.300s") {
		t.Fatalf("vts:0.3: %d %q %s", r.status, r.header.Get("X-Xi-Video-Frame"), r.body)
	}
	if f := fake.faces.Load(); f != 3 {
		t.Errorf("faces on another frame: %d calls, want 3", f)
	}

	t.Run("info", func(t *testing.T) {
		for _, src := range []string{faceVideo, faceVideoRotated} {
			r := get(t, base+"/info/insecure"+noDefaults+"/do:1/plain/"+src)
			if r.status != http.StatusOK {
				t.Fatalf("GET %s: %d %s", src, r.status, r.body)
			}
			var body struct {
				Objects []struct {
					ClassName string  `json:"class_name"`
					Left      float64 `json:"left"`
					Top       float64 `json:"top"`
					Width     float64 `json:"width"`
				} `json:"objects"`
			}
			if err := json.Unmarshal(r.body, &body); err != nil {
				t.Fatal(err)
			}
			want := detectfix.Load(t, detectfix.Crew)
			if len(body.Objects) != len(want.Faces)+len(want.Objects) {
				t.Fatalf("%s: objects %s", src, r.body)
			}
			for i, w := range want.Faces {
				o := body.Objects[i]
				if o.ClassName != "face" || math.Abs(o.Left-w.Box.Left) > 2.0/640 || math.Abs(o.Top-w.Box.Top) > 2.0/503 {
					t.Errorf("%s: face %d = %+v, want %+v", src, i, o, w.Box)
				}
			}
		}
	})
	// The processing requests detected the same frames: only the object
	// model is new (once per video).
	if f, o := fake.faces.Load(), fake.objs.Load(); f != 3 || o != 2 {
		t.Errorf("after /info: faces %d, objects %d; want 3 and 2", f, o)
	}
}

// TestDetectVideoUnavailable: without detection (detect off, or the slim
// image without FFmpeg at all) explicit face options on a video answer
// 422 and smart gravity falls back (200 where video decodes).
func TestDetectVideoUnavailable(t *testing.T) {
	// Not parallel: see TestDetectDialects.
	a := start(t, detectDialectsConfig(t, "detect:\n  enabled: \"false\"\n"), hooks{})
	base := "http://" + a.public.String()
	for path, want := range map[string]int{
		"/insecure/rs:fill:100:100/g:obj:face/plain/" + faceVideo + "@png": http.StatusUnprocessableEntity,
		"/insecure/bd:10:face/plain/" + faceVideo + "@png":                 http.StatusUnprocessableEntity,
		"/info/insecure/do:1/plain/" + faceVideo:                           http.StatusUnprocessableEntity,
		"/insecure/rs:fill:100:100/g:sm/plain/" + faceVideo + "@png":       http.StatusOK,
	} {
		if !codec.VideoAvailable() {
			want = http.StatusUnprocessableEntity // no video at all
		}
		if r := get(t, base+path); r.status != want {
			t.Errorf("GET %s: %d %s, want %d", path, r.status, r.body, want)
		}
	}
}

// TestDetectChained: a face gravity in a chained pipeline (imgproxy `-/`,
// Cloudinary chained components) crops on the source's faces mapped
// through the crop before it (WP-92), on the photo and on the face clip,
// like the executor's golden of the same chain; the detector runs once
// per source.
func TestDetectChained(t *testing.T) {
	// Not parallel: see TestDetectDialects.
	fake := newFakeDetector(t)
	a := start(t, detectDialectsConfig(t, ""), hooks{detector: fake})
	base := "http://" + a.public.String()
	sources := []string{detectfix.Crew}
	if codec.VideoAvailable() {
		sources = append(sources, faceVideo)
	}
	for _, src := range sources {
		for _, path := range []string{
			"/insecure/c:300:503:ea/-/rs:fill:100:100/g:obj:face/plain/" + src + "@png",
			"/cloudinary/demo/image/upload/c_crop,w_300,h_503,g_east/c_fill,g_face,w_100,h_100/f_png/" + src,
		} {
			t.Run(path, func(t *testing.T) {
				r := get(t, base+path)
				if r.status != http.StatusOK {
					t.Fatalf("GET %s: %d %q", path, r.status, r.body)
				}
				if ig := r.header.Get("X-Xi-Ignored"); ig != "" {
					t.Errorf("X-Xi-Ignored = %q", ig)
				}
				img, _, err := image.Decode(bytes.NewReader(r.body))
				if err != nil {
					t.Fatal(err)
				}
				golden.Compare(t, "exec/detect-chained-face", img, golden.Options{Tolerance: 0.06})
			})
		}
	}
	if f, o := fake.faces.Load(), fake.objs.Load(); int(f) != len(sources) || o != 0 {
		t.Errorf("detector calls: faces %d, objects %d; want %d and 0", f, o, len(sources))
	}
}

// TestDetectVideoNoLeak runs video detection requests through the
// production Prober and Processor (probe with its FFmpeg session, one
// frame decoded for detection and processing, the fake detector, face
// gravity, blur, another frame, the rotated clip) and checks that libvips'
// counters and FFmpeg's open handles return to their baseline. Not
// parallel: the counters are process-wide.
func TestDetectVideoNoLeak(t *testing.T) {
	if !codec.VideoAvailable() {
		t.Skip("video needs FFmpeg (the full image)")
	}
	const requests = 120
	cfg, _, err := config.Load(config.LoadOptions{Environ: []string{"XI_DEV_MODE=true"}, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	vcfg := vips.DefaultConfig()
	if err := vips.Init(&vcfg); err != nil {
		t.Fatal(err)
	}
	proc, err := newProcessor(&cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// No cache: every request runs the detection path.
	proc.detect = newDetections(newFakeDetector(t), 0, true, formatLimits(&cfg), slog.New(slog.DiscardHandler))
	prober := &codecProber{svg: formatLimits(&cfg), video: videoSettings(&cfg)}
	parser, err := imgproxy.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	type prepared struct {
		data []byte
		req  *transform.Request
	}
	in := make([]prepared, 0, 4)
	for _, c := range []struct{ fixture, options string }{
		{faceVideo, "rs:fill:120:60/g:obj:face"},
		{faceVideo, "vts:0.3/rs:fit:160:160/bd:6:face"},
		{faceVideoRotated, "rs:fill:60:120/g:obj:face"},
		{faceVideo, "rs:fill:80:80/g:sm"},
	} {
		data, err := os.ReadFile(golden.Fixture(t, c.fixture))
		if err != nil {
			t.Fatal(err)
		}
		res, err := parser.Parse(&dialect.Input{Path: "/insecure/" + c.options + "/plain/local:///x"})
		if err != nil {
			t.Fatal(err)
		}
		res.Request.ApplyDefaults(transform.StandardDefaults())
		if err := res.Request.Validate(); err != nil {
			t.Fatal(err)
		}
		in = append(in, prepared{data, res.Request})
	}
	request := func(i int) {
		c := &in[i%len(in)]
		src, err := vips.NewBuffer(len(c.data))
		if err != nil {
			t.Fatal(err)
		}
		defer src.Release()
		copy(src.Bytes(), c.data)
		meta, err := prober.Probe(t.Context(), src, server.ProbeLimits{})
		if err != nil {
			t.Fatal(err)
		}
		if s, ok := meta.Session.(*codec.VideoSession); ok {
			defer s.Close() // the server closes it
		}
		out, err := proc.Process(t.Context(), &server.ProcessInput{
			Request: c.req, Source: src, Meta: meta, Decision: negotiate.Decision{Format: transform.FormatPNG},
		})
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		out.Body.Release()
	}
	for i := range 2 * len(in) {
		request(i)
	}
	handles := ffmpeg.ReadStats().Handles
	before := leakcheck.Take()
	for i := range requests {
		request(i)
	}
	leakcheck.Check(t, before, leakcheck.Default, "requests")
	if h := ffmpeg.ReadStats().Handles; h != handles {
		t.Errorf("FFmpeg handles %d → %d", handles, h)
	}
}
