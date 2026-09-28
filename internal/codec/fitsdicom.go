package codec

import (
	"context"
	"encoding/binary"
	"math"

	"github.com/xantiagoma/xtandard-image/internal/codec/dicom"
	"github.com/xantiagoma/xtandard-image/internal/codec/fits"
	"github.com/xantiagoma/xtandard-image/internal/codec/raster"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// probeFITS describes a FITS image from its headers (codec/fits): it is
// decoded to 8-bit RGB (a grey image replicated, a 3-plane cube as colour),
// stretched for display, so the pipeline sees 8-bit sRGB.
func probeFITS(data []byte) (ImageMeta, error) {
	c, err := fits.DecodeConfig(data)
	if err != nil {
		return ImageMeta{}, err
	}
	cs := ColorSpaceGrey
	if c.Colour {
		cs = ColorSpaceSRGB
	}
	return ImageMeta{
		Format:      FormatFITS,
		Width:       c.Width,
		Height:      c.Height,
		Frames:      1,
		Pages:       1,
		PageHeight:  c.Height,
		Orientation: 1,
		BitDepth:    8,
		Loop:        -1,
		ColorSpace:  cs,
	}, nil
}

// probeDICOM describes the first frame of a DICOM file from its dataset
// (codec/dicom, which reads the pixel description only, never patient
// data): it is rendered to 8-bit RGB (windowed; grey replicated). A frame
// compressed with a codec the image has no decoder for is refused here:
// JPEG frames the standalone JPEG probe refuses (hierarchical, arithmetic
// lossless, precisions other than 8 and 12 bits for DCT), and JPEG-LS.
// 12-bit DCT and lossless JPEG frames decode through the shim's
// jpeghpload (WP-106).
func probeDICOM(data []byte) (ImageMeta, error) {
	h, err := dicom.ParseHeader(data)
	if err != nil {
		return ImageMeta{}, err
	}
	if h.Encoding.Codec() {
		if _, err := dicomCodestream(data, &h); err != nil {
			return ImageMeta{}, err
		}
	}
	cs := ColorSpaceGrey
	if h.Samples == 3 || h.Photometric == "PALETTE COLOR" {
		cs = ColorSpaceSRGB
	}
	return ImageMeta{
		Format:      FormatDICOM,
		Width:       h.Width,
		Height:      h.Height,
		Frames:      1,
		Pages:       1,
		PageHeight:  h.Height,
		Orientation: 1,
		BitDepth:    8,
		Loop:        -1,
		ColorSpace:  cs,
	}, nil
}

// dicomCodestream extracts the first frame's codestream and checks that
// the image has a decoder for it.
func dicomCodestream(data []byte, h *dicom.Header) ([]byte, error) {
	if h.Encoding == dicom.EncodingJPEGLS {
		return nil, errs.WithPublic(errs.New(errs.KindUnsupported, "codec: dicom: JPEG-LS frame"),
			"JPEG-LS compressed DICOM frames are not supported")
	}
	cs, err := dicom.FirstFrame(data, h)
	if err != nil {
		return nil, err
	}
	f, ok := Sniff(cs)
	switch {
	case !ok || (f != FormatJPEG && f != FormatJP2):
		return nil, errs.Newf(errs.KindCorrupt, "codec: dicom: %s frame is not a JPEG or JPEG 2000 codestream", h.Encoding)
	case f == FormatJPEG:
		if err := checkJPEGFrame(cs); err != nil {
			return nil, err
		}
	}
	return cs, nil
}

// decodeDICOM renders the first frame of a DICOM file to 8-bit RGB: native,
// deflated and RLE frames in Go (codec/dicom), JPEG and JPEG 2000 frames
// decoded by libvips from a C copy of the codestream, then windowed in Go
// like the native ones. It must run inside vips.Run.
func decodeDICOM(ctx context.Context, data []byte, maxPixels int64) (*raster.Raw, error) {
	h, err := dicom.ParseHeader(data)
	if err != nil {
		return nil, err
	}
	if !h.Encoding.Codec() {
		return dicom.Decode(data, dicom.Options{MaxPixels: maxPixels})
	}
	if int64(h.Width)*int64(h.Height) > maxPixels {
		return nil, errs.Newf(errs.KindLimitExceeded, "codec: dicom: %dx%d exceeds the pixel limit of %d",
			h.Width, h.Height, maxPixels)
	}
	cs, err := dicomCodestream(data, &h)
	if err != nil {
		return nil, err
	}
	buf, err := vips.NewBuffer(len(cs))
	if err != nil {
		return nil, err
	}
	defer buf.Release()
	copy(buf.Bytes(), cs)
	var a vips.Arena
	defer a.Close()
	img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{
		Access: vips.AccessSequential, FailOn: vips.FailOnError, MaxPixels: maxPixels,
	}))
	if err != nil {
		return nil, err
	}
	if img.Width() != h.Width || img.Height() != h.Height {
		return nil, errs.Newf(errs.KindCorrupt, "codec: dicom: %dx%d frame, header %dx%d",
			img.Width(), img.Height(), h.Width, h.Height)
	}
	bands := img.Bands()
	if bands == 2 || bands == 4 { // an alpha band the codestream carries: not DICOM's
		if img, err = a.Add(img.ExtractBand(ctx, 0, bands-1)); err != nil {
			return nil, err
		}
		bands--
	}
	px, err := img.WriteToMemory(ctx)
	if err != nil {
		return nil, err
	}
	samples, err := dicomSamples(px, img.BandFormat(), img.Width()*img.Height()*bands)
	if err != nil {
		return nil, err
	}
	if _, bits, ok := JPEGFrame(cs); ok {
		jpegStoredSamples(samples, bits, img.BandFormat(), &h)
	}
	return h.Render(dicom.Samples{
		Width: img.Width(), Height: img.Height(), Bands: bands, Data: samples,
		// libjpeg and OpenJPEG return RGB for colour frames whatever the
		// photometric interpretation (YBR) says.
		Decoded: bands == 3,
	})
}

