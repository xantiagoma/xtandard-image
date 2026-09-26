package codec

import (
	"bytes"
	"encoding/binary"

	"github.com/xantiagoma/xtandard-image/internal/codec/bmp"
	"github.com/xantiagoma/xtandard-image/internal/codec/ico"
	"github.com/xantiagoma/xtandard-image/internal/codec/raw"
	"github.com/xantiagoma/xtandard-image/internal/security/svg"
)

// SniffLen is how many leading bytes Sniff needs to recognise every format
// but UltraHDR: SVG looks at up to svg.SniffLen bytes. UltraHDR is recognised
// only when its MPF and gain-map markers lie within the bytes given, which
// can be up to the start of scan (Probe passes the whole buffer).
const SniffLen = svg.SniffLen

// Magic numbers (constants, not package-level slices: no mutable globals).
const (
	magicPNG     = "\x89PNG\r\n\x1a\n"
	magicJXLBox  = "\x00\x00\x00\x0cJXL \r\n\x87\n"
	magicJP2Box  = "\x00\x00\x00\x0cjP  \r\n\x87\n"
	magicJ2K     = "\xff\x4f\xff\x51"
	jpegMPF      = "MPF\x00"
	jpegISOGain  = "urn:iso:std:iso:ts:21496:-1\x00"
	jpegXMP      = "http://ns.adobe.com/xap/1.0/\x00"
	jpegHDRGMXMP = "hdrgm:Version"
	magicEXR     = "v/1\x01"
	magicRad     = "#?RADIANCE"
	magicRGBE    = "#?RGBE"
)

// has reports whether b starts with s, without allocating.
func has(b []byte, s string) bool { return len(b) >= len(s) && string(b[:len(s)]) == s }

// Sniff detects the format of an image from its first bytes (at least
// SniffLen when available). ok is false for unrecognised input, which must
// be rejected: detection never falls back to the extension or the upstream
// Content-Type (FORMATS.md). It allocates nothing and reads at most a bounded
// prefix of head, so it is safe on untrusted input of any size.
func Sniff(head []byte) (Format, bool) {
	f := sniff(head)
	return f, f != FormatUnknown
}

func sniff(b []byte) Format {
	if len(b) < 2 {
		return FormatUnknown
	}
	switch {
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		if isUltraHDR(b) {
			return FormatUHDR
		}
		return FormatJPEG
	case has(b, magicPNG):
		return FormatPNG
	case has(b, "GIF87a"), has(b, "GIF89a"):
		return FormatGIF
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return FormatWebP
	case b[0] == 0xFF && b[1] == 0x0A, has(b, magicJXLBox):
		return FormatJXL
	case has(b, magicJP2Box), has(b, magicJ2K):
		return FormatJP2
	case has(b, "%PDF-"):
		return FormatPDF
	case len(b) >= 6 && string(b[:4]) == "8BPS" && b[4] == 0 && (b[5] == 1 || b[5] == 2):
		return FormatPSD
	case has(b, magicEXR):
		return FormatEXR
	case isLine(b, magicRad), isLine(b, magicRGBE):
		return FormatRadiance
	case len(b) >= 3 && b[0] == 'P' && isPNMSpace(b[2]) && (b[1] == '5' || b[1] == '6'):
		return FormatPPM
	case len(b) >= 3 && b[0] == 'P' && isPNMSpace(b[2]) && (b[1] == 'F' || b[1] == 'f'):
		return FormatPFM
	case len(b) >= 4 && (string(b[:4]) == "II*\x00" || string(b[:4]) == "MM\x00*" ||
		string(b[:4]) == "II+\x00" || string(b[:4]) == "MM\x00+"):
		// TIFF-shaped RAWs: CR2, and NEF, ARW, DNG, PEF, ... by IFD0.
		if _, ok := raw.Sniff(b); ok {
			return FormatRAW
		}
		return FormatTIFF
	case len(b) >= 8 && string(b[4:8]) == "ftyp":
		return sniffISOBMFF(b)
	case isRAW(b):
		return FormatRAW
	case bmp.Sniff(b):
		return FormatBMP
	case ico.Sniff(b):
		return FormatICO
	case svg.IsSVG(b):
		return FormatSVG
	}
	return FormatUnknown
}

