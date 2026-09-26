package plan

import "github.com/xantiagoma/xtandard-image/internal/transform"

// InfoCrop places crop c on a w×h image (as displayed) the way the
// pre-crop of a processing request would (imgproxy CalcCropSize plus
// cropImage's calcPosition, offsets unscaled), for the imgproxy /info
// `crop` option (WP-63). An unset gravity is the centre. Face and object
// gravities (and smart gravity when faces were found) are placed on dets
// (detect.go), with c's objects position or op; other content-aware
// gravities (and those without detections) need the pixels, so they are
// placed at the centre and approximated reports it. A crop that does
// nothing is the whole image.
func InfoCrop(c *transform.Crop, op *transform.ObjectsPosition, w, h int, dets *Detections) (x, y, cw, ch int, approximated bool) {
	g, smart := resolveGravity(&c.Gravity)
	if box, ok := detectionBox(&c.Gravity, dets); ok {
		if c.Gravity.ObjectsPosition != nil {
			op = c.Gravity.ObjectsPosition
		}
		g, smart = placeObjects(box, op), InterestingNone
	}
	geo := CropGeometry{Width: cropSize(w, c.Width), Height: cropSize(h, c.Height), Gravity: g, OffsetScale: 1}
	x, y, cw, ch, _ = geo.Rect(w, h)
	return x, y, cw, ch, smart != InterestingNone
}
