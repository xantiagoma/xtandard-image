package cloudflare

import (
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Reasons recorded in Request.Ignored.
const (
	reasonUnknown        = "unknown option"
	reasonUnsupported    = "unsupported"
	reasonNotImplemented = "not implemented"
	reasonApproximated   = "approximated"
	reasonM2             = "takes effect in M2"
	reasonM3             = "takes effect in M3"
	reasonHosted         = "not supported for hosted images"
)

// Expectation phrases (client-safe, static).
const (
	expDim       = "positive integer or auto"
	expDimNoAuto = "positive integer"
	expFit       = "scale-down, contain, cover, crop, pad, squeeze, scale-up or aspect-crop"
	expGravity   = "auto, face, left, right, top, bottom or XxY with X and Y in 0..1"
	expUnit      = "number in 0..1"
	expQuality   = "integer 1..100, high, medium-high, medium-low or low"
	expFormat    = "auto, avif, webp, jpeg, baseline-jpeg, png, gif or json"
	expDPR       = "positive number"
	expBool      = "true or false"
	expBlur      = "number in 0..250"
	expSharpen   = "number in 0..10"
	expFactor    = "non-negative number"
	expRotate    = "0, 90, 180 or 270"
	expFlip      = "h, v or hv"
	expTrim      = "border or top;right;bottom;left"
	expTrimSide  = "non-negative integer (pixels) or a fraction below 1"
	expTolerance = "integer 0..255"
	expBorder    = "color;width or color;top;right;bottom;left"
	expBorderW   = "non-negative integer"
	expMetadata  = "keep, copyright or none"
	expOnError   = "redirect"
	expComp      = "fast"
	expSegment   = "foreground"
	expUpscale   = "interpolate or generate"
)

// Upper bounds of Cloudflare's numeric options.
const (
	maxBlur    = 250
	maxSharpen = 10
)

// Named quality levels (docs/URL-DIALECTS.md §8).
const (
	qualityHigh       = 90
	qualityMediumHigh = 80
	qualityMediumLow  = 65
	qualityLow        = 50
)

// state is one parse of an option list.
type state struct {
	b      *imgproxy.Builder
	strict bool
	hosted bool // Cloudflare Images delivery: onerror is not supported
	trim   trimSides
	border *transform.Border
}

func newState(b *imgproxy.Builder, strict, hosted bool) *state {
	return &state{b: b, strict: strict, hosted: hosted}
}

func (st *state) pl() *transform.Pipeline { return st.b.Pipeline() }

func (st *state) req() *transform.Request { return st.b.Request() }

// applyList applies a comma-separated k=v option list (the escaped
// options segment of a Cloudflare URL). Commas inside parentheses
// (rgb(1,2,3)) do not split; keys and values are percent-decoded after
// splitting; empty items are skipped. Later options override earlier ones.
func (st *state) applyList(escaped string) error {
	depth, start := 0, 0
	for i := 0; i <= len(escaped); i++ {
		if i < len(escaped) {
			switch escaped[i] {
			case '(':
				depth++
				continue
			case ')':
				depth = max(0, depth-1)
				continue
			case ',':
				if depth > 0 {
					continue
				}
			default:
				continue
			}
		}
		if err := st.applyItem(escaped[start:i]); err != nil {
			return err
		}
		start = i + 1
	}
	return nil
}

func (st *state) applyItem(item string) error {
	if item == "" {
		return nil
	}
	k, v, ok := strings.Cut(item, "=")
	key, err := url.PathUnescape(k)
	if err != nil {
		return dialect.InvalidURL("invalid option escaping")
	}
	if !ok {
		return dialect.InvalidArgument(strings.ToLower(key), "", "key=value")
	}
	value, err := url.PathUnescape(v)
	if err != nil {
		return dialect.InvalidURL("invalid option escaping")
	}
	return st.apply(strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value))
}

