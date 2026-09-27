package plan

import (
	"image"
	"math"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Detections in chained pipelines (WP-92). The detector sees the source;
// a chained pipeline runs on the result of the one before it. The boxes
// follow the pixels through every step of the pipelines before it:
//
//   - the source as displayed → the stored orientation the first pipeline
//     starts from (Stored: the inverse of the EXIF rotate and flip, which
//     the first pipeline applies again as steps);
//   - crops (pre-crop, result crop, crop_objects, a mask's trim) keep the
//     part of each box inside the rectangle and drop boxes left outside;
//   - resizes (shrink-on-load included) change nothing: boxes are
//     fractions;
//   - rotations by multiples of 90 and flips rotate and mirror them;
//   - extend, padding and borders place them on the larger canvas;
//   - an arbitrary rotation maps them to the bounding box of their rotated
//     corners on the rotated canvas;
//   - filters, colour, flatten, masks without trim, rounded corners and
//     watermarks keep them as they are.
//
// A smart crop and a trim have rectangles only the executor knows: it maps
// the boxes with Crop on the rectangle it cut. Where a step has no mapping
// (MapStep's ok is false) the detections are dropped for the pipelines
// after it, which fall back like pipelines without detections (smart crop,
// nothing to blur or draw; "detections" in Unplanned).

// Stored maps d from the source as displayed onto the stored orientation
// the first pipeline's steps start from, for an EXIF orientation (1..8)
// and the pipeline's auto_rotate (the planner applies the orientation only
// when it is on, like mapArea).
func (d *Detections) Stored(o int, autoRotate bool) *Detections {
	angle, flip := orientation(o, autoRotate)
	if d == nil || (angle == 0 && !flip) {
		return d
	}
	// Displayed = the stored image rotated by angle, then mirrored.
	return d.mapPoints(func(x, y float64) (float64, float64) {
		if flip {
			x = 1 - x
		}
		return rotPoint(x, y, -angle)
	})
}

// MapStep maps d, fractions of a w×h image, through s, which runs on that
// image, and returns them as fractions of its result with the result's
// size (predicted for a resize). ok is false when s moves pixels in a way
// the boxes cannot follow from the plan: a smart crop or a trim (the
// executor maps their rectangle with Crop instead) or an unknown step.
// Boxes that leave the image are dropped, the others are clipped to it.
func (d *Detections) MapStep(s Step, w, h int) (out *Detections, ow, oh int, ok bool) {
	switch s := s.(type) {
	case LoadStep, ColourStep, FilterStep, ColourAdjustStep, FlattenStep, WatermarkStep,
		DrawDetectionsStep, RadiusStep:
		return d, w, h, true
	case MaskStep:
		if x, y, tw, th, cut := s.TrimRect(w, h); s.Trim && cut {
			return d.Crop(image.Rect(x, y, x+tw, y+th), w, h), tw, th, true
		}
		return d, w, h, true
	case PreCropStep:
		return d.cropGeometry(&s.CropGeometry, w, h)
	case CropStep:
		return d.cropGeometry(&s.CropGeometry, w, h)
	case ResizeStep:
		ow, oh = w, h
		if s.W > 0 && s.H > 0 {
			ow, oh = s.W, s.H
		}
		return d, ow, oh, true
	case RotateStep:
		angle := (s.Angle%360 + 360) % 360
		if angle%90 != 0 {
			return nil, w, h, false
		}
		ow, oh = w, h
		if angle%180 != 0 {
			ow, oh = h, w
		}
		return d.mapPoints(func(x, y float64) (float64, float64) { return rotPoint(x, y, angle) }), ow, oh, true
	case FlipStep:
		return d.mapPoints(func(x, y float64) (float64, float64) {
			if s.Horizontal {
				x = 1 - x
			}
			if s.Vertical {
				y = 1 - y
			}
			return x, y
		}), w, h, true
	case ExtendStep:
		x, y, cw, ch, grow := s.Rect(w, h)
		if !grow {
			return d, w, h, true
		}
		return d.place(x, y, w, h, cw, ch), cw, ch, true
	case PaddingStep:
		cw, ch := w+s.Left+s.Right, h+s.Top+s.Bottom
		return d.place(s.Left, s.Top, w, h, cw, ch), cw, ch, true
	case BorderStep:
		cw, ch := w+s.Left+s.Right, h+s.Top+s.Bottom
		return d.place(s.Left, s.Top, w, h, cw, ch), cw, ch, true
	case RotateAnyStep:
		out, ow, oh = d.rotateAny(s.Angle, w, h)
		return out, ow, oh, true
	default: // TrimStep and anything new: not mappable from the plan
		return nil, w, h, false
	}
}

// Crop maps d, fractions of a w×h image, onto the rectangle r of it (a
// crop the executor placed: a smart crop, a trim).
func (d *Detections) Crop(r image.Rectangle, w, h int) *Detections {
	if d == nil || r.Dx() <= 0 || r.Dy() <= 0 || w <= 0 || h <= 0 {
		return d
	}
	fw, fh := float64(w), float64(h)
	x0, y0 := float64(r.Min.X)/fw, float64(r.Min.Y)/fh
	sx, sy := fw/float64(r.Dx()), fh/float64(r.Dy())
	return d.mapPoints(func(x, y float64) (float64, float64) { return (x - x0) * sx, (y - y0) * sy })
}

// cropGeometry maps d through a crop the plan places; a smart crop's
// position is unknown.
func (d *Detections) cropGeometry(c *CropGeometry, w, h int) (*Detections, int, int, bool) {
	x, y, cw, ch, cut := c.Rect(w, h)
	if !cut {
		return d, w, h, true
	}
	if c.Smart != InterestingNone {
		return nil, cw, ch, false
	}
	return d.Crop(image.Rect(x, y, x+cw, y+ch), w, h), cw, ch, true
}

// place maps d, fractions of a w×h image, onto a cw×ch canvas the image
// is placed on at x, y.
func (d *Detections) place(x, y, w, h, cw, ch int) *Detections {
	if d == nil || cw <= 0 || ch <= 0 {
		return d
	}
	fw, fh, fcw, fch := float64(w), float64(h), float64(cw), float64(ch)
	return d.mapPoints(func(px, py float64) (float64, float64) {
		return (px*fw + float64(x)) / fcw, (py*fh + float64(y)) / fch
	})
}

// rotateAny maps d through RotateAnyStep's rotation by angle degrees
// clockwise (libvips similarity: the canvas is the extent of the rotated
// corners, RotatedSize).
func (d *Detections) rotateAny(angle float64, w, h int) (*Detections, int, int) {
	ow, oh := RotatedSize(w, h, angle)
	if d == nil {
		return nil, ow, oh
	}
	rad := angle * math.Pi / 180
	a, b := math.Cos(rad), -math.Sin(rad)
	c, e := -b, a
	fw, fh := float64(w), float64(h)
	xs := [4]float64{0, a * fw, b * fh, a*fw + b*fh}
	ys := [4]float64{0, c * fw, e * fh, c*fw + e*fh}
	minX, maxX := min(xs[0], xs[1], xs[2], xs[3]), max(xs[0], xs[1], xs[2], xs[3])
	minY, maxY := min(ys[0], ys[1], ys[2], ys[3]), max(ys[0], ys[1], ys[2], ys[3])
	ew, eh := max(maxX-minX, 1), max(maxY-minY, 1)
	return d.mapPoints(func(x, y float64) (float64, float64) {
		px, py := x*fw, y*fh
		return (a*px + b*py - minX) / ew, (c*px + e*py - minY) / eh
	}), ow, oh
}

// rotPoint rotates the point x, y (fractions) clockwise with the image by
// angle, a multiple of 90.
func rotPoint(x, y float64, angle int) (float64, float64) {
	switch (angle%360 + 360) % 360 {
	case 90:
		return 1 - y, x
	case 180:
		return 1 - x, 1 - y
	case 270:
		return y, 1 - x
	default:
		return x, y
	}
}

// mapPoints maps every box (the bounding box of its mapped corners, exact
// for the axis-aligned steps) and landmark through f, clips the boxes to
// the image and drops those left without area. Landmarks are clamped.
func (d *Detections) mapPoints(f func(x, y float64) (float64, float64)) *Detections {
	if d == nil {
		return nil
	}
	mapList := func(in []Detected) []Detected {
		if in == nil {
			return nil
		}
		out := make([]Detected, 0, len(in))
		for _, x := range in {
			b := x.Box
			var x0, y0, x1, y1 float64
			for i, c := range [4][2]float64{
				{b.Left, b.Top},
				{b.Left + b.Width, b.Top},
				{b.Left, b.Top + b.Height},
				{b.Left + b.Width, b.Top + b.Height},
			} {
				px, py := f(c[0], c[1])
				if i == 0 {
					x0, y0, x1, y1 = px, py, px, py
					continue
				}
				x0, y0, x1, y1 = min(x0, px), min(y0, py), max(x1, px), max(y1, py)
			}
			x0, y0, x1, y1 = clampUnit(x0), clampUnit(y0), clampUnit(x1), clampUnit(y1)
			if !(x1 > x0) || !(y1 > y0) {
				continue
			}
			m := Detected{Class: x.Class, Score: x.Score, Box: transform.Area{Left: x0, Top: y0, Width: x1 - x0, Height: y1 - y0}}
			if x.Landmarks != nil {
				m.Landmarks = make([][2]float64, len(x.Landmarks))
				for i, l := range x.Landmarks {
					px, py := f(l[0], l[1])
					m.Landmarks[i] = [2]float64{clampUnit(px), clampUnit(py)}
				}
			}
			out = append(out, m)
		}
		return out
	}
	return &Detections{Faces: mapList(d.Faces), Objects: mapList(d.Objects)}
}

// MapPlan maps d, fractions of the w×h image pl runs on (one frame of an
// animation), through pl's Steps and After (MapStep); nil when a step
// cannot carry them. The planner uses it to predict the chained plans; the
// executor maps step by step on what it observed.
func (d *Detections) MapPlan(pl *Plan, w, h int) *Detections {
	for _, steps := range [][]Step{pl.Steps, pl.After} {
		for _, s := range steps {
			if d == nil {
				return nil
			}
			var ok bool
			if d, w, h, ok = d.MapStep(s, w, h); !ok {
				return nil
			}
		}
	}
	return d
}

// SourceDetections are the source's detections as fractions of the image
// the root plan pl starts from (its Load size, stored orientation), for
// MapStep; nil when there are none, or when the root loads several pages
// stacked into one image (the detections describe the first page).
func SourceDetections(req *transform.Request, meta *SourceMeta, pl *Plan) *Detections {
	d := meta.Detections
	if d == nil || (pl.Animation == nil && pl.Load.NPages > 1) {
		return nil
	}
	autoRotate := true
	if len(req.Pipelines) > 0 && req.Pipelines[0].AutoRotate != nil {
		autoRotate = *req.Pipelines[0].AutoRotate
	}
	return d.Stored(meta.Orientation, autoRotate)
}
