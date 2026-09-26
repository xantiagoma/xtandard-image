package exec

import (
	"context"
	"image"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// Watermark is the configured watermark image (XI_PROCESSING_WATERMARK_SOURCE)
// decoded once for the process lifetime. Every Run reads the same pixels
// through its own vips handle; Close it after the last Run.
type Watermark struct {
	img     *vips.Shared
	opacity float64
}

// WatermarkOptions configures NewWatermark.
type WatermarkOptions struct {
	// Format is the sniffed codec format name, as Job.Meta.Format ("png",
	// "svg", "bmp", ...). An SVG must already be sanitised.
	Format string
	// Opacity is XI_PROCESSING_WATERMARK_OPACITY (0..1): it multiplies
	// every request's watermark opacity.
	Opacity float64
	// FailOn is the decoder strictness (XI_VIPS_FAIL_ON).
	FailOn vips.FailOn
	// MaxPixels bounds the decoded watermark (XI_LIMITS_MAX_SRC_PIXELS);
	// 0 is no limit.
	MaxPixels int64
}

// NewWatermark decodes src the way imgproxy prepares its watermark before
// sizing it: first frame or page only, SVG at its natural size, EXIF
// orientation applied, converted to the working colour space (8-bit sRGB or
// grey, alpha kept), every metadata field dropped. The caller keeps its
// reference to src.
func NewWatermark(ctx context.Context, src *vips.Buffer, o *WatermarkOptions) (*Watermark, error) {
	if src == nil || o == nil {
		return nil, errs.New(errs.KindInternal, "exec: incomplete watermark")
	}
	if o.Opacity < 0 || o.Opacity > 1 {
		return nil, errs.Newf(errs.KindInternal, "exec: watermark opacity %g outside [0, 1]", o.Opacity)
	}
	var shared *vips.Shared
	err := vips.Run(ctx, func() error {
		var a vips.Arena
		defer a.Close()
		r := &runner{
			e: &Executor{cfg: Config{FailOn: o.FailOn, Limits: plan.Limits{MaxSrcPixels: o.MaxPixels}}},
			j: &Job{Source: src, Meta: plan.SourceMeta{Format: o.Format}},
			a: &a,
		}
		img, err := r.loadWatermark(ctx, o.MaxPixels)
		if err != nil {
			return err
		}
		if img, err = r.toProcessing(ctx, img); err != nil {
			return err
		}
		shared, err = vips.Share(ctx, img)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &Watermark{img: shared, opacity: o.Opacity}, nil
}

// Width and Height are the decoded watermark's natural size.
func (w *Watermark) Width() int { return w.img.Width() }

// Height is the decoded watermark's natural height.
func (w *Watermark) Height() int { return w.img.Height() }

// Close releases the decoded pixels. It is idempotent and nil-safe.
func (w *Watermark) Close() {
	if w != nil {
		w.img.Close()
	}
}

func (r *runner) loadWatermark(ctx context.Context, maxPixels int64) (*vips.Image, error) {
	switch r.j.Meta.Format {
	case "bmp", "ico":
		return r.loadGo(ctx)
	default:
		return r.a.Add(vips.Load(ctx, r.j.Source, &vips.LoadOptions{
			NPages:     1,
			Access:     vips.AccessRandom,
			FailOn:     r.e.cfg.FailOn,
			Autorotate: true,
			MaxPixels:  maxPixels,
		}))
	}
}

// watermark executes s on img, the whole image or the joined strip of an
// animation (imgproxy applyWatermark): the configured watermark is fitted
// into Scale × one frame, placed by gravity with overflow allowed, or tiled
// across the frame with the offsets as spacing, its alpha multiplied by the
// request's and the configured opacity, and blended over every frame. The
// result keeps img's band format and has alpha only when img had it.
//
// Watermarks with their own source or text and chessboard tiling are M3:
// they are skipped, as is a watermark when none is configured (imgproxy
// ignores it then).
func (r *runner) watermark(ctx context.Context, img *vips.Image, s *plan.WatermarkStep) (*vips.Image, error) {
	wm := r.e.cfg.Watermark
	switch {
	case !s.Default:
		r.skip("watermark_source")
		return img, nil
	case s.Tile == transform.TileChessboard:
		r.skip("watermark_chessboard")
		return img, nil
	case wm == nil:
		r.skip("watermark")
		return img, nil
	default:
	}
	opacity := s.Opacity * wm.opacity
	if opacity <= 0 {
		return img, nil
	}
	w, fh, frames := img.Width(), img.PageHeight(), img.Pages()
	tiled := s.Tile == transform.TileRepeat

	mark, err := r.prepareWatermark(ctx, wm, s, w, fh)
	if err != nil {
		return nil, err
	}
	if mark, err = r.opacity(ctx, mark, opacity); err != nil {
		return nil, err
	}
	var left, top int
	if tiled {
		if mark, err = r.replicate(ctx, mark, w, fh, true); err != nil {
			return nil, err
		}
	} else {
		left, top = s.Place(w, fh, mark.Width(), mark.Height())
	}
	if frames > 1 {
		// One frame-sized overlay (clipped to the frame, so nothing
		// bleeds into the next one) repeated down the strip, blended once.
		if !tiled {
			if mark, err = r.a.Add(mark.Embed(ctx, left, top, w, fh, vips.ExtendBlack, nil)); err != nil {
				return nil, err
			}
			left, top = 0, 0
		}
		if mark, err = r.replicate(ctx, mark, w, fh*frames, false); err != nil {
			return nil, err
		}
	}
	return r.composite(ctx, img, mark, left, top)
}

// prepareWatermark sizes the configured watermark for a w×fh frame
// (imgproxy prepareWatermark): fit into the scale box, enlarging allowed,
// then, when tiled, padded with the offsets as spacing. A changed
// watermark is rendered to memory: it is read many times when tiled or
// blended on every frame.
func (r *runner) prepareWatermark(ctx context.Context, wm *Watermark, s *plan.WatermarkStep, w, fh int) (*vips.Image, error) {
	mark := r.a.Track(wm.img.Image())
	changed := false
	var err error
	if sc := s.FitScale(w, fh, mark.Width(), mark.Height()); sc != 1 {
		if mark, err = r.resize(ctx, mark, &plan.ResizeStep{ScaleX: sc, ScaleY: sc, Kernel: transform.KernelLanczos3}); err != nil {
			return nil, err
		}
		changed = true
	}
	if s.Tile == transform.TileRepeat {
		px, py := s.TileSpacing(w, fh)
		px, py = max(px, 0), max(py, 0)
		if px > 0 || py > 0 {
			if mark, err = r.embed(ctx, mark, px/2, py/2, mark.Width()+px, mark.Height()+py); err != nil {
				return nil, err
			}
			changed = true
		}
	}
	if !changed {
		return mark, nil // the shared image is already in memory
	}
	return r.a.Add(mark.CopyMemory(ctx))
}

// opacity gives mark an alpha band and multiplies it by opacity (imgproxy
// vips_apply_watermark).
func (r *runner) opacity(ctx context.Context, mark *vips.Image, opacity float64) (*vips.Image, error) {
	var err error
	if !mark.HasAlpha() {
		if mark, err = r.a.Add(mark.AddAlpha(ctx)); err != nil {
			return nil, err
		}
	}
	if opacity >= 1 {
		return mark, nil
	}
	n := mark.Bands() - 1
	colour, err := r.a.Add(mark.ExtractBand(ctx, 0, n))
	if err != nil {
		return nil, err
	}
	alpha, err := r.a.Add(mark.ExtractBand(ctx, n, 1))
	if err != nil {
		return nil, err
	}
	if alpha, err = r.a.Add(alpha.Linear(ctx, []float64{opacity}, []float64{0})); err != nil {
		return nil, err
	}
	return r.a.Add(vips.Bandjoin(ctx, []*vips.Image{colour, alpha}))
}

// replicate tiles mark over a w×h area (imgproxy vips_replicate_go): when
// centered, an odd number of tiles is laid out and the middle w×h is kept,
// so a tile sits in the centre.
func (r *runner) replicate(ctx context.Context, mark *vips.Image, w, h int, centered bool) (*vips.Image, error) {
	mw, mh := mark.Width(), mark.Height()
	across, down := ceilDiv(w, mw), ceilDiv(h, mh)
	if centered {
		across |= 1
		down |= 1
	}
	t, err := r.a.Add(mark.Replicate(ctx, across, down))
	if err != nil {
		return nil, err
	}
	var left, top int
	if centered {
		left, top = (t.Width()-w)/2, (t.Height()-h)/2
	}
	return r.a.Add(t.ExtractArea(ctx, image.Rect(left, top, left+w, top+h)))
}

func ceilDiv(a, b int) int { return (a + b - 1) / b }

// composite blends mark over img at (left, top) and restores img's band
// format and alpha (imgproxy vips_apply_watermark). libvips picks the
// compositing space: grey only when both are grey.
func (r *runner) composite(ctx context.Context, img, mark *vips.Image, left, top int) (*vips.Image, error) {
	hadAlpha, bf := img.HasAlpha(), img.BandFormat()
	out, err := r.a.Add(img.Composite2(ctx, mark, vips.BlendOver, left, top))
	if err != nil {
		return nil, err
	}
	if out, err = r.a.Add(out.Cast(ctx, bf)); err != nil {
		return nil, err
	}
	if !hadAlpha && out.HasAlpha() {
		return r.a.Add(out.ExtractBand(ctx, 0, out.Bands()-1))
	}
	return out, nil
}
