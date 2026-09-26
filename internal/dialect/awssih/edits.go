package awssih

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// builder applies SIH edits to one request.
type builder struct {
	p *Parser
	r *transform.Request
	// pre is the pipeline edits apply to; post is a second pipeline created
	// for an extract that follows resize in the edits' key order (sharp
	// crops after resizing in that case).
	pre, post  *transform.Pipeline
	resizeSeen bool
	// Output format bookkeeping (see finishFormat).
	toFormat   transform.Format // edits.toFormat
	editFormat transform.Format // last format edit with force != false
	outFormat  transform.Format // top-level outputFormat
	perFormat  map[transform.Format]*formatOpts
}

// formatOpts are the request-wide options of one sharp format edit that
// apply only when that format is the output.
type formatOpts struct {
	lossless *bool
	effort   int // normalised 1..9; 0 = unset
}

func newBuilder(p *Parser, r *transform.Request) *builder {
	return &builder{p: p, r: r, pre: &r.Pipelines[len(r.Pipelines)-1]}
}

func (b *builder) ignore(option, reason string) {
	for _, ig := range b.r.Ignored {
		if ig.Option == option && ig.Reason == reason {
			return
		}
	}
	b.r.Ignored = append(b.r.Ignored, transform.Ignored{Option: option, Reason: reason})
}

// unsupported records an edit SIH knows but xtandard-image does not apply;
// in strict mode it is a 400 like an unknown option.
func (b *builder) unsupported(name string) error {
	if b.p.o.Strict {
		return dialect.UnknownOption(name)
	}
	b.ignore(name, reasonUnsupported)
	return nil
}

func invalidJSON(field, expected string) error {
	return dialect.InvalidArgument(field, "", expected)
}

// unsupportedEdits are sharp operations on SIH's edit allowlist
// (lib/constants.ts) that have no equivalent in the request model, plus
// SIH's Rekognition content moderation.
var unsupportedEdits = map[string]bool{
	"removeAlpha": true, "ensureAlpha": true, "extractChannel": true, "joinChannel": true, "bandbool": true,
	"pipelineColourspace": true, "pipelineColorspace": true, "toColourspace": true, "toColorspace": true,
	"affine": true, "median": true, "unflatten": true, "gamma": true, "clahe": true, "convolve": true,
	"threshold": true, "boolean": true, "linear": true, "recomb": true, "contentModeration": true,
}

// applyEdit applies one member of the edits object.
//
//nolint:gocyclo // one flat switch over SIH's edit allowlist is the clearest form
func (b *builder) applyEdit(name string, raw json.RawMessage) error {
	pl := b.pre
	switch name {
	case "resize":
		b.resizeSeen = true
		return b.resize(raw)
	case "extract", "crop":
		return b.extract(name, raw)
	case "extend":
		return b.extend(raw)
	case "trim":
		return b.trim(raw)
	case "rotate":
		return b.rotate(raw)
	case "flip":
		pl.Flip.V = sharpFlag(raw)
	case "flop":
		pl.Flip.H = sharpFlag(raw)
	case "greyscale", "grayscale":
		pl.Grayscale = sharpFlag(raw)
	case "negate":
		pl.Negate = sharpFlag(raw)
	case "normalise", "normalize":
		pl.Equalize = sharpFlag(raw)
		if kind(raw) == kindObject {
			b.ignore(name, reasonApproximated) // lower/upper percentiles
		}
	case "flatten":
		return b.flatten(raw)
	case "tint":
		c, err := parseColor("tint", raw)
		if err != nil {
			return err
		}
		pl.Tint = &c
	case "sharpen":
		return b.sharpen(raw)
	case "blur":
		return b.blur(raw)
	case "modulate":
		return b.modulate(raw)
	case "toFormat":
		s, ok := asString(raw)
		if !ok {
			return invalidJSON("toFormat", formatExpected)
		}
		f, err := parseFormat("toFormat", s)
		if err != nil {
			return err
		}
		b.toFormat = f
	case "jpeg", "png", "webp", "avif", "heif", "gif", "tiff":
		return b.formatEdit(name, raw)
	case "overlayWith":
		return b.overlay(raw)
	case "smartCrop":
		return b.smartCrop(raw)
	case "roundCrop":
		return b.roundCrop(raw)
	case "animated":
		v, ok := asBool(raw)
		if !ok {
			return invalidJSON("animated", "a boolean")
		}
		if v {
			all := -1
			pl.Pages, pl.DisableAnimation = &all, false
		} else {
			pl.Pages, pl.DisableAnimation = nil, true
		}
	case "stripExif":
		if v, _ := asBool(raw); v {
			b.r.Output.StripMetadata = new(true)
		}
	case "stripIcc":
		if v, _ := asBool(raw); v {
			b.r.Output.StripColorProfile = new(true)
		}
	default:
		if unsupportedEdits[name] {
			return b.unsupported(name)
		}
		if b.p.o.Strict {
			return dialect.UnknownOption(name)
		}
		b.ignore(truncate(name), reasonUnknown)
	}
	return nil
}

