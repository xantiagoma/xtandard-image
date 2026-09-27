package plan

import (
	"math"
	"slices"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Face and object detection (WP-72/73). The planner never runs a model:
// the caller detects on the source (internal/detect, upright, first page)
// when NeedsDetection asks for it and hands the boxes over in
// SourceMeta.Detections. The planner then
//
//   - turns face, object and weighted-object gravities (and smart gravity
//     when faces were found) into GravityObject placements: the crop is
//     placed so that the selected detections' box lands where
//     objects_position says (centred by default);
//   - turns crop_objects (and face gravity with a face pad) into a pre-crop
//     on the selected detections' box;
//   - maps the boxes of blur_detections and draw_detections through the
//     geometry like blur_areas.
//
// Without detections (nil: detection did not run, or nothing matched) the
// gravities fall back to the smart crop, crop_objects does nothing and the
// filters have nothing to draw. Detections describe the source; chained
// pipelines use them mapped through the pipelines before them
// (detectmap.go). In the first pipeline a trim is not accounted for: the
// boxes of the untrimmed source apply to the trimmed image (like pixel
// focus points).

// Detections are the faces and objects detected in the source.
type Detections struct {
	// Faces and Objects are best score first. Boxes are fractions of the
	// source as displayed: EXIF orientation applied, first page, before
	// the rotate and flip options (like transform.Area in blur_areas).
	Faces   []Detected
	Objects []Detected
}

// Detected is one detection.
type Detected struct {
	Class string // transform.FaceClass or a COCO class (transform.ObjectClasses)
	Score float64
	Box   transform.Area
	// Landmarks of a face as [x, y] fractions like Box (right eye, left
	// eye, nose tip, right and left mouth corners), for /info; nil for
	// objects. The planner does not use them.
	Landmarks [][2]float64
}

// DefaultDetectionSigma is the blur sigma of blur_detections-like filters
// that name neither a sigma nor a pixelate size.
const DefaultDetectionSigma = 10

// DetectionNeeds is what a request needs from the detector (NeedsDetection).
type DetectionNeeds struct {
	// Faces and Objects are required: an option of the request needs that
	// model, and the request fails (Unsupported) when it is missing.
	Faces, Objects bool
	// SmartFaces is set when smart gravity would crop on faces: faces are
	// detected when the model is there, and the smart crop is used
	// otherwise.
	SmartFaces bool
}

// Any reports whether detection should run at all.
func (n DetectionNeeds) Any() bool { return n.Faces || n.Objects || n.SmartFaces }

// NeedsDetection reports which detection models req uses: the gravities
// and filters of its pipelines (chained ones use the source's boxes mapped
// through the pipelines before them, WP-92), or for an info request (KindInfo)
// detect_objects and a crop placed by a detection gravity. Class lists
// must be valid (Validate).
func NeedsDetection(req *transform.Request) DetectionNeeds {
	var n DetectionNeeds
	need := func(classes []string) {
		f, o := classModels(classes)
		n.Faces = n.Faces || f
		n.Objects = n.Objects || o
	}
	gravity := func(g *transform.Gravity) {
		switch g.Type {
		case transform.GravityFace:
			n.Faces = true
		case transform.GravityObject, transform.GravityObjectWeighted:
			need(g.Classes)
		case transform.GravitySmart:
			n.SmartFaces = true
		default:
		}
	}
	info := req.Kind == transform.KindInfo
	for i := range req.Pipelines {
		if info {
			break
		}
		p := &req.Pipelines[i]
		gravity(&p.Gravity)
		if p.Crop != nil {
			gravity(&p.Crop.Gravity)
		}
		if c := p.CropObjects; c != nil {
			need(c.Classes)
		}
		for _, f := range []*transform.DetectionFilter{p.BlurDetections, p.DrawDetections} {
			if f != nil {
				need(f.Classes)
			}
		}
	}
	if in := req.Info; in != nil && info {
		if in.DetectObjects {
			need(in.DetectClasses)
		}
		if in.Crop != nil {
			gravity(&in.Crop.Gravity)
		}
	}
	if n.Faces {
		n.SmartFaces = false // detected anyway
	}
	return n
}

// classModels reports which models a class list needs: none named is
// every class of both.
func classModels(classes []string) (faces, objects bool) {
	resolved, _ := transform.ResolveClasses(classes)
	if len(resolved) == 0 {
		return true, true
	}
	for _, c := range resolved {
		if c == transform.FaceClass {
			faces = true
		} else {
			objects = true
		}
	}
	return faces, objects
}

// Select returns the detections of the classes (see
// transform.Gravity.Classes; none = every class), faces first, each list
// best first.
func (d *Detections) Select(classes []string) []Detected {
	if d == nil {
		return nil
	}
	want, _ := transform.ResolveClasses(classes)
	out := make([]Detected, 0, len(d.Faces)+len(d.Objects))
	for _, list := range [][]Detected{d.Faces, d.Objects} {
		for _, x := range list {
			if len(want) == 0 || slices.Contains(want, x.Class) {
				out = append(out, x)
			}
		}
	}
	return out
}

// faces returns the faces, or only face index i when i is set.
func (d *Detections) faces(i *int) []Detected {
	if d == nil {
		return nil
	}
	if i == nil {
		return d.Faces
	}
	if *i < 0 || *i >= len(d.Faces) {
		return nil
	}
	return d.Faces[*i : *i+1]
}

// boxOf is the rectangle of a detection box.
func boxOf(a transform.Area) relRect {
	return relRect{a.Left, a.Top, a.Left + a.Width, a.Top + a.Height}
}

// union is the bounding box of dets; ok is false when there are none.
func union(dets []Detected) (r relRect, ok bool) {
	for i, x := range dets {
		b := boxOf(x.Box)
		if i == 0 {
			r = b
			continue
		}
		r = relRect{min(r.x0, b.x0), min(r.y0, b.y0), max(r.x1, b.x1), max(r.y1, b.y1)}
	}
	return r, len(dets) > 0
}

// weighted is the box of objw gravity: centred on the weighted centre of
// the detections with a positive weight (weight × box area), as large as
// their union but no larger than fits the image around that centre.
func weighted(dets []Detected, classes []string, weights []float64) (relRect, bool) {
	w := make(map[string]float64, len(classes))
	for i, c := range classes {
		cs, _ := transform.ResolveClass(c)
		for _, name := range cs {
			if i < len(weights) {
				w[name] = weights[i]
			} else {
				w[name] = 1
			}
		}
	}
	var (
		keep       []Detected
		sx, sy, sw float64
	)
	for _, x := range dets {
		k, ok := w[x.Class]
		if len(classes) == 0 {
			k, ok = 1, true
		}
		if !ok || !(k > 0) {
			continue
		}
		b := boxOf(x.Box)
		a := k * max((b.x1-b.x0)*(b.y1-b.y0), 1e-12)
		sx += a * (b.x0 + b.x1) / 2
		sy += a * (b.y0 + b.y1) / 2
		sw += a
		keep = append(keep, x)
	}
	u, ok := union(keep)
	if !ok || !(sw > 0) {
		return relRect{}, false
	}
	cx, cy := sx/sw, sy/sw
	hw := min((u.x1-u.x0)/2, cx, 1-cx)
	hh := min((u.y1-u.y0)/2, cy, 1-cy)
	return relRect{cx - hw, cy - hh, cx + hw, cy + hh}, true
}

// detectionBox is the box a detection gravity centres on, in fractions of
// the source as displayed; ok is false when nothing was found (the smart
// crop takes over).
func detectionBox(g *transform.Gravity, d *Detections) (relRect, bool) {
	if d == nil {
		return relRect{}, false
	}
	switch g.Type {
	case transform.GravityFace:
		return union(d.faces(g.FaceIndex))
	case transform.GravitySmart:
		return union(d.Faces)
	case transform.GravityObject:
		return union(d.Select(g.Classes))
	case transform.GravityObjectWeighted:
		return weighted(d.Select(g.Classes), g.Classes, g.Weights)
	default:
		return relRect{}, false
	}
}

// placeObjects is the GravityObject placement of box (fractions of the
// image the crop runs on) for objects_position op (nil = centred).
func placeObjects(box relRect, op *transform.ObjectsPosition) Gravity {
	cx, cy := (box.x0+box.x1)/2, (box.y0+box.y1)/2
	g := Gravity{Type: transform.GravityObject, X: cx, Y: cy, AX: 0.5, AY: 0.5}
	switch {
	case op == nil:
	case op.Proportional:
		g.AX, g.AY = cx, cy
	case op.Type == transform.GravityFocusPoint:
		g.AX, g.AY = op.X, op.Y
	default:
		g.OX, g.OY = op.X, op.Y
		switch op.Type {
		case transform.GravityNorth, transform.GravityNorthEast, transform.GravityNorthWest:
			g.Y, g.AY = box.y0, 0
		case transform.GravitySouth, transform.GravitySouthEast, transform.GravitySouthWest:
			g.Y, g.AY, g.OY = box.y1, 1, -op.Y
		default:
		}
		switch op.Type {
		case transform.GravityWest, transform.GravityNorthWest, transform.GravitySouthWest:
			g.X, g.AX = box.x0, 0
		case transform.GravityEast, transform.GravityNorthEast, transform.GravitySouthEast:
			g.X, g.AX, g.OX = box.x1, 1, -op.X
		default:
		}
	}
	return g
}

// frame describes how the source as displayed maps onto the image the
// geometry of a pipeline runs on: the rotate and flip options.
type frame struct {
	rw, rh       int // the source after EXIF orientation and the rotate option
	rotate       int
	flipH, flipV bool
}

func newFrame(p *transform.Pipeline, s *source) frame {
	w, h := s.w, s.h
	if angle, _ := orientation(s.orientation, p.AutoRotate == nil || *p.AutoRotate); angle%180 == 90 {
		w, h = h, w
	}
	f := frame{rw: w, rh: h, rotate: ((p.Rotate%360)/90*90 + 360) % 360, flipH: p.Flip.H, flipV: p.Flip.V}
	if f.rotate%180 == 90 {
		f.rw, f.rh = h, w
	}
	return f
}

// toRotated maps a box of the source as displayed onto the rotated and
// flipped source (the space of crop sizes and gravities).
func (f *frame) toRotated(r relRect) relRect {
	return r.rot(f.rotate).flip(f.flipH, f.flipV)
}

// detectGeo is what detection changes in a pipeline's geometry: the
// placements of the pre-crop and the result crop (nil = the request's
// gravity as resolveGravity reads it).
type detectGeo struct {
	crop, result *Gravity
}

// cropGravityOf is the gravity that places p's pre-crop.
func cropGravityOf(p *transform.Pipeline) *transform.Gravity {
	if p.Crop != nil && p.Crop.Gravity.Type != transform.GravityUnset {
		return &p.Crop.Gravity
	}
	return &p.Gravity
}

// objectsPosition is g's objects_position, or the pipeline gravity's for a
// crop gravity that has none (imgproxy's objects_position is a pipeline
// option).
func objectsPosition(g, pipeline *transform.Gravity) *transform.ObjectsPosition {
	if g.ObjectsPosition != nil {
		return g.ObjectsPosition
	}
	return pipeline.ObjectsPosition
}

// detectGeometry applies s.dets to p (see the file comment). It returns p
// itself when detection changes nothing, else a copy with the crop_objects
// pre-crop in Crop.
func detectGeometry(p *transform.Pipeline, s *source) (*transform.Pipeline, detectGeo) {
	var dg detectGeo
	d := s.dets
	if d == nil {
		return p, dg
	}
	f := newFrame(p, s)
	if crop, ok := objectsCrop(p, d, &f); ok {
		cp := *p
		cp.Crop = crop
		p = &cp
	}

	// The pre-crop rectangle in the rotated source, as emitGeometry places
	// it (a smart crop's position is unknown: its centre).
	cx, cy, cw, ch := 0, 0, f.rw, f.rh
	if c := p.Crop; c != nil {
		cw = minNonZero(cropSize(f.rw, c.Width), f.rw)
		ch = minNonZero(cropSize(f.rh, c.Height), f.rh)
		cg := cropGravityOf(p)
		var g Gravity
		if box, ok := detectionBox(cg, d); ok && cg.Type.IsDetection() {
			g = placeObjects(f.toRotated(box), objectsPosition(cg, &p.Gravity))
			dg.crop = &g
		} else {
			g, _ = resolveGravity(cg)
		}
		if cw < f.rw || ch < f.rh {
			cx, cy = position(f.rw, f.rh, cw, ch, g, 1)
		}
	}
	if box, ok := detectionBox(&p.Gravity, d); ok {
		r := f.toRotated(box).crop(f.rw, f.rh, cx, cy, cw, ch)
		r = relRect{clampUnit(r.x0), clampUnit(r.y0), clampUnit(r.x1), clampUnit(r.y1)}
		g := placeObjects(r, p.Gravity.ObjectsPosition)
		dg.result = &g
	}
	return p, dg
}

// objectsCrop is the pre-crop of crop_objects (or of face gravity with a
// face pad) in the rotated source; ok is false when there is none or
// nothing was detected.
func objectsCrop(p *transform.Pipeline, d *Detections, f *frame) (*transform.Crop, bool) {
	var (
		dets    []Detected
		scale   float64
		padding int
	)
	switch co := p.CropObjects; {
	case co != nil:
		dets = d.Select(co.Classes)
		if i := co.Index; i != nil {
			if *i < 0 || *i >= len(dets) {
				return nil, false
			}
			dets = dets[*i : *i+1]
		}
		scale, padding = co.Scale, co.Padding
	case p.Gravity.Type == transform.GravityFace && p.Gravity.FacePad > 0:
		dets = d.faces(p.Gravity.FaceIndex)
		scale = p.Gravity.FacePad
	default:
		return nil, false
	}
	box, ok := union(dets)
	if !ok {
		return nil, false
	}
	if !(scale > 0) || scale > transform.MaxFactor {
		scale = 1
	}
	r := f.toRotated(box)
	rw, rh := float64(f.rw), float64(f.rh)
	// Pixels of the rotated source: scale around the centre, then pad.
	cx, cy := (r.x0+r.x1)/2*rw, (r.y0+r.y1)/2*rh
	bw := (r.x1-r.x0)*rw*scale + 2*float64(padding)
	bh := (r.y1-r.y0)*rh*scale + 2*float64(padding)
	if ar, ok := fillAspect(p, f); ok {
		// fill and fill-down: grow the short side to the result's aspect
		// ratio, so the resize does not cut into the objects.
		if bw/bh < ar {
			bw = bh * ar
		} else {
			bh = bw / ar
		}
	}
	w := min(max(int(math.Round(bw)), 1), f.rw)
	h := min(max(int(math.Round(bh)), 1), f.rh)
	// Keep the box inside the image: its centre moves in, not its size.
	cx = min(max(cx, float64(w)/2), rw-float64(w)/2)
	cy = min(max(cy, float64(h)/2), rh-float64(h)/2)
	return &transform.Crop{
		Width:  transform.Dim{Px: w},
		Height: transform.Dim{Px: h},
		Gravity: transform.Gravity{
			Type: transform.GravityFocusPoint, FocusX: clampUnit(cx / rw), FocusY: clampUnit(cy / rh),
		},
	}, true
}

// fillAspect is the width/height ratio of a fill or fill-down result with
// both sides set.
func fillAspect(p *transform.Pipeline, f *frame) (float64, bool) {
	rs := &p.Resize
	if rs.Type != transform.ResizeFill && rs.Type != transform.ResizeFillDown {
		return 0, false
	}
	w, h := dim(rs.Width, f.rw), dim(rs.Height, f.rh)
	if ar := rs.AspectRatio; ar > 0 && !math.IsInf(ar, 0) {
		switch {
		case w > 0 && h == 0:
			h = max(1, roundInt(float64(w)/ar))
		case h > 0 && w == 0:
			w = max(1, roundInt(float64(h)*ar))
		}
	}
	if w <= 0 || h <= 0 {
		return 0, false
	}
	return float64(w) / float64(h), true
}

// DetectionBlur blurs or pixelates the boxes of detected objects (imgproxy
// blur_detections, imgix face-blur and face-pixel, Cloudinary
// e_blur_faces and e_pixelate_faces), after BlurAreas.
type DetectionBlur struct {
	Sigma    float64 // Gaussian sigma, when Pixelate ≤ 1
	Pixelate int     // block size in pixels; > 1 pixelates instead of blurring
	// Areas are fractions of the frame at the FilterStep (mapped through
	// the geometry like BlurAreas), within 0..1.
	Areas []transform.Area
}

// detectionBlur is the DetectionBlur of p, nil when there is nothing to
// blur.
func detectionBlur(p *transform.Pipeline, b *builder) *DetectionBlur {
	bd := p.BlurDetections
	if bd == nil || b.dets == nil {
		return nil
	}
	db := &DetectionBlur{Sigma: bd.Sigma, Pixelate: bd.Pixelate}
	if db.Pixelate <= 1 {
		db.Pixelate = 0
		if !(db.Sigma > 0) {
			db.Sigma = DefaultDetectionSigma
		}
	}
	for _, x := range b.dets.Select(bd.Classes) {
		if a, ok := mapArea(x.Box, b); ok {
			db.Areas = append(db.Areas, a)
		}
	}
	if len(db.Areas) == 0 {
		return nil
	}
	return db
}

// DrawDetectionsStep draws the boxes of detected objects with their class
// and score (imgproxy draw_detections), per frame, after the colour
// adjustments.
type DrawDetectionsStep struct {
	Boxes []DetectionBox
}

// DetectionBox is one box to draw: fractions of the frame, within 0..1.
type DetectionBox struct {
	Area  transform.Area
	Class string
	Score float64
}

func (DrawDetectionsStep) step() {}

// drawDetections is the DrawDetectionsStep of p; ok is false when nothing
// is drawn.
func drawDetections(p *transform.Pipeline, b *builder) (DrawDetectionsStep, bool) {
	dd := p.DrawDetections
	if dd == nil || b.dets == nil {
		return DrawDetectionsStep{}, false
	}
	var st DrawDetectionsStep
	for _, x := range b.dets.Select(dd.Classes) {
		if a, ok := mapArea(x.Box, b); ok {
			st.Boxes = append(st.Boxes, DetectionBox{Area: a, Class: x.Class, Score: x.Score})
		}
	}
	return st, len(st.Boxes) > 0
}
