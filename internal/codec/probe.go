package codec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/codec/bmp"
	"github.com/xantiagoma/xtandard-image/internal/codec/ico"
	"github.com/xantiagoma/xtandard-image/internal/codec/raw"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/svg"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// Probe sniffs buf, reads only its headers and enforces lim before anything
// is decoded (ARCHITECTURE §2 stage 11):
//
//   - unrecognised input fails with errs.KindUnsupported, without reaching
//     a decoder;
//   - camera RAW headers are read by LibRaw without unpacking (codec/raw);
//   - BMP and ICO headers are parsed by the pure-Go loaders' config
//     functions, and PSD/PSB headers by ParsePSDHeader, never by libvips
//     (ImageMagick decodes a whole PSD even for its header);
//   - OpenEXR headers are parsed by ParseEXRHeader, so the pixel limits hold
//     before OpenEXR sizes anything from the data window;
//   - PDF pages are counted by PDFium's header read (nothing is rendered)
//     and more than Limits.MaxPDFPages fail with errs.KindLimitExceeded; a
//     PDF that needs a password fails with errs.KindUnsupported;
//   - SVG over Limits.MaxSVGBytes fails with errs.KindLimitExceeded, and any
//     other SVG is sanitised (security/svg, with the SVG depth and element
//     limits) before librsvg parses the sanitised copy's header;
//   - video containers (MP4, MOV, WebM, MKV, AVI) are read by FFmpeg
//     (internal/video/ffmpeg: container and stream headers, the
//     XI_VIDEO_* limits) when lim.Video.Enabled, and are
//     errs.KindUnsupported otherwise (and always without the ffmpeg build
//     tag); the frame size is the display size;
//   - every other format goes through vips.Probe, which runs the loader
//     header-only;
//   - the header is then checked against lim (Limits.Check): too many
//     frames or pixels fail with errs.KindLimitExceeded.
//
// Unreadable headers fail with errs.KindCorrupt. contentType is the type the
// origin declared: it is never used for detection, and a disagreement only
// sets ImageMeta.ContentTypeMismatch.
//
// Probe calls into libvips for most formats, so it must run inside vips.Run.
func Probe(ctx context.Context, buf *vips.Buffer, lim Limits, contentType string) (ImageMeta, error) {
	if err := ctxErr(ctx); err != nil {
		return ImageMeta{}, err
	}
	lim = lim.withDefaults()
	data := buf.Bytes()
	f, ok := Sniff(data)
	if !ok {
		return ImageMeta{}, errs.New(errs.KindUnsupported, "codec: unrecognised image format")
	}
	if !f.Supported() {
		return ImageMeta{}, errs.Newf(errs.KindUnsupported, "codec: %s input is not supported yet", f)
	}

	var (
		m   ImageMeta
		err error
	)
	switch f {
	case FormatBMP:
		m, err = probeBMP(data)
	case FormatICO:
		m, err = probeICO(data)
	case FormatSVG:
		m, err = probeSVG(ctx, data, lim)
	case FormatPSD:
		m, err = probePSD(data, lim)
	case FormatPDF:
		m, err = probePDF(ctx, buf, lim)
	case FormatRAW:
		m, err = probeRaw(ctx, buf)
	case FormatEXR:
		m, err = probeEXR(data)
	case FormatRadiance:
		m, err = probeRadiance(ctx, buf)
	case FormatMP4, FormatMOV, FormatWebM, FormatMKV, FormatAVI:
		m, err = probeVideo(ctx, buf, f, &lim.Video)
	default:
		m, err = probeVips(ctx, buf, f, 0)
	}
	if err != nil {
		return ImageMeta{}, fmt.Errorf("codec: probe %s: %w", f, err)
	}
	if err := lim.Check(&m); err != nil {
		return ImageMeta{}, err
	}
	m.HDR = m.Format.HDR()
	m.ContentTypeMismatch = !m.Format.MatchesContentType(contentType)
	return m, nil
}

// ctxErr maps a finished context to its errs kind.
func ctxErr(ctx context.Context) error {
	err := ctx.Err()
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.DeadlineExceeded):
		return errs.Wrap(errs.KindTimeout, err)
	default:
		return errs.Wrap(errs.KindCanceled, err)
	}
}