// Sharp fit values (lib/enums.ts ImageFitTypes).
func resizeType(fit string) (transform.ResizeType, bool) {
	switch fit {
	case "cover":
		return transform.ResizeFill, true
	case "contain", "inside":
		return transform.ResizeFit, true
	case "fill":
		return transform.ResizeForce, true
	case "outside":
		return transform.ResizeOutside, true
	}
	return 0, false
}

// Sharp positions, gravities and strategies (sharp lib/resize.mjs), by
// name and by their numeric constants.
var positions = map[string]transform.GravityType{
	"center": transform.GravityCenter, "centre": transform.GravityCenter, "0": transform.GravityCenter,
	"top": transform.GravityNorth, "north": transform.GravityNorth, "1": transform.GravityNorth,
	"right": transform.GravityEast, "east": transform.GravityEast, "2": transform.GravityEast,
	"bottom": transform.GravitySouth, "south": transform.GravitySouth, "3": transform.GravitySouth,
	"left": transform.GravityWest, "west": transform.GravityWest, "4": transform.GravityWest,
	"right top": transform.GravityNorthEast, "northeast": transform.GravityNorthEast, "5": transform.GravityNorthEast,
	"right bottom": transform.GravitySouthEast, "southeast": transform.GravitySouthEast, "6": transform.GravitySouthEast,
	"left bottom": transform.GravitySouthWest, "southwest": transform.GravitySouthWest, "7": transform.GravitySouthWest,
	"left top": transform.GravityNorthWest, "northwest": transform.GravityNorthWest, "8": transform.GravityNorthWest,
	"entropy": transform.GravityEntropy, "16": transform.GravityEntropy,
	"attention": transform.GravitySmart, "17": transform.GravitySmart,
}

const positionExpected = "a sharp position, gravity or strategy (centre, top, right top, north, entropy, attention, ...)"

func parsePosition(name string, raw json.RawMessage) (transform.GravityType, error) {
	var key string
	switch kind(raw) {
	case kindString:
		key, _ = asString(raw)
		key = strings.ToLower(strings.TrimSpace(key))
	case kindNumber:
		key = strings.TrimSpace(string(raw))
	default:
	}
	g, ok := positions[key]
	if !ok {
		return 0, dialect.InvalidArgument(name, string(raw), positionExpected)
	}
	return g, nil
}

var kernels = map[string]transform.Kernel{
	"nearest": transform.KernelNearest, "linear": transform.KernelLinear, "cubic": transform.KernelCubic,
	"mitchell": transform.KernelMitchell, "lanczos2": transform.KernelLanczos2, "lanczos3": transform.KernelLanczos3,
}

// sharp's default contain background is opaque black.
var black = transform.Color{A: 255}

