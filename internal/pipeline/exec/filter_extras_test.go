//go:build cgo

package exec_test

import (
	"image"
	"image/color"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// unsharp_masking, blur_areas and progressive_blur are imgproxy Pro
// features: none of these cases runs in TestAgainstImgproxy.

// filterExtraCases are the golden cases of the Pro filters on a photo, a
// PNG with graded alpha and an animated GIF (filtered per frame).
var filterExtraCases = []struct {
	goldenCase
	set pipelineFunc
}{
	{name: "ush-auto", fixture: jpegPhoto, options: "rs:fit:160:160/ush:auto", out: transform.FormatPNG, w: 160, h: 120},
	{name: "ush-always-strong", fixture: jpegPhoto, options: "rs:fit:160:160/ush:always:2:12", out: transform.FormatPNG, w: 160, h: 120},
	{
		name: "usm-classic", fixture: jpegPhoto, options: "rs:fit:160:160", out: transform.FormatPNG, w: 160, h: 120,
		set: func(p *transform.Pipeline) { p.Unsharp = &transform.Unsharp{Sigma: 2.5, Amount: 1} },
	},
	{name: "blur-areas", fixture: jpegPhoto, options: "rs:fit:160:160/ba:6:0.1:0.1:0.3:0.3:0.5:0.4:0.4:0.5", out: transform.FormatPNG, w: 160, h: 120},
	{name: "progressive-blur", fixture: jpegPhoto, options: "rs:fit:160:160/pbl:8", out: transform.FormatPNG, w: 160, h: 120},
	{name: "progressive-blur-angle", fixture: jpegPhoto, options: "rs:fit:160:160/pbl:8:45:0.2:0.8", out: transform.FormatPNG, w: 160, h: 120},
	{name: "png-ush", fixture: pngAlpha, options: "ush:always:1:8", out: transform.FormatPNG, w: 128, h: 96},
	{name: "png-blur-areas", fixture: pngAlpha, options: "ba:4:0.2:0.2:0.5:0.5", out: transform.FormatPNG, w: 128, h: 96},
	{name: "png-progressive-blur", fixture: pngAlpha, options: "pbl:6:right", out: transform.FormatPNG, w: 128, h: 96},
	{name: "gif-ush", fixture: gifAnim, options: "rs:fit:48:48/ush:auto", out: transform.FormatGIF, w: 48, h: 36, frames: 6},
	{name: "gif-blur-areas", fixture: gifAnim, options: "ba:3:0:0:0.5:0.5", out: transform.FormatGIF, w: 96, h: 72, frames: 6},
	{name: "gif-progressive-blur", fixture: gifAnim, options: "pbl:5:up", out: transform.FormatGIF, w: 96, h: 72, frames: 6},
}

func TestGoldenFilterExtras(t *testing.T) {
	t.Parallel()
	for _, tc := range filterExtraCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := processReq(t, readFixture(t, tc.fixture), tc.options, tc.set, tc.out)
			checkOutput(t, &tc.goldenCase, &o)
			if len(o.res.Skipped) != 0 {
				t.Errorf("Skipped = %q", o.res.Skipped)
			}
			golden.Compare(t, "exec/"+tc.name, o.img, golden.Options{Tolerance: tc.tolerance})
		})
	}
}

// nrgba is the decoded output as *image.NRGBA (what decode returns).
func nrgba(tb testing.TB, img image.Image) *image.NRGBA {
	tb.Helper()
	n, ok := img.(*image.NRGBA)
	if !ok {
		tb.Fatalf("decoded %T, want *image.NRGBA", img)
	}
	return n
}

// stepPNG is a w×h opaque grey image, lo left of column edge and hi from
// it on.
func stepPNG(tb testing.TB, w, h, edge int, lo, hi uint8) []byte {
	tb.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			v := lo
			if x >= edge {
				v = hi
			}
			img.SetGray(x, y, color.Gray{Y: v})
		}
	}
	return encodePNG(tb, img)
}

// texturePNG is a w×h opaque RGB image of deterministic high-frequency
// texture: every pixel differs from its neighbours.
func texturePNG(tb testing.TB, w, h int) *image.NRGBA {
	tb.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, color.NRGBA{
				R: byte(x*37 + y*91), G: byte(x*x + 3*y), B: byte((x ^ y) * 5), A: 255, //nolint:gosec // G115: wrapping to a byte is the texture
			})
		}
	}
	return img
}

