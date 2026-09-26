package plan

import (
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Input is the result of a pipeline as the executor observed it: the input
// of the pipeline chained after it.
type Input struct {
	Width, Height int // one frame
	// Frames is the number of frames of the strip; ≤ 1 is a single image.
	Frames int
	Alpha  bool
}

// Chain plans pipeline i (1 ≤ i < len(req.Pipelines)) of req on in, the
// observed result of pipeline i−1, so that its geometry (resize scales,
// crops, extends) is computed on the real size rather than on the
// prediction Build made. A chained pipeline sees a fresh image: no EXIF
// orientation (it was applied by the first pipeline), no shrink-on-load and
// no page selection. obs is nil, or the size after the pipeline's own
// TrimStep (Observed.Trimmed); the steps before it are unchanged.
//
// The returned plan's Load is zero and its Chained is empty. Errors are
// *errs.Error like Build's.
//
//nolint:gocritic // hugeParam: values like Build
func Chain(req *transform.Request, meta SourceMeta, limits Limits, i int, in Input, obs *Observed) (*Plan, error) {
	if req == nil {
		return nil, errs.New(errs.KindInternal, "plan: nil request")
	}
	if i < 1 || i >= len(req.Pipelines) {
		return nil, errs.Newf(errs.KindInternal, "plan: no chained pipeline %d", i)
	}
	if in.Width <= 0 || in.Height <= 0 || in.Width > maxCoord || in.Height > maxCoord {
		return nil, errs.New(errs.KindCorrupt, "plan: chained input size is out of range")
	}
	if obs != nil && (!obs.Trimmed || obs.Width <= 0 || obs.Height <= 0 || obs.Width > maxCoord || obs.Height > maxCoord) {
		return nil, errs.New(errs.KindCorrupt, "plan: observed image size is out of range")
	}
	lim := resolveLimits(&limits, &req.Limits)
	outFmt := req.Output.Format
	if outFmt == transform.FormatUnset {
		outFmt, _ = transform.ParseFormat(strings.ToLower(meta.Format))
	}
	return planChained(req.Pipelines, i, &in, &lim, outFmt, obs)
}

// planChained plans pipelines[i] on in.
func planChained(pipes []transform.Pipeline, i int, in *Input, lim *effLimits, outFmt transform.Format, obs *Observed) (*Plan, error) {
	s := source{w: in.Width, h: in.Height, alpha: in.Alpha, frames: max(in.Frames, 1)}
	pl, err := planPipeline(&pipes[i], &s, lim, outFmt, obs, i == len(pipes)-1)
	if err != nil {
		return nil, err
	}
	// The input of a chained pipeline is the previous result, a lazy libvips
	// graph. It is computed into memory first only when a step reads it out
	// of order: a sequential source cannot be read twice, and a random
	// access would recompute the previous pipelines per region.
	pl.Materialize = pl.Load.Access == AccessRandom
	pl.Load = LoadStep{}
	return pl, nil
}

// chainCost adds the chained plans to root's cost: pipeline k runs while
// the materialised results before it are still held (the executor's arena
// releases every image at the end of the run), so its peak is theirs plus
// its own.
func chainCost(root *Plan) {
	prev := root
	var heldPx, heldBytes int64
	for _, pl := range root.Chained {
		pc := pl.Cost
		root.Cost.Pixels = max(root.Cost.Pixels, heldPx+pc.Pixels)
		root.Cost.EstimatedBytes = max(root.Cost.EstimatedBytes, heldBytes+pc.EstimatedBytes)
		if pl.Materialize {
			px, b := outputCost(&prev.Output)
			heldPx += px
			heldBytes += b
		}
		prev = pl
	}
}

// outputCost is the size of a plan's result in memory (8-bit samples).
func outputCost(o *Output) (px, bytes int64) {
	px = int64(o.Width) * int64(o.Height) * int64(max(o.Frames, 1))
	bands := int64(3)
	if o.Alpha {
		bands = 4
	}
	return px, px * bands
}
