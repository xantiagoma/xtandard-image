// Package codec holds format detection and header probing (ARCHITECTURE
// §2 stage 11, §3): Sniff recognises every input format of FORMATS.md from
// magic bytes, and Probe reads only headers into an ImageMeta and enforces
// the pixel, frame and SVG limits before anything is decoded. The pure-Go
// loaders live in codec/bmp and codec/ico, their pixel types in
// codec/raster.
package codec

import (
	"mime"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// This file and meta.go are the cgo-free half of the package: they import
// neither internal/vips nor "C", so pipeline/plan can depend on Format and
// ImageMeta alone (ARCHITECTURE §3).

// Format is an input image format as detected from magic bytes (Sniff),
// never from a file extension or an upstream Content-Type (FORMATS.md).
type Format uint8

// Formats, one per input row of FORMATS.md §1. PDF, PSD and RAW are
// recognised so that they fail with a precise error, but they are M2 and
// Supported reports false for them.
const (
	FormatUnknown Format = iota
	FormatJPEG
	FormatPNG
	FormatGIF
	FormatWebP
	FormatAVIF
	// FormatHEIC is HEIF with HEVC-coded images (heic/heix/hevc/... brands).
	FormatHEIC
	// FormatHEIF is a generic HEIF container (mif1/msf1 brands) whose codec
	// the brands do not name; Probe refines it to AVIF or HEIC.
	FormatHEIF
	FormatJXL
	FormatTIFF
	FormatBMP
	// FormatICO covers both icons (ICO) and cursors (CUR).
	FormatICO
	FormatSVG
	// FormatJP2 is JPEG 2000: the JP2 container or a raw J2K codestream.
	FormatJP2
	// FormatUHDR is an UltraHDR JPEG: a JPEG carrying a gain map as an MPF
	// secondary image (libultrahdr, vips uhdrload).
	FormatUHDR
	FormatPDF
	// FormatPSD covers Photoshop PSD and PSB.
	FormatPSD
	// FormatRAW is a camera RAW with its own signature (CR2, CR3, CRW, RAF,
	// ORF, RW2, X3F, MRW). TIFF-based RAWs without one (NEF, ARW, DNG, PEF,
	// SRW, ...) sniff as FormatTIFF.
	FormatRAW

	numFormats
)

type formatInfo struct {
	name      string
	ext       string
	mime      string
	anim      bool // can carry an animation the engine keeps (FORMATS.md §3)
	alpha     bool
	supported bool // decodable in the current milestone
}

// info is the single table of per-format facts. It is a switch rather than a
// package-level map so there is no mutable global.
func (f Format) info() formatInfo {
	switch f {
	case FormatJPEG:
		return formatInfo{"jpeg", "jpg", "image/jpeg", false, false, true}
	case FormatPNG:
		return formatInfo{"png", "png", "image/png", false, true, true}
	case FormatGIF:
		return formatInfo{"gif", "gif", "image/gif", true, true, true}
	case FormatWebP:
		return formatInfo{"webp", "webp", "image/webp", true, true, true}
	case FormatAVIF:
		return formatInfo{"avif", "avif", "image/avif", false, true, true}
	case FormatHEIC:
		return formatInfo{"heic", "heic", "image/heic", false, true, true}
	case FormatHEIF:
		return formatInfo{"heif", "heif", "image/heif", false, true, true}
	case FormatJXL:
		return formatInfo{"jxl", "jxl", "image/jxl", true, true, true}
	case FormatTIFF:
		return formatInfo{"tiff", "tiff", "image/tiff", false, true, true}
	case FormatBMP:
		return formatInfo{"bmp", "bmp", "image/bmp", false, true, true}
	case FormatICO:
		return formatInfo{"ico", "ico", "image/x-icon", false, true, true}
	case FormatSVG:
		return formatInfo{"svg", "svg", "image/svg+xml", false, true, true}
	case FormatJP2:
		return formatInfo{"jp2", "jp2", "image/jp2", false, true, true}
	case FormatUHDR:
		return formatInfo{"uhdr", "jpg", "image/jpeg", false, false, true}
	case FormatPDF:
		return formatInfo{"pdf", "pdf", "application/pdf", false, true, false}
	case FormatPSD:
		return formatInfo{"psd", "psd", "image/vnd.adobe.photoshop", false, true, false}
	case FormatRAW:
		return formatInfo{"raw", "", "application/octet-stream", false, false, false}
	default:
		return formatInfo{"unknown", "", "application/octet-stream", false, false, false}
	}
}

// String returns the lower-case canonical name ("jpeg", "uhdr", ...).
func (f Format) String() string { return f.info().name }

// MIME returns the canonical media type. UltraHDR is image/jpeg (it is a
// valid JPEG); RAW and unknown formats are application/octet-stream.
func (f Format) MIME() string { return f.info().mime }

// Ext returns the usual file extension without the dot ("jpg", "tiff"), or
// "" for RAW and unknown formats.
func (f Format) Ext() string { return f.info().ext }

// SupportsAnimation reports whether the format can carry an animation that
// the engine reads and writes frame by frame: GIF, WebP and JPEG XL. APNG and
// AVIF sequences are loaded as their first frame (FORMATS.md §1).
func (f Format) SupportsAnimation() bool { return f.info().anim }

// SupportsAlpha reports whether the format can carry transparency.
func (f Format) SupportsAlpha() bool { return f.info().alpha }

// Supported reports whether the format can be decoded in this milestone.
// PDF, PSD and RAW are recognised but land in M2; Probe rejects them with
// errs.KindUnsupported.
func (f Format) Supported() bool { return f.info().supported }

// MarshalText implements encoding.TextMarshaler (the canonical name).
func (f Format) MarshalText() ([]byte, error) {
	if f >= numFormats {
		return nil, errs.Newf(errs.KindInternal, "codec: invalid format %d", uint8(f))
	}
	return []byte(f.String()), nil
}

// ParseFormat parses a canonical name or a common extension or alias
// ("jpg", "tif", "cur", "j2k", "jpx", "psb", ...), case-insensitively.
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(s) {
	case "jpeg", "jpg", "jpe", "jfif":
		return FormatJPEG, nil
	case "png", "apng":
		return FormatPNG, nil
	case "gif":
		return FormatGIF, nil
	case "webp":
		return FormatWebP, nil
	case "avif", "avifs":
		return FormatAVIF, nil
	case "heic", "heics":
		return FormatHEIC, nil
	case "heif", "heifs", "hif":
		return FormatHEIF, nil
	case "jxl":
		return FormatJXL, nil
	case "tiff", "tif":
		return FormatTIFF, nil
	case "bmp", "dib":
		return FormatBMP, nil
	case "ico", "cur":
		return FormatICO, nil
	case "svg":
		return FormatSVG, nil
	case "jp2", "j2k", "j2c", "jpx", "jpf", "jpeg2000":
		return FormatJP2, nil
	case "uhdr", "ultrahdr":
		return FormatUHDR, nil
	case "pdf":
		return FormatPDF, nil
	case "psd", "psb":
		return FormatPSD, nil
	case "raw":
		return FormatRAW, nil
	}
	return FormatUnknown, errs.Newf(errs.KindUnsupported, "codec: unknown format %q", s)
}

