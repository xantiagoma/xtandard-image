// Filter semantics from thumbor/filters/*.py and thumbor/ext/filters/*.c
// (MIT). Copyright xtandard-image contributors, MIT.

package thumbor

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/dialect/thumbor/filters"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Argument descriptions for 400 messages.
const (
	expNone         = "no arguments"
	expInt          = "an integer"
	expUint         = "a non-negative integer"
	expPosInt       = "a positive integer"
	expDecimal      = "a decimal number"
	expBool         = "true, false, 1 or 0"
	expColor        = "a colour name or hex rrggbb"
	expFormat       = "png, jpeg, jpg, gif, webp, avif, heic or heif"
	expFocal        = "AxB:CxD"
	expQuality      = "an integer from 0 to 100"
	expPercent      = "an integer from 0 to 100"
	expMarkPosition = "an integer, Np, center or repeat"
	expMarkRatio    = "an integer or none"
	expRadius       = "N or N|M"
	expPct100       = "an integer from -100 to 100"
	maxMaxAge       = 10 * 365 * 24 * 60 * 60 // seconds
	maxFilterPx     = transform.MaxDimension
)

// filter applies one filter and returns the (possibly replaced) image.
//
//nolint:gocyclo // one flat switch over thumbor's filter table is the clearest form
func (b *builder) filter(f filters.Filter, image string) (string, error) {
	raw, err := unescape("filters", f.Args)
	if err != nil {
		return image, err
	}
	args := filters.Args(raw)
	pl := b.pipeline()
	switch f.Name {
	// Output.
	case "quality":
		n, err := oneUint(f.Name, args, 100, expQuality)
		if err != nil {
			return image, err
		}
		b.r.Output.Quality = n
	case "format":
		if err := wantArgs(f.Name, args, 1, 1, expFormat); err != nil {
			return image, err
		}
		fm, ok := format(args[0])
		if !ok {
			return image, dialect.InvalidArgument(f.Name, args[0], expFormat)
		}
		b.r.Output.Format = fm
	case "max_bytes":
		n, err := oneUint(f.Name, args, math.MaxInt32, expUint)
		if err != nil {
			return image, err
		}
		b.r.Output.MaxBytes = n
	case "autojpg":
		on := true
		if len(args) > 0 {
			if on, err = oneBool(f.Name, args); err != nil {
				return image, err
			}
		}
		b.r.Output.PreferJPEGIfOpaque = on
	case "strip_exif":
		if err := wantArgs(f.Name, args, 0, 0, expNone); err != nil {
			return image, err
		}
		b.r.Output.StripMetadata = new(true)
	case "strip_icc":
		if err := wantArgs(f.Name, args, 0, 0, expNone); err != nil {
			return image, err
		}
		b.r.Output.StripColorProfile = new(true)
	case "max_age":
		n, err := oneUint(f.Name, args, maxMaxAge, expUint)
		if err != nil {
			return image, err
		}
		b.r.Delivery.TTL = time.Duration(n) * time.Second

	// Geometry.
	case "no_upscale":
		if err := wantArgs(f.Name, args, 0, 0, expNone); err != nil {
			return image, err
		}
		pl.Resize.Enlarge = false
	case "upscale":
		if err := wantArgs(f.Name, args, 0, 0, expNone); err != nil {
			return image, err
		}
		pl.Resize.Enlarge = true
	case "stretch":
		if err := wantArgs(f.Name, args, 0, 0, expNone); err != nil {
			return image, err
		}
		// thumbor skips its fill crop; fit-in ignores stretch.
		if b.fitIn {
			b.ignore(f.Name, reasonNoEffect)
		} else {
			pl.Resize.Type = transform.ResizeForce
		}
	case "proportion":
		if err := wantArgs(f.Name, args, 1, 1, expDecimal); err != nil {
			return image, err
		}
		v, err := decimal(f.Name, args[0])
		if err != nil {
			return image, err
		}
		// thumbor ignores values outside (0, 1].
		if v <= 0 || v > 1 {
			b.ignore(f.Name, reasonNoEffect)
			break
		}
		pl.Resize.ZoomX, pl.Resize.ZoomY = v, v
	case "rotate":
		if err := wantArgs(f.Name, args, 1, 1, expInt); err != nil {
			return image, err
		}
		n, err := integer(f.Name, args[0])
		if err != nil {
			return image, err
		}
		if n%90 != 0 {
			b.ignore(f.Name, reasonNoEffect)
			break
		}
		// thumbor rotates counter-clockwise; Rotate is clockwise.
		pl.Rotate = (360 - n%360) % 360
	case "focal":
		if err := b.focal(f.Name, args); err != nil {
			return image, err
		}
	case "extract_focal":
		if err := wantArgs(f.Name, args, 0, 0, expNone); err != nil {
			return image, err
		}
		return b.extractFocal(image), nil

	// Colour.
	case "brightness":
		n, err := onePct(f.Name, args)
		if err != nil {
			return image, err
		}
		v := float64(n) * 255 / 100
		pl.Adjust.Brightness = &v
	case "contrast":
		n, err := onePct(f.Name, args)
		if err != nil {
			return image, err
		}
		// _contrast.c: factor = ((100 + n)² / 100) / 100.
		v := float64((100+n)*(100+n)) / 10000
		pl.Adjust.Contrast = &v
	case "saturation":
		if err := wantArgs(f.Name, args, 1, 1, expDecimal); err != nil {
			return image, err
		}
		v, err := decimal(f.Name, args[0])
		if err != nil {
			return image, err
		}
		// The documented percentage change (docs/saturation.rst).
		s := max(0, 1+v/100)
		pl.Adjust.Saturation = &s
	case "rgb":
		if err := wantArgs(f.Name, args, 3, 3, expPct100); err != nil {
			return image, err
		}
		var ch [3]*float64
		for i, a := range args {
			n, err := pct(f.Name, a)
			if err != nil {
				return image, err
			}
			// thumbor adds n% of 255 per channel; the model scales channels.
			v := max(0, 1+float64(n)/100)
			ch[i] = &v
		}
		pl.Adjust.Red, pl.Adjust.Green, pl.Adjust.Blue = ch[0], ch[1], ch[2]
		b.ignore(f.Name, reasonApproximated)
	case "grayscale":
		if err := wantArgs(f.Name, args, 0, 0, expNone); err != nil {
			return image, err
		}
		pl.Grayscale = true
	case "equalize":
		if err := wantArgs(f.Name, args, 0, 0, expNone); err != nil {
			return image, err
		}
		pl.Equalize = true
	case "background_color":
		if err := wantArgs(f.Name, args, 1, 1, expColor); err != nil {
			return image, err
		}
		c, err := color(f.Name, args[0])
		if err != nil {
			return image, err
		}
		pl.Background = &c
	case "fill":
		if err := b.fill(f.Name, args); err != nil {
			return image, err
		}
	case "round_corner":
		if err := b.roundCorner(f.Name, args); err != nil {
			return image, err
		}

	// Effects.
	case "blur":
		if err := wantArgs(f.Name, args, 1, 2, "radius[,sigma]"); err != nil {
			return image, err
		}
		radius, err := uintArg(f.Name, args[0], maxFilterPx)
		if err != nil || radius == 0 {
			return image, dialect.InvalidArgument(f.Name, args[0], expPosInt)
		}
		sigma := 0.0
		if len(args) == 2 {
			if sigma, err = decimal(f.Name, args[1]); err != nil {
				return image, err
			}
		}
		if sigma <= 0 {
			sigma = float64(radius)
		}
		pl.Blur = sigma
	case "sharpen":
		if err := wantArgs(f.Name, args, 3, 3, "amount,radius,luminance_only"); err != nil {
			return image, err
		}
		amount, err := decimal(f.Name, args[0])
		if err != nil {
			return image, err
		}
		radius, err := decimal(f.Name, args[1])
		if err != nil {
			return image, err
		}
		if _, err := parseBool(f.Name, args[2]); err != nil {
			return image, err
		}
		if amount <= 0 || radius <= 0 {
			b.ignore(f.Name, reasonNoEffect)
			break
		}
		pl.Unsharp = &transform.Unsharp{Sigma: radius, Amount: amount}
	case "watermark":
		if err := b.watermark(f.Name, args); err != nil {
			return image, err
		}

	// Recognised, not applied.
	case "gifv", "cover", "frame":
		b.ignore(f.Name, reasonM3)
	case "colorize", "convolution", "curve", "noise", "red_eye":
		b.ignore(f.Name, reasonUnsupported)
	default:
		if b.strict {
			return image, dialect.UnknownOption(f.Name)
		}
		b.ignore(f.Name, reasonUnknown)
	}
	return image, nil
}

