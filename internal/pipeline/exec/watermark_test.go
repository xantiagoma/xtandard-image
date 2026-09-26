//go:build cgo

package exec_test

import (
	"bytes"
	"image"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/codec/encode"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/exec"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/testutil/ssim"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// watermarkLogo is the configured watermark of these tests (48x32 RGBA; the
// imgproxy comparison sets IMGPROXY_WATERMARK_PATH to the same file).
const watermarkLogo = "watermark/logo.png"

// watermarkCases are checked against testdata/golden/exec/<name>.png and,
// with XI_IMGPROXY_URL, against imgproxy with the same watermark.
var watermarkCases = []goldenCase{
	// Every position with offsets on a 200x150 JPEG.
	{name: "wm-center", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:ce:6:4", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wm-north", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:no:6:4", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wm-south", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:so:6:4", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wm-east", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:ea:6:4", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wm-west", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:we:6:4", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wm-north-east", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:noea:6:4", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wm-north-west", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:nowe:6:4", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wm-south-east", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:soea:6:4", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wm-south-west", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:sowe:6:4", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wm-fraction-offsets", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:nowe:0.1:0.2", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wm-overflow", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:soea:-24:-16", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wm-dpr2", fixture: jpegPhoto, options: "rs:fit:100:100/dpr:2/wm:1:nowe:5:5", out: transform.FormatPNG, w: 200, h: 150},

	// Scale (fit into scale × the image, enlarging) and opacity.
	{name: "wm-scale-up", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:ce:0:0:0.5", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wm-scale-down", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:soea:4:4:0.1", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wm-opacity", fixture: jpegPhoto, options: "rs:fit:200:200/wm:0.4:ce:0:0:0.4", out: transform.FormatPNG, w: 200, h: 150},

	// Tiling ("re"): plain, with spacing, scaled, translucent.
	{name: "wm-tile", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:re", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wm-tile-spacing", fixture: jpegPhoto, options: "rs:fit:200:200/wm:0.7:re:12:8", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wm-tile-scale", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:re:0.05:0.05:0.2", out: transform.FormatPNG, w: 200, h: 150},

	// PNG with alpha keeps its alpha; JPEG output.
	{name: "wm-png-alpha", fixture: pngAlpha, options: "rs:fit:128:128/wm:0.8:soea:4:4", out: transform.FormatPNG, w: 128, h: 96},
	{name: "wm-png-alpha-tile", fixture: pngAlpha, options: "rs:fit:128:128/wm:0.6:re:0:0:0.25", out: transform.FormatPNG, w: 128, h: 96},
	{name: "wm-jpeg", fixture: jpegPhoto, options: "rs:fit:160:160/wm:1:nowe:8:8:0.25", out: transform.FormatJPEG, w: 160, h: 120},

	// Animations: the watermark lands on every frame, which are kept.
	{name: "wm-gif", fixture: gifAnim, options: "rs:fill:96:72/wm:1:soea:2:2:0.4", out: transform.FormatGIF, w: 96, h: 72, frames: 6},
	{name: "wm-gif-overflow", fixture: gifAnim, options: "rs:fill:96:72/wm:1:so:0:-12", out: transform.FormatGIF, w: 96, h: 72, frames: 6},
	{name: "wm-gif-tile", fixture: gifAnim, options: "rs:fill:96:72/wm:0.8:re:4:4:0.3", out: transform.FormatGIF, w: 96, h: 72, frames: 6},
	{name: "wm-webp-anim", fixture: webpAnim, options: "rs:fill:64:48/wm:0.7:nowe:2:2:0.5", out: transform.FormatWebP, w: 64, h: 48, frames: 6},

	// Chained: each pipeline has its own watermark (imgproxy Pro).
	{name: "wm-chained", fixture: jpegPhoto, options: "rs:fill:120:90/wm:1:nowe:0:0:0.3/-/rot:90/wm:0.5:soea:0:0:0.3", out: transform.FormatPNG, w: 90, h: 120, noImgproxy: true},
}

// newWatermark decodes testdata/fixtures/<fixture> as the configured
// watermark with the given configured opacity. It is closed when tb ends.
func newWatermark(tb testing.TB, fixture, format string, opacity float64) *exec.Watermark {
	tb.Helper()
	buf := newBuffer(tb, readFixture(tb, fixture))
	defer buf.Release()
	wm, err := exec.NewWatermark(tb.Context(), buf, &exec.WatermarkOptions{Format: format, Opacity: opacity})
	if err != nil {
		tb.Fatalf("NewWatermark: %v", err)
	}
	tb.Cleanup(wm.Close)
	return wm
}

func newWatermarkExecutor(tb testing.TB, wm *exec.Watermark) *exec.Executor {
	tb.Helper()
	return newExecutorWith(tb, &exec.Config{Limits: testLimits, Watermark: wm})
}

// newExecutorWith builds an executor from cfg with the default encoder.
func newExecutorWith(tb testing.TB, cfg *exec.Config) *exec.Executor {
	tb.Helper()
	ec := encode.DefaultConfig()
	enc, err := encode.New(&ec)
	if err != nil {
		tb.Fatal(err)
	}
	e, err := exec.New(cfg, enc)
	if err != nil {
		tb.Fatal(err)
	}
	return e
}

// processWith runs options on a fixture with e and decodes the result.
func processWith(tb testing.TB, e *exec.Executor, fixture, options string, out transform.Format) output {
	tb.Helper()
	return processDataWith(tb, e, readFixture(tb, fixture), options, out)
}

func TestWatermarkGolden(t *testing.T) {
	t.Parallel()
	e := newWatermarkExecutor(t, newWatermark(t, watermarkLogo, "png", 1))
	for _, tc := range watermarkCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := processWith(t, e, tc.fixture, tc.options, tc.out)
			checkOutput(t, &tc, &o)
			if len(o.res.Skipped) > 0 {
				t.Errorf("Skipped = %v", o.res.Skipped)
			}
			golden.Compare(t, "exec/"+tc.name, o.img, golden.Options{Tolerance: tc.tolerance})
		})
	}
}