// resize maps sharp resize options. SIH rounds width and height with
// Number() + Math.round and rejects values <= 0 with a 400; null is auto.
// sharp enlarges by default, so Enlarge is !withoutEnlargement.
func (b *builder) resize(raw json.RawMessage) error {
	m, err := objectFields(raw)
	if err != nil {
		return invalidJSON("resize", "an object")
	}
	pl := b.pre
	pl.Resize = transform.Resize{Type: transform.ResizeFill, Enlarge: true}
	var (
		width, height float64
		ratio         float64
		fit           = "cover"
		position      = transform.GravityUnset
		background    *transform.Color
	)
	for _, f := range m {
		switch f.key {
		case "width", "height":
			if kind(f.raw) == kindNull {
				continue
			}
			v, ok := asNumber(f.raw)
			if !ok || jsRound(v) <= 0 {
				return dialect.InvalidArgument("resize."+f.key, string(f.raw), "a positive number or null")
			}
			if f.key == "width" {
				width = jsRound(v)
			} else {
				height = jsRound(v)
			}
		case "fit":
			s, ok := asString(f.raw)
			if _, known := resizeType(s); !ok || !known {
				return dialect.InvalidArgument("resize.fit", string(f.raw), "cover, contain, fill, inside or outside")
			}
			fit = s
		case "position", "gravity":
			g, err := parsePosition("resize."+f.key, f.raw)
			if err != nil {
				return err
			}
			position = g
		case "background":
			c, err := parseColor("resize.background", f.raw)
			if err != nil {
				return err
			}
			background = &c
		case "kernel":
			s, _ := asString(f.raw)
			k, ok := kernels[s]
			switch {
			case ok:
				pl.Resize.Algorithm = k
			case s == "mks2013" || s == "mks2021":
				b.ignore("resize.kernel", reasonApproximated)
			default:
				return dialect.InvalidArgument("resize.kernel", string(f.raw), "nearest, linear, cubic, mitchell, lanczos2, lanczos3, mks2013 or mks2021")
			}
		case "withoutEnlargement":
			v, ok := asBool(f.raw)
			if !ok {
				return invalidJSON("resize.withoutEnlargement", "a boolean")
			}
			pl.Resize.Enlarge = !v
		case "ratio":
			v, err := asFloat("resize.ratio", f.raw, 0, transform.MaxFactor)
			if err != nil {
				return err
			}
			ratio = v
		case "withoutReduction", "fastShrinkOnLoad":
			b.ignore("resize."+f.key, reasonUnsupported)
		default:
			b.ignore(truncate("resize."+f.key), reasonUnknown)
		}
	}
	if ratio > 0 {
		// SIH (image-handler.ts applyResize): with both dimensions the
		// ratio scales them; otherwise it scales the source dimensions.
		if width > 0 && height > 0 {
			width, height = jsRound(width*ratio), jsRound(height*ratio)
		} else {
			width, height = 0, 0
			pl.Resize.ZoomX, pl.Resize.ZoomY = ratio, ratio
		}
		if _, ok := m.lookup("fit"); !ok {
			fit = "inside"
		}
	}
	if width > float64(transform.MaxDimension) || height > float64(transform.MaxDimension) {
		return dialect.InvalidArgument("resize", "", "width and height at most "+strconv.Itoa(transform.MaxDimension))
	}
	pl.Resize.Width, pl.Resize.Height = transform.Dim{Px: int(width)}, transform.Dim{Px: int(height)}
	pl.Resize.Type, _ = resizeType(fit)
	switch fit {
	case "cover":
		pl.Gravity = transform.Gravity{Type: position}
	case "contain":
		if !position.IsCompass() {
			position = transform.GravityUnset
		}
		pl.Extend = transform.Extend{Enabled: true, Gravity: transform.Gravity{Type: position}}
		if background == nil {
			background = &black
		}
		pl.Background = background
	}
	return nil
}

// region decodes sharp's {left, top, width, height} region.
func region(name string, raw json.RawMessage) (left, top, width, height int, err error) {
	m, err := objectMap(raw)
	if err != nil {
		return 0, 0, 0, 0, invalidJSON(name, "an object {left, top, width, height}")
	}
	var v [4]int
	for i, k := range []string{"left", "top", "width", "height"} {
		lo := 0
		if i >= 2 {
			lo = 1
		}
		r, ok := m[k]
		if !ok {
			return 0, 0, 0, 0, dialect.InvalidArgument(name+"."+k, "", "an integer")
		}
		if v[i], err = asInt(name+"."+k, r, lo, transform.MaxDimension); err != nil {
			return 0, 0, 0, 0, err
		}
	}
	return v[0], v[1], v[2], v[3], nil
}

