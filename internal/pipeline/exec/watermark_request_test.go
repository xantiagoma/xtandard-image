//go:build cgo

package exec_test

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/exec"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/testutil/ssim"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// watermarkBadge is the per-request watermark image of these tests (40x40
// RGBA): the executor receives its bytes in Job.Watermarks, as the server
// fetched them for watermark_url.
const watermarkBadge = "watermark/badge.png"

// wmu is the watermark_url option for any URL (the bytes come from the
// fixture); wmt the watermark_text option for markup.
var wmu = "wmu:" + base64.RawURLEncoding.EncodeToString([]byte("https://example.com/badge.png"))

func wmt(markup string) string {
	return "wmt:" + base64.RawURLEncoding.EncodeToString([]byte(markup))
}

// requestWatermarkCases are checked against testdata/golden/exec/<name>.png.
// Text goldens rely on DejaVu Sans (the base image's only sans font) and the
// perceptual tolerance covers hinting differences.
var requestWatermarkCases = []goldenCase{
	// watermark_url: placement, size, rotation, shadow, tiling.
	{name: "wmu-center", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:ce/" + wmu, out: transform.FormatPNG, w: 200, h: 150},
	{name: "wmu-south-east", fixture: jpegPhoto, options: "rs:fit:200:200/wm:0.8:soea:6:6/" + wmu, out: transform.FormatPNG, w: 200, h: 150},
	{name: "wmu-size-width", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:nowe:4:4/" + wmu + "/wms:80:0", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wmu-size-height", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:nowe:4:4/" + wmu + "/wms:0:20", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wmu-size-box", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:ce/" + wmu + "/wms:100:30", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wmu-scale-wins", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:ce:0:0:0.5/" + wmu + "/wms:10:10", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wmu-rotate", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:ce/" + wmu + "/wms:60:0/wmr:30", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wmu-rotate-negative", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:soea:4:4/" + wmu + "/wmr:-90", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wmu-shadow", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:nowe:10:10/" + wmu + "/wmsh:3", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wmu-tile", fixture: jpegPhoto, options: "rs:fit:200:200/wm:0.7:re:8:8/" + wmu + "/wms:24:0", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wmu-chessboard", fixture: jpegPhoto, options: "rs:fit:200:200/wm:0.9:ch:4:4/" + wmu + "/wms:24:0", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wmu-rotate-tile", fixture: jpegPhoto, options: "rs:fit:200:200/wm:0.8:re:6:6:0.15/" + wmu + "/wmr:45", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wmu-dpr2", fixture: jpegPhoto, options: "rs:fit:100:100/dpr:2/wm:1:nowe:4:4/" + wmu + "/wms:20:0/wmsh:1", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wmu-png-alpha", fixture: pngAlpha, options: "rs:fit:128:128/wm:0.9:soea:2:2/" + wmu + "/wms:32:0", out: transform.FormatPNG, w: 128, h: 96},
	{name: "wmu-gif", fixture: gifAnim, options: "rs:fill:96:72/wm:1:soea:2:2:0.4/" + wmu + "/wmr:15", out: transform.FormatGIF, w: 96, h: 72, frames: 6},

	// Chessboard with the configured watermark.
	{name: "wm-chessboard", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:ch", out: transform.FormatPNG, w: 200, h: 150},
	{name: "wm-chessboard-spacing", fixture: jpegPhoto, options: "rs:fit:200:200/wm:0.7:ch:8:8:0.15", out: transform.FormatPNG, w: 200, h: 150},

	// watermark_text: plain text, markup, sizing, rotation, shadow, tiling.
	{name: "wmt-plain", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:soea:6:6/" + wmt("© xtandard"), out: transform.FormatPNG, w: 200, h: 150, tolerance: 0.03},
	{name: "wmt-markup", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:ce/" + wmt(`<span foreground="#ffffff" size="20pt"><b>Big</b></span> <i>text</i>`), out: transform.FormatPNG, w: 200, h: 150, tolerance: 0.03},
	{name: "wmt-scale", fixture: jpegPhoto, options: "rs:fit:200:200/wm:0.8:ce:0:0:0.8/" + wmt("SAMPLE"), out: transform.FormatPNG, w: 200, h: 150, tolerance: 0.03},
	{name: "wmt-size", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:nowe:4:4/" + wmt("sized") + "/wms:120:0", out: transform.FormatPNG, w: 200, h: 150, tolerance: 0.03},
	{name: "wmt-shadow", fixture: jpegPhoto, options: "rs:fit:200:200/wm:1:ce/" + wmt(`<span foreground="white" size="24pt">Shadow</span>`) + "/wmsh:2", out: transform.FormatPNG, w: 200, h: 150, tolerance: 0.03},
	{name: "wmt-rotate-tile", fixture: jpegPhoto, options: "rs:fit:200:200/wm:0.5:re:16:16/" + wmt("draft") + "/wmr:-30", out: transform.FormatPNG, w: 200, h: 150, tolerance: 0.03},
	{name: "wmt-chessboard", fixture: jpegPhoto, options: "rs:fit:200:200/wm:0.6:ch:6:6/" + wmt("<b>xi</b>"), out: transform.FormatPNG, w: 200, h: 150, tolerance: 0.03},
	{name: "wmt-dpr2", fixture: jpegPhoto, options: "rs:fit:100:100/dpr:2/wm:1:sowe:4:4/" + wmt("dpr 2"), out: transform.FormatPNG, w: 200, h: 150, tolerance: 0.03},
	{name: "wmt-wrap-shrink", fixture: jpegPhoto, options: "rs:fit:120:120/wm:1:ce/" + wmt(`<span size="40pt">a long line of text that cannot fit</span>`), out: transform.FormatPNG, w: 120, h: 90, tolerance: 0.03},
	{name: "wmt-gif", fixture: gifAnim, options: "rs:fill:96:72/wm:1:no:0:2/" + wmt("<b>gif</b>"), out: transform.FormatGIF, w: 96, h: 72, frames: 6, tolerance: 0.03},
}

// processMarked runs options with the badge as every per-request
// watermark image of the first pipeline.
func processMarked(tb testing.TB, e *exec.Executor, fixture, options string, out transform.Format) output {
	tb.Helper()
	req := parse(tb, options)
	j, buf := job(tb, readFixture(tb, fixture), req, out)
	defer buf.Release()
	badge := newBuffer(tb, readFixture(tb, watermarkBadge))
	defer badge.Release()
	for i := range req.Pipelines {
		for k := range req.Pipelines[i].Watermarks {
			if req.Pipelines[i].Watermarks[k].Source != nil {
				j.Watermarks = append(j.Watermarks, exec.WatermarkImage{Pipeline: i, Index: k, Source: badge, Format: "png"})
			}
		}
	}
	res, err := e.Run(tb.Context(), j)
	if err != nil {
		tb.Fatalf("run %q: %v", options, err)
	}
	defer res.Body.Release()
	enc := bytes.Clone(res.Body.Bytes())
	img, frames := decode(tb, enc)
	return output{res: res, img: img, frames: frames, bytes: enc}
}

func TestRequestWatermarkGolden(t *testing.T) {
	t.Parallel()
	e := newWatermarkExecutor(t, newWatermark(t, watermarkLogo, "png", 1))
	for _, tc := range requestWatermarkCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := processMarked(t, e, tc.fixture, tc.options, tc.out)
			checkOutput(t, &tc, &o)
			if len(o.res.Skipped) > 0 {
				t.Errorf("Skipped = %v", o.res.Skipped)
			}
			golden.Compare(t, "exec/"+tc.name, o.img, golden.Options{Tolerance: tc.tolerance})
		})
	}
}

// TestRequestWatermarkChanges checks that each option changes the output
// (a golden alone would also accept an option that does nothing).
func TestRequestWatermarkChanges(t *testing.T) {
	t.Parallel()
	e := newWatermarkExecutor(t, newWatermark(t, watermarkLogo, "png", 1))
	base := "rs:fit:200:200/wm:1:ce/"
	for _, tc := range []struct{ name, a, b string }{
		{name: "url vs configured", a: base + wmu, b: base},
		{name: "size", a: base + wmu, b: base + wmu + "/wms:60:0"},
		{name: "rotate", a: base + wmu, b: base + wmu + "/wmr:30"},
		{name: "shadow", a: base + wmu, b: base + wmu + "/wmsh:2"},
		{name: "chessboard vs repeat", a: "rs:fit:200:200/wm:1:re/" + wmu, b: "rs:fit:200:200/wm:1:ch/" + wmu},
		{name: "text vs url", a: base + wmu, b: base + wmt("text")},
		{name: "text markup", a: base + wmt("text"), b: base + wmt("<b>text</b>")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := processMarked(t, e, jpegPhoto, tc.a, transform.FormatPNG)
			b := processMarked(t, e, jpegPhoto, tc.b, transform.FormatPNG)
			if r, err := ssim.Compare(a.img, b.img); err != nil || r.DSSIM < 0.0005 {
				t.Errorf("%q and %q look the same: dssim %.5f, %v", tc.a, tc.b, r.DSSIM, err)
			}
		})
	}
}

// TestRequestWatermarkOpacity checks that XI_PROCESSING_WATERMARK_OPACITY
// applies to per-request images and text too, without a configured image.
func TestRequestWatermarkOpacity(t *testing.T) {
	t.Parallel()
	half := 0.5
	exe := func(o *float64) *exec.Executor {
		return newExecutorWith(t, &exec.Config{Limits: testLimits, Watermarks: exec.WatermarkConfig{Opacity: o}})
	}
	for _, opt := range []string{wmu, wmt("<b>opacity</b>")} {
		a := processMarked(t, exe(&half), jpegPhoto, "rs:fit:200:200/wm:0.8:ce/"+opt, transform.FormatPNG)
		b := processMarked(t, exe(nil), jpegPhoto, "rs:fit:200:200/wm:0.4:ce/"+opt, transform.FormatPNG)
		if r, err := ssim.Compare(a.img, b.img); err != nil || r.DSSIM > 0.0005 {
			t.Errorf("%s: config opacity 0.5 × wm 0.8 vs wm 0.4: dssim %.5f, %v", opt, r.DSSIM, err)
		}
	}
}

// TestRequestWatermarkMissing: a per-request image the caller did not
// supply is skipped and reported, and the image is unchanged.
func TestRequestWatermarkMissing(t *testing.T) {
	t.Parallel()
	e := newWatermarkExecutor(t, newWatermark(t, watermarkLogo, "png", 1))
	plain := processWith(t, e, jpegPhoto, "rs:fit:120:120", transform.FormatPNG)
	o := processWith(t, e, jpegPhoto, "rs:fit:120:120/wm:1:ce/"+wmu, transform.FormatPNG)
	if len(o.res.Skipped) != 1 || o.res.Skipped[0] != "watermark_source" {
		t.Errorf("Skipped = %v, want [watermark_source]", o.res.Skipped)
	}
	if r, err := ssim.Compare(plain.img, o.img); err != nil || r.DSSIM > 0.0001 {
		t.Errorf("image changed: dssim %.5f, %v", r.DSSIM, err)
	}
	blank := processWith(t, e, jpegPhoto, "rs:fit:120:120/wm:1:ce/"+wmt("   "), transform.FormatPNG)
	if len(blank.res.Skipped) != 1 || blank.res.Skipped[0] != "watermark_text_empty" {
		t.Errorf("blank text: Skipped = %v", blank.res.Skipped)
	}
}

// TestRequestWatermarkTextErrors: markup outside the whitelist is a client
// error (400), markup over the limits 422.
func TestRequestWatermarkTextErrors(t *testing.T) {
	t.Parallel()
	e := newWatermarkExecutor(t, nil)
	for _, tc := range []struct {
		name string
		mut  func(*transform.Watermark)
		want errs.Kind
	}{
		{name: "unknown element", mut: func(w *transform.Watermark) { w.Text = `<img src="/etc/passwd"/>` }, want: errs.KindInvalidURL},
		{name: "unknown attribute", mut: func(w *transform.Watermark) { w.Text = `<span letter_spacing="99999999">x</span>` }, want: errs.KindInvalidURL},
		{name: "entity", mut: func(w *transform.Watermark) { w.Text = `<!DOCTYPE x [<!ENTITY e SYSTEM "file:///etc/passwd">]>&e;` }, want: errs.KindInvalidURL},
		{name: "malformed", mut: func(w *transform.Watermark) { w.Text = `<b>x` }, want: errs.KindInvalidURL},
		{name: "huge size", mut: func(w *transform.Watermark) { w.Text = `<span size="99999999">x</span>` }, want: errs.KindLimitExceeded},
		{name: "big nesting", mut: func(w *transform.Watermark) {
			w.Text = strings.Repeat("<big>", 8) + "x" + strings.Repeat("</big>", 8)
			w.Font = "sans 600"
		}, want: errs.KindLimitExceeded},
		{name: "font size", mut: func(w *transform.Watermark) { w.Text, w.Font = "x", "sans 5000" }, want: errs.KindLimitExceeded},
		{name: "font characters", mut: func(w *transform.Watermark) { w.Text, w.Font = "x", "sans;16" }, want: errs.KindInvalidURL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := parse(t, "rs:fit:120:120")
			w := transform.Watermark{Opacity: 1, Position: transform.Gravity{Type: transform.GravityCenter}}
			tc.mut(&w)
			req.Pipelines[0].Watermarks = []transform.Watermark{w}
			j, buf := job(t, readFixture(t, jpegPhoto), req, transform.FormatPNG)
			defer buf.Release()
			res, err := e.Run(t.Context(), j)
			if err == nil {
				res.Body.Release()
				t.Fatal("Run succeeded")
			}
			if got := errs.KindOf(err); got != tc.want {
				t.Errorf("kind = %v, want %v (%v)", got, tc.want, err)
			}
		})
	}
}

