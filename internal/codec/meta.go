package codec

import (
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// ColorSpace is the colour space of the decoded pixels as the loader reports
// it (cgo-free mirror of the libvips interpretations that matter to the
// planner).
type ColorSpace uint8

// Colour spaces.
const (
	ColorSpaceUnknown ColorSpace = iota
	ColorSpaceSRGB               // RGB, 8 or 16 bits (see BitDepth)
	ColorSpaceGrey               // greyscale, 8 or 16 bits
	ColorSpaceCMYK
	ColorSpaceLab
	ColorSpaceScRGB // linear light float RGB
	ColorSpaceOther // multiband, XYZ, HSV, ...
)

// String returns the lower-case name.
func (c ColorSpace) String() string {
	switch c {
	case ColorSpaceSRGB:
		return "srgb"
	case ColorSpaceGrey:
		return "grey"
	case ColorSpaceCMYK:
		return "cmyk"
	case ColorSpaceLab:
		return "lab"
	case ColorSpaceScRGB:
		return "scrgb"
	case ColorSpaceOther:
		return "other"
	default:
		return "unknown"
	}
}

// ImageMeta is what the probe stage learns from the headers of a source
// image, before any pixel is decoded (ARCHITECTURE §2 stage 11). It is the
// planner's view of the source.
type ImageMeta struct {
	// Format is the detected format, refined by the loader where magic bytes
	// are ambiguous (generic HEIF → AVIF or HEIC, JPEG → UltraHDR).
	Format Format
	// Width and Height are the size of one frame or page, before EXIF
	// orientation is applied.
	Width  int
	Height int
	// Frames is the number of animation frames: Pages when IsAnimated, else 1.
	Frames int
	// Pages is the number of pages, frames or icon entries in the file (1 for
	// single images).
	Pages int
	// PageHeight is the height of one page as loaded one page at a time; it
	// equals Height.
	PageHeight int
	HasAlpha   bool
	// Orientation is the EXIF orientation 1..8 (1 when absent).
	Orientation int
	// BitDepth is the bits per sample the loader produces (8 or 16 for most
	// formats).
	BitDepth int
	// IsAnimated reports several frames with frame delays (GIF, WebP, JXL).
	IsAnimated bool
	// Delays are the per-frame delays in milliseconds (nil when the format has
	// none).
	Delays []int
	// Loop is the animation loop count, 0 = forever, -1 when absent.
	Loop       int
	HasICC     bool
	ColorSpace ColorSpace
	// ContentTypeMismatch reports that the Content-Type declared by the
	// origin disagrees with the sniffed format. It is only a warning: the
	// declared type is never trusted for detection.
	ContentTypeMismatch bool
}

// Pixels returns Width × Height × Frames, the pixels a full decode of every
// animation frame produces (the unit of Limits.MaxSrcPixels).
func (m *ImageMeta) Pixels() int64 {
	return int64(m.Width) * int64(m.Height) * int64(max(m.Frames, 1))
}

// Limits bound what a source may declare in its headers. They are checked
// by Probe before anything is decoded. A zero field takes the value of
// DefaultLimits. They also carry the two per-format probe settings (PDF
// density, PSD composite policy), which decide what a header means.
type Limits struct {
	// MaxSrcPixels caps Width × Height × Frames (XI_LIMITS_MAX_SRC_PIXELS,
	// imgproxy's IMGPROXY_MAX_SRC_RESOLUTION).
	MaxSrcPixels int64
	// MaxAnimationFrames caps the frames of an animation
	// (XI_LIMITS_MAX_ANIMATION_FRAMES).
	MaxAnimationFrames int
	// MaxAnimationFramePixels caps Width × Height of one frame of an
	// animation (XI_LIMITS_MAX_ANIMATION_FRAME_PIXELS).
	MaxAnimationFramePixels int64
	// MaxSVGBytes, MaxSVGDepth and MaxSVGElements bound SVG documents
	// (XI_LIMITS_MAX_SVG_*), enforced by the sanitiser that runs before
	// librsvg sees the document.
	MaxSVGBytes    int64
	MaxSVGDepth    int
	MaxSVGElements int
	// MaxPDFPages caps the pages a PDF may declare
	// (XI_LIMITS_MAX_PDF_PAGES); the page count is read before any page is
	// rendered.
	MaxPDFPages int
	// PDFDensity is the DPI PDF pages are measured (and later rendered) at
	// when nothing is resized: the natural size of a page
	// (XI_FORMATS_PDF_DPI, 72 by default: one pixel per point).
	PDFDensity float64
	// PSDRequireComposite refuses PSD/PSB files saved without a merged
	// composite ("Maximize compatibility" off) with errs.KindUnsupported
	// instead of letting ImageMagick flatten the layers
	// (XI_FORMATS_PSD_REQUIRE_COMPOSITE, off by default).
	PSDRequireComposite bool
}

// DefaultLimits returns the XI_LIMITS_* defaults of docs/CONFIG.md.
func DefaultLimits() Limits {
	return Limits{
		MaxSrcPixels:            50_000_000,
		MaxAnimationFrames:      256,
		MaxAnimationFramePixels: 16_000_000,
		MaxSVGBytes:             5 << 20,
		MaxSVGDepth:             256,
		MaxSVGElements:          200_000,
		MaxPDFPages:             500,
		PDFDensity:              72,
	}
}

// withDefaults fills the zero fields from DefaultLimits.
func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.MaxSrcPixels <= 0 {
		l.MaxSrcPixels = d.MaxSrcPixels
	}
	if l.MaxAnimationFrames <= 0 {
		l.MaxAnimationFrames = d.MaxAnimationFrames
	}
	if l.MaxAnimationFramePixels <= 0 {
		l.MaxAnimationFramePixels = d.MaxAnimationFramePixels
	}
	if l.MaxSVGBytes <= 0 {
		l.MaxSVGBytes = d.MaxSVGBytes
	}
	if l.MaxSVGDepth <= 0 {
		l.MaxSVGDepth = d.MaxSVGDepth
	}
	if l.MaxSVGElements <= 0 {
		l.MaxSVGElements = d.MaxSVGElements
	}
	if l.MaxPDFPages <= 0 {
		l.MaxPDFPages = d.MaxPDFPages
	}
	if !(l.PDFDensity > 0) {
		l.PDFDensity = d.PDFDensity
	}
	return l
}