// TestWatermarkEveryFrame checks that each frame of an animation differs
// from the unwatermarked frame, in the watermark's corner only.
func TestWatermarkEveryFrame(t *testing.T) {
	t.Parallel()
	e := newWatermarkExecutor(t, newWatermark(t, watermarkLogo, "png", 1))
	const opts = "rs:fill:96:72"
	plain := processWith(t, e, gifAnim, opts, transform.FormatGIF)
	marked := processWith(t, e, gifAnim, opts+"/wm:1:soea:0:0:0.4", transform.FormatGIF)
	if plain.frames != 6 || marked.frames != 6 {
		t.Fatalf("frames: plain %d, watermarked %d, want 6", plain.frames, marked.frames)
	}
	for i := range 6 {
		frame := image.Rect(0, i*72, 96, (i+1)*72)
		// The watermark is fitted into 38x29 in the bottom-right corner.
		corner := image.Rect(96-30, (i+1)*72-20, 96, (i+1)*72).Intersect(frame)
		away := image.Rect(0, i*72, 40, i*72+30)
		if d := meanDiff(plain.img, marked.img, corner); d < 20 {
			t.Errorf("frame %d: corner differs by %.1f, want a watermark", i, d)
		}
		if d := meanDiff(plain.img, marked.img, away); d > 6 {
			t.Errorf("frame %d: top-left differs by %.1f, want untouched", i, d)
		}
	}
}

// meanDiff is the mean absolute RGB difference of a and b over r.
func meanDiff(a, b image.Image, r image.Rectangle) float64 {
	var sum, n float64
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			ar, ag, ab, _ := a.At(x, y).RGBA()
			br, bg, bb, _ := b.At(x, y).RGBA()
			sum += absDiff(ar, br) + absDiff(ag, bg) + absDiff(ab, bb)
			n += 3
		}
	}
	return sum / n / 257
}

func absDiff(a, b uint32) float64 {
	if a > b {
		return float64(a - b)
	}
	return float64(b - a)
}

// TestWatermarkConfiguredOpacity checks that XI_PROCESSING_WATERMARK_OPACITY
// multiplies the request's opacity.
func TestWatermarkConfiguredOpacity(t *testing.T) {
	t.Parallel()
	half := newWatermarkExecutor(t, newWatermark(t, watermarkLogo, "png", 0.5))
	full := newWatermarkExecutor(t, newWatermark(t, watermarkLogo, "png", 1))
	a := processWith(t, half, jpegPhoto, "rs:fit:200:200/wm:0.8:ce:0:0:0.5", transform.FormatPNG)
	b := processWith(t, full, jpegPhoto, "rs:fit:200:200/wm:0.4:ce:0:0:0.5", transform.FormatPNG)
	r, err := ssim.Compare(a.img, b.img)
	if err != nil {
		t.Fatal(err)
	}
	if r.DSSIM > 0.0005 {
		t.Errorf("config opacity 0.5 × wm 0.8 vs wm 0.4: dssim %.5f", r.DSSIM)
	}
	c := processWith(t, full, jpegPhoto, "rs:fit:200:200/wm:0.8:ce:0:0:0.5", transform.FormatPNG)
	if r, err := ssim.Compare(a.img, c.img); err != nil || r.DSSIM < 0.001 {
		t.Errorf("config opacity had no effect: dssim %.5f, %v", r.DSSIM, err)
	}
}

