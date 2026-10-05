//go:build cgo

package exec_test

import (
	"bytes"
	"image"
	"image/png"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/exec"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// transparentPNG is a fully transparent w×h PNG: the image path blends a
// watermark over it unchanged, which is what a video overlay must look
// like.
func transparentPNG(tb testing.TB, w, h int) []byte {
	tb.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, w, h))); err != nil {
		tb.Fatal(err)
	}
	return b.Bytes()
}

// The video frame size of these tests.
const frameW, frameH = 200, 150

// renderOverlay renders the first watermark of options as a video overlay
// on a frameW×frameH frame, with the badge as its image.
func renderOverlay(tb testing.TB, e *exec.Executor, options string) *exec.Overlay {
	tb.Helper()
	req := parse(tb, options)
	wm := &req.Pipelines[0].Watermarks[0]
	var img *exec.OverlayImage
	if wm.Source != nil {
		badge := newBuffer(tb, readFixture(tb, watermarkBadge))
		defer badge.Release()
		img = &exec.OverlayImage{Source: badge, Format: "png"}
	}
	o, err := e.RenderOverlay(tb.Context(), wm, img, frameW, frameH)
	if err != nil {
		tb.Fatalf("RenderOverlay %q: %v", options, err)
	}
	return o
}

// canvas draws o on a transparent w×h image.
func canvas(o *exec.Overlay, w, h int) *image.NRGBA {
	c := image.NewNRGBA(image.Rect(0, 0, w, h))
	if o == nil {
		return c
	}
	pix := o.Pixels.Bytes()
	for y := range o.Height {
		copy(c.Pix[(o.Y+y)*c.Stride+4*o.X:], pix[y*4*o.Width:(y+1)*4*o.Width])
	}
	return c
}

// TestRenderOverlayMatchesImage: a video overlay is the image pipeline's
// watermark, pixel for pixel: placement, size, rotation, shadow, opacity,
// tiling and text (the same fonts and shaping), clipped to the frame.
func TestRenderOverlayMatchesImage(t *testing.T) {
	t.Parallel()
	e := newWatermarkExecutor(t, newWatermark(t, watermarkLogo, "png", 1))
	const w, h = frameW, frameH
	src := transparentPNG(t, w, h)
	for _, tc := range []struct{ name, options string }{
		{"wmu-center", "wm:1:ce/" + wmu},
		{"wmu-south-east-opacity", "wm:0.6:soea:6:6/" + wmu},
		{"wmu-scale", "wm:1:nowe:4:4:0.5/" + wmu},
		{"wmu-size-rotate", "wm:1:ce/" + wmu + "/wms:60:0/wmr:30"},
		{"wmu-shadow", "wm:1:nowe:10:10/" + wmu + "/wmsh:3"},
		{"wmu-overflow", "wm:1:nowe:-20:-20/" + wmu},
		{"wmu-tile", "wm:0.7:re:8:8/" + wmu + "/wms:24:0"},
		{"wmu-chessboard", "wm:0.9:ch:4:4/" + wmu + "/wms:24:0"},
		{"configured", "wm:0.5:soea:4:4:0.3"},
		{"wmt-plain", "wm:1:soea:6:6/" + wmt("© xtandard")},
		{"wmt-markup", "wm:1:ce/" + wmt(`<span foreground="#ff0000" size="20pt"><b>Big</b></span> <i>text</i>`)},
		{"wmt-shadow-rotate", "wm:0.8:ce/" + wmt(`<span foreground="white">Shadow</span>`) + "/wmsh:2/wmr:-20"},
		{"wmt-hostile-literal", "wm:1:ce/" + wmt(`[in]drawtext=text='%{pts\:hms}':x=0;movie=/etc/passwd`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := renderOverlay(t, e, tc.options)
			if o == nil {
				t.Fatal("nothing rendered")
			}
			defer o.Release()
			if o.X < 0 || o.Y < 0 || o.X+o.Width > w || o.Y+o.Height > h || o.Pixels.Len() != 4*o.Width*o.Height {
				t.Fatalf("overlay %dx%d at %d,%d (%d bytes) outside the %dx%d frame", o.Width, o.Height, o.X, o.Y, o.Pixels.Len(), w, h)
			}
			got := canvas(o, w, h)
			req := parse(t, tc.options)
			j, buf := job(t, src, req, transform.FormatPNG)
			defer buf.Release()
			if req.Pipelines[0].Watermarks[0].Source != nil {
				badge := newBuffer(t, readFixture(t, watermarkBadge))
				defer badge.Release()
				j.Watermarks = []exec.WatermarkImage{{Source: badge, Format: "png"}}
			}
			res, err := e.Run(t.Context(), j)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Release()
			want, _ := decode(t, res.Body.Bytes())
			nrgba, ok := want.(*image.NRGBA)
			if !ok {
				t.Fatalf("decoded %T", want)
			}
			comparePixels(t, got, nrgba)
		})
	}
}

