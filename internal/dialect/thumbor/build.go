// Semantics from thumbor transformer.py and context.py (MIT). Copyright
// xtandard-image contributors, MIT.

package thumbor

import (
	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Reasons recorded in Request.Ignored.
const (
	reasonUnknown        = "unknown option"
	reasonNotImplemented = "not implemented"
	reasonUnsupported    = "unsupported"
	reasonM3             = "takes effect in M3"
	reasonApproximated   = "approximated"
	reasonNoEffect       = "no effect"
)

// maxTrimThreshold is the largest transform.Trim threshold; thumbor's
// tolerance (an RGB distance up to 442) is clamped to it.
const maxTrimThreshold = 255

// builder turns parts into a request. It is single-use.
type builder struct {
	r      *transform.Request
	strict bool
	fitIn  bool
}

func newBuilder(strict bool) *builder {
	return &builder{r: newRequest(), strict: strict}
}

func (b *builder) pipeline() *transform.Pipeline { return &b.r.Pipelines[0] }

// ignore records an option that is parsed but not applied.
func (b *builder) ignore(option, reason string) {
	for _, ig := range b.r.Ignored {
		if ig.Option == option && ig.Reason == reason {
			return
		}
	}
	b.r.Ignored = append(b.r.Ignored, transform.Ignored{Option: option, Reason: reason})
}

// build applies the path segments, then the filters in URL order, then
// decodes the image.
func (b *builder) build(u *parts) error {
	pl := b.pipeline()
	if u.debug {
		b.ignore("debug", reasonNotImplemented)
	}
	if u.meta {
		b.r.Kind = transform.KindInfo
		b.r.Info = &transform.InfoOptions{
			Schema: transform.InfoSchemaThumbor, Size: true, Format: true, Dimensions: true,
		}
	}
	if u.trim {
		pl.Trim = &transform.Trim{
			Threshold:            float64(min(u.trimTolerance, maxTrimThreshold)),
			ColorFromBottomRight: u.trimBottomRight,
		}
		if u.trimTolerance > maxTrimThreshold {
			b.ignore("trim", reasonApproximated)
		}
	}
	b.crop(u)
	b.resize(u)
	pl.Flip = transform.Flip{H: u.flipH, V: u.flipV}
	pl.Gravity.Type = alignGravity(u.halign, u.valign)
	if u.smart {
		pl.Gravity.Type = transform.GravitySmart
	}
	image, err := unescape("image", u.image)
	if err != nil {
		return err
	}
	for _, f := range u.filters {
		if image, err = b.filter(f, image); err != nil {
			return err
		}
	}
	if !imgproxy.PlausibleSource(image) {
		return dialect.InvalidURL("invalid source url")
	}
	b.r.Source = transform.SourceSpec{Raw: image, Encoding: transform.SourcePlain}
	return nil
}

// crop applies the manual crop AxB:CxD (pre-resize, source pixels).
// thumbor ignores an all-zero or empty rectangle.
func (b *builder) crop(u *parts) {
	if !u.crop {
		return
	}
	l, t, r, bt := u.cropBox[0], u.cropBox[1], u.cropBox[2], u.cropBox[3]
	if l == 0 && t == 0 && r == 0 && bt == 0 {
		return
	}
	if r <= l || bt <= t {
		b.ignore("crop", reasonNoEffect)
		return
	}
	b.pipeline().Crop = &transform.Crop{
		Width:  transform.Dim{Px: r - l},
		Height: transform.Dim{Px: bt - t},
		Gravity: transform.Gravity{
			Type: transform.GravityNorthWest, XOffset: float64(l), YOffset: float64(t),
		},
	}
}

// resize maps the size and fit-in segments. Without fit-in thumbor crops
// to fill the box and upscales; fit-in fits without upscaling; full-fit-in
// covers the box without cropping (and does upscale); adaptive- swaps the
// box when the orientations differ.
func (b *builder) resize(u *parts) {
	rs := &b.pipeline().Resize
	rs.Width, rs.Height = dim(u.width), dim(u.height)
	b.fitIn = u.fitIn
	switch {
	case u.fitIn && u.full:
		rs.Type, rs.Enlarge = transform.ResizeOutside, true
	case u.fitIn:
		rs.Type = transform.ResizeFit
	default:
		rs.Type, rs.Enlarge = transform.ResizeFill, true
	}
	rs.Adaptive = u.adaptive
}

func dim(s size) transform.Dim {
	if s.orig {
		return transform.Dim{Rel: 1}
	}
	return transform.Dim{Px: s.px}
}

// alignGravity maps halign/valign (defaults center/middle) to a compass
// gravity; both absent leaves the gravity unset (centre).
func alignGravity(h, v string) transform.GravityType {
	if h == "" && v == "" {
		return transform.GravityUnset
	}
	col := 1 // 0 west, 1 centre, 2 east
	switch h {
	case "left":
		col = 0
	case "right":
		col = 2
	}
	row := 1 // 0 north, 1 centre, 2 south
	switch v {
	case "top":
		row = 0
	case "bottom":
		row = 2
	}
	return compass(row, col)
}

// compass returns the gravity for row (0 north, 1 centre, 2 south) and col
// (0 west, 1 centre, 2 east).
func compass(row, col int) transform.GravityType {
	grid := [3][3]transform.GravityType{
		{transform.GravityNorthWest, transform.GravityNorth, transform.GravityNorthEast},
		{transform.GravityWest, transform.GravityCenter, transform.GravityEast},
		{transform.GravitySouthWest, transform.GravitySouth, transform.GravitySouthEast},
	}
	return grid[row][col]
}
