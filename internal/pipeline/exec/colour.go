package exec

import (
	"bytes"
	"context"

	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// toProcessing converts img to the working colour space (imgproxy
// colorspaceToProcessing without HDR): an embedded ICC profile is imported
// unless it is sRGB IEC61966-2.1, then the pixels are converted to 8-bit
// sRGB, or 8-bit grey for grey sources. Alpha is kept.
func (r *runner) toProcessing(ctx context.Context, img *vips.Image) (*vips.Image, error) {
	src := img.Interpretation()
	target := targetSpace(src)
	var err error
	if icc, ok := img.Blob(vips.FieldICC); ok && importable(img, src, icc) {
		if img, err = r.importICC(ctx, img, target); err != nil {
			return nil, err
		}
		r.iccImported = true
		return img, nil
	}
	if src == target {
		return img, nil
	}
	return r.a.Add(img.Colourspace(ctx, target))
}

// targetSpace is imgproxy's guessTargetColorspace for outputs without HDR.
func targetSpace(i vips.Interpretation) vips.Interpretation {
	switch i {
	case vips.InterpretationSRGB, vips.InterpretationBW:
		return i
	case vips.InterpretationGrey16:
		return vips.InterpretationBW
	default: // RGB, RGB16, CMYK, Lab, scRGB, ...
		return vips.InterpretationSRGB
	}
}

// importable reports whether the embedded profile must be imported: 8- and
// 16-bit pixels only, and not when it is the standard sRGB profile, which is
// the same as no profile (imgproxy vips_icc_import_go).
func importable(img *vips.Image, i vips.Interpretation, icc []byte) bool {
	if len(icc) < 128 {
		return false
	}
	if bf := img.BandFormat(); bf != vips.BandFormatUchar && bf != vips.BandFormatUshort {
		return false
	}
	return i != vips.InterpretationSRGB || !isSRGBIEC61966(icc)
}

// isSRGBIEC61966 recognises the HP/Microsoft sRGB IEC61966-2.1 profile from
// its header, as imgproxy's vips_icc_is_srgb_iec61966 does.
func isSRGBIEC61966(h []byte) bool {
	return bytes.Equal(h[48:52], []byte("IEC ")) && // device manufacturer
		bytes.Equal(h[16:20], []byte("RGB ")) && // colour space
		bytes.Equal(h[52:56], []byte("sRGB")) && // device model
		bytes.Equal(h[80:84], []byte("HP  ")) && // profile creator
		bytes.Equal(h[24:30], []byte{7, 206, 0, 2, 0, 9}) && // created 1998-12-01
		bytes.Equal(h[8:12], []byte{2, 16, 0, 0}) // version 2.1
}

// importICC converts img from its embedded profile to target. libvips'
// icc_import does not rescale a 16-bit alpha band, so it is split off,
// brought to 8 bits and joined back (imgproxy vips_icc_import_go).
func (r *runner) importICC(ctx context.Context, img *vips.Image, target vips.Interpretation) (*vips.Image, error) {
	var alpha *vips.Image
	var err error
	if img.BandFormat() == vips.BandFormatUshort && img.HasAlpha() {
		n := img.Bands() - 1
		if alpha, err = r.a.Add(img.ExtractBand(ctx, n, 1)); err != nil {
			return nil, err
		}
		if alpha, err = r.a.Add(alpha.Linear(ctx, []float64{1.0 / 257}, []float64{0})); err != nil {
			return nil, err
		}
		if alpha, err = r.a.Add(alpha.Cast(ctx, vips.BandFormatUchar)); err != nil {
			return nil, err
		}
		if img, err = r.a.Add(img.ExtractBand(ctx, 0, n)); err != nil {
			return nil, err
		}
	}
	pcs, err := r.a.Add(img.ICCImport(ctx, "", vips.IntentRelative, true))
	if err != nil {
		return nil, err
	}
	out, err := r.a.Add(pcs.Colourspace(ctx, target))
	if err != nil {
		return nil, err
	}
	if alpha == nil {
		return out, nil
	}
	return r.a.Add(vips.Bandjoin(ctx, []*vips.Image{out, alpha}))
}

// toResult prepares the output colour: after an ICC import the pixels are
// sRGB, so the source profile no longer describes them and is dropped (an
// untagged image is sRGB). Keeping the original profile by converting back
// to it is not done (strip_color_profile=false keeps sRGB pixels untagged).
func (r *runner) toResult(ctx context.Context, img *vips.Image) (*vips.Image, error) {
	if !r.iccImported {
		return img, nil
	}
	if _, ok := img.Blob(vips.FieldICC); !ok {
		return img, nil
	}
	return r.a.Add(img.RemoveMetadata(ctx, vips.FieldICC))
}
