package codec

import (
	"bytes"
	"encoding/binary"

	"github.com/xantiagoma/xtandard-image/internal/codec/bmp"
	"github.com/xantiagoma/xtandard-image/internal/codec/icns"
	"github.com/xantiagoma/xtandard-image/internal/codec/ico"
	"github.com/xantiagoma/xtandard-image/internal/codec/raw"
	"github.com/xantiagoma/xtandard-image/internal/codec/wbmp"
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
	magicEBML    = "\x1a\x45\xdf\xa3"
	magicJXR     = "II\xbc"
	magicDDS     = "DDS \x7c\x00\x00\x00" // "DDS " and the header size, 124
	magicQOI     = "qoif"
	magicSun     = "\x59\xa6\x6a\x95"
	magicXPM     = "/* XPM */"
	magicXBM     = "#define "
)

// has reports whether b starts with s, without allocating.
func has(b []byte, s string) bool { return len(b) >= len(s) && string(b[:len(s)]) == s }

// Sniff detects the format of an image from its first bytes (at least
// SniffLen when available). ok is false for unrecognised input, which must
// be rejected: detection never falls back to the extension or the upstream
// Content-Type (FORMATS.md). It allocates nothing and reads at most a bounded
// prefix of head, so it is safe on untrusted input of any size. WBMP, which
// has no signature, is only recognised with the whole file (its length must
// match its header); every caller passes the whole source.
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
	case len(b) >= 3 && b[0] == 'P' && isPNMSpace(b[2]) && b[1] >= '1' && b[1] <= '6':
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
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "AVI ":
		return FormatAVI
	case has(b, magicEBML):
		return sniffMatroska(b)
	case isQuickTime(b):
		return FormatMOV
	case isRAW(b):
		return FormatRAW
	case len(b) >= 8 && has(b, magicJXR) && b[3] <= 1:
		return FormatJXR
	case len(b) >= 128 && has(b, magicDDS):
		return FormatDDS
	case len(b) >= 14 && has(b, magicQOI):
		return FormatQOI
	case len(b) >= 32 && has(b, magicSun):
		return FormatSunRaster
	case isSGI(b):
		return FormatSGI
	case isPCX(b):
		return FormatPCX
	case has(b, magicXPM):
		return FormatXPM
	case isXBM(b):
		return FormatXBM
	case len(b) >= 3 && b[0] == 'P' && b[1] == '7' && isPNMSpace(b[2]):
		return FormatPAM
	case icns.Sniff(b):
		return FormatICNS
	case bmp.Sniff(b):
		return FormatBMP
	case ico.Sniff(b):
		return FormatICO
	case svg.IsSVG(b):
		return FormatSVG
	// No signature: last, and only when the header is self-consistent.
	case wbmp.Sniff(b):
		return FormatWBMP
	case isTGA(b):
		return FormatTGA
	}
	return FormatUnknown
}

// isSGI recognises an SGI image header: magic 474, storage 0 (raw) or 1
// (run-length), 1 or 2 bytes per channel and 1-3 dimensions.
func isSGI(b []byte) bool {
	return len(b) >= 512 && b[0] == 0x01 && b[1] == 0xDA && b[2] <= 1 && (b[3] == 1 || b[3] == 2) &&
		binary.BigEndian.Uint16(b[4:]) >= 1 && binary.BigEndian.Uint16(b[4:]) <= 3
}

// isPCX recognises a ZSoft PCX header of the versions ImageMagick detects
// (2 and 5): manufacturer 10, encoding 0 or 1 (run-length), 1/2/4/8 bits per
// plane.
func isPCX(b []byte) bool {
	return len(b) >= 128 && b[0] == 0x0A && (b[1] == 2 || b[1] == 5) && b[2] <= 1 &&
		(b[3] == 1 || b[3] == 2 || b[3] == 4 || b[3] == 8)
}

// isXBM recognises an X11 bitmap: it starts with "#define <name>_width".
func isXBM(b []byte) bool {
	if !has(b, magicXBM) {
		return false
	}
	line := b[len(magicXBM):min(len(b), 256)]
	if i := bytes.IndexAny(line, " \t\r\n"); i >= 0 {
		line = line[:i]
	}
	return bytes.HasSuffix(line, []byte("_width"))
}

// isTGA recognises a Truevision TGA file, which has no signature, by a
// self-consistent 18-byte header (the checks of the shim's xi_is_tga, which
// gates ImageMagick): a colour map only with a colour-mapped image type, a
// known image type with a matching pixel depth, non-zero dimensions and no
// interleaving; ISO BMFF files are excluded.
func isTGA(b []byte) bool {
	if len(b) < 18 || string(b[4:8]) == "ftyp" || b[1] > 1 {
		return false
	}
	le := binary.LittleEndian
	cmap, depth, desc := b[1], b[16], b[17]
	if le.Uint16(b[12:]) == 0 || le.Uint16(b[14:]) == 0 || desc&0xC0 != 0 || desc&0x0F > 8 {
		return false
	}
	switch b[2] {
	case 1, 9: // colour-mapped
		bits := b[7]
		return cmap == 1 && le.Uint16(b[5:]) > 0 && (bits == 15 || bits == 16 || bits == 24 || bits == 32) &&
			(depth == 8 || depth == 16)
	case 2, 10: // true colour
		return cmap == 0 && (depth == 15 || depth == 16 || depth == 24 || depth == 32)
	case 3, 11: // grey
		return cmap == 0 && (depth == 8 || depth == 16)
	default:
		return false
	}
}

// isLine reports whether b starts with the line s (ended by LF or CR).
func isLine(b []byte, s string) bool {
	return len(b) > len(s) && has(b, s) && (b[len(s)] == '\n' || b[len(s)] == '\r')
}

// isPNMSpace is the whitespace that ends a Netpbm magic number: PBM, PGM
// and PPM (P1-P6, ASCII and binary), PAM (P7) and PFM (PF, Pf).
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
	// No image brand: an MP4-family video (isom, mp41, mp42, avc1, av01,
	// dash, 3gp*, M4V, ...), or QuickTime.
	if string(b[8:12]) == "qt  " {
		return FormatMOV
	}
	return FormatMP4
}

// isQuickTime recognises a QuickTime movie without an ftyp box (older
// files): a leading moov, mdat, wide, free, skip or pnot atom with a
// plausible size (8 bytes or more, or 0/1 for "to the end"/64-bit).
func isQuickTime(b []byte) bool {
	if len(b) < 8 {
		return false
	}
	switch string(b[4:8]) {
	case "moov", "mdat", "wide", "free", "skip", "pnot":
	default:
		return false
	}
	size := binary.BigEndian.Uint32(b)
	return size == 0 || size == 1 || size >= 8
}

// sniffMatroska tells WebM from other Matroska files by the DocType
// element (ID 0x4282) of the EBML header that starts b. Only the header's
// first 64 bytes are read.
func sniffMatroska(b []byte) Format {
	head := b[:min(len(b), 64)]
	i := bytes.Index(head, []byte{0x42, 0x82})
	if i < 0 || i+3 > len(head) {
		return FormatMKV
	}
	n := int(head[i+2] &^ 0x80) // a one-byte EBML size (0x80 | length)
	if head[i+2]&0x80 == 0 || i+3+n > len(head) {
		return FormatMKV
	}
	if string(head[i+3:i+3+n]) == "webm" {
		return FormatWebM
	}
	return FormatMKV
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