// TestRequestWatermarkTextBounded: text larger than the image is shrunk
// to fit the frame, and text that cannot fit even at the smallest
// resolution is refused (422) quickly, whatever the pixel cap.
func TestRequestWatermarkTextBounded(t *testing.T) {
	t.Parallel()
	run := func(limit int64, text, font string) (*exec.Result, error) {
		e := newExecutorWith(t, &exec.Config{Limits: testLimits, Watermarks: exec.WatermarkConfig{MaxTextPixels: limit}})
		req := parse(t, "rs:fit:200:200")
		req.Pipelines[0].Watermarks = []transform.Watermark{{
			Opacity: 1, Position: transform.Gravity{Type: transform.GravityCenter}, Text: text, Font: font,
		}}
		j, buf := job(t, readFixture(t, jpegPhoto), req, transform.FormatPNG)
		defer buf.Release()
		return e.Run(t.Context(), j)
	}
	for _, limit := range []int64{0, 10_000} {
		res, err := run(limit, strings.Repeat("huge ", 12), "sans 300")
		if err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
		if res.Width != 200 || res.Height != 150 {
			t.Errorf("limit %d: size %dx%d", limit, res.Width, res.Height)
		}
		res.Body.Release()

		start := time.Now()
		res, err = run(limit, strings.Repeat("WWWW ", 800), "sans 900")
		if err == nil {
			res.Body.Release()
			t.Fatalf("limit %d: 4000 characters at 900 pt accepted", limit)
		}
		if k := errs.KindOf(err); k != errs.KindLimitExceeded {
			t.Errorf("limit %d: kind %v (%v)", limit, k, err)
		}
		if d := time.Since(start); d > 20*time.Second {
			t.Errorf("limit %d: took %v", limit, d)
		}
	}
}

