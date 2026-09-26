package imgix

import (
	"math"
	"strconv"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// --- rotation (https://docs.imgix.com/apis/rendering/rotation) ------------

// exifOps is the rotation (clockwise) and horizontal flip that display an
// image with the given EXIF orientation upright.
var exifOps = [9]struct {
	rotate int
	flipH  bool
	flipV  bool
}{
	1: {}, 2: {flipH: true}, 3: {rotate: 180}, 4: {flipV: true},
	5: {rotate: 90, flipH: true}, 6: {rotate: 90}, 7: {rotate: 270, flipH: true}, 8: {rotate: 270},
}

// applyOrientation maps orient (overrides the EXIF orientation: 1–8 as
// EXIF values, 90/180/270 as EXIF 6/3/8), rot (0–359 counter-clockwise;
// imgix zooms to hide the corners, xi fills them with the background) and
// flip.
func (st *state) applyOrientation() error {
	pl := st.pl
	if v, ok := st.get("orient"); ok {
		n, err := strconv.Atoi(v)
		switch n {
		case 90:
			n = 6
		case 180:
			n = 3
		case 270:
			n = 8
		}
		if err != nil || n < 1 || n > 8 {
			return dialect.InvalidArgument("orient", v, expOrient)
		}
		ops := exifOps[n]
		off := false
		pl.AutoRotate = &off
		pl.Rotate = ops.rotate
		pl.Flip = transform.Flip{H: ops.flipH, V: ops.flipV}
	}
	if v, ok := st.get("rot"); ok {
		deg, err := numRange("rot", v, 0, 359)
		if err != nil {
			return err
		}
		if deg != 0 {
			pl.RotateAny = -deg
			if math.Mod(deg, 90) != 0 {
				st.b.Ignore("rot", reasonM2)
			}
		}
	}
	if v, ok := st.get("flip"); ok {
		var h, vert bool
		switch v {
		case "h":
			h = true
		case "v":
			vert = true
		case "hv", "vh":
			h, vert = true, true
		default:
			return dialect.InvalidArgument("flip", v, expFlip)
		}
		pl.Flip.H = pl.Flip.H != h
		pl.Flip.V = pl.Flip.V != vert
	}
	return nil
}

// --- adjustments and stylize (M2 except blur, px and sharp) ---------------

// applyEffects maps the adjustment and stylize parameters. imgix's
// -100..100 scales become: bri → additive brightness v*255/100; con, sat,
// exp, vib → multiplier 1 + v/100; gam → multiplier 2^(v/100); hue →
// degrees. blur 0–2000 → sigma v/20, sharp 0–100 → sigma 0.5 + v/50,
// usm/usmrad → unsharp amount usm/100 with sigma usmrad (default 2.5).
func (st *state) applyEffects() error {
	pl := st.pl
	a := &pl.Adjust
	for _, c := range []struct {
		key    string
		lo, hi float64
		dst    **float64
		conv   func(float64) float64
	}{
		{"bri", -100, 100, &a.Brightness, func(v float64) float64 { return v * 255 / 100 }},
		{"con", -100, 100, &a.Contrast, linear},
		{"exp", -100, 100, &a.Exposure, linear},
		{"gam", -100, 100, &a.Gamma, func(v float64) float64 { return math.Exp2(v / 100) }},
		{"sat", -100, 100, &a.Saturation, linear},
		{"vib", -100, 100, &a.Vibrance, linear},
		{"hue", -359, 359, &a.Hue, func(v float64) float64 { return v }},
	} {
		if v, ok := st.get(c.key); ok {
			f, err := numRange(c.key, v, c.lo, c.hi)
			if err != nil {
				return err
			}
			if f == 0 { // identity
				continue
			}
			x := c.conv(f)
			*c.dst = &x
			st.b.Ignore(c.key, reasonM2)
		}
	}

	if v, ok := st.get("blur"); ok {
		f, err := numRange("blur", v, 0, 2000)
		if err != nil {
			return err
		}
		pl.Blur = f / 20
	}
	if v, ok := st.get("px"); ok {
		n, err := intRange("px", v, 0, 100)
		if err != nil {
			return err
		}
		pl.Pixelate = n
	}
	if v, ok := st.get("sharp"); ok {
		f, err := numRange("sharp", v, 0, 100)
		if err != nil {
			return err
		}
		if f > 0 {
			pl.Sharpen = 0.5 + f/50
		}
	}
	if err := st.unsharp(); err != nil {
		return err
	}
	return st.colourEffects()
}

func linear(v float64) float64 { return 1 + v/100 }

func (st *state) unsharp() error {
	v, ok := st.get("usm")
	if !ok {
		st.ignoreIf(reasonNeedsParent, "usmrad")
		_, err := st.optNum("usmrad", 0, 500, 0)
		return err
	}
	amount, err := numRange("usm", v, -100, 100)
	if err != nil {
		return err
	}
	radius, err := st.optNum("usmrad", 0, 500, 2.5)
	if err != nil {
		return err
	}
	if amount != 0 {
		st.pl.Unsharp = &transform.Unsharp{Sigma: radius, Amount: amount / 100}
	}
	return nil
}

// colourEffects maps sepia (any amount > 0 turns sepia on), monochrome
// (colour; its alpha is the intensity), duotone (+ duotone-alpha) and
// invert.
func (st *state) colourEffects() error {
	pl := st.pl
	if v, ok := st.get("sepia"); ok {
		f, err := numRange("sepia", v, 0, 100)
		if err != nil {
			return err
		}
		if f > 0 {
			pl.Sepia = true
			st.b.Ignore("sepia", reasonM2)
			if f < 100 {
				st.b.Ignore("sepia", reasonApproximated)
			}
		}
	}
	if v, ok := st.get("monochrome"); ok {
		c, err := parseColor("monochrome", v)
		if err != nil {
			return err
		}
		pl.Monochrome = &transform.Monochrome{Intensity: float64(c.A) / 255, Color: opaque(c)}
		st.b.Ignore("monochrome", reasonM2)
	}
	alpha, err := st.optNum("duotone-alpha", 0, 100, 100)
	if err != nil {
		return err
	}
	if v, ok := st.get("duotone"); ok {
		parts := list(v)
		if len(parts) != 2 {
			return dialect.InvalidArgument("duotone", v, expDuotone)
		}
		shadow, err := parseColor("duotone", parts[0])
		if err != nil {
			return err
		}
		highlight, err := parseColor("duotone", parts[1])
		if err != nil {
			return err
		}
		pl.Duotone = &transform.Duotone{Intensity: alpha / 100, Shadow: opaque(shadow), Highlight: opaque(highlight)}
		st.b.Ignore("duotone", reasonM2)
	} else {
		st.ignoreIf(reasonNeedsParent, "duotone-alpha")
	}
	if v, ok := st.get("invert"); ok {
		on, err := imgproxy.ParseBool("invert", v)
		if err != nil {
			return err
		}
		if on {
			pl.Negate = true
			st.b.Ignore("invert", reasonM2)
		}
	}
	return nil
}

func opaque(c transform.Color) transform.Color {
	c.A = 255
	return c
}