// comparePixels fails when a and b differ by more than rounding: alpha
// everywhere, colour where the pixel is visible.
func comparePixels(t *testing.T, a, b *image.NRGBA) {
	t.Helper()
	if a.Rect != b.Rect {
		t.Fatalf("sizes %v and %v", a.Rect, b.Rect)
	}
	var worst, bad int
	for i := 0; i < len(a.Pix); i += 4 {
		d := absDiff8(a.Pix[i+3], b.Pix[i+3])
		if min(a.Pix[i+3], b.Pix[i+3]) > 32 {
			for c := range 3 {
				d = max(d, absDiff8(a.Pix[i+c], b.Pix[i+c]))
			}
		}
		worst = max(worst, d)
		if d > 4 {
			bad++
		}
	}
	if bad > len(a.Pix)/4/1000 || worst > 48 {
		t.Errorf("%d pixels differ by more than 4 (worst %d)", bad, worst)
	}
}

func absDiff8(a, b uint8) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

// TestRenderOverlayNothing: what draws nothing renders nothing.
func TestRenderOverlayNothing(t *testing.T) {
	t.Parallel()
	e := newWatermarkExecutor(t, newWatermark(t, watermarkLogo, "png", 1))
	if o := renderOverlay(t, e, "wm:1:ce/"+wmt("<b></b>")); o != nil {
		o.Release()
		t.Errorf("empty text rendered a %dx%d overlay", o.Width, o.Height)
	}
	// Placed far outside, a watermark keeps one pixel inside (imgproxy
	// calcPosition with overflow), clipped to it.
	if o := renderOverlay(t, e, "wm:1:nowe:400:400/"+wmu); o == nil || o.Width != 1 || o.Height != 1 || o.X != 199 || o.Y != 149 {
		t.Errorf("overflowing overlay %+v, want the corner pixel", o)
	} else {
		o.Release()
	}
	transparent := &transform.Watermark{Text: "x", Position: transform.Gravity{Type: transform.GravityCenter}}
	if o, err := e.RenderOverlay(t.Context(), transparent, nil, frameW, frameH); o != nil || err != nil {
		t.Errorf("opacity 0: %v, %v", o, err)
	}
	// Without a configured watermark, the default overlay is skipped.
	if o := renderOverlay(t, newExecutor(t), "wm:1:ce"); o != nil {
		o.Release()
		t.Error("the missing configured watermark rendered")
	}
}

// TestRenderOverlayErrors: hostile markup is refused as a client error,
// never passed on.
func TestRenderOverlayErrors(t *testing.T) {
	t.Parallel()
	e := newExecutor(t)
	for _, tc := range []struct {
		text string
		kind errs.Kind
	}{
		{`<span font_file="/etc/passwd">x</span>`, errs.KindInvalidURL},
		{`<img src="x"/>`, errs.KindInvalidURL},
		{`<!DOCTYPE x [<!ENTITY a "b">]>&a;`, errs.KindInvalidURL},
		{`<span size="100000pt">x</span>`, errs.KindLimitExceeded},
	} {
		wm := &transform.Watermark{Opacity: 1, Text: tc.text, Position: transform.Gravity{Type: transform.GravityCenter}}
		o, err := e.RenderOverlay(t.Context(), wm, nil, 200, 150)
		if o != nil {
			o.Release()
		}
		if errs.KindOf(err) != tc.kind {
			t.Errorf("%q: %v, want %s", tc.text, err, tc.kind)
		}
	}
	wm := &transform.Watermark{Opacity: 1, Source: &transform.SourceSpec{Raw: "x.png"}}
	if _, err := e.RenderOverlay(t.Context(), wm, nil, 200, 150); errs.KindOf(err) != errs.KindInternal {
		t.Errorf("image overlay without its image: %v", err)
	}
	if _, err := e.RenderOverlay(t.Context(), &transform.Watermark{Opacity: 1}, nil, 0, 150); errs.KindOf(err) != errs.KindInternal {
		t.Errorf("empty frame: %v", err)
	}
}

// TestRenderOverlayNoLeak: rendering and releasing overlays leaves no
// libvips object behind, and libvips's tracked memory does not grow with
// the number of overlays (each is 300 KiB of pixels; the threadpool's
// per-thread buffers come and go on their own, so a little slack is
// allowed).
func TestRenderOverlayNoLeak(t *testing.T) {
	e := newExecutor(t)
	wm := &transform.Watermark{
		Opacity: 0.7, Text: "<b>leak</b>", Tile: transform.TileRepeat,
		Position: transform.Gravity{Type: transform.GravityCenter, XOffset: 4, YOffset: 4},
	}
	render := func() {
		o, err := e.RenderOverlay(t.Context(), wm, nil, 320, 240)
		if err != nil || o == nil {
			t.Fatalf("RenderOverlay: %v", err)
		}
		o.Release()
		o.Release() // idempotent
	}
	render()
	objects, mem := vips.LiveObjects(), vips.ReadMemStats().Mem
	for range 50 {
		render()
	}
	if n := vips.LiveObjects(); n > objects {
		t.Errorf("libvips objects %d → %d over 50 overlays", objects, n)
	}
	if m := vips.ReadMemStats().Mem; m > mem+2<<20 {
		t.Errorf("libvips tracked memory grew %d → %d bytes over 50 overlays", mem, m)
	}
}
