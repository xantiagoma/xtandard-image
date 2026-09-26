package imagekit

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
	expDim         = "positive integer, fraction below 1 or auto"
	expStartOffset = "seconds (a non-negative number)"
	expDimNoAuto   = "positive integer or fraction below 1"
	expAR          = "<width>-<height> with positive numbers"
	expCrop        = "maintain_ratio, maintain_ratio_no_enlarge, force, at_max, at_max_enlarge or at_least"
	expCropMode    = "pad_resize, pad_resize_no_enlarge, extract or pad_extract"
	expFocus       = "compass position, auto, face, custom or an object name"
	expPosition    = "center, top, left, bottom, right, top_left, top_right, bottom_left or bottom_right"
	expCoord       = "non-negative number"
	expPositive    = "positive number"
	expQuality     = "integer 1-100"
	expFormat      = "auto, orig, jpg, jpeg, png, webp, avif, gif, jxl, svg, mp4 or webm"
	expDPR         = "number 0.1-5 or auto"
	expBlur        = "number 1-100"
	expSharpen     = "non-negative number"
	expUSM         = "<radius>-<sigma>-<amount>-<threshold> with non-negative numbers"
	expRadius      = "max, <n> or <tl>_<tr>_<br>_<bl> with non-negative integers"
	expBorder      = "<width>_<color>"
	expColor       = "RRGGBB, RRGGBBAA or a color name"
	expRotate      = "degrees, N<degrees> or auto"
	expFlip        = "h, v or h_v"
	expTrim        = "true, false or integer 1-99"
	expOpacity     = "number 0-100"
	expPath        = "non-empty path"
	expPage        = "<n> or <from>_<to> with positive integers"
	expOffset      = "integer, N<integer> for negative"
	expLayerSize   = "positive integer"
)

// Crop strategies (c-) and modes (cm-).
const (
	cropMaintainRatio          = "maintain_ratio"
	cropMaintainRatioNoEnlarge = "maintain_ratio_no_enlarge"
	cropForce                  = "force"
	cropAtMax                  = "at_max"
	cropAtMaxEnlarge           = "at_max_enlarge"
	cropAtLeast                = "at_least"

	modePadResize          = "pad_resize"
	modePadResizeNoEnlarge = "pad_resize_no_enlarge"
	modeExtract            = "extract"
	modePadExtract         = "pad_extract"
)

// compass maps ImageKit focus/position names to gravities.
var compass = map[string]transform.GravityType{
	"center":       transform.GravityCenter,
	"top":          transform.GravityNorth,
	"bottom":       transform.GravitySouth,
	"left":         transform.GravityWest,
	"right":        transform.GravityEast,
	"top_left":     transform.GravityNorthWest,
	"top_right":    transform.GravityNorthEast,
	"bottom_left":  transform.GravitySouthWest,
	"bottom_right": transform.GravitySouthEast,
}

// parseState is the per-request parse state.
type parseState struct {
	p           *Parser
	b           *imgproxy.Builder
	mountSource string
	params      []string // scratch
}

// dimArg is a parsed w- or h- value.
type dimArg struct {
	set  bool
	auto bool
	d    transform.Dim
}

// optFloat is an optional number.
type optFloat struct {
	set bool
	v   float64
}

// focus is a parsed fo- value.
type focus struct {
	set bool
	g   transform.Gravity
}

// step collects the parameters of one chain step whose effect depends on
// each other (size, crop strategy and mode, focus, coordinates,
// background); the rest apply as they are read.
type step struct {
	w, h       dimArg
	ar         float64
	crop, mode string
	fo         focus
	x, y       optFloat
	xc, yc     optFloat
	zoom       float64
	bg         *transform.Color
	bgBlurred  bool
}

// coord returns the field for x, y, xc or yc.
func (st *step) coord(key string) *optFloat {
	switch key {
	case "x":
		return &st.x
	case "y":
		return &st.y
	case "xc":
		return &st.xc
	default:
		return &st.yc
	}
}

