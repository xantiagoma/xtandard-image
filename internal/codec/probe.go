package codec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/codec/bmp"
	"github.com/xantiagoma/xtandard-image/internal/codec/ico"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/svg"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// Probe sniffs buf, reads only its headers and enforces lim before anything
// is decoded (ARCHITECTURE §2 stage 11):
//
//   - unrecognised input and formats of a later milestone (PDF, PSD, RAW)
//     fail with errs.KindUnsupported, without reaching a decoder;
//   - BMP and ICO headers are parsed by the pure-Go loaders' config
//     functions, never by libvips;
//   - SVG over Limits.MaxSVGBytes fails with errs.KindLimitExceeded, and any
//     other SVG is sanitised (security/svg, with the SVG depth and element
//     limits) before librsvg parses the sanitised copy's header;
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
	default:
		m, err = probeVips(ctx, buf, f)
	}
	if err != nil {
		return ImageMeta{}, fmt.Errorf("codec: probe %s: %w", f, err)
	}
	if err := lim.Check(&m); err != nil {
		return ImageMeta{}, err
	}
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
	return probeVips(ctx, sbuf, FormatSVG)
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

func probeVips(ctx context.Context, buf *vips.Buffer, sniffed Format) (ImageMeta, error) {
	h, err := vips.Probe(ctx, buf)
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
// decode). UltraHDR is vips.FormatJPEG and HEIC/HEIF vips.FormatHEIF.
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
	default:
		return vips.FormatUnknown, false
	}
}
