package codec

import (
	"context"

	"github.com/xantiagoma/xtandard-image/internal/codec/ktx2"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// vipsKTX2 binds codec/ktx2 to the parts of internal/vips it needs: the
// Basis Universal transcoder and Zstandard (WP-108).
type vipsKTX2 struct{}

func (vipsKTX2) BasisInfo(ctx context.Context, data []byte) (ktx2.BasisInfo, error) {
	i, err := vips.BasisKTX2Info(ctx, data)
	if err != nil {
		return ktx2.BasisInfo{}, err
	}
	return ktx2.BasisInfo{Width: i.Width, Height: i.Height, Alpha: i.Alpha, HDR: i.HDR}, nil
}

func (vipsKTX2) Basis(ctx context.Context, data []byte, maxPixels int64) (ktx2.BasisImage, error) {
	img, err := vips.DecodeBasisKTX2(ctx, data, maxPixels)
	if err != nil {
		return ktx2.BasisImage{}, err
	}
	return ktx2.BasisImage{
		BasisInfo: ktx2.BasisInfo{Width: img.Width, Height: img.Height, Alpha: img.Alpha, HDR: img.HDR},
		RGBA8:     img.RGBA8,
		RGBAHalf:  img.RGBAHalf,
	}, nil
}

func (vipsKTX2) Zstd(ctx context.Context, dst, src []byte) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	return vips.ZstdDecompress(dst, src)
}

// probeKTX2 describes the first image of a KTX2 texture from its header
// (and, for a Basis Universal payload, the transcoder's header read): 8-bit
// sRGB, grey for one-channel formats, or scene-linear float for BC6H, the
// float formats and UASTC/ASTC HDR, tone-mapped like OpenEXR (HDR).
func probeKTX2(ctx context.Context, data []byte) (ImageMeta, error) {
	c, err := ktx2.DecodeConfig(ctx, data, vipsKTX2{})
	if err != nil {
		return ImageMeta{}, err
	}
	m := ImageMeta{
		Format:      FormatKTX2,
		Width:       c.Width,
		Height:      c.Height,
		Frames:      1,
		Pages:       1,
		PageHeight:  c.Height,
		HasAlpha:    c.Alpha,
		Orientation: 1,
		BitDepth:    8,
		Loop:        -1,
		ColorSpace:  ColorSpaceSRGB,
	}
	switch {
	case c.HDR:
		m.BitDepth, m.ColorSpace, m.HDR = 32, ColorSpaceScRGB, true
	case c.Grey:
		m.ColorSpace = ColorSpaceGrey
	}
	return m, nil
}
