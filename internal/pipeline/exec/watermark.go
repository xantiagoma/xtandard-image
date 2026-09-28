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
		img, err := r.decodeWatermark(ctx, o.MaxPixels)
		if err != nil {
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

// WatermarkImage is a per-request watermark image (plan.WatermarkImage,
// imgproxy watermark_url) the caller fetched: the image of
// Request.Pipelines[Pipeline].Watermarks[Index].
type WatermarkImage struct {
	Pipeline, Index int
	// Source holds the bytes (an SVG already sanitised); the caller keeps
	// its reference and may release it once Run returns.
	Source *vips.Buffer
	// Format is the sniffed codec format name, as Job.Meta.Format.
	Format string
}

// decodeWatermark loads r.j.Source as a watermark (imgproxy
// prepareWatermark before sizing): first frame, EXIF orientation applied,
// in the working colour space.
func (r *runner) decodeWatermark(ctx context.Context, maxPixels int64) (*vips.Image, error) {
	var img *vips.Image
	var err error
	switch {
	case ownFormat(r.j.Meta.Format):
		img, err = r.loadGo(ctx)
	default:
		img, err = r.loadSource(ctx, &vips.LoadOptions{
			NPages:     1,
			Access:     vips.AccessRandom,
			FailOn:     r.e.cfg.FailOn,
			Autorotate: true,
			MaxPixels:  maxPixels,
		})
	}
	if err != nil {
		return nil, err
	}
	return r.toProcessing(ctx, img)
}

// watermark executes s on img, the whole image or the joined strip of an
// animation (imgproxy applyWatermark): the watermark (the configured image,
// the request's own image or rendered text) is fitted into Scale × one
// frame or into Width×Height, rotated, given its shadow, its alpha
// multiplied by the request's and the configured opacity, then placed by
// gravity with overflow allowed, or tiled across the frame (repeated or in
// a chessboard) with the offsets as spacing, and blended over every frame.
// The result keeps img's band format and has alpha only when img had it.
//
// A configured watermark that is missing (none configured, imgproxy then
// ignores it) or a per-request image the caller did not supply is skipped
// and reported in Result.Skipped.
func (r *runner) watermark(ctx context.Context, img *vips.Image, s *plan.WatermarkStep) (*vips.Image, error) {
	opacity := s.Opacity * r.e.watermarkOpacity()
	if opacity <= 0 {
		return img, nil
	}
	w, fh, frames := img.Width(), img.PageHeight(), img.Pages()
	mark, err := r.watermarkSource(ctx, s, w, fh)
	if err != nil || mark == nil {
		return img, err
	}
	if mark, err = r.prepareWatermark(ctx, mark, s, w, fh, opacity); err != nil {
		return nil, err
	}
	var left, top int
	switch s.Tile {
	case transform.TileRepeat:
		mark, err = r.replicate(ctx, mark, w, fh, true)
	case transform.TileChessboard:
		mark, err = r.chessboard(ctx, mark, w, fh)
	default:
		left, top = s.Place(w, fh, mark.Width(), mark.Height())
	}
	if err != nil {
		return nil, err
	}
	if frames > 1 {
		// One frame-sized overlay (clipped to the frame, so nothing
		// bleeds into the next one) repeated down the strip, blended once.
		if s.Tile == transform.TileNone {
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

// watermarkOpacity is XI_PROCESSING_WATERMARK_OPACITY.
func (e *Executor) watermarkOpacity() float64 {
	switch {
	case e.cfg.Watermarks.Opacity != nil:
		return *e.cfg.Watermarks.Opacity
	case e.cfg.Watermark != nil:
		return e.cfg.Watermark.opacity
	default:
		return 1
	}
}

// watermarkSource is the unsized watermark of s on a w×fh frame; nil when
// it is skipped.
func (r *runner) watermarkSource(ctx context.Context, s *plan.WatermarkStep, w, fh int) (*vips.Image, error) {
	switch s.Kind {
	case plan.WatermarkText:
		if s.Text == nil {
			return nil, errs.New(errs.KindInternal, "exec: text watermark without text")
		}
		return r.renderText(ctx, s.Text, w, fh)
	case plan.WatermarkImage:
		for i := range r.j.Watermarks {
			if wi := &r.j.Watermarks[i]; wi.Pipeline == s.Pipeline && wi.Index == s.Index && wi.Source != nil {
				sub := &runner{e: r.e, j: &Job{Source: wi.Source, Meta: plan.SourceMeta{Format: wi.Format}}, a: r.a}
				return sub.decodeWatermark(ctx, r.e.cfg.Limits.MaxSrcPixels)
			}
		}
		r.skip("watermark_source")
		return nil, nil
	default:
		if r.e.cfg.Watermark == nil {
			r.skip("watermark")
			return nil, nil
		}
		return r.a.Track(r.e.cfg.Watermark.img.Image()), nil
	}
}

// prepareWatermark sizes mark for a w×fh frame (imgproxy
// prepareWatermark): fit into the scale box or the size box, enlarging
// allowed; then it rotates it, adds the shadow, applies the opacity and,
// when tiled, pads it with the offsets as spacing. A changed watermark is
// rendered to memory: it is read many times when tiled or blended on every
// frame.
func (r *runner) prepareWatermark(ctx context.Context, mark *vips.Image, s *plan.WatermarkStep, w, fh int, opacity float64) (*vips.Image, error) {
	var err error
	if sc := s.FitScale(w, fh, mark.Width(), mark.Height()); sc != 1 {
		if mark, err = r.resize(ctx, mark, &plan.ResizeStep{ScaleX: sc, ScaleY: sc, Kernel: transform.KernelLanczos3}); err != nil {
			return nil, err
		}
	}
	if !mark.HasAlpha() {
		if mark, err = r.a.Add(mark.AddAlpha(ctx)); err != nil {
			return nil, err
		}
	}
	if angle := math.Mod(s.Rotate, 360); angle != 0 {
		if mark, err = r.rotateMark(ctx, mark, angle); err != nil {
			return nil, err
		}
	}
	if s.Shadow > 0 {
		if mark, err = r.shadow(ctx, mark, s.Shadow); err != nil {
			return nil, err
		}
	}
	if mark, err = r.opacity(ctx, mark, opacity); err != nil {
		return nil, err
	}
	if s.Tile != transform.TileNone {
		px, py := s.TileSpacing(w, fh)
		px, py = max(px, 0), max(py, 0)
		if px > 0 || py > 0 {
			if mark, err = r.embed(ctx, mark, px/2, py/2, mark.Width()+px, mark.Height()+py); err != nil {
				return nil, err
			}
		}
	}
	return r.a.Add(mark.CopyMemory(ctx))
}

// rotateMark rotates mark (with alpha) by angle degrees clockwise onto its
// transparent bounding box, premultiplied so the edges do not darken.
func (r *runner) rotateMark(ctx context.Context, mark *vips.Image, angle float64) (*vips.Image, error) {
	pm, err := r.a.Add(mark.Premultiply(ctx))
	if err != nil {
		return nil, err
	}
	if pm, err = r.a.Add(pm.Similarity(ctx, 1, angle, make([]float64, mark.Bands()))); err != nil {
		return nil, err
	}
	if pm, err = r.a.Add(pm.Unpremultiply(ctx)); err != nil {
		return nil, err
	}
	return r.uchar(ctx, pm)
}

// shadow lays mark (with alpha) over a black copy of its alpha blurred
// with sigma, on a canvas grown by 3 sigma on every side so the blur is
// not cut off (imgproxy watermark_shadow, imgix txt-shad).
func (r *runner) shadow(ctx context.Context, mark *vips.Image, sigma float64) (*vips.Image, error) {
	m := int(math.Ceil(3 * sigma))
	padded, err := r.embed(ctx, mark, m, m, mark.Width()+2*m, mark.Height()+2*m)
	if err != nil {
		return nil, err
	}
	n := padded.Bands()
	a := make([]float64, n)
	a[n-1] = 1
	black, err := r.a.Add(padded.Linear(ctx, a, make([]float64, n)))
	if err != nil {
		return nil, err
	}
	blurred, err := r.a.Add(black.Gaussblur(ctx, sigma))
	if err != nil {
		return nil, err
	}
	out, err := r.a.Add(blurred.Composite2(ctx, padded, vips.BlendOver, 0, 0))
	if err != nil {
		return nil, err
	}
	return r.uchar(ctx, out)
}

// opacity multiplies the alpha band of mark by opacity (imgproxy
// vips_apply_watermark).
func (r *runner) opacity(ctx context.Context, mark *vips.Image, opacity float64) (*vips.Image, error) {
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

// chessboard tiles cell over a w×h area with every other cell empty
// (imgproxy Pro position "ch"): a 2×2 unit holds the cell top-left and
// bottom-right, and the units are laid out so that a cell sits in the
// centre of the area.
func (r *runner) chessboard(ctx context.Context, cell *vips.Image, w, h int) (*vips.Image, error) {
	cw, ch := cell.Width(), cell.Height()
	unit, err := r.a.Add(cell.Embed(ctx, 0, 0, 2*cw, 2*ch, vips.ExtendBlack, nil))
	if err != nil {
		return nil, err
	}
	if unit, err = r.a.Add(unit.Insert(ctx, cell, cw, ch)); err != nil {
		return nil, err
	}
	if unit, err = r.a.Add(unit.CopyMemory(ctx)); err != nil {
		return nil, err
	}
	// Centred on a unit, the area's centre is a cell corner; half a cell
	// more on each axis moves it to the centre of a cell.
	t, err := r.replicate(ctx, unit, w+cw, h+ch, true)
	if err != nil {
		return nil, err
	}
	return r.a.Add(t.ExtractArea(ctx, image.Rect(0, 0, w, h)))
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