// jpegStoredSamples turns the samples of a JPEG frame of the given
// precision back into stored values. jpeghpload (WP-106) scales 9–16-bit
// samples to 0–65535 and 2–7-bit ones to 0–255, like any 16- or 8-bit
// image; DICOM's rescale and window apply to the stored values, and a
// signed frame (PixelRepresentation 1) keeps its two's complement pattern
// in the low BitsStored bits of the decoded sample.
func jpegStoredSamples(s []int32, bits int, f vips.BandFormat, h *dicom.Header) {
	full := 0
	switch {
	case f == vips.BandFormatUshort && bits > 8 && bits < 16:
		full = 65535
	case f == vips.BandFormatUchar && bits >= 2 && bits < 8:
		full = 255
	}
	if full != 0 {
		maxv := float64(int(1)<<bits - 1)
		for i, v := range s {
			s[i] = int32(math.Round(float64(v) * maxv / float64(full)))
		}
	}
	if !h.Signed || h.Samples != 1 || h.BitsStored < 2 || h.BitsStored > bits {
		return
	}
	mask := int32(1)<<h.BitsStored - 1
	sign := int32(1) << (h.BitsStored - 1)
	for i, v := range s {
		if v &= mask; v&sign != 0 {
			v -= mask + 1
		}
		s[i] = v
	}
}

// dicomSamples converts libvips' pixels (native byte order) to stored
// sample values.
func dicomSamples(px []byte, f vips.BandFormat, n int) ([]int32, error) {
	out := make([]int32, n)
	ne := binary.NativeEndian
	switch f {
	case vips.BandFormatUchar:
		if len(px) < n {
			break
		}
		for i := range out {
			out[i] = int32(px[i])
		}
		return out, nil
	case vips.BandFormatChar:
		if len(px) < n {
			break
		}
		for i := range out {
			out[i] = int32(int8(px[i])) //nolint:gosec // signed samples
		}
		return out, nil
	case vips.BandFormatUshort:
		if len(px) < 2*n {
			break
		}
		for i := range out {
			out[i] = int32(ne.Uint16(px[2*i:]))
		}
		return out, nil
	case vips.BandFormatShort:
		if len(px) < 2*n {
			break
		}
		for i := range out {
			out[i] = int32(int16(ne.Uint16(px[2*i:]))) //nolint:gosec // signed samples
		}
		return out, nil
	case vips.BandFormatUint, vips.BandFormatInt:
		if len(px) < 4*n {
			break
		}
		for i := range out {
			v := ne.Uint32(px[4*i:])
			if f == vips.BandFormatUint {
				out[i] = int32(min(v, math.MaxInt32))
			} else {
				out[i] = int32(v) //nolint:gosec // signed samples
			}
		}
		return out, nil
	default:
		return nil, errs.Newf(errs.KindUnsupported, "codec: dicom: codec frame decoded to band format %d", int(f))
	}
	return nil, errs.Newf(errs.KindInternal, "codec: dicom: %d bytes for %d samples", len(px), n)
}
