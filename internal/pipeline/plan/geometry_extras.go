package plan

import (
	"math"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Geometry extras (WP-60): crop_aspect_ratio, arbitrary rotation, shape
// masks, borders and rounded corners. imgproxy has only crop_aspect_ratio
// (Pro); the others come from the imgix, ImageKit, Cloudinary and wsrv
// dialects and have no imgproxy pipeline position to mirror. Their place in
// the step order is:
//
//	… result crop → filters → colour adjustments → RotateAnyStep →
//	extend → padding → MaskStep → BorderStep → RadiusStep →
//	encoder size fix → flatten → watermarks (After)
//
// The rotation runs after the colour adjustments, so the corners it fills
// keep the exact background colour, and before extend and padding, so they
// grow the rotated canvas to the requested box. Mask, border and radius
// shape the finished canvas (extend and padding included), like wsrv's
// mask, Cloudinary's bo_ and r_ and imgix's border and border-radius; the
// border comes before the radius so that the corners round the border too.
// Flatten then fills whatever they made transparent with the background
// (white for formats without alpha).

// RotateAnyStep rotates by an arbitrary angle (not a multiple of 90) with
// libvips similarity: the canvas grows to the rotated bounding box and the
// uncovered corners are filled with Background, or left transparent when it
// is nil (an alpha band is added).
type RotateAnyStep struct {
	Angle      float64          // degrees clockwise, (−180, 180]
	Background *transform.Color // nil = transparent
	W, H       int              // predicted canvas
}

// MaskStep cuts the image to a shape (wsrv mask, imgix mask=ellipse): the
// alpha band is multiplied by the anti-aliased shape; outside it the image
// becomes transparent, or Background when set. Trim crops to the shape's
// bounding box (not for ellipses, which fill the frame).
type MaskStep struct {
	Shape      transform.MaskShape
	Trim       bool
	Background *transform.Color // nil = transparent
}

// BorderStep draws a solid border of the given widths (already scaled by
// DPR) around the image, growing the canvas.
type BorderStep struct {
	Top, Right, Bottom, Left int
	Color                    transform.Color
}

// RadiusStep rounds the corners with circular radii in pixels (already
// scaled by DPR); Max makes the image an ellipse (a circle when square,
// Cloudinary r_max, ImageKit r-max). Radii that do not fit are scaled down
// together, as CSS border-radius does.
type RadiusStep struct {
	TopLeft, TopRight, BottomRight, BottomLeft int
	Max                                        bool
}

func (RotateAnyStep) step() {}
func (MaskStep) step()      {}
func (BorderStep) step()    {}
func (RadiusStep) step()    {}

// cropAspect corrects a w×h crop area (0 = the whole image on that axis) of
// a srcW×srcH image to the aspect ratio ar (imgproxy Pro
// crop_aspect_ratio): the area shrinks to match, or grows with enlarge and
// then shrinks again, keeping the ratio, when it no longer fits the image.
func cropAspect(w, h, srcW, srcH int, ar float64, enlarge bool) (int, int) {
	fw := float64(minNonZero(min(w, srcW), srcW))
	fh := float64(minNonZero(min(h, srcH), srcH))
	switch cur := fw / fh; {
	case cur > ar && enlarge:
		fh = fw / ar
	case cur > ar:
		fw = fh * ar
	case cur < ar && enlarge:
		fw = fh * ar
	case cur < ar:
		fh = fw / ar
	}
	if fw > float64(srcW) {
		fw = float64(srcW)
		fh = fw / ar
	}
	if fh > float64(srcH) {
		fh = float64(srcH)
		fw = fh * ar
	}
	return max(1, min(roundInt(fw), srcW)), max(1, min(roundInt(fh), srcH))
}

// RotatedSize is the canvas libvips similarity gives a w×h image rotated by
// angle degrees clockwise: the rounded extent of the transformed corners
// (vips__transform_set_area).
func RotatedSize(w, h int, angle float64) (int, int) {
	rad := angle * math.Pi / 180
	a, b := math.Cos(rad), -math.Sin(rad)
	c, d := -b, a
	fw, fh := float64(w), float64(h)
	xs := [4]float64{0, a * fw, b * fh, a*fw + b*fh}
	ys := [4]float64{0, c * fw, d * fh, c*fw + d*fh}
	extent := func(v [4]float64) int {
		lo, hi := min(v[0], v[1], v[2], v[3]), max(v[0], v[1], v[2], v[3])
		return max(1, int(hi-lo+0.5)) // VIPS_ROUND_INT
	}
	return extent(xs), extent(ys)
}

// Point is a vertex of an Outline, in pixels from the top-left corner of
// the frame.
type Point struct{ X, Y float64 }

// Outline is a closed polygon (the last vertex joins the first). Curves are
// flattened finely enough (segments of about one pixel) that rasterising it
// with coverage anti-aliasing gives smooth edges at any size.
type Outline []Point

// maxArcSegments bounds the vertices of one flattened curve.
const maxArcSegments = 1 << 16

// arcSegments is the number of segments for an arc of the given length in
// pixels: about one pixel each.
func arcSegments(length float64, minimum int) int {
	if !(length > 0) {
		return minimum
	}
	return min(max(int(math.Ceil(length)), minimum), maxArcSegments)
}

// appendArc appends the arc of the ellipse centred at (cx, cy) with radii
// rx, ry from angle a0 to a1 (radians, clockwise on screen), end points
// included.
func appendArc(o Outline, cx, cy, rx, ry, a0, a1 float64) Outline {
	if rx <= 0 || ry <= 0 {
		return append(o, Point{cx, cy})
	}
	n := arcSegments(max(rx, ry)*math.Abs(a1-a0), 2)
	for i := range n + 1 {
		t := a0 + (a1-a0)*float64(i)/float64(n)
		o = append(o, Point{cx + rx*math.Cos(t), cy + ry*math.Sin(t)})
	}
	return o
}

// Outline is the rounded rectangle of a w×h frame.
func (r RadiusStep) Outline(w, h int) Outline {
	fw, fh := float64(w), float64(h)
	if r.Max {
		return ellipse(fw/2, fh/2, fw/2, fh/2)
	}
	tl, tr := float64(r.TopLeft), float64(r.TopRight)
	br, bl := float64(r.BottomRight), float64(r.BottomLeft)
	// CSS border-radius: scale every radius by the same factor until the
	// adjacent ones fit each side.
	f := 1.0
	for _, s := range [...]struct{ side, sum float64 }{
		{fw, tl + tr}, {fw, bl + br}, {fh, tl + bl}, {fh, tr + br},
	} {
		if s.sum > s.side {
			f = min(f, s.side/s.sum)
		}
	}
	tl, tr, br, bl = tl*f, tr*f, br*f, bl*f
	o := make(Outline, 0, 16)
	o = appendArc(o, tl, tl, tl, tl, math.Pi, 1.5*math.Pi)
	if tl <= 0 {
		o[len(o)-1] = Point{0, 0}
	}
	o = appendArc(o, fw-tr, tr, tr, tr, 1.5*math.Pi, 2*math.Pi)
	if tr <= 0 {
		o[len(o)-1] = Point{fw, 0}
	}
	o = appendArc(o, fw-br, fh-br, br, br, 0, 0.5*math.Pi)
	if br <= 0 {
		o[len(o)-1] = Point{fw, fh}
	}
	o = appendArc(o, bl, fh-bl, bl, bl, 0.5*math.Pi, math.Pi)
	if bl <= 0 {
		o[len(o)-1] = Point{0, fh}
	}
	return o
}

// Rounds reports whether the step changes anything.
func (r RadiusStep) Rounds() bool {
	return r.Max || r.TopLeft > 0 || r.TopRight > 0 || r.BottomRight > 0 || r.BottomLeft > 0
}

func ellipse(cx, cy, rx, ry float64) Outline {
	n := arcSegments(2*math.Pi*max(rx, ry), 16)
	o := make(Outline, n)
	for i := range n {
		t := 2 * math.Pi * float64(i) / float64(n)
		o[i] = Point{cx + rx*math.Cos(t), cy + ry*math.Sin(t)}
	}
	return o
}

// maskShape is the outline of a wsrv mask on a w×h frame and the size of
// its bounding box (wsrv Mask::svg_path_by_type): circle and polygons are
// centred with the frame's shorter side as diameter, polygons and the heart
// then scaled up to fit the frame; the ellipse fills it.
func maskShape(shape transform.MaskShape, w, h int) (o Outline, bw, bh int) {
	fw, fh := float64(w), float64(h)
	outer := float64(min(w, h)) / 2
	midX, midY := fw/2, fh/2
	switch shape {
	case transform.MaskEllipse:
		return ellipse(midX, midY, midX, midY), w, h
	case transform.MaskCircle:
		return ellipse(midX, midY, outer, outer), min(w, h), min(w, h)
	case transform.MaskHeart:
		pts := make(Outline, 0, 315)
		for i := range 315 {
			t := -math.Pi + float64(i)*0.02
			x := 16 * math.Pow(math.Sin(t), 3)
			y := 13*math.Cos(t) - 5*math.Cos(2*t) - 2*math.Cos(3*t) - math.Cos(4*t)
			pts = append(pts, Point{outer + x*outer, outer - y*outer})
		}
		return fitShape(pts, w, h)
	default:
	}
	inner, initial := outer, 0.0
	var points int
	switch shape {
	case transform.MaskHexagon:
		points = 6
	case transform.MaskPentagon:
		points = 5
	case transform.MaskPentagon180:
		points, initial = 5, math.Pi
	case transform.MaskStar:
		points, inner = 10, outer*0.382
	case transform.MaskSquare:
		points = 4
	case transform.MaskTriangle:
		points = 3
	case transform.MaskTriangle180:
		points, initial = 3, math.Pi
	default: // unset: the whole frame
		return Outline{{0, 0}, {fw, 0}, {fw, fh}, {0, fh}}, w, h
	}
	pts := make(Outline, points)
	for i := range points {
		a := float64(i)*2*math.Pi/float64(points) - math.Pi/2 + initial
		r := inner
		if i%2 == 0 {
			r = outer
		}
		pts[i] = Point{midX + r*math.Cos(a), midY + r*math.Sin(a)}
	}
	return fitShape(pts, w, h)
}

// fitShape scales pts about the origin so their rounded bounding box fits
// w×h and centres it (wsrv translation_and_scaling).
func fitShape(pts Outline, w, h int) (Outline, int, int) {
	xMin, yMin := math.Inf(1), math.Inf(1)
	xMax, yMax := math.Inf(-1), math.Inf(-1)
	for _, p := range pts {
		xMin, xMax = min(xMin, p.X), max(xMax, p.X)
		yMin, yMax = min(yMin, p.Y), max(yMax, p.Y)
	}
	mx, my := math.Round(xMin), math.Round(yMin)
	mw, mh := max(math.Round(xMax-xMin), 1), max(math.Round(yMax-yMin), 1)
	scale := min(float64(w)/mw, float64(h)/mh)
	sw, sh := mw*scale, mh*scale
	tx := float64(w)/2 - (mx*scale + sw/2)
	ty := float64(h)/2 - (my*scale + sh/2)
	for i := range pts {
		pts[i] = Point{pts[i].X*scale + tx, pts[i].Y*scale + ty}
	}
	return pts, int(math.Round(sw)), int(math.Round(sh))
}

// Outline is the mask shape on a w×h frame.
func (m MaskStep) Outline(w, h int) Outline {
	o, _, _ := maskShape(m.Shape, w, h)
	return o
}

// TrimRect is the area Trim keeps of a w×h frame: the shape's bounding
// box, centred. ok is false when nothing is cut.
func (m MaskStep) TrimRect(w, h int) (x, y, tw, th int, ok bool) {
	if !m.Trim || m.Shape == transform.MaskEllipse {
		return 0, 0, w, h, false
	}
	_, bw, bh := maskShape(m.Shape, w, h)
	bw, bh = min(max(bw, 1), w), min(max(bh, 1), h)
	if bw >= w && bh >= h {
		return 0, 0, w, h, false
	}
	x = int(math.Round(float64(w-bw) / 2))
	y = int(math.Round(float64(h-bh) / 2))
	return x, y, bw, bh, true
}

// emitRotateAny adds the arbitrary rotation.
func emitRotateAny(p *transform.Pipeline, b *builder) {
	if p.RotateAny == 0 || math.IsNaN(p.RotateAny) || math.IsInf(p.RotateAny, 0) {
		return
	}
	w, h := RotatedSize(b.curW, b.curH, p.RotateAny)
	b.add(RotateAnyStep{Angle: p.RotateAny, Background: p.Background, W: w, H: h})
	b.random = true
	b.size(w, h)
	b.alpha = b.alpha || p.Background == nil || p.Background.A < 255
}

// emitShape adds mask, border and radius.
func emitShape(p *transform.Pipeline, g *geo, b *builder) {
	if m := p.Mask; m != nil && m.Shape != transform.MaskUnset {
		st := MaskStep{Shape: m.Shape, Trim: m.Trim, Background: m.Background}
		b.add(st)
		if _, _, w, h, ok := st.TrimRect(b.curW, b.curH); ok {
			b.size(w, h)
		}
		b.alpha = b.alpha || m.Background == nil || m.Background.A < 255
	}
	if bo := p.Border; bo != nil {
		st := BorderStep{
			Top:    scaleDimEven(bo.Top, g.dprScale),
			Right:  scaleDimEven(bo.Right, g.dprScale),
			Bottom: scaleDimEven(bo.Bottom, g.dprScale),
			Left:   scaleDimEven(bo.Left, g.dprScale),
			Color:  bo.Color,
		}
		if st.Top != 0 || st.Right != 0 || st.Bottom != 0 || st.Left != 0 {
			b.add(st)
			b.size(b.curW+st.Left+st.Right, b.curH+st.Top+st.Bottom)
			b.alpha = b.alpha || bo.Color.A < 255
		}
	}
	if r := p.Radius; r != nil {
		st := RadiusStep{
			TopLeft:     scaleDimEven(r.TopLeft, g.dprScale),
			TopRight:    scaleDimEven(r.TopRight, g.dprScale),
			BottomRight: scaleDimEven(r.BottomRight, g.dprScale),
			BottomLeft:  scaleDimEven(r.BottomLeft, g.dprScale),
			Max:         r.Max,
		}
		if st.Rounds() {
			b.add(st)
			b.alpha = true
		}
	}
}