// transformations applies a decoded transformation string: ":"-separated
// steps, one pipeline each. Empty steps and parameters are skipped.
func (ps *parseState) transformations(tr string) error {
	first := true
	for s := range strings.SplitSeq(tr, ":") {
		ps.params = ps.params[:0]
		for param := range strings.SplitSeq(s, ",") {
			if param != "" {
				ps.params = append(ps.params, param)
			}
		}
		if len(ps.params) == 0 {
			continue
		}
		if !first {
			if err := ps.b.NewPipeline(); err != nil {
				return err
			}
		}
		first = false
		if err := ps.step(ps.params); err != nil {
			return err
		}
	}
	return nil
}

// splitParam splits "key-value". Effects ("e-sharpen-10") and layers
// ("l-image") keep their two-part name.
func splitParam(param string) (key, value string) {
	if rest, ok := strings.CutPrefix(param, "e-"); ok {
		name, v, _ := strings.Cut(rest, "-")
		return "e-" + name, v
	}
	if strings.HasPrefix(param, "l-") {
		return param, ""
	}
	key, value, _ = strings.Cut(param, "-")
	return key, value
}

// layerEnd returns the index of the l-end closing the layer opened at
// params[start] (nested layers counted), or len(params) when the step
// ends first.
func layerEnd(params []string, start int) int {
	depth := 0
	for i := start; i < len(params); i++ {
		switch {
		case params[i] == "l-end":
			depth--
			if depth == 0 {
				return i
			}
		case strings.HasPrefix(params[i], "l-"):
			depth++
		}
	}
	return len(params)
}

// step applies one chain step.
func (ps *parseState) step(params []string) error {
	var st step
	for i := 0; i < len(params); i++ {
		key, value := splitParam(params[i])
		if strings.HasPrefix(key, "l-") {
			if key == "l-end" {
				return dialect.InvalidURL("l-end without a layer")
			}
			end := layerEnd(params, i)
			if err := ps.layer(key, params[i+1:end]); err != nil {
				return err
			}
			i = end
			continue
		}
		if err := ps.param(&st, key, value); err != nil {
			return err
		}
	}
	return ps.finishStep(&st)
}

