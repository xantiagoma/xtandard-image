package codec

import (
	"context"
	"fmt"

	"github.com/xantiagoma/xtandard-image/internal/codec/bmp"
	"github.com/xantiagoma/xtandard-image/internal/codec/icns"
	"github.com/xantiagoma/xtandard-image/internal/codec/ico"
	"github.com/xantiagoma/xtandard-image/internal/codec/raster"
	"github.com/xantiagoma/xtandard-image/internal/codec/wbmp"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// Sample decodes the first page or frame of buf (format f, as probed) to
// 8-bit sRGB RGBA with straight alpha, upright (EXIF orientation applied)
// and fitted inside maxSide×maxSide: the input of the pixel-derived /info
// fields (WP-63). A video is sampled from the frame its default selection
// picks (XI_VIDEO_THUMBNAIL_*), decoded at the sample size. libvips formats go through vips_thumbnail, so JPEG and
// WebP shrink on load and nothing larger than the result is kept in
// memory; the pure-Go BMP and ICO loaders return their pixels at full size
// (bounded by lim.MaxSrcPixels) and the caller fits them. Like Inspect it
// must run inside vips.Run.
func Sample(ctx context.Context, buf *vips.Buffer, f Format, maxSide int, lim Limits) (Raw, error) {
	return sample(ctx, buf, f, maxSide, lim, nil)
}

// SampleMedia is Sample for images and, when v enables them, video
// sources, sampled from the frame v's default selection picks.
func SampleMedia(ctx context.Context, buf *vips.Buffer, f Format, maxSide int, lim Limits, v *video.Settings) (Raw, error) {
	return sample(ctx, buf, f, maxSide, lim, v)
}

func sample(ctx context.Context, buf *vips.Buffer, f Format, maxSide int, lim Limits, v *video.Settings) (Raw, error) {
	if maxSide <= 0 {
		return Raw{}, errs.Newf(errs.KindInternal, "codec: sample size %d", maxSide)
	}
	switch f {
	case FormatBMP, FormatICO, FormatWBMP, FormatICNS:
		d, err := decodeOwn(buf.Bytes(), f, lim.withDefaults().MaxSrcPixels)
		if err != nil {
			return Raw{}, err
		}
		if d.Raw != nil {
			return rgba(d.Raw)
		}
		emb, err := vips.NewBuffer(len(d.Embedded))
		if err != nil {
			return Raw{}, err
		}
		defer emb.Release()
		copy(emb.Bytes(), d.Embedded)
		return sampleVips(ctx, emb, maxSide)
	case FormatMP4, FormatMOV, FormatWebM, FormatMKV, FormatAVI:
		return sampleVideo(ctx, buf, maxSide, v)
	case FormatEPS:
		// Never Ghostscript: the embedded preview, when there is one.
		pre, err := EPSPreview(buf.Bytes())
		if err != nil {
			return Raw{}, err
		}
		defer pre.Release()
		return sampleVips(ctx, pre, maxSide)
	case FormatXCF:
		var a vips.Arena
		defer a.Close()
		img, err := flattenXCFSample(ctx, &a, buf, maxSide, lim)
		if err != nil {
			return Raw{}, err
		}
		return sampleImage(ctx, &a, img)
	default:
		return sampleVips(ctx, buf, maxSide)
	}
}

// IsOwn reports the formats decoded by the pure-Go loaders (DecodeOwn):
// BMP, ICO, WBMP and ICNS.
func (f Format) IsOwn() bool {
	return f == FormatBMP || f == FormatICO || f == FormatWBMP || f == FormatICNS
}

// DecodeOwn runs the pure-Go loader of f (IsOwn), bounded by maxPixels:
// the pixels, or a PNG/JPEG/JPEG 2000 stream embedded in the container for
// libvips to load.
func DecodeOwn(data []byte, f Format, maxPixels int64) (raster.Decoded, error) {
	return decodeOwn(data, f, maxPixels)
}

// decodeOwn runs the pure-Go loader of f.
func decodeOwn(data []byte, f Format, maxPixels int64) (raster.Decoded, error) {
	var d raster.Decoded
	var err error
	switch f {
	case FormatBMP:
		d, err = bmp.Decode(data, bmp.Options{MaxPixels: maxPixels})
	case FormatICO:
		d, err = ico.Decode(data, ico.Options{MaxPixels: maxPixels})
	case FormatWBMP:
		d, err = wbmp.Decode(data, wbmp.Options{MaxPixels: maxPixels})
	case FormatICNS:
		d, err = icns.Decode(data, icns.Options{MaxPixels: maxPixels})
	default:
		return d, errs.Newf(errs.KindInternal, "codec: %s has no own loader", f)
	}
	if err != nil {
		switch errs.KindOf(err) {
		case errs.KindLimitExceeded, errs.KindUnsupported, errs.KindCorrupt:
			return d, err
		default:
			return d, errs.Wrapf(errs.KindCorrupt, err, "codec: sample %s", f)
		}
	}
	if d.Raw == nil && d.Embedded == nil {
		return d, errs.Newf(errs.KindCorrupt, "codec: sample %s: decoder returned no pixels", f)
	}
	if d.Raw != nil {
		if err := d.Raw.Validate(); err != nil {
			return d, errs.Wrap(errs.KindCorrupt, err)
		}
	}
	return d, nil
}

// rgba returns r with four bands, adding opaque alpha to RGB.
func rgba(r *Raw) (Raw, error) {
	if r.Bands == 4 {
		return *r, nil
	}
	out := Raw{Width: r.Width, Height: r.Height, Bands: 4, Data: make([]byte, r.Width*r.Height*4)}
	for i := range r.Width * r.Height {
		copy(out.Data[i*4:i*4+3], r.Data[i*3:i*3+3])
		out.Data[i*4+3] = 255
	}
	return out, nil
}

// sampleVips thumbnails buf's first page to maxSide and exports it as
// uchar sRGB with alpha.
func sampleVips(ctx context.Context, buf *vips.Buffer, maxSide int) (Raw, error) {
	var a vips.Arena
	defer a.Close()
	img, err := a.Add(vips.Thumbnail(ctx, buf, &vips.ThumbnailOptions{
		Width: maxSide, Height: maxSide, Size: vips.SizeDown, FailOn: vips.FailOnError,
	}))
	if err != nil {
		return Raw{}, fmt.Errorf("codec: sample: %w", err)
	}
	return sampleImage(ctx, &a, img)
}

// flattenXCFSample flattens an XCF source (FlattenXCF) and fits it inside
// maxSide×maxSide.
func flattenXCFSample(ctx context.Context, a *vips.Arena, buf *vips.Buffer, maxSide int, lim Limits) (*vips.Image, error) {
	img, err := FlattenXCF(ctx, a, buf, lim.withDefaults().MaxSrcPixels, vips.FailOnError)
	if err != nil {
		return nil, err
	}
	if s := float64(maxSide) / float64(max(img.Width(), img.Height())); s < 1 {
		return a.Add(img.Resize(ctx, s, s, vips.KernelLanczos3))
	}
	return img, nil
}

// sampleImage exports img as uchar sRGB with alpha.
func sampleImage(ctx context.Context, a *vips.Arena, img *vips.Image) (Raw, error) {
	var err error
	if img.Interpretation() != vips.InterpretationSRGB || img.BandFormat() != vips.BandFormatUchar {
		if img, err = a.Add(img.Colourspace(ctx, vips.InterpretationSRGB)); err != nil {
			return Raw{}, fmt.Errorf("codec: sample: %w", err)
		}
		if img.BandFormat() != vips.BandFormatUchar {
			if img, err = a.Add(img.CastRound(ctx, vips.BandFormatUchar)); err != nil {
				return Raw{}, fmt.Errorf("codec: sample: %w", err)
			}
		}
	}
	switch n := img.Bands(); {
	case n == 3:
		img, err = a.Add(img.BandjoinConst(ctx, []float64{255}))
	case n > 4:
		img, err = a.Add(img.ExtractBand(ctx, 0, 4))
	case n < 3:
		err = errs.Newf(errs.KindInternal, "codec: sample: %d bands after sRGB conversion", n)
	}
	if err != nil {
		return Raw{}, fmt.Errorf("codec: sample: %w", err)
	}
	w, h := img.Width(), img.Height()
	if ph := img.PageHeight(); ph > 0 && ph < h {
		h = ph // a loader that returned a strip: keep the first page
	}
	pix, err := img.WriteToMemory(ctx)
	if err != nil {
		return Raw{}, fmt.Errorf("codec: sample: %w", err)
	}
	if len(pix) < w*h*4 {
		return Raw{}, errs.Newf(errs.KindInternal, "codec: sample: %d bytes for %dx%d", len(pix), w, h)
	}
	return Raw{Width: w, Height: h, Bands: 4, Data: pix[:w*h*4]}, nil
}
