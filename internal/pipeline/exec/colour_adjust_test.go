//go:build cgo

package exec_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// The colour options of imgproxy (adjust, monochrome, duotone, colorize,
// gradient, background_alpha) are imgproxy Pro features: open-source
// imgproxy cannot serve them, so none of these cases runs in
// TestAgainstImgproxy.

// pipelineFunc sets pipeline fields the imgproxy grammar has no option for
// (they come from the other dialects: gamma, hue, tint, sepia, ...).
type pipelineFunc func(p *transform.Pipeline)

// processReq is process with the request parsed from options and then
// changed by set.
func processReq(tb testing.TB, data []byte, options string, set pipelineFunc, out transform.Format) output {
	tb.Helper()
	req := parse(tb, options)
	if set != nil {
		set(&req.Pipelines[0])
		req.ApplyDefaults(transform.StandardDefaults())
		if err := req.Validate(); err != nil {
			tb.Fatalf("validate: %v", err)
		}
	}
	j, buf := job(tb, data, req, out)
	defer buf.Release()
	res, err := newExecutor(tb).Run(tb.Context(), j)
	if err != nil {
		tb.Fatalf("run %q: %v", options, err)
	}
	defer res.Body.Release()
	enc := bytes.Clone(res.Body.Bytes())
	img, frames := decode(tb, enc)
	return output{res: res, img: img, frames: frames, bytes: enc}
}

// solidPNG encodes a w×h PNG of one colour; opaque colours are stored
// without an alpha channel.
func solidPNG(tb testing.TB, w, h int, c color.NRGBA) []byte {
	tb.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, c)
		}
	}
	return encodePNG(tb, img)
}

// encodePNG encodes img as PNG (opaque images without an alpha channel).
func encodePNG(tb testing.TB, img image.Image) []byte {
	tb.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		tb.Fatal(err)
	}
	return b.Bytes()
}

func rgb(r, g, b uint8) color.NRGBA     { return color.NRGBA{R: r, G: g, B: b, A: 255} }
func rgba(r, g, b, a uint8) color.NRGBA { return color.NRGBA{R: r, G: g, B: b, A: a} }

