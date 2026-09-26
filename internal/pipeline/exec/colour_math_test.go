package exec

import (
	"math"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

func TestColourOpsCompile(t *testing.T) {
	t.Parallel()
	kinds := func(ops []colourOp) []colourOpKind {
		out := make([]colourOpKind, len(ops))
		for i := range ops {
			out[i] = ops[i].kind
		}
		return out
	}
	red := transform.Color{R: 255, A: 255}
	for _, tc := range []struct {
		name  string
		step  plan.ColourAdjustStep
		alpha bool
		want  []colourOpKind
	}{
		{"nothing", plan.ColourAdjustStep{}, false, []colourOpKind{}},
		{"identity opacity", plan.ColourAdjustStep{Adjust: transform.Adjust{Opacity: new(1.0)}}, false, []colourOpKind{}},
		{"tone curves and negate merge", plan.ColourAdjustStep{Adjust: transform.Adjust{Brightness: new(5.0), Gamma: new(2.0)}, Negate: true}, false, []colourOpKind{opLUT}},
		{"matrices merge", plan.ColourAdjustStep{Adjust: transform.Adjust{Saturation: new(2.0), Hue: new(10.0)}, Grayscale: true, Sepia: true}, false, []colourOpKind{opMatrix}},
		{"order", plan.ColourAdjustStep{
			Adjust: transform.Adjust{Contrast: new(2.0), Saturation: new(0.5), Opacity: new(0.5)}, Tint: &red,
			Monochrome: &transform.Monochrome{Intensity: 1}, Duotone: &transform.Duotone{Intensity: 0.5},
			Negate: true, Gradient: &transform.Gradient{Opacity: 1, Stop: 1},
		}, false, []colourOpKind{opLUT, opMatrix, opPalette, opPalette, opPalette, opLUT, opGradient, opOpacity}},
		{"zero intensities are off", plan.ColourAdjustStep{
			Monochrome: &transform.Monochrome{}, Duotone: &transform.Duotone{}, Colorize: &transform.Colorize{},
			Gradient: &transform.Gradient{},
		}, true, []colourOpKind{}},
		{"colorize opaque is a table", plan.ColourAdjustStep{Colorize: &transform.Colorize{Opacity: 0.5}}, false, []colourOpKind{opLUT}},
		{"colorize keep alpha is a table", plan.ColourAdjustStep{Colorize: &transform.Colorize{Opacity: 0.5, KeepAlpha: true}}, true, []colourOpKind{opLUT}},
		{"colorize alpha is an overlay", plan.ColourAdjustStep{Colorize: &transform.Colorize{Opacity: 0.5}}, true, []colourOpKind{opOverlay}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tc.want, kinds(colourOps(&tc.step, tc.alpha))); diff != "" {
				t.Errorf("ops (-want +got):\n%s", diff)
			}
		})
	}
}

// TestToneLUT pins the per-channel curves on single values.
func TestToneLUT(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		a    transform.Adjust
		in   uint8
		want [3]uint8
	}{
		{"brightness", transform.Adjust{Brightness: new(10.0)}, 100, [3]uint8{110, 110, 110}},
		{"brightness then contrast", transform.Adjust{Brightness: new(200.0), Contrast: new(2.0)}, 100, [3]uint8{255, 255, 255}},
		{"contrast", transform.Adjust{Contrast: new(0.5)}, 0, [3]uint8{64, 64, 64}},
		{"gamma", transform.Adjust{Gamma: new(0.5)}, 128, [3]uint8{64, 64, 64}},
		{"exposure", transform.Adjust{Exposure: new(0.5)}, 255, [3]uint8{188, 188, 188}},
		{"channels", transform.Adjust{Red: new(0.0), Green: new(2.0), Blue: new(0.5)}, 100, [3]uint8{0, 200, 50}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lut, ok := toneLUT(&tc.a)
			if !ok {
				t.Fatal("no table")
			}
			if got := [3]uint8{lut[0][tc.in], lut[1][tc.in], lut[2][tc.in]}; got != tc.want {
				t.Errorf("%d → %v, want %v", tc.in, got, tc.want)
			}
		})
	}
	if _, ok := toneLUT(&transform.Adjust{Saturation: new(2.0)}); ok {
		t.Error("saturation alone made a tone table")
	}
}

func TestMatrices(t *testing.T) {
	t.Parallel()
	identity := [9]float64{1, 0, 0, 0, 1, 0, 0, 0, 1}
	approx := cmp.Comparer(func(a, b float64) bool { return math.Abs(a-b) < 1e-3 })
	for _, tc := range []struct {
		name string
		got  [9]float64
		want [9]float64
	}{
		{"saturate 1", saturateMatrix(1), identity},
		{"hue 0", hueRotateMatrix(0), identity},
		{"hue 360", hueRotateMatrix(360), identity},
		{"merge applies b first", mulMatrix(saturateMatrix(0), saturateMatrix(1)), saturateMatrix(0)},
	} {
		if diff := cmp.Diff(tc.want, tc.got, approx); diff != "" {
			t.Errorf("%s (-want +got):\n%s", tc.name, diff)
		}
	}
	// Every row of saturate(0) is the luma: rows sum to 1, so grey stays.
	for r := range 3 {
		m := saturateMatrix(0)
		if s := m[3*r] + m[3*r+1] + m[3*r+2]; math.Abs(s-1) > 1e-9 {
			t.Errorf("saturate(0) row %d sums to %v", r, s)
		}
	}
}