// focal applies focal(AxB:CxD): the centre of the rectangle becomes the
// focus point. thumbor's coordinates are source pixels (Gravity.FocusPx,
// WP-86); a rectangle given in fractions of the source (every value ≤ 1, as
// imagor and Storyblok clients sometimes write) is a relative focus point.
func (b *builder) focal(name string, args []string) error {
	if err := wantArgs(name, args, 1, 1, expFocal); err != nil {
		return err
	}
	a, c, ok := strings.Cut(args[0], ":")
	x1s, y1s, ok1 := strings.Cut(a, "x")
	x2s, y2s, ok2 := strings.Cut(c, "x")
	if !ok || !ok1 || !ok2 {
		return dialect.InvalidArgument(name, args[0], expFocal)
	}
	var v [4]float64
	for i, s := range [4]string{x1s, y1s, x2s, y2s} {
		f, err := decimal(name, s)
		if err != nil || f < 0 || f > maxFilterPx {
			return dialect.InvalidArgument(name, args[0], expFocal)
		}
		v[i] = f
	}
	if v[2] <= v[0] || v[3] <= v[1] {
		b.ignore(name, reasonNoEffect)
		return nil
	}
	g := &b.pipeline().Gravity
	if v[2] > 1 || v[3] > 1 {
		pg, ok := transform.FocusFromPixels(int(v[0]), int(v[1]), int(v[2]), int(v[3]))
		if !ok {
			return dialect.InvalidArgument(name, args[0], expFocal)
		}
		g.Type, g.FocusX, g.FocusY, g.FocusPx = pg.Type, 0, 0, pg.FocusPx
		return nil
	}
	g.Type, g.FocusX, g.FocusY, g.FocusPx = transform.GravityFocusPoint, (v[0]+v[2])/2, (v[1]+v[3])/2, nil
	return nil
}