// TestWatermarkSkipped covers the watermarks that are not applied: none
// configured, zero opacity, and a watermark source of its own whose image
// the caller did not supply.
func TestWatermarkSkipped(t *testing.T) {
	t.Parallel()
	with := newWatermarkExecutor(t, newWatermark(t, watermarkLogo, "png", 1))
	without := newWatermarkExecutor(t, nil)
	plain := processWith(t, with, jpegPhoto, "rs:fit:120:120", transform.FormatPNG)

	for _, tc := range []struct {
		name    string
		e       *exec.Executor
		req     func(*transform.Request)
		options string
		skipped []string
	}{
		{name: "none configured", e: without, options: "rs:fit:120:120/wm:1:ce", skipped: []string{"watermark"}},
		{name: "zero opacity", e: with, options: "rs:fit:120:120", req: func(r *transform.Request) {
			r.Pipelines[0].Watermarks = []transform.Watermark{{Opacity: 0, Position: transform.Gravity{Type: transform.GravityCenter}}}
		}},
		{name: "own source", e: with, options: "rs:fit:120:120", skipped: []string{"watermark_source"}, req: func(r *transform.Request) {
			r.Pipelines[0].Watermarks = []transform.Watermark{{
				Opacity: 1, Position: transform.Gravity{Type: transform.GravityCenter},
				Source: &transform.SourceSpec{Raw: "https://example.com/wm.png"},
			}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := parse(t, tc.options)
			if tc.req != nil {
				tc.req(req)
			}
			j, buf := job(t, readFixture(t, jpegPhoto), req, transform.FormatPNG)
			defer buf.Release()
			res, err := tc.e.Run(t.Context(), j)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Release()
			if diff := cmp.Diff(tc.skipped, slices.Sorted(slices.Values(res.Skipped))); diff != "" {
				t.Errorf("Skipped (-want +got):\n%s", diff)
			}
			got, _ := decode(t, bytes.Clone(res.Body.Bytes()))
			if r, err := ssim.Compare(plain.img, got); err != nil || r.DSSIM > 0.0001 {
				t.Errorf("image changed: dssim %.5f, %v", r.DSSIM, err)
			}
		})
	}
}

// TestWatermarkFormats loads the watermark from every kind of decoder:
// libvips raster, SVG (sanitised by the caller), the Go BMP loader, an
// animation (first frame) and a JPEG with EXIF orientation (applied).
func TestWatermarkFormats(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fixture, format string
		w, h            int
	}{
		{fixture: watermarkLogo, format: "png", w: 48, h: 32},
		{fixture: "svg/logo.svg", format: "svg", w: 96, h: 72},
		{fixture: "bmp/rgb24.bmp", format: "bmp", w: 96, h: 72},
		{fixture: gifAnim, format: "gif", w: 96, h: 72},
		{fixture: exif6, format: "jpeg", w: 320, h: 240},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			wm := newWatermark(t, tc.fixture, tc.format, 1)
			if wm.Width() != tc.w || wm.Height() != tc.h {
				t.Errorf("watermark %dx%d, want %dx%d", wm.Width(), wm.Height(), tc.w, tc.h)
			}
			o := processWith(t, newWatermarkExecutor(t, wm), jpegPhoto, "rs:fit:160:160/wm:0.5:re:0:0:0.3", transform.FormatJPEG)
			if o.res.Width != 160 || o.res.Height != 120 {
				t.Errorf("size = %dx%d", o.res.Width, o.res.Height)
			}
		})
	}
}

func TestNewWatermarkErrors(t *testing.T) {
	t.Parallel()
	photo := readFixture(t, jpegPhoto)
	for _, tc := range []struct {
		name string
		data []byte
		opts exec.WatermarkOptions
		want errs.Kind
	}{
		{name: "corrupt", data: photo[:len(photo)/3], opts: exec.WatermarkOptions{Format: "jpeg"}, want: errs.KindCorrupt},
		{name: "not an image", data: []byte("not an image at all"), opts: exec.WatermarkOptions{Format: "png"}, want: errs.KindUnsupported},
		{name: "too many pixels", data: photo, opts: exec.WatermarkOptions{Format: "jpeg", MaxPixels: 100}, want: errs.KindLimitExceeded},
		{name: "opacity", data: photo, opts: exec.WatermarkOptions{Format: "jpeg", Opacity: 2}, want: errs.KindInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			buf := newBuffer(t, tc.data)
			defer buf.Release()
			wm, err := exec.NewWatermark(t.Context(), buf, &tc.opts)
			if err == nil {
				wm.Close()
				t.Fatal("NewWatermark succeeded")
			}
			if got := errs.KindOf(err); got != tc.want {
				t.Errorf("kind = %v, want %v (%v)", got, tc.want, err)
			}
		})
	}
}

