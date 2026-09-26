package exec

import (
	"math"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// maxThumbAspectError is how far the aspect ratio of an Exif thumbnail may
// differ from the image's before it is refused: cameras pad 3:2 photos into
// 160×120 thumbnails with black bars, which must never reach the output.
const maxThumbAspectError = 0.01

// exifThumbnail implements enforce_thumbnail for JPEG sources (an
// extension: imgproxy only has it for HEIF/AVIF, whose embedded thumbnail
// the planner loads through heifload). When the request enforces
// thumbnails, the source is a single JPEG with an Exif JPEG thumbnail of the
// same aspect ratio, and that thumbnail is at least as large as what the
// pipeline needs — planned on the thumbnail, the result has the same size
// as planned on the image and nothing is enlarged — it returns the
// thumbnail as a new source buffer (released by the caller), the source
// meta describing it and its plan. ok is false otherwise, and the image is
// processed as usual.
func (e *Executor) exifThumbnail(j *Job, req *transform.Request, meta *plan.SourceMeta, full *plan.Plan) (*vips.Buffer, plan.SourceMeta, *plan.Plan, bool) {
	o := req.Output.EnforceThumbnail
	if o == nil || !*o || meta.Format != "jpeg" || meta.Frames > 1 || full.Animation != nil || len(full.Chained) > 0 {
		return nil, *meta, nil, false
	}
	th, ok := codec.FindEXIFThumbnail(j.Source.Bytes())
	if !ok || th.Width >= meta.Width || th.Height >= meta.Height {
		return nil, *meta, nil, false
	}
	ar := float64(meta.Width) / float64(meta.Height)
	if math.Abs(float64(th.Width)/float64(th.Height)/ar-1) > maxThumbAspectError {
		return nil, *meta, nil, false
	}
	tm := *meta
	tm.Width, tm.Height, tm.PageHeight = th.Width, th.Height, 0
	tp, err := plan.Build(req, tm, e.cfg.Limits)
	if err != nil || tp.Output.Width != full.Output.Width || tp.Output.Height != full.Output.Height || enlarges(tp) {
		return nil, *meta, nil, false
	}
	buf, err := vips.NewBuffer(len(th.Data))
	if err != nil {
		return nil, *meta, nil, false // not worth failing the request: use the image
	}
	copy(buf.Bytes(), th.Data)
	return buf, tm, tp, true
}

// enlarges reports whether any resize of p scales up.
func enlarges(p *plan.Plan) bool {
	const eps = 1e-9
	for _, s := range p.Steps {
		if r, ok := s.(plan.ResizeStep); ok && (r.ScaleX > 1+eps || r.ScaleY > 1+eps) {
			return true
		}
	}
	return false
}
