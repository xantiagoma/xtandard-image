package encode

import (
	"context"
	"image"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/testutil/ssim"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// CompareMaxDimension bounds the longer side of the copies the dssim method
// compares. Both the pre-encode image and each decoded candidate are reduced
// to it with the same resize, so SSIM costs O(512²) per save instead of
// O(pixels). The price is sensitivity: downscaling averages away part of the
// compression artefacts, so the DSSIM of a large image is measured lower than
// at full size and the search settles on a somewhat lower quality (see
// FORMATS.md "Auto-quality" for the measured gap).
const CompareMaxDimension = 512

// comparer measures the DSSIM of encoded candidates against one pre-encode
// image. Its SSIM reference is Go memory; it holds no vips image.
type comparer struct {
	ref   *ssim.Reference
	scale float64 // resize factor applied to both sides (1: none)
}

// newComparer prepares img (one frame, the image that is being saved).
func newComparer(ctx context.Context, img *vips.Image) (*comparer, error) {
	c := &comparer{scale: 1}
	if longest := max(img.Width(), img.PageHeight()); longest > CompareMaxDimension {
		c.scale = float64(CompareMaxDimension) / float64(longest)
	}
	var a vips.Arena
	defer a.Close()
	sample, err := c.sample(ctx, &a, img)
	if err != nil {
		return nil, err
	}
	if c.ref, err = ssim.NewReference(sample); err != nil {
		return nil, errs.Wrap(errs.KindInternal, err)
	}
	return c, nil
}

// dssim decodes body with libvips and returns its DSSIM against the
// reference.
func (c *comparer) dssim(ctx context.Context, body Body) (float64, error) {
	buf, err := vips.NewBuffer(body.Len())
	if err != nil {
		return 0, err
	}
	copy(buf.Bytes(), body.Bytes())
	var a vips.Arena
	defer a.Close()
	img, err := a.Add(vips.Load(ctx, buf, nil))
	buf.Release() // the image holds its own reference
	if err != nil {
		// Our own output failed to decode: an encoder problem, not the
		// client's.
		return 0, errs.Wrap(errs.KindInternal, err)
	}
	sample, err := c.sample(ctx, &a, img)
	if err != nil {
		return 0, err
	}
	res, err := c.ref.Compare(sample)
	if err != nil {
		return 0, errs.Wrap(errs.KindInternal, err)
	}
	return res.DSSIM, nil
}

// sample returns the first frame of img as 8-bit sRGB, alpha-premultiplied,
// reduced by c.scale: the input of the SSIM luma.
func (c *comparer) sample(ctx context.Context, a *vips.Arena, img *vips.Image) (*image.RGBA, error) {
	var err error
	if img.Pages() > 1 {
		if img, err = a.Add(img.ExtractArea(ctx, image.Rect(0, 0, img.Width(), img.PageHeight()))); err != nil {
			return nil, err
		}
	}
	if img.Interpretation() != vips.InterpretationSRGB {
		if img, err = a.Add(img.Colourspace(ctx, vips.InterpretationSRGB)); err != nil {
			return nil, err
		}
	}
	if img.BandFormat() != vips.BandFormatUchar {
		if img, err = a.Add(img.Cast(ctx, vips.BandFormatUchar)); err != nil {
			return nil, err
		}
	}
	// Resample premultiplied so colour under transparent pixels, which
	// encoders may drop, does not bleed into the edges.
	alpha := img.HasAlpha()
	if alpha {
		if img, err = a.Add(img.Premultiply(ctx)); err != nil {
			return nil, err
		}
	}
	if c.scale < 1 {
		if img, err = a.Add(img.Resize(ctx, c.scale, 0, vips.KernelLinear)); err != nil {
			return nil, err
		}
	}
	if img.BandFormat() != vips.BandFormatUchar {
		if img, err = a.Add(img.Cast(ctx, vips.BandFormatUchar)); err != nil {
			return nil, err
		}
	}
	pix, err := img.WriteToMemory(ctx)
	if err != nil {
		return nil, err
	}
	w, h, bands := img.Width(), img.Height(), img.Bands()
	if bands < 1 || len(pix) != w*h*bands {
		return nil, errs.Newf(errs.KindInternal, "encode: unexpected comparison sample %dx%d×%d", w, h, bands)
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range w * h {
		p, o := pix[i*bands:(i+1)*bands], out.Pix[4*i:4*i+4:4*i+4]
		switch {
		case bands >= 4 && alpha:
			o[0], o[1], o[2], o[3] = p[0], p[1], p[2], p[3]
		case bands >= 3:
			o[0], o[1], o[2], o[3] = p[0], p[1], p[2], 0xff
		case bands == 2 && alpha:
			o[0], o[1], o[2], o[3] = p[0], p[0], p[0], p[1]
		default:
			o[0], o[1], o[2], o[3] = p[0], p[0], p[0], 0xff
		}
	}
	return out, nil
}
