package plan

import "github.com/xantiagoma/xtandard-image/internal/transform"

// pixelFocus returns p with every pixel focus (transform.Gravity.FocusPx)
// replaced by the equivalent fractional focus point, or p itself when it
// has none. The rest of the planner only knows fractions, so a pixel focus
// yields exactly the geometry of FocusX/FocusY at the same place.
//
// Pixels are in the source as displayed: s is one frame as stored, so the
// EXIF orientation swaps its sides first. The point is then carried
// through the pipeline's rotate and flip (the fractions the planner reads
// are in the rotated image, see undoRotateFlip). The pipeline gravity
// places the result crop, which runs on the pre-cropped image, so with a
// pre-crop its point is taken relative to that crop (clamped to it), like
// thumbor's focal points after a manual crop. Trim is not accounted for:
// the fraction of the untrimmed source applies to the trimmed image.
func pixelFocus(p *transform.Pipeline, s *source) *transform.Pipeline {
	cropPx := p.Crop != nil && p.Crop.Gravity.FocusPx != nil
	if p.Gravity.FocusPx == nil && !cropPx {
		return p
	}
	w, h := s.w, s.h
	if angle, _ := orientation(s.orientation, p.AutoRotate == nil || *p.AutoRotate); angle%180 == 90 {
		w, h = h, w
	}
	rotate := ((p.Rotate%360)/90*90 + 360) % 360
	rw, rh := w, h
	if rotate%180 == 90 {
		rw, rh = h, w
	}
	frac := func(g *transform.Gravity) transform.Gravity {
		fx, fy := g.FocusPx.Fraction(w, h)
		fx, fy = rotateFlipPoint(fx, fy, rotate, p.Flip.H, p.Flip.V)
		out := *g
		out.FocusPx, out.FocusX, out.FocusY = nil, fx, fy
		return out
	}

	cp := *p
	if cropPx {
		c := *p.Crop
		c.Gravity = frac(&c.Gravity)
		cp.Crop = &c
	}
	if p.Gravity.FocusPx == nil {
		return &cp
	}
	g := frac(&p.Gravity)
	if c := cp.Crop; c != nil {
		// The pre-crop rectangle in the rotated source, as emitGeometry
		// places it (a smart crop's position is unknown: its centre).
		cw := minNonZero(cropSize(rw, c.Width), rw)
		ch := minNonZero(cropSize(rh, c.Height), rh)
		if cw < rw || ch < rh {
			cg, _ := resolveGravity(&c.Gravity)
			x, y := position(rw, rh, cw, ch, cg, 1)
			g.FocusX = clampUnit((g.FocusX*float64(rw) - float64(x)) / float64(cw))
			g.FocusY = clampUnit((g.FocusY*float64(rh) - float64(y)) / float64(ch))
		}
	}
	cp.Gravity = g
	return &cp
}

// rotateFlipPoint carries a fractional point through a clockwise rotation
// by angle (a multiple of 90) and then the flips: the inverse of
// undoRotateFlip for GravityFocusPoint.
func rotateFlipPoint(x, y float64, angle int, flipX, flipY bool) (float64, float64) {
	switch angle {
	case 90:
		x, y = 1-y, x
	case 180:
		x, y = 1-x, 1-y
	case 270:
		x, y = y, 1-x
	}
	if flipX {
		x = 1 - x
	}
	if flipY {
		y = 1 - y
	}
	return x, y
}

func clampUnit(f float64) float64 { return min(max(f, 0), 1) }