// apply applies one option. Keys are the Cloudflare names and aliases
// (developers.cloudflare.com/images/optimization/features/).
//
//nolint:gocyclo,cyclop // one flat switch over the option table is the clearest form
func (st *state) apply(key, v string) error {
	pl, r := st.pl(), st.req()
	switch key {
	case "width", "w":
		return st.dim("width", v, &pl.Resize.Width, &r.ClientHints.Width)
	case "height", "h":
		return st.dim("height", v, &pl.Resize.Height, nil)
	case "wbreakpoints", "wmobile", "wdesktop":
		st.b.Ignore(key, reasonNotImplemented)
	case "fit":
		return st.fit(v)
	case "gravity", "g":
		return st.gravity(v)
	case "zoom", "face-zoom":
		z, err := unitFloat(key, v)
		if err != nil {
			return err
		}
		pl.Gravity.FocusZoom = z
		st.b.Ignore(key, reasonM3)
	case "quality", "q":
		return quality("quality", v, &r.Output.Quality)
	case "slow-connection-quality", "scq":
		if err := quality("slow-connection-quality", v, &r.Output.SlowConnectionQuality); err != nil {
			return err
		}
		r.ClientHints.SaveData = true
	case "format", "f":
		return st.format(v)
	case "dpr":
		f, err := parseFloat(v)
		if err != nil || f <= 0 || f > transform.MaxFactor {
			return dialect.InvalidArgument("dpr", v, expDPR)
		}
		pl.Resize.DPR = f
	case "anim":
		b, err := parseBool("anim", v)
		if err != nil {
			return err
		}
		pl.DisableAnimation = !b
	case "background":
		c, err := parseColor("background", v)
		if err != nil {
			return err
		}
		pl.Background = &c
	case "blur":
		f, err := parseFloat(v)
		if err != nil || f < 0 || f > maxBlur {
			return dialect.InvalidArgument("blur", v, expBlur)
		}
		pl.Blur = f / 2
	case "sharpen":
		f, err := parseFloat(v)
		if err != nil || f < 0 || f > maxSharpen {
			return dialect.InvalidArgument("sharpen", v, expSharpen)
		}
		pl.Sharpen = f / 4
	case "brightness":
		// A multiplier: Adjust.Brightness is additive, Exposure multiplies.
		return st.factor(key, v, &pl.Adjust.Exposure)
	case "contrast":
		return st.factor(key, v, &pl.Adjust.Contrast)
	case "saturation":
		return st.factor(key, v, &pl.Adjust.Saturation)
	case "gamma":
		return st.gamma(v)
	case "rotate":
		switch v {
		case "0", "90", "180", "270":
			pl.Rotate, _ = strconv.Atoi(v)
		default:
			return dialect.InvalidArgument("rotate", v, expRotate)
		}
	case "flip":
		switch v {
		case "h":
			pl.Flip = transform.Flip{H: true}
		case "v":
			pl.Flip = transform.Flip{V: true}
		case "hv", "vh":
			pl.Flip = transform.Flip{H: true, V: true}
		default:
			return dialect.InvalidArgument("flip", v, expFlip)
		}
	case "trim", "trim.top", "trim.right", "trim.bottom", "trim.left", "trim.width", "trim.height",
		"trim.border", "trim.border.color", "trim.border.tolerance", "trim.border.keep":
		return st.applyTrim(key, v)
	case "border", "border.color", "border.width", "border.top", "border.right", "border.bottom", "border.left":
		return st.applyBorder(key, v)
	case "metadata":
		return st.metadata(v)
	case "onerror":
		if v != "redirect" {
			return dialect.InvalidArgument("onerror", v, expOnError)
		}
		if st.hosted {
			st.b.Ignore(key, reasonHosted)
			return nil
		}
		r.Delivery.OnErrorRedirect = true
	case "compression":
		if v != "fast" {
			return dialect.InvalidArgument("compression", v, expComp)
		}
		r.Output.Effort = 1
	case "segment":
		if v != "foreground" {
			return dialect.InvalidArgument("segment", v, expSegment)
		}
		return st.unsupported(key)
	case "upscale":
		switch v {
		case "interpolate": // the default
		case "generate":
			return st.unsupported(key)
		default:
			return dialect.InvalidArgument("upscale", v, expUpscale)
		}
	default:
		if st.strict {
			return dialect.UnknownOption(key)
		}
		st.b.Ignore(key, reasonUnknown)
	}
	return nil
}

func (st *state) unsupported(key string) error {
	if st.strict {
		return dialect.UnknownOption(key)
	}
	st.b.Ignore(key, reasonUnsupported)
	return nil
}

