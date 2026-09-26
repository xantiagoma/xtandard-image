package server

import (
	"encoding/json"
	"math"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// thumborMeta is thumbor's /meta response (thumbor/engines/json_engine.py
// JSONEngine.read): the source, the crop/resize/flip operations the
// request performs, the target size and the focal points. Field and key
// order follow thumbor.
type thumborMeta struct {
	Thumbor thumborMetaBody `json:"thumbor"`
}

type thumborMetaBody struct {
	Source      thumborSource  `json:"source"`
	Operations  []thumborOp    `json:"operations"`
	Target      thumborSize    `json:"target"`
	FocalPoints []thumborFocal `json:"focal_points,omitempty"`
}

type thumborSource struct {
	URL        string `json:"url"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	FrameCount int    `json:"frameCount"`
}

type thumborSize struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// thumborOp is one of {"type":"resize","width","height"},
// {"type":"crop","left","top","right","bottom"} and
// {"type":"flip_horizontally"} / {"type":"flip_vertically"}.
type thumborOp struct {
	Type   string `json:"type"`
	Width  *int   `json:"width,omitempty"`
	Height *int   `json:"height,omitempty"`
	Left   *int   `json:"left,omitempty"`
	Top    *int   `json:"top,omitempty"`
	Right  *int   `json:"right,omitempty"`
	Bottom *int   `json:"bottom,omitempty"`
}

// thumborFocal is thumbor's FocalPoint.to_dict.
type thumborFocal struct {
	X      int     `json:"x"`
	Y      int     `json:"y"`
	Z      float64 `json:"z"`
	Height int     `json:"height"`
	Width  int     `json:"width"`
	Origin string  `json:"origin"`
}

// thumborRect is a crop in thumbor coordinates.
type thumborRect struct{ left, top, right, bottom int }

// thumborMetaBody renders the thumbor meta response for req (an info
// request with InfoSchemaThumbor). The target size and the operations
// come from the plan the request would get, so they are what processing
// produces. Coordinates are those of the source as displayed (EXIF
// orientation applied, as xtandard-image processes it). Best effort
// against thumbor: rotations, fills, trims and filters are not listed
// (thumbor's JSONEngine does not list them either), and the JSONP
// callback is not supported.
func (s *Server) thumborMetaBody(req *transform.Request, si *SourceInfo) ([]byte, error) {
	meta := thumborPlanMeta(si)
	meta.Detections = si.Detections
	pl, err := plan.Build(req, meta, s.cfg.Info.PlanLimits)
	if err != nil {
		return nil, err
	}
	var p0 transform.Pipeline
	if len(req.Pipelines) > 0 {
		p0 = req.Pipelines[0]
	}
	ow, oh := orientedSize(&si.Meta)
	ops, manual := thumborOperations(pl, &meta, &p0)
	last := pl
	if n := len(pl.Chained); n > 0 {
		last = pl.Chained[n-1]
	}
	out := thumborMeta{Thumbor: thumborMetaBody{
		Source:      thumborSource{URL: req.Source.Raw, Width: ow, Height: oh, FrameCount: max(si.Pages, si.Meta.Frames, 1)},
		Operations:  ops,
		Target:      thumborSize{Width: last.Output.Width, Height: last.Output.Height},
		FocalPoints: thumborFocalPoints(&p0.Gravity, manual, ow, oh),
	}}
	if faces := thumborFaces(&p0.Gravity, manual, si.Detections, ow, oh); faces != nil {
		out.Thumbor.FocalPoints = faces
	}
	b, err := json.Marshal(&out)
	if err != nil {
		return nil, errs.Wrapf(errs.KindInternal, err, "server: encode thumbor meta")
	}
	return b, nil
}

// thumborPlanMeta is the planner's view of the inspected source.
func thumborPlanMeta(si *SourceInfo) plan.SourceMeta {
	return plan.SourceMeta{
		Width:       si.Meta.Width,
		Height:      si.Meta.Height,
		Frames:      max(si.Meta.Frames, si.Pages),
		HasAlpha:    si.Meta.HasAlpha,
		Orientation: si.Meta.Orientation,
		IsAnimated:  si.Meta.IsAnimated,
		Format:      si.Format,
		IsVector:    si.Format == "svg" || si.Format == "pdf",
	}
}

// thumborOperations lists the crops, resizes and flips of the first plan
// in thumbor's terms. Steps before the EXIF orientation work on the
// loaded image as stored: their rectangles are mapped to the displayed
// source (and, before any resize, scaled from the shrunk-on-load size to
// source pixels, so on the shrink-on-load grid); shrink-on-load itself is
// listed as a resize. manual is the pre-crop in source pixels, if any.
func thumborOperations(pl *plan.Plan, meta *plan.SourceMeta, p *transform.Pipeline) (ops []thumborOp, manual *thumborRect) {
	ops = []thumborOp{}
	angle, flip := exifTransform(meta.Orientation, p.AutoRotate == nil || *p.AutoRotate)
	frameH := meta.Height
	if meta.PageHeight > 0 {
		frameH = meta.PageHeight
	}
	sx, sy := 1.0, 1.0
	if pl.Load.Width > 0 && pl.Load.Height > 0 {
		sx, sy = float64(meta.Width)/float64(pl.Load.Width), float64(frameH)/float64(pl.Load.Height)
	}
	curW, curH := pl.Load.Width, pl.Load.Height
	resized := false
	crop := func(g *plan.CropGeometry, pre bool) {
		x, y, w, h, fw, fh := g.X, g.Y, g.W, g.H, curW, curH
		if !resized {
			x, y, w, h = roundScale(x, sx), roundScale(y, sy), roundScale(w, sx), roundScale(h, sy)
			fw, fh = meta.Width, frameH
		}
		if angle != 0 || flip {
			x, y, w, h = orientRect(x, y, w, h, fw, fh, angle, flip)
		}
		r := thumborRect{x, y, x + w, y + h}
		ops = append(ops, thumborOp{Type: "crop", Left: &r.left, Top: &r.top, Right: &r.right, Bottom: &r.bottom})
		if pre && manual == nil && !resized {
			manual = &r
		}
		curW, curH = g.W, g.H
	}
	// Shrink-on-load is a resize in thumbor's terms: list it before the
	// first operation that works on the shrunk image.
	shrunk := pl.Load.Width != meta.Width || pl.Load.Height != frameH
	shrink := func() {
		if resized || !shrunk {
			return
		}
		w, h := curW, curH
		if angle%180 == 90 {
			w, h = h, w
		}
		ops = append(ops, thumborOp{Type: "resize", Width: &w, Height: &h})
		resized = true
	}
	for _, st := range pl.Steps {
		switch v := st.(type) {
		case plan.PreCropStep:
			crop(&v.CropGeometry, true)
		case plan.CropStep:
			shrink()
			crop(&v.CropGeometry, false)
		case plan.ResizeStep:
			w, h := v.W, v.H
			if angle%180 == 90 {
				w, h = h, w
			}
			ops = append(ops, thumborOp{Type: "resize", Width: &w, Height: &h})
			curW, curH, resized = v.W, v.H, true
		case plan.RotateStep:
			if angle != 0 { // the EXIF rotation comes first
				angle = 0
			}
			if v.Angle%180 == 90 {
				curW, curH = curH, curW
			}
		case plan.FlipStep:
			if angle == 0 && flip { // the EXIF flip follows its rotation
				flip = false
			}
		default:
		}
	}
	shrink()
	// thumbor flips last and lists only the requested flips.
	if p.Flip.H {
		ops = append(ops, thumborOp{Type: "flip_horizontally"})
	}
	if p.Flip.V {
		ops = append(ops, thumborOp{Type: "flip_vertically"})
	}
	return ops, manual
}

// thumborFocalPoints is thumbor's Transformer.adjust_focal_points: the
// explicit (pixel) focus moved into the manual crop, or else one
// "alignment" point from the gravity on the displayed source (or the
// manual crop).
func thumborFocalPoints(g *transform.Gravity, manual *thumborRect, w, h int) []thumborFocal {
	if r := g.FocusPx; r != nil {
		x, y := r.Center()
		fw, fh := r.X2-r.X1, r.Y2-r.Y1
		pt := thumborFocal{X: x, Y: y, Z: float64(fw * fh), Width: fw, Height: fh, Origin: "Explicit"}
		if fw == 0 || fh == 0 {
			pt.Z, pt.Width, pt.Height = 1, 1, 1
		}
		switch {
		case manual == nil:
			return []thumborFocal{pt}
		case x >= manual.left && x <= manual.right && y >= manual.top && y <= manual.bottom:
			pt.X, pt.Y = x-manual.left, y-manual.top
			return []thumborFocal{pt}
		}
	}
	if manual != nil {
		w, h = manual.right-manual.left, manual.bottom-manual.top
	}
	fx, fy := 0.5, 0.5
	switch g.Type {
	case transform.GravityFocusPoint:
		if g.FocusPx == nil {
			fx, fy = g.FocusX, g.FocusY
		}
	case transform.GravityNorth:
		fy = 0
	case transform.GravitySouth:
		fy = 1
	case transform.GravityEast:
		fx = 1
	case transform.GravityWest:
		fx = 0
	case transform.GravityNorthEast:
		fx, fy = 1, 0
	case transform.GravityNorthWest:
		fx, fy = 0, 0
	case transform.GravitySouthEast:
		fx, fy = 1, 1
	case transform.GravitySouthWest:
		fx, fy = 0, 1
	default:
	}
	return []thumborFocal{{
		X: int(float64(w) * fx), Y: int(float64(h) * fy), Z: 1, Width: 1, Height: 1, Origin: "alignment",
	}}
}

// thumborFaceHair is thumbor's face detector HAIR_OFFSET: the box is
// raised by 12 % of its height to include the hair.
const thumborFaceHair = 0.12

// thumborFaces are the focal points of smart cropping on detected faces
// (thumbor's face detector: FocalPoint.from_square with origin "Face
// Detection" and the hair offset), in the displayed source or the manual
// crop; nil unless the gravity is smart and faces were found.
func thumborFaces(g *transform.Gravity, manual *thumborRect, dets *plan.Detections, w, h int) []thumborFocal {
	if g.Type != transform.GravitySmart || dets == nil || len(dets.Faces) == 0 {
		return nil
	}
	var out []thumborFocal
	for _, f := range dets.Faces {
		left := int(f.Box.Left * float64(w))
		top := int(f.Box.Top * float64(h))
		fw, fh := int(f.Box.Width*float64(w)), int(f.Box.Height*float64(h))
		if off := float64(fh) * thumborFaceHair; float64(top)-off >= 0 {
			top = int(float64(top) - off)
		}
		if manual != nil {
			left, top = left-manual.left, top-manual.top
		}
		out = append(out, thumborFocal{
			X: left + fw/2, Y: top + fh/2, Z: float64(fw * fh), Width: fw, Height: fh, Origin: "Face Detection",
		})
	}
	return out
}

// exifTransform is the clockwise rotation and the horizontal flip that
// display an EXIF orientation (the planner's).
func exifTransform(o int, autoRotate bool) (angle int, flip bool) {
	if !autoRotate {
		return 0, false
	}
	switch o {
	case 3, 4:
		angle = 180
	case 5, 6:
		angle = 90
	case 7, 8:
		angle = 270
	}
	return angle, o == 2 || o == 4 || o == 5 || o == 7
}

// orientRect maps a rectangle of a w×h image as stored to the displayed
// image: rotated clockwise by angle, then flipped horizontally.
func orientRect(x, y, rw, rh, w, h, angle int, flip bool) (int, int, int, int) {
	switch angle {
	case 90:
		x, y, rw, rh, w = h-(y+rh), x, rh, rw, h
	case 180:
		x, y = w-(x+rw), h-(y+rh)
	case 270:
		x, y, rw, rh, w = y, w-(x+rw), rh, rw, h
	}
	if flip {
		x = w - (x + rw)
	}
	return x, y, rw, rh
}

func roundScale(v int, s float64) int { return int(math.Round(float64(v) * s)) }
