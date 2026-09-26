package app

import (
	"context"
	"math"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/server"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// qualities are the per-format default qualities (XI_FORMATS_*_QUALITY).
type qualities struct {
	def, jpeg, webp, avif, heic, jxl int
}

// vipsProcessor is the temporary server.Processor carried over from the
// walking skeleton: one shrink-on-load thumbnail fitting the first
// pipeline's width×height (× DPR) and a save in the negotiated format, all
// in one vips.Run. WP-25/26a (planner + executor) and WP-30 (encoders)
// replace it; every other option is ignored until then.
type vipsProcessor struct {
	q qualities
}

// Process implements server.Processor.
func (p *vipsProcessor) Process(ctx context.Context, in *server.ProcessInput) (*server.ProcessOutput, error) {
	format, ok := vipsFormat(in.Decision.Format)
	if !ok {
		return nil, errs.Newf(errs.KindUnsupported, "app: cannot encode %s yet", in.Decision.Format)
	}
	src, err := vipsBuffer(in.Source)
	if err != nil {
		return nil, err
	}
	defer src.Release()

	var w, h int
	if len(in.Request.Pipelines) > 0 {
		rs := &in.Request.Pipelines[0].Resize
		dpr := max(rs.DPR, 1)
		w = int(math.Round(float64(rs.Width.Px) * dpr))
		h = int(math.Round(float64(rs.Height.Px) * dpr))
	}
	opts := vips.DefaultSaveOptions()
	q := p.quality(in.Request, in.Decision.Format, in.QualityDelta)
	opts.JPEG.Quality, opts.WebP.Quality, opts.HEIF.Quality, opts.JXL.Quality = q, q, q, q
	opts.JPEG.Interlace = true

	var out *server.ProcessOutput
	err = vips.Run(ctx, func() error {
		img, err := vips.LoadThumbnail(ctx, src, w, h)
		if err != nil {
			return err
		}
		defer img.Close()
		if err := ctx.Err(); err != nil {
			return errs.Wrap(errs.KindOf(err), context.Cause(ctx))
		}
		blob, err := img.Save(ctx, format, &opts)
		if err != nil {
			return err
		}
		out = &server.ProcessOutput{
			Body:        blob,
			ContentType: negotiate.ContentType(in.Decision.Format),
			Format:      in.Decision.Format,
			Width:       img.Width(),
			Height:      img.Height(),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// quality follows the precedence of ARCHITECTURE §8.3 (URL quality, then
// per-format URL quality, then config per format, then global), lowered by
// the Save-Data delta.
func (p *vipsProcessor) quality(req *transform.Request, f transform.Format, delta int) int {
	q := req.Output.Quality
	if q == 0 {
		q = req.Output.FormatQuality[f]
	}
	if q == 0 {
		switch f {
		case transform.FormatJPEG:
			q = p.q.jpeg
		case transform.FormatWebP:
			q = p.q.webp
		case transform.FormatAVIF:
			q = p.q.avif
		case transform.FormatHEIC:
			q = p.q.heic
		case transform.FormatJXL:
			q = p.q.jxl
		default:
		}
	}
	if q == 0 {
		q = p.q.def
	}
	return min(max(q+delta, 1), 100)
}