// Check reports errs.KindLimitExceeded when m declares more than l allows:
// more animation frames than MaxAnimationFrames, an animation frame larger
// than MaxAnimationFramePixels, or more pixels in total (all frames) than
// MaxSrcPixels (FORMATS.md §3). Zero fields of l take their defaults.
func (l Limits) Check(m *ImageMeta) error {
	l = l.withDefaults()
	if m.Width <= 0 || m.Height <= 0 {
		return errs.Newf(errs.KindCorrupt, "codec: %s declares %dx%d pixels", m.Format, m.Width, m.Height)
	}
	frame := int64(m.Width) * int64(m.Height)
	if m.Frames > 1 {
		if m.Frames > l.MaxAnimationFrames {
			return errs.Newf(errs.KindLimitExceeded, "codec: %d animation frames exceed the limit of %d",
				m.Frames, l.MaxAnimationFrames)
		}
		if frame > l.MaxAnimationFramePixels {
			return errs.Newf(errs.KindLimitExceeded, "codec: %dx%d animation frame exceeds the limit of %d pixels",
				m.Width, m.Height, l.MaxAnimationFramePixels)
		}
	}
	if p := m.Pixels(); p > l.MaxSrcPixels {
		return errs.Newf(errs.KindLimitExceeded, "codec: %dx%d×%d pixels exceed the limit of %d",
			m.Width, m.Height, max(m.Frames, 1), l.MaxSrcPixels)
	}
	return nil
}
