package codec

import (
	"context"
	"fmt"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// SampleImage decodes the first page or frame of buf (format f, as probed)
// upright (EXIF orientation applied) and scaled down to fit maxSide ×
// maxSide, as a libvips image tracked by a: the input of face and object
// detection (WP-72/73), which letterboxes it to the model and renders it
// to 8-bit sRGB itself. libvips formats go through vips_thumbnail (JPEG
// and WebP shrink on load); BMP, ICO, WBMP and ICNS are decoded in Go like
// Sample. It
// must run inside vips.Run.
func SampleImage(ctx context.Context, a *vips.Arena, buf *vips.Buffer, f Format, maxSide int, lim Limits) (*vips.Image, error) {
	if maxSide <= 0 {
		return nil, errs.Newf(errs.KindInternal, "codec: sample size %d", maxSide)
	}
	if f.IsOwn() {
		d, err := decodeOwn(ctx, buf.Bytes(), f, lim.withDefaults().MaxSrcPixels)
		if err != nil {
			return nil, err
		}
		r := ownRaw(d)
		if r == nil {
			emb, err := vips.NewBuffer(len(d.Embedded))
			if err != nil {
				return nil, err
			}
			defer emb.Release()
			copy(emb.Bytes(), d.Embedded)
			return thumbnailImage(ctx, a, emb, maxSide)
		}
		img, err := a.Add(vips.NewFromMemory(ctx, r.Data, r.Width, r.Height, r.Bands, vips.BandFormatUchar))
		if err != nil {
			return nil, err
		}
		if img, err = a.Add(img.Copy(ctx, vips.CopyOptions{Interpretation: vips.InterpretationSRGB})); err != nil {
			return nil, err
		}
		if s := float64(maxSide) / float64(max(r.Width, r.Height)); s < 1 {
			return a.Add(img.Resize(ctx, s, s, vips.KernelLanczos3))
		}
		return img, nil
	}
	if f == FormatEPS {
		// Detection never runs Ghostscript: it samples the embedded
		// preview, when there is one.
		pre, err := EPSPreview(buf.Bytes())
		if err != nil {
			return nil, err
		}
		defer pre.Release() // the image keeps its own reference
		return thumbnailImage(ctx, a, pre, maxSide)
	}
	if f == FormatXCF {
		return flattenXCFSample(ctx, a, buf, maxSide, lim)
	}
	if f == FormatPICT || f == FormatPCD {
		return magickCoderSample(ctx, a, buf, f, maxSide)
	}
	if f == FormatTIFF {
		if l, ok := ParseLabTIFF(buf.Bytes()); ok {
			return labImage(ctx, a, &l, buf, maxSide)
		}
	}
	return thumbnailImage(ctx, a, buf, maxSide)
}

// thumbnailImage is vips_thumbnail of buf's first page, down to maxSide.
func thumbnailImage(ctx context.Context, a *vips.Arena, buf *vips.Buffer, maxSide int) (*vips.Image, error) {
	img, err := a.Add(vips.Thumbnail(ctx, buf, &vips.ThumbnailOptions{
		Width: maxSide, Height: maxSide, Size: vips.SizeDown, FailOn: vips.FailOnError,
	}))
	if err != nil {
		return nil, fmt.Errorf("codec: sample image: %w", err)
	}
	return img, nil
}
