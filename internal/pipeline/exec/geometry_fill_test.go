//go:build cgo

package exec_test

import (
	"bytes"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/pipeline/exec"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

const (
	bordered  = "png/bordered.png" // 56x42 photo, white margins 6 (top), 20 (right), 12 (bottom), 4 (left)
	multipage = "tiff/multipage.tif"
)

// fillCases are the WP-26b golden cases: trim, extend, extend_aspect_ratio
// and padding on stills and per frame on animations, disable_animation and
// the page of a multi-page file. TestAgainstImgproxyFill compares them with
// imgproxy.
var fillCases = []goldenCase{
	// Trim: auto colour (top-left pixel), explicit colour, equal_hor and
	// equal_ver (cut the smaller of the two opposite margins on both sides).
	{name: "trim-bordered", fixture: bordered, options: "t:10", out: transform.FormatPNG, w: 56, h: 42},
	{name: "trim-bordered-color", fixture: bordered, options: "t:10:FFFFFF", out: transform.FormatPNG, w: 56, h: 42},
	{name: "trim-equal-hor", fixture: bordered, options: "t:10:FFFFFF:1:0", out: transform.FormatPNG, w: 72, h: 42},
	{name: "trim-equal-ver", fixture: bordered, options: "t:10:FFFFFF:0:1", out: transform.FormatPNG, w: 56, h: 48},
	{name: "trim-equal-both", fixture: bordered, options: "t:10::1:1", out: transform.FormatPNG, w: 72, h: 48},
	{name: "trim-fill", fixture: bordered, options: "t:10/rs:fill:40:40", out: transform.FormatPNG, w: 40, h: 40},

	// Extend and extend_aspect_ratio with gravity and offsets, padding with
	// DPR.
	{name: "extend-offsets", fixture: jpegPhoto, options: "rs:fit:200:200/ex:1:soea:10:20/bg:0:0:0", out: transform.FormatPNG, w: 200, h: 200},
	{name: "extend-ar-south", fixture: jpegPhoto, options: "rs:fit:100:100/exar:1:so", out: transform.FormatPNG, w: 100, h: 100},
	{name: "extend-ar-alpha", fixture: pngAlpha, options: "rs:fit:120:60/exar:1:ea", out: transform.FormatPNG, w: 120, h: 60},
	{name: "padding-dpr", fixture: pngAlpha, options: "rs:fit:32:32/pd:4:6/dpr:2", out: transform.FormatPNG, w: 88, h: 64},

	// Animations: every frame is extended or padded; delays and loop are
	// checked by TestAnimationTiming.
	{name: "gif-extend-transparent", fixture: gifAnim, options: "rs:fit:40:40/ex:1:so", out: transform.FormatGIF, w: 40, h: 40, frames: 6},
	{name: "gif-padding", fixture: gifAnim, options: "rs:fit:32:32/pd:4:8/bg:0:0:255", out: transform.FormatGIF, w: 48, h: 32, frames: 6},
	{name: "webp-anim-extend-ar", fixture: webpAnim, options: "rs:fit:40:40/exar:1", out: transform.FormatWebP, w: 40, h: 40, frames: 6},
	{name: "webp-anim-padding", fixture: webpAnim, options: "rs:fit:32:32/pd:3", out: transform.FormatWebP, w: 38, h: 30, frames: 6},
	{name: "webp-anim-extend-offset", fixture: webpAnim, options: "rs:fit:48:48/ex:1:nowe:4:2/bg:255:255:0", out: transform.FormatWebP, w: 48, h: 48, frames: 6},

	// disable_animation keeps the first frame only (imgproxy Pro).
	{name: "gif-disable-animation", fixture: gifAnim, options: "rs:fit:48:48/da:1", out: transform.FormatGIF, w: 48, h: 36, noImgproxy: true},
	{name: "webp-disable-animation", fixture: webpAnim, options: "da:1", out: transform.FormatWebP, w: 96, h: 72, noImgproxy: true},

	// The second page of a multi-page TIFF has its own size (imgproxy's
	// page option is Pro).
	{name: "tiff-page-1", fixture: multipage, options: "pg:1", out: transform.FormatPNG, w: 96, h: 72, noImgproxy: true},
	{name: "tiff-page-1-fit", fixture: multipage, options: "pg:1/rs:fit:48:48", out: transform.FormatPNG, w: 48, h: 36, noImgproxy: true},
	{name: "tiff-page-0", fixture: multipage, options: "rs:fit:80:80", out: transform.FormatPNG, w: 80, h: 60, noImgproxy: true},
}

func TestGoldenFill(t *testing.T) {
	t.Parallel()
	for _, tc := range fillCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := process(t, tc.fixture, tc.options, tc.out)
			checkOutput(t, &tc, &o)
			golden.Compare(t, "exec/"+tc.name, o.img, golden.Options{Tolerance: tc.tolerance})
		})
	}
}

// TestTrimBottomRight trims with the colour of the bottom-right pixel
// (thumbor trim:bottom-right) on a source whose corners differ: white at
// the top left, blue at the bottom right.
func TestTrimBottomRight(t *testing.T) {
	t.Parallel()
	src := process(t, bordered, "pd:0:10:10:0/bg:0:0:255", transform.FormatPNG)
	if w, h := src.res.Width, src.res.Height; w != 90 || h != 70 {
		t.Fatalf("source %dx%d, want 90x70", w, h)
	}
	e := newExecutor(t)
	for _, tc := range []struct {
		name        string
		bottomRight bool
		equal       bool
		w, h        int
	}{
		// The top-left white: the blue bands are content and reach every
		// edge (the bottom one spans the full width), so nothing is cut.
		{name: "trim-top-left", w: 90, h: 70},
		// The bottom-right blue: the white margins stay.
		{name: "trim-bottom-right", bottomRight: true, w: 80, h: 60},
		// Nothing is blue on the top and left: equal cuts keep everything.
		{name: "trim-bottom-right-equal", bottomRight: true, equal: true, w: 90, h: 70},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := parse(t, "t:10")
			tr := req.Pipelines[0].Trim
			tr.ColorFromBottomRight = tc.bottomRight
			tr.EqualHor, tr.EqualVer = tc.equal, tc.equal
			o := processRequest(t, e, src.bytes, req, transform.FormatPNG)
			if o.res.Width != tc.w || o.res.Height != tc.h {
				t.Errorf("size = %dx%d, want %dx%d", o.res.Width, o.res.Height, tc.w, tc.h)
			}
			golden.Compare(t, "exec/"+tc.name, o.img, golden.Options{})
		})
	}
}

// processRequest runs req on data with e and decodes the result.
func processRequest(tb testing.TB, e *exec.Executor, data []byte, req *transform.Request, out transform.Format) output {
	tb.Helper()
	j, buf := job(tb, data, req, out)
	defer buf.Release()
	res, err := e.Run(tb.Context(), j)
	if err != nil {
		tb.Fatalf("run: %v", err)
	}
	defer res.Body.Release()
	enc := bytes.Clone(res.Body.Bytes())
	img, frames := decode(tb, enc)
	return output{res: res, img: img, frames: frames, bytes: enc}
}
