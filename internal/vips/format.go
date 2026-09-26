package vips

import "strings"

// Format is an image file format known to the engine: what a loader
// detected (Header.Format) or what Save writes.
type Format int

// Formats. BMP and ICO are handled in Go (codec, WP-08) and never reach
// libvips.
const (
	FormatUnknown Format = iota
	FormatJPEG
	FormatPNG
	FormatWebP
	FormatGIF
	FormatAVIF
	FormatHEIF
	FormatJXL
	FormatTIFF
	FormatSVG
	FormatJP2K
	// FormatPDF is loaded by PDFium (pdfload); it is never written.
	FormatPDF
	// FormatPSD is Photoshop PSD/PSB, loaded by ImageMagick (magickload),
	// which is allowed for nothing else; it is never written.
	FormatPSD
	// FormatEXR is OpenEXR (openexrload: 4-band float scRGB); never written.
	FormatEXR
	// FormatRadiance is Radiance RGBE (radload: RGBE-coded, decoded to float
	// scRGB or XYZ); never written.
	FormatRadiance
	// FormatPPM is a binary PGM/PPM (8/16-bit) or PFM (float) portable
	// anymap (ppmload); never written.
	FormatPPM
)

// String returns the lower-case format name.
func (f Format) String() string {
	switch f {
	case FormatJPEG:
		return "jpeg"
	case FormatPNG:
		return "png"
	case FormatWebP:
		return "webp"
	case FormatGIF:
		return "gif"
	case FormatAVIF:
		return "avif"
	case FormatHEIF:
		return "heif"
	case FormatJXL:
		return "jxl"
	case FormatTIFF:
		return "tiff"
	case FormatSVG:
		return "svg"
	case FormatJP2K:
		return "jp2k"
	case FormatPDF:
		return "pdf"
	case FormatPSD:
		return "psd"
	case FormatEXR:
		return "exr"
	case FormatRadiance:
		return "radiance"
	case FormatPPM:
		return "ppm"
	default:
		return "unknown"
	}
}

// formatOfLoader maps a vips-loader name ("jpegload") and, for heifload,
// the heif-compression metadata ("av1", "hevc") to a Format.
func formatOfLoader(loader, subformat string) Format {
	name, _ := strings.CutSuffix(loader, "_buffer")
	switch name {
	case "jpegload", "uhdrload":
		return FormatJPEG
	case "pngload":
		return FormatPNG
	case "webpload":
		return FormatWebP
	case "gifload":
		return FormatGIF
	case "heifload":
		if subformat == "av1" {
			return FormatAVIF
		}
		return FormatHEIF
	case "jxlload":
		return FormatJXL
	case "tiffload":
		return FormatTIFF
	case "svgload":
		return FormatSVG
	case "jp2kload":
		return FormatJP2K
	case "pdfload":
		return FormatPDF
	case "magickload":
		// ImageMagick loads PSD/PSB only (xi_check_loader and policy.xml).
		return FormatPSD
	case "openexrload":
		return FormatEXR
	case "radload":
		return FormatRadiance
	case "ppmload":
		return FormatPPM
	default:
		return FormatUnknown
	}
}

// Header is what Probe reads without decoding pixels.
type Header struct {
	Loader         string // vips-loader, e.g. "jpegload", "uhdrload"
	Format         Format
	Width          int
	Height         int // one page/frame
	Bands          int
	BandFormat     BandFormat
	Interpretation Interpretation
	Pages          int  // pages or frames in the file (n-pages; 1 without)
	PageHeight     int  // height of one page as loaded one page at a time (= Height)
	Orientation    int  // EXIF orientation 1..8 (1 when absent)
	HasAlpha       bool // an alpha band is present
	HasICC         bool // an embedded ICC profile is present
	BitDepth       int  // bits per sample
	IsAnimated     bool // several frames with frame delays (GIF, WebP, JXL)
	Delay          []int
	Loop           int // loop count, 0 = forever; -1 when absent
}

// Pixels returns Width × Height × Pages, the decoded size of all pages.
func (h *Header) Pixels() int64 {
	return int64(h.Width) * int64(h.Height) * int64(max(h.Pages, 1))
}