// TestRequestWatermarkFormats decodes per-request images from several
// decoders: SVG (sanitised by the caller), the Go BMP loader, an animation
// (first frame) and a JPEG with EXIF orientation.
func TestRequestWatermarkFormats(t *testing.T) {
	t.Parallel()
	e := newWatermarkExecutor(t, nil)
	for _, tc := range []struct{ fixture, format string }{
		{fixture: "svg/logo.svg", format: "svg"},
		{fixture: "bmp/rgb24.bmp", format: "bmp"},
		{fixture: gifAnim, format: "gif"},
		{fixture: exif6, format: "jpeg"},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			req := parse(t, "rs:fit:160:160/wm:0.5:re:0:0:0.3/"+wmu)
			j, buf := job(t, readFixture(t, jpegPhoto), req, transform.FormatJPEG)
			defer buf.Release()
			mark := newBuffer(t, readFixture(t, tc.fixture))
			defer mark.Release()
			j.Watermarks = []exec.WatermarkImage{{Source: mark, Format: tc.format}}
			res, err := e.Run(t.Context(), j)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Release()
			if res.Width != 160 || res.Height != 120 || len(res.Skipped) > 0 {
				t.Errorf("size %dx%d, skipped %v", res.Width, res.Height, res.Skipped)
			}
		})
	}
	t.Run("corrupt", func(t *testing.T) {
		t.Parallel()
		req := parse(t, "rs:fit:160:160/wm:1:ce/"+wmu)
		j, buf := job(t, readFixture(t, jpegPhoto), req, transform.FormatJPEG)
		defer buf.Release()
		photo := readFixture(t, jpegPhoto)
		mark := newBuffer(t, photo[:len(photo)/3])
		defer mark.Release()
		j.Watermarks = []exec.WatermarkImage{{Source: mark, Format: "jpeg"}}
		res, err := e.Run(t.Context(), j)
		if err == nil {
			res.Body.Release()
			t.Fatal("Run succeeded")
		}
		if k := errs.KindOf(err); k != errs.KindCorrupt {
			t.Errorf("kind = %v (%v)", k, err)
		}
	})
}