// isLine reports whether b starts with the line s (ended by LF or CR).
func isLine(b []byte, s string) bool {
	return len(b) > len(s) && has(b, s) && (b[len(s)] == '\n' || b[len(s)] == '\r')
}

// isPNMSpace is the whitespace that ends a Netpbm magic number. Only the
// binary variants are recognised: PGM/PPM (P5, P6) and PFM (PF, Pf); ASCII
// (P1-P3) and bitmap (P4) files are not images this service decodes.
func isPNMSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// isRAW recognises camera RAW formats with their own signature (RAF, ORF,
// RW2, X3F, MRW, CRW, IIQ); see raw.Sniff.
func isRAW(b []byte) bool {
	_, ok := raw.Sniff(b)
	return ok
}

// sniffISOBMFF classifies an ISO base media file by the brands of its
// leading ftyp box: the major brand first, then the compatible brands, the
// way libheif does. Only brands inside the box (and inside b) are read.
func sniffISOBMFF(b []byte) Format {
	// A bogus box size falls back to what is there; either way the scan
	// stays within SniffLen.
	size := int(binary.BigEndian.Uint32(b))
	if size < 16 || size > len(b) {
		size = len(b)
	}
	size = min(size, SniffLen)
	if size < 12 {
		return FormatUnknown
	}
	if f := brandFormat(string(b[8:12])); f != FormatUnknown && f != FormatHEIF {
		return f
	}
	generic := isGenericHEIFBrand(string(b[8:12]))
	// Compatible brands follow the 4-byte minor version.
	for i := 16; i+4 <= size; i += 4 {
		br := string(b[i : i+4])
		switch f := brandFormat(br); f {
		case FormatAVIF, FormatHEIC:
			return f
		case FormatHEIF:
			generic = true
		default:
		}
	}
	if generic {
		return FormatHEIF
	}
	return FormatUnknown
}

func isGenericHEIFBrand(br string) bool {
	switch br {
	case "mif1", "mif2", "mif3", "msf1", "miaf":
		return true
	}
	return false
}

// brandFormat maps an ftyp brand to a format. Generic HEIF brands map to
// FormatHEIF, which the compatible brands may refine.
func brandFormat(br string) Format {
	switch br {
	case "avif", "avis", "avio":
		return FormatAVIF
	case "heic", "heix", "hevc", "hevx", "heim", "heis", "hevm", "hevs":
		return FormatHEIC
	case "crx ":
		return FormatRAW // Canon CR3
	}
	if isGenericHEIFBrand(br) {
		return FormatHEIF
	}
	return FormatUnknown
}

// isUltraHDR reports whether a JPEG carries a gain map the way libultrahdr
// writes it. Like vips uhdrload's is_a, it requires an APP2 MPF segment
// (the gain map is the MPF secondary image) and, instead of libultrahdr's full
// parse, gain-map metadata in the primary image: the ISO 21496-1 APP2 marker
// or Adobe hdrgm XMP. Only the header segments before the first SOS are
// walked.
func isUltraHDR(b []byte) bool {
	var mpf, gain bool
	for i := 2; i+4 <= len(b); {
		if b[i] != 0xFF {
			return false
		}
		m := b[i+1]
		switch {
		case m == 0xFF: // fill byte
			i++
			continue
		case m == 0x01 || (m >= 0xD0 && m <= 0xD8): // no payload
			i += 2
			continue
		case m == 0xD9 || m == 0xDA: // EOI, SOS: end of the header
			return mpf && gain
		}
		n := int(binary.BigEndian.Uint16(b[i+2:]))
		if n < 2 {
			return false
		}
		seg := b[i+4 : min(i+2+n, len(b))]
		switch m {
		case 0xE2: // APP2
			mpf = mpf || has(seg, jpegMPF)
			gain = gain || has(seg, jpegISOGain)
		case 0xE1: // APP1
			gain = gain || (has(seg, jpegXMP) && bytes.Contains(seg, []byte(jpegHDRGMXMP)))
		}
		if mpf && gain {
			return true
		}
		i += 2 + n
	}
	return false
}
