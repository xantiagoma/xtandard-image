// Option grammar from the imgproxy documentation (docs.imgproxy.net
// usage/processing) and @imgproxy/imgproxy-js-core (MIT); the Pro options
// have no open-source implementation to port. Copyright xtandard-image
// contributors, MIT.

package imgproxy

import (
	"encoding/base64"
	"math"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Expectation phrases of the later-milestone options.
const (
	expDirection    = "down, up, left, right or an angle in degrees"
	expUnsharpMode  = "auto, none or always"
	expAQMethod     = "none, size, dssim or ml"
	expWebPComp     = "lossy, near_lossless or lossless" //nolint:gosec // G101 false positive: an error message, not a credential
	expWebPPreset   = "default, photo, picture, drawing, icon or text"
	expAVIFSub      = "auto, on or off"
	expHashType     = "none, md5, sha1, sha256 or sha512"
	expHexDigest    = "hex digest of the hashsum type"
	expBlurAreas    = "sigma followed by left:top:width:height groups"
	expColorProfile = "srgb, cmyk or an ICC file name"
	expCacheTag     = "non-empty tag of visible ASCII without commas"
	expText         = "url-safe base64 of UTF-8 text"
	expSourceURL    = "url-safe base64 source url"
	expObjPos       = "ce, no, so, ea, we, noea, nowe, soea, sowe, fp or prop"
	expPalette      = "0 or an integer in range 2-256"
	expTooManyItems = "at most 64 entries"
	expColumnsRows  = "integer > 0 when step is not 0"
	expFrames       = "integer > 0 when step is not 0"
	expMinMax       = "min_quality <= max_quality"
)

// Default colours of the imgproxy colour effects.
var (
	colorBlack      = transform.Color{A: 255}
	colorWhite      = transform.Color{R: 255, G: 255, B: 255, A: 255}
	colorMonochrome = transform.Color{R: 0xb3, G: 0xb3, B: 0xb3, A: 255}
)

// applied marks a later option the executor implements: it is not recorded
// in Request.Ignored.
const applied = ""

// applyLater parses the options of docs/URL-DIALECTS.md §3.2 scheduled for
// M2 and M3 into the request and records each in Request.Ignored with its
// milestone, so the server can reject or skip them until the executor
// implements them (options already implemented, such as autoquality, are not
// recorded). It reports whether name is one of them.
//
//nolint:gocyclo // one flat switch over the option table is the clearest form
func (st *state) applyLater(pl *transform.Pipeline, name string, args []string) (bool, error) {
	var err error
	var milestone string // every case sets it
	switch name {
	// Geometry (crop_aspect_ratio applied since WP-60).
	case "crop_aspect_ratio", "crop_ar", "car":
		milestone = applied
		err = cropAspectRatio(pl, args)
	case "objects_position", "obj_pos", "op":
		milestone = reasonM3
		err = objectsPosition(&pl.Gravity, args)

	// Colour (applied since WP-59).
	case "adjust", "a":
		milestone = applied
		err = adjust(&pl.Adjust, args)
	case "brightness", "br":
		milestone = applied
		err = oneRangePtr("brightness", args, -transform.MaxBrightness, transform.MaxBrightness, &pl.Adjust.Brightness)
	case "contrast", "co":
		milestone = applied
		err = oneRangePtr("contrast", args, 0, transform.MaxFactor, &pl.Adjust.Contrast)
	case "saturation", "sa":
		milestone = applied
		err = oneRangePtr("saturation", args, 0, transform.MaxFactor, &pl.Adjust.Saturation)
	case "monochrome", "mc":
		milestone = applied
		err = monochrome(pl, args)
	case "duotone", "dt":
		milestone = applied
		err = duotone(pl, args)
	case "colorize", "col":
		milestone = applied
		err = colorize(pl, args)
	case "gradient", "gr":
		milestone = applied
		err = gradient(pl, args)

	// Blur and sharpening (applied since WP-61).
	case "unsharp_masking", "ush":
		milestone = applied
		err = unsharpMasking(pl, args)
	case "blur_areas", "ba":
		milestone = applied
		err = blurAreas(pl, args)
	case "progressive_blur", "pbl":
		milestone = applied
		err = progressiveBlur(pl, args)
	case "blur_detections", "bd":
		milestone = reasonM3
		err = blurDetections(pl, args)
	case "draw_detections", "dd":
		milestone = reasonM3
		err = drawDetections(pl, args)
	case "crop_objects", "c_obj":
		milestone = reasonM3
		err = cropObjects(pl, args)

	// Watermark details (the watermark itself is wm).
	case "watermark_url", "wmu":
		milestone = reasonM3
		err = watermarkURL(firstWatermark(pl), args)
	case "watermark_text", "wmt":
		milestone = reasonM3
		err = watermarkText(firstWatermark(pl), args)
	case "watermark_size", "wms":
		milestone = reasonM3
		err = watermarkSize(firstWatermark(pl), args)
	case "watermark_rotate", "wm_rot", "wmr":
		milestone = reasonM3
		err = watermarkRotate(firstWatermark(pl), args)
	case "watermark_shadow", "wmsh":
		milestone = reasonM3
		err = oneRange("watermark_shadow", args, 0, transform.MaxSigma, &firstWatermark(pl).Shadow)
	case "style", "stl", "st":
		milestone = applied // WP-64: SVG sources only (the executor skips it otherwise)
		err = oneBase64Text("style", args, &pl.SVGStyle)

	// Output (request-wide).
	case "dpi":
		milestone = applied // WP-64
		err = oneIntRange("dpi", args, 0, transform.MaxDPI, &st.r.Output.DPI)
	case "color_profile", "cp", "icc":
		milestone = applied
		err = colorProfile(&st.r.Output, args)
	case "autoquality", "aq":
		milestone = applied // WP-56
		err = autoquality(&st.r.Output, args)
	case "jpeg_options", "jpgo":
		milestone = applied // WP-64 (the mozjpeg-only arguments are no-ops with libjpeg-turbo)
		err = jpegOptions(&st.r.Output, args)
	case "png_options", "pngo":
		milestone = applied
		err = pngOptions(&st.r.Output, args)
	case "webp_options", "webpo":
		milestone = applied
		err = webpOptions(&st.r.Output, args)
	case "avif_options", "avifo":
		milestone = applied
		err = avifOptions(&st.r.Output, args)

	// Video (pipeline).
	case "video_thumbnail_second", "vts":
		milestone = reasonM3
		v := video(pl)
		var s float64
		if err = oneRange("video_thumbnail_second", args, 0, math.MaxInt32, &s); err == nil {
			v.Second = &s
		}
	case "video_thumbnail_keyframes", "vtk":
		milestone = reasonM3
		err = oneBoolPtr("video_thumbnail_keyframes", args, &video(pl).Keyframes)
	case "video_thumbnail_tile", "vtt":
		milestone = reasonM3
		err = videoTile(video(pl), args)
	case "video_thumbnail_animation", "vta":
		milestone = reasonM3
		err = videoAnimation(video(pl), args)

	// Delivery and source (request-wide).
	case "fallback_image_url", "fiu":
		milestone = applied // WP-34c (honoured with XI_FALLBACK_ALLOW_REQUEST_URL)
		err = fallbackImageURL(&st.r.Delivery, args)
	case "bypass_cache", "bc":
		milestone = applied // WP-52 (honoured with XI_CACHE_ALLOW_BYPASS)
		err = oneBool("bypass_cache", args, &st.r.Delivery.BypassCache)
	case "cache_tags", "ct":
		milestone = applied // WP-34b header, XI_CACHE_TAGS_HEADER
		err = cacheTags(&st.r.Delivery, args)
	case "hashsum", "hs":
		milestone = applied // WP-64
		err = hashsum(&st.r.Source, args)
	default:
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if milestone != applied {
		st.ignore(name, milestone)
	}
	return true, nil
}

// finishLater applies the cross-option rules of the later options once
// every option is known: a colour effect with zero intensity or opacity,
// or a progressive blur with zero sigma, is off; watermark text replaces
// the watermark image; autoquality bounds must be ordered.
func finishLater(r *transform.Request) error {
	for i := range r.Pipelines {
		pl := &r.Pipelines[i]
		if pl.Monochrome != nil && pl.Monochrome.Intensity == 0 {
			pl.Monochrome = nil
		}
		if pl.Duotone != nil && pl.Duotone.Intensity == 0 {
			pl.Duotone = nil
		}
		if pl.Colorize != nil && pl.Colorize.Opacity == 0 {
			pl.Colorize = nil
		}
		if pl.Gradient != nil && pl.Gradient.Opacity == 0 {
			pl.Gradient = nil
		}
		if pl.ProgressiveBlur != nil && pl.ProgressiveBlur.Sigma == 0 {
			pl.ProgressiveBlur = nil
		}
		for j := range pl.Watermarks {
			if w := &pl.Watermarks[j]; w.Text != "" {
				w.Source = nil
			}
		}
		if v := pl.Video; v != nil && *v == (transform.VideoOptions{}) {
			pl.Video = nil
		}
	}
	if aq := r.Output.AutoQuality; aq != nil && aq.Min != 0 && aq.Max != 0 && aq.Min > aq.Max {
		return dialect.InvalidArgument("autoquality.min_quality", strconv.Itoa(aq.Min), expMinMax)
	}
	return nil
}

// Argument helpers.

// parseRange parses a float in [lo, hi].
func parseRange(field, s string, lo, hi float64) (float64, error) {
	f, ok := parseFloatRaw(s)
	if !ok || f < lo || f > hi {
		return 0, dialect.InvalidArgument(field, s, "number in range "+fmtNum(lo)+" to "+fmtNum(hi))
	}
	return f, nil
}

// parseIntRange parses an integer in [lo, hi].
func parseIntRange(field, s string, lo, hi int) (int, error) {
	i, err := strconv.Atoi(s)
	if err != nil || i < lo || i > hi {
		return 0, dialect.InvalidArgument(field, s, "integer in range "+strconv.Itoa(lo)+" to "+strconv.Itoa(hi))
	}
	return i, nil
}

func fmtNum(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func oneRange(field string, args []string, lo, hi float64, dst *float64) error {
	if err := maxArgs(field, args, 1); err != nil {
		return err
	}
	v, err := parseRange(field, args[0], lo, hi)
	if err != nil {
		return err
	}
	*dst = v
	return nil
}

func oneRangePtr(field string, args []string, lo, hi float64, dst **float64) error {
	var v float64
	if err := oneRange(field, args, lo, hi, &v); err != nil {
		return err
	}
	*dst = &v
	return nil
}

func oneIntRange(field string, args []string, lo, hi int, dst *int) error {
	if err := maxArgs(field, args, 1); err != nil {
		return err
	}
	v, err := parseIntRange(field, args[0], lo, hi)
	if err != nil {
		return err
	}
	*dst = v
	return nil
}

// optBoolPtr sets *dst from s unless s is empty (keep the previous value).
func optBoolPtr(field, s string, dst **bool) error {
	if s == "" {
		return nil
	}
	b, err := parseBool(field, s)
	if err != nil {
		return err
	}
	*dst = &b
	return nil
}

// arg returns args[i], or "" when absent.
func arg(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}

// parseDirection parses down/up/left/right or an angle in degrees.
func parseDirection(field, s string) (transform.Direction, float64, error) {
	switch s {
	case "down":
		return transform.DirectionDown, 0, nil
	case "up":
		return transform.DirectionUp, 0, nil
	case "left":
		return transform.DirectionLeft, 0, nil
	case "right":
		return transform.DirectionRight, 0, nil
	}
	a, ok := parseFloatRaw(s)
	if !ok || a < -360 || a > 360 {
		return 0, 0, dialect.InvalidArgument(field, s, expDirection)
	}
	return transform.DirectionAngle, a, nil
}

// parseClasses validates object class names (detections, crop_objects).
func parseClasses(field string, args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, nil
	}
	if len(args) > transform.MaxListLen {
		return nil, dialect.InvalidArgument(field, joinArgs(args), expTooManyItems)
	}
	out := make([]string, 0, len(args))
	for _, c := range args {
		if c == "" {
			return nil, dialect.InvalidArgument(field, c, expNonEmpty)
		}
		out = append(out, c)
	}
	return out, nil
}

// decodeText decodes url-safe base64 (padding optional) into UTF-8 text
// without NUL bytes.
func decodeText(field, s string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil || !utf8.Valid(b) || strings.IndexByte(string(b), 0) >= 0 {
		return "", dialect.InvalidArgument(field, s, expText)
	}
	return string(b), nil
}

func oneBase64Text(field string, args []string, dst *string) error {
	if err := maxArgs(field, args, 1); err != nil {
		return err
	}
	t, err := decodeText(field, args[0])
	if err != nil {
		return err
	}
	*dst = t
	return nil
}

// decodeSourceArg decodes a url-safe base64 source URL option argument;
// "" means none.
func decodeSourceArg(field, s string) (*transform.SourceSpec, error) {
	if s == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil || !plausibleSource(string(b)) {
		return nil, dialect.InvalidArgument(field, s, expSourceURL)
	}
	return &transform.SourceSpec{Raw: string(b), Encoding: transform.SourceBase64}, nil
}

// Geometry.

// cropAspectRatio is car:ratio[:enlarge]; it corrects the crop area of the
// crop option (the whole image without one).
func cropAspectRatio(pl *transform.Pipeline, args []string) error {
	if err := maxArgs("crop_aspect_ratio", args, 2); err != nil {
		return err
	}
	c := transform.Crop{}
	if pl.Crop != nil {
		c = *pl.Crop
	}
	if args[0] != "" {
		r, err := parseRange("crop_aspect_ratio.aspect_ratio", args[0], 0, transform.MaxFactor)
		if err != nil {
			return err
		}
		c.AspectRatio = r
	}
	if s := arg(args, 1); s != "" {
		if err := oneBool("crop_aspect_ratio.enlarge", args[1:2], &c.AspectEnlarge); err != nil {
			return err
		}
	}
	pl.Crop = &c
	return nil
}

// objectsPosition is op:type[:x:y], op:fp[:x:y] or op:prop.
func objectsPosition(g *transform.Gravity, args []string) error {
	const field = "objects_position"
	if err := maxArgs(field, args, 3); err != nil {
		return err
	}
	t := args[0]
	switch t {
	case "prop":
		if len(args) > 1 {
			return dialect.InvalidArgument(field, joinArgs(args), "prop without arguments")
		}
		g.ObjectsPosition = &transform.ObjectsPosition{Proportional: true}
		return nil
	case "fp":
		op := &transform.ObjectsPosition{Type: transform.GravityFocusPoint, X: 0.5, Y: 0.5}
		var err error
		if s := arg(args, 1); s != "" {
			if op.X, err = parseUnitFloat(field+".x", s); err != nil {
				return err
			}
		}
		if s := arg(args, 2); s != "" {
			if op.Y, err = parseUnitFloat(field+".y", s); err != nil {
				return err
			}
		}
		g.ObjectsPosition = op
		return nil
	}
	gt, ok := compass[t]
	if !ok {
		return dialect.InvalidArgument(field+".type", t, expObjPos)
	}
	op := &transform.ObjectsPosition{Type: gt}
	var err error
	if s := arg(args, 1); s != "" {
		if op.X, err = parseRange(field+".x_offset", s, -transform.MaxDimension, transform.MaxDimension); err != nil {
			return err
		}
	}
	if s := arg(args, 2); s != "" {
		if op.Y, err = parseRange(field+".y_offset", s, -transform.MaxDimension, transform.MaxDimension); err != nil {
			return err
		}
	}
	g.ObjectsPosition = op
	return nil
}

// Colour.

// adjust is a:brightness:contrast:saturation; empty arguments keep the
// previous value.
func adjust(a *transform.Adjust, args []string) error {
	if err := maxArgs("adjust", args, 3); err != nil {
		return err
	}
	type part struct {
		field  string
		lo, hi float64
		dst    **float64
	}
	parts := [3]part{
		{"adjust.brightness", -transform.MaxBrightness, transform.MaxBrightness, &a.Brightness},
		{"adjust.contrast", 0, transform.MaxFactor, &a.Contrast},
		{"adjust.saturation", 0, transform.MaxFactor, &a.Saturation},
	}
	for i, s := range args {
		if s == "" {
			continue
		}
		v, err := parseRange(parts[i].field, s, parts[i].lo, parts[i].hi)
		if err != nil {
			return err
		}
		*parts[i].dst = &v
	}
	return nil
}

// optColor sets *dst from a hex argument unless it is empty.
func optColor(field, s string, dst *transform.Color) error {
	if s == "" {
		return nil
	}
	c, err := parseHexColor(field, s)
	if err != nil {
		return err
	}
	*dst = c
	return nil
}

// optUnit sets *dst from a 0..1 argument unless it is empty.
func optUnit(field, s string, dst *float64) error {
	if s == "" {
		return nil
	}
	v, err := parseUnitFloat(field, s)
	if err != nil {
		return err
	}
	*dst = v
	return nil
}

// monochrome is mc:intensity[:color] (default color b3b3b3).
func monochrome(pl *transform.Pipeline, args []string) error {
	if err := maxArgs("monochrome", args, 2); err != nil {
		return err
	}
	m := transform.Monochrome{Color: colorMonochrome}
	if pl.Monochrome != nil {
		m = *pl.Monochrome
	}
	if err := optUnit("monochrome.intensity", args[0], &m.Intensity); err != nil {
		return err
	}
	if err := optColor("monochrome.color", arg(args, 1), &m.Color); err != nil {
		return err
	}
	pl.Monochrome = &m
	return nil
}

// duotone is dt:intensity[:shadow[:highlight]] (default 000000:ffffff).
func duotone(pl *transform.Pipeline, args []string) error {
	if err := maxArgs("duotone", args, 3); err != nil {
		return err
	}
	d := transform.Duotone{Shadow: colorBlack, Highlight: colorWhite}
	if pl.Duotone != nil {
		d = *pl.Duotone
	}
	if err := optUnit("duotone.intensity", args[0], &d.Intensity); err != nil {
		return err
	}
	if err := optColor("duotone.color1", arg(args, 1), &d.Shadow); err != nil {
		return err
	}
	if err := optColor("duotone.color2", arg(args, 2), &d.Highlight); err != nil {
		return err
	}
	pl.Duotone = &d
	return nil
}

// colorize is col:opacity[:color[:keep_alpha]] (default color 000).
func colorize(pl *transform.Pipeline, args []string) error {
	if err := maxArgs("colorize", args, 3); err != nil {
		return err
	}
	c := transform.Colorize{Color: colorBlack}
	if pl.Colorize != nil {
		c = *pl.Colorize
	}
	if err := optUnit("colorize.opacity", args[0], &c.Opacity); err != nil {
		return err
	}
	if err := optColor("colorize.color", arg(args, 1), &c.Color); err != nil {
		return err
	}
	if s := arg(args, 2); s != "" {
		if err := oneBool("colorize.keep_alpha", args[2:3], &c.KeepAlpha); err != nil {
			return err
		}
	}
	pl.Colorize = &c
	return nil
}

// gradient is gr:opacity[:color[:direction[:start[:stop]]]] (default
// 000:down:0:1).
func gradient(pl *transform.Pipeline, args []string) error {
	if err := maxArgs("gradient", args, 5); err != nil {
		return err
	}
	g := transform.Gradient{Color: colorBlack, Stop: 1}
	if pl.Gradient != nil {
		g = *pl.Gradient
	}
	if err := optUnit("gradient.opacity", args[0], &g.Opacity); err != nil {
		return err
	}
	if err := optColor("gradient.color", arg(args, 1), &g.Color); err != nil {
		return err
	}
	if s := arg(args, 2); s != "" {
		d, a, err := parseDirection("gradient.direction", s)
		if err != nil {
			return err
		}
		g.Direction, g.Angle = d, a
	}
	if err := optUnit("gradient.start", arg(args, 3), &g.Start); err != nil {
		return err
	}
	if err := optUnit("gradient.stop", arg(args, 4), &g.Stop); err != nil {
		return err
	}
	pl.Gradient = &g
	return nil
}

// Blur and sharpening.

var unsharpModes = map[string]transform.UnsharpMode{
	"auto":   transform.UnsharpAuto,
	"none":   transform.UnsharpNone,
	"always": transform.UnsharpAlways,
}

// unsharpMasking is ush:mode:weight:divider, every argument optional.
func unsharpMasking(pl *transform.Pipeline, args []string) error {
	if err := maxArgs("unsharp_masking", args, 3); err != nil {
		return err
	}
	u := transform.Unsharp{}
	if pl.Unsharp != nil {
		u = *pl.Unsharp
	}
	if s := args[0]; s != "" {
		m, ok := unsharpModes[s]
		if !ok {
			return dialect.InvalidArgument("unsharp_masking.mode", s, expUnsharpMode)
		}
		u.Mode = m
	}
	var err error
	if s := arg(args, 1); s != "" {
		if u.Weight, err = parseRange("unsharp_masking.weight", s, 0, transform.MaxFactor); err != nil {
			return err
		}
	}
	if s := arg(args, 2); s != "" {
		if u.Divider, err = parseRange("unsharp_masking.divider", s, 0, transform.MaxFactor); err != nil {
			return err
		}
	}
	pl.Unsharp = &u
	return nil
}

// blurAreas is ba:sigma:l:t:w:h[:l:t:w:h...] in relative coordinates; a
// zero sigma or no areas turns it off.
func blurAreas(pl *transform.Pipeline, args []string) error {
	rest := args[1:]
	if len(rest)%4 != 0 {
		return dialect.InvalidArgument("blur_areas", joinArgs(args), expBlurAreas)
	}
	if len(rest)/4 > transform.MaxBlurAreas {
		return dialect.InvalidArgument("blur_areas", joinArgs(args), "at most "+strconv.Itoa(transform.MaxBlurAreas)+" areas")
	}
	sigma, err := parseRange("blur_areas.sigma", args[0], 0, transform.MaxSigma)
	if err != nil {
		return err
	}
	ba := &transform.BlurAreas{Sigma: sigma, Areas: make([]transform.Area, 0, len(rest)/4)}
	for i := 0; i < len(rest); i += 4 {
		var v [4]float64
		for j, f := range [4]string{"blur_areas.left", "blur_areas.top", "blur_areas.width", "blur_areas.height"} {
			if v[j], err = parseUnitFloat(f, rest[i+j]); err != nil {
				return err
			}
		}
		ba.Areas = append(ba.Areas, transform.Area{Left: v[0], Top: v[1], Width: v[2], Height: v[3]})
	}
	if sigma == 0 || len(ba.Areas) == 0 {
		ba = nil
	}
	pl.BlurAreas = ba
	return nil
}

// progressiveBlur is pbl:sigma[:direction[:start[:stop]]] (default
// down:0:1).
func progressiveBlur(pl *transform.Pipeline, args []string) error {
	if err := maxArgs("progressive_blur", args, 4); err != nil {
		return err
	}
	pb := transform.ProgressiveBlur{Stop: 1}
	if pl.ProgressiveBlur != nil {
		pb = *pl.ProgressiveBlur
	}
	var err error
	if s := args[0]; s != "" {
		if pb.Sigma, err = parseRange("progressive_blur.sigma", s, 0, transform.MaxSigma); err != nil {
			return err
		}
	}
	if s := arg(args, 1); s != "" {
		if pb.Direction, pb.Angle, err = parseDirection("progressive_blur.direction", s); err != nil {
			return err
		}
	}
	if err := optUnit("progressive_blur.start", arg(args, 2), &pb.Start); err != nil {
		return err
	}
	if err := optUnit("progressive_blur.stop", arg(args, 3), &pb.Stop); err != nil {
		return err
	}
	pl.ProgressiveBlur = &pb
	return nil
}

// blurDetections is bd:sigma[:classes...]; sigma 0 turns it off.
func blurDetections(pl *transform.Pipeline, args []string) error {
	sigma, err := parseRange("blur_detections.sigma", args[0], 0, transform.MaxSigma)
	if err != nil {
		return err
	}
	classes, err := parseClasses("blur_detections.classes", args[1:])
	if err != nil {
		return err
	}
	pl.BlurDetections = nil
	if sigma > 0 {
		pl.BlurDetections = &transform.DetectionFilter{Sigma: sigma, Classes: classes}
	}
	return nil
}

// drawDetections is dd:draw[:classes...].
func drawDetections(pl *transform.Pipeline, args []string) error {
	draw, err := parseBool("draw_detections.draw", args[0])
	if err != nil {
		return err
	}
	classes, err := parseClasses("draw_detections.classes", args[1:])
	if err != nil {
		return err
	}
	pl.DrawDetections = nil
	if draw {
		pl.DrawDetections = &transform.DetectionFilter{Classes: classes}
	}
	return nil
}

// cropObjects is c_obj:scale_factor[:classes...]; scale 0 turns it off.
func cropObjects(pl *transform.Pipeline, args []string) error {
	scale, err := parseRange("crop_objects.scale_factor", args[0], 0, transform.MaxFactor)
	if err != nil {
		return err
	}
	classes, err := parseClasses("crop_objects.classes", args[1:])
	if err != nil {
		return err
	}
	pl.CropObjects = nil
	if scale > 0 {
		pl.CropObjects = &transform.CropObjects{Scale: scale, Classes: classes}
	}
	return nil
}

// Watermark details. They configure the pipeline's watermark, which is
// drawn only when wm sets a non-zero opacity (imgproxy semantics).

func firstWatermark(pl *transform.Pipeline) *transform.Watermark {
	if len(pl.Watermarks) == 0 {
		pl.Watermarks = make([]transform.Watermark, 1)
	}
	return &pl.Watermarks[0]
}

// watermarkURL is wmu:base64url; empty means the configured watermark.
func watermarkURL(w *transform.Watermark, args []string) error {
	if err := maxArgs("watermark_url", args, 1); err != nil {
		return err
	}
	s, err := decodeSourceArg("watermark_url", args[0])
	if err != nil {
		return err
	}
	w.Source = s
	return nil
}

// watermarkText is wmt:base64url Pango markup; it takes precedence over
// watermark_url.
func watermarkText(w *transform.Watermark, args []string) error {
	return oneBase64Text("watermark_text", args, &w.Text)
}

// watermarkSize is wms:width:height in pixels; 0 = auto.
func watermarkSize(w *transform.Watermark, args []string) error {
	if err := maxArgs("watermark_size", args, 2); err != nil {
		return err
	}
	var err error
	if s := args[0]; s != "" {
		if w.Width, err = parseIntRange("watermark_size.width", s, 0, transform.MaxDimension); err != nil {
			return err
		}
	}
	if s := arg(args, 1); s != "" {
		if w.Height, err = parseIntRange("watermark_size.height", s, 0, transform.MaxDimension); err != nil {
			return err
		}
	}
	return nil
}

// watermarkRotate is wmr:degrees, reduced to (-360, 360).
func watermarkRotate(w *transform.Watermark, args []string) error {
	if err := maxArgs("watermark_rotate", args, 1); err != nil {
		return err
	}
	r, err := parseFloat("watermark_rotate", args[0])
	if err != nil {
		return err
	}
	w.Rotate = math.Mod(r, 360)
	return nil
}

// Output.

// colorProfile is cp:srgb|cmyk|file; the file name is percent-decoded and
// must not contain a path.
func colorProfile(o *transform.Output, args []string) error {
	if err := maxArgs("color_profile", args, 1); err != nil {
		return err
	}
	name, err := url.PathUnescape(args[0])
	if err != nil || name == "." || name == ".." || strings.ContainsAny(name, "/\\") || (name != "" && !plausibleSource(name)) {
		return dialect.InvalidArgument("color_profile", args[0], expColorProfile)
	}
	o.ColorProfile = name
	return nil
}

var autoQualityMethods = map[string]transform.AutoQualityMethod{
	"none":  transform.AutoQualityNone,
	"size":  transform.AutoQualitySize,
	"dssim": transform.AutoQualityDSSIM,
	"ml":    transform.AutoQualityML,
}

// autoquality is aq:method:target:min_quality:max_quality:allowed_error,
// every argument optional.
func autoquality(o *transform.Output, args []string) error {
	if err := maxArgs("autoquality", args, 5); err != nil {
		return err
	}
	aq := transform.AutoQuality{}
	if o.AutoQuality != nil {
		aq = *o.AutoQuality
	}
	if s := args[0]; s != "" {
		m, ok := autoQualityMethods[s]
		if !ok {
			return dialect.InvalidArgument("autoquality.method", s, expAQMethod)
		}
		aq.Method = m
	}
	var err error
	if s := arg(args, 1); s != "" {
		if aq.Target, err = parseRange("autoquality.target", s, 0, transform.MaxAutoQualityTarget); err != nil {
			return err
		}
	}
	if s := arg(args, 2); s != "" {
		if aq.Min, err = parseQuality("autoquality.min_quality", s); err != nil {
			return err
		}
	}
	if s := arg(args, 3); s != "" {
		if aq.Max, err = parseQuality("autoquality.max_quality", s); err != nil {
			return err
		}
	}
	if err := optUnit("autoquality.allowed_error", arg(args, 4), &aq.AllowedError); err != nil {
		return err
	}
	o.AutoQuality = &aq
	return nil
}

// jpegOptions is jpgo:progressive:no_subsample:trellis_quant:
// overshoot_deringing:optimize_scans:quant_table, every argument optional.
func jpegOptions(o *transform.Output, args []string) error {
	if err := maxArgs("jpeg_options", args, 6); err != nil {
		return err
	}
	j := transform.JPEGOptions{}
	if o.JPEG != nil {
		j = *o.JPEG
	}
	bools := [5]struct {
		field string
		dst   **bool
	}{
		{"jpeg_options.progressive", &j.Progressive},
		{"jpeg_options.no_subsample", &j.NoSubsample},
		{"jpeg_options.trellis_quant", &j.TrellisQuant},
		{"jpeg_options.overshoot_deringing", &j.OvershootDeringing},
		{"jpeg_options.optimize_scans", &j.OptimizeScans},
	}
	for i, b := range bools {
		if err := optBoolPtr(b.field, arg(args, i), b.dst); err != nil {
			return err
		}
	}
	if s := arg(args, 5); s != "" {
		q, err := parseIntRange("jpeg_options.quant_table", s, 0, transform.MaxJPEGQuant)
		if err != nil {
			return err
		}
		j.QuantTable = &q
	}
	o.JPEG = &j
	return nil
}

// pngOptions is pngo:interlaced:quantize:quantization_colors, every
// argument optional; 0 colours means the default.
func pngOptions(o *transform.Output, args []string) error {
	if err := maxArgs("png_options", args, 3); err != nil {
		return err
	}
	p := transform.PNGOptions{}
	if o.PNG != nil {
		p = *o.PNG
	}
	if err := optBoolPtr("png_options.interlaced", args[0], &p.Interlaced); err != nil {
		return err
	}
	if err := optBoolPtr("png_options.quantize", arg(args, 1), &p.Quantize); err != nil {
		return err
	}
	if s := arg(args, 2); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n == 1 || n < 0 || n > transform.MaxPaletteSize {
			return dialect.InvalidArgument("png_options.quantization_colors", s, expPalette)
		}
		p.QuantizationColors = n
	}
	o.PNG = &p
	return nil
}

var (
	webpCompressions = map[string]transform.WebPCompression{
		"lossy":         transform.WebPLossy,
		"near_lossless": transform.WebPNearLossless,
		"lossless":      transform.WebPLossless,
	}
	webpPresets = map[string]transform.WebPPreset{
		"default": transform.WebPPresetDefault,
		"photo":   transform.WebPPresetPhoto,
		"picture": transform.WebPPresetPicture,
		"drawing": transform.WebPPresetDrawing,
		"icon":    transform.WebPPresetIcon,
		"text":    transform.WebPPresetText,
	}
	avifSubsamples = map[string]transform.Subsampling{
		"auto": transform.SubsamplingAuto,
		"on":   transform.Subsampling420,
		"off":  transform.Subsampling444,
	}
)

// webpOptions is webpo:compression:smart_subsample:preset, every argument
// optional.
func webpOptions(o *transform.Output, args []string) error {
	if err := maxArgs("webp_options", args, 3); err != nil {
		return err
	}
	w := transform.WebPOptions{}
	if o.WebP != nil {
		w = *o.WebP
	}
	if s := args[0]; s != "" {
		c, ok := webpCompressions[s]
		if !ok {
			return dialect.InvalidArgument("webp_options.compression", s, expWebPComp)
		}
		w.Compression = c
	}
	if err := optBoolPtr("webp_options.smart_subsample", arg(args, 1), &w.SmartSubsample); err != nil {
		return err
	}
	if s := arg(args, 2); s != "" {
		p, ok := webpPresets[s]
		if !ok {
			return dialect.InvalidArgument("webp_options.preset", s, expWebPPreset)
		}
		w.Preset = p
	}
	o.WebP = &w
	return nil
}

// avifOptions is avifo:subsample (auto, on = 4:2:0, off = 4:4:4).
func avifOptions(o *transform.Output, args []string) error {
	if err := maxArgs("avif_options", args, 1); err != nil {
		return err
	}
	s, ok := avifSubsamples[args[0]]
	if !ok {
		return dialect.InvalidArgument("avif_options.subsample", args[0], expAVIFSub)
	}
	o.AVIF = &transform.AVIFOptions{Subsample: s}
	return nil
}

// Video.

func video(pl *transform.Pipeline) *transform.VideoOptions {
	if pl.Video == nil {
		pl.Video = &transform.VideoOptions{}
	}
	return pl.Video
}

// videoStep parses a tile or animation step: 0 turns the feature off,
// a negative step means "derive from the duration" and is stored as 0.
func videoStep(field, s string) (step float64, on bool, err error) {
	f, ok := parseFloatRaw(s)
	if !ok || f < -math.MaxInt32 || f > math.MaxInt32 {
		return 0, false, dialect.InvalidArgument(field, s, expFloat)
	}
	return max(f, 0), f != 0, nil
}

// videoFrameOpts parses the extend:trim:fill:focus_x:focus_y tail shared by
// video_thumbnail_tile and video_thumbnail_animation.
func videoFrameOpts(field, extendName string, args []string, extend, trim, fill *bool, fx, fy *float64) error {
	for i, b := range [3]struct {
		name string
		dst  *bool
	}{{extendName, extend}, {"trim", trim}, {"fill", fill}} {
		if s := arg(args, i); s != "" {
			v, err := parseBool(field+"."+b.name, s)
			if err != nil {
				return err
			}
			*b.dst = v
		}
	}
	if err := optUnit(field+".focus_x", arg(args, 3), fx); err != nil {
		return err
	}
	return optUnit(field+".focus_y", arg(args, 4), fy)
}

// videoTile is vtt:step:columns:rows:tile_width:tile_height[:extend_tile:
// trim:fill:focus_x:focus_y]; step 0 turns it off.
func videoTile(v *transform.VideoOptions, args []string) error {
	const field = "video_thumbnail_tile"
	if err := maxArgs(field, args, 10); err != nil {
		return err
	}
	t := transform.VideoTile{FocusX: 0.5, FocusY: 0.5}
	step, on, err := videoStep(field+".step", args[0])
	if err != nil {
		return err
	}
	t.Step = step
	ints := [4]struct {
		name string
		dst  *int
	}{{"columns", &t.Columns}, {"rows", &t.Rows}, {"tile_width", &t.TileWidth}, {"tile_height", &t.TileHeight}}
	for i, n := range ints {
		if s := arg(args, i+1); s != "" {
			if *n.dst, err = parseIntRange(field+"."+n.name, s, 0, transform.MaxDimension); err != nil {
				return err
			}
		}
	}
	if len(args) > 5 {
		if err := videoFrameOpts(field, "extend_tile", args[5:], &t.ExtendTile, &t.Trim, &t.Fill, &t.FocusX, &t.FocusY); err != nil {
			return err
		}
	}
	if !on {
		v.Tile = nil
		return nil
	}
	if t.Columns == 0 {
		return dialect.InvalidArgument(field+".columns", arg(args, 1), expColumnsRows)
	}
	if t.Rows == 0 {
		return dialect.InvalidArgument(field+".rows", arg(args, 2), expColumnsRows)
	}
	v.Tile = &t
	return nil
}

// videoAnimation is vta:step:delay:frames:frame_width:frame_height[:
// extend_frame:trim:fill:focus_x:focus_y]; delay is in milliseconds and
// step 0 turns it off.
func videoAnimation(v *transform.VideoOptions, args []string) error {
	const field = "video_thumbnail_animation"
	if err := maxArgs(field, args, 10); err != nil {
		return err
	}
	a := transform.VideoAnimation{FocusX: 0.5, FocusY: 0.5}
	step, on, err := videoStep(field+".step", args[0])
	if err != nil {
		return err
	}
	a.Step = step
	if s := arg(args, 1); s != "" {
		ms, err := parseRange(field+".delay", s, 0, math.MaxInt32)
		if err != nil {
			return err
		}
		a.Delay = ms / 1000
	}
	ints := [3]struct {
		name string
		dst  *int
	}{{"frames", &a.Frames}, {"frame_width", &a.FrameWidth}, {"frame_height", &a.FrameHeight}}
	for i, n := range ints {
		if s := arg(args, i+2); s != "" {
			if *n.dst, err = parseIntRange(field+"."+n.name, s, 0, transform.MaxDimension); err != nil {
				return err
			}
		}
	}
	if len(args) > 5 {
		if err := videoFrameOpts(field, "extend_frame", args[5:], &a.ExtendFrame, &a.Trim, &a.Fill, &a.FocusX, &a.FocusY); err != nil {
			return err
		}
	}
	if !on {
		v.Animation = nil
		return nil
	}
	if a.Frames == 0 {
		return dialect.InvalidArgument(field+".frames", arg(args, 2), expFrames)
	}
	v.Animation = &a
	return nil
}

// Delivery and source.

// fallbackImageURL is fiu:base64url; empty means the configured fallback.
func fallbackImageURL(d *transform.Delivery, args []string) error {
	if err := maxArgs("fallback_image_url", args, 1); err != nil {
		return err
	}
	s, err := decodeSourceArg("fallback_image_url", args[0])
	if err != nil {
		return err
	}
	d.Fallback = s
	return nil
}

// cacheTags is ct:tag[:tag...]; it replaces earlier tags and "ct:" clears
// them. Tags become header values, so they are visible ASCII without
// commas.
func cacheTags(d *transform.Delivery, args []string) error {
	if len(args) == 1 && args[0] == "" {
		d.CacheTags = nil
		return nil
	}
	if len(args) > transform.MaxListLen {
		return dialect.InvalidArgument("cache_tags", joinArgs(args), expTooManyItems)
	}
	tags := make([]string, 0, len(args))
	for _, t := range args {
		if !validTag(t) {
			return dialect.InvalidArgument("cache_tags", t, expCacheTag)
		}
		tags = append(tags, t)
	}
	d.CacheTags = tags
	return nil
}

func validTag(t string) bool {
	if t == "" {
		return false
	}
	for i := range len(t) {
		if c := t[i]; c <= 0x20 || c >= 0x7f || c == ',' {
			return false
		}
	}
	return true
}

var hashAlgorithms = map[string]transform.HashAlgorithm{
	"md5":    transform.HashMD5,
	"sha1":   transform.HashSHA1,
	"sha256": transform.HashSHA256,
	"sha512": transform.HashSHA512,
}

// hashsum is hs:type:hex; hs:none (or hs:) removes the check.
func hashsum(src *transform.SourceSpec, args []string) error {
	if err := maxArgs("hashsum", args, 2); err != nil {
		return err
	}
	if args[0] == "" || args[0] == "none" {
		src.Hashsum = nil
		return nil
	}
	alg, ok := hashAlgorithms[args[0]]
	if !ok {
		return dialect.InvalidArgument("hashsum.type", args[0], expHashType)
	}
	digest := strings.ToLower(arg(args, 1))
	if len(digest) != alg.HexLen() || strings.Trim(digest, "0123456789abcdef") != "" {
		return dialect.InvalidArgument("hashsum.hashsum", digest, expHexDigest)
	}
	src.Hashsum = &transform.Hashsum{Algorithm: alg, Digest: digest}
	return nil
}