// maxFocalDepth is thumbor's MAX_LEVEL for extract_focal.
const maxFocalDepth = 10

// extractFocal implements extract_focal(): when the image is itself a
// thumbor URL, the original image replaces it and the manual crop of the
// innermost URL that has one becomes the focal rectangle, in source pixels
// (Gravity.FocusPx) like a pixel focal().
func (b *builder) extractFocal(image string) string {
	cur := image
	for range maxFocalDepth {
		// thumbor strips the scheme and host: ^(https?://)?.*?/
		s := strings.TrimPrefix(strings.TrimPrefix(cur, "http://"), "https://")
		_, path, ok := strings.Cut(s, "/")
		if !ok {
			break
		}
		first, rest, ok := strings.Cut(path, "/")
		if !ok || (first != "unsafe" && len(first) < minSignatureLen) {
			break
		}
		u, err := parseParts(rest)
		if err != nil {
			break
		}
		if u.crop {
			l, t, r, bt := u.cropBox[0], u.cropBox[1], u.cropBox[2], u.cropBox[3]
			if pg, ok := transform.FocusFromPixels(l, t, r, bt); ok && r > l && bt > t {
				g := &b.pipeline().Gravity
				g.Type, g.FocusX, g.FocusY, g.FocusPx = pg.Type, 0, 0, pg.FocusPx
			} else {
				b.ignore("extract_focal", reasonNoEffect)
			}
			return u.image
		}
		cur = u.image
	}
	b.ignore("extract_focal", reasonNoEffect)
	return image
}

// fill applies fill(color[,fill_transparent]): the canvas is extended to
// the requested box with the colour (thumbor pastes the image centred).
func (b *builder) fill(name string, args []string) error {
	if err := wantArgs(name, args, 1, 2, "color[,fill_transparent]"); err != nil {
		return err
	}
	if len(args) == 2 {
		if _, err := parseBool(name, args[1]); err != nil {
			return err
		}
	}
	pl := b.pipeline()
	pl.Extend = transform.Extend{Enabled: true, Mode: transform.ExtendBackground}
	switch strings.ToLower(args[0]) {
	case "blur":
		pl.Extend.Mode = transform.ExtendBlur
		b.ignore(name, reasonM3)
		return nil
	case "auto":
		// The median colour of the image.
		b.ignore(name, reasonM3)
		return nil
	}
	c, err := color(name, args[0])
	if err != nil {
		return err
	}
	pl.Background = &c
	return nil
}

