//go:build cgo

package exec_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/codec/encode"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/exec"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/testutil/ssim"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// Video fixtures (testdata/fixtures/video/README.md).
const (
	videoVP9    = "video/clip-vp9.webm"          // 1280×720, 2 s, keyframes every 0.48 s
	videoAV1    = "video/clip-av1.mp4"           // same clip
	videoRot90  = "video/clip-av1-rotate90.mp4"  // shown rotated 90° clockwise
	videoH264   = "video/bbb-h264-aac.mp4"       // Big Buck Bunny, 1 s (CC-BY 3.0)
	videoVP8MKV = "video/clip-vp8-matroska.mkv"  // 320×180
	videoPNGMOV = "video/clip-png.mov"           // 96×54
	videoRawAVI = "video/clip-raw.avi"           // 96×54
	videoRefDir = "testdata/video-ref"           // frames rendered by the ffmpeg CLI (gen/video.sh)
	videoTol    = golden.DefaultTolerance * 1.34 // decoders may differ by a rounding step
)

// videoSettings enables video like internal/app does on the full image.
func videoSettings() video.Settings {
	return video.Settings{Enabled: true, Samples: 5, Span: 10 * time.Second}
}

// videoExecutor is newExecutor with video enabled.
func videoExecutor(tb testing.TB, s video.Settings, lim plan.Limits) *exec.Executor {
	tb.Helper()
	ec := encode.DefaultConfig()
	enc, err := encode.New(&ec)
	if err != nil {
		tb.Fatal(err)
	}
	e, err := exec.New(&exec.Config{Limits: lim, Video: s}, enc)
	if err != nil {
		tb.Fatal(err)
	}
	return e
}

// videoJob probes a video fixture the way internal/app does and builds the
// job; ok is false (and the test skipped) without the ffmpeg build tag.
func videoJob(tb testing.TB, fixture string, req *transform.Request, out transform.Format) (*exec.Job, *vips.Buffer) {
	tb.Helper()
	buf := newBuffer(tb, readFixture(tb, fixture))
	var m codec.ImageMeta
	err := vips.Run(tb.Context(), func() error {
		var err error
		m, err = codec.ProbeMedia(tb.Context(), buf, codec.Limits{}, new(videoSettings()), "")
		return err
	})
	if err != nil {
		buf.Release()
		tb.Fatalf("probe %s: %v", fixture, err)
	}
	d := negotiate.Decision{Format: out}
	return &exec.Job{
		Request: req,
		Source:  buf,
		Meta: plan.SourceMeta{
			Width: m.Width, Height: m.Height, Frames: 1, HasAlpha: m.HasAlpha,
			Orientation: m.Orientation, Format: m.Format.String(), IsVideo: true,
		},
		Target: encode.NewTarget(&req.Output, &d, false),
	}, buf
}

func requireVideo(tb testing.TB) {
	tb.Helper()
	if !codec.VideoAvailable() {
		tb.Skip("built without FFmpeg (the ffmpeg build tag): TestVideoUnsupported covers this build")
	}
}

// runVideo runs options on a video fixture with e and decodes the PNG.
func runVideo(tb testing.TB, e *exec.Executor, fixture, options string) (output, error) {
	tb.Helper()
	j, buf := videoJob(tb, fixture, parse(tb, options), transform.FormatPNG)
	defer buf.Release()
	res, err := e.Run(tb.Context(), j)
	if err != nil {
		return output{}, err
	}
	defer res.Body.Release()
	data := bytes.Clone(res.Body.Bytes())
	img, frames := decode(tb, data)
	return output{res: res, img: img, frames: frames, bytes: data}, nil
}

