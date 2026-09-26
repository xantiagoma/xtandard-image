package plan

import "github.com/xantiagoma/xtandard-image/internal/transform"

// InfoCrop places crop c on a w×h image the way the pre-crop of a
// processing request would (imgproxy CalcCropSize plus cropImage's
// calcPosition, offsets unscaled), for the imgproxy /info `crop` option
// (WP-63). An unset gravity is the centre. Content-aware gravities
// (smart, entropy, face, object) need the pixels; they are placed at the
// centre and approximated reports it. A crop that does nothing is the
// whole image.
func InfoCrop(c *transform.Crop, w, h int) (x, y, cw, ch int, approximated bool) {
	g, smart := resolveGravity(&c.Gravity)
	geo := CropGeometry{Width: cropSize(w, c.Width), Height: cropSize(h, c.Height), Gravity: g, OffsetScale: 1}
	x, y, cw, ch, _ = geo.Rect(w, h)
	return x, y, cw, ch, smart != InterestingNone
}
