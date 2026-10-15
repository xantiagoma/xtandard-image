package exec

import (
	"context"
	"image"
	"math"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// Video overlays (WP-124, ADR 0012 §3): an image or text watermark is
// rendered once by the image watermark code for the size of a video's
// output frame, and FFmpeg composites the pixels over every frame of its
// time window. Nothing of the watermark reaches FFmpeg but RGBA pixels and
// their place.

// Overlay is a watermark rendered for a video frame: straight (not
// premultiplied) 8-bit RGBA pixels in C memory, Width × Height × 4 bytes,
// to be drawn with their top-left corner at (X, Y). The rectangle lies
// inside the frame. The caller releases Pixels.
type Overlay struct {
	Pixels              *vips.Buffer
	X, Y, Width, Height int
}

// Release frees the pixels. It is nil-safe and idempotent.
func (o *Overlay) Release() {
	if o != nil && o.Pixels != nil {
		o.Pixels.Release()
		o.Pixels = nil
	}
}

// OverlayImage is the fetched image of an image overlay
// (transform.VideoOverlay.Source).
type OverlayImage struct {
	// Source holds the bytes (an SVG already sanitised); the caller keeps
	// its reference.
	Source *vips.Buffer
	// Format is the sniffed codec format name, as Job.Meta.Format.
	Format string
}

// RenderOverlay renders w as it would be drawn on a frameW × frameH image
// (the image pipeline's watermark step at DPR 1: the configured image, img,
// or sanitised text; fitted, rotated, shadowed, its opacity multiplied by
// the configured one, then placed by gravity or tiled), clipped to the
// frame. img is the overlay's own image (nil for text and the configured
// watermark). w's position must be defaulted.
//
// It returns nil when nothing would be drawn: opacity 0, no configured
// watermark, empty text, or a placement entirely outside the frame.
func (e *Executor) RenderOverlay(ctx context.Context, w *transform.Watermark, img *OverlayImage, frameW, frameH int) (*Overlay, error) {
	return e.RenderOverlayScaled(ctx, w, img, frameW, frameH, frameW, frameH)
}

// RenderOverlayScaled is RenderOverlay laid out on a refW × refH frame and
// then scaled to a frameW × frameH one (premultiplied Lanczos, its
// rectangle scaled and kept inside the frame): the renditions of an HLS or
// DASH stream (WP-131) show the same overlay at the same relative size and
// place as on the largest one, so it never jumps when a player switches
// renditions.
func (e *Executor) RenderOverlayScaled(ctx context.Context, w *transform.Watermark, img *OverlayImage, refW, refH, frameW, frameH int) (*Overlay, error) {
	if w == nil || frameW <= 0 || frameH <= 0 || refW <= 0 || refH <= 0 {
		return nil, errs.Newf(errs.KindInternal, "exec: overlay for a %dx%d frame laid out on %dx%d", frameW, frameH, refW, refH)
	}
	s := plan.WatermarkFor(w)
	if s.Kind == plan.WatermarkImage && (img == nil || img.Source == nil) {
		return nil, errs.New(errs.KindInternal, "exec: image overlay without its image")
	}
	opacity := s.Opacity * e.watermarkOpacity()
	if opacity <= 0 {
		return nil, nil
	}
	var out *Overlay
	err := vips.Run(ctx, func() error {
		var a vips.Arena
		defer a.Close()
		j := &Job{}
		if img != nil && s.Kind == plan.WatermarkImage {
			j.Watermarks = []WatermarkImage{{Pipeline: s.Pipeline, Index: s.Index, Source: img.Source, Format: img.Format}}
		}
		r := &runner{e: e, j: j, a: &a}
		var err error
		out, err = r.overlay(ctx, &s, refW, refH, frameW, frameH, opacity)
		return err
	})
	if err != nil {
		out.Release()
		return nil, err
	}
	return out, nil
}

// overlay is RenderOverlayScaled on the locked thread: laid out on an
// fw × fh frame, scaled to tw × th.
func (r *runner) overlay(ctx context.Context, s *plan.WatermarkStep, fw, fh, tw, th int, opacity float64) (*Overlay, error) {
	mark, err := r.watermarkSource(ctx, s, fw, fh)
	if err != nil || mark == nil {
		return nil, err
	}
	if mark, err = r.prepareWatermark(ctx, mark, s, fw, fh, opacity); err != nil {
		return nil, err
	}
	var left, top int
	switch s.Tile {
	case transform.TileRepeat:
		mark, err = r.replicate(ctx, mark, fw, fh, true)
	case transform.TileChessboard:
		mark, err = r.chessboard(ctx, mark, fw, fh)
	default:
		left, top = s.Place(fw, fh, mark.Width(), mark.Height())
	}
	if err != nil {
		return nil, err
	}
	place := image.Rect(left, top, left+mark.Width(), top+mark.Height())
	vis := place.Intersect(image.Rect(0, 0, fw, fh))
	if vis.Empty() {
		return nil, nil
	}
	if vis != place {
		if mark, err = r.a.Add(mark.ExtractArea(ctx, vis.Sub(place.Min))); err != nil {
			return nil, err
		}
	}
	if mark, err = r.rgba(ctx, mark); err != nil {
		return nil, err
	}
	if tw != fw || th != fh {
		if mark, vis, err = r.scaleOverlay(ctx, mark, vis, fw, fh, tw, th); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, errs.Wrap(errs.KindOf(err), context.Cause(ctx))
	}
	pix, err := mark.WritePixels(ctx)
	if err != nil {
		return nil, err
	}
	defer pix.Release()
	n := vis.Dx() * vis.Dy() * 4
	if pix.Len() != n {
		return nil, errs.Newf(errs.KindInternal, "exec: overlay of %d bytes, want %d", pix.Len(), n)
	}
	buf, err := vips.NewBuffer(n)
	if err != nil {
		return nil, err
	}
	copy(buf.Bytes(), pix.Bytes())
	return &Overlay{Pixels: buf, X: vis.Min.X, Y: vis.Min.Y, Width: vis.Dx(), Height: vis.Dy()}, nil
}

// scaleOverlay scales the RGBA overlay mark, placed at vis on an fw × fh
// frame, to a tw × th frame: its rectangle scaled (rounded, at least one
// pixel, inside the frame) and its pixels resized premultiplied, so
// transparent edges keep their colour.
func (r *runner) scaleOverlay(ctx context.Context, mark *vips.Image, vis image.Rectangle, fw, fh, tw, th int) (*vips.Image, image.Rectangle, error) {
	kx, ky := float64(tw)/float64(fw), float64(th)/float64(fh)
	axis := func(lo, hi int, k float64, size int) (int, int) {
		a, b := int(math.Round(float64(lo)*k)), int(math.Round(float64(hi)*k))
		a = min(max(a, 0), size-1)
		b = min(max(b, a+1), size)
		return a, b
	}
	x0, x1 := axis(vis.Min.X, vis.Max.X, kx, tw)
	y0, y1 := axis(vis.Min.Y, vis.Max.Y, ky, th)
	pm, err := r.a.Add(mark.Premultiply(ctx))
	if err != nil {
		return nil, vis, err
	}
	hs, vs := float64(x1-x0)/float64(vis.Dx()), float64(y1-y0)/float64(vis.Dy())
	if pm, err = r.a.Add(pm.Resize(ctx, hs, vs, vips.KernelLanczos3)); err != nil {
		return nil, vis, err
	}
	if pm, err = r.a.Add(pm.Unpremultiply(ctx)); err != nil {
		return nil, vis, err
	}
	if mark, err = r.uchar(ctx, pm); err != nil {
		return nil, vis, err
	}
	// The resize rounds its size: keep what fits the scaled rectangle.
	w, h := min(mark.Width(), x1-x0), min(mark.Height(), y1-y0)
	if w != mark.Width() || h != mark.Height() {
		if mark, err = r.a.Add(mark.ExtractArea(ctx, image.Rect(0, 0, w, h))); err != nil {
			return nil, vis, err
		}
	}
	return mark, image.Rect(x0, y0, x0+w, y0+h), nil
}

// rgba converts a watermark (working space: 8-bit sRGB or grey, with
// alpha) to exactly four 8-bit bands, R G B A, sRGB.
func (r *runner) rgba(ctx context.Context, img *vips.Image) (*vips.Image, error) {
	var err error
	if img.Bands() < 3 || img.Interpretation() != vips.InterpretationSRGB {
		if img, err = r.a.Add(img.Colourspace(ctx, vips.InterpretationSRGB)); err != nil {
			return nil, err
		}
	}
	if !img.HasAlpha() {
		if img, err = r.a.Add(img.AddAlpha(ctx)); err != nil {
			return nil, err
		}
	}
	if img, err = r.uchar(ctx, img); err != nil {
		return nil, err
	}
	if img.Bands() > 4 {
		colour, err := r.a.Add(img.ExtractBand(ctx, 0, 3))
		if err != nil {
			return nil, err
		}
		alpha, err := r.a.Add(img.ExtractBand(ctx, img.Bands()-1, 1))
		if err != nil {
			return nil, err
		}
		return r.a.Add(vips.Bandjoin(ctx, []*vips.Image{colour, alpha}))
	}
	return img, nil
}