// param applies one parameter of a step.
//
//nolint:gocyclo // one flat switch over the parameter table is the clearest form
func (ps *parseState) param(st *step, key, v string) error {
	r := ps.b.Request()
	pl := ps.b.Pipeline()
	var err error
	switch key {
	// Size and crop.
	case "w":
		return ps.dim(&st.w, "w", v, true)
	case "h":
		return ps.dim(&st.h, "h", v, false)
	case "ar":
		if isExpression(v) {
			return ps.unsupportedAs("ar", reasonExpression)
		}
		st.ar, err = parseAspectRatio(v)
	case "c":
		switch v {
		case cropMaintainRatio, cropMaintainRatioNoEnlarge, cropForce, cropAtMax, cropAtMaxEnlarge, cropAtLeast:
			st.crop = v
		default:
			return dialect.InvalidArgument("c", v, expCrop)
		}
	case "cm":
		switch v {
		case modePadResize, modePadResizeNoEnlarge, modeExtract, modePadExtract:
			st.mode = v
		default:
			return dialect.InvalidArgument("cm", v, expCropMode)
		}
	case "fo":
		return ps.focus(st, v)
	case "x", "y", "xc", "yc":
		if isExpression(v) {
			return ps.unsupportedAs(key, reasonExpression)
		}
		f, ok := parseFloatIn(v, 0, transform.MaxDimension)
		if !ok {
			return dialect.InvalidArgument(key, v, expCoord)
		}
		*st.coord(key) = optFloat{set: true, v: f}
	case "z":
		f, ok := parseFloatIn(v, 0, transform.MaxFactor)
		if !ok || f == 0 {
			return dialect.InvalidArgument("z", v, expPositive)
		}
		st.zoom = f
	case "bg":
		return ps.background(st, v)

	// Output (request-wide).
	case "q":
		n, convErr := strconv.Atoi(v)
		if convErr != nil || n < 1 || n > transform.MaxQuality {
			return dialect.InvalidArgument("q", v, expQuality)
		}
		r.Output.Quality = n
	case "f":
		return ps.format(v)
	case "pr":
		err = boolPtr("pr", v, &r.Output.Progressive, false)
	case "lo":
		err = boolPtr("lo", v, &r.Output.Lossless, false)
	case "md":
		err = boolPtr("md", v, &r.Output.StripMetadata, true)
	case "cp":
		err = boolPtr("cp", v, &r.Output.StripColorProfile, true)
	case "orig":
		var on bool
		if on, err = imgproxy.ParseBool("orig", v); err == nil && on {
			r.Kind = transform.KindRaw
		}
	case "dpr":
		return ps.dpr(v)

	// Effects.
	case "bl", "blur": // "blur" is what unpic's generator emits
		f, ok := parseFloatIn(v, 0, 100)
		if !ok || f == 0 {
			return dialect.InvalidArgument(key, v, expBlur)
		}
		pl.Blur = f / 2
	case "e-sharpen":
		pl.Sharpen, err = sharpenSigma(v)
	case "e-usm":
		return ps.unsharp(pl, v)
	case "e-grayscale":
		pl.Grayscale = true
	case "e-contrast":
		// Kept for the cache key; the planner has no equalize step
		// (plan.Unplanned).
		pl.Equalize = true
		ps.b.Ignore(key, reasonNotImplemented)
	case "r":
		pl.Radius, err = parseRadius(v)
	case "b":
		pl.Border, err = parseBorder(v)
	case "rt":
		return ps.rotate(pl, v)
	case "fl":
		switch v {
		case "h":
			pl.Flip = transform.Flip{H: true}
		case "v":
			pl.Flip = transform.Flip{V: true}
		case "h_v", "v_h":
			pl.Flip = transform.Flip{H: true, V: true}
		default:
			return dialect.InvalidArgument("fl", v, expFlip)
		}
	case "t":
		pl.Trim, err = parseTrim(v)
	case "o":
		f, ok := parseFloatIn(v, 0, 100)
		if !ok {
			return dialect.InvalidArgument("o", v, expOpacity)
		}
		op := f / 100
		pl.Adjust.Opacity = &op

	// Delivery and presets.
	case "n":
		return ps.named(v)
	case "di":
		path := decodeLayerPath(v)
		if path == "" {
			return dialect.InvalidArgument("di", v, expPath)
		}
		r.Delivery.Fallback = &transform.SourceSpec{Raw: relativeKey(path), Encoding: transform.SourceRelative, Mount: ps.mountSource}
	case "pg":
		return ps.page(v)
	case "so":
		return ps.startOffset(v)

	// Video, AI and other features without an equivalent.
	case "vc", "ac", "eo", "du", "sr", "cr", "dn", "e-bgremove", "e-removedotbg", "e-upscale",
		"e-retouch", "e-genvar", "e-dropshadow", "e-shadow", "e-gradient", "e-changebg", "e-edit",
		"e-distort", "e-colorize":
		return ps.unsupported(key)
	default:
		return ps.unknown(key)
	}
	return err
}

