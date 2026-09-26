package exec

import (
	"context"

	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// colourAdjust runs a ColourAdjustStep (colour_math.go) on 8-bit sRGB:
// grey images become sRGB, since most effects add colour, and stay sRGB.
// The alpha band is kept apart from the colour maths; overlays are
// composited over it premultiplied. Animations reach it once per frame.
func (r *runner) colourAdjust(ctx context.Context, img *vips.Image, s *plan.ColourAdjustStep) (*vips.Image, error) {
	ops := colourOps(s, img.HasAlpha())
	if len(ops) == 0 {
		return img, nil
	}
	var err error
	if img.Interpretation() != vips.InterpretationSRGB {
		if img, err = r.a.Add(img.Colourspace(ctx, vips.InterpretationSRGB)); err != nil {
			return nil, err
		}
	}
	if img.BandFormat() != vips.BandFormatUchar {
		if img, err = r.a.Add(img.CastRound(ctx, vips.BandFormatUchar)); err != nil {
			return nil, err
		}
	}
	for i := range ops {
		if err := ctxErr(ctx); err != nil {
			return nil, err
		}
		if img, err = r.colourOp(ctx, img, &ops[i]); err != nil {
			return nil, err
		}
	}
	return img, nil
}

func (r *runner) colourOp(ctx context.Context, img *vips.Image, op *colourOp) (*vips.Image, error) {
	switch op.kind {
	case opLUT:
		return r.mapLUT(ctx, img, op.lut)
	case opMatrix:
		m := op.m[:]
		if img.HasAlpha() {
			m = []float64{
				op.m[0], op.m[1], op.m[2], 0,
				op.m[3], op.m[4], op.m[5], 0,
				op.m[6], op.m[7], op.m[8], 0,
				0, 0, 0, 1,
			}
		}
		return r.a.Add(img.Recomb(ctx, m, img.Bands()))
	case opPalette:
		return r.palette(ctx, img, op.pal, op.mix)
	case opOverlay:
		c := op.colour
		over, err := r.solid(ctx, img.Width(), img.Height(), []float64{float64(c.R), float64(c.G), float64(c.B), 255 * op.mix})
		if err != nil {
			return nil, err
		}
		return r.over(ctx, img, over)
	case opGradient:
		return r.gradient(ctx, img, op.grad)
	case opOpacity:
		return r.multiplyAlpha(ctx, img, op.amount)
	default:
		return img, nil
	}
}

// mapLUT maps the colour bands through lut; the alpha band maps to itself.
func (r *runner) mapLUT(ctx context.Context, img *vips.Image, lut *[3][256]uint8) (*vips.Image, error) {
	bands := img.Bands()
	table := make([]byte, 256*bands)
	for v := range 256 {
		for c := range bands {
			if c < 3 {
				table[v*bands+c] = lut[c][v]
			} else {
				table[v*bands+c] = byte(v)
			}
		}
	}
	l, err := r.a.Add(vips.NewFromMemory(ctx, table, 256, 1, bands, vips.BandFormatUchar))
	if err != nil {
		return nil, err
	}
	return r.a.Add(img.MapLUT(ctx, l))
}

// palette replaces the colour of every pixel by pal[luma], blended with the
// original colour by mix (1 replaces it).
func (r *runner) palette(ctx context.Context, img *vips.Image, pal *[256][3]uint8, mix float64) (*vips.Image, error) {
	colour, alpha, err := r.splitAlpha(ctx, img)
	if err != nil {
		return nil, err
	}
	grey, err := r.a.Add(colour.Recomb(ctx, []float64{lumaR, lumaG, lumaB}, 1))
	if err != nil {
		return nil, err
	}
	table := make([]byte, 256*3)
	for v := range 256 {
		copy(table[3*v:], pal[v][:])
	}
	l, err := r.a.Add(vips.NewFromMemory(ctx, table, 256, 1, 3, vips.BandFormatUchar))
	if err != nil {
		return nil, err
	}
	mapped, err := r.a.Add(grey.MapLUT(ctx, l))
	if err != nil {
		return nil, err
	}
	if mapped, err = r.a.Add(mapped.Copy(ctx, vips.CopyOptions{Interpretation: vips.InterpretationSRGB})); err != nil {
		return nil, err
	}
	if mix < 1 {
		if mapped, err = r.a.Add(colour.Mix(ctx, mapped, mix)); err != nil {
			return nil, err
		}
	}
	if alpha == nil {
		return mapped, nil
	}
	return r.a.Add(vips.Bandjoin(ctx, []*vips.Image{mapped, alpha}))
}

// gradient composites the linear gradient of g over img: transparent at
// Start, the colour at Opacity by Stop, along the direction.
func (r *runner) gradient(ctx context.Context, img *vips.Image, g *transform.Gradient) (*vips.Image, error) {
	w, h := img.Width(), img.Height()
	ax, ay, b := gradientRamp(g, w, h)
	mask, err := r.a.Add(vips.GradientMask(ctx, w, h, ax, ay, b, min(g.Opacity, 1)))
	if err != nil {
		return nil, err
	}
	colour, err := r.solid(ctx, w, h, []float64{float64(g.Color.R), float64(g.Color.G), float64(g.Color.B)})
	if err != nil {
		return nil, err
	}
	over, err := r.a.Add(vips.Bandjoin(ctx, []*vips.Image{colour, mask}))
	if err != nil {
		return nil, err
	}
	if over, err = r.a.Add(over.Copy(ctx, vips.CopyOptions{Interpretation: vips.InterpretationSRGB})); err != nil {
		return nil, err
	}
	return r.over(ctx, img, over)
}

// over composites the sRGBA overlay over img (premultiplied, vips
// composite "over"), rounds back to 8 bits and drops the alpha band when
// img had none.
func (r *runner) over(ctx context.Context, img, overlay *vips.Image) (*vips.Image, error) {
	hadAlpha := img.HasAlpha()
	out, err := r.a.Add(img.Composite2(ctx, overlay, vips.BlendOver, 0, 0))
	if err != nil {
		return nil, err
	}
	if out, err = r.a.Add(out.CastRound(ctx, vips.BandFormatUchar)); err != nil {
		return nil, err
	}
	if !hadAlpha && out.HasAlpha() {
		return r.a.Add(out.ExtractBand(ctx, 0, out.Bands()-1))
	}
	return out, nil
}

// multiplyAlpha multiplies the alpha band by k, adding an opaque one first
// when img has none.
func (r *runner) multiplyAlpha(ctx context.Context, img *vips.Image, k float64) (*vips.Image, error) {
	var err error
	if !img.HasAlpha() {
		if img, err = r.a.Add(img.AddAlpha(ctx)); err != nil {
			return nil, err
		}
	}
	colour, alpha, err := r.splitAlpha(ctx, img)
	if err != nil {
		return nil, err
	}
	if alpha, err = r.a.Add(alpha.Linear(ctx, []float64{k}, []float64{0})); err != nil {
		return nil, err
	}
	if alpha, err = r.a.Add(alpha.CastRound(ctx, vips.BandFormatUchar)); err != nil {
		return nil, err
	}
	return r.a.Add(vips.Bandjoin(ctx, []*vips.Image{colour, alpha}))
}

// splitAlpha returns the colour bands and the alpha band (nil without one).
func (r *runner) splitAlpha(ctx context.Context, img *vips.Image) (colour, alpha *vips.Image, err error) {
	if !img.HasAlpha() {
		return img, nil, nil
	}
	n := img.Bands() - 1
	if colour, err = r.a.Add(img.ExtractBand(ctx, 0, n)); err != nil {
		return nil, nil, err
	}
	if alpha, err = r.a.Add(img.ExtractBand(ctx, n, 1)); err != nil {
		return nil, nil, err
	}
	return colour, alpha, nil
}

// solid is a w×h 8-bit sRGB image of the constant pixel vals (3 or 4
// bands).
func (r *runner) solid(ctx context.Context, w, h int, vals []float64) (*vips.Image, error) {
	img, err := r.a.Add(vips.Black(ctx, w, h, 1))
	if err != nil {
		return nil, err
	}
	if img, err = r.a.Add(img.Linear(ctx, make([]float64, len(vals)), vals)); err != nil {
		return nil, err
	}
	if img, err = r.a.Add(img.CastRound(ctx, vips.BandFormatUchar)); err != nil {
		return nil, err
	}
	return r.a.Add(img.Copy(ctx, vips.CopyOptions{Interpretation: vips.InterpretationSRGB}))
}
