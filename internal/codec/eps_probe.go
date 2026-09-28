package codec

import (
	"context"
	"math"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// EPSDensity is the resolution an EPS page is measured at: one pixel per
// point, like PDF's default (the renderer then draws it at the density the
// plan asks for).
const EPSDensity = 72.0

// EPSPixels is a length of pts points in pixels at dpi, rounded up so the
// page is never clipped (a hair above an integer is not rounded up).
func EPSPixels(pts, dpi float64) int {
	return max(1, int(math.Ceil(pts*dpi/72-1e-6)))
}

// probeEPS describes an EPS/PostScript source. With Ghostscript (lim.EPSEnabled)
// it is the page box at EPSDensity, with alpha (pages render on a
// transparent background) and the %%Pages count; without, the embedded
// TIFF preview of a DOS EPS file, or errs.KindUnsupported.
func probeEPS(ctx context.Context, data []byte, lim Limits) (ImageMeta, error) {
	h, err := ParseEPSHeader(data)
	if err != nil {
		return ImageMeta{}, err
	}
	if !lim.EPSEnabled {
		return probeEPSPreview(ctx, data, &h)
	}
	w, ht := EPSPixels(h.Box.Width(), EPSDensity), EPSPixels(h.Box.Height(), EPSDensity)
	return ImageMeta{
		Format:      FormatEPS,
		Width:       w,
		Height:      ht,
		Frames:      1,
		Pages:       h.Pages,
		PageHeight:  ht,
		HasAlpha:    true,
		Orientation: 1,
		BitDepth:    8,
		Loop:        -1,
		ColorSpace:  ColorSpaceSRGB,
	}, nil
}

// probeEPSPreview describes the TIFF preview of a DOS EPS file.
func probeEPSPreview(ctx context.Context, data []byte, h *EPSHeader) (ImageMeta, error) {
	buf, err := epsPreview(data, h)
	if err != nil {
		return ImageMeta{}, err
	}
	defer buf.Release()
	m, err := probeVips(ctx, buf, FormatTIFF, 0)
	if err != nil {
		return ImageMeta{}, err
	}
	m.Format = FormatEPS
	m.Frames, m.Pages, m.PageHeight, m.IsAnimated, m.Delays = 1, 1, m.Height, false, nil
	return m, nil
}

// EPSPreview returns a C copy of the TIFF preview embedded in a DOS EPS
// file, for decoding it when Ghostscript is not available; the caller
// releases it. A file without one fails with errs.KindUnsupported and the
// public EPSDisabledMessage.
func EPSPreview(data []byte) (*vips.Buffer, error) {
	h, err := ParseEPSHeader(data)
	if err != nil {
		return nil, err
	}
	return epsPreview(data, &h)
}

func epsPreview(data []byte, h *EPSHeader) (*vips.Buffer, error) {
	tiff := h.TIFFPreview(data)
	if tiff == nil {
		return nil, errs.WithPublic(errs.New(errs.KindUnsupported, "codec: eps without ghostscript or a tiff preview"),
			EPSDisabledMessage)
	}
	buf, err := vips.NewBuffer(len(tiff))
	if err != nil {
		return nil, err
	}
	copy(buf.Bytes(), tiff)
	return buf, nil
}