// roundCorner applies round_corner(a[|b],r,g,b[,transparent]). The model
// has one radius per corner, so an elliptical a|b uses a.
func (b *builder) roundCorner(name string, args []string) error {
	if err := wantArgs(name, args, 4, 5, "radius,r,g,b[,transparent]"); err != nil {
		return err
	}
	ra, rb, elliptic := strings.Cut(args[0], "|")
	a, err := uintArg(name, ra, maxFilterPx)
	if err != nil {
		return dialect.InvalidArgument(name, args[0], expRadius)
	}
	if elliptic {
		v, err := uintArg(name, rb, maxFilterPx)
		if err != nil {
			return dialect.InvalidArgument(name, args[0], expRadius)
		}
		if v != a {
			b.ignore(name, reasonApproximated)
		}
	}
	var rgb [3]uint8
	for i := range 3 {
		v, err := uintArg(name, args[1+i], 255)
		if err != nil {
			return err
		}
		rgb[i] = uint8(v) //nolint:gosec // uintArg bounds v to 0..255
	}
	transparent := false
	if len(args) == 5 {
		if transparent, err = parseBool(name, args[4]); err != nil {
			return err
		}
	}
	pl := b.pipeline()
	pl.Radius = &transform.Radius{TopLeft: a, TopRight: a, BottomRight: a, BottomLeft: a}
	if !transparent {
		pl.Background = &transform.Color{R: rgb[0], G: rgb[1], B: rgb[2], A: 255}
	}
	return nil
}

// watermark applies watermark(url,x,y,alpha[,w_ratio[,h_ratio]]).
//
// x and y are pixels from the left/top edge, negative pixels from the
// right/bottom edge, Np percent of the image size, center, or repeat
// (tile along that axis; the model tiles both axes). alpha is the
// transparency in percent (0 = opaque). w_ratio is the watermark width in
// percent of the image width; h_ratio alone is approximated by nothing.
func (b *builder) watermark(name string, args []string) error {
	if err := wantArgs(name, args, 4, 6, "url,x,y,alpha[,w_ratio[,h_ratio]]"); err != nil {
		return err
	}
	src := args[0]
	if src == "" || !imgproxy.PlausibleSource(src) {
		return dialect.InvalidArgument(name, src, "an image url")
	}
	xs, err := wmPos(name, args[1])
	if err != nil {
		return err
	}
	ys, err := wmPos(name, args[2])
	if err != nil {
		return err
	}
	alpha, err := uintArg(name, args[3], 100)
	if err != nil {
		return dialect.InvalidArgument(name, args[3], expPercent)
	}
	w := transform.Watermark{
		Opacity: float64(100-alpha) / 100,
		Source:  &transform.SourceSpec{Raw: src, Encoding: transform.SourcePlain},
	}
	if xs.repeat || ys.repeat {
		w.Tile = transform.TileRepeat
		if xs.repeat != ys.repeat {
			b.ignore(name, reasonApproximated)
		}
	}
	w.Position = transform.Gravity{Type: compass(ys.side, xs.side), XOffset: xs.offset, YOffset: ys.offset}
	if len(args) > 4 {
		wr, err := wmRatio(name, args[4])
		if err != nil {
			return err
		}
		hr := 0
		if len(args) > 5 {
			if hr, err = wmRatio(name, args[5]); err != nil {
				return err
			}
		}
		switch {
		case wr > 0:
			w.Scale = float64(wr) / 100
			if hr > 0 {
				b.ignore(name, reasonApproximated)
			}
		case hr > 0:
			b.ignore(name, reasonApproximated)
		}
	}
	pl := b.pipeline()
	pl.Watermarks = append(pl.Watermarks, w)
	return nil
}

// wmAxis is one watermark coordinate.
type wmAxis struct {
	side   int     // 0 left/top, 1 centre, 2 right/bottom
	offset float64 // px (≥ 1) or fraction (< 1) from that side
	repeat bool
}

func wmPos(name, s string) (wmAxis, error) {
	switch s {
	case "center":
		return wmAxis{side: 1}, nil
	case "repeat":
		return wmAxis{side: 1, repeat: true}, nil
	}
	body, neg := strings.CutPrefix(s, "-")
	body, percent := strings.CutSuffix(body, "p")
	n, err := uintArg(name, body, maxFilterPx)
	if err != nil {
		return wmAxis{}, dialect.InvalidArgument(name, s, expMarkPosition)
	}
	a := wmAxis{offset: float64(n)}
	if percent {
		// A fraction of the image size; 100% (and more) is the far edge.
		a.offset = min(float64(n)/100, 0.9999)
	}
	if neg {
		a.side = 2
	}
	return a, nil
}