// finishStep applies the size, crop and focus parameters of a step.
func (ps *parseState) finishStep(st *step) error {
	r := ps.b.Request()
	pl := ps.b.Pipeline()
	if st.w.set {
		pl.Resize.Width = st.w.d
		r.ClientHints.Width = r.ClientHints.Width || st.w.auto
	}
	if st.h.set {
		pl.Resize.Height = st.h.d
	}
	if st.ar > 0 {
		pl.Resize.AspectRatio = st.ar
	}
	sized := st.w.set || st.h.set || st.ar > 0
	coords := st.x.set || st.y.set || st.xc.set || st.yc.set

	switch st.mode {
	case modeExtract, modePadExtract:
		crop := &transform.Crop{Width: pl.Resize.Width, Height: pl.Resize.Height, Gravity: ps.extractGravity(st, pl)}
		pl.Crop = crop
		if st.mode == modePadExtract {
			pl.Resize.Type = transform.ResizeFit
			pl.Resize.Enlarge = false
			pl.Extend = transform.Extend{Enabled: true, Gravity: transform.Gravity{Type: transform.GravityCenter}}
		} else {
			pl.Resize.Width, pl.Resize.Height = transform.Dim{}, transform.Dim{}
			if st.ar > 0 {
				pl.Resize.AspectRatio = 0
				ps.b.Ignore("ar", reasonNotApplied)
			}
		}
		coords = false
	case modePadResize, modePadResizeNoEnlarge:
		pl.Resize.Type = transform.ResizeFit
		pl.Resize.Enlarge = st.mode == modePadResize
		g := transform.Gravity{Type: transform.GravityCenter}
		if st.fo.set {
			if st.fo.g.Type.IsCompass() {
				g = st.fo.g
			} else {
				ps.b.Ignore("fo", reasonNotApplied)
			}
		}
		pl.Extend = transform.Extend{Enabled: true, Gravity: g}
	default:
		if sized || st.crop != "" {
			pl.Resize.Type, pl.Resize.Enlarge = cropStrategy(st.crop)
		}
		if st.fo.set {
			pl.Gravity = st.fo.g
		}
	}
	if coords {
		ps.b.Ignore("x", reasonNotApplied)
	}
	if st.zoom > 0 {
		// z- zooms on the faces or objects of fo-face / fo-<object>: their
		// box scaled by 1/z is cropped before the resize (WP-72).
		switch {
		case pl.Crop != nil || !st.fo.set:
			ps.b.Ignore("z", reasonNotApplied)
		case pl.Gravity.Type == transform.GravityFace:
			pl.Gravity.FacePad = 1 / st.zoom
		case pl.Gravity.Type == transform.GravityObject:
			pl.CropObjects = &transform.CropObjects{Scale: 1 / st.zoom, Classes: pl.Gravity.Classes}
		default:
			ps.b.Ignore("z", reasonNotApplied)
		}
	}
	if st.bg != nil {
		pl.Background = st.bg
	}
	if st.bgBlurred {
		pl.Extend.Mode = transform.ExtendBlur
		ps.b.Ignore("bg-blurred", reasonM3)
	}
	return nil
}

// cropStrategy maps a c- value ("" = the default maintain_ratio).
func cropStrategy(c string) (transform.ResizeType, bool) {
	switch c {
	case cropMaintainRatioNoEnlarge:
		return transform.ResizeFill, false
	case cropForce:
		return transform.ResizeForce, true
	case cropAtMax:
		return transform.ResizeFit, false
	case cropAtMaxEnlarge:
		return transform.ResizeFit, true
	case cropAtLeast:
		return transform.ResizeOutside, true
	default:
		return transform.ResizeFill, true
	}
}

// extractGravity positions a cm-extract crop: x/y are its top-left
// corner, xc/yc its centre (converted to the corner, which needs pixel
// sizes), otherwise fo- or the centre.
func (ps *parseState) extractGravity(st *step, pl *transform.Pipeline) transform.Gravity {
	switch {
	case st.x.set || st.y.set:
		return transform.Gravity{Type: transform.GravityNorthWest, XOffset: st.x.v, YOffset: st.y.v}
	case st.xc.set || st.yc.set:
		w, h := pl.Resize.Width.Px, pl.Resize.Height.Px
		if w == 0 || h == 0 {
			ps.b.Ignore("xc", reasonNotApplied)
			break
		}
		return transform.Gravity{
			Type:    transform.GravityNorthWest,
			XOffset: math.Max(st.xc.v-float64(w)/2, 0),
			YOffset: math.Max(st.yc.v-float64(h)/2, 0),
		}
	case st.fo.set:
		return st.fo.g
	}
	return transform.Gravity{Type: transform.GravityCenter}
}

// dim parses w- or h-.
func (ps *parseState) dim(dst *dimArg, field, v string, allowAuto bool) error {
	if v == "auto" && allowAuto {
		*dst = dimArg{set: true, auto: true}
		return nil
	}
	if isExpression(v) {
		return ps.unsupportedAs(field, reasonExpression)
	}
	exp := expDimNoAuto
	if allowAuto {
		exp = expDim
	}
	if n, err := strconv.Atoi(v); err == nil {
		if n < 1 || n > transform.MaxDimension {
			return dialect.InvalidArgument(field, v, exp)
		}
		*dst = dimArg{set: true, d: transform.Dim{Px: n}}
		return nil
	}
	f, ok := parseFloatIn(v, 0, 1)
	if !ok || f == 0 || f == 1 {
		return dialect.InvalidArgument(field, v, exp)
	}
	*dst = dimArg{set: true, d: transform.Dim{Rel: f}}
	return nil
}

