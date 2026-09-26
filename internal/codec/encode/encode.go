package encode

import (
	"context"
	"image"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// Encoder encodes processed images with the configured defaults. It holds no
// per-request state and is safe for concurrent use; each Encode call runs on
// its caller's vips.Run thread.
type Encoder struct {
	cfg Config
}

// New returns an Encoder for cfg after validating it.
func New(cfg *Config) (*Encoder, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Encoder{cfg: *cfg}, nil
}

// Config returns the encoder's configuration.
func (e *Encoder) Config() Config { return e.cfg }

// Encode encodes img as t describes. Neither img nor t is modified, and img
// is not closed; images
// Encode derives from it are closed before it returns. The Result's Blob
// holds one reference that the caller releases after writing it.
//
// Errors: errs.KindUnsupported for a format that is not encodable (HEIC
// without Config.HEICEncode, SVG, video, JSON, unset), errs.KindLimitExceeded
// for an ICO over 256×256, and the vips errors of the savers.
func (e *Encoder) Encode(ctx context.Context, img *vips.Image, t *Target) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, errs.Wrap(errs.KindOf(err), context.Cause(ctx))
	}
	if t.Format == transform.FormatHEIC && !e.cfg.HEICEncode {
		return nil, errs.New(errs.KindUnsupported, "encode: heic output is disabled (XI_FORMATS_HEIC_ENCODE)")
	}

	var a vips.Arena
	defer a.Close()

	img, err := e.frames(ctx, &a, img, t)
	if err != nil {
		return nil, err
	}
	res := &Result{Format: t.Format, ContentType: negotiate.ContentType(t.Format)}

	if t.Format == transform.FormatBMP || t.Format == transform.FormatICO {
		body, err := encodeGo(ctx, &a, img, t.Format)
		if err != nil {
			return nil, err
		}
		res.Blob, res.Attempts = body, 1
		res.OverBudget = t.MaxBytes > 0 && body.Len() > t.MaxBytes
		return res, nil
	}

	p, ok := e.cfg.planFor(t, imageInfo{
		pixels:   int64(img.Width()) * int64(img.PageHeight()),
		hasAlpha: img.HasAlpha(),
	})
	if !ok {
		return nil, errs.Newf(errs.KindUnsupported, "encode: cannot encode %q", t.Format.String())
	}
	if p.flatten && img.HasAlpha() {
		// Formats without alpha flatten on white (FORMATS §5).
		if img, err = a.Add(img.Flatten(ctx, []float64{255, 255, 255})); err != nil {
			return nil, err
		}
	}
	img, keep, err := prepareMetadata(ctx, &a, img, t.Metadata)
	if err != nil {
		return nil, err
	}
	p.opts.Keep = keep

	save := func(ctx context.Context, q int) (Body, error) {
		if p.quality > 0 {
			p.setQuality(q)
		}
		blob, err := img.Save(ctx, p.format, &p.opts)
		if err != nil {
			return nil, err
		}
		return blob, nil
	}

	aq, ignored := e.cfg.AutoQuality.autoQuality(t, p.quality)
	res.AutoQuality.Ignored = ignored
	if aq.method == transform.AutoQualityUnset && (t.MaxBytes <= 0 || p.quality == 0) {
		body, err := save(ctx, p.quality)
		if err != nil {
			return nil, err
		}
		res.Blob, res.Quality, res.Attempts = body, p.quality, 1
		res.OverBudget = t.MaxBytes > 0 && body.Len() > t.MaxBytes
		return res, nil
	}

	// Several saves follow: render the pipeline once so each save only
	// encodes (and a sequential source is read once).
	mem, err := a.Add(img.CopyMemory(ctx))
	if err != nil {
		return nil, err
	}
	img = mem

	switch aq.method {
	case transform.AutoQualityDSSIM:
		err = autoDSSIM(ctx, &a, img, &p, aq, t, res)
	case transform.AutoQualitySize:
		// The whole output counts against the budget, so an animation
		// is searched with all its frames.
		var fit fitResult
		fit, err = fitBytes(ctx, aq.lo, aq.hi, int(aq.target), save)
		if err == nil {
			res.Blob, res.Quality, res.Attempts, res.OverBudget = fit.body, fit.quality, fit.attempts, fit.overBudget
			res.AutoQuality = AutoQualityResult{
				Method: aq.method, Target: aq.target, Metric: float64(fit.body.Len()), Met: !fit.overBudget,
			}
		}
	default:
		var fit fitResult
		fit, err = fitBytes(ctx, 1, p.quality, t.MaxBytes, save)
		if err == nil {
			res.Blob, res.Quality, res.Attempts, res.OverBudget = fit.body, fit.quality, fit.attempts, fit.overBudget
		}
	}
	if err != nil {
		return nil, errs.Wrap(errs.KindOf(err), err)
	}
	return res, nil
}