// extract maps sharp extract (and SIH's crop, which calls extract). sharp
// crops before resizing when extract precedes resize in call order and
// after it otherwise; SIH calls edits in key order, so an extract after
// resize becomes a crop in a chained pipeline.
func (b *builder) extract(name string, raw json.RawMessage) error {
	left, top, width, height, err := region(name, raw)
	if err != nil {
		return err
	}
	pl := b.pre
	if b.resizeSeen {
		pl = b.postPipeline()
	}
	pl.Crop = &transform.Crop{
		Width:   transform.Dim{Px: width},
		Height:  transform.Dim{Px: height},
		Gravity: transform.Gravity{Type: transform.GravityNorthWest, XOffset: float64(left), YOffset: float64(top)},
	}
	return nil
}

func (b *builder) postPipeline() *transform.Pipeline {
	if b.post == nil {
		// b.pre points into r.Pipelines; re-derive it after appending.
		i := len(b.r.Pipelines) - 1
		b.r.Pipelines = append(b.r.Pipelines, transform.Pipeline{})
		b.pre = &b.r.Pipelines[i]
		b.post = &b.r.Pipelines[i+1]
	}
	return b.post
}

// extend maps sharp extend: an integer for every side or an object.
func (b *builder) extend(raw json.RawMessage) error {
	pad := &transform.Padding{}
	if kind(raw) != kindObject {
		n, err := asInt("extend", raw, 1, transform.MaxDimension)
		if err != nil {
			return err
		}
		*pad = transform.Padding{Top: n, Right: n, Bottom: n, Left: n}
		b.pre.Padding = pad
		return nil
	}
	fs, err := objectFields(raw)
	if err != nil {
		return invalidJSON("extend", "an integer or an object")
	}
	for _, f := range fs {
		var dst *int
		switch f.key {
		case "top":
			dst = &pad.Top
		case "right":
			dst = &pad.Right
		case "bottom":
			dst = &pad.Bottom
		case "left":
			dst = &pad.Left
		case "background":
			c, err := parseColor("extend.background", f.raw)
			if err != nil {
				return err
			}
			b.pre.Background = &c
			continue
		case "extendWith":
			if s, _ := asString(f.raw); s != "background" {
				b.ignore("extend.extendWith", reasonApproximated)
			}
			continue
		default:
			b.ignore(truncate("extend."+f.key), reasonUnknown)
			continue
		}
		if *dst, err = asInt("extend."+f.key, f.raw, 0, transform.MaxDimension); err != nil {
			return err
		}
	}
	b.pre.Padding = pad
	return nil
}

// trim maps sharp trim: true/null (threshold 10), a number (the legacy
// threshold argument) or {background, threshold}; false is off.
func (b *builder) trim(raw json.RawMessage) error {
	t := &transform.Trim{Threshold: 10}
	switch kind(raw) {
	case kindBool:
		if v, _ := asBool(raw); !v {
			b.pre.Trim = nil
			return nil
		}
	case kindNull:
	case kindObject:
		fs, err := objectFields(raw)
		if err != nil {
			return invalidJSON("trim", "an object")
		}
		for _, f := range fs {
			switch f.key {
			case "threshold":
				if t.Threshold, err = asFloat("trim.threshold", f.raw, 0, 255); err != nil {
					return err
				}
			case "background":
				c, err := parseColor("trim.background", f.raw)
				if err != nil {
					return err
				}
				t.Color = &c
			case "lineArt", "margin":
				b.ignore("trim."+f.key, reasonUnsupported)
			default:
				b.ignore(truncate("trim."+f.key), reasonUnknown)
			}
		}
	default:
		v, err := asFloat("trim", raw, 0, 255)
		if err != nil {
			return err
		}
		t.Threshold = v
	}
	b.pre.Trim = t
	return nil
}

// rotate maps sharp rotate(angle): null auto-orients, a multiple of 90
// rotates, any other angle rotates with a (black) fill.
func (b *builder) rotate(raw json.RawMessage) error {
	pl := b.pre
	if kind(raw) == kindNull {
		pl.AutoRotate = new(true)
		return nil
	}
	a, ok := asNumber(raw)
	if !ok || math.Abs(a) > math.MaxInt32 {
		return dialect.InvalidArgument("rotate", string(raw), "an angle in degrees or null")
	}
	pl.Rotate, pl.RotateAny = 0, 0
	if a == math.Trunc(a) && int(a)%90 == 0 {
		pl.Rotate = ((int(a) % 360) + 360) % 360
		return nil
	}
	a = math.Mod(a, 360)
	switch {
	case a > 180:
		a -= 360
	case a <= -180:
		a += 360
	}
	pl.RotateAny = a
	return nil
}

