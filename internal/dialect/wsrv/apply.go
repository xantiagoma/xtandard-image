package wsrv

import (
	"math"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Expectation phrases (client-safe, static).
const (
	expCoord  = "pixels (0 or more) or a percentage (0% to 100%)"
	expDPR    = "a number greater than 0 and at most 8"
	expFit    = "inside, outside, cover, fill or contain"
	expAlign  = "center, top, right, bottom, left, top-left, top-right, bottom-left, bottom-right, entropy, attention or focal"
	expUnit   = "a number from 0 to 1"
	expMask   = "circle, ellipse, triangle, triangle-180, pentagon, pentagon-180, hexagon, square, star, heart or none"
	expFilt   = "greyscale, sepia, duotone, negate or none"
	expOutput = "jpg, png, gif, tiff, webp, avif, jxl, json or origin"
	expInt    = "an integer"
	expNum    = "a number"
)

// wsrv fallbacks for values its processors replace (weserv
// src/api/processors).
const (
	// mildBlurSigma approximates wsrv's fast 3×3 box blur (the standard
	// deviation of a 3-tap box is sqrt(2/3)).
	mildBlurSigma = 0.8
	// mildSharpenSigma approximates wsrv's fast 3×3 sharpen kernel.
	mildSharpenSigma = 0.5
	// defaultGamma is wsrv's gamma for an empty or out-of-range gam.
	defaultGamma = 2.2
	// defaultTrim is wsrv's threshold for an empty trim.
	defaultTrim = 10
	maxDPR      = 8
	// maxModulate is the largest brightness or saturation multiplier
	// (wsrv allows 10000; transform validation caps factors).
	maxModulate = transform.MaxFactor
)

// apply maps the parameters onto the builder's request.
//
// Pipelines follow wsrv's fixed processing order (api_manager_impl.cpp):
// trim, then size (resize, EXIF/flip/flop/rotate, cover alignment) with the
// crop before it (precrop) or after it, then effects (embed for contain,
// arbitrary rotation, modulate, contrast, gamma, sharpen, filter, blur,
// tint, background, mask). A post-resize crop (the default when cx, cy,
// cw or ch is set) needs the resized image, so it starts a chained
// pipeline, which also takes the effects.
func apply(b *imgproxy.Builder, ps *params, mountBound bool) error {
	r := b.Request()
	if err := output(b, r, ps); err != nil {
		return err
	}
	if err := delivery(b, r, ps, mountBound); err != nil {
		return err
	}
	if r.Kind == transform.KindInfo {
		return nil
	}

	p0 := b.Pipeline()
	if err := geometry(b, p0, ps); err != nil {
		return err
	}
	crop, err := cropSpec(ps)
	if err != nil {
		return err
	}
	precrop := boolParam(ps, "precrop")
	fx := p0
	if crop != nil {
		if precrop {
			p0.Crop = crop
		} else {
			if err := b.NewPipeline(); err != nil {
				return err
			}
			fx = b.Pipeline()
			fx.Crop = crop
			p0 = &r.Pipelines[0]
			// contain embeds after the crop: move the extend along.
			if p0.Extend.Enabled {
				fx.Extend, p0.Extend = p0.Extend, transform.Extend{}
				fx.Resize = transform.Resize{Type: transform.ResizeFit, Width: p0.Resize.Width, Height: p0.Resize.Height, DPR: p0.Resize.DPR}
			}
		}
	}
	return effects(b, fx, ps)
}

// geometry applies size, fit, alignment, trim, orientation and pages.
func geometry(b *imgproxy.Builder, p *transform.Pipeline, ps *params) error {
	rs := &p.Resize
	var err error
	if rs.Width, err = coordParam(ps, "w"); err != nil {
		return err
	}
	if rs.Height, err = coordParam(ps, "h"); err != nil {
		return err
	}
	if v, ok := ps.get("dpr"); ok && v != "" {
		d, err := parseFloat("dpr", v, expDPR)
		if err != nil {
			return err
		}
		switch {
		case d == 0: // wsrv: dpr=0 zeroes the target, i.e. auto
		case d < 0 || d > maxDPR:
			return dialect.InvalidArgument("dpr", v, expDPR)
		default:
			rs.DPR = d
		}
	}
	rs.Enlarge = !boolParam(ps, "we") // wsrv enlarges unless &we

	g, err := gravity(ps)
	if err != nil {
		return err
	}
	if v, ok := ps.get("fit"); ok && v != "" {
		switch fitCanvas(v) {
		case canvasMax:
			rs.Type = transform.ResizeFit
		case canvasMin:
			rs.Type = transform.ResizeOutside
		case canvasCrop:
			rs.Type = transform.ResizeFill
		case canvasIgnoreAspect:
			rs.Type = transform.ResizeForce
		case canvasEmbed:
			rs.Type = transform.ResizeFit
			p.Extend = transform.Extend{Enabled: true, Gravity: extendGravity(&g), Mode: transform.ExtendBackground}
		default:
			return dialect.InvalidArgument("fit", v, expFit)
		}
	}
	if g.Type != transform.GravityUnset {
		p.Gravity = g
	}

	if v, ok := ps.get("trim"); ok {
		t := defaultTrim
		if v != "" {
			if t, err = parseInt("trim", v, "an integer from 1 to 254"); err != nil {
				return err
			}
			if t != 0 && (t < 1 || t > 254) {
				return dialect.InvalidArgument("trim", v, "an integer from 1 to 254")
			}
		}
		if t != 0 {
			p.Trim = &transform.Trim{Threshold: float64(t)}
			if c, ok, err := colorParam(ps, "tbg"); err != nil {
				return err
			} else if ok {
				p.Trim.Color = &c
			}
		}
	}

	p.Flip.V = boolParam(ps, "flip")
	p.Flip.H = boolParam(ps, "flop")
	if v, ok := ps.get("ro"); ok && v != "" {
		ro, err := parseFloat("ro", v, "degrees")
		if err != nil {
			return err
		}
		if ro = math.Trunc(ro); ro != 0 && math.Mod(ro, 90) == 0 {
			p.Rotate = int(math.Mod(ro, 360)+360) % 360
		} else if ro != 0 {
			p.RotateAny = ro // arbitrary angles are an effect; see effects
		}
	}

	if v, ok := ps.get("n"); ok && v != "" {
		n, err := parseInt("n", v, "-1 (all pages) or a positive integer")
		if err != nil {
			return err
		}
		if n != -1 && n < 1 {
			return dialect.InvalidArgument("n", v, "-1 (all pages) or a positive integer")
		}
		p.Pages = &n
	}
	if v, ok := ps.get("page"); ok && v != "" {
		pg, err := parseInt("page", v, "a page index, -1 (largest) or -2 (smallest)")
		if err != nil {
			return err
		}
		switch {
		case pg >= 0:
			p.Page = &pg
		case pg == -1 || pg == -2:
			b.Ignore("page:"+v, reasonNotImplemented)
		default:
			return dialect.InvalidArgument("page", v, "a page index, -1 (largest) or -2 (smallest)")
		}
	}
	for _, k := range []string{"loop", "delay"} {
		if ps.has(k) {
			b.Ignore(k, reasonNotImplemented)
		}
	}
	if ps.has("fsol") {
		b.Ignore("fsol", reasonNotApplicable)
	}
	return nil
}

// effects applies the phase-3 processors of wsrv to p.
func effects(b *imgproxy.Builder, p *transform.Pipeline, ps *params) error {
	r := b.Request()
	// The arbitrary rotation was parsed with the geometry.
	if p0 := &r.Pipelines[0]; p0 != p && p0.RotateAny != 0 {
		p.RotateAny, p0.RotateAny = p0.RotateAny, 0
	}

	bg, hasBG, err := colorParam(ps, "bg")
	if err != nil {
		return err
	}
	if hasBG {
		p.Background = &bg
	}
	// contain and arbitrary-rotation fills share Background with bg.
	for _, k := range []string{"cbg", "rbg"} {
		c, ok, err := colorParam(ps, k)
		if err != nil {
			return err
		}
		used := (k == "cbg" && p.Extend.Enabled) || (k == "rbg" && p.RotateAny != 0)
		switch {
		case !ok || !used:
		case p.Background == nil:
			p.Background = &c
		default:
			b.Ignore(k, reasonApproximated)
		}
	}

	a := &p.Adjust
	if v, ok := ps.get("mod.b"); ok {
		if a.Exposure, err = optRange(v, "mod", 0, maxModulate); err != nil {
			return err
		}
	}
	if v, ok := ps.get("sat"); ok {
		if a.Saturation, err = optRange(v, "sat", 0, maxModulate); err != nil {
			return err
		}
	}
	if v, ok := ps.get("hue"); ok && v != "" {
		h, err := parseFloat("hue", v, "degrees")
		if err != nil {
			return err
		}
		h = math.Mod(math.Trunc(h), 360)
		if h < 0 {
			h += 360
		}
		a.Hue = &h
	}
	if v, ok := ps.get("con"); ok && v != "" {
		c, err := parseInt("con", v, "an integer from -100 to 100")
		if err != nil {
			return err
		}
		if c < -100 || c > 100 {
			return dialect.InvalidArgument("con", v, "an integer from -100 to 100")
		}
		if c != 0 {
			// wsrv applies a sigmoidal contrast of strength con*0.3.
			m := 1 + float64(c)/100
			a.Contrast = &m
			b.Ignore("con", reasonApproximated)
		}
	}
	if v, ok := ps.get("gam"); ok {
		g := defaultGamma
		if v != "" {
			x, err := parseFloat("gam", v, expNum)
			if err != nil {
				return err
			}
			if x != 0 {
				if x >= 1 && x <= 3 {
					g = x
				}
			} else {
				g = 0
			}
		}
		if g != 0 {
			a.Gamma = &g
		}
	}

	if v, ok := ps.get("sharp"); ok {
		s := mildSharpenSigma
		mild := v == ""
		if !mild {
			x, err := parseFloat("sharp", v, expNum)
			if err != nil {
				return err
			}
			switch {
			case x == 0:
				s = 0
			case x < 0.000001 || x > 10:
				mild = true
			default:
				s = x
			}
		}
		p.Sharpen = s
		if mild {
			b.Ignore("sharp", reasonApproximated)
		}
	}
	for _, k := range []string{"sharpf", "sharpj"} {
		if v, ok := ps.get(k); ok {
			if _, err := parseFloat(k, v, expNum); err != nil && v != "" {
				return err
			}
			b.Ignore(k, reasonNotImplemented)
		}
	}

	if err := filter(p, ps); err != nil {
		return err
	}

	if v, ok := ps.get("blur"); ok {
		s := mildBlurSigma
		mild := v == ""
		if !mild {
			x, err := parseFloat("blur", v, expNum)
			if err != nil {
				return err
			}
			switch {
			case x == 0:
				s = 0
			case x < 0.3 || x > 1000:
				mild = true
			default:
				s = x
			}
		}
		p.Blur = s
		if mild {
			b.Ignore("blur", reasonApproximated)
		}
	}
	if c, ok, err := colorParam(ps, "tint"); err != nil {
		return err
	} else if ok {
		p.Tint = &c
	}

	return mask(p, ps)
}

// filter maps filt (and start/stop for duotone).
func filter(p *transform.Pipeline, ps *params) error {
	v, ok := ps.get("filt")
	if !ok {
		return nil
	}
	switch v {
	case "", "none":
	case "greyscale":
		p.Grayscale = true
	case "sepia":
		p.Sepia = true
	case "negate":
		p.Negate = true
	case "duotone":
		d := &transform.Duotone{
			Intensity: 1,
			// weserv filter.cpp defaults: #C83658 and #D8E74F.
			Shadow:    transform.Color{R: 0xC8, G: 0x36, B: 0x58, A: 255},
			Highlight: transform.Color{R: 0xD8, G: 0xE7, B: 0x4F, A: 255},
		}
		if c, ok, err := colorParam(ps, "start"); err != nil {
			return err
		} else if ok {
			d.Shadow = c
		}
		if c, ok, err := colorParam(ps, "stop"); err != nil {
			return err
		} else if ok {
			d.Highlight = c
		}
		p.Duotone = d
	default:
		return dialect.InvalidArgument("filt", v, expFilt)
	}
	return nil
}

// mask maps mask, mtrim and mbg.
func mask(p *transform.Pipeline, ps *params) error {
	v, ok := ps.get("mask")
	if !ok || v == "" || v == "none" {
		return nil
	}
	shape, ok := maskShape(v)
	if !ok {
		return dialect.InvalidArgument("mask", v, expMask)
	}
	m := &transform.Mask{Shape: shape}
	m.Trim = boolParam(ps, "mtrim")
	if c, ok, err := colorParam(ps, "mbg"); err != nil {
		return err
	} else if ok {
		m.Background = &c
	}
	p.Mask = m
	return nil
}

func maskShape(v string) (transform.MaskShape, bool) {
	switch v {
	case "circle":
		return transform.MaskCircle, true
	case "ellipse":
		return transform.MaskEllipse, true
	case "triangle":
		return transform.MaskTriangle, true
	case "triangle-180":
		return transform.MaskTriangle180, true
	case "pentagon":
		return transform.MaskPentagon, true
	case "pentagon-180":
		return transform.MaskPentagon180, true
	case "hexagon":
		return transform.MaskHexagon, true
	case "square":
		return transform.MaskSquare, true
	case "star":
		return transform.MaskStar, true
	case "heart":
		return transform.MaskHeart, true
	}
	return transform.MaskUnset, false
}

// output maps output, q, l, af, il and ll.
func output(b *imgproxy.Builder, r *transform.Request, ps *params) error {
	o := &r.Output
	if v, ok := ps.get("output"); ok && v != "" {
		switch v {
		case "origin":
		case "json":
			r.Kind = transform.KindInfo
			r.Info = &transform.InfoOptions{
				Size: true, Format: true, Dimensions: true, Colorspace: true,
				Bands: true, Alpha: true, PagesNumber: true, EXIF: true,
			}
			b.Ignore("output:json", reasonInfoSchema)
		default:
			f, ok := outputFormat(v)
			if !ok {
				return dialect.InvalidArgument("output", v, expOutput)
			}
			o.Format = f
		}
	}
	if v, ok := ps.get("q"); ok && v != "" {
		q, err := parseInt("q", v, "an integer from 1 to 100")
		if err != nil {
			return err
		}
		if q < 1 || q > transform.MaxQuality {
			return dialect.InvalidArgument("q", v, "an integer from 1 to 100")
		}
		o.Quality = q
	}
	if v, ok := ps.get("l"); ok && v != "" {
		l, err := parseInt("l", v, "an integer from 0 to 9")
		if err != nil {
			return err
		}
		if l < 0 || l > transform.MaxPNGLevel {
			return dialect.InvalidArgument("l", v, "an integer from 0 to 9")
		}
		png(o).Compression = &l
	}
	if ps.has("af") {
		af := boolParam(ps, "af")
		png(o).AdaptiveFilter = &af
	}
	if ps.has("il") {
		il := boolParam(ps, "il")
		o.Progressive = &il
	}
	if ps.has("ll") {
		ll := boolParam(ps, "ll")
		o.Lossless = &ll
	}
	return nil
}

func png(o *transform.Output) *transform.PNGOptions {
	if o.PNG == nil {
		o.PNG = &transform.PNGOptions{}
	}
	return o.PNG
}

func outputFormat(v string) (transform.Format, bool) {
	switch v {
	case "jpg", "jpeg":
		return transform.FormatJPEG, true
	case "png":
		return transform.FormatPNG, true
	case "gif":
		return transform.FormatGIF, true
	case "tiff", "tif":
		return transform.FormatTIFF, true
	case "webp":
		return transform.FormatWebP, true
	case "avif", "av1":
		return transform.FormatAVIF, true
	case "jxl":
		return transform.FormatJXL, true
	}
	return transform.FormatUnset, false
}

// delivery maps filename, maxage, encoding and default.
func delivery(b *imgproxy.Builder, r *transform.Request, ps *params, mountBound bool) error {
	d := &r.Delivery
	if v, ok := ps.get("filename"); ok && v != "" {
		if err := b.Apply("filename", v); err != nil {
			return err
		}
	}
	if v, ok := ps.get("maxage"); ok && v != "" {
		ttl, err := parseMaxAge(v)
		if err != nil {
			return err
		}
		d.TTL = ttl
	}
	if v, ok := ps.get("encoding"); ok {
		if strings.EqualFold(v, "base64") {
			d.Encoding = transform.ResponseDataURL
		} else {
			b.Ignore("encoding", reasonUnknown)
		}
	}
	if v, ok := ps.get("default"); ok && v != "" {
		d.OnErrorRedirect = true
		if v != "1" {
			spec, err := sourceSpec("default", v, mountBound)
			if err != nil {
				return err
			}
			d.Fallback = &spec
		}
	}
	return nil
}

// ---- alignment ----------------------------------------------------------

// isFocal reports whether an a= value selects the focal point (including
// the deprecated crop-X-Y form).
func isFocal(v string) bool {
	return strings.HasPrefix(v, "focal") || strings.HasPrefix(v, "crop-")
}

// gravity parses a= with fpx/fpy. Unset a= leaves the gravity unset
// (center).
func gravity(ps *params) (transform.Gravity, error) {
	v, ok := ps.get("a")
	if !ok || v == "" {
		return transform.Gravity{}, nil
	}
	var t transform.GravityType
	switch v {
	case "center", "centre":
		t = transform.GravityCenter
	case "top", "t":
		t = transform.GravityNorth
	case "right", "r":
		t = transform.GravityEast
	case "bottom", "b":
		t = transform.GravitySouth
	case "left", "l":
		t = transform.GravityWest
	case "top-left":
		t = transform.GravityNorthWest
	case "top-right":
		t = transform.GravityNorthEast
	case "bottom-left":
		t = transform.GravitySouthWest
	case "bottom-right":
		t = transform.GravitySouthEast
	case "entropy":
		t = transform.GravityEntropy
	case "attention":
		t = transform.GravitySmart
	default:
		if !isFocal(v) {
			return transform.Gravity{}, dialect.InvalidArgument("a", v, expAlign)
		}
		g := transform.Gravity{Type: transform.GravityFocusPoint, FocusX: 0.5, FocusY: 0.5}
		var err error
		if g.FocusX, err = unitParam(ps, "fpx", 0.5); err != nil {
			return g, err
		}
		if g.FocusY, err = unitParam(ps, "fpy", 0.5); err != nil {
			return g, err
		}
		return g, nil
	}
	return transform.Gravity{Type: t}, nil
}

// extendGravity is the embed position of fit=contain: content-aware
// positions do not apply to an embed and mean center (weserv embed.cpp).
func extendGravity(g *transform.Gravity) transform.Gravity {
	if g.Type == transform.GravityEntropy || g.Type == transform.GravitySmart {
		return transform.Gravity{}
	}
	return *g
}

// canvas is wsrv's fit enum (weserv enums.h Canvas).
type canvas int

const (
	canvasInvalid canvas = iota
	canvasMax
	canvasMin
	canvasCrop
	canvasEmbed
	canvasIgnoreAspect
)

// fitCanvas parses fit with the deprecated values of enumeration.h.
func fitCanvas(v string) canvas {
	switch v {
	case "inside", "fit", "fitup":
		return canvasMax
	case "outside":
		return canvasMin
	case "cover", "square", "squaredown":
		return canvasCrop
	case "contain", "letterbox":
		return canvasEmbed
	case "fill", "absolute":
		return canvasIgnoreAspect
	}
	if strings.HasPrefix(v, "crop") {
		return canvasCrop
	}
	return canvasInvalid
}

// cropSpec builds the cx/cy/cw/ch rectangle: its top-left corner at
// (cx, cy) and its size cw × ch, missing sizes meaning "to the edge"
// (weserv crop.cpp). nil when none of the four is set.
func cropSpec(ps *params) (*transform.Crop, error) {
	if !ps.has("cx") && !ps.has("cy") && !ps.has("cw") && !ps.has("ch") {
		return nil, nil
	}
	c := &transform.Crop{Gravity: transform.Gravity{Type: transform.GravityNorthWest}}
	var err error
	if c.Width, err = coordParam(ps, "cw"); err != nil {
		return nil, err
	}
	if c.Height, err = coordParam(ps, "ch"); err != nil {
		return nil, err
	}
	if c.Gravity.XOffset, err = offsetParam(ps, "cx"); err != nil {
		return nil, err
	}
	if c.Gravity.YOffset, err = offsetParam(ps, "cy"); err != nil {
		return nil, err
	}
	return c, nil
}

// ---- value parsers --------------------------------------------------------

// coordParam parses a wsrv coordinate: pixels or a percentage. Empty or
// absent is zero (auto).
func coordParam(ps *params, k string) (transform.Dim, error) {
	v, ok := ps.get(k)
	if !ok || v == "" {
		return transform.Dim{}, nil
	}
	if pct, ok := strings.CutSuffix(v, "%"); ok {
		f, err := strconv.ParseFloat(pct, 64)
		if err != nil || math.IsNaN(f) || f < 0 || f > 100 {
			return transform.Dim{}, dialect.InvalidArgument(k, v, expCoord)
		}
		return transform.Dim{Rel: f / 100}, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > transform.MaxDimension {
		return transform.Dim{}, dialect.InvalidArgument(k, v, expCoord)
	}
	return transform.Dim{Px: n}, nil
}

// offsetParam is a coordinate used as a crop offset: pixels, or a
// fraction below 1 (imgproxy offset semantics). 100% is the far edge,
// which wsrv resets to 0.
func offsetParam(ps *params, k string) (float64, error) {
	d, err := coordParam(ps, k)
	switch {
	case err != nil:
		return 0, err
	case d.Rel >= 1:
		return 0, nil
	case d.Rel > 0:
		return d.Rel, nil
	}
	return float64(d.Px), nil
}

// boolParam is wsrv's boolean: absent is false; present is true unless
// its value is "0" or "false".
func boolParam(ps *params, k string) bool {
	v, ok := ps.get(k)
	return ok && v != "0" && v != "false"
}

// colorParam parses an optional colour; ok is false when absent or empty.
func colorParam(ps *params, k string) (transform.Color, bool, error) {
	v, ok := ps.get(k)
	if !ok || v == "" {
		return transform.Color{}, false, nil
	}
	c, err := parseColor(k, v)
	if err != nil {
		return transform.Color{}, false, err
	}
	return c, true, nil
}

// unitParam parses an optional number in 0..1; def when absent or empty.
func unitParam(ps *params, k string, def float64) (float64, error) {
	v, ok := ps.get(k)
	if !ok || v == "" {
		return def, nil
	}
	f, err := parseFloat(k, v, expUnit)
	if err != nil {
		return 0, err
	}
	if f < 0 || f > 1 {
		return 0, dialect.InvalidArgument(k, v, expUnit)
	}
	return f, nil
}

// optRange parses an optional number in [lo, hi] into a pointer; field is
// the client-visible name. Empty is unset.
func optRange(v, field string, lo, hi float64) (*float64, error) {
	if v == "" {
		return nil, nil
	}
	f, err := parseFloat(field, v, expNum)
	if err != nil {
		return nil, err
	}
	if f < lo || f > hi {
		return nil, dialect.InvalidArgument(field, v, "a number from "+strconv.FormatFloat(lo, 'f', -1, 64)+" to "+strconv.FormatFloat(hi, 'f', -1, 64))
	}
	return &f, nil
}

func parseFloat(field, v, expected string) (float64, error) {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, dialect.InvalidArgument(field, v, expected)
	}
	return f, nil
}

func parseInt(field, v, expected string) (int, error) {
	n, err := strconv.Atoi(v)
	if err != nil || n < math.MinInt32 || n > math.MaxInt32 {
		if expected == "" {
			expected = expInt
		}
		return 0, dialect.InvalidArgument(field, v, expected)
	}
	return n, nil
}