// autoDSSIM runs the dssim search (searchDSSIM) on the first frame of img,
// then saves img (every frame of an animation) at the chosen quality, and
// fits the result under t.MaxBytes when it is over. img is rendered to
// memory.
func autoDSSIM(ctx context.Context, a *vips.Arena, img *vips.Image, p *plan, aq aqParams, t *Target, res *Result) error {
	save := func(ctx context.Context, img *vips.Image, q int) (Body, error) {
		p.setQuality(q)
		blob, err := img.Save(ctx, p.format, &p.opts)
		if err != nil {
			return nil, err
		}
		return blob, nil
	}
	frame := img
	if img.Pages() > 1 {
		var err error
		if frame, err = firstFrame(ctx, a, img); err != nil {
			return err
		}
		if frame, err = a.Add(frame.CopyMemory(ctx)); err != nil {
			return err
		}
	}
	cmp, err := newComparer(ctx, frame)
	if err != nil {
		return err
	}
	sr, err := searchDSSIM(ctx, aq, p.quality,
		func(ctx context.Context, q int) (Body, error) { return save(ctx, frame, q) },
		cmp.dssim)
	if err != nil {
		return err
	}
	body, attempts := sr.body, sr.attempts
	if frame != img {
		// Searched on the first frame: encode the animation once more.
		body.Release()
		if body, err = save(ctx, img, sr.q); err != nil {
			return err
		}
		attempts++
	}
	res.Blob, res.Quality, res.Attempts = body, sr.q, attempts
	res.AutoQuality = AutoQualityResult{Method: aq.method, Target: aq.target, Metric: sr.d, Met: sr.met}
	if t.MaxBytes <= 0 || body.Len() <= t.MaxBytes {
		return nil
	}
	// max_bytes bounds the size whatever the metric says: search below
	// the chosen quality.
	res.AutoQuality.Met = false
	if sr.q <= 1 {
		res.OverBudget = true
		return nil
	}
	fit, err := fitBytes(ctx, 1, sr.q-1, t.MaxBytes, func(ctx context.Context, q int) (Body, error) { return save(ctx, img, q) })
	if err != nil {
		body.Release()
		res.Blob = nil
		return err
	}
	res.Attempts += fit.attempts
	if fit.overBudget && fit.body.Len() >= body.Len() {
		fit.body.Release()
		res.OverBudget = true
		return nil
	}
	body.Release()
	res.Blob, res.Quality, res.OverBudget = fit.body, fit.quality, fit.overBudget
	return nil
}

// frames reduces a multi-frame image to its first frame unless t asks for an
// animation the format can hold.
func (*Encoder) frames(ctx context.Context, a *vips.Arena, img *vips.Image, t *Target) (*vips.Image, error) {
	if img.Pages() <= 1 || (t.Animated && animatable(t.Format)) {
		return img, nil
	}
	return firstFrame(ctx, a, img)
}

// firstFrame returns the first frame of a multi-frame image as a still.
func firstFrame(ctx context.Context, a *vips.Arena, img *vips.Image) (*vips.Image, error) {
	first, err := a.Add(img.ExtractArea(ctx, image.Rect(0, 0, img.Width(), img.PageHeight())))
	if err != nil {
		return nil, err
	}
	return a.Add(first.RemoveMetadata(ctx,
		vips.FieldPageHeight, vips.FieldNPages, vips.FieldDelay, vips.FieldLoop))
}