// dpr parses dpr-<n> or dpr-auto.
func (ps *parseState) dpr(v string) error {
	if v == "auto" {
		ps.b.Request().ClientHints.DPR = true
		return nil
	}
	f, ok := parseFloatIn(v, 0.1, 5)
	if !ok {
		return dialect.InvalidArgument("dpr", v, expDPR)
	}
	ps.b.Pipeline().Resize.DPR = f
	return nil
}

// focus parses fo-.
func (ps *parseState) focus(st *step, v string) error {
	if gt, ok := compass[v]; ok {
		st.fo = focus{set: true, g: transform.Gravity{Type: gt}}
		return nil
	}
	switch v {
	case "auto", "auto-custom_override":
		st.fo = focus{set: true, g: transform.Gravity{Type: transform.GravitySmart}}
	case "face", "faces":
		st.fo = focus{set: true, g: transform.Gravity{Type: transform.GravityFace}}
	case "custom":
		// A per-file custom crop area stored in the ImageKit media library.
		return ps.unsupportedAs("fo-custom", reasonUnsupported)
	default:
		if !isObjectName(v) {
			return dialect.InvalidArgument("fo", v, expFocus)
		}
		cs, ok := objectClasses(v)
		if !ok {
			// ImageKit detects more object types than the COCO classes of
			// the object model: the smart crop stands in.
			st.fo = focus{set: true, g: transform.Gravity{Type: transform.GravitySmart}}
			ps.b.Ignore("fo-"+v, reasonUnsupportedObject)
			return nil
		}
		st.fo = focus{set: true, g: transform.Gravity{Type: transform.GravityObject, Classes: cs}}
	}
	return nil
}

// background parses bg-.
func (ps *parseState) background(st *step, v string) error {
	switch {
	case v == "blurred" || strings.HasPrefix(v, "blurred_"):
		st.bgBlurred = true
		return nil
	case v == "dominant" || strings.HasPrefix(v, "gradient") || strings.HasPrefix(v, "genfill"):
		return ps.unsupportedAs("bg-"+firstWord(v), reasonUnsupported)
	}
	c, ok := parseColor(v)
	if !ok {
		return dialect.InvalidArgument("bg", v, expColor)
	}
	st.bg = &c
	return nil
}

// format parses f-.
func (ps *parseState) format(v string) error {
	o := &ps.b.Request().Output
	switch v {
	case "auto", "orig":
		o.Format = transform.FormatUnset
		return nil
	case "svg":
		return ps.unsupportedAs("f-svg", reasonUnsupported)
	case "mp4":
		o.Format = transform.FormatMP4
		return nil
	case "webm":
		o.Format = transform.FormatWebM
		return nil
	case "jpg", "jpeg", "png", "webp", "avif", "gif", "jxl":
		f, err := imgproxy.ParseFormat("f", v)
		if err != nil {
			return err
		}
		o.Format = f
		return nil
	}
	return dialect.InvalidArgument("f", v, expFormat)
}

// unsharp parses e-usm-<radius>-<sigma>-<amount>-<threshold>. libvips
// sharpening is driven by sigma; radius and threshold have no equivalent.
func (ps *parseState) unsharp(pl *transform.Pipeline, v string) error {
	var args [4]float64
	n := 0
	for part := range strings.SplitSeq(v, "-") {
		if n == len(args) {
			return dialect.InvalidArgument("e-usm", v, expUSM)
		}
		f, ok := parseFloatIn(part, 0, transform.MaxSigma)
		if !ok {
			return dialect.InvalidArgument("e-usm", v, expUSM)
		}
		args[n] = f
		n++
	}
	if n != len(args) {
		return dialect.InvalidArgument("e-usm", v, expUSM)
	}
	pl.Unsharp = &transform.Unsharp{Sigma: args[1], Amount: args[2]}
	return nil
}

// rotate parses rt-<deg>, rt-N<deg> (counter-clockwise) and rt-auto.
func (ps *parseState) rotate(pl *transform.Pipeline, v string) error {
	if v == "auto" {
		on := true
		pl.AutoRotate = &on
		return nil
	}
	deg, ok := parseSigned(v, 360)
	if !ok {
		return dialect.InvalidArgument("rt", v, expRotate)
	}
	if deg == math.Trunc(deg) && int(deg)%90 == 0 {
		pl.Rotate = (int(deg)%360 + 360) % 360
		pl.RotateAny = 0
		return nil
	}
	a := math.Mod(deg, 360)
	switch {
	case a > 180:
		a -= 360
	case a <= -180:
		a += 360
	}
	pl.RotateAny = a
	return nil
}