// TestWatermarkNoLeak runs watermarked jobs (placed, scaled, tiled,
// chessboard, animated; the configured image, per-request images and text,
// sized, rotated and shadowed) and checks that libvips' memory, allocations and live objects
// return to their baseline, and that closing the watermark releases it.
//
// Not parallel: the counters are process-wide.
func TestWatermarkNoLeak(t *testing.T) {
	const (
		requests    = 300
		slackBytes  = 1 << 20
		slackAllocs = 64
		slackObjs   = 16
	)
	type prepared struct {
		data []byte
		req  *transform.Request
		out  transform.Format
	}
	in := make([]prepared, 0, 10)
	for _, c := range []struct{ fixture, options string }{
		{jpegPhoto, "rs:fit:120:120/wm:0.5:soea:4:4:0.3"},
		{pngAlpha, "rs:fit:64:64/wm:0.7:re:4:4:0.2"},
		{gifAnim, "rs:fill:48:48/wm:1:nowe:2:2:0.5"},
		{gifAnim, "rs:fill:48:48/wm:0.6:re"},
		{jpegPhoto, "rs:fit:120:120/wm:0.8:ch:2:2:0.2"},
		{jpegPhoto, "rs:fit:120:120/wm:0.8:soea:2:2/" + wmu + "/wms:30:0/wmr:20/wmsh:2"},
		{pngAlpha, "rs:fit:64:64/wm:0.7:ch/" + wmu + "/wms:16:0"},
		{gifAnim, "rs:fill:48:48/wm:1:ce/" + wmu + "/wmr:45"},
		{jpegPhoto, "rs:fit:120:120/wm:1:ce/" + wmt("<b>leak</b> test") + "/wmsh:1/wmr:10"},
		{jpegPhoto, "rs:fit:120:120/wm:0.5:re:4:4/" + wmt(`<span size="30pt">shrunk to fit the frame</span>`)},
	} {
		out := transform.FormatPNG
		if c.fixture == gifAnim {
			out = transform.FormatGIF
		}
		in = append(in, prepared{data: readFixture(t, c.fixture), req: parse(t, c.options), out: out})
	}

	baseObjects := vips.LiveObjects()
	logo := newBuffer(t, readFixture(t, watermarkLogo))
	wm, err := exec.NewWatermark(t.Context(), logo, &exec.WatermarkOptions{Format: "png", Opacity: 1})
	logo.Release()
	if err != nil {
		t.Fatal(err)
	}
	e := newWatermarkExecutor(t, wm)
	badge := newBuffer(t, readFixture(t, watermarkBadge))
	defer badge.Release()
	request := func(i int) {
		p := &in[i%len(in)]
		j, buf := job(t, p.data, p.req, p.out)
		defer buf.Release()
		j.Watermarks = []exec.WatermarkImage{{Source: badge, Format: "png"}}
		res, err := e.Run(t.Context(), j)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		res.Body.Release()
	}
	for i := range 2 * len(in) {
		request(i) // warm up lazily initialised libvips state
	}
	before, objects := vips.ReadMemStats(), vips.LiveObjects()
	for i := range requests {
		request(i)
	}
	after, objectsAfter := vips.ReadMemStats(), vips.LiveObjects()
	if after.Mem-before.Mem > slackBytes || after.Allocs-before.Allocs > slackAllocs ||
		after.Files != before.Files || objectsAfter-objects > slackObjs {
		t.Errorf("after %d requests: mem %d → %d B, allocs %d → %d, files %d → %d, objects %d → %d",
			requests, before.Mem, after.Mem, before.Allocs, after.Allocs, before.Files, after.Files,
			objects, objectsAfter)
	}
	wm.Close()
	if n := vips.LiveObjects(); n-baseObjects > slackObjs {
		t.Errorf("after Close: %d live objects, %d before NewWatermark", n, baseObjects)
	}
	t.Logf("after %d requests: mem %+d B, allocs %+d, objects %+d", requests,
		after.Mem-before.Mem, after.Allocs-before.Allocs, objectsAfter-objects)
}