// probeRaw describes the demosaiced image: its size in the stored
// orientation and LibRaw's orientation. Pixels are 8-bit sRGB (grey for
// monochrome sensors) whichever path the executor takes.
func probeRaw(ctx context.Context, buf *vips.Buffer) (ImageMeta, error) {
	h, err := raw.Probe(ctx, buf)
	if err != nil {
		return ImageMeta{}, err
	}
	cs := ColorSpaceSRGB
	if h.Colors == 1 {
		cs = ColorSpaceGrey
	}
	return ImageMeta{
		Format:      FormatRAW,
		Width:       h.Width,
		Height:      h.Height,
		Frames:      1,
		Pages:       1,
		PageHeight:  h.Height,
		Orientation: h.Orientation,
		BitDepth:    8,
		Loop:        -1,
		ColorSpace:  cs,
	}, nil
}

func probeBMP(data []byte) (ImageMeta, error) {
	c, err := bmp.DecodeConfig(data)
	if err != nil {
		return ImageMeta{}, err
	}
	return ImageMeta{
		Format:      FormatBMP,
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
	}, nil
}

// probeICO describes the largest entry, the one that bounds the work of any
// entry the loader may pick. Icons always carry transparency (an alpha
// channel or the AND mask).
func probeICO(data []byte) (ImageMeta, error) {
	entries, err := ico.Entries(data)
	if err != nil {
		return ImageMeta{}, err
	}
	e := ico.Pick(entries, 0, 0)
	return ImageMeta{
		Format:      FormatICO,
		Width:       e.Width,
		Height:      e.Height,
		Frames:      1,
		Pages:       len(entries),
		PageHeight:  e.Height,
		HasAlpha:    true,
		Orientation: 1,
		BitDepth:    8,
		Loop:        -1,
		ColorSpace:  ColorSpaceSRGB,
	}, nil
}

// probeSVG enforces the byte limit, sanitises the document and probes the
// sanitised copy, so librsvg never parses untrusted markup.
func probeSVG(ctx context.Context, data []byte, lim Limits) (ImageMeta, error) {
	clean, err := SanitizeSVG(data, lim)
	if err != nil {
		return ImageMeta{}, err
	}
	sbuf, err := vips.NewBuffer(len(clean))
	if err != nil {
		return ImageMeta{}, err
	}
	defer sbuf.Release()
	copy(sbuf.Bytes(), clean)
	return probeVips(ctx, sbuf, FormatSVG, 0)
}

// probePSD reads the header in Go. The size is the canvas, pages are the
// merged image plus one per layer (the `page` option selects a layer the
// way ImageMagick numbers them), and files without a merged composite are
// refused when lim.PSDRequireComposite is set.
func probePSD(data []byte, lim Limits) (ImageMeta, error) {
	h, err := ParsePSDHeader(data)
	if err != nil {
		return ImageMeta{}, err
	}
	if !h.Composite && lim.PSDRequireComposite {
		return ImageMeta{}, errs.WithPublic(
			errs.New(errs.KindUnsupported, "codec: psd without a merged composite"),
			"photoshop file saved without a composite image is not supported")
	}
	bits := max(h.Depth, 8) // 1-bit bitmaps load as 8-bit grey
	return ImageMeta{
		Format:      FormatPSD,
		Width:       h.Width,
		Height:      h.Height,
		Frames:      1,
		Pages:       1 + h.Layers,
		PageHeight:  h.Height,
		HasAlpha:    h.HasAlpha(),
		Orientation: 1,
		BitDepth:    bits,
		Loop:        -1,
		HasICC:      h.ICC,
		ColorSpace:  h.ColorSpace(),
	}, nil
}

// probePDF measures the first page at lim.PDFDensity and counts the pages
// (PDFium reads the page tree, nothing is rendered). Pages render onto an
// opaque white background unless a request asks for transparency, so the
// source has no alpha.
func probePDF(ctx context.Context, buf *vips.Buffer, lim Limits) (ImageMeta, error) {
	m, err := probeVips(ctx, buf, FormatPDF, lim.PDFDensity)
	if err != nil {
		return ImageMeta{}, err
	}
	if m.Pages > lim.MaxPDFPages {
		return ImageMeta{}, errs.Newf(errs.KindLimitExceeded, "codec: pdf of %d pages exceeds the limit of %d",
			m.Pages, lim.MaxPDFPages)
	}
	m.HasAlpha = false
	m.BitDepth = 8
	return m, nil
}

