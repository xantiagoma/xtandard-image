package exec

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// toProcessing converts img to the working colour space (imgproxy
// colorspaceToProcessing without HDR; scene-linear sources go through
// fromHDR instead): an embedded ICC profile is imported
// unless it is sRGB IEC61966-2.1, then the pixels are converted to 8-bit
// sRGB, or 8-bit grey for grey sources. Alpha is kept. A profile is imported
// once: a chained pipeline sees pixels that are already sRGB but still carry
// the source profile, which must not be applied a second time.
func (r *runner) toProcessing(ctx context.Context, img *vips.Image) (*vips.Image, error) {
	if r.gainMapWanted() {
		hdr, ok, err := r.applyGainMap(ctx, img)
		if err != nil {
			return nil, err
		}
		if ok {
			return r.fromHDR(ctx, hdr)
		}
		// An unusable gain map: the SDR base goes on as a plain image.
		if r.req.Output.PreserveHDR != nil && *r.req.Output.PreserveHDR {
			r.skip("preserve_hdr")
		}
		if r.req.Output.GainMap == transform.GainMapHDR {
			r.skip("gain_map")
		}
	}
	if r.hdrPending() {
		return r.fromHDR(ctx, img)
	}
	src := img.Interpretation()
	target := targetSpace(src)
	var err error
	if icc, ok := img.Blob(vips.FieldICC); ok && !r.iccImported && importable(img, src, icc) {
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

// toResult prepares the output colour (FORMATS §4). Without an ICC import
// the image is still in its source space, whose profile (sRGB IEC61966-2.1,
// or none) the encoder keeps or drops per strip_color_profile. After an
// import the pixels are sRGB (or grey), so the source profile no longer
// describes them: it is dropped, and with strip_color_profile=false
// replaced by libvips' built-in sRGB (sGrey) profile. Converting back to the
// source profile, as imgproxy does, is not done.
//
// color_profile (imgproxy Pro) then converts the pixels to the requested
// profile and embeds it (colorProfile).
func (r *runner) toResult(ctx context.Context, img *vips.Image) (*vips.Image, error) {
	img, err := r.resultSpace(ctx, img)
	if err != nil {
		return nil, err
	}
	if name := r.req.Output.ColorProfile; name != "" {
		return r.colorProfile(ctx, img, name)
	}
	return img, nil
}

func (r *runner) resultSpace(ctx context.Context, img *vips.Image) (*vips.Image, error) {
	if !r.iccImported {
		return img, nil
	}
	if r.j.Target.Metadata.StripColorProfile {
		if _, ok := img.Blob(vips.FieldICC); !ok {
			return img, nil
		}
		return r.a.Add(img.RemoveMetadata(ctx, vips.FieldICC))
	}
	name := "srgb"
	if i := img.Interpretation(); i == vips.InterpretationBW || i == vips.InterpretationGrey16 {
		name = "sgrey"
	}
	icc, err := vips.ProfileLoad(ctx, name)
	if err != nil {
		return nil, err
	}
	if len(icc) == 0 {
		return r.a.Add(img.RemoveMetadata(ctx, vips.FieldICC))
	}
	return r.a.Add(img.SetBlob(ctx, vips.FieldICC, icc))
}

// colorProfile converts img to the ICC profile name, "srgb", "cmyk" or a
// file in Config.ColorProfilesDir, and embeds it (imgproxy Pro
// color_profile). CMYK is only produced for JPEG and TIFF, the formats that
// hold it; elsewhere, and for a file name without a configured directory,
// the option is skipped.
func (r *runner) colorProfile(ctx context.Context, img *vips.Image, name string) (*vips.Image, error) {
	var profile string
	switch strings.ToLower(name) {
	case "srgb":
		profile = "srgb"
	case "cmyk":
		if f := r.j.Target.Format; f != transform.FormatJPEG && f != transform.FormatTIFF {
			r.skip("color_profile")
			return img, nil
		}
		profile = "cmyk"
	default:
		dir := r.e.cfg.ColorProfilesDir
		if dir == "" || name != filepath.Base(name) {
			r.skip("color_profile")
			return img, nil
		}
		profile = filepath.Join(dir, name)
		if st, err := os.Stat(profile); err != nil || !st.Mode().IsRegular() {
			return nil, errs.New(errs.KindInvalidURL, "exec: unknown color profile")
		}
	}
	out, err := r.a.Add(img.ICCTransform(ctx, profile, vips.IntentRelative, true, 8))
	if err != nil {
		return nil, err
	}
	r.profileEmbedded = true
	return out, nil
}