func wmRatio(name, s string) (int, error) {
	if s == "none" {
		return 0, nil
	}
	n, err := integer(name, s)
	if err != nil {
		return 0, dialect.InvalidArgument(name, s, expMarkRatio)
	}
	return max(0, min(n, 100000)), nil
}

// format parses thumbor's ALLOWED_FORMATS (case-insensitive).
func format(s string) (transform.Format, bool) {
	switch strings.ToLower(s) {
	case "png":
		return transform.FormatPNG, true
	case "jpeg", "jpg":
		return transform.FormatJPEG, true
	case "gif":
		return transform.FormatGIF, true
	case "webp":
		return transform.FormatWebP, true
	case "avif":
		return transform.FormatAVIF, true
	case "heic", "heif":
		return transform.FormatHEIC, true
	}
	return transform.FormatUnset, false
}

func color(name, s string) (transform.Color, error) {
	c, ok := filters.ParseColor(s)
	if !ok {
		return transform.Color{}, dialect.InvalidArgument(name, s, expColor)
	}
	return transform.Color{R: c.R, G: c.G, B: c.B, A: c.A}, nil
}

// wantArgs checks the argument count.
func wantArgs(name string, args []string, lo, hi int, exp string) error {
	if len(args) < lo || len(args) > hi {
		return dialect.InvalidArgument(name, strings.Join(args, ","), exp)
	}
	return nil
}

func oneUint(name string, args []string, maxV int, exp string) (int, error) {
	if err := wantArgs(name, args, 1, 1, exp); err != nil {
		return 0, err
	}
	n, err := uintArg(name, args[0], maxV)
	if err != nil {
		return 0, dialect.InvalidArgument(name, args[0], exp)
	}
	return n, nil
}

func oneBool(name string, args []string) (bool, error) {
	if err := wantArgs(name, args, 1, 1, expBool); err != nil {
		return false, err
	}
	return parseBool(name, args[0])
}

// onePct parses one integer percentage, clamped to −100..100 as thumbor's
// filters document.
func onePct(name string, args []string) (int, error) {
	if err := wantArgs(name, args, 1, 1, expPct100); err != nil {
		return 0, err
	}
	return pct(name, args[0])
}

func pct(name, s string) (int, error) {
	n, err := integer(name, s)
	if err != nil {
		return 0, err
	}
	return max(-100, min(n, 100)), nil
}

// uintArg parses thumbor's PositiveNumber ([\d]+) up to maxV.
func uintArg(name, s string, maxV int) (int, error) {
	if !isDigits(s) || len(s) > 10 {
		return 0, dialect.InvalidArgument(name, s, expUint)
	}
	n, err := strconv.Atoi(s)
	if err != nil || n > maxV {
		return 0, dialect.InvalidArgument(name, s, "an integer from 0 to "+strconv.Itoa(maxV))
	}
	return n, nil
}

// integer parses thumbor's Number (-?[\d]+).
func integer(name, s string) (int, error) {
	body := strings.TrimPrefix(s, "-")
	if !isDigits(body) || len(body) > 9 {
		return 0, dialect.InvalidArgument(name, s, expInt)
	}
	n, _ := strconv.Atoi(s)
	return n, nil
}

// decimal parses thumbor's DecimalNumber: an optional "-", digits and at
// most one "." with a digit on at least one side.
func decimal(name, s string) (float64, error) {
	body := strings.TrimPrefix(s, "-")
	digits, dots := 0, 0
	for i := range len(body) {
		switch c := body[i]; {
		case c >= '0' && c <= '9':
			digits++
		case c == '.':
			dots++
		default:
			return 0, dialect.InvalidArgument(name, s, expDecimal)
		}
	}
	if digits == 0 || dots > 1 || len(body) > 32 {
		return 0, dialect.InvalidArgument(name, s, expDecimal)
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, dialect.InvalidArgument(name, s, expDecimal)
	}
	return v, nil
}

// parseBool parses thumbor's Boolean: true, True, 1, false, False or 0.
func parseBool(name, s string) (bool, error) {
	switch s {
	case "true", "True", "1":
		return true, nil
	case "false", "False", "0":
		return false, nil
	}
	return false, dialect.InvalidArgument(name, s, expBool)
}