// Output returns the output format that re-encodes f in kind, for keeping
// the source format: UltraHDR maps to JPEG and generic HEIF to HEIC. JPEG
// 2000, PDF, PSD, RAW and unknown formats cannot be written (FORMATS.md §5)
// and report false.
func (f Format) Output() (transform.Format, bool) {
	switch f {
	case FormatJPEG, FormatUHDR:
		return transform.FormatJPEG, true
	case FormatPNG:
		return transform.FormatPNG, true
	case FormatGIF:
		return transform.FormatGIF, true
	case FormatWebP:
		return transform.FormatWebP, true
	case FormatAVIF:
		return transform.FormatAVIF, true
	case FormatHEIC, FormatHEIF:
		return transform.FormatHEIC, true
	case FormatJXL:
		return transform.FormatJXL, true
	case FormatTIFF:
		return transform.FormatTIFF, true
	case FormatBMP:
		return transform.FormatBMP, true
	case FormatICO:
		return transform.FormatICO, true
	case FormatSVG:
		return transform.FormatSVG, true
	default:
		return transform.FormatUnset, false
	}
}

// MatchesContentType reports whether a declared Content-Type is consistent
// with f. The declared type is never trusted for detection; a mismatch is
// only reported (ImageMeta.ContentTypeMismatch). Empty, unparsable and
// generic types (application/octet-stream, binary/octet-stream) match
// anything, as do the aliases browsers and origins commonly send (image/jpg,
// image/x-png, image/x-ms-bmp, image/vnd.microsoft.icon, ...).
func (f Format) MatchesContentType(contentType string) bool {
	if strings.TrimSpace(contentType) == "" {
		return true
	}
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return true
	}
	switch mt {
	case "application/octet-stream", "binary/octet-stream", "application/unknown":
		return true
	case f.MIME():
		return true
	}
	switch f {
	case FormatJPEG, FormatUHDR:
		return mt == "image/jpg" || mt == "image/pjpeg"
	case FormatPNG:
		return mt == "image/x-png" || mt == "image/apng" || mt == "image/vnd.mozilla.apng"
	case FormatAVIF:
		return mt == "image/avif-sequence"
	case FormatHEIC, FormatHEIF:
		return mt == "image/heic" || mt == "image/heif" || mt == "image/heic-sequence" || mt == "image/heif-sequence"
	case FormatTIFF:
		return mt == "image/tiff-fx"
	case FormatBMP:
		return mt == "image/x-bmp" || mt == "image/x-ms-bmp" || mt == "image/x-windows-bmp"
	case FormatICO:
		return mt == "image/vnd.microsoft.icon" || mt == "image/ico" || mt == "image/icon" || mt == "image/x-cursor"
	case FormatJP2:
		return mt == "image/jpx" || mt == "image/jpm" || mt == "image/j2k" || mt == "image/x-jp2"
	case FormatPSD:
		return mt == "image/x-photoshop" || mt == "application/x-photoshop" || mt == "image/psd"
	case FormatRAW:
		return strings.HasPrefix(mt, "image/x-")
	default:
		return false
	}
}
