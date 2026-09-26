// imaginary endpoint semantics reimplemented from h2non/imaginary
// (image.go, params.go, options.go; MIT) and h2non/bimg (resizer.go; MIT).
// See NOTICE.

package query

import (
	"math"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Expectation phrases for imaginary params (client-safe, static).
const (
	expNonNegNumber = "number >= 0"
	expBool         = "boolean (1, t, true, 0, f, false)"
	expString       = "string"
	expRGB          = "r,g,b with each 0-255"
	expGravity      = "north, south, east, west, centre or smart"
	expExtend       = "black, white, copy, mirror, lastpixel or background"
	expColorspace   = "srgb or bw"
	expAspectRatio  = "w:h with positive integers"
	expCompression  = "integer in range 0-9"
	expRequired     = "required by this endpoint"
	expWidthHeight  = "width or height"
	expSigma        = "sigma or minampl"
)

// iparams holds imaginary's ImageOptions (options.go) for one operation.
type iparams struct {
	width, height, areaWidth, areaHeight, top, left int
	quality, compression, rotate, factor, textWidth int
	margin, dpi                                     int
	opacity, sigma, minAmpl                         float64
	flip, flop, force, embed, noCrop, noReplicate   bool
	noRotation, noProfile, stripMeta, interlace     bool
	palette                                         bool
	text, image, font, typ, aspectRatio             string
	gravity, extend, colorspace, operations         string
	color, background                               *transform.Color
	defined                                         map[string]bool
}

func newIParams() *iparams { return &iparams{defined: map[string]bool{}} }

// set coerces one imaginary param. v is a string (query) or a JSON value
// (float64, bool, string) from /pipeline. known is false for params
// imaginary does not define.
//
//nolint:gocyclo // one flat switch over imaginary's coercion table
func (ip *iparams) set(key string, v any) (known bool, err error) {
	switch key {
	case "width":
		ip.width, err = coerceInt(key, v)
	case "height":
		ip.height, err = coerceInt(key, v)
	case "areawidth":
		ip.areaWidth, err = coerceInt(key, v)
	case "areaheight":
		ip.areaHeight, err = coerceInt(key, v)
	case "top":
		ip.top, err = coerceInt(key, v)
	case "left":
		ip.left, err = coerceInt(key, v)
	case "quality":
		ip.quality, err = coerceInt(key, v)
	case "compression":
		ip.compression, err = coerceInt(key, v)
	case "rotate":
		ip.rotate, err = coerceInt(key, v)
	case "factor":
		ip.factor, err = coerceInt(key, v)
	case "textwidth":
		ip.textWidth, err = coerceInt(key, v)
	case "margin":
		ip.margin, err = coerceInt(key, v)
	case "dpi":
		ip.dpi, err = coerceInt(key, v)
	case "speed":
		_, err = coerceInt(key, v)
	case "opacity":
		ip.opacity, err = coerceFloat(key, v)
	case "sigma":
		ip.sigma, err = coerceFloat(key, v)
	case "minampl":
		ip.minAmpl, err = coerceFloat(key, v)
	case "flip":
		ip.flip, err = coerceBool(key, v)
	case "flop":
		ip.flop, err = coerceBool(key, v)
	case "force":
		ip.force, err = coerceBool(key, v)
	case "embed":
		ip.embed, err = coerceBool(key, v)
	case "nocrop":
		ip.noCrop, err = coerceBool(key, v)
	case "noreplicate":
		ip.noReplicate, err = coerceBool(key, v)
	case "norotation":
		ip.noRotation, err = coerceBool(key, v)
	case "noprofile":
		ip.noProfile, err = coerceBool(key, v)
	case "stripmeta":
		ip.stripMeta, err = coerceBool(key, v)
	case "interlace":
		ip.interlace, err = coerceBool(key, v)
	case "palette":
		ip.palette, err = coerceBool(key, v)
	case "text":
		ip.text, err = coerceString(key, v)
	case "image":
		ip.image, err = coerceString(key, v)
	case "font":
		ip.font, err = coerceString(key, v)
	case "type":
		ip.typ, err = coerceString(key, v)
	case "aspectratio":
		ip.aspectRatio, err = coerceString(key, v)
	case "gravity":
		ip.gravity, err = coerceString(key, v)
	case "extend":
		ip.extend, err = coerceString(key, v)
	case "colorspace":
		ip.colorspace, err = coerceString(key, v)
	case "operations":
		ip.operations, err = coerceString(key, v)
	case "color":
		ip.color, err = coerceRGB(key, v)
	case "background":
		ip.background, err = coerceRGB(key, v)
	default:
		return false, nil
	}
	ip.defined[key] = true
	return true, err
}

// coerceFloat follows imaginary coerceTypeFloat: numbers or numeric
// strings, "" is 0. Negative, NaN and infinite values are rejected
// (imaginary takes the absolute value).
func coerceFloat(field string, v any) (float64, error) {
	var f float64
	switch x := v.(type) {
	case float64:
		f = x
	case string:
		if x == "" {
			return 0, nil
		}
		var err error
		if f, err = strconv.ParseFloat(x, 64); err != nil {
			return 0, dialect.InvalidArgument(field, x, expNonNegNumber)
		}
	default:
		return 0, dialect.InvalidArgument(field, "", expNonNegNumber)
	}
	if math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > math.MaxInt32 {
		return 0, dialect.InvalidArgument(field, strconv.FormatFloat(f, 'g', -1, 64), expNonNegNumber)
	}
	return f, nil
}

// coerceInt follows imaginary coerceTypeInt: strings are rounded half up
// (params.go parseInt), JSON numbers truncated.
func coerceInt(field string, v any) (int, error) {
	f, err := coerceFloat(field, v)
	if err != nil {
		return 0, err
	}
	if _, isString := v.(string); isString {
		return int(math.Floor(f + 0.5)), nil
	}
	return int(f), nil
}

func coerceBool(field string, v any) (bool, error) {
	switch x := v.(type) {
	case bool:
		return x, nil
	case string:
		return imgproxy.ParseBool(field, x)
	}
	return false, dialect.InvalidArgument(field, "", expBool)
}

func coerceString(field string, v any) (string, error) {
	if s, ok := v.(string); ok {
		return s, nil
	}
	return "", dialect.InvalidArgument(field, "", expString)
}

// coerceRGB parses imaginary's "r,g,b" decimal colours; "" is unset.
func coerceRGB(field string, v any) (*transform.Color, error) {
	s, err := coerceString(field, v)
	if err != nil || s == "" {
		return nil, err
	}
	parts := strings.Split(s, ",")
	if len(parts) != 3 {
		return nil, dialect.InvalidArgument(field, s, expRGB)
	}
	var c [3]uint8
	for i, p := range parts {
		n, err := strconv.ParseUint(strings.TrimSpace(p), 10, 8)
		if err != nil {
			return nil, dialect.InvalidArgument(field, s, expRGB)
		}
		c[i] = uint8(n)
	}
	return &transform.Color{R: c[0], G: c[1], B: c[2], A: 255}, nil
}

// endpointFunc applies one imaginary operation to the builder's current
// pipeline.
type endpointFunc func(b *imgproxy.Builder, ip *iparams) error

// operation returns the imaginary operation for an HTTP endpoint
// (server.go) or, with pipeline set, a /pipeline operation name
// (image.go OperationsMap).
func operation(name string, pipeline bool) (endpointFunc, bool) {
	switch name {
	case "resize":
		return opResize, true
	case "fit":
		return opFit, true
	case "enlarge":
		return opEnlarge, true
	case "extract":
		return opExtract, true
	case "crop":
		return opCrop, true
	case "smartcrop":
		return opSmartCrop, true
	case "rotate":
		return opRotate, true
	case "autorotate":
		return opAutoRotate, true
	case "flip":
		return opFlip, true
	case "flop":
		return opFlop, true
	case "thumbnail":
		return opThumbnail, true
	case "zoom":
		return opZoom, true
	case "convert":
		return opConvert, true
	case "watermark":
		return opWatermark, true
	case "blur":
		return opBlur, true
	}
	if pipeline {
		if name == "watermarkImage" {
			return opWatermarkImage, true
		}
		return nil, false
	}
	switch name {
	case "watermarkimage":
		return opWatermarkImage, true
	case "info":
		return opInfo, true
	case "pipeline":
		return opPipeline, true
	}
	return nil, false
}

// imaginary parses endpoint mode.
func (p *Parser) imaginary(b *imgproxy.Builder, endpoint string, params []param) error {
	op, ok := operation(endpoint, false)
	if !ok {
		return dialect.InvalidURL("unknown endpoint")
	}
	ip := newIParams()
	for _, kv := range params {
		if kv.key == paramURL || isSignatureParam(kv.key) || kv.key == "field" {
			continue
		}
		known, err := ip.set(kv.key, kv.value)
		if err != nil {
			return err
		}
		if !known {
			if err := p.unknown(b, kv.key); err != nil {
				return err
			}
		}
	}
	if ip.defined["speed"] {
		b.Ignore("speed", reasonNotImplemented)
	}
	return op(b, ip)
}

// missing returns the 400 for an absent required param.
func missing(field, expected string) error {
	return dialect.InvalidArgument(field, "", expected)
}

// common applies the params every imaginary operation honours
// (options.go BimgOptions) except the resize box.
func common(b *imgproxy.Builder, ip *iparams) error {
	r, pl := b.Request(), b.Pipeline()
	if ip.quality > 0 {
		if err := b.Apply("quality", strconv.Itoa(ip.quality)); err != nil {
			return err
		}
	}
	if ip.defined["compression"] {
		if ip.compression > 9 {
			return dialect.InvalidArgument("compression", strconv.Itoa(ip.compression), expCompression)
		}
		png := pngOptions(r)
		c := ip.compression
		png.Compression = &c
	}
	if ip.defined["palette"] {
		q := ip.palette
		pngOptions(r).Quantize = &q
	}
	if ip.defined["interlace"] {
		v := ip.interlace
		r.Output.Progressive = &v
	}
	if ip.defined["stripmeta"] {
		v := ip.stripMeta
		r.Output.StripMetadata = &v
	}
	if ip.noProfile {
		v := true
		r.Output.StripColorProfile = &v
	}
	if ip.noRotation {
		v := false
		pl.AutoRotate = &v
	}
	switch ip.typ {
	case "", "auto":
		// auto: negotiated from Accept like imaginary; "" keeps the source format.
	default:
		f, err := imgproxy.ParseFormat("type", ip.typ)
		if err != nil {
			return err
		}
		r.Output.Format = f
	}
	if ip.rotate != 0 {
		// bimg getAngle: round down to a multiple of 90.
		a := ip.rotate % 360
		pl.Rotate = a - a%90
	}
	// bimg rotateAndFlipImage: Flip mirrors horizontally, Flop vertically.
	pl.Flip.H = pl.Flip.H || ip.flip
	pl.Flip.V = pl.Flip.V || ip.flop
	if ip.background != nil {
		pl.Background = ip.background
	}
	switch ip.colorspace {
	case "", "srgb":
	case "bw":
		pl.Grayscale = true
	default:
		return dialect.InvalidArgument("colorspace", ip.colorspace, expColorspace)
	}
	if ip.sigma > 0 {
		pl.Blur = ip.sigma
	}
	if ip.minAmpl > 0 {
		b.Ignore("minampl", reasonNotImplemented)
	}
	switch ip.gravity {
	case "":
	case "north":
		pl.Gravity = transform.Gravity{Type: transform.GravityNorth}
	case "south":
		pl.Gravity = transform.Gravity{Type: transform.GravitySouth}
	case "east":
		pl.Gravity = transform.Gravity{Type: transform.GravityEast}
	case "west":
		pl.Gravity = transform.Gravity{Type: transform.GravityWest}
	case "centre", "center":
		pl.Gravity = transform.Gravity{Type: transform.GravityCenter}
	case "smart":
		pl.Gravity = transform.Gravity{Type: transform.GravitySmart}
	default:
		return dialect.InvalidArgument("gravity", ip.gravity, expGravity)
	}
	if ip.aspectRatio != "" && (ip.width == 0) != (ip.height == 0) {
		w, h, ok := strings.Cut(ip.aspectRatio, ":")
		wi, werr := strconv.Atoi(w)
		hi, herr := strconv.Atoi(h)
		if !ok || werr != nil || herr != nil || wi <= 0 || hi <= 0 {
			return dialect.InvalidArgument("aspectratio", ip.aspectRatio, expAspectRatio)
		}
		pl.Resize.AspectRatio = float64(wi) / float64(hi)
	}
	return nil
}

func pngOptions(r *transform.Request) *transform.PNGOptions {
	if r.Output.PNG == nil {
		r.Output.PNG = &transform.PNGOptions{}
	}
	return r.Output.PNG
}

// box sets the resize box the way bimg.Resize treats Options: force wins
// (and may enlarge), then crop (fill at the gravity), then embed (fit and
// extend the canvas to the box with the extend mode), then enlarge (fit,
// upscaling allowed); otherwise bimg's normalizeOperation forces the size
// unless a rotation is requested (fit). Nothing happens without width and
// height.
func box(b *imgproxy.Builder, ip *iparams, crop, embed, enlarge bool) error {
	if ip.width == 0 && ip.height == 0 {
		return nil
	}
	pl := b.Pipeline()
	pl.Resize.Width = transform.Dim{Px: ip.width}
	pl.Resize.Height = transform.Dim{Px: ip.height}
	switch {
	case ip.force:
		pl.Resize.Type = transform.ResizeForce
		enlarge = true
	case crop:
		pl.Resize.Type = transform.ResizeFill
	case embed:
		pl.Resize.Type = transform.ResizeFit
		if ip.width > 0 && ip.height > 0 {
			if err := extend(b, ip); err != nil {
				return err
			}
		}
	case enlarge, ip.rotate != 0:
		pl.Resize.Type = transform.ResizeFit
	default:
		pl.Resize.Type = transform.ResizeForce
		enlarge = true
	}
	if enlarge {
		pl.Resize.Enlarge = true
	}
	return nil
}

// extend enables canvas extension with imaginary's extend mode (default
// copy, params.go buildParamsFromQuery).
func extend(b *imgproxy.Builder, ip *iparams) error {
	pl := b.Pipeline()
	pl.Extend = transform.Extend{Enabled: true, Gravity: transform.Gravity{Type: transform.GravityCenter}}
	switch strings.ToLower(strings.TrimSpace(ip.extend)) {
	case "", "copy", "lastpixel":
		pl.Extend.Mode = transform.ExtendCopy
	case "mirror":
		pl.Extend.Mode = transform.ExtendCopy
		b.Ignore("extend:mirror", reasonApproximated)
	case "black":
		pl.Background = &transform.Color{A: 255}
	case "white":
		pl.Background = &transform.Color{R: 255, G: 255, B: 255, A: 255}
	case "background":
		if pl.Background == nil {
			pl.Background = &transform.Color{A: 255}
		}
	default:
		return dialect.InvalidArgument("extend", ip.extend, expExtend)
	}
	return nil
}

func needWidthOrHeight(ip *iparams) error {
	if ip.width == 0 && ip.height == 0 {
		return missing("width", expWidthHeight)
	}
	return nil
}

func needWidthAndHeight(ip *iparams) error {
	if ip.width == 0 {
		return missing("width", expRequired)
	}
	if ip.height == 0 {
		return missing("height", expRequired)
	}
	return nil
}

// opResize: fit and embed into the box; nocrop=false crops instead
// (image.go Resize).
func opResize(b *imgproxy.Builder, ip *iparams) error {
	if err := needWidthOrHeight(ip); err != nil {
		return err
	}
	if err := common(b, ip); err != nil {
		return err
	}
	crop := ip.defined["nocrop"] && !ip.noCrop
	return box(b, ip, crop, true, false)
}

// opFit: fit inside the box (image.go Fit computes the fitted size, so
// the embed is a no-op).
func opFit(b *imgproxy.Builder, ip *iparams) error {
	if err := needWidthAndHeight(ip); err != nil {
		return err
	}
	if err := common(b, ip); err != nil {
		return err
	}
	pl := b.Pipeline()
	pl.Resize.Type = transform.ResizeFit
	pl.Resize.Width = transform.Dim{Px: ip.width}
	pl.Resize.Height = transform.Dim{Px: ip.height}
	return nil
}

// opEnlarge: fill the box, upscaling; nocrop fits instead.
func opEnlarge(b *imgproxy.Builder, ip *iparams) error {
	if err := needWidthAndHeight(ip); err != nil {
		return err
	}
	if err := common(b, ip); err != nil {
		return err
	}
	return box(b, ip, !ip.noCrop, ip.embed, true)
}

// opExtract: crop the area at left/top. bimg extracts after resizing;
// here the area is in source pixels and any width/height resize follows.
func opExtract(b *imgproxy.Builder, ip *iparams) error {
	if ip.areaWidth == 0 {
		return missing("areawidth", expRequired)
	}
	if ip.areaHeight == 0 {
		return missing("areaheight", expRequired)
	}
	if err := common(b, ip); err != nil {
		return err
	}
	b.Pipeline().Crop = area(ip.areaWidth, ip.areaHeight, ip.left, ip.top)
	return box(b, ip, false, ip.embed, false)
}

// area is a pre-resize crop of w×h pixels whose top-left corner is at
// (left, top).
func area(w, h, left, top int) *transform.Crop {
	return &transform.Crop{
		Width:  transform.Dim{Px: w},
		Height: transform.Dim{Px: h},
		Gravity: transform.Gravity{
			Type:    transform.GravityNorthWest,
			XOffset: float64(left),
			YOffset: float64(top),
		},
	}
}

// opCrop: with both dimensions, fill the box at the gravity; with one,
// bimg crops that dimension without scaling (imageCalculations keeps the
// other at the source size).
func opCrop(b *imgproxy.Builder, ip *iparams) error {
	if err := needWidthOrHeight(ip); err != nil {
		return err
	}
	if err := common(b, ip); err != nil {
		return err
	}
	if ip.force || (ip.width > 0 && ip.height > 0) {
		return box(b, ip, true, ip.embed, false)
	}
	b.Pipeline().Crop = &transform.Crop{
		Width:  transform.Dim{Px: ip.width},
		Height: transform.Dim{Px: ip.height},
	}
	return nil
}

// opSmartCrop is opCrop with smart gravity.
func opSmartCrop(b *imgproxy.Builder, ip *iparams) error {
	ip.gravity = "smart"
	return opCrop(b, ip)
}

func opRotate(b *imgproxy.Builder, ip *iparams) error {
	if ip.rotate == 0 {
		return missing("rotate", expRequired)
	}
	if err := common(b, ip); err != nil {
		return err
	}
	return box(b, ip, false, ip.embed, false)
}

// opAutoRotate only applies the EXIF orientation (image.go AutoRotate
// ignores every other param).
func opAutoRotate(b *imgproxy.Builder, _ *iparams) error {
	on := true
	b.Pipeline().AutoRotate = &on
	return nil
}

func opFlip(b *imgproxy.Builder, ip *iparams) error {
	ip.flip = true
	return opGeneric(b, ip)
}

func opFlop(b *imgproxy.Builder, ip *iparams) error {
	ip.flop = true
	return opGeneric(b, ip)
}

// opGeneric applies the common params and the box.
func opGeneric(b *imgproxy.Builder, ip *iparams) error {
	if err := common(b, ip); err != nil {
		return err
	}
	return box(b, ip, false, ip.embed, false)
}

func opThumbnail(b *imgproxy.Builder, ip *iparams) error {
	if err := needWidthOrHeight(ip); err != nil {
		return err
	}
	return opGeneric(b, ip)
}

// opZoom scales by factor+1 (bimg zoomImage calls vips_zoom with
// zoom+1). An area given with top/left is cropped first, in source pixels
// divided by the zoom (bimg crops the zoomed image).
func opZoom(b *imgproxy.Builder, ip *iparams) error {
	if ip.factor == 0 {
		return missing("factor", expRequired)
	}
	if err := common(b, ip); err != nil {
		return err
	}
	pl := b.Pipeline()
	z := float64(ip.factor + 1)
	pl.Resize.ZoomX, pl.Resize.ZoomY = z, z
	pl.Resize.Enlarge = true
	if ip.top > 0 || ip.left > 0 {
		if ip.areaWidth == 0 && ip.areaHeight == 0 {
			return missing("areawidth", expRequired)
		}
		aw, ah := ip.areaWidth, ip.areaHeight
		if aw == 0 {
			aw = ip.width
		}
		if ah == 0 {
			ah = ip.height
		}
		f := ip.factor + 1
		pl.Crop = area(max(aw/f, 1), max(ah/f, 1), ip.left/f, ip.top/f)
		b.Ignore("zoom:area", reasonApproximated)
	}
	return box(b, ip, false, ip.embed, false)
}

func opConvert(b *imgproxy.Builder, ip *iparams) error {
	if ip.typ == "" {
		return missing("type", expRequired)
	}
	return opGeneric(b, ip)
}

// opWatermark adds a replicated text watermark (bimg
// watermarkImageWithText): opacity 0 means 0.25, the font defaults to
// "sans 10" rendered at dpi (default 150), which is its point size scaled
// by dpi/72 at xi's 72 DPI; text wraps at textwidth and margin spaces the
// tiles (default textwidth). bimg's default text width, a sixth of the
// image width, has no pixel equivalent: without textwidth the text wraps
// at the image width.
func opWatermark(b *imgproxy.Builder, ip *iparams) error {
	if ip.text == "" {
		return missing("text", expRequired)
	}
	if err := opGeneric(b, ip); err != nil {
		return err
	}
	dpi := ip.dpi
	if dpi <= 0 {
		dpi = 150
	}
	font, err := scaleFont(ip.font, float64(dpi)/72)
	if err != nil {
		return err
	}
	w := transform.Watermark{
		Opacity:   watermarkOpacity(ip.opacity, 0.25),
		Text:      ip.text,
		Font:      font,
		Color:     ip.color,
		TextWidth: max(ip.textWidth, 0),
		Tile:      transform.TileRepeat,
	}
	margin := ip.margin
	if margin <= 0 {
		margin = ip.textWidth
	}
	if margin > 0 {
		w.Position.XOffset, w.Position.YOffset = float64(margin), float64(margin)
	}
	if ip.noReplicate {
		w.Tile = transform.TileNone
		w.Position = transform.Gravity{Type: transform.GravityNorthWest}
	}
	if ip.textWidth <= 0 {
		b.Ignore("watermark:textwidth", reasonApproximated)
	}
	pl := b.Pipeline()
	pl.Watermarks = append(pl.Watermarks, w)
	return nil
}

// scaleFont multiplies the size of a Pango font description (default
// bimg's "sans 10") by f.
func scaleFont(desc string, f float64) (string, error) {
	if desc == "" {
		desc = "sans 10"
	}
	words := strings.Fields(desc)
	size := 10.0
	if n := len(words); n > 0 {
		if v, err := strconv.ParseFloat(strings.TrimSuffix(words[n-1], "px"), 64); err == nil {
			if !(v > 0) || v > 1000 {
				return "", dialect.InvalidArgument("font", desc, "a Pango font description with a size up to 1000")
			}
			size = v
			words = words[:n-1]
		}
	}
	if len(words) == 0 {
		words = []string{"sans"}
	}
	return strings.Join(words, " ") + " " + strconv.FormatFloat(math.Round(size*f*10)/10, 'f', -1, 64), nil
}

// opWatermarkImage overlays the image at left/top (bimg
// watermarkImageWithAnotherImage: opacity 0 means 1).
func opWatermarkImage(b *imgproxy.Builder, ip *iparams) error {
	if ip.image == "" {
		return missing("image", expRequired)
	}
	if !imgproxy.PlausibleSource(ip.image) {
		return dialect.InvalidArgument("image", ip.image, "watermark image url")
	}
	if err := opGeneric(b, ip); err != nil {
		return err
	}
	pl := b.Pipeline()
	pl.Watermarks = append(pl.Watermarks, transform.Watermark{
		Opacity: watermarkOpacity(ip.opacity, 1),
		Source:  &transform.SourceSpec{Raw: ip.image, Encoding: transform.SourcePlain},
		Position: transform.Gravity{
			Type:    transform.GravityNorthWest,
			XOffset: float64(ip.left),
			YOffset: float64(ip.top),
		},
	})
	return nil
}

func watermarkOpacity(o, zero float64) float64 {
	if o == 0 {
		return zero
	}
	return min(o, 1)
}

func opBlur(b *imgproxy.Builder, ip *iparams) error {
	if ip.sigma == 0 && ip.minAmpl == 0 {
		return missing("sigma", expSigma)
	}
	return opGeneric(b, ip)
}

// opInfo returns metadata. imaginary's JSON shape (width, height, type,
// space, hasAlpha, hasProfile, channels, orientation) is served with the
// imgproxy info schema until an imaginary schema exists.
func opInfo(b *imgproxy.Builder, _ *iparams) error {
	r := b.Request()
	r.Kind = transform.KindInfo
	r.Info = &transform.InfoOptions{
		Format:     true,
		Dimensions: true,
		Colorspace: true,
		Bands:      true,
		Alpha:      true,
		EXIF:       true,
	}
	b.Ignore("info:schema", reasonNotImplemented)
	return nil
}