// TestColourMaths checks the exact channel values each operation gives on
// a solid colour, end to end (URL options or pipeline fields, libvips,
// PNG). Values are worked out by hand from the definitions in
// colour_math.go; tol allows ±1 where libvips composites in float.
func TestColourMaths(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		in      color.NRGBA
		options string
		set     pipelineFunc
		want    color.NRGBA
		tol     int
	}{
		// brightness: v + b.
		{name: "brightness +10", in: rgb(100, 100, 100), options: "br:10", want: rgb(110, 110, 110)},
		{name: "brightness clamps", in: rgb(100, 150, 200), options: "br:-120", want: rgb(0, 30, 80)},
		{name: "brightness keeps alpha", in: rgba(100, 150, 200, 77), options: "br:55", want: rgba(155, 205, 255, 77)},
		// contrast: (v − 128) × k + 128.
		{name: "contrast 1.5", in: rgb(100, 150, 200), options: "co:1.5", want: rgb(86, 161, 236)},
		{name: "contrast 0", in: rgb(100, 150, 200), options: "co:0", want: rgb(128, 128, 128)},
		// saturation: CSS saturate(); 0 is the Rec. 709 luma 142.98.
		{name: "saturation 0", in: rgb(100, 150, 200), options: "sa:0", want: rgb(143, 143, 143)},
		{name: "saturation 2", in: rgb(100, 150, 200), options: "sa:2", want: rgb(57, 157, 255)},
		{name: "saturation keeps alpha", in: rgba(100, 150, 200, 77), options: "sa:0", want: rgba(143, 143, 143, 77)},
		// adjust: brightness, then contrast, then saturation.
		{name: "adjust", in: rgb(100, 150, 200), options: "a:10:1.5:0", want: rgb(165, 165, 165)},
		// gamma g: 255 × (v/255)^(1/g).
		{name: "gamma 2", in: rgb(64, 128, 255), set: func(p *transform.Pipeline) { p.Adjust.Gamma = new(2.0) }, want: rgb(128, 181, 255)},
		// exposure e: linear light × e (100 → 0.12743 × 2 → 138).
		{name: "exposure 2", in: rgb(100, 100, 100), set: func(p *transform.Pipeline) { p.Adjust.Exposure = new(2.0) }, want: rgb(138, 138, 138)},
		// channel multipliers.
		{name: "red 1.5", in: rgb(100, 150, 200), set: func(p *transform.Pipeline) { p.Adjust.Red = new(1.5) }, want: rgb(150, 150, 200)},
		// hue: CSS hue-rotate(); red turned 180° is (−146, 109, 109).
		{name: "hue 180", in: rgb(255, 0, 0), set: func(p *transform.Pipeline) { p.Adjust.Hue = new(180.0) }, want: rgb(0, 109, 109)},
		{name: "grayscale", in: rgb(100, 150, 200), set: func(p *transform.Pipeline) { p.Grayscale = true }, want: rgb(143, 143, 143)},
		// sepia: weserv's matrix.
		{name: "sepia", in: rgb(100, 150, 200), set: func(p *transform.Pipeline) { p.Sepia = true }, want: rgb(169, 141, 113)},
		{name: "negate", in: rgb(100, 150, 200), set: func(p *transform.Pipeline) { p.Negate = true }, want: rgb(155, 105, 55)},
		{name: "negate keeps alpha", in: rgba(100, 150, 200, 9), set: func(p *transform.Pipeline) { p.Negate = true }, want: rgba(155, 105, 55, 9)},
		// tint keeps black and white neutral.
		{name: "tint white", in: rgb(255, 255, 255), set: func(p *transform.Pipeline) { p.Tint = &transform.Color{R: 255, A: 255} }, want: rgb(255, 255, 255)},
		{name: "tint black", in: rgb(0, 0, 0), set: func(p *transform.Pipeline) { p.Tint = &transform.Color{R: 255, A: 255} }, want: rgb(0, 0, 0)},
		// monochrome b3b3b3 is plain greyscale; intensity mixes it in.
		{name: "monochrome", in: rgb(100, 150, 200), options: "mc:1", want: rgb(143, 143, 143)},
		{name: "monochrome 0.4", in: rgb(100, 150, 200), options: "mc:0.4", want: rgb(117, 147, 177)},
		{name: "monochrome keeps white", in: rgb(255, 255, 255), options: "mc:1:ff0000", want: rgb(255, 255, 255)},
		// duotone: shadow at black, highlight at white.
		{name: "duotone shadow", in: rgb(0, 0, 0), options: "dt:1:ff0000:0000ff", want: rgb(255, 0, 0)},
		{name: "duotone highlight", in: rgb(255, 255, 255), options: "dt:1:ff0000:0000ff", want: rgb(0, 0, 255)},
		{name: "duotone half", in: rgb(255, 255, 255), options: "dt:0.25:000:000", want: rgb(191, 191, 191)},
		// colorize: v × (1 − o) + c × o.
		{name: "colorize", in: rgb(100, 160, 200), options: "col:0.25:ff0000", want: rgb(139, 120, 150)},
		{name: "colorize keep alpha", in: rgba(100, 160, 200, 128), options: "col:0.25:ff0000:1", want: rgba(139, 120, 150, 128)},
		// colorize over a translucent pixel: α' = 0.25 + 0.502 × 0.75.
		{name: "colorize over alpha", in: rgba(100, 160, 200, 128), options: "col:0.25:ff0000", want: rgba(162, 96, 120, 160), tol: 1},
		{name: "colorize over transparent", in: rgba(0, 0, 0, 0), options: "col:0.5:00ff00", want: rgba(0, 255, 0, 128), tol: 1},
		// opacity multiplies alpha, adding one.
		{name: "opacity", in: rgb(100, 150, 200), set: func(p *transform.Pipeline) { p.Adjust.Opacity = new(0.5) }, want: rgba(100, 150, 200, 128)},
		{name: "opacity of alpha", in: rgba(100, 150, 200, 200), set: func(p *transform.Pipeline) { p.Adjust.Opacity = new(0.25) }, want: rgba(100, 150, 200, 50)},
		// background_alpha: the image over a translucent background.
		{name: "background alpha transparent", in: rgba(0, 0, 0, 0), options: "bg:ff0000/bga:0.5", want: rgba(255, 0, 0, 128)},
		{name: "background alpha opaque", in: rgb(1, 2, 3), options: "bg:ff0000/bga:0.5", want: rgb(1, 2, 3)},
		{name: "background alpha half", in: rgba(0, 0, 255, 128), options: "bg:ff0000/bga:0.5", want: rgba(85, 0, 170, 192), tol: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := processReq(t, solidPNG(t, 4, 4, tc.in), tc.options, tc.set, transform.FormatPNG)
			for _, p := range []image.Point{{0, 0}, {3, 3}} {
				got := o.img.At(p.X, p.Y).(color.NRGBA) //nolint:forcetypeassert // decode returns *image.NRGBA
				if !near(got, tc.want, tc.tol) {
					t.Errorf("pixel %v = %v, want %v (±%d)", p, got, tc.want, tc.tol)
				}
			}
			if len(o.res.Skipped) != 0 {
				t.Errorf("Skipped = %q", o.res.Skipped)
			}
		})
	}
}

