package exec

import (
	"context"
	"fmt"
	"image"
	"slices"

	"github.com/xantiagoma/xtandard-image/internal/codec/raw"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// defaultDelay is the frame delay of animations that carry none (imgproxy:
// 40 ms, 25 fps).
const defaultDelay = 40

// runner is the state of one Run. It lives on the Run's locked thread.
type runner struct {
	e    *Executor
	j    *Job
	req  *transform.Request // j.Request planned for the output format
	a    *vips.Arena
	plan *plan.Plan // root plan; replaced by plan.Continue
	// cur is the plan of the pipeline that ran last (the root, or a
	// chained plan from plan.Chain): its Output is the response's.
	cur *plan.Plan
	// meta is j.Meta, with the size of the loaded page when a page other
	// than the first has its own size (multi-page TIFF).
	meta plan.SourceMeta
	// iccImported is set once the source profile was converted to sRGB, so
	// the output colour step drops the (now wrong) embedded profile.
	iccImported bool
	// hdrDone is set once a scene-linear source went through fromHDR.
	hdrDone bool
	// profileEmbedded is set when color_profile converted the result and
	// attached its profile, which the encoder must then keep.
	profileEmbedded bool
	skipped         []string
	// transparent renders PDF pages on a transparent background
	// (pdfTransparent); otherwise they are opaque.
	transparent bool
	// coverage caches the rasterised mask and radius outlines per frame
	// size (geometry_extras.go).
	coverage map[coverageKey][]byte
	// rawHdr is the LibRaw header of a camera RAW source (planRaw),
	// rawPath how it was decoded and rawDecoded to what size.
	rawHdr     *raw.Header
	rawPath    string
	rawDecoded image.Point
	// stackAlpha is set when loadStack gave stacked pages an alpha band the
	// source header did not announce.
	stackAlpha bool
}

// last returns the plan whose Output is the response.
func (r *runner) last() *plan.Plan {
	if r.cur != nil {
		return r.cur
	}
	return r.plan
}

func (r *runner) skip(what string) {
	if !slices.Contains(r.skipped, what) {
		r.skipped = append(r.skipped, what)
	}
}

// replan asks the planner again with what was observed.
func (r *runner) replan(obs plan.Observed) error {
	p, err := plan.Continue(r.req, r.meta, r.e.cfg.Limits, obs)
	if err != nil {
		return err
	}
	r.plan = p
	return nil
}

func (r *runner) execute(ctx context.Context) (*vips.Image, error) {
	if err := r.measureSelection(ctx); err != nil {
		return nil, err
	}
	img, err := r.loadObserved(ctx)
	if err != nil {
		return nil, err
	}
	for _, u := range r.plan.Unplanned {
		r.skip(u)
	}
	if !r.hdrPending() && r.req.Output.PreserveHDR != nil && *r.req.Output.PreserveHDR {
		// Only scene-linear sources keep more than 8 bits so far (fromHDR).
		r.skip("preserve_hdr")
	}
	img, err = r.pipeline(ctx, img)
	if err != nil {
		return nil, pipelineErr(0, len(r.plan.Chained), err)
	}
	r.cur = r.plan
	// r.plan may have been replaced while the root ran; its Chained are
	// only the prediction.
	n := len(r.plan.Chained)
	for i := 1; i <= n; i++ {
		if img, err = r.chained(ctx, img, i); err != nil {
			return nil, pipelineErr(i, n, err)
		}
	}
	return img, nil
}

// pipelineErr attributes err to pipeline i when the request chains several.
func pipelineErr(i, chained int, err error) error {
	if chained == 0 {
		return err
	}
	return fmt.Errorf("exec: pipeline %d: %w", i, err)
}

// chained plans pipeline i on the observed result of pipeline i−1 (img) and
// runs it (imgproxy Pro chained pipelines): the geometry sees the real size,
// frame count and alpha, never the prediction.
func (r *runner) chained(ctx context.Context, img *vips.Image, i int) (*vips.Image, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	in := plan.Input{Width: img.Width(), Height: img.PageHeight(), Frames: img.Pages(), Alpha: img.HasAlpha()}
	p, err := plan.Chain(r.req, r.meta, r.e.cfg.Limits, i, in, nil)
	if err != nil {
		return nil, err
	}
	r.cur = p
	for _, u := range p.Unplanned {
		r.skip(u)
	}
	if p.Materialize {
		if img, err = r.a.Add(img.CopyMemory(ctx)); err != nil {
			return nil, err
		}
	}
	trimmed := func(w, h int) (*plan.Plan, error) {
		np, err := plan.Chain(r.req, r.meta, r.e.cfg.Limits, i, in, &plan.Observed{Width: w, Height: h, Trimmed: true})
		if err != nil {
			return nil, err
		}
		r.cur = np
		return np, nil
	}
	return r.run(ctx, img, p, 0, trimmed)
}

// loadObserved loads the source and reports the loaded size to the planner
// when a header could not predict it (loader rounding, embedded thumbnail).
func (r *runner) loadObserved(ctx context.Context) (*vips.Image, error) {
	ls := r.plan.Load
	img, err := r.load(ctx, &ls, r.plan.Animation)
	if err != nil {
		return nil, err
	}
	if r.plan.Animation != nil {
		return img, nil
	}
	w, h := img.Width(), img.PageHeight()
	switch {
	case ls.Thumbnail:
		if err := r.replan(plan.Observed{Width: w, Height: h, Thumbnail: true}); err != nil {
			return nil, err
		}
		if !r.plan.Load.Thumbnail {
			// Not worth it: load the full image instead.
			ls = r.plan.Load
			return r.load(ctx, &ls, nil)
		}
	case (w != ls.Width || h != ls.Height || r.stackAlpha) && (ls.Page > 0 || ls.NPages > 1) && ls.ShrinkOnLoad <= 1 && ls.Scale == 0:
		// The header describes the first page; this one (or the stack of
		// pages) has its own size.
		if err := r.replanPage(w, h); err != nil {
			return nil, err
		}
	case w != ls.Width || h != ls.Height:
		if err := r.replan(plan.Observed{Width: w, Height: h, Loaded: true}); err != nil {
			return nil, err
		}
	}
	return img, nil
}

// replanFunc returns the plan to continue with after a TrimStep left a w×h
// image; nil means the plan is kept.
type replanFunc func(w, h int) (*plan.Plan, error)

// pipeline runs the root plan on img: Steps (per frame for animations), then
// After. A trim replans the root with the trimmed size.
func (r *runner) pipeline(ctx context.Context, img *vips.Image) (*vips.Image, error) {
	return r.run(ctx, img, r.plan, 0, r.replanTrimmed)
}

// replanTrimmed continues the root plan after its trim.
func (r *runner) replanTrimmed(w, h int) (*plan.Plan, error) {
	if err := r.replan(plan.Observed{Width: w, Height: h, Trimmed: true}); err != nil {
		return nil, err
	}
	return r.plan, nil
}

// run executes p from step start, then p's After steps on the whole image.
func (r *runner) run(ctx context.Context, img *vips.Image, p *plan.Plan, start int, trimmed replanFunc) (*vips.Image, error) {
	var err error
	if p.Animation != nil && img.Pages() > 1 {
		img, err = r.frames(ctx, img, p, start)
	} else {
		img, p, err = r.steps(ctx, img, p, start, trimmed)
	}
	if err != nil {
		return nil, err
	}
	for _, s := range p.After {
		if err := ctxErr(ctx); err != nil {
			return nil, err
		}
		if img, err = r.after(ctx, img, s); err != nil {
			return nil, err
		}
	}
	return img, nil
}

// frames runs p.Steps on every frame of an animation strip and joins the
// frames back, restoring page-height, delays and loop count.
func (r *runner) frames(ctx context.Context, img *vips.Image, p *plan.Plan, start int) (*vips.Image, error) {
	n, ph, w := img.Pages(), img.PageHeight(), img.Width()
	delay, _ := img.Ints(vips.FieldDelay)
	loop, hasLoop := img.Int(vips.FieldLoop)

	out := make([]*vips.Image, 0, n)
	for i := range n {
		if err := ctxErr(ctx); err != nil {
			return nil, err
		}
		f, err := r.a.Add(img.ExtractArea(ctx, image.Rect(0, i*ph, w, (i+1)*ph)))
		if err != nil {
			return nil, err
		}
		// A frame is a single image now: without this, an operation that
		// doubles its height would see two frames.
		if f, err = r.a.Add(f.RemoveMetadata(ctx, vips.FieldPageHeight, vips.FieldNPages)); err != nil {
			return nil, err
		}
		if f, _, err = r.steps(ctx, f, p, start, nil); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	joined, err := r.a.Add(vips.Arrayjoin(ctx, out, 1))
	if err != nil {
		return nil, err
	}
	if joined, err = r.a.Add(joined.SetPageHeight(ctx, out[0].Height())); err != nil {
		return nil, err
	}
	if joined, err = r.a.Add(joined.SetInt(ctx, vips.FieldNPages, n)); err != nil {
		return nil, err
	}
	switch {
	case len(delay) == 0:
		delay = slices.Repeat([]int{defaultDelay}, n)
	case len(delay) > n:
		delay = delay[:n]
	default:
	}
	if joined, err = r.a.Add(joined.SetInts(ctx, vips.FieldDelay, delay)); err != nil {
		return nil, err
	}
	if !hasLoop {
		loop = 0
	}
	return r.a.Add(joined.SetInt(ctx, vips.FieldLoop, loop))
}

// steps runs p.Steps[start:] on a single image. A trim replans with the
// trimmed size through trimmed (when set) and continues on the new plan,
// which is returned.
func (r *runner) steps(ctx context.Context, img *vips.Image, p *plan.Plan, start int, trimmed replanFunc) (*vips.Image, *plan.Plan, error) {
	var err error
	for i := start; i < len(p.Steps); i++ {
		if err := ctxErr(ctx); err != nil {
			return nil, nil, err
		}
		s := p.Steps[i]
		if t, ok := s.(plan.TrimStep); ok {
			if img, err = r.trim(ctx, img, &t); err != nil {
				return nil, nil, err
			}
			if trimmed == nil {
				continue
			}
			if p, err = trimmed(img.Width(), img.Height()); err != nil {
				return nil, nil, err
			}
			i = trimIndex(p.Steps)
			if i < 0 {
				return nil, nil, errs.New(errs.KindInternal, "exec: replanned trim lost its step")
			}
			continue
		}
		if img, err = r.step(ctx, img, s); err != nil {
			return nil, nil, err
		}
	}
	return img, p, nil
}

func trimIndex(steps []plan.Step) int {
	return slices.IndexFunc(steps, func(s plan.Step) bool {
		_, ok := s.(plan.TrimStep)
		return ok
	})
}
