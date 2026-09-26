package exec

import (
	"math"

	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Colour adjustments (plan.ColourAdjustStep) are compiled here, in pure Go,
// into a short list of operations that libvips runs on 8-bit sRGB
// (colour_adjust.go):
//
//   - per-channel lookup tables (brightness, contrast, gamma, exposure, the
//     red/green/blue multipliers, negate, and colorize when the alpha band
//     does not change),
//   - 3×3 matrices (saturation, hue rotation, grayscale, sepia),
//   - palettes indexed by the pixel's luma (tint, monochrome, duotone),
//     blended with the original by the effect's intensity,
//   - overlays composited over the image (colorize over transparent pixels,
//     gradient) and the opacity multiplier of the alpha band.
//
// imgproxy's colour options (adjust, brightness, contrast, saturation,
// monochrome, duotone, colorize, gradient, background_alpha) are imgproxy
// Pro features with no open-source implementation, so the maths follows
// their documented semantics and the public definitions of the other
// dialects: CSS Filter Effects (saturate, hue-rotate, grayscale luma
// weights), weserv/sharp (sepia matrix, tint, duotone in CIELAB).

// Luma weights of the grey used by grayscale and the palettes (Rec. 709 on
// the sRGB values, as CSS grayscale()).
const lumaR, lumaG, lumaB = 0.2126, 0.7152, 0.0722

// colourOpKind selects what a colourOp does.
type colourOpKind uint8

const (
	opLUT      colourOpKind = iota // lut maps each colour band
	opMatrix                       // m recombines the colour bands
	opPalette                      // pal maps the luma, mixed by mix
	opOverlay                      // colour composited over with a constant alpha
	opGradient                     // colour composited over with a linear ramp
	opOpacity                      // the alpha band is multiplied by amount
)

// colourOp is one libvips operation of a colour adjustment.
type colourOp struct {
	kind   colourOpKind
	lut    *[3][256]uint8
	m      [9]float64
	pal    *[256][3]uint8
	mix    float64 // opPalette: 1 replaces the colour; opOverlay: the overlay's opacity
	colour transform.Color
	grad   *transform.Gradient
	amount float64 // opOpacity
}

// colourOps compiles s for an image with (alpha) or without an alpha band.
// Adjacent tables and adjacent matrices are merged into one.
func colourOps(s *plan.ColourAdjustStep, alpha bool) []colourOp {
	var ops []colourOp
	a := &s.Adjust
	if lut, ok := toneLUT(a); ok {
		ops = appendLUT(ops, lut)
	}
	if a.Saturation != nil {
		ops = appendMatrix(ops, saturateMatrix(*a.Saturation))
	}
	if a.Hue != nil {
		ops = appendMatrix(ops, hueRotateMatrix(*a.Hue))
	}
	if s.Grayscale {
		ops = appendMatrix(ops, [9]float64{lumaR, lumaG, lumaB, lumaR, lumaG, lumaB, lumaR, lumaG, lumaB})
	}
	if s.Sepia {
		ops = appendMatrix(ops, sepiaMatrix)
	}
	if s.Tint != nil {
		ops = append(ops, colourOp{kind: opPalette, pal: tintPalette(*s.Tint), mix: 1})
	}
	if m := s.Monochrome; m != nil && m.Intensity > 0 {
		ops = append(ops, colourOp{kind: opPalette, pal: monochromePalette(m.Color), mix: min(m.Intensity, 1)})
	}
	if d := s.Duotone; d != nil && d.Intensity > 0 {
		ops = append(ops, colourOp{kind: opPalette, pal: duotonePalette(d.Shadow, d.Highlight), mix: min(d.Intensity, 1)})
	}
	if s.Negate {
		ops = appendLUT(ops, channelLUT(func(_ int, v float64) float64 { return 255 - v }))
	}
	if c := s.Colorize; c != nil && c.Opacity > 0 {
		o := min(c.Opacity, 1)
		if alpha && !c.KeepAlpha {
			ops = append(ops, colourOp{kind: opOverlay, colour: c.Color, mix: o})
		} else {
			// Without an alpha band to change, the overlay is a blend of
			// every channel towards the colour.
			rgb := [3]float64{float64(c.Color.R), float64(c.Color.G), float64(c.Color.B)}
			ops = appendLUT(ops, channelLUT(func(ch int, v float64) float64 { return v*(1-o) + rgb[ch]*o }))
		}
	}
	if g := s.Gradient; g != nil && g.Opacity > 0 {
		ops = append(ops, colourOp{kind: opGradient, colour: g.Color, grad: g})
	}
	if a.Opacity != nil && *a.Opacity < 1 {
		ops = append(ops, colourOp{kind: opOpacity, amount: max(*a.Opacity, 0)})
	}
	return ops
}

func appendLUT(ops []colourOp, lut *[3][256]uint8) []colourOp {
	if n := len(ops); n > 0 && ops[n-1].kind == opLUT {
		prev := ops[n-1].lut
		merged := new([3][256]uint8)
		for c := range 3 {
			for v := range 256 {
				merged[c][v] = lut[c][prev[c][v]]
			}
		}
		ops[n-1].lut = merged
		return ops
	}
	return append(ops, colourOp{kind: opLUT, lut: lut})
}

func appendMatrix(ops []colourOp, m [9]float64) []colourOp {
	if n := len(ops); n > 0 && ops[n-1].kind == opMatrix {
		ops[n-1].m = mulMatrix(m, ops[n-1].m)
		return ops
	}
	return append(ops, colourOp{kind: opMatrix, m: m})
}

// mulMatrix is a × b: b applies first.
func mulMatrix(a, b [9]float64) [9]float64 {
	var out [9]float64
	for r := range 3 {
		for c := range 3 {
			for k := range 3 {
				out[3*r+c] += a[3*r+k] * b[3*k+c]
			}
		}
	}
	return out
}

// channelLUT tabulates f(channel, value) for the three colour channels,
// rounded to nearest and clamped to 0..255.
func channelLUT(f func(ch int, v float64) float64) *[3][256]uint8 {
	lut := new([3][256]uint8)
	for c := range 3 {
		for v := range 256 {
			lut[c][v] = clamp8(f(c, float64(v)))
		}
	}
	return lut
}

// toneLUT is the per-channel curve of the Adjust fields that act on each
// channel alone, applied in order and clamped after each:
//
//   - brightness b: v + b (imgproxy brightness, −255..255),
//   - contrast k: (v − 128) × k + 128 (k = 1 unchanged),
//   - gamma g: 255 × (v/255)^(1/g) (g > 1 brightens, as vips_gamma),
//   - exposure e: the linear-light value × e (e = 2 is one stop brighter),
//   - red, green, blue: v × multiplier.
//
// ok is false when none is set.
func toneLUT(a *transform.Adjust) (*[3][256]uint8, bool) {
	if a.Brightness == nil && a.Contrast == nil && a.Gamma == nil && a.Exposure == nil &&
		a.Red == nil && a.Green == nil && a.Blue == nil {
		return nil, false
	}
	mult := [3]*float64{a.Red, a.Green, a.Blue}
	return channelLUT(func(ch int, v float64) float64 {
		if b := a.Brightness; b != nil {
			v = clampF(v + *b)
		}
		if k := a.Contrast; k != nil {
			v = clampF((v-128)**k + 128)
		}
		if g := a.Gamma; g != nil && *g > 0 {
			v = 255 * math.Pow(v/255, 1 / *g)
		}
		if e := a.Exposure; e != nil {
			v = 255 * linearToSRGB(min(srgbToLinear(v/255)**e, 1))
		}
		if m := mult[ch]; m != nil {
			v = clampF(v * *m)
		}
		return v
	}), true
}

// saturateMatrix is CSS saturate(s): s = 0 is grey, 1 unchanged, > 1
// oversaturates, keeping the luma.
func saturateMatrix(s float64) [9]float64 {
	return [9]float64{
		lumaR + (1-lumaR)*s, lumaG - lumaG*s, lumaB - lumaB*s,
		lumaR - lumaR*s, lumaG + (1-lumaG)*s, lumaB - lumaB*s,
		lumaR - lumaR*s, lumaG - lumaG*s, lumaB + (1-lumaB)*s,
	}
}

// hueRotateMatrix is CSS hue-rotate(deg).
func hueRotateMatrix(deg float64) [9]float64 {
	sin, cos := math.Sincos(deg * math.Pi / 180)
	return [9]float64{
		0.213 + cos*0.787 - sin*0.213, 0.715 - cos*0.715 - sin*0.715, 0.072 - cos*0.072 + sin*0.928,
		0.213 - cos*0.213 + sin*0.143, 0.715 + cos*0.285 + sin*0.140, 0.072 - cos*0.072 - sin*0.283,
		0.213 - cos*0.213 - sin*0.787, 0.715 - cos*0.715 + sin*0.715, 0.072 + cos*0.928 + sin*0.072,
	}
}

// sepiaMatrix is weserv's (wsrv filt=sepia).
var sepiaMatrix = [9]float64{
	0.3588, 0.7044, 0.1368,
	0.2990, 0.5870, 0.1140,
	0.2392, 0.4696, 0.0912,
}

// tintPalette is sharp's tint (wsrv tint): every grey level keeps its
// CIELAB lightness and takes the colour's a*b*, weighted towards the
// midtones so black and white stay neutral.
func tintPalette(c transform.Color) *[256][3]uint8 {
	_, ca, cb := srgbToLab(c)
	return palette(func(l float64) (float64, float64) {
		w := 1 - 4*(l/100-0.5)*(l/100-0.5)
		return w * ca, w * cb
	})
}

// monochromePalette is imgproxy's monochrome palette built from one colour:
// every grey level keeps its CIELAB lightness and takes the colour's a*b*,
// fully at the colour's own lightness (where the palette is the colour) and
// fading linearly to neutral at black and white. The default b3b3b3 gives
// plain greyscale.
func monochromePalette(c transform.Color) *[256][3]uint8 {
	cl, ca, cb := srgbToLab(c)
	return palette(func(l float64) (float64, float64) {
		var w float64
		switch {
		case l <= cl && cl > 0:
			w = l / cl
		case l > cl && cl < 100:
			w = (100 - l) / (100 - cl)
		default:
			w = 1
		}
		return w * ca, w * cb
	})
}

// palette maps each grey level g (sRGB g, g, g) to the sRGB colour of its
// CIELAB lightness L and the chroma ab(L).
func palette(ab func(l float64) (a, b float64)) *[256][3]uint8 {
	pal := new([256][3]uint8)
	for g := range 256 {
		l, _, _ := srgbToLab(transform.Color{R: uint8(g), G: uint8(g), B: uint8(g), A: 255})
		a, b := ab(l)
		pal[g] = labToSRGB(l, a, b)
	}
	return pal
}

// duotonePalette maps the grey levels linearly in CIELAB from shadow (0)
// to highlight (255), like weserv's duotone.
func duotonePalette(shadow, highlight transform.Color) *[256][3]uint8 {
	l0, a0, b0 := srgbToLab(shadow)
	l1, a1, b1 := srgbToLab(highlight)
	pal := new([256][3]uint8)
	for g := range 256 {
		t := float64(g) / 255
		pal[g] = labToSRGB(l0+(l1-l0)*t, a0+(a1-a0)*t, b0+(b1-b0)*t)
	}
	pal[0] = [3]uint8{shadow.R, shadow.G, shadow.B}
	pal[255] = [3]uint8{highlight.R, highlight.G, highlight.B}
	return pal
}

// gradientRamp returns ax, ay and b such that ax·x + ay·y + b, at pixel
// (x, y) of a w×h frame, is 255 × (t − start)/(stop − start), where t runs
// from 0 to 1 across the frame along the direction (at pixel centres, like
// CSS linear-gradient: the corners reach 0 and 1). The gradient goes from
// transparent at start to the colour at stop. Angles are CSS angles:
// 0 = up (bottom → top), 90 = right, 180 = down, 270 = left.
func gradientRamp(g *transform.Gradient, w, h int) (ax, ay, b float64) {
	var deg float64
	switch g.Direction {
	case transform.DirectionUp:
		deg = 0
	case transform.DirectionRight:
		deg = 90
	case transform.DirectionLeft:
		deg = 270
	case transform.DirectionAngle:
		deg = g.Angle
	default: // down
		deg = 180
	}
	sin, cos := math.Sincos(deg * math.Pi / 180)
	dx, dy := snap(sin), snap(-cos)
	fw, fh := float64(w), float64(h)
	length := math.Abs(fw*dx) + math.Abs(fh*dy)
	span := g.Stop - g.Start
	if math.Abs(span) < 1e-6 {
		span = math.Copysign(1e-6, span)
	}
	k := 255 / (length * span)
	// t = ((x + 0.5 − w/2)·dx + (y + 0.5 − h/2)·dy)/length + 0.5
	ax, ay = dx*k, dy*k
	b = ((0.5-fw/2)*dx+(0.5-fh/2)*dy)*k + (0.5-g.Start)*255/span
	return ax, ay, b
}

// snap removes the rounding error of sin/cos at multiples of 90°.
func snap(v float64) float64 {
	if math.Abs(v) < 1e-12 {
		return 0
	}
	return v
}

func clampF(v float64) float64 { return min(max(v, 0), 255) }

func clamp8(v float64) uint8 { return uint8(math.Round(clampF(v))) }

// sRGB ↔ CIELAB (D65), the conversions libvips uses.

func srgbToLinear(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func linearToSRGB(v float64) float64 {
	if v <= 0.0031308 {
		return 12.92 * v
	}
	return 1.055*math.Pow(v, 1/2.4) - 0.055
}

const whiteX, whiteY, whiteZ = 0.950470, 1.0, 1.088827

func labF(t float64) float64 {
	if t > 216.0/24389 {
		return math.Cbrt(t)
	}
	return (24389.0/27*t + 16) / 116
}

func labFInv(t float64) float64 {
	if t3 := t * t * t; t3 > 216.0/24389 {
		return t3
	}
	return (116*t - 16) * 27 / 24389
}

func srgbToLab(c transform.Color) (l, a, b float64) {
	r := srgbToLinear(float64(c.R) / 255)
	g := srgbToLinear(float64(c.G) / 255)
	bl := srgbToLinear(float64(c.B) / 255)
	x := (0.4124564*r + 0.3575761*g + 0.1804375*bl) / whiteX
	y := (0.2126729*r + 0.7151522*g + 0.0721750*bl) / whiteY
	z := (0.0193339*r + 0.1191920*g + 0.9503041*bl) / whiteZ
	fx, fy, fz := labF(x), labF(y), labF(z)
	return 116*fy - 16, 500 * (fx - fy), 200 * (fy - fz)
}

func labToSRGB(l, a, b float64) [3]uint8 {
	fy := (l + 16) / 116
	fx := fy + a/500
	fz := fy - b/200
	x, y, z := labFInv(fx)*whiteX, labFInv(fy)*whiteY, labFInv(fz)*whiteZ
	r := 3.2404542*x - 1.5371385*y - 0.4985314*z
	g := -0.9692660*x + 1.8760108*y + 0.0415560*z
	bl := 0.0556434*x - 0.2040259*y + 1.0572252*z
	enc := func(v float64) uint8 { return clamp8(255 * linearToSRGB(min(max(v, 0), 1))) }
	return [3]uint8{enc(r), enc(g), enc(bl)}
}