// TestGradientMaths checks the gradient ramp on a white 1×4 or 4×1 image:
// t at the pixel centres is 0.125, 0.375, 0.625, 0.875.
func TestGradientMaths(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		w, h    int
		options string
		want    []uint8 // green channel along the ramp
	}{
		{"down", 1, 4, "gr:1:ff0000", []uint8{223, 159, 96, 32}},
		{"up", 1, 4, "gr:1:ff0000:up", []uint8{32, 96, 159, 223}},
		{"right", 4, 1, "gr:1:ff0000:right", []uint8{223, 159, 96, 32}},
		{"left", 4, 1, "gr:1:ff0000:left", []uint8{32, 96, 159, 223}},
		{"angle 180 is down", 1, 4, "gr:1:ff0000:180", []uint8{223, 159, 96, 32}},
		{"start and stop", 1, 4, "gr:1:ff0000:down:0.5:1", []uint8{255, 255, 191, 64}},
		{"opacity", 1, 4, "gr:0.5:ff0000:down:0:0.25", []uint8{191, 127, 127, 127}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := processReq(t, solidPNG(t, tc.w, tc.h, rgb(255, 255, 255)), tc.options, nil, transform.FormatPNG)
			for i, want := range tc.want {
				x, y := 0, i
				if tc.h == 1 {
					x, y = i, 0
				}
				got := o.img.At(x, y).(color.NRGBA) //nolint:forcetypeassert // decode returns *image.NRGBA
				if !near(got, rgb(255, want, want), 1) {
					t.Errorf("pixel %d = %v, want green and blue %d", i, got, want)
				}
			}
		})
	}
}

func near(a, b color.NRGBA, tol int) bool {
	d := func(x, y uint8) bool { return max(int(x)-int(y), int(y)-int(x)) <= tol }
	return d(a.R, b.R) && d(a.G, b.G) && d(a.B, b.B) && d(a.A, b.A)
}

