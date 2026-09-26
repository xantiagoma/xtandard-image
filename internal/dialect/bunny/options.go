package bunny

import (
	"encoding/hex"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Reasons recorded in Request.Ignored.
const (
	reasonUnknown      = "unknown option"
	reasonNotEnforced  = "not enforced"
	reasonApproximated = "approximated"
)

// Expectation phrases (client-safe, static).
const (
	expCrop        = "width,height or width,height,x,y in pixels"
	expFocusCrop   = "width,height,x,y with pixel or 0-1 relative x,y"
	expFaceCrop    = "true, false or width,height"
	expAspectRatio = "width:height with positive numbers, or auto"
	expGravity     = "center, north, south, east, west, northeast, northwest, southeast, southwest"
	expUpscaling   = "off or resampling"
	expFormat      = "webp, jpeg, png, gif or avif"
	expOptimize    = "low, medium or high"
	expBool        = "true or false"
	expRotate      = "-270, -180, -90, 0, 90, 180 or 270"
	expTint        = "hex color rrggbb"
)

// Option parameters, in the order Bunny applies them (Dynamic Images API
// "Transformation order"): crop operations, resize, reflections and
// rotations, filters, colour and luminosity, format and quality.
var optionOrder = []string{
	"crop", "focus_crop", "face_crop", "crop_gravity", "aspect_ratio",
	"width", "height", "upscaling",
	"flip", "flop", "rotate",
	"blur", "sharpen",
	"brightness", "gamma", "contrast", "saturation", "hue", "tint", "sepia",
	"format", "output", "quality", "auto_optimize",
}

// classParam selects a Bunny image class (an xi preset).
const classParam = "class"

// isOption reports whether key is an Optimizer option (class excluded).
func isOption(key string) bool { return slices.Contains(optionOrder, key) }

// apply maps the Optimizer parameters onto b (docs/URL-DIALECTS.md §9).
func (p *Parser) apply(b *imgproxy.Builder, params []sign.BunnyParam) error {
	last := make(map[string]string, len(params))
	var class string
	hasClass := false
	for _, kv := range params {
		switch {
		case kv.Key == classParam:
			class, hasClass = kv.Value, true
		case isOption(kv.Key):
			last[kv.Key] = kv.Value
		default:
			if err := p.other(b, kv.Key); err != nil {
				return err
			}
		}
	}
	if s := p.o.Presets; s != nil {
		if s.Only() && len(last) > 0 {
			return &errs.Error{Kind: errs.KindForbidden, Public: "only image classes are allowed", Err: imgproxy.ErrOnlyPresets}
		}
		if _, ok := s.Default(); ok {
			if err := b.Apply("preset", "default"); err != nil {
				return err
			}
		}
	}
	if hasClass {
		if err := b.Apply("preset", class); err != nil {
			return err
		}
	}
	for _, key := range optionOrder {
		v, ok := last[key]
		if !ok {
			continue
		}
		if err := option(b, key, v, last); err != nil {
			return err
		}
	}
	if _, ok := last["aspect_ratio"]; ok {
		aspectToFill(b)
	}
	return nil
}

// aspectToFill rewrites the common aspect_ratio=W:H&width=W&height=H (what
// unpic generates) as a fill resize: cropping the source to W:H around the
// gravity and fitting it into W×H without upscaling is imgproxy's
// fill-down (fill with upscaling=resampling). Any other aspect_ratio stays
// a crop aspect ratio (Crop.AspectRatio).
func aspectToFill(b *imgproxy.Builder) {
	pl := b.Pipeline()
	c, rs := pl.Crop, &pl.Resize
	if c == nil || c.AspectRatio == 0 {
		return
	}
	w, h := rs.Width.Px, rs.Height.Px
	if c.Width.IsZero() && c.Height.IsZero() && w > 0 && h > 0 && math.Abs(c.AspectRatio*float64(h)-float64(w)) < 0.5 {
		pl.Crop = nil
		rs.Type = transform.ResizeFillDown
		if rs.Enlarge {
			rs.Type = transform.ResizeFill
		}
	}
}

// other handles a parameter that is not an Optimizer option: signing
// parameters are skipped (verify handled them), the restrictions Bunny
// enforces at its edge are recorded as not enforced, anything else is
// unknown.
func (p *Parser) other(b *imgproxy.Builder, key string) error {
	switch key {
	case sign.BunnyTokenParam, sign.BunnyExpiresParam, sign.BunnyPathTokenParam,
		sign.BunnyTokenPathParam, sign.BunnyIgnoreParamsParam:
		return nil
	case "optimizer": // optimizer=image forces processing; implicit here
		return nil
	case "token_countries", "token_countries_blocked", "limit":
		b.Ignore(key, reasonNotEnforced)
		return nil
	}
	if p.o.Strict {
		return dialect.UnknownOption(key)
	}
	b.Ignore(key, reasonUnknown)
	return nil
}

// option applies one Optimizer option. all holds every option value, for
// the ones that depend on another (auto_optimize on quality).
//
//nolint:gocyclo // one flat switch over the parameter table is the clearest form
func option(b *imgproxy.Builder, key, v string, all map[string]string) error {
	pl := b.Pipeline()
	switch key {
	// Crop operations.
	case "crop":
		return crop(b, v)
	case "focus_crop":
		return focusCrop(b, v)
	case "face_crop":
		return faceCrop(b, v)
	case "crop_gravity":
		code, ok := gravityCode(v)
		if !ok {
			return dialect.InvalidArgument(key, v, expGravity)
		}
		return b.Apply("gravity", code)
	case "aspect_ratio":
		return aspectRatio(pl, v)

	// Resize.
	case "width", "height":
		n, err := parseInt(key, v, 0, transform.MaxDimension)
		if err != nil {
			return err
		}
		return b.Apply(key, strconv.Itoa(n))
	case "upscaling":
		switch v {
		case "off":
			pl.Resize.Enlarge = false
		case "resampling":
			pl.Resize.Enlarge = true
		default:
			return dialect.InvalidArgument(key, v, expUpscaling)
		}

	// Reflections and rotations.
	case "flip":
		return parseBool(key, v, &pl.Flip.V)
	case "flop":
		return parseBool(key, v, &pl.Flip.H)
	case "rotate":
		n, err := strconv.Atoi(v)
		if err != nil || n%90 != 0 || n < -270 || n > 270 {
			return dialect.InvalidArgument(key, v, expRotate)
		}
		pl.Rotate = (n%360 + 360) % 360

	// Filters.
	case "blur":
		f, err := parseScale(key, v, 0)
		if err != nil {
			return err
		}
		pl.Blur = f / 4
	case "sharpen":
		return sharpen(b, v)

	// Colour and luminosity (Bunny's -100..100 scales).
	case "brightness":
		return adjust(key, v, -100, &pl.Adjust.Brightness, func(f float64) float64 { return f * 2.55 })
	case "contrast":
		return adjust(key, v, -100, &pl.Adjust.Contrast, func(f float64) float64 { return 1 + f/100 })
	case "saturation":
		return adjust(key, v, -100, &pl.Adjust.Saturation, func(f float64) float64 { return 1 + f/100 })
	case "gamma":
		return adjust(key, v, -100, &pl.Adjust.Gamma, func(f float64) float64 { return math.Exp2(f / 100) })
	case "hue":
		// "Every 33 units shifts to the next primary colour": 100 = 360°.
		return adjust(key, v, 0, &pl.Adjust.Hue, func(f float64) float64 { return f * 3.6 })
	case "tint":
		return tint(b, v)
	case "sepia":
		f, err := parseScale(key, v, 0)
		if err != nil {
			return err
		}
		pl.Sepia = f > 0

	// Format and quality.
	case "format", "output":
		switch strings.ToLower(v) {
		case "webp", "jpeg", "jpg", "png", "gif", "avif":
			return b.Apply("format", strings.ToLower(v))
		}
		return dialect.InvalidArgument(key, v, expFormat)
	case "quality":
		n, err := parseInt(key, v, 0, transform.MaxQuality)
		if err != nil {
			return err
		}
		return b.Apply("quality", strconv.Itoa(n))
	case "auto_optimize":
		q, ok := map[string]int{"low": 85, "medium": 75, "high": 65}[v]
		if !ok {
			return dialect.InvalidArgument(key, v, expOptimize)
		}
		if _, explicit := all["quality"]; !explicit {
			b.Request().Output.Quality = q
		}
	}
	return nil
}

// gravityCode maps Bunny crop_gravity values (and the CSS-style aliases
// unpic's FocusArea type allows) to imgproxy compass codes.
func gravityCode(v string) (string, bool) {
	code, ok := map[string]string{
		"center": "ce", "centre": "ce",
		"north": "no", "top": "no",
		"south": "so", "bottom": "so",
		"east": "ea", "right": "ea",
		"west": "we", "left": "we",
		"northeast": "noea", "top_right": "noea",
		"northwest": "nowe", "top_left": "nowe",
		"southeast": "soea", "bottom_right": "soea",
		"southwest": "sowe", "bottom_left": "sowe",
	}[v]
	return code, ok
}

// crop is crop=w,h (anchored by crop_gravity, default centre) or
// crop=w,h,x,y (x,y = top-left corner), in source pixels.
func crop(b *imgproxy.Builder, v string) error {
	args := strings.Split(v, ",")
	n := make([]int, len(args))
	for i, a := range args {
		x, err := strconv.Atoi(a)
		if err != nil || x < 0 || x > transform.MaxDimension || (i < 2 && x == 0) {
			return dialect.InvalidArgument("crop", v, expCrop)
		}
		n[i] = x
	}
	switch len(n) {
	case 2:
		return b.Apply("crop", args[0], args[1])
	case 4:
		return b.Apply("crop", args[0], args[1], "nowe", args[2], args[3])
	}
	return dialect.InvalidArgument("crop", v, expCrop)
}

// focusCrop is focus_crop=w,h,x,y: a w×h crop centred on the focal point
// (x,y), shifted inwards at the borders. Relative x,y (0..1, written with
// a decimal point) become a focus-point gravity; pixel x,y place the
// crop's top-left corner at (x-w/2, y-h/2), which the planner clamps to
// the image like Bunny does.
func focusCrop(b *imgproxy.Builder, v string) error {
	args := strings.Split(v, ",")
	if len(args) != 4 {
		return dialect.InvalidArgument("focus_crop", v, expFocusCrop)
	}
	w, werr := strconv.Atoi(args[0])
	h, herr := strconv.Atoi(args[1])
	if werr != nil || herr != nil || w <= 0 || h <= 0 || w > transform.MaxDimension || h > transform.MaxDimension {
		return dialect.InvalidArgument("focus_crop", v, expFocusCrop)
	}
	if strings.Contains(args[2], ".") || strings.Contains(args[3], ".") {
		x, xerr := strconv.ParseFloat(args[2], 64)
		y, yerr := strconv.ParseFloat(args[3], 64)
		if xerr != nil || yerr != nil || !(x >= 0 && x <= 1) || !(y >= 0 && y <= 1) {
			return dialect.InvalidArgument("focus_crop", v, expFocusCrop)
		}
		return b.Apply("crop", args[0], args[1], "fp", args[2], args[3])
	}
	x, xerr := strconv.Atoi(args[2])
	y, yerr := strconv.Atoi(args[3])
	if xerr != nil || yerr != nil || x < 0 || y < 0 || x > transform.MaxDimension || y > transform.MaxDimension {
		return dialect.InvalidArgument("focus_crop", v, expFocusCrop)
	}
	return b.Apply("crop", args[0], args[1], "nowe", strconv.Itoa(max(0, x-w/2)), strconv.Itoa(max(0, y-h/2)))
}

// faceCrop is face_crop=true|false or face_crop=w,h (Bunny Optimizer):
// true crops the requested size on the faces (face gravity, WP-72), w,h
// crops a w×h area of the source centred on the faces before resizing.
// Without faces the smart crop places it; without the detector (slim) it
// answers 422.
func faceCrop(b *imgproxy.Builder, v string) error {
	if on, err := strconv.ParseBool(v); err == nil {
		if on {
			b.Pipeline().Gravity = transform.Gravity{Type: transform.GravityFace}
		}
		return nil
	}
	w, h, ok := strings.Cut(v, ",")
	wi, werr := strconv.Atoi(w)
	hi, herr := strconv.Atoi(h)
	if !ok || werr != nil || herr != nil || wi <= 0 || hi <= 0 || wi > transform.MaxDimension || hi > transform.MaxDimension {
		return dialect.InvalidArgument("face_crop", v, expFaceCrop)
	}
	if err := b.Apply("crop", w, h, "ce"); err != nil {
		return err
	}
	b.Pipeline().Crop.Gravity = transform.Gravity{Type: transform.GravityFace}
	return nil
}

// aspectRatio is aspect_ratio=w:h, a crop of the source (or of crop=) to
// that ratio anchored by crop_gravity; "auto" is the default (none).
func aspectRatio(pl *transform.Pipeline, v string) error {
	if v == "auto" {
		return nil
	}
	w, h, ok := strings.Cut(v, ":")
	wf, werr := strconv.ParseFloat(w, 64)
	hf, herr := strconv.ParseFloat(h, 64)
	if !ok || werr != nil || herr != nil || !(wf > 0) || !(hf > 0) || math.IsInf(wf, 0) || math.IsInf(hf, 0) {
		return dialect.InvalidArgument("aspect_ratio", v, expAspectRatio)
	}
	r := wf / hf
	if !(r > 0) || r > transform.MaxFactor || r < 1/transform.MaxFactor {
		return dialect.InvalidArgument("aspect_ratio", v, expAspectRatio)
	}
	c := transform.Crop{}
	if pl.Crop != nil {
		c = *pl.Crop
	}
	c.AspectRatio = r
	pl.Crop = &c
	return nil
}

// sharpen is sharpen=true|false; Bunny's examples also use sharpen=<n>,
// which is approximated by the same fixed sharpening.
func sharpen(b *imgproxy.Builder, v string) error {
	pl := b.Pipeline()
	if on, err := strconv.ParseBool(v); err == nil {
		pl.Sharpen = 0
		if on {
			pl.Sharpen = 1
		}
		return nil
	}
	f, err := parseScale("sharpen", v, 0)
	if err != nil {
		return dialect.InvalidArgument("sharpen", v, expBool)
	}
	pl.Sharpen = 0
	if f > 0 {
		pl.Sharpen = 1
		b.Ignore("sharpen", reasonApproximated)
	}
	return nil
}

// adjust sets a colour adjustment from Bunny's scale; 0 is the identity.
func adjust(key, v string, lo float64, dst **float64, conv func(float64) float64) error {
	f, err := parseScale(key, v, lo)
	if err != nil {
		return err
	}
	if f == 0 {
		*dst = nil
		return nil
	}
	x := conv(f)
	*dst = &x
	return nil
}

// tint is tint=rrggbb (with or without "#"); ffffff means no tint.
func tint(b *imgproxy.Builder, v string) error {
	rgb, err := hex.DecodeString(strings.TrimPrefix(v, "#"))
	if err != nil || len(rgb) != 3 {
		return dialect.InvalidArgument("tint", v, expTint)
	}
	pl := b.Pipeline()
	if rgb[0] == 0xff && rgb[1] == 0xff && rgb[2] == 0xff {
		pl.Tint = nil
		return nil
	}
	pl.Tint = &transform.Color{R: rgb[0], G: rgb[1], B: rgb[2], A: 255}
	return nil
}

func parseInt(field, v string, lo, hi int) (int, error) {
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		return 0, dialect.InvalidArgument(field, v, "integer "+strconv.Itoa(lo)+"-"+strconv.Itoa(hi))
	}
	return n, nil
}

// parseScale parses a number on one of Bunny's scales, lo..100.
func parseScale(field, v string, lo float64) (float64, error) {
	const hi = 100
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || !(f >= lo && f <= hi) {
		return 0, dialect.InvalidArgument(field, v,
			"number "+strconv.FormatFloat(lo, 'g', -1, 64)+" to "+strconv.FormatFloat(hi, 'g', -1, 64))
	}
	return f, nil
}

func parseBool(field, v string, dst *bool) error {
	on, err := strconv.ParseBool(v)
	if err != nil {
		return dialect.InvalidArgument(field, v, expBool)
	}
	*dst = on
	return nil
}