// named expands n-<name> as a preset.
func (ps *parseState) named(v string) error {
	if v == "" {
		return dialect.InvalidArgument("n", v, "named transformation")
	}
	if ps.p.o.Presets == nil {
		ps.b.Ignore("n", reasonNoPresets)
	}
	return ps.b.Apply("preset", v)
}

// page parses pg-<n> (1-based) and pg-<from>_<to>.
func (ps *parseState) page(v string) error {
	from, to, isRange := strings.Cut(v, "_")
	a, err := strconv.Atoi(from)
	if err != nil || a < 1 {
		return dialect.InvalidArgument("pg", v, expPage)
	}
	if err := ps.b.Apply("page", strconv.Itoa(a-1)); err != nil {
		return err
	}
	if !isRange {
		return nil
	}
	z, err := strconv.Atoi(to)
	if err != nil || z < a {
		return dialect.InvalidArgument("pg", v, expPage)
	}
	return ps.b.Apply("pages", strconv.Itoa(z-a+1))
}

// startOffset is so-<seconds>: the frame of a video source a thumbnail
// (/ik-thumbnail.jpg) is taken from, imgproxy video_thumbnail_second.
// Arithmetic expressions have no equivalent.
func (ps *parseState) startOffset(v string) error {
	sec, err := strconv.ParseFloat(v, 64)
	if err != nil {
		if strings.ContainsAny(v, "_") {
			return ps.unsupportedAs("so", reasonUnsupported)
		}
		return dialect.InvalidArgument("so", v, expStartOffset)
	}
	if sec < 0 {
		return dialect.InvalidArgument("so", v, expStartOffset)
	}
	return ps.b.Apply("video_thumbnail_second", strconv.FormatFloat(sec, 'f', -1, 64))
}

// unknown handles a parameter the dialect does not know.
func (ps *parseState) unknown(key string) error {
	if ps.p.o.Strict {
		return dialect.UnknownOption(key)
	}
	ps.b.Ignore(key, reasonUnknown)
	return nil
}

// unsupported handles a known parameter without an equivalent.
func (ps *parseState) unsupported(key string) error {
	return ps.unsupportedAs(key, reasonUnsupported)
}

func (ps *parseState) unsupportedAs(key, reason string) error {
	if ps.p.o.Strict {
		return dialect.UnknownOption(key)
	}
	ps.b.Ignore(key, reason)
	return nil
}

// sharpenSigma maps e-sharpen[-n] to a Gaussian sigma: the bare effect is
// sigma 1, and n follows imgix's sharp scale (0.5 + n/50), an
// approximation of ImageKit's undocumented unit.
func sharpenSigma(v string) (float64, error) {
	if v == "" || v == "true" {
		return 1, nil
	}
	f, ok := parseFloatIn(v, 0, 1000)
	if !ok {
		return 0, dialect.InvalidArgument("e-sharpen", v, expSharpen)
	}
	return 0.5 + f/50, nil
}

// parseAspectRatio parses ar-<w>-<h> (also "_" as separator).
func parseAspectRatio(v string) (float64, error) {
	i := strings.IndexAny(v, "-_")
	if i < 0 {
		return 0, dialect.InvalidArgument("ar", v, expAR)
	}
	w, ok1 := parseFloatIn(v[:i], 0, transform.MaxDimension)
	h, ok2 := parseFloatIn(v[i+1:], 0, transform.MaxDimension)
	if !ok1 || !ok2 || w == 0 || h == 0 {
		return 0, dialect.InvalidArgument("ar", v, expAR)
	}
	ar := w / h
	if ar > transform.MaxFactor || ar < 1/transform.MaxFactor {
		return 0, dialect.InvalidArgument("ar", v, expAR)
	}
	return ar, nil
}

