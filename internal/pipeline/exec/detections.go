package exec

import (
	"context"
	"html"
	"image"
	"math"
	"strconv"

	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// The detection filters (WP-73): blur or pixelate detected objects
// (FilterStep.Detections) and draw their boxes (DrawDetectionsStep). The
// planner already mapped the boxes onto the frame.

// detectionBlur blurs (like blur areas) or pixelates the areas of db.
// Pixelating averages db.Pixelate-sized blocks aligned on each area's
// top-left corner and enlarges them back with nearest neighbour, inside
// the area only.
func (r *runner) detectionBlur(ctx context.Context, img *vips.Image, db *plan.DetectionBlur) (*vips.Image, error) {
	if db.Pixelate <= 1 {
		if !(db.Sigma > 0) {
			return img, nil
		}
		return r.blurAreas(ctx, img, &plan.BlurAreas{Sigma: db.Sigma, Areas: db.Areas})
	}
	src := img
	w, h := img.Width(), img.Height()
	for _, a := range db.Areas {
		rc, ok := areaRect(a, w, h)
		if !ok {
			continue
		}
		sub, err := r.a.Add(src.ExtractArea(ctx, rc))
		if err != nil {
			return nil, err
		}
		sw, sh := rc.Dx(), rc.Dy()
		p := filterParams{Pixelate: min(db.Pixelate, max(sw, sh)), PadW: sw, PadH: sh}
		if p.Pixelate > 1 {
			p.PadW, p.PadH = ceilMultiple(sw, p.Pixelate), ceilMultiple(sh, p.Pixelate)
			if sub, err = r.pixelate(ctx, sub, &p, sw, sh); err != nil {
				return nil, err
			}
		}
		if img, err = r.a.Add(img.Insert(ctx, sub, rc.Min.X, rc.Min.Y)); err != nil {
			return nil, err
		}
	}
	return img, nil
}

// detectionColor is the box colour of a class: a fixed palette indexed by
// the class id, so a class always has the same colour.
func detectionColor(class string) transform.Color {
	palette := [...]transform.Color{
		{R: 0x00, G: 0xe0, B: 0x5a, A: 255}, // face
		{R: 0xff, G: 0x38, B: 0x38, A: 255},
		{R: 0x2c, G: 0x99, B: 0xff, A: 255},
		{R: 0xff, G: 0xb2, B: 0x1d, A: 255},
		{R: 0xcf, G: 0x3c, B: 0xff, A: 255},
		{R: 0x00, G: 0xd4, B: 0xd4, A: 255},
		{R: 0xff, G: 0x70, B: 0x1f, A: 255},
		{R: 0x86, G: 0xe0, B: 0x00, A: 255},
		{R: 0xff, G: 0x5f, B: 0xa2, A: 255},
		{R: 0x52, G: 0x4c, B: 0xff, A: 255},
	}
	i := 0
	if class != transform.FaceClass {
		i = 1 + max(transform.ObjectClassID(class), 0)%(len(palette)-1)
	}
	return palette[i]
}

// detectionLabel is the text drawn above a box: the class and the score
// in percent.
func detectionLabel(b *plan.DetectionBox) string {
	return b.Class + " " + strconv.Itoa(int(math.Round(b.Score*100))) + "%"
}

// drawDetections draws each box's outline and a label with its class and
// score, in the class colour. The line is about 1/200 of the shorter
// side (at least 2 pixels); labels sit above the box, or inside it at
// the top of the frame. Grey images become sRGB, the band format and the
// alpha (opaque boxes) are kept.
func (r *runner) drawDetections(ctx context.Context, img *vips.Image, s *plan.DrawDetectionsStep) (*vips.Image, error) {
	img, err := r.uchar(ctx, img)
	if err != nil {
		return nil, err
	}
	if img.Interpretation() == vips.InterpretationBW {
		if img, err = r.a.Add(img.Colourspace(ctx, vips.InterpretationSRGB)); err != nil {
			return nil, err
		}
	}
	w, h := img.Width(), img.Height()
	bounds := image.Rect(0, 0, w, h)
	t := max(2, int(math.Round(float64(min(w, h))/200)))
	size := min(max(11, min(w, h)/40), 48)
	for i := range s.Boxes {
		if err := ctxErr(ctx); err != nil {
			return nil, err
		}
		b := &s.Boxes[i]
		rc, ok := areaRect(b.Area, w, h)
		if !ok {
			continue
		}
		c := detectionColor(b.Class)
		vals := fill(img, c)
		edges := [...]image.Rectangle{
			image.Rect(rc.Min.X, rc.Min.Y, rc.Max.X, rc.Min.Y+t),
			image.Rect(rc.Min.X, rc.Max.Y-t, rc.Max.X, rc.Max.Y),
			image.Rect(rc.Min.X, rc.Min.Y, rc.Min.X+t, rc.Max.Y),
			image.Rect(rc.Max.X-t, rc.Min.Y, rc.Max.X, rc.Max.Y),
		}
		for _, e := range edges {
			if e = e.Intersect(bounds).Intersect(rc); e.Empty() {
				continue
			}
			line, err := r.band(ctx, e.Dx(), e.Dy(), vals)
			if err != nil {
				return nil, err
			}
			if img, err = r.a.Add(img.Insert(ctx, line, e.Min.X, e.Min.Y)); err != nil {
				return nil, err
			}
		}
		if img, err = r.drawLabel(ctx, img, b, c, rc, size); err != nil {
			return nil, err
		}
	}
	return img, nil
}

// band is a w×h uchar image of the constant pixel vals (any band count),
// sRGB when it has colour.
func (r *runner) band(ctx context.Context, w, h int, vals []float64) (*vips.Image, error) {
	if len(vals) >= 3 {
		return r.solid(ctx, w, h, vals)
	}
	img, err := r.a.Add(vips.Black(ctx, w, h, 1))
	if err != nil {
		return nil, err
	}
	if img, err = r.a.Add(img.Linear(ctx, make([]float64, len(vals)), vals)); err != nil {
		return nil, err
	}
	return r.a.Add(img.CastRound(ctx, vips.BandFormatUchar))
}

// drawLabel composites the label of box b (white text on the class
// colour) above rc, or inside it when there is no room above.
func (r *runner) drawLabel(ctx context.Context, img *vips.Image, b *plan.DetectionBox, c transform.Color, rc image.Rectangle, size int) (*vips.Image, error) {
	text, _, err := vips.Text(ctx, &vips.TextOptions{
		Markup: `<span foreground="white">` + html.EscapeString(detectionLabel(b)) + `</span>`,
		Font:   "sans " + strconv.Itoa(size),
		DPI:    72,
	})
	if text, err = r.a.Add(text, err); err != nil {
		return nil, err
	}
	pad := max(2, size/5)
	lw, lh := text.Width()+2*pad, text.Height()+2*pad
	bg, err := r.solid(ctx, lw, lh, []float64{float64(c.R), float64(c.G), float64(c.B), 255})
	if err != nil {
		return nil, err
	}
	label, err := r.a.Add(bg.Composite2(ctx, text, vips.BlendOver, pad, pad))
	if err != nil {
		return nil, err
	}
	x := max(0, min(rc.Min.X, img.Width()-lw))
	y := rc.Min.Y - lh
	if y < 0 {
		y = max(0, rc.Min.Y)
	}
	return r.composite(ctx, img, label, x, y)
}
