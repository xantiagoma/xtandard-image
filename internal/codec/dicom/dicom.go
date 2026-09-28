// Package dicom reads DICOM Part 10 files (medical images) in pure Go: the
// file meta information, the image pixel module of the top-level dataset
// and the first frame of its pixel data (docs/FORMATS.md).
//
// Supported transfer syntaxes:
//
//   - native pixel data in implicit VR little endian, explicit VR little
//     endian, explicit VR big endian and deflated explicit VR little endian
//     (Decode);
//   - RLE Lossless (Decode);
//   - encapsulated JPEG (baseline, extended, lossless), JPEG-LS, JPEG 2000
//     and HTJ2K frames, whose codestream FirstFrame extracts for an image
//     codec (libvips) to decode; Header.Render then maps the decoded
//     samples like native ones.
//
// Rendering (Header.Render) applies the modality rescale (slope and
// intercept), the first VOI window of the dataset (LINEAR, LINEAR_EXACT or
// SIGMOID; without a window the frame's own minimum and maximum), inverts
// MONOCHROME1, converts YBR_FULL and YBR_FULL_422 to RGB and maps PALETTE
// COLOR through its lookup tables, always to 8-bit RGB.
//
// Privacy: DICOM files carry patient data (name, identifiers, dates,
// institution, free text). This package reads the values of only the
// elements listed in wanted (transfer syntax, image pixel module, rescale
// and VOI) at the top level of the dataset, and the value of no other
// element: everything else, sequences included, is skipped by length. No
// other value is kept, returned or put in an error message; errors name
// tags, never values.
//
// Parsing is bounded: every length is checked against the data before it
// is used, element counts and sequence nesting are capped, a deflated
// dataset is inflated up to maxInflated bytes, and Options.MaxPixels caps
// the image before anything is allocated for it.
package dicom