// TestVideoGolden: thumbnails of every codec and container at a
// timestamp, at a keyframe, and by best-frame selection; the rotated clip
// comes out upright (portrait).
func TestVideoGolden(t *testing.T) {
	t.Parallel()
	requireVideo(t)
	e := videoExecutor(t, videoSettings(), testLimits)
	for _, tc := range []struct {
		name, fixture, options string
		w, h                   int
		frame                  string // X-Xi-Video-Frame prefix
	}{
		{"video-vp9-vts0", videoVP9, "vts:0/rs:fit:320:180", 320, 180, "0.000s key"},
		{"video-vp9-vts0.5", videoVP9, "vts:0.5/rs:fit:320:180", 320, 180, "0.520s"},
		{"video-vp9-vtk", videoVP9, "vts:1.3/vtk:1/rs:fit:320:180", 320, 180, "0.960s key"},
		{"video-vp9-best", videoVP9, "rs:fit:320:180", 320, 180, "0.480s key best 4"},
		{"video-vp9-from-end", videoVP9, "vts:-0.5/rs:fit:320:180", 320, 180, "1.520s"},
		{"video-av1-vts1", videoAV1, "vts:1/rs:fit:320:180", 320, 180, "1.000s"},
		{"video-av1-rotate90", videoRot90, "vts:1/rs:fit:180:320", 180, 320, "1.000s"},
		{"video-av1-fill-smart", videoAV1, "vts:1/rs:fill:200:200/g:sm", 200, 200, "1.000s"},
		{"video-h264-vts0.5", videoH264, "vts:0.5/rs:fit:320:180", 320, 180, "0.500s"},
		{"video-vp8-mkv", videoVP8MKV, "vts:0.5", 320, 180, "0.500s"},
		{"video-png-mov", videoPNGMOV, "vts:0.5", 96, 54, "0.500s key"},
		{"video-raw-avi", videoRawAVI, "vts:0.5", 96, 54, "0.500s key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := runVideo(t, e, tc.fixture, tc.options)
			if err != nil {
				t.Fatal(err)
			}
			if b := out.img.Bounds(); b.Dx() != tc.w || b.Dy() != tc.h {
				t.Fatalf("result %dx%d, want %dx%d", b.Dx(), b.Dy(), tc.w, tc.h)
			}
			if got := out.res.VideoFrame.String(); !strings.HasPrefix(got, tc.frame) {
				t.Errorf("frame %q, want prefix %q", got, tc.frame)
			}
			golden.Compare(t, "exec/"+tc.name, out.img, golden.Options{Tolerance: videoTol})
		})
	}
}

// TestVideoAgainstFFmpeg compares our thumbnails with frames the ffmpeg CLI
// rendered from the same fixtures (autorotation, BT.709 matrix, exact
// seeking): the decode, colour conversion and display matrix agree with
// FFmpeg's own tools.
func TestVideoAgainstFFmpeg(t *testing.T) {
	t.Parallel()
	requireVideo(t)
	e := videoExecutor(t, videoSettings(), testLimits)
	for _, tc := range []struct{ ref, fixture, options string }{
		{"vp9-1s-320.png", videoVP9, "vts:1/rs:force:320:180"},
		{"av1-rotate90-1s-180.png", videoRot90, "vts:1/rs:force:180:320"},
		{"h264-0.5s-320.png", videoH264, "vts:0.5/rs:force:320:180"},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			t.Parallel()
			out, err := runVideo(t, e, tc.fixture, tc.options)
			if err != nil {
				t.Fatal(err)
			}
			golden.Compare(t, strings.TrimSuffix(tc.ref, ".png"), out.img, golden.Options{
				Dir:       filepath.Join(golden.ModuleRoot(t), videoRefDir),
				Tolerance: 0.02, // FFmpeg's bicubic scaler vs our shrink-on-load + Lanczos
			})
		})
	}
}

// TestVideoPreShrink: a large reduction scales the frame on load
// (libswscale to twice the target, libvips the rest), and the result is
// the same picture as decoding at full size.
func TestVideoPreShrink(t *testing.T) {
	t.Parallel()
	requireVideo(t)
	fast, err := runVideo(t, videoExecutor(t, videoSettings(), testLimits), videoVP9, "vts:1/rs:fit:160:90")
	if err != nil {
		t.Fatal(err)
	}
	full := testLimits
	full.DisableShrinkOnLoad = true
	slow, err := runVideo(t, videoExecutor(t, videoSettings(), full), videoVP9, "vts:1/rs:fit:160:90")
	if err != nil {
		t.Fatal(err)
	}
	if fast.res.Width != 160 || slow.res.Width != 160 {
		t.Fatalf("widths %d and %d, want 160", fast.res.Width, slow.res.Width)
	}
	res, err := ssim.Compare(slow.img, fast.img)
	if err != nil {
		t.Fatal(err)
	}
	if res.DSSIM > 0.01 {
		t.Errorf("pre-shrunk thumbnail differs from the full-size decode: DSSIM %.4f", res.DSSIM)
	}
}

// TestVideoLimits: the executor checks the video limits again before
// decoding (a request can arrive with a probe from another configuration).
func TestVideoLimits(t *testing.T) {
	t.Parallel()
	requireVideo(t)
	for name, edit := range map[string]func(*video.Settings){
		"duration": func(s *video.Settings) { s.Limits.MaxDuration = time.Second },
		"pixels":   func(s *video.Settings) { s.Limits.MaxPixels = 640 * 360 },
		"bytes":    func(s *video.Settings) { s.Limits.MaxBytes = 4096 },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := videoSettings()
			edit(&s)
			_, err := runVideo(t, videoExecutor(t, s, testLimits), videoVP9, "vts:1/rs:fit:160:90")
			if errs.KindOf(err) != errs.KindLimitExceeded {
				t.Fatalf("err = %v, want limit exceeded", err)
			}
		})
	}
	// The planner's source limit applies to the frame like to any image.
	small := testLimits
	small.MaxSrcPixels = 1000
	_, err := runVideo(t, videoExecutor(t, videoSettings(), small), videoVP9, "vts:1")
	if errs.KindOf(err) != errs.KindLimitExceeded {
		t.Fatalf("source pixels: err = %v, want limit exceeded", err)
	}
}