// colourCases are the golden cases of every colour option on a photo, a
// PNG with graded alpha and an animated GIF.
var colourCases = []struct {
	goldenCase
	set pipelineFunc
}{
	{name: "colour-brightness", fixture: jpegPhoto, options: "rs:fit:160:160/br:40", out: transform.FormatPNG, w: 160, h: 120},
	{name: "colour-contrast", fixture: jpegPhoto, options: "rs:fit:160:160/co:1.6", out: transform.FormatPNG, w: 160, h: 120},
	{name: "colour-saturation", fixture: jpegPhoto, options: "rs:fit:160:160/sa:1.8", out: transform.FormatPNG, w: 160, h: 120},
	{name: "colour-adjust", fixture: jpegPhoto, options: "rs:fit:160:160/a:-20:1.2:0.5", out: transform.FormatPNG, w: 160, h: 120},
	{
		name: "colour-gamma", fixture: jpegPhoto, options: "rs:fit:160:160", out: transform.FormatPNG, w: 160, h: 120,
		set: func(p *transform.Pipeline) { p.Adjust.Gamma = new(1.8) },
	},
	{
		name: "colour-hue", fixture: jpegPhoto, options: "rs:fit:160:160", out: transform.FormatPNG, w: 160, h: 120,
		set: func(p *transform.Pipeline) { p.Adjust.Hue = new(120.0) },
	},
	{name: "colour-monochrome", fixture: jpegPhoto, options: "rs:fit:160:160/mc:0.8:2060c0", out: transform.FormatPNG, w: 160, h: 120},
	{name: "colour-duotone", fixture: jpegPhoto, options: "rs:fit:160:160/dt:1:c83658:d8e74f", out: transform.FormatPNG, w: 160, h: 120},
	{name: "colour-colorize", fixture: jpegPhoto, options: "rs:fit:160:160/col:0.4:0080ff", out: transform.FormatPNG, w: 160, h: 120},
	{name: "colour-gradient", fixture: jpegPhoto, options: "rs:fit:160:160/gr:0.8:000000:down:0.3:1", out: transform.FormatPNG, w: 160, h: 120},
	{name: "colour-gradient-angle", fixture: jpegPhoto, options: "rs:fit:160:160/gr:1:ff00ff:45:0:0.8", out: transform.FormatPNG, w: 160, h: 120},
	{
		name: "colour-tint", fixture: jpegPhoto, options: "rs:fit:160:160", out: transform.FormatPNG, w: 160, h: 120,
		set: func(p *transform.Pipeline) { p.Tint = &transform.Color{R: 255, G: 160, B: 40, A: 255} },
	},
	{
		name: "colour-sepia", fixture: jpegPhoto, options: "rs:fit:160:160", out: transform.FormatPNG, w: 160, h: 120,
		set: func(p *transform.Pipeline) { p.Sepia = true },
	},
	{
		name: "colour-negate", fixture: jpegPhoto, options: "rs:fit:160:160", out: transform.FormatPNG, w: 160, h: 120,
		set: func(p *transform.Pipeline) { p.Negate = true },
	},
	{
		name: "colour-grayscale", fixture: jpegPhoto, options: "rs:fit:160:160", out: transform.FormatPNG, w: 160, h: 120,
		set: func(p *transform.Pipeline) { p.Grayscale = true },
	},
	{name: "colour-jpeg-out", fixture: jpegPhoto, options: "rs:fit:160:160/a:10:1.1:1.2/dt:0.5:102030:f0e0d0", out: transform.FormatJPEG, w: 160, h: 120},

	{name: "png-colour-brightness", fixture: pngAlpha, options: "br:-60", out: transform.FormatPNG, w: 128, h: 96},
	{name: "png-colour-contrast", fixture: pngAlpha, options: "co:0.5", out: transform.FormatPNG, w: 128, h: 96},
	{name: "png-colour-saturation", fixture: pngAlpha, options: "sa:0.2", out: transform.FormatPNG, w: 128, h: 96},
	{
		name: "png-colour-gamma", fixture: pngAlpha, options: "", out: transform.FormatPNG, w: 128, h: 96,
		set: func(p *transform.Pipeline) { p.Adjust.Gamma = new(0.6) },
	},
	{
		name: "png-colour-hue", fixture: pngAlpha, options: "", out: transform.FormatPNG, w: 128, h: 96,
		set: func(p *transform.Pipeline) { p.Adjust.Hue = new(-90.0) },
	},
	{name: "png-colour-monochrome", fixture: pngAlpha, options: "mc:1", out: transform.FormatPNG, w: 128, h: 96},
	{name: "png-colour-duotone", fixture: pngAlpha, options: "dt:0.7:002040:ffd080", out: transform.FormatPNG, w: 128, h: 96},
	{name: "png-colour-colorize", fixture: pngAlpha, options: "col:0.5:ff0000", out: transform.FormatPNG, w: 128, h: 96},
	{name: "png-colour-colorize-keep-alpha", fixture: pngAlpha, options: "col:0.5:ff0000:1", out: transform.FormatPNG, w: 128, h: 96},
	{name: "png-colour-gradient", fixture: pngAlpha, options: "gr:1:0000ff:right", out: transform.FormatPNG, w: 128, h: 96},
	{
		name: "png-colour-tint", fixture: pngAlpha, options: "", out: transform.FormatPNG, w: 128, h: 96,
		set: func(p *transform.Pipeline) { p.Tint = &transform.Color{R: 40, G: 200, B: 120, A: 255} },
	},
	{
		name: "png-colour-sepia", fixture: pngAlpha, options: "", out: transform.FormatPNG, w: 128, h: 96,
		set: func(p *transform.Pipeline) { p.Sepia = true },
	},
	{
		name: "png-colour-negate", fixture: pngAlpha, options: "", out: transform.FormatPNG, w: 128, h: 96,
		set: func(p *transform.Pipeline) { p.Negate = true },
	},
	{
		name: "png-colour-grayscale", fixture: pngAlpha, options: "", out: transform.FormatPNG, w: 128, h: 96,
		set: func(p *transform.Pipeline) { p.Grayscale = true },
	},
	{
		name: "png-colour-opacity", fixture: pngAlpha, options: "", out: transform.FormatPNG, w: 128, h: 96,
		set: func(p *transform.Pipeline) { p.Adjust.Opacity = new(0.5) },
	},
	{name: "png-background-alpha", fixture: pngAlpha, options: "bg:00ff00/bga:0.4", out: transform.FormatPNG, w: 128, h: 96},

	{name: "gif-colour-duotone", fixture: gifAnim, options: "dt:1:200040:ffe0a0", out: transform.FormatGIF, w: 96, h: 72, frames: 6},
	{name: "gif-colour-gradient", fixture: gifAnim, options: "rs:fit:48:48/gr:0.9:000000:up", out: transform.FormatGIF, w: 48, h: 36, frames: 6},
	{
		name: "webp-anim-colour-negate", fixture: webpAnim, options: "rs:fill:48:32", out: transform.FormatWebP, w: 48, h: 32, frames: 6,
		set: func(p *transform.Pipeline) { p.Negate = true },
	},
}

