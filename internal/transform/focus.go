package transform

// MaxSourceCoord bounds source-pixel coordinates (FocusRect): libvips'
// VIPS_MAX_COORD, the largest image side the planner accepts.
const MaxSourceCoord = 10_000_000

// FocusRect is a focus point or region in source pixels: X1,Y1 is the
// top-left and X2,Y2 the bottom-right corner (X1 ≤ X2, Y1 ≤ Y2; equal
// corners are a point). Coordinates are in the source as displayed, that
// is after EXIF orientation and before the pipeline's own rotate, flip,
// trim, crop or resize; for a chained pipeline, in the previous
// pipeline's result.
//
// The planner turns it into the fractional focus point Center/(width,
// height) of that image, so a pixel focus gives exactly the crop geometry
// of the equivalent FocusX/FocusY. When the pipeline also pre-crops
// (Pipeline.Crop), the pipeline gravity's point is taken relative to the
// crop, like thumbor's focal points after a manual crop.
type FocusRect struct {
	X1, Y1, X2, Y2 int
}

// Center is the focus point in source pixels. It rounds down like
// thumbor's FocalPoint.from_square (left + width // 2), which dialects
// with a pixel focal rectangle reproduce.
func (r FocusRect) Center() (x, y int) {
	return r.X1 + (r.X2-r.X1)/2, r.Y1 + (r.Y2-r.Y1)/2
}

// Fraction is the focus point as a fraction (0..1) of a width×height
// image: Center divided by the size, clamped to the image. A non-positive
// size gives the centre (0.5).
func (r FocusRect) Fraction(width, height int) (fx, fy float64) {
	x, y := r.Center()
	return fraction(x, width), fraction(y, height)
}

func fraction(v, size int) float64 {
	if size <= 0 {
		return 0.5
	}
	return min(max(float64(v)/float64(size), 0), 1)
}

// FocusFromPixels is the gravity a dialect sets for a focus given in
// source pixels (thumbor focal(AxB:CxD), Cloudinary g_xy_center with
// absolute x_/y_): GravityFocusPoint with FocusPx set to the rectangle
// (x1,y1)-(x2,y2). Pass the same corner twice for a point. The corners
// may come in any order; ok is false when a coordinate is negative or
// above MaxSourceCoord, so the result always passes Validate.
func FocusFromPixels(x1, y1, x2, y2 int) (g Gravity, ok bool) {
	for _, v := range [...]int{x1, y1, x2, y2} {
		if v < 0 || v > MaxSourceCoord {
			return Gravity{}, false
		}
	}
	r := FocusRect{X1: min(x1, x2), Y1: min(y1, y2), X2: max(x1, x2), Y2: max(y1, y2)}
	return Gravity{Type: GravityFocusPoint, FocusPx: &r}, true
}