// TestUnsharpEdgeContrast checks the local contrast unsharp masking adds at
// a step edge. imgproxy's kernel gives the pixel beside the edge v +
// w/d × (8v − Σ neighbours); three of the eight neighbours are across the
// edge, so each side moves by 3w/d × Δ away from the other and the edge
// delta grows by exactly 1 + 6w/d. Pixels two columns away do not move.
func TestUnsharpEdgeContrast(t *testing.T) {
	t.Parallel()
	const w, h, edge, lo, hi = 16, 8, 8, 64, 192
	data := stepPNG(t, w, h, edge, lo, hi)
	for _, tc := range []struct {
		options string
		gain    float64 // 1 + 6w/d
	}{
		{"ush:always", 1.25},         // w 1, d 24
		{"ush:always:1:12", 1.5},     // w 1, d 12
		{"ush:always:0.5:24", 1.125}, // w 0.5, d 24
	} {
		t.Run(tc.options, func(t *testing.T) {
			t.Parallel()
			o := processData(t, data, tc.options, transform.FormatPNG)
			img := nrgba(t, o.img)
			grey := func(x, y int) int { return int(img.NRGBAAt(x, y).R) }
			for y := range h {
				a, b := grey(edge-1, y), grey(edge, y)
				want := tc.gain * (hi - lo)
				if got := float64(b - a); got < want-1 || got > want+1 {
					t.Errorf("row %d: edge delta %d (%d → %d), want %.1f (×%v)", y, b-a, a, b, want, tc.gain)
				}
				if a >= lo || b <= hi {
					t.Errorf("row %d: edge %d → %d did not overshoot %d → %d", y, a, b, lo, hi)
				}
				for x := range w {
					if x < edge-1 || x > edge {
						if v, want := grey(x, y), map[bool]int{true: lo, false: hi}[x < edge]; v != want {
							t.Errorf("(%d, %d) = %d, want %d: flat areas must not change", x, y, v, want)
						}
					}
				}
			}
		})
	}

	// The classic mask (imgix usm) also raises the edge delta; with
	// amount 0 nothing is applied.
	o := processReq(t, data, "", func(p *transform.Pipeline) { p.Unsharp = &transform.Unsharp{Sigma: 1, Amount: 1} }, transform.FormatPNG)
	img := nrgba(t, o.img)
	if a, b := int(img.NRGBAAt(edge-1, 0).R), int(img.NRGBAAt(edge, 0).R); b-a <= hi-lo {
		t.Errorf("classic unsharp: edge delta %d, want > %d", b-a, hi-lo)
	}
}

// TestBlurAreasExact checks that blur areas change pixels inside the areas
// only: every pixel outside is bit-identical to the source.
func TestBlurAreasExact(t *testing.T) {
	t.Parallel()
	const w, h = 64, 48
	src := texturePNG(t, w, h)
	data := encodePNG(t, src)
	// Areas in pixels: x 16..48 × y 12..36, and x 0..8 × y 36..48.
	o := processData(t, data, "ba:3:0.25:0.25:0.5:0.5:0:0.75:0.125:0.25", transform.FormatPNG)
	got := nrgba(t, o.img)
	if got.Bounds() != src.Bounds() {
		t.Fatalf("size %v, want %v", got.Bounds(), src.Bounds())
	}
	areas := []image.Rectangle{image.Rect(16, 12, 48, 36), image.Rect(0, 36, 8, 48)}
	changed := 0
	for y := range h {
		for x := range w {
			in := false
			for _, a := range areas {
				in = in || image.Pt(x, y).In(a)
			}
			g, s := got.NRGBAAt(x, y), src.NRGBAAt(x, y)
			switch {
			case !in && g != s:
				t.Fatalf("(%d, %d) outside the areas changed: %v → %v", x, y, s, g)
			case in && g != s:
				changed++
			}
		}
	}
	if total := 32*24 + 8*12; changed < total*9/10 {
		t.Errorf("%d of %d pixels inside the areas changed, want nearly all", changed, total)
	}
}

// TestProgressiveBlurMonotonic checks that the blur grows along the
// gradient: on 1-pixel vertical stripes, the mean horizontal contrast of
// each band of rows falls from the source's at the top (start 0, no blur)
// to almost nothing at the bottom (stop 1, full sigma), never rising.
func TestProgressiveBlurMonotonic(t *testing.T) {
	t.Parallel()
	const w, h, band = 64, 128, 8
	stripes := image.NewGray(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			stripes.SetGray(x, y, color.Gray{Y: uint8(255 * (x % 2))})
		}
	}
	o := processData(t, encodePNG(t, stripes), "pbl:4", transform.FormatPNG)
	img := nrgba(t, o.img)
	contrast := func(y0 int) float64 {
		var sum int
		for y := y0; y < y0+band; y++ {
			for x := 1; x < w; x++ {
				d := int(img.NRGBAAt(x, y).R) - int(img.NRGBAAt(x-1, y).R)
				sum += max(d, -d)
			}
		}
		return float64(sum) / float64(band*(w-1))
	}
	first := contrast(0)
	if first < 200 {
		t.Errorf("top band contrast %.1f, want nearly the source's 255", first)
	}
	prev := first
	for y := band; y < h; y += band {
		c := contrast(y)
		if c > prev+0.5 {
			t.Errorf("rows %d..%d: contrast %.1f rose from %.1f", y, y+band, c, prev)
		}
		prev = c
	}
	if prev > 0.1*first {
		t.Errorf("bottom band contrast %.1f, want under 10%% of the top's %.1f", prev, first)
	}
}
