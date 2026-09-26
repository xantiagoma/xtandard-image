package exec

import (
	"context"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/codec/raw"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// formatRAW is the Job.Meta.Format of camera RAW sources.
const formatRAW = "raw"

// Result.RawPath values.
const (
	RawPathPreview      = "preview"       // the embedded JPEG preview
	RawPathDemosaic     = "demosaic"      // LibRaw demosaic at full size
	RawPathDemosaicHalf = "demosaic-half" // LibRaw half-size demosaic
)

// planRaw reads the LibRaw header of a camera RAW source (header only) and
// plans with its preview size: whether the preview is large enough is the
// planner's decision (plan.LoadStep.Preview, FORMATS.md §2).
func (r *runner) planRaw(ctx context.Context) error {
	h, err := raw.Probe(ctx, r.j.Source)
	if err != nil {
		return err
	}
	r.rawHdr = &h
	if h.HasPreview {
		r.meta.PreviewWidth, r.meta.PreviewHeight = h.Preview.Width, h.Preview.Height
	}
	p, err := plan.Build(r.req, r.meta, r.e.cfg.Limits)
	if err != nil {
		return err
	}
	r.plan = p
	return nil
}

// loadRaw decodes a camera RAW as ls says (the embedded preview, or the
// demosaic at full or half size) and attaches the camera metadata as EXIF
// fields. Both paths produce the stored orientation; the plan applies the
// RAW's orientation like any other.
func (r *runner) loadRaw(ctx context.Context, ls *plan.LoadStep) (*vips.Image, error) {
	h := r.rawHdr
	if h == nil {
		return nil, errs.New(errs.KindInternal, "exec: raw source was not probed")
	}
	var img *vips.Image
	var err error
	switch {
	case ls.Preview && h.HasPreview:
		img, err = r.loadRawPreview(ctx, ls, h.Preview)
		r.rawPath = RawPathPreview
	default:
		img, err = r.a.Add(vips.RawLoad(ctx, r.j.Source, &vips.RawOptions{
			HalfSize:  ls.HalfSize,
			MaxPixels: r.e.cfg.Limits.MaxRawDemosaicPixels,
		}))
		r.rawPath = RawPathDemosaic
		if ls.HalfSize {
			r.rawPath = RawPathDemosaicHalf
		}
	}
	if err != nil {
		return nil, err
	}
	return r.rawMetadata(ctx, img)
}

// loadRawPreview loads the embedded JPEG from a C copy of its bytes (SOI
// restored like LibRaw's unpack_thumb), with JPEG shrink-on-load.
func (r *runner) loadRawPreview(ctx context.Context, ls *plan.LoadStep, p raw.Preview) (*vips.Image, error) {
	buf, err := vips.NewBuffer(p.Length)
	if err != nil {
		return nil, err
	}
	defer buf.Release() // the image keeps its own reference
	if err := raw.PreviewBytes(buf.Bytes(), r.j.Source.Bytes(), p); err != nil {
		return nil, err
	}
	o := &vips.LoadOptions{
		Shrink:    ls.ShrinkOnLoad,
		Access:    vips.AccessSequential,
		FailOn:    r.e.cfg.FailOn,
		MaxPixels: r.e.cfg.Limits.MaxSrcPixels,
	}
	if ls.Access == plan.AccessRandom {
		o.Access = vips.AccessRandom
	}
	return r.a.Add(vips.Load(ctx, buf, o))
}

// rawMetadata replaces whatever EXIF the preview carried with the RAW's own
// camera metadata (make, model, exposure, ISO, lens, capture time), which
// the encoder keeps or strips by the metadata policy like any EXIF.
func (r *runner) rawMetadata(ctx context.Context, img *vips.Image) (*vips.Image, error) {
	var remove []vips.MetaField
	for _, f := range img.Fields() {
		if strings.HasPrefix(string(f), "exif-") || f == "jpeg-thumbnail-data" {
			remove = append(remove, f)
		}
	}
	var err error
	if len(remove) > 0 {
		if img, err = r.a.Add(img.RemoveMetadata(ctx, remove...)); err != nil {
			return nil, err
		}
	}
	tags := raw.EXIF(&r.rawHdr.RawHeader)
	if len(tags) == 0 {
		return img, nil
	}
	fields := make([]vips.TextField, len(tags))
	for i := range tags {
		fields[i] = tags[i].Field()
	}
	return r.a.Add(img.SetTexts(ctx, fields...))
}