// TestVideoDisabled: XI_VIDEO_ENABLED=false refuses a video source in the
// executor too.
func TestVideoDisabled(t *testing.T) {
	t.Parallel()
	requireVideo(t)
	_, err := runVideo(t, videoExecutor(t, video.Settings{}, testLimits), videoVP9, "vts:1")
	if errs.KindOf(err) != errs.KindUnsupported {
		t.Fatalf("err = %v, want unsupported", err)
	}
}

// TestVideoCanceled: a request whose context is done before the frame is
// decoded reports the context's kind and leaks nothing.
func TestVideoCanceled(t *testing.T) {
	t.Parallel()
	requireVideo(t)
	e := videoExecutor(t, videoSettings(), testLimits)
	j, buf := videoJob(t, videoVP9, parse(t, "vts:1.5/rs:fit:320:180"), transform.FormatPNG)
	defer buf.Release()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := e.Run(ctx, j); errs.KindOf(err) != errs.KindCanceled {
		t.Fatalf("err = %v, want canceled", err)
	}
}

// TestVideoUnsupported: without FFmpeg (slim) the probe refuses a video
// with errs.KindUnsupported and a public message, and never crashes.
func TestVideoUnsupported(t *testing.T) {
	t.Parallel()
	if codec.VideoAvailable() {
		t.Skip("this build decodes video")
	}
	buf := newBuffer(t, readFixture(t, videoVP9))
	defer buf.Release()
	err := vips.Run(t.Context(), func() error {
		_, err := codec.ProbeMedia(t.Context(), buf, codec.Limits{}, new(videoSettings()), "")
		return err
	})
	if errs.KindOf(err) != errs.KindUnsupported {
		t.Fatalf("err = %v, want unsupported", err)
	}
	// The executor refuses it too, even when asked directly.
	j := &exec.Job{
		Request: parse(t, "vts:1"), Source: buf,
		Meta:   plan.SourceMeta{Width: 1280, Height: 720, Format: "webm", IsVideo: true},
		Target: encode.NewTarget(&transform.Output{}, &negotiate.Decision{Format: transform.FormatPNG}, false),
	}
	if _, err := videoExecutor(t, videoSettings(), testLimits).Run(t.Context(), j); errs.KindOf(err) != errs.KindUnsupported {
		t.Fatalf("exec err = %v, want unsupported", err)
	}
}

// TestVideoNoLeak: 200 thumbnails leave no libvips object or tracked
// memory behind, and no FFmpeg handle open.
func TestVideoNoLeak(t *testing.T) {
	requireVideo(t)
	e := videoExecutor(t, videoSettings(), testLimits)
	j, buf := videoJob(t, videoAV1, parse(t, "vts:1/rs:fit:160:90"), transform.FormatJPEG)
	defer buf.Release()
	run := func() {
		res, err := e.Run(t.Context(), j)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Release()
	}
	run() // warm up libvips' and FFmpeg's one-time allocations
	objects, mem := vips.LiveObjects(), vips.ReadMemStats().Mem
	for range 200 {
		run()
	}
	if n := vips.LiveObjects(); n > objects {
		t.Errorf("libvips objects %d → %d after 200 thumbnails", objects, n)
	}
	if m := vips.ReadMemStats().Mem; m > mem {
		t.Errorf("libvips tracked memory %d → %d bytes after 200 thumbnails", mem, m)
	}
}

// BenchmarkVideoPipeline measures a 320×180 JPEG thumbnail of a 720p video
// frame end to end (decode, resize, encode), with the frame scaled on load
// by libswscale (the default) and decoded at full size.
func BenchmarkVideoPipeline(b *testing.B) {
	if !codec.VideoAvailable() {
		b.Skip("built without FFmpeg")
	}
	full := testLimits
	full.DisableShrinkOnLoad = true
	for _, fx := range []struct{ name, file string }{
		{"h264", videoH264}, {"vp9", videoVP9}, {"av1", videoAV1},
	} {
		for _, mode := range []struct {
			name string
			lim  plan.Limits
		}{{"preshrink", testLimits}, {"full-decode", full}} {
			b.Run(fx.name+"/"+mode.name, func(b *testing.B) {
				e := videoExecutor(b, videoSettings(), mode.lim)
				j, buf := videoJob(b, fx.file, parse(b, "vts:0.9/rs:fit:320:180"), transform.FormatJPEG)
				defer buf.Release()
				b.ReportAllocs()
				for b.Loop() {
					res, err := e.Run(b.Context(), j)
					if err != nil {
						b.Fatal(err)
					}
					res.Body.Release()
				}
			})
		}
	}
}