// flatten maps sharp flatten: removes alpha onto the background (default
// black).
func (b *builder) flatten(raw json.RawMessage) error {
	if !sharpFlag(raw) {
		b.pre.Background = nil
		return nil
	}
	c := black
	if kind(raw) == kindObject {
		m, err := objectMap(raw)
		if err != nil {
			return invalidJSON("flatten", "an object")
		}
		if v, ok := m["background"]; ok {
			if c, err = parseColor("flatten.background", v); err != nil {
				return err
			}
		}
	}
	b.pre.Background = &c
	return nil
}

// Sigma used for sharp's parameterless "mild" blur (a 3×3 box blur) and
// sharpen (a 3×3 convolution): the Gaussian with the same variance.
const (
	mildBlurSigma    = 0.8
	mildSharpenSigma = 0.5
)

// sharpen maps sharp sharpen: {sigma, m1, m2, x1, y2, y3} sets the sigma
// (the flat/jagged controls are approximated); any other value is sharp's
// mild sharpen.
func (b *builder) sharpen(raw json.RawMessage) error {
	if kind(raw) != kindObject {
		b.pre.Sharpen = mildSharpenSigma
		b.ignore("sharpen", reasonApproximated)
		return nil
	}
	fs, err := objectFields(raw)
	if err != nil {
		return invalidJSON("sharpen", "an object")
	}
	sigma, ok := fs.lookup("sigma")
	if !ok {
		return dialect.InvalidArgument("sharpen.sigma", "", "a number between 0.000001 and 10")
	}
	if b.pre.Sharpen, err = asFloat("sharpen.sigma", sigma, 0.000001, 10); err != nil {
		return err
	}
	for _, f := range fs {
		if f.key != "sigma" {
			b.ignore("sharpen", reasonApproximated)
		}
	}
	return nil
}

// blur maps sharp blur: a sigma (0.3–1000), {sigma, precision,
// minAmplitude}, true/null for the mild box blur, false for none.
func (b *builder) blur(raw json.RawMessage) error {
	switch kind(raw) {
	case kindBool, kindNull:
		if sharpFlag(raw) {
			b.pre.Blur = mildBlurSigma
			b.ignore("blur", reasonApproximated)
		} else {
			b.pre.Blur = 0
		}
		return nil
	case kindObject:
		fs, err := objectFields(raw)
		if err != nil {
			return invalidJSON("blur", "an object")
		}
		sigma, ok := fs.lookup("sigma")
		if !ok {
			return dialect.InvalidArgument("blur.sigma", "", "a number between 0.3 and 1000")
		}
		if b.pre.Blur, err = asFloat("blur.sigma", sigma, 0.3, 1000); err != nil {
			return err
		}
		if len(fs) > 1 {
			b.ignore("blur", reasonApproximated)
		}
		return nil
	default:
		v, err := asFloat("blur", raw, 0.3, 1000)
		if err != nil {
			return err
		}
		b.pre.Blur = v
		return nil
	}
}

// modulate maps sharp modulate: brightness (multiplier) → Exposure,
// saturation → Saturation, hue (degrees) → Hue, lightness (additive L*) →
// Brightness scaled to 0–255 (approximated).
func (b *builder) modulate(raw json.RawMessage) error {
	fs, err := objectFields(raw)
	if err != nil {
		return invalidJSON("modulate", "an object")
	}
	a := &b.pre.Adjust
	for _, f := range fs {
		switch f.key {
		case "brightness":
			v, err := asFloat("modulate.brightness", f.raw, 0, transform.MaxFactor)
			if err != nil {
				return err
			}
			a.Exposure = &v
		case "saturation":
			v, err := asFloat("modulate.saturation", f.raw, 0, transform.MaxFactor)
			if err != nil {
				return err
			}
			a.Saturation = &v
		case "hue":
			v, err := asInt("modulate.hue", f.raw, -math.MaxInt32, math.MaxInt32)
			if err != nil {
				return err
			}
			h := float64(v % 360)
			a.Hue = &h
		case "lightness":
			v, err := asFloat("modulate.lightness", f.raw, -100, 100)
			if err != nil {
				return err
			}
			br := v * 2.55
			a.Brightness = &br
			b.ignore("modulate.lightness", reasonApproximated)
		default:
			b.ignore(truncate("modulate."+f.key), reasonUnknown)
		}
	}
	return nil
}