// parseRadius parses r-max, r-<n> and r-<tl>_<tr>_<br>_<bl>.
func parseRadius(v string) (*transform.Radius, error) {
	if v == "max" {
		return &transform.Radius{Max: true}, nil
	}
	var c [4]int
	n := 0
	for part := range strings.SplitSeq(v, "_") {
		if n == len(c) {
			return nil, dialect.InvalidArgument("r", v, expRadius)
		}
		x, err := strconv.Atoi(part)
		if err != nil || x < 0 || x > transform.MaxDimension {
			return nil, dialect.InvalidArgument("r", v, expRadius)
		}
		c[n] = x
		n++
	}
	switch n {
	case 1:
		c[1], c[2], c[3] = c[0], c[0], c[0]
	case 4:
	default:
		return nil, dialect.InvalidArgument("r", v, expRadius)
	}
	return &transform.Radius{TopLeft: c[0], TopRight: c[1], BottomRight: c[2], BottomLeft: c[3]}, nil
}

// parseBorder parses b-<width>_<color> ("-" is accepted as separator
// because the SDK's own tests write it).
func parseBorder(v string) (*transform.Border, error) {
	i := strings.IndexAny(v, "_-")
	if i < 0 {
		return nil, dialect.InvalidArgument("b", v, expBorder)
	}
	w, err := strconv.Atoi(v[:i])
	if err != nil || w < 0 || w > transform.MaxDimension {
		return nil, dialect.InvalidArgument("b", v, expBorder)
	}
	c, ok := parseColor(v[i+1:])
	if !ok {
		return nil, dialect.InvalidArgument("b", v, expBorder)
	}
	return &transform.Border{Top: w, Right: w, Bottom: w, Left: w, Color: c}, nil
}

// defaultTrimThreshold is imgproxy's (and ImageKit's t-true) threshold.
const defaultTrimThreshold = 10

// parseTrim parses t-true, t-false and t-<1..99>.
func parseTrim(v string) (*transform.Trim, error) {
	switch v {
	case "true":
		return &transform.Trim{Threshold: defaultTrimThreshold}, nil
	case "false":
		return nil, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 99 {
		return nil, dialect.InvalidArgument("t", v, expTrim)
	}
	return &transform.Trim{Threshold: float64(n)}, nil
}

// boolPtr parses a true/false flag into *dst, inverted when invert is set
// (md-true means "do not strip").
func boolPtr(field, v string, dst **bool, invert bool) error {
	on, err := imgproxy.ParseBool(field, v)
	if err != nil {
		return err
	}
	on = on != invert
	*dst = &on
	return nil
}

// parseFloatIn parses a finite number in [lo, hi].
func parseFloatIn(s string, lo, hi float64) (float64, bool) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || f < lo || f > hi {
		return 0, false
	}
	return f, true
}

// parseSigned parses <n> or N<n> (negative) with |n| <= limit.
func parseSigned(s string, limit float64) (float64, bool) {
	neg := false
	if rest, ok := strings.CutPrefix(s, "N"); ok {
		neg, s = true, rest
	}
	f, ok := parseFloatIn(s, 0, limit)
	if !ok {
		return 0, false
	}
	if neg {
		f = -f
	}
	return f, true
}

// isExpression reports whether v is an ImageKit arithmetic expression
// such as "iw_div_2" or "bh_mul_0.5".
func isExpression(v string) bool {
	if v == "" || v[0] < 'a' || v[0] > 'z' {
		return false
	}
	return strings.Contains(v, "_")
}

// objectClasses resolves fo-<object>: one class name with underscores for
// spaces ("traffic_light") or several joined by underscores
// ("truck_car_train"); ok is false when a name is not a class or
// supercategory the object model knows.
func objectClasses(v string) ([]string, bool) {
	if cs, ok := transform.ResolveClass(v); ok {
		return cs, true
	}
	return transform.ResolveClasses(strings.Split(v, "_"))
}

// isObjectName reports whether v looks like an object class list
// ("car", "traffic_light", "truck_car_train").
func isObjectName(v string) bool {
	if v == "" || len(v) > 64 {
		return false
	}
	for i := range len(v) {
		c := v[i]
		if (c < 'a' || c > 'z') && c != '_' {
			return false
		}
	}
	return true
}

// firstWord returns v up to the first "_".
func firstWord(v string) string {
	w, _, _ := strings.Cut(v, "_")
	return w
}
