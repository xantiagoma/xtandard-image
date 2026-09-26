package exec

import (
	"context"
	"image"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// step executes one plan step on a single image (one frame of an
// animation).
func (r *runner) step(ctx context.Context, img *vips.Image, s plan.Step) (*vips.Image, error) {
	switch s := s.(type) {
	case plan.ColourStep:
		if s.Stage == plan.ColourToResult {
			return r.toResult(ctx, img)
		}
		return r.toProcessing(ctx, img)
	case plan.PreCropStep:
		return r.crop(ctx, img, &s.CropGeometry)
	case plan.CropStep:
		return r.crop(ctx, img, &s.CropGeometry)
	case plan.ResizeStep:
		return r.resize(ctx, img, &s)
	case plan.RotateStep:
		return r.rotate(ctx, img, s.Angle)
	case plan.FlipStep:
		return r.flip(ctx, img, s)
	case plan.ExtendStep:
		return r.extend(ctx, img, &s)
	case plan.PaddingStep:
		return r.padding(ctx, img, &s)
	case plan.FlattenStep:
		return r.flatten(ctx, img, s.Background)
	case plan.FilterStep:
		return r.filter(ctx, img, &s)
	case plan.ColourAdjustStep:
		return r.colourAdjust(ctx, img, &s)
	case plan.WatermarkStep:
		return r.watermark(ctx, img, &s)
	case plan.TrimStep:
		return r.trim(ctx, img, &s)
	default:
		return nil, errs.Newf(errs.KindInternal, "exec: unknown step %T", s)
	}
}

// after executes one of a plan's After steps on the whole image (the joined
// strip of an animation).
func (r *runner) after(ctx context.Context, img *vips.Image, s plan.Step) (*vips.Image, error) {
	return r.step(ctx, img, s)
}

// crop cuts c out of img, placed on its real size (imgproxy cropImage).
// A smart crop runs on a memory copy, as it reads the image several times.
func (r *runner) crop(ctx context.Context, img *vips.Image, c *plan.CropGeometry) (*vips.Image, error) {
	x, y, w, h, ok := c.Rect(img.Width(), img.Height())
	if !ok {
		return img, nil
	}
	if c.Smart != plan.InterestingNone {
		mem, err := r.a.Add(img.CopyMemory(ctx))
		if err != nil {
			return nil, err
		}
		return r.a.Add(mem.SmartCrop(ctx, w, h, interesting(c.Smart)))
	}
	return r.a.Add(img.Crop(ctx, image.Rect(x, y, x+w, y+h)))
}

func interesting(i plan.Interesting) vips.Interesting {
	if i == plan.InterestingEntropy {
		return vips.InterestingEntropy
	}
	return vips.InterestingAttention
}

// resize scales img, premultiplied when it has alpha so transparent pixels
// do not bleed into their neighbours (imgproxy vips_resize_go).
func (r *runner) resize(ctx context.Context, img *vips.Image, s *plan.ResizeStep) (*vips.Image, error) {
	if s.ScaleX == 1 && s.ScaleY == 1 {
		return img, nil
	}
	k := kernel(s.Kernel)
	if !img.HasAlpha() {
		return r.a.Add(img.Resize(ctx, s.ScaleX, s.ScaleY, k))
	}
	bf := img.BandFormat()
	pm, err := r.a.Add(img.Premultiply(ctx))
	if err != nil {
		return nil, err
	}
	if pm, err = r.a.Add(pm.Cast(ctx, bf)); err != nil {
		return nil, err
	}
	rs, err := r.a.Add(pm.Resize(ctx, s.ScaleX, s.ScaleY, k))
	if err != nil {
		return nil, err
	}
	// Unpremultiply casts back to its input's format, bf.
	return r.a.Add(rs.Unpremultiply(ctx))
}

func kernel(k transform.Kernel) vips.Kernel {
	switch k {
	case transform.KernelNearest:
		return vips.KernelNearest
	case transform.KernelLinear:
		return vips.KernelLinear
	case transform.KernelCubic:
		return vips.KernelCubic
	case transform.KernelMitchell:
		return vips.KernelMitchell
	case transform.KernelLanczos2:
		return vips.KernelLanczos2
	default:
		return vips.KernelLanczos3
	}
}

func (r *runner) rotate(ctx context.Context, img *vips.Image, angle int) (*vips.Image, error) {
	var a vips.Angle
	switch ((angle % 360) + 360) % 360 {
	case 90:
		a = vips.Angle90
	case 180:
		a = vips.Angle180
	case 270:
		a = vips.Angle270
	default:
		return img, nil
	}
	return r.a.Add(img.Rot(ctx, a))
}

func (r *runner) flip(ctx context.Context, img *vips.Image, s plan.FlipStep) (*vips.Image, error) {
	var err error
	if s.Horizontal {
		if img, err = r.a.Add(img.Flip(ctx, vips.DirectionHorizontal)); err != nil {
			return nil, err
		}
	}
	if s.Vertical {
		if img, err = r.a.Add(img.Flip(ctx, vips.DirectionVertical)); err != nil {
			return nil, err
		}
	}
	return img, nil
}

// extend grows the canvas. New pixels are transparent (an alpha band is
// added when missing); a FlattenStep later fills them with the background,
// exactly like imgproxy's embed.
func (r *runner) extend(ctx context.Context, img *vips.Image, s *plan.ExtendStep) (*vips.Image, error) {
	x, y, w, h, ok := s.Rect(img.Width(), img.Height())
	if !ok {
		return img, nil
	}
	switch s.Mode {
	case transform.ExtendCopy:
		return r.a.Add(img.Embed(ctx, x, y, w, h, vips.ExtendCopy, nil))
	case transform.ExtendBlur:
		r.skip("extend_blur") // M3: filled like ExtendBackground until then
	default:
	}
	return r.embed(ctx, img, x, y, w, h)
}

func (r *runner) padding(ctx context.Context, img *vips.Image, s *plan.PaddingStep) (*vips.Image, error) {
	w := img.Width() + s.Left + s.Right
	h := img.Height() + s.Top + s.Bottom
	if w == img.Width() && h == img.Height() {
		return img, nil
	}
	return r.embed(ctx, img, s.Left, s.Top, w, h)
}

// embed places img at (x, y) on a transparent w×h canvas (imgproxy
// vips_embed_go).
func (r *runner) embed(ctx context.Context, img *vips.Image, x, y, w, h int) (*vips.Image, error) {
	var err error
	if !img.HasAlpha() {
		if img, err = r.a.Add(img.AddAlpha(ctx)); err != nil {
			return nil, err
		}
	}
	return r.a.Add(img.Embed(ctx, x, y, w, h, vips.ExtendBlack, nil))
}

// flatten blends the alpha band onto bg (imgproxy vips_flatten_go): a grey
// image is converted to colour first when bg is not grey. A translucent bg
// (imgproxy background_alpha) is composited under the image instead, which
// keeps an alpha band.
func (r *runner) flatten(ctx context.Context, img *vips.Image, bg transform.Color) (*vips.Image, error) {
	if !img.HasAlpha() {
		return img, nil
	}
	if bg.A < 255 {
		return r.translucentBackground(ctx, img, bg)
	}
	grey := bg.R == bg.G && bg.G == bg.B
	var err error
	switch img.Interpretation() {
	case vips.InterpretationBW:
		if !grey {
			if img, err = r.a.Add(img.Colourspace(ctx, vips.InterpretationSRGB)); err != nil {
				return nil, err
			}
		}
	case vips.InterpretationGrey16:
		if !grey {
			if img, err = r.a.Add(img.Colourspace(ctx, vips.InterpretationRGB16)); err != nil {
				return nil, err
			}
		}
	default:
	}
	vals := []float64{float64(bg.R), float64(bg.G), float64(bg.B)}
	if img.Bands()-1 < 3 {
		vals = vals[:1]
	}
	return r.a.Add(img.Flatten(ctx, vals))
}

// translucentBackground composites img over a w×h background of bg, alpha
// included: the result is opaque where either is.
func (r *runner) translucentBackground(ctx context.Context, img *vips.Image, bg transform.Color) (*vips.Image, error) {
	if bg.A == 0 {
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
	base, err := r.solid(ctx, img.Width(), img.Height(), []float64{float64(bg.R), float64(bg.G), float64(bg.B), float64(bg.A)})
	if err != nil {
		return nil, err
	}
	return r.over(ctx, base, img)
}

// trim removes a uniform border (imgproxy vips_trim) and renders the result:
// the planner continues with its size, and a trimmed image is read again by
// every later step.
func (r *runner) trim(ctx context.Context, img *vips.Image, t *plan.TrimStep) (*vips.Image, error) {
	var bg []float64
	switch {
	case t.Color != nil:
		bg = []float64{float64(t.Color.R), float64(t.Color.G), float64(t.Color.B)}
	case t.ColorFromBottomRight:
		var err error
		if bg, err = r.pixel(ctx, img, img.Width()-1, img.Height()-1); err != nil {
			return nil, err
		}
	default: // the top-left pixel
	}
	box, err := img.FindTrim(ctx, t.Threshold, bg, false)
	if err != nil {
		return nil, err
	}
	left, top, width, height := box.Min.X, box.Min.Y, box.Dx(), box.Dy()
	if t.EqualHor {
		right := img.Width() - left - width
		if d := right - left; d > 0 {
			width += d
		} else if d < 0 {
			left = right
			width -= d
		}
	}
	if t.EqualVer {
		bottom := img.Height() - top - height
		if d := bottom - top; d > 0 {
			height += d
		} else if d < 0 {
			top = bottom
			height -= d
		}
	}
	if width <= 0 || height <= 0 {
		return img, nil // nothing but background
	}
	out, err := r.a.Add(img.ExtractArea(ctx, image.Rect(left, top, left+width, top+height)))
	if err != nil {
		return nil, err
	}
	return r.a.Add(out.CopyMemory(ctx))
}

// pixel returns the sRGB value of one pixel, transparency flattened onto
// magenta like FindTrim compares it.
func (r *runner) pixel(ctx context.Context, img *vips.Image, x, y int) ([]float64, error) {
	var err error
	if img.Interpretation() != vips.InterpretationSRGB {
		if img, err = r.a.Add(img.Colourspace(ctx, vips.InterpretationSRGB)); err != nil {
			return nil, err
		}
	}
	if img, err = r.a.Add(img.ExtractArea(ctx, image.Rect(x, y, x+1, y+1))); err != nil {
		return nil, err
	}
	if img.HasAlpha() {
		if img, err = r.a.Add(img.Flatten(ctx, []float64{255, 0, 255})); err != nil {
			return nil, err
		}
	}
	if img, err = r.a.Add(img.Cast(ctx, vips.BandFormatUchar)); err != nil {
		return nil, err
	}
	pix, err := img.WriteToMemory(ctx)
	if err != nil {
		return nil, err
	}
	if len(pix) < 3 {
		return nil, errs.New(errs.KindInternal, "exec: short pixel read")
	}
	return []float64{float64(pix[0]), float64(pix[1]), float64(pix[2])}, nil
}
