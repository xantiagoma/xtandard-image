package exec

import (
	"context"
	"image"

	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// filterParams are the libvips parameters of one FilterStep on a w×h image
// (imgproxy vips_apply_filters). The URL values are used as they are: the
// dialects convert their own scales to imgproxy sigmas and block sizes
// (URL-DIALECTS.md), and imgproxy does not scale filters by DPR.
type filterParams struct {
	// Premultiply is set when blur or sharpen run on an image with alpha,
	// so transparent pixels do not bleed into their neighbours.
	Premultiply bool
	// Blur is the vips_gaussblur sigma; 0 = off.
	Blur float64
	// Sharpen is the vips_sharpen sigma (the other parameters are the
	// libvips defaults); 0 = off.
	Sharpen float64
	// Pixelate is the block size, clamped to the larger image side;
	// ≤ 1 = off.
	Pixelate int
	// PadW, PadH are the size the image is mirrored out to before
	// pixelating, a multiple of Pixelate (w, h when it already is).
	PadW, PadH int
	// Unsharp, BlurAreas and ProgressiveBlur are the Pro filters of the
	// step (filter_extras.go); nil = off.
	Unsharp         *plan.UnsharpMask
	BlurAreas       *plan.BlurAreas
	ProgressiveBlur *transform.ProgressiveBlur
	// Detections blurs or pixelates detected objects (detections.go); nil
	// = off.
	Detections *plan.DetectionBlur
}

func newFilterParams(s *plan.FilterStep, w, h int, alpha bool) filterParams {
	p := filterParams{
		Blur: max(s.Blur, 0), Sharpen: max(s.Sharpen, 0), PadW: w, PadH: h,
		Unsharp: s.Unsharp, ProgressiveBlur: s.ProgressiveBlur,
	}
	if s.BlurAreas != nil && s.BlurAreas.Sigma > 0 && len(s.BlurAreas.Areas) > 0 {
		p.BlurAreas = s.BlurAreas
	}
	if p.ProgressiveBlur != nil && !(p.ProgressiveBlur.Sigma > 0) {
		p.ProgressiveBlur = nil
	}
	if d := s.Detections; d != nil && len(d.Areas) > 0 && (d.Pixelate > 1 || d.Sigma > 0) {
		p.Detections = d
	}
	p.Premultiply = alpha && (p.Blur > 0 || p.Sharpen > 0 || p.extras())
	if n := min(s.Pixelate, max(w, h)); n > 1 {
		p.Pixelate = n
		p.PadW = ceilMultiple(w, n)
		p.PadH = ceilMultiple(h, n)
	}
	return p
}

func (p *filterParams) active() bool {
	return p.Blur > 0 || p.Sharpen > 0 || p.Pixelate > 1 || p.extras()
}

// extras reports whether a Pro filter (unsharp, blur areas, progressive
// blur, detections) is on.
func (p *filterParams) extras() bool {
	return p.Unsharp != nil || p.BlurAreas != nil || p.ProgressiveBlur != nil || p.Detections != nil
}

func ceilMultiple(v, n int) int { return (v + n - 1) / n * n }

// filter applies blur, sharpen, unsharp masking, pixelate, blur areas,
// progressive blur and the detection blur, in that order, in sRGB (premultiplied when the image
// has alpha) and converts the result back to img's interpretation and band
// format (imgproxy applyFilters, then the Pro filters). Animations reach it
// once per frame.
func (r *runner) filter(ctx context.Context, img *vips.Image, s *plan.FilterStep) (*vips.Image, error) {
	w, h := img.Width(), img.Height()
	p := newFilterParams(s, w, h, img.HasAlpha())
	if !p.active() {
		return img, nil
	}
	interp, bf := img.Interpretation(), img.BandFormat()
	var err error
	if interp != vips.InterpretationSRGB {
		if img, err = r.a.Add(img.Colourspace(ctx, vips.InterpretationSRGB)); err != nil {
			return nil, err
		}
	}
	if p.Premultiply {
		if img, err = r.a.Add(img.Premultiply(ctx)); err != nil {
			return nil, err
		}
		if img, err = r.a.Add(img.Cast(ctx, bf)); err != nil {
			return nil, err
		}
	}
	if p.Blur > 0 {
		if img, err = r.a.Add(img.Gaussblur(ctx, p.Blur)); err != nil {
			return nil, err
		}
	}
	if p.Sharpen > 0 {
		o := vips.DefaultSharpenOptions()
		o.Sigma = p.Sharpen
		if img, err = r.a.Add(img.Sharpen(ctx, o)); err != nil {
			return nil, err
		}
	}
	if p.Unsharp != nil {
		if img, err = r.unsharp(ctx, img, p.Unsharp); err != nil {
			return nil, err
		}
	}
	if p.Pixelate > 1 {
		if img, err = r.pixelate(ctx, img, &p, w, h); err != nil {
			return nil, err
		}
	}
	if p.BlurAreas != nil {
		if img, err = r.blurAreas(ctx, img, p.BlurAreas); err != nil {
			return nil, err
		}
	}
	if p.ProgressiveBlur != nil {
		if img, err = r.progressiveBlur(ctx, img, p.ProgressiveBlur); err != nil {
			return nil, err
		}
	}
	if p.Detections != nil {
		if img, err = r.detectionBlur(ctx, img, p.Detections); err != nil {
			return nil, err
		}
	}
	if p.Premultiply {
		if img, err = r.a.Add(img.Unpremultiply(ctx)); err != nil {
			return nil, err
		}
	}
	if img.Interpretation() != interp {
		if img, err = r.a.Add(img.Colourspace(ctx, interp)); err != nil {
			return nil, err
		}
	}
	if img.BandFormat() != bf {
		return r.a.Add(img.Cast(ctx, bf))
	}
	return img, nil
}

// pixelate averages p.Pixelate-sized blocks and enlarges them back with
// nearest neighbour. The image is mirrored out to whole blocks first and
// cropped back to w×h after.
func (r *runner) pixelate(ctx context.Context, img *vips.Image, p *filterParams, w, h int) (*vips.Image, error) {
	n := p.Pixelate
	padded := p.PadW > w || p.PadH > h
	var err error
	if padded {
		if img, err = r.a.Add(img.Embed(ctx, 0, 0, p.PadW, p.PadH, vips.ExtendMirror, nil)); err != nil {
			return nil, err
		}
	}
	if img, err = r.a.Add(img.Shrink(ctx, n, n)); err != nil {
		return nil, err
	}
	// Resize with the nearest kernel and a whole scale is vips_zoom.
	if img, err = r.a.Add(img.Resize(ctx, float64(n), float64(n), vips.KernelNearest)); err != nil {
		return nil, err
	}
	if padded {
		return r.a.Add(img.ExtractArea(ctx, image.Rect(0, 0, w, h)))
	}
	return img, nil
}
