package exec

import (
	"context"
	"image"

	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// Geometry extras (WP-60): arbitrary rotation, shape masks, borders and
// rounded corners. Each runs per frame; see plan/geometry_extras.go for
// their place in the step order.

// coverageKey identifies a rasterised outline: the step (mask or radius)
// on a frame size. Every frame of an animation has the same size, so the
// mask is rasterised once per request.
type coverageKey struct {
	step plan.Step
	w, h int
}

// coverageOf rasterises o on w×h, cached per step and size.
func (r *runner) coverageOf(key coverageKey, o func() plan.Outline) []byte {
	if m, ok := r.coverage[key]; ok {
		return m
	}
	m := coverage(o(), key.w, key.h)
	if r.coverage == nil {
		r.coverage = make(map[coverageKey][]byte, 1)
	}
	r.coverage[key] = m
	return m
}

// greyImage wraps w×h one-band 8-bit pixels as a B_W libvips image.
func (r *runner) greyImage(ctx context.Context, pix []byte, w, h int) (*vips.Image, error) {
	img, err := r.a.Add(vips.NewFromMemory(ctx, pix, w, h, 1, vips.BandFormatUchar))
	if err != nil {
		return nil, err
	}
	return r.a.Add(img.Copy(ctx, vips.CopyOptions{Interpretation: vips.InterpretationBW}))
}

// uchar brings img to 8-bit samples (the working space is 8-bit sRGB or
// grey; a premultiplied resize may leave another format).
func (r *runner) uchar(ctx context.Context, img *vips.Image) (*vips.Image, error) {
	if img.BandFormat() == vips.BandFormatUchar {
		return img, nil
	}
	return r.a.Add(img.CastRound(ctx, vips.BandFormatUchar))
}

// forColour converts a grey img to sRGB when c is not grey, so a fill of
// that colour keeps its hue.
func (r *runner) forColour(ctx context.Context, img *vips.Image, c *transform.Color) (*vips.Image, error) {
	if c == nil || (c.R == c.G && c.G == c.B) || img.Interpretation() != vips.InterpretationBW {
		return img, nil
	}
	return r.a.Add(img.Colourspace(ctx, vips.InterpretationSRGB))
}

// fill is c as libvips band values for img: the colour bands (one for
// grey), then the alpha when img has one.
func fill(img *vips.Image, c transform.Color) []float64 {
	vals := []float64{float64(c.R), float64(c.G), float64(c.B)}
	n := img.Bands()
	if img.HasAlpha() {
		n--
	}
	if n < 3 {
		vals = vals[:1]
	}
	if img.HasAlpha() {
		vals = append(vals, float64(c.A))
	}
	return vals
}

// applyCoverage multiplies img's alpha by the 8-bit mask m (w×h, the size
// of img); an image without alpha gets m as its alpha band.
func (r *runner) applyCoverage(ctx context.Context, img *vips.Image, m []byte) (*vips.Image, error) {
	mask, err := r.greyImage(ctx, m, img.Width(), img.Height())
	if err != nil {
		return nil, err
	}
	if !img.HasAlpha() {
		return r.a.Add(vips.Bandjoin(ctx, []*vips.Image{img, mask}))
	}
	// dest-in keeps img where the overlay is opaque: alpha × m.
	overlay, err := r.a.Add(vips.Bandjoin(ctx, []*vips.Image{mask, mask}))
	if err != nil {
		return nil, err
	}
	out, err := r.a.Add(img.Composite2(ctx, overlay, vips.BlendDestIn, 0, 0))
	if err != nil {
		return nil, err
	}
	return r.uchar(ctx, out)
}

// rotateAny rotates img clockwise by s.Angle with libvips similarity
// (bilinear): the canvas grows to the rotated bounding box, filled with
// s.Background or transparent. Images with alpha are rotated
// premultiplied, so transparent pixels do not darken the edges.
func (r *runner) rotateAny(ctx context.Context, img *vips.Image, s *plan.RotateAnyStep) (*vips.Image, error) {
	var bg transform.Color // transparent
	if s.Background != nil {
		bg = *s.Background
	}
	img, err := r.uchar(ctx, img)
	if err != nil {
		return nil, err
	}
	if img, err = r.forColour(ctx, img, s.Background); err != nil {
		return nil, err
	}
	if bg.A < 255 && !img.HasAlpha() {
		if img, err = r.a.Add(img.AddAlpha(ctx)); err != nil {
			return nil, err
		}
	}
	vals := fill(img, bg)
	if !img.HasAlpha() {
		return r.a.Add(img.Similarity(ctx, 1, s.Angle, vals))
	}
	a := float64(bg.A) / 255
	for i := range len(vals) - 1 {
		vals[i] *= a
	}
	pm, err := r.a.Add(img.Premultiply(ctx))
	if err != nil {
		return nil, err
	}
	if pm, err = r.a.Add(pm.Similarity(ctx, 1, s.Angle, vals)); err != nil {
		return nil, err
	}
	if pm, err = r.a.Add(pm.Unpremultiply(ctx)); err != nil {
		return nil, err
	}
	return r.uchar(ctx, pm)
}

// mask cuts img to the step's shape like wsrv: the alpha is multiplied by
// the anti-aliased shape (skipped when an opaque background replaces the
// outside of an opaque image anyway), then the background, if any, is laid
// over the outside, and Trim crops to the shape's bounding box.
func (r *runner) mask(ctx context.Context, img *vips.Image, s *plan.MaskStep) (*vips.Image, error) {
	w, h := img.Width(), img.Height()
	m := r.coverageOf(coverageKey{step: *s, w: w, h: h}, func() plan.Outline { return s.Outline(w, h) })
	img, err := r.uchar(ctx, img)
	if err != nil {
		return nil, err
	}
	bg := s.Background
	if bg == nil || bg.A < 255 || img.HasAlpha() {
		if img, err = r.applyCoverage(ctx, img, m); err != nil {
			return nil, err
		}
	}
	if bg != nil && bg.A > 0 {
		// The frame's alpha: (255 − m) × A / 255.
		alpha, err := r.greyImage(ctx, m, w, h)
		if err != nil {
			return nil, err
		}
		k := float64(bg.A) / 255
		if alpha, err = r.a.Add(alpha.Linear(ctx, []float64{-k}, []float64{255 * k})); err != nil {
			return nil, err
		}
		if alpha, err = r.uchar(ctx, alpha); err != nil {
			return nil, err
		}
		colour, err := r.solid(ctx, w, h, []float64{float64(bg.R), float64(bg.G), float64(bg.B)})
		if err != nil {
			return nil, err
		}
		frame, err := r.a.Add(vips.Bandjoin(ctx, []*vips.Image{colour, alpha}))
		if err != nil {
			return nil, err
		}
		if img, err = r.over(ctx, img, frame); err != nil {
			return nil, err
		}
	}
	if x, y, tw, th, ok := s.TrimRect(w, h); ok {
		return r.a.Add(img.ExtractArea(ctx, image.Rect(x, y, x+tw, y+th)))
	}
	return img, nil
}

// border grows the canvas by the step's widths, filled with its colour
// (with its alpha: an alpha band is added when the colour is translucent).
func (r *runner) border(ctx context.Context, img *vips.Image, s *plan.BorderStep) (*vips.Image, error) {
	img, err := r.uchar(ctx, img)
	if err != nil {
		return nil, err
	}
	if img, err = r.forColour(ctx, img, &s.Color); err != nil {
		return nil, err
	}
	if s.Color.A < 255 && !img.HasAlpha() {
		if img, err = r.a.Add(img.AddAlpha(ctx)); err != nil {
			return nil, err
		}
	}
	w := img.Width() + s.Left + s.Right
	h := img.Height() + s.Top + s.Bottom
	return r.a.Add(img.Embed(ctx, s.Left, s.Top, w, h, vips.ExtendBackground, fill(img, s.Color)))
}

// radius rounds the corners: the alpha is multiplied by the anti-aliased
// rounded rectangle (an alpha band is added). A later FlattenStep fills the
// corners with the background for formats without alpha.
func (r *runner) radius(ctx context.Context, img *vips.Image, s *plan.RadiusStep) (*vips.Image, error) {
	w, h := img.Width(), img.Height()
	m := r.coverageOf(coverageKey{step: *s, w: w, h: h}, func() plan.Outline { return s.Outline(w, h) })
	img, err := r.uchar(ctx, img)
	if err != nil {
		return nil, err
	}
	return r.applyCoverage(ctx, img, m)
}
