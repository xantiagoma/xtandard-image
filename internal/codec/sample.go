package codec

import (
	"context"
	"fmt"

	"github.com/xantiagoma/xtandard-image/internal/codec/bmp"
	"github.com/xantiagoma/xtandard-image/internal/codec/ico"
	"github.com/xantiagoma/xtandard-image/internal/codec/raster"
	"github.com/xantiagoma/xtandard-image/internal/errs"
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
	if maxSide <= 0 {
		return Raw{}, errs.Newf(errs.KindInternal, "codec: sample size %d", maxSide)
	}
	switch f {
	case FormatBMP, FormatICO:
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
		return sampleVideo(ctx, buf, maxSide, &lim.Video)
	default:
		return sampleVips(ctx, buf, maxSide)
	}
}

// decodeOwn runs the pure-Go loader of f.
func decodeOwn(data []byte, f Format, maxPixels int64) (raster.Decoded, error) {
	var d raster.Decoded
	var err error
	if f == FormatBMP {
		d, err = bmp.Decode(data, bmp.Options{MaxPixels: maxPixels})
	} else {
		d, err = ico.Decode(data, ico.Options{MaxPixels: maxPixels})
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
