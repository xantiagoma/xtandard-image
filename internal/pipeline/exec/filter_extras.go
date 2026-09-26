package exec

import (
	"context"
	"image"
	"math"

	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// The Pro filters of FilterStep (WP-61): unsharp masking, blur areas and
// progressive blur. imgproxy's implementations are closed source; the maths
// follows its documented semantics (plan.UnsharpMask, plan.BlurAreas).

// unsharpKernel is imgproxy's unsharp_masking kernel for weight w and
// divider d: −w/d around a centre of (d + 8w)/d, which sums to 1.
func unsharpKernel(w, d float64) [9]float64 {
	n := -w / d
	return [9]float64{n, n, n, n, (d + 8*w) / d, n, n, n, n}
}

// unsharp applies the resolved unsharp mask u to every band.
func (r *runner) unsharp(ctx context.Context, img *vips.Image, u *plan.UnsharpMask) (*vips.Image, error) {
	if u.Sigma > 0 {
		return r.a.Add(img.Unsharp(ctx, u.Sigma, u.Amount))
	}
	k := unsharpKernel(u.Weight, u.Divider)
	return r.a.Add(img.Conv3(ctx, &k))
}

// areaRect is area a of a w×h frame in pixels, rounded to the nearest
// edge and clipped; ok is false when it is empty.
func areaRect(a transform.Area, w, h int) (image.Rectangle, bool) {
	px := func(v float64, n int) int { return min(max(int(math.Round(v*float64(n))), 0), n) }
	rc := image.Rect(px(a.Left, w), px(a.Top, h), px(a.Left+a.Width, w), px(a.Top+a.Height, h))
	return rc, !rc.Empty()
}

// blurMargin is how far outside an area a Gaussian of sigma reads: libvips
// cuts its mask where it falls below 0.2/255 of the peak, well inside 3σ.
func blurMargin(sigma float64) int { return int(math.Ceil(3*sigma)) + 1 }

// blurAreas blurs every area of ba with a Gaussian of ba.Sigma. Each area
// is blurred from the unblurred image, together with a margin so its edges
// see their real neighbours, and inserted back: pixels outside the areas are
// untouched. Where areas overlap the later one wins.
func (r *runner) blurAreas(ctx context.Context, img *vips.Image, ba *plan.BlurAreas) (*vips.Image, error) {
	src := img
	w, h := img.Width(), img.Height()
	bounds := image.Rect(0, 0, w, h)
	margin := blurMargin(ba.Sigma)
	for _, a := range ba.Areas {
		rc, ok := areaRect(a, w, h)
		if !ok {
			continue
		}
		ext := rc.Inset(-margin).Intersect(bounds)
		sub, err := r.a.Add(src.ExtractArea(ctx, ext))
		if err != nil {
			return nil, err
		}
		if sub, err = r.a.Add(sub.Gaussblur(ctx, ba.Sigma)); err != nil {
			return nil, err
		}
		if sub, err = r.a.Add(sub.ExtractArea(ctx, rc.Sub(ext.Min))); err != nil {
			return nil, err
		}
		if img, err = r.a.Add(img.Insert(ctx, sub, rc.Min.X, rc.Min.Y)); err != nil {
			return nil, err
		}
	}
	return img, nil
}

// progressiveBlur blends a copy blurred with pb.Sigma into img through a
// linear ramp: none of it before pb.Start, all of it after pb.Stop, along
// pb's direction (the ramp of the gradient effect, colour_math.go).
func (r *runner) progressiveBlur(ctx context.Context, img *vips.Image, pb *transform.ProgressiveBlur) (*vips.Image, error) {
	w, h := img.Width(), img.Height()
	blurred, err := r.a.Add(img.Gaussblur(ctx, pb.Sigma))
	if err != nil {
		return nil, err
	}
	ax, ay, b := progressiveRamp(pb, w, h)
	mask, err := r.a.Add(vips.GradientMask(ctx, w, h, ax, ay, b, 1))
	if err != nil {
		return nil, err
	}
	return r.a.Add(img.BlendMask(ctx, mask, blurred))
}

// progressiveRamp is the mask ramp of pb on a w×h frame: 0 (sharp) at
// Start, 255 (fully blurred) at Stop.
func progressiveRamp(pb *transform.ProgressiveBlur, w, h int) (ax, ay, b float64) {
	return gradientRamp(&transform.Gradient{
		Direction: pb.Direction, Angle: pb.Angle, Start: pb.Start, Stop: pb.Stop, Opacity: 1,
	}, w, h)
}