// overlayWith maps SIH's overlay (image-handler.ts applyOverlayWith) to a
// watermark with a per-request source: {bucket, key, wRatio, hRatio, alpha,
// options: {left, top, gravity}}. left/top are pixels or "<n>p" percent;
// negative values count from the right/bottom edge. Watermarks take effect
// in M3.
func (b *builder) overlay(raw json.RawMessage) error {
	m, err := objectMap(raw)
	if err != nil {
		return invalidJSON("overlayWith", "an object {bucket, key, ...}")
	}
	var bucket *string
	if v, ok := m["bucket"]; ok && kind(v) != kindNull {
		s, ok := asString(v)
		if !ok {
			return invalidJSON("overlayWith.bucket", "a string")
		}
		bucket = &s
	}
	src, ok := b.p.overlaySource(bucket)
	if !ok {
		return errBucket()
	}
	keyRaw, ok := m["key"]
	key, isStr := asString(keyRaw)
	if !ok || !isStr || key == "" {
		return invalidJSON("overlayWith.key", "a non-empty string")
	}
	spec, err := sourceSpec("overlayWith.key", src, key)
	if err != nil {
		return err
	}
	wm := transform.Watermark{Opacity: 1, Source: &spec, Position: transform.Gravity{Type: transform.GravityCenter}}
	// wRatio, hRatio and alpha may sit at the top level (as SIH reads them)
	// or inside options.
	opts := map[string]json.RawMessage{}
	if o, ok := m["options"]; ok && kind(o) == kindObject {
		if opts, err = objectMap(o); err != nil {
			return invalidJSON("overlayWith.options", "an object")
		}
	}
	get := func(k string) (json.RawMessage, bool) {
		if v, ok := m[k]; ok {
			return v, true
		}
		v, ok := opts[k]
		return v, ok
	}
	if v, ok := get("alpha"); ok {
		if a, ok := percent(v); ok {
			wm.Opacity = 1 - float64(a)/100
		}
	}
	if v, ok := get("wRatio"); ok {
		if w, ok := percent(v); ok && w > 0 {
			wm.Scale = float64(w) / 100
		}
	}
	if _, ok := get("hRatio"); ok {
		b.ignore("overlayWith.hRatio", reasonApproximated)
	}
	if err := overlayPosition(&wm.Position, opts); err != nil {
		return err
	}
	b.pre.Watermarks = append(b.pre.Watermarks, wm)
	return nil
}

