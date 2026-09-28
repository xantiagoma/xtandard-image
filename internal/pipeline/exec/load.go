package exec

import (
	"context"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// load decodes the source as ls describes and drops the EXIF orientation
// tag (the plan applies orientation explicitly). A single image never keeps
// page-height, so later operations see exactly one frame.
func (r *runner) load(ctx context.Context, ls *plan.LoadStep, anim *plan.AnimationStep) (*vips.Image, error) {
	var img *vips.Image
	var err error
	switch {
	case r.j.Meta.IsVideo:
		img, err = r.loadVideo(ctx, ls, anim)
	case ownFormat(r.j.Meta.Format):
		img, err = r.loadGo(ctx)
	case r.j.Meta.Format == formatRAW:
		img, err = r.loadRaw(ctx, ls)
	case r.j.Meta.Format == formatEPS:
		img, err = r.loadEPS(ctx, ls)
	case r.j.Meta.Format == formatXCF:
		// No stored composite: the visible layers are decoded one by one
		// and flattened (codec.FlattenXCF).
		img, err = codec.FlattenXCF(ctx, r.a, r.j.Source, r.e.cfg.Limits.MaxSrcPixels, r.e.cfg.FailOn)
	case stackable(r.j.Meta.Format):
		if anim == nil && ls.NPages > 1 {
			// Stacked pages of a multi-page still may differ in size.
			img, err = r.loadStack(ctx, ls)
			break
		}
		img, err = r.loadSource(ctx, r.loadOptions(ls, anim))
	default:
		img, err = r.loadSource(ctx, r.loadOptions(ls, anim))
	}
	if err != nil {
		return nil, err
	}
	if r.j.Meta.Format == "pdf" && !r.transparent && img.HasAlpha() {
		// Rendered onto opaque white. Only the gaps PDFium leaves beside a
		// narrower page of a stack are transparent: fill them with white too.
		if img, err = r.a.Add(img.Flatten(ctx, []float64{255, 255, 255})); err != nil {
			return nil, err
		}
	}
	if img.Orientation() != 1 {
		if img, err = r.a.Add(img.RemoveOrientation(ctx)); err != nil {
			return nil, err
		}
	}
	if anim == nil {
		if _, ok := img.Int(vips.FieldPageHeight); ok {
			if img, err = r.a.Add(img.RemoveMetadata(ctx, vips.FieldPageHeight, vips.FieldNPages)); err != nil {
				return nil, err
			}
		}
	}
	return img, nil
}

// formatXCF is the Job.Meta.Format of GIMP XCF sources.
const formatXCF = "xcf"

// loadSource loads the source through libvips with o. A CIE L*a*b* TIFF,
// which libvips 8.18 cannot read as such, is loaded from a copy relabelled
// RGB and converted to 8-bit sRGB (codec.LoadLabTIFF), one page at a time.
func (r *runner) loadSource(ctx context.Context, o *vips.LoadOptions) (*vips.Image, error) {
	if r.j.Meta.Format == "tiff" {
		if l, ok := codec.ParseLabTIFF(r.j.Source.Bytes()); ok {
			return codec.LoadLabTIFF(ctx, r.a, &l, r.j.Source, o)
		}
	}
	return r.a.Add(vips.Load(ctx, r.j.Source, o))
}

// ownFormat reports the formats decoded by the pure-Go loaders
// (codec.DecodeOwn): BMP, ICO, WBMP, ICNS and DDS.
func ownFormat(format string) bool {
	switch format {
	case "bmp", "ico", "wbmp", "icns", "dds":
		return true
	default:
		return false
	}
}

// stackable reports the multi-page still formats whose pages are loaded one
// at a time and stacked (loadStack).
func stackable(format string) bool {
	switch format {
	case "tiff", "heic", "heif", "avif", "jp2", "jxl":
		return true
	default:
		return false
	}
}

func (r *runner) loadOptions(ls *plan.LoadStep, anim *plan.AnimationStep) *vips.LoadOptions {
	o := &vips.LoadOptions{
		Shrink:        ls.ShrinkOnLoad,
		Scale:         ls.Scale,
		Density:       ls.Density,
		Page:          ls.Page,
		NPages:        max(ls.NPages, 1),
		Access:        vips.AccessSequential,
		FailOn:        r.e.cfg.FailOn,
		HEIFThumbnail: ls.Thumbnail,
	}
	if anim != nil {
		o.NPages = anim.Frames
	}
	if r.j.Meta.Format == "pdf" {
		o.Background = r.pdfBackground()
	}
	if ls.Access == plan.AccessRandom {
		o.Access = vips.AccessRandom
	}
	return o
}

// loadGo decodes BMP, ICO, WBMP, ICNS and DDS with the pure-Go loaders. A
// PNG, JPEG or JPEG 2000 stream embedded in the container is loaded by
// libvips from a C copy; an HDR texture becomes float scRGB, which the
// colour step tone-maps (fromHDR).
func (r *runner) loadGo(ctx context.Context) (*vips.Image, error) {
	f, err := codec.ParseFormat(r.j.Meta.Format)
	if err != nil {
		return nil, err
	}
	d, err := codec.DecodeOwn(r.j.Source.Bytes(), f, r.e.cfg.Limits.MaxSrcPixels)
	if err != nil {
		return nil, goDecodeError(r.j.Meta.Format, err)
	}
	if d.Embedded != nil {
		buf, err := vips.NewBuffer(len(d.Embedded))
		if err != nil {
			return nil, err
		}
		defer buf.Release() // the image keeps its own reference
		copy(buf.Bytes(), d.Embedded)
		return r.a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Access: vips.AccessRandom, FailOn: r.e.cfg.FailOn}))
	}
	if fl := d.Float; fl != nil {
		if err := fl.Validate(); err != nil {
			return nil, errs.Wrap(errs.KindCorrupt, err)
		}
		img, err := r.a.Add(vips.NewFromFloats(ctx, fl.Data, fl.Width, fl.Height, fl.Bands))
		if err != nil {
			return nil, err
		}
		return r.a.Add(img.Copy(ctx, vips.CopyOptions{Interpretation: vips.InterpretationScRGB}))
	}
	raw := d.Raw
	if raw == nil {
		return nil, errs.New(errs.KindCorrupt, "exec: decoder returned no pixels")
	}
	if err := raw.Validate(); err != nil {
		return nil, errs.Wrap(errs.KindCorrupt, err)
	}
	return r.a.Add(vips.NewFromMemory(ctx, raw.Data, raw.Width, raw.Height, raw.Bands, vips.BandFormatUchar))
}

// goDecodeError keeps the kinds the Go loaders chose (limits) and reports
// everything else as a corrupt source.
func goDecodeError(format string, err error) error {
	switch errs.KindOf(err) {
	case errs.KindLimitExceeded, errs.KindUnsupported, errs.KindCorrupt, errs.KindCanceled, errs.KindTimeout:
		return err
	default:
		return errs.Wrapf(errs.KindCorrupt, err, "exec: decode %s", format)
	}
}