func TestGoldenColour(t *testing.T) {
	t.Parallel()
	for _, tc := range colourCases {
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

// TestColourAnimationFrames: every frame of an animation is adjusted, and
// the frame count survives.
func TestColourAnimationFrames(t *testing.T) {
	t.Parallel()
	plain := process(t, gifAnim, "", transform.FormatGIF)
	neg := processReq(t, readFixture(t, gifAnim), "", func(p *transform.Pipeline) { p.Negate = true }, transform.FormatGIF)
	if plain.frames != 6 || neg.frames != 6 || neg.res.Frames != 6 {
		t.Fatalf("frames: plain %d, negated %d (result %d), want 6", plain.frames, neg.frames, neg.res.Frames)
	}
	// Sample the centre of every frame: negated ≈ 255 − original (GIF
	// palettes quantise, hence the tolerance).
	h := neg.res.Height
	for f := range 6 {
		x, y := neg.res.Width/2, f*h+h/2
		a := plain.img.At(x, y).(color.NRGBA) //nolint:forcetypeassert // decode returns *image.NRGBA
		b := neg.img.At(x, y).(color.NRGBA)   //nolint:forcetypeassert // decode returns *image.NRGBA
		want := rgb(255-a.R, 255-a.G, 255-a.B)
		if !near(b, want, 24) {
			t.Errorf("frame %d: %v, want ≈ %v", f, b, want)
		}
	}
}