// TestPalettes: neutral monochrome and tint colours give plain greyscale,
// black and white stay neutral, the monochrome colour is hit at its own lightness and duotone
// ends on its two colours.
func TestPalettes(t *testing.T) {
	t.Parallel()
	grey := transform.Color{R: 0xb3, G: 0xb3, B: 0xb3, A: 255}
	neutral := [...]*[256][3]uint8{monochromePalette(grey), tintPalette(grey)}
	for i, pal := range neutral {
		for g := range 256 {
			v := uint8(g)
			if d := pal[g]; d != [3]uint8{v, v, v} {
				t.Errorf("neutral palette %d: %d → %v", i, g, d)
				break
			}
		}
	}
	red := transform.Color{R: 255, A: 255}
	for name, pal := range map[string]*[256][3]uint8{"tint": tintPalette(red), "monochrome": monochromePalette(red)} {
		if pal[0] != [3]uint8{0, 0, 0} || pal[255] != [3]uint8{255, 255, 255} {
			t.Errorf("%s: black → %v, white → %v", name, pal[0], pal[255])
		}
	}
	// The grey level whose lightness is the colour's maps to the colour.
	c := transform.Color{R: 40, G: 90, B: 160, A: 255}
	cl, _, _ := srgbToLab(c)
	best, bestD := 0, math.Inf(1)
	for g := range 256 {
		l, _, _ := srgbToLab(transform.Color{R: uint8(g), G: uint8(g), B: uint8(g)})
		if d := math.Abs(l - cl); d < bestD {
			best, bestD = g, d
		}
	}
	got := monochromePalette(c)[best]
	for i, want := range [3]uint8{c.R, c.G, c.B} {
		if d := int(got[i]) - int(want); d < -3 || d > 3 {
			t.Errorf("monochrome at L=%.1f: %v, want ≈ %v", cl, got, c)
			break
		}
	}
	d := duotonePalette(red, transform.Color{B: 255, A: 255})
	if d[0] != [3]uint8{255, 0, 0} || d[255] != [3]uint8{0, 0, 255} {
		t.Errorf("duotone ends: %v, %v", d[0], d[255])
	}
}

func TestLabRoundTrip(t *testing.T) {
	t.Parallel()
	for _, c := range []transform.Color{{}, {R: 255, G: 255, B: 255}, {R: 255}, {G: 255}, {B: 255}, {R: 12, G: 200, B: 99}} {
		if got := labToSRGB(srgbToLab(c)); got != [3]uint8{c.R, c.G, c.B} {
			t.Errorf("%v → %v", c, got)
		}
	}
}

// TestGradientRamp evaluates the ramp at pixel centres: 255 × (t − start)
// / (stop − start) with t from 0 to 1 along the direction.
func TestGradientRamp(t *testing.T) {
	t.Parallel()
	eval := func(g transform.Gradient, w, h, x, y int) float64 {
		ax, ay, b := gradientRamp(&g, w, h)
		return ax*float64(x) + ay*float64(y) + b
	}
	for _, tc := range []struct {
		name string
		g    transform.Gradient
		w, h int
		x, y int
		want float64
	}{
		{"down top", transform.Gradient{Stop: 1}, 10, 4, 3, 0, 31.875},
		{"down bottom", transform.Gradient{Stop: 1}, 10, 4, 3, 3, 223.125},
		{"up top", transform.Gradient{Direction: transform.DirectionUp, Stop: 1}, 10, 4, 0, 0, 223.125},
		{"right", transform.Gradient{Direction: transform.DirectionRight, Stop: 1}, 4, 10, 1, 7, 95.625},
		{"left", transform.Gradient{Direction: transform.DirectionLeft, Stop: 1}, 4, 10, 1, 7, 159.375},
		// imgproxy angles: clockwise, 0 = top → bottom, 90 = right → left.
		{"angle 0 is down", transform.Gradient{Direction: transform.DirectionAngle, Stop: 1}, 10, 4, 3, 3, 223.125},
		{"angle 90 is left", transform.Gradient{Direction: transform.DirectionAngle, Angle: 90, Stop: 1}, 4, 10, 1, 7, 159.375},
		{"angle 180 is up", transform.Gradient{Direction: transform.DirectionAngle, Angle: 180, Stop: 1}, 10, 4, 0, 0, 223.125},
		{"angle -90 is right", transform.Gradient{Direction: transform.DirectionAngle, Angle: -90, Stop: 1}, 4, 10, 1, 7, 95.625},
		{"start stop", transform.Gradient{Start: 0.5, Stop: 1}, 1, 4, 0, 3, 191.25},
		// 45° (top-right → bottom-left) on a square: the bottom-left pixel
		// centre is 1/(2·8) from the end.
		{"diagonal", transform.Gradient{Direction: transform.DirectionAngle, Angle: 45, Stop: 1}, 4, 4, 0, 3, 255 * 7.0 / 8},
		{"equal start and stop is a step", transform.Gradient{Start: 0.5, Stop: 0.5}, 1, 4, 0, 3, math.Inf(1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := eval(tc.g, tc.w, tc.h, tc.x, tc.y)
			if math.IsInf(tc.want, 1) {
				if got < 255 {
					t.Errorf("got %v, want ≥ 255", got)
				}
				return
			}
			if math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