// probeEXR reads the header in Go. libvips loads every OpenEXR image as
// four float bands (RGBA, A = 1 when the file has none); the probe reports
// the alpha the file really has.
func probeEXR(data []byte) (ImageMeta, error) {
	h, err := ParseEXRHeader(data)
	if err != nil {
		return ImageMeta{}, err
	}
	bits := 16
	if h.Float {
		bits = 32
	}
	return ImageMeta{
		Format:      FormatEXR,
		Width:       h.Width,
		Height:      h.Height,
		Frames:      1,
		Pages:       1,
		PageHeight:  h.Height,
		HasAlpha:    h.HasAlpha(),
		Orientation: 1,
		BitDepth:    bits,
		Loop:        -1,
		ColorSpace:  ColorSpaceScRGB,
	}, nil
}

// probeRadiance reads the Radiance header through libvips (text lines and
// the resolution string; nothing is decoded). The loader reports four
// RGBE-coded uchar bands; they decode to three float bands without alpha.
func probeRadiance(ctx context.Context, buf *vips.Buffer) (ImageMeta, error) {
	m, err := probeVips(ctx, buf, FormatRadiance, 0)
	if err != nil {
		return ImageMeta{}, err
	}
	// libvips guesses sRGB for any RGBE-coded image; the pixels are linear
	// (XYZE files too, converted to scRGB when decoded).
	m.HasAlpha = false
	m.BitDepth = 32
	m.ColorSpace = ColorSpaceScRGB
	return m, nil
}

// SanitizeSVG checks an SVG document against lim's SVG limits and returns
// the sanitised copy (security/svg) that is safe to hand to librsvg. Byte,
// depth and element limits fail with errs.KindLimitExceeded; malformed or
// hostile documents (DTDs, non-SVG roots) with errs.KindCorrupt.
func SanitizeSVG(data []byte, lim Limits) ([]byte, error) {
	lim = lim.withDefaults()
	if int64(len(data)) > lim.MaxSVGBytes {
		return nil, errs.Newf(errs.KindLimitExceeded, "codec: svg of %d bytes exceeds the limit of %d",
			len(data), lim.MaxSVGBytes)
	}
	var out bytes.Buffer
	out.Grow(len(data))
	err := svg.Sanitize(&out, bytes.NewReader(data), svg.Options{
		MaxBytes:    lim.MaxSVGBytes,
		MaxDepth:    lim.MaxSVGDepth,
		MaxElements: lim.MaxSVGElements,
	})
	switch {
	case err == nil:
		return out.Bytes(), nil
	case errors.Is(err, svg.ErrTooLarge), errors.Is(err, svg.ErrTooDeep), errors.Is(err, svg.ErrTooManyElements):
		return nil, errs.Wrap(errs.KindLimitExceeded, err)
	default:
		return nil, errs.Wrap(errs.KindCorrupt, err)
	}
}

func probeVips(ctx context.Context, buf *vips.Buffer, sniffed Format, dpi float64) (ImageMeta, error) {
	h, err := vips.ProbeDensity(ctx, buf, dpi)
	if err != nil {
		return ImageMeta{}, err
	}
	f, err := reconcile(sniffed, &h)
	if err != nil {
		return ImageMeta{}, err
	}
	// vips_image_get_n_pages reports 1 for 10000 pages or more, so a GIF
	// with 10k frames would pass as a still image; the delay array still has
	// one entry per frame.
	pages := max(h.Pages, len(h.Delay), 1)
	m := ImageMeta{
		Format:      f,
		Width:       h.Width,
		Height:      h.Height,
		Frames:      1,
		Pages:       pages,
		PageHeight:  h.PageHeight,
		HasAlpha:    h.HasAlpha,
		Orientation: h.Orientation,
		BitDepth:    h.BitDepth,
		IsAnimated:  pages > 1 && len(h.Delay) > 0,
		Delays:      h.Delay,
		Loop:        h.Loop,
		HasICC:      h.HasICC,
		ColorSpace:  colorSpaceOf(h.Interpretation),
	}
	if m.IsAnimated {
		m.Frames = m.Pages
	}
	return m, nil
}

