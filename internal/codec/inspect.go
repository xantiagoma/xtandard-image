package codec

import (
	"context"
	"fmt"

	"github.com/xantiagoma/xtandard-image/internal/codec/imagemeta"
	"github.com/xantiagoma/xtandard-image/internal/codec/raw"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// Info is what the /info endpoint reports about a source
// (docs/URL-DIALECTS.md §3.3): the probed header plus the band layout and the
// EXIF, IPTC and XMP metadata the loader attaches. Nothing is decoded.
type Info struct {
	Meta ImageMeta
	// Bands is the number of bands, alpha included.
	Bands int
	// SampleFormat is the libvips band format nickname ("uchar", "ushort",
	// "float", ...).
	SampleFormat string
	// Colorspace is the libvips interpretation nickname ("srgb", "b-w",
	// "cmyk", "rgb16", "grey16", ...).
	Colorspace string
	// EXIF holds the parsed EXIF tags in libvips order.
	EXIF []EXIFTag
	// IPTC is the raw "iptc-data" blob (a Photoshop resource section from
	// JPEG APP13, raw IPTC-IIM from TIFF); see imagemeta.ParseIPTC.
	IPTC []byte
	// XMP is the raw XMP packet; see imagemeta.ParseXMP.
	XMP []byte
	// HDR mirrors Meta.HDR: scene-linear samples (OpenEXR, Radiance, PFM).
	HDR bool
	// Video mirrors Meta.Video: the container, its tags and streams.
	Video *video.Meta
}

// EXIFTag is one EXIF tag as libexif (through libvips) reads it.
type EXIFTag struct {
	IFD int // 0 = IFD0, 1 = thumbnail, 2 = EXIF, 3 = GPS, 4 = interoperability
	// Name is libexif's canonical tag name ("DateTimeOriginal").
	Name string
	// Title is libexif's human-readable title ("Date and Time (Original)"),
	// the key imgproxy reports; Name when libexif has none.
	Title string
	// Value is libexif's human-readable value ("8.00 EV (f/16.0)").
	Value string
}

// Inspect probes buf like Probe (same limits and errors) and additionally
// reads the band layout and the metadata fields libvips attaches to the
// image header. BMP and ICO (pure-Go loaders), SVG and PSD carry no EXIF,
// IPTC or XMP here; their band layout follows from the probe (the
// Photoshop header for PSD). Camera RAW reports the camera metadata LibRaw
// reads as EXIF (codec/raw.EXIF). Like Probe it must run inside vips.Run.
func Inspect(ctx context.Context, buf *vips.Buffer, lim Limits, contentType string) (Info, error) {
	return inspect(ctx, buf, lim, nil, contentType)
}

// InspectMedia is Inspect for images and, when v enables them, video
// sources (ProbeMedia): a video reports its frame's band layout and its
// container metadata (Info.Video).
func InspectMedia(ctx context.Context, buf *vips.Buffer, lim Limits, v *video.Settings, contentType string) (Info, error) {
	return inspect(ctx, buf, lim, v, contentType)
}

func inspect(ctx context.Context, buf *vips.Buffer, lim Limits, v *video.Settings, contentType string) (Info, error) {
	m, err := probe(ctx, buf, lim, v, contentType)
	if err != nil {
		return Info{}, err
	}
	info := Info{Meta: m, HDR: m.HDR}
	if m.Format == FormatRAW {
		if err := inspectRaw(ctx, buf, &info); err != nil {
			return Info{}, err
		}
		return info, nil
	}
	switch m.Format {
	case FormatBMP, FormatICO, FormatSVG, FormatWBMP, FormatICNS, FormatEPS, FormatXCF:
		info.Bands = 3
		if m.HasAlpha || m.Format == FormatSVG {
			info.Bands = 4
		}
		info.SampleFormat, info.Colorspace = "uchar", "srgb"
		return info, nil
	case FormatPSD:
		// ImageMagick decodes the whole file even for a header: describe
		// the merged image from the Photoshop header instead.
		h, err := ParsePSDHeader(buf.Bytes())
		if err != nil {
			return Info{}, err
		}
		info.Bands, info.SampleFormat, info.Colorspace = psdLayout(&h)
		return info, nil
	case FormatMP4, FormatMOV, FormatWebM, FormatMKV, FormatAVI:
		// A frame decodes to 8-bit sRGB, RGBA when the pixel format has
		// alpha.
		info.Bands = 3
		if m.HasAlpha {
			info.Bands = 4
		}
		info.SampleFormat, info.Colorspace = "uchar", "srgb"
		info.Video = m.Video
		return info, nil
	case FormatEXR:
		// Described from the Go header: loading would spool the file for
		// OpenEXR. The pipeline sees float scRGB, alpha only when present.
		info.Bands = 3
		if m.HasAlpha {
			info.Bands = 4
		}
		info.SampleFormat, info.Colorspace = "float", "scrgb"
		return info, nil
	default:
	}
	if err := ctxErr(ctx); err != nil {
		return Info{}, err
	}
	// A header-only load: nothing below requests pixels.
	img, err := vips.Load(ctx, buf, &vips.LoadOptions{Access: vips.AccessSequential, FailOn: vips.FailOnError})
	if err != nil {
		return Info{}, fmt.Errorf("codec: inspect %s: %w", m.Format, err)
	}
	defer img.Close()
	info.Bands = img.Bands()
	if m.Format == FormatPDF && !m.HasAlpha {
		info.Bands = 3 // PDFium's RGBA, rendered opaque by default (probePDF)
	}
	if m.Format == FormatRadiance {
		// Four RGBE-coded bytes per pixel decode to three float bands of
		// linear light (libvips guesses sRGB for coded images; XYZE files
		// are converted to scRGB when decoded).
		info.Bands = 3
		info.SampleFormat = bandFormatName(vips.BandFormatFloat)
		info.Colorspace = interpretationName(vips.InterpretationScRGB)
		return info, nil
	}
	info.SampleFormat = bandFormatName(img.BandFormat())
	info.Colorspace = interpretationName(img.Interpretation())
	for _, f := range img.Fields() {
		ifd, name, ok := imagemeta.EXIFField(string(f))
		if !ok {
			continue
		}
		raw, ok := img.Text(f)
		if !ok {
			continue
		}
		title, ok := vips.EXIFTitle(ifd, name)
		if !ok {
			title = name
		}
		info.EXIF = append(info.EXIF, EXIFTag{IFD: ifd, Name: name, Title: title, Value: imagemeta.EXIFValue(raw)})
	}
	if b, ok := img.Blob(vips.FieldIPTC); ok {
		info.IPTC = b
	}
	if b, ok := img.Blob(vips.FieldXMP); ok {
		info.XMP = b
	}
	return info, nil
}

// inspectRaw describes a RAW file from its LibRaw header (nothing is
// unpacked): 8-bit sRGB or grey output and the camera metadata as EXIF.
func inspectRaw(ctx context.Context, buf *vips.Buffer, info *Info) error {
	h, err := raw.Probe(ctx, buf)
	if err != nil {
		return fmt.Errorf("codec: inspect raw: %w", err)
	}
	info.Bands, info.SampleFormat, info.Colorspace = 3, "uchar", "srgb"
	if h.Colors == 1 {
		info.Bands, info.Colorspace = 1, "b-w"
	}
	for _, t := range raw.EXIF(&h.RawHeader) {
		title, ok := vips.EXIFTitle(t.IFD, t.Name)
		if !ok {
			title = t.Name
		}
		info.EXIF = append(info.EXIF, EXIFTag{IFD: t.IFD, Name: t.Name, Title: title, Value: t.Value})
	}
	return nil
}

// bandFormatName is the libvips nickname of f (VipsBandFormat).
func bandFormatName(f vips.BandFormat) string {
	switch f {
	case vips.BandFormatUchar:
		return "uchar"
	case vips.BandFormatChar:
		return "char"
	case vips.BandFormatUshort:
		return "ushort"
	case vips.BandFormatShort:
		return "short"
	case vips.BandFormatUint:
		return "uint"
	case vips.BandFormatInt:
		return "int"
	case vips.BandFormatFloat:
		return "float"
	case vips.BandFormatComplex:
		return "complex"
	case vips.BandFormatDouble:
		return "double"
	case vips.BandFormatDpcomplex:
		return "dpcomplex"
	default:
		return "notset"
	}
}

// interpretationName is the libvips nickname of i (VipsInterpretation).
func interpretationName(i vips.Interpretation) string {
	switch i {
	case vips.InterpretationMultiband:
		return "multiband"
	case vips.InterpretationBW:
		return "b-w"
	case vips.InterpretationHistogram:
		return "histogram"
	case vips.InterpretationXYZ:
		return "xyz"
	case vips.InterpretationLab:
		return "lab"
	case vips.InterpretationCMYK:
		return "cmyk"
	case vips.InterpretationLabQ:
		return "labq"
	case vips.InterpretationRGB:
		return "rgb"
	case vips.InterpretationCMC:
		return "cmc"
	case vips.InterpretationLCh:
		return "lch"
	case vips.InterpretationLabS:
		return "labs"
	case vips.InterpretationSRGB:
		return "srgb"
	case vips.InterpretationYXY:
		return "yxy"
	case vips.InterpretationFourier:
		return "fourier"
	case vips.InterpretationRGB16:
		return "rgb16"
	case vips.InterpretationGrey16:
		return "grey16"
	case vips.InterpretationMatrix:
		return "matrix"
	case vips.InterpretationScRGB:
		return "scrgb"
	case vips.InterpretationHSV:
		return "hsv"
	case vips.InterpretationOKLab:
		return "oklab"
	case vips.InterpretationOKLCh:
		return "oklch"
	default:
		return "error"
	}
}

// InfoFormat returns the format name and MIME type imgproxy's /info reports
// for f (imgproxy imagetype/defs.go): UltraHDR is "jpeg", generic HEIF is
// "heic", and HEIC/HEIF are "image/heif".
func InfoFormat(f Format) (name, mime string) {
	switch f {
	case FormatUHDR:
		return FormatJPEG.String(), FormatJPEG.MIME()
	case FormatHEIC, FormatHEIF:
		return FormatHEIC.String(), "image/heif"
	default:
		return f.String(), f.MIME()
	}
}

// psdLayout is the band count, libvips sample format and interpretation
// the merged image of h loads with.
func psdLayout(h *PSDHeader) (bands int, sample, colorspace string) {
	sample = "uchar"
	switch h.Depth {
	case 16:
		sample = "ushort"
	case 32:
		sample = "float"
	default:
	}
	switch h.ColorSpace() {
	case ColorSpaceSRGB:
		colorspace = "srgb"
		if h.Depth == 16 {
			colorspace = "rgb16"
		}
	case ColorSpaceGrey:
		colorspace = "b-w"
		if h.Depth == 16 {
			colorspace = "grey16"
		}
	case ColorSpaceCMYK:
		colorspace = "cmyk"
	case ColorSpaceLab:
		colorspace = "lab"
	default:
		colorspace = "multiband"
	}
	return h.Channels, sample, colorspace
}