// percent decodes SIH's 0–100 ratio (/^(100|[1-9]?\d)$/ on the string
// form); anything else is ignored like SIH does.
func percent(raw json.RawMessage) (int, bool) {
	var s string
	switch kind(raw) {
	case kindString:
		s, _ = asString(raw)
	case kindNumber:
		s = strings.TrimSpace(string(raw))
	default:
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 100 || strconv.Itoa(n) != s {
		return 0, false
	}
	return n, true
}

// overlayPosition maps options.gravity or options.left/top.
func overlayPosition(g *transform.Gravity, opts map[string]json.RawMessage) error {
	if v, ok := opts["gravity"]; ok {
		t, err := parsePosition("overlayWith.options.gravity", v)
		if err != nil {
			return err
		}
		if t.IsCompass() {
			g.Type = t
		}
	}
	left, hasLeft, err := overlayOffset("overlayWith.options.left", opts)
	if err != nil {
		return err
	}
	top, hasTop, err := overlayOffset("overlayWith.options.top", opts)
	if err != nil {
		return err
	}
	if !hasLeft && !hasTop {
		return nil
	}
	// Negative offsets anchor to the far edge (SIH calcOverlaySizeOption).
	east, south := hasLeft && math.Signbit(left), hasTop && math.Signbit(top)
	switch {
	case east && south:
		g.Type = transform.GravitySouthEast
	case east:
		g.Type = transform.GravityNorthEast
	case south:
		g.Type = transform.GravitySouthWest
	default:
		g.Type = transform.GravityNorthWest
	}
	g.XOffset, g.YOffset = math.Abs(left), math.Abs(top)
	return nil
}

// overlayOffset decodes a left/top option: an integer number of pixels or
// "<n>p" percent of the image (as a relative offset < 1).
func overlayOffset(name string, opts map[string]json.RawMessage) (float64, bool, error) {
	raw, ok := opts[strings.TrimPrefix(name, "overlayWith.options.")]
	if !ok || kind(raw) == kindNull {
		return 0, false, nil
	}
	s := strings.TrimSpace(string(raw))
	if kind(raw) == kindString {
		s, _ = asString(raw)
	}
	if p, ok := strings.CutSuffix(s, "p"); ok {
		n, ok := parseIntJS(p)
		if !ok || n <= -100 || n >= 100 {
			return 0, false, dialect.InvalidArgument(name, s, "pixels or a percentage between -99p and 99p")
		}
		return float64(n) / 100, true, nil
	}
	n, ok := parseIntJS(s)
	if !ok || n < -transform.MaxDimension || n > transform.MaxDimension {
		return 0, false, dialect.InvalidArgument(name, s, "pixels or a percentage")
	}
	return float64(n), true, nil
}

// smartCrop maps SIH's Rekognition face crop (true or {faceIndex,
// padding}) to crop_objects on the faces (WP-72): the box of face
// faceIndex (0-based, best first; SIH orders by confidence too) grown by
// padding pixels on every side and cut out of the source. SIH extracts it
// after resizing, from the resized image; here it is cut from the source
// before the resize, so with a resize the result has the requested size
// rather than the face's size in the resized image (recorded as
// approximated). A faceIndex past the faces found leaves the image uncut
// (SIH answers 400). Without the detector (slim) it answers 422.
func (b *builder) smartCrop(raw json.RawMessage) error {
	if v, ok := asBool(raw); ok && !v {
		return nil
	}
	co := &transform.CropObjects{Scale: 1, Classes: []string{transform.FaceClass}, Index: new(0)}
	if kind(raw) == kindObject {
		m, err := objectMap(raw)
		if err != nil {
			return invalidJSON("smartCrop", "an object")
		}
		if v, ok := m["faceIndex"]; ok {
			idx, err := asInt("smartCrop.faceIndex", v, 0, transform.MaxListLen)
			if err != nil {
				return err
			}
			co.Index = &idx
		}
		if v, ok := m["padding"]; ok {
			pad, err := asInt("smartCrop.padding", v, 0, transform.MaxDimension)
			if err != nil {
				return err
			}
			co.Padding = pad
		}
	}
	b.pre.CropObjects = co
	return nil
}

// roundCrop maps SIH's ellipse mask (true or {top, left, rx, ry}): the
// default is a circle of radius min(w, h)/2 centred on the image, followed
// by a trim, which is Mask circle with trim. Explicit centre or radii are
// approximated by a centred circle or ellipse.
func (b *builder) roundCrop(raw json.RawMessage) error {
	if v, ok := asBool(raw); ok && !v {
		return nil
	}
	mask := &transform.Mask{Shape: transform.MaskCircle, Trim: true}
	if kind(raw) == kindObject {
		m, err := objectMap(raw)
		if err != nil {
			return invalidJSON("roundCrop", "an object")
		}
		var rx, ry float64
		for _, k := range []string{"top", "left", "rx", "ry"} {
			v, ok := m[k]
			if !ok {
				continue
			}
			f, err := asFloat("roundCrop."+k, v, -transform.MaxDimension, transform.MaxDimension)
			if err != nil {
				return err
			}
			switch k {
			case "rx":
				rx = f
			case "ry":
				ry = f
			default:
			}
			b.ignore("roundCrop", reasonApproximated)
		}
		if rx > 0 && ry > 0 && rx != ry {
			mask.Shape = transform.MaskEllipse
		}
	}
	b.pre.Mask = mask
	return nil
}