// reconcile checks that the loader libvips chose agrees with the magic bytes
// and refines the sniffed format with what only the loader knows: the codec
// inside a generic HEIF container and UltraHDR gain maps. A disagreement
// means a polyglot or mislabelled file and is refused.
func reconcile(sniffed Format, h *vips.Header) (Format, error) {
	var got Format
	switch h.Format {
	case vips.FormatJPEG:
		got = FormatJPEG
		if strings.HasPrefix(h.Loader, "uhdrload") {
			got = FormatUHDR
		}
	case vips.FormatPNG:
		got = FormatPNG
	case vips.FormatWebP:
		got = FormatWebP
	case vips.FormatGIF:
		got = FormatGIF
	case vips.FormatAVIF:
		got = FormatAVIF
	case vips.FormatHEIF:
		got = FormatHEIC
	case vips.FormatJXL:
		got = FormatJXL
	case vips.FormatTIFF:
		got = FormatTIFF
	case vips.FormatSVG:
		got = FormatSVG
	case vips.FormatJP2K:
		got = FormatJP2
	case vips.FormatPDF:
		got = FormatPDF
	case vips.FormatPSD:
		got = FormatPSD
	case vips.FormatEXR:
		got = FormatEXR
	case vips.FormatRadiance:
		got = FormatRadiance
	case vips.FormatPPM:
		got = FormatPPM
		if h.BandFormat == vips.BandFormatFloat {
			got = FormatPFM
		}
	default:
		got = FormatUnknown
	}
	if family(got) != family(sniffed) || got == FormatUnknown {
		return FormatUnknown, errs.Newf(errs.KindUnsupported, "codec: sniffed %s but loader %s matched", sniffed, h.Loader)
	}
	return got, nil
}

// family groups the formats that share a libvips loader.
func family(f Format) Format {
	switch f {
	case FormatUHDR:
		return FormatJPEG
	case FormatAVIF, FormatHEIC:
		return FormatHEIF
	case FormatPFM:
		return FormatPPM
	default:
		return f
	}
}

func colorSpaceOf(i vips.Interpretation) ColorSpace {
	switch i {
	case vips.InterpretationSRGB, vips.InterpretationRGB, vips.InterpretationRGB16:
		return ColorSpaceSRGB
	case vips.InterpretationBW, vips.InterpretationGrey16:
		return ColorSpaceGrey
	case vips.InterpretationCMYK:
		return ColorSpaceCMYK
	case vips.InterpretationLab, vips.InterpretationLabQ, vips.InterpretationLabS, vips.InterpretationLCh:
		return ColorSpaceLab
	case vips.InterpretationScRGB:
		return ColorSpaceScRGB
	case vips.InterpretationError:
		return ColorSpaceUnknown
	default:
		return ColorSpaceOther
	}
}

// VipsFormat returns the libvips format of f, for the formats libvips loads
// itself (every supported format except BMP and ICO, which the Go loaders
// decode). UltraHDR is vips.FormatJPEG and HEIC/HEIF vips.FormatHEIF; PSD
// is loaded by ImageMagick (vips.FormatPSD), PPM and PFM by ppmload
// (vips.FormatPPM).
func (f Format) VipsFormat() (vips.Format, bool) {
	switch f {
	case FormatJPEG, FormatUHDR:
		return vips.FormatJPEG, true
	case FormatPNG:
		return vips.FormatPNG, true
	case FormatGIF:
		return vips.FormatGIF, true
	case FormatWebP:
		return vips.FormatWebP, true
	case FormatAVIF:
		return vips.FormatAVIF, true
	case FormatHEIC, FormatHEIF:
		return vips.FormatHEIF, true
	case FormatJXL:
		return vips.FormatJXL, true
	case FormatTIFF:
		return vips.FormatTIFF, true
	case FormatSVG:
		return vips.FormatSVG, true
	case FormatJP2:
		return vips.FormatJP2K, true
	case FormatPDF:
		return vips.FormatPDF, true
	case FormatPSD:
		return vips.FormatPSD, true
	case FormatEXR:
		return vips.FormatEXR, true
	case FormatRadiance:
		return vips.FormatRadiance, true
	case FormatPPM, FormatPFM:
		return vips.FormatPPM, true
	default:
		return vips.FormatUnknown, false
	}
}