import (
	"strconv"

	"github.com/xantiagoma/xtandard-image/internal/codec/raster"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

const (
	preambleLen = 128
	magic       = "DICM"
	// maxSide bounds Rows and Columns (both are 16-bit anyway).
	maxSide = 1 << 16
	// maxFrames bounds NumberOfFrames.
	maxFrames = 1 << 20
)

// Sniff reports whether b starts like a DICOM Part 10 file: a 128-byte
// preamble followed by "DICM".
func Sniff(b []byte) bool {
	return len(b) >= preambleLen+4 && string(b[preambleLen:preambleLen+4]) == magic
}

// Encoding is how the pixel data of a transfer syntax is stored.
type Encoding uint8

// Encodings.
const (
	EncodingUnknown Encoding = iota
	// EncodingNative is uncompressed pixel data.
	EncodingNative
	// EncodingDeflated is native pixel data in a deflated dataset.
	EncodingDeflated
	// EncodingRLE is RLE Lossless.
	EncodingRLE
	// EncodingJPEGBaseline is JPEG baseline (process 1, 8-bit).
	EncodingJPEGBaseline
	// EncodingJPEGExtended is JPEG extended (processes 2 and 4, 12-bit).
	EncodingJPEGExtended
	// EncodingJPEGLossless is lossless JPEG (process 14, including SV1).
	EncodingJPEGLossless
	// EncodingJPEGLS is JPEG-LS, lossless or near-lossless.
	EncodingJPEGLS
	// EncodingJPEG2000 is JPEG 2000, lossless or lossy.
	EncodingJPEG2000
	// EncodingHTJ2K is High-Throughput JPEG 2000.
	EncodingHTJ2K
)

// String is the encoding's short name.
func (e Encoding) String() string {
	switch e {
	case EncodingNative:
		return "native"
	case EncodingDeflated:
		return "deflated"
	case EncodingRLE:
		return "rle"
	case EncodingJPEGBaseline:
		return "jpeg-baseline"
	case EncodingJPEGExtended:
		return "jpeg-extended"
	case EncodingJPEGLossless:
		return "jpeg-lossless"
	case EncodingJPEGLS:
		return "jpeg-ls"
	case EncodingJPEG2000:
		return "jpeg2000"
	case EncodingHTJ2K:
		return "htj2k"
	default:
		return "unknown"
	}
}

// Encapsulated reports whether frames are stored as encapsulated
// fragments (RLE and the image codecs).
func (e Encoding) Encapsulated() bool {
	return e >= EncodingRLE
}

// Codec reports whether a frame is a codestream for an image codec (JPEG,
// JPEG-LS, JPEG 2000, HTJ2K): FirstFrame extracts it.
func (e Encoding) Codec() bool {
	return e >= EncodingJPEGBaseline
}

// Transfer syntax UIDs.
const (
	tsImplicitLE = "1.2.840.10008.1.2"
	tsExplicitLE = "1.2.840.10008.1.2.1"
	tsDeflated   = "1.2.840.10008.1.2.1.99"
	tsExplicitBE = "1.2.840.10008.1.2.2"
	tsRLE        = "1.2.840.10008.1.2.5"
)

// syntax is how a transfer syntax encodes the dataset and its pixel data.
type syntax struct {
	explicit, bigEndian, deflated bool
	enc                           Encoding
}

// syntaxOf maps a transfer syntax UID; ok is false for unknown ones.
func syntaxOf(uid string) (syntax, bool) {
	switch uid {
	case tsImplicitLE:
		return syntax{enc: EncodingNative}, true
	case tsExplicitLE:
		return syntax{explicit: true, enc: EncodingNative}, true
	case tsDeflated:
		return syntax{explicit: true, deflated: true, enc: EncodingDeflated}, true
	case tsExplicitBE:
		return syntax{explicit: true, bigEndian: true, enc: EncodingNative}, true
	case tsRLE:
		return syntax{explicit: true, enc: EncodingRLE}, true
	case "1.2.840.10008.1.2.4.50":
		return syntax{explicit: true, enc: EncodingJPEGBaseline}, true
	case "1.2.840.10008.1.2.4.51":
		return syntax{explicit: true, enc: EncodingJPEGExtended}, true
	case "1.2.840.10008.1.2.4.57", "1.2.840.10008.1.2.4.70":
		return syntax{explicit: true, enc: EncodingJPEGLossless}, true
	case "1.2.840.10008.1.2.4.80", "1.2.840.10008.1.2.4.81":
		return syntax{explicit: true, enc: EncodingJPEGLS}, true
	case "1.2.840.10008.1.2.4.90", "1.2.840.10008.1.2.4.91":
		return syntax{explicit: true, enc: EncodingJPEG2000}, true
	case "1.2.840.10008.1.2.4.201", "1.2.840.10008.1.2.4.202", "1.2.840.10008.1.2.4.203":
		return syntax{explicit: true, enc: EncodingHTJ2K}, true
	}
	return syntax{}, false
}

// Header is what the dataset declares about its image. It holds no other
// value of the file (see the package documentation on privacy).
type Header struct {
	// Width and Height are Columns and Rows.
	Width, Height int
	// Frames is NumberOfFrames (1 when absent).
	Frames int
	// Samples is SamplesPerPixel (1 or 3).
	Samples int
	// BitsAllocated, BitsStored and HighBit describe each stored sample.
	BitsAllocated, BitsStored, HighBit int
	// Signed is PixelRepresentation 1 (two's complement samples).
	Signed bool
	// Photometric is PhotometricInterpretation (MONOCHROME1, MONOCHROME2,
	// RGB, YBR_FULL, YBR_FULL_422, PALETTE COLOR, ...).
	Photometric string
	// Planar is PlanarConfiguration 1 (colour-by-plane).
	Planar bool
	// TransferSyntax is the transfer syntax UID and Encoding how it
	// stores the pixel data.
	TransferSyntax string
	Encoding       Encoding
	// RescaleSlope and RescaleIntercept map stored values to modality
	// values (1 and 0 when absent).
	RescaleSlope, RescaleIntercept float64
	// WindowCenter and WindowWidth are the first VOI window, when
	// HasWindow.
	WindowCenter, WindowWidth float64
	HasWindow                 bool
	// VOILUTFunction is LINEAR (the default), LINEAR_EXACT or SIGMOID.
	VOILUTFunction string

	palette *palette
}

// Options bound Decode.
type Options struct {
	// MaxPixels bounds Width*Height (errs.KindLimitExceeded). Zero means
	// raster.DefaultMaxPixels.
	MaxPixels int64
}

func corrupt(what string) error { return errs.New(errs.KindCorrupt, "dicom: "+what) }

func corruptf(format string, args ...any) error {
	return errs.Newf(errs.KindCorrupt, "dicom: "+format, args...)
}

func unsupported(internal, public string) error {
	return errs.WithPublic(errs.New(errs.KindUnsupported, "dicom: "+internal), public)
}

// ParseHeader reads the file meta information and the image elements of
// the top-level dataset. Malformed files fail with errs.KindCorrupt; an
// unknown transfer syntax or an image this package cannot render with
// errs.KindUnsupported.
func ParseHeader(b []byte) (Header, error) {
	p, err := parse(b)
	if err != nil {
		return Header{}, err
	}
	return p.h, nil
}

// check validates the image elements once the dataset is read.
func (h *Header) check() error {
	if h.Width <= 0 || h.Height <= 0 {
		return corrupt("missing rows or columns")
	}
	if h.Width > maxSide || h.Height > maxSide {
		return corruptf("%dx%d", h.Width, h.Height)
	}
	if h.Frames < 1 || h.Frames > maxFrames {
		return corruptf("%d frames", h.Frames)
	}
	switch h.BitsAllocated {
	case 1, 8, 16, 32:
	default:
		return corruptf("bits allocated %d", h.BitsAllocated)
	}
	if h.BitsStored == 0 {
		h.BitsStored = h.BitsAllocated
	}
	if h.BitsStored < 1 || h.BitsStored > h.BitsAllocated {
		return corruptf("bits stored %d of %d", h.BitsStored, h.BitsAllocated)
	}
	if h.HighBit < 0 {
		h.HighBit = h.BitsStored - 1
	}
	if h.HighBit < h.BitsStored-1 || h.HighBit >= h.BitsAllocated {
		return corruptf("high bit %d", h.HighBit)
	}
	switch h.Photometric {
	case "MONOCHROME1", "MONOCHROME2":
		if h.Samples != 1 {
			return corruptf("%s with %d samples", h.Photometric, h.Samples)
		}
	case "PALETTE COLOR":
		if h.Samples != 1 {
			return corruptf("%s with %d samples", h.Photometric, h.Samples)
		}
		if h.palette == nil || h.palette.check() != nil {
			return unsupported("palette colour without usable lookup tables", "this DICOM palette colour image is not supported")
		}
	case "RGB", "YBR_FULL", "YBR_FULL_422", "YBR_RCT", "YBR_ICT", "YBR_PARTIAL_420", "YBR_PARTIAL_422":
		if h.Samples != 3 {
			return corruptf("%s with %d samples", h.Photometric, h.Samples)
		}
		if h.BitsAllocated == 1 || h.BitsAllocated == 32 {
			return unsupported("colour with bits allocated "+strconv.Itoa(h.BitsAllocated), "this DICOM colour depth is not supported")
		}
	case "":
		return corrupt("missing photometric interpretation")
	default:
		return unsupported("photometric interpretation", "this DICOM photometric interpretation is not supported")
	}
	if h.RescaleSlope == 0 {
		h.RescaleSlope = 1
	}
	return nil
}

// pixelLimit checks Width*Height against o.
func (h *Header) pixelLimit(o Options) error {
	limit := o.MaxPixels
	if limit <= 0 {
		limit = raster.DefaultMaxPixels
	}
	if int64(h.Width)*int64(h.Height) > limit {
		return errs.Newf(errs.KindLimitExceeded, "dicom: %dx%d exceeds %d pixels", h.Width, h.Height, limit)
	}
	return nil
}
