package encode

import (
	"context"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/codec/bmp"
	"github.com/xantiagoma/xtandard-image/internal/codec/ico"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// maxICOSide is the largest icon side an ICO entry can describe.
const maxICOSide = 256

// encodeGo writes BMP or ICO with the pure-Go encoders. img is one frame.
func encodeGo(ctx context.Context, a *vips.Arena, img *vips.Image, f transform.Format) (Body, error) {
	if f == transform.FormatICO && (img.Width() > maxICOSide || img.Height() > maxICOSide) {
		return nil, errs.Newf(errs.KindLimitExceeded,
			"encode: ico is at most %dx%d, image is %dx%d", maxICOSide, maxICOSide, img.Width(), img.Height())
	}
	raw, err := toRaw(ctx, a, img)
	if err != nil {
		return nil, err
	}
	var out []byte
	if f == transform.FormatICO {
		out, err = ico.Encode(raw)
	} else {
		out, err = bmp.Encode(raw)
	}
	if err != nil {
		return nil, errs.Wrapf(errs.KindInternal, err, "encode: %s", f)
	}
	return &goBody{b: out}, nil
}

// toRaw converts img to 8-bit sRGB with 3 or 4 bands and copies its pixels
// out (codec.Raw's contract).
func toRaw(ctx context.Context, a *vips.Arena, img *vips.Image) (*codec.Raw, error) {
	var err error
	if img.Interpretation() != vips.InterpretationSRGB || (img.Bands() != 3 && img.Bands() != 4) {
		if img, err = a.Add(img.Colourspace(ctx, vips.InterpretationSRGB)); err != nil {
			return nil, err
		}
	}
	if img.Bands() > 4 {
		if img, err = a.Add(img.ExtractBand(ctx, 0, 4)); err != nil {
			return nil, err
		}
	}
	if img.BandFormat() != vips.BandFormatUchar {
		if img, err = a.Add(img.Cast(ctx, vips.BandFormatUchar)); err != nil {
			return nil, err
		}
	}
	if b := img.Bands(); b != 3 && b != 4 {
		return nil, errs.Newf(errs.KindInternal, "encode: %d bands after sRGB conversion", b)
	}
	pix, err := img.WriteToMemory(ctx)
	if err != nil {
		return nil, err
	}
	return &codec.Raw{
		Width:  img.Width(),
		Height: img.Height(),
		Bands:  img.Bands(),
		Data:   pix,
		XRes:   img.XRes(),
		YRes:   img.YRes(),
	}, nil
}