// dim parses width/height: a positive integer, or auto (width only) which
// opts into the Width client hint.
func (st *state) dim(field, v string, dst *transform.Dim, auto *bool) error {
	if v == "auto" && auto != nil {
		*dst, *auto = transform.Dim{}, true
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 || n > transform.MaxDimension {
		if auto != nil {
			return dialect.InvalidArgument(field, v, expDim)
		}
		return dialect.InvalidArgument(field, v, expDimNoAuto)
	}
	*dst = transform.Dim{Px: n}
	if auto != nil {
		*auto = false
	}
	return nil
}

// fit maps Cloudflare fit modes (docs/URL-DIALECTS.md §8).
func (st *state) fit(v string) error {
	rs := &st.pl().Resize
	pl := st.pl()
	pl.Extend = transform.Extend{}
	switch v {
	case "scale-down":
		rs.Type, rs.Enlarge = transform.ResizeFit, false
	case "contain":
		rs.Type, rs.Enlarge = transform.ResizeFit, true
	case "cover":
		rs.Type, rs.Enlarge = transform.ResizeFill, true
	case "crop":
		rs.Type, rs.Enlarge = transform.ResizeFillDown, false
	case "pad":
		rs.Type, rs.Enlarge = transform.ResizeFit, true
		pl.Extend = transform.Extend{Enabled: true}
	case "squeeze":
		rs.Type, rs.Enlarge = transform.ResizeForce, true
	case "scale-up":
		// Never downscales: no request field says "only enlarge".
		rs.Type, rs.Enlarge = transform.ResizeFit, true
		st.b.Ignore("fit=scale-up", reasonApproximated)
	case "aspect-crop":
		// Crops smaller images to the aspect ratio without scaling.
		rs.Type, rs.Enlarge = transform.ResizeFill, false
		st.b.Ignore("fit=aspect-crop", reasonApproximated)
	default:
		return dialect.InvalidArgument("fit", v, expFit)
	}
	return nil
}

func (st *state) gravity(v string) error {
	g := &st.pl().Gravity
	zoom := g.FocusZoom
	switch v {
	case "auto":
		*g = transform.Gravity{Type: transform.GravitySmart}
	case "face":
		*g = transform.Gravity{Type: transform.GravityFace}
		st.b.Ignore("gravity=face", reasonM3)
	case "left":
		*g = transform.Gravity{Type: transform.GravityWest}
	case "right":
		*g = transform.Gravity{Type: transform.GravityEast}
	case "top":
		*g = transform.Gravity{Type: transform.GravityNorth}
	case "bottom":
		*g = transform.Gravity{Type: transform.GravitySouth}
	default:
		xs, ys, ok := strings.Cut(v, "x")
		x, errX := parseFloat(xs)
		y, errY := parseFloat(ys)
		if !ok || errX != nil || errY != nil || x < 0 || x > 1 || y < 0 || y > 1 {
			return dialect.InvalidArgument("gravity", v, expGravity)
		}
		*g = transform.Gravity{Type: transform.GravityFocusPoint, FocusX: x, FocusY: y}
	}
	g.FocusZoom = zoom
	return nil
}

func quality(field, v string, dst *int) error {
	switch v {
	case "high":
		*dst = qualityHigh
	case "medium-high":
		*dst = qualityMediumHigh
	case "medium-low":
		*dst = qualityMediumLow
	case "low":
		*dst = qualityLow
	default:
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > transform.MaxQuality {
			return dialect.InvalidArgument(field, v, expQuality)
		}
		*dst = n
	}
	return nil
}

func (st *state) format(v string) error {
	o := &st.req().Output
	switch v {
	case "auto":
		o.Format = transform.FormatUnset
	case "avif":
		o.Format = transform.FormatAVIF
	case "webp":
		o.Format = transform.FormatWebP
	case "jpeg", "jpg":
		// Cloudflare's jpeg is progressive.
		on := true
		o.Format, o.Progressive = transform.FormatJPEG, &on
	case "baseline-jpeg":
		off := false
		o.Format, o.Progressive = transform.FormatJPEG, &off
	case "png":
		o.Format = transform.FormatPNG
	case "gif":
		o.Format = transform.FormatGIF
	case "json":
		// Cloudflare's JSON differs from the /info schema; the image is
		// served instead.
		st.b.Ignore("format=json", reasonNotImplemented)
	default:
		return dialect.InvalidArgument("format", v, expFormat)
	}
	return nil
}

// factor parses a non-negative multiplier into dst (brightness, contrast,
// saturation), applied by the M2 adjust stage.
func (st *state) factor(field, v string, dst **float64) error {
	f, err := parseFloat(v)
	if err != nil || f < 0 || f > transform.MaxFactor {
		return dialect.InvalidArgument(field, v, expFactor)
	}
	*dst = &f
	st.b.Ignore(field, reasonM2)
	return nil
}

// gamma: 0 and 1 both mean "unchanged".
func (st *state) gamma(v string) error {
	f, err := parseFloat(v)
	if err != nil || f < 0 || f > transform.MaxFactor {
		return dialect.InvalidArgument("gamma", v, expFactor)
	}
	a := &st.pl().Adjust
	if f == 0 {
		a.Gamma = nil
		return nil
	}
	a.Gamma = &f
	st.b.Ignore("gamma", reasonM2)
	return nil
}

func (st *state) metadata(v string) error {
	o := &st.req().Output
	strip, keepCopyright := true, false
	switch v {
	case "keep":
		strip = false
	case "copyright":
		keepCopyright = true
	case "none":
	default:
		return dialect.InvalidArgument("metadata", v, expMetadata)
	}
	o.StripMetadata, o.KeepCopyright = &strip, &keepCopyright
	return nil
}

// Scalar helpers.

func parseFloat(s string) (float64, error) {
	f, err := strconv.ParseFloat(s, 64)
	if err == nil && (math.IsNaN(f) || math.IsInf(f, 0)) {
		return 0, strconv.ErrSyntax
	}
	return f, err
}

func unitFloat(field, s string) (float64, error) {
	f, err := parseFloat(s)
	if err != nil || f < 0 || f > 1 {
		return 0, dialect.InvalidArgument(field, s, expUnit)
	}
	return f, nil
}

func parseBool(field, s string) (bool, error) {
	switch s {
	case "true", "1":
		return true, nil
	case "false", "0":
		return false, nil
	}
	return false, dialect.InvalidArgument(field, s, expBool)
}
