package raw

import (
	"bytes"
	"encoding/binary"
	"strings"
)

// Kind is the specific camera RAW container, for logs and /info. The
// decoder is the same for all of them (LibRaw).
type Kind uint8

// Kinds.
const (
	KindUnknown Kind = iota
	KindCR2          // Canon, TIFF-based
	KindCR3          // Canon, ISOBMFF ("crx " brand)
	KindCRW          // Canon CIFF
	KindNEF          // Nikon (also NRW)
	KindARW          // Sony (also SRF, SR2)
	KindDNG          // Adobe Digital Negative
	KindRAF          // Fujifilm
	KindORF          // Olympus / OM System
	KindRW2          // Panasonic, Leica (RWL)
	KindPEF          // Pentax, Ricoh
	KindSRW          // Samsung
	KindX3F          // Sigma
	Kind3FR          // Hasselblad (also FFF)
	KindIIQ          // Phase One
	KindERF          // Epson
	KindMEF          // Mamiya
	KindMOS          // Leaf
	KindDCR          // Kodak (also KDC)
	KindMRW          // Minolta
)

// String returns the usual file extension of the kind, lower case.
func (k Kind) String() string {
	switch k {
	case KindCR2:
		return "cr2"
	case KindCR3:
		return "cr3"
	case KindCRW:
		return "crw"
	case KindNEF:
		return "nef"
	case KindARW:
		return "arw"
	case KindDNG:
		return "dng"
	case KindRAF:
		return "raf"
	case KindORF:
		return "orf"
	case KindRW2:
		return "rw2"
	case KindPEF:
		return "pef"
	case KindSRW:
		return "srw"
	case KindX3F:
		return "x3f"
	case Kind3FR:
		return "3fr"
	case KindIIQ:
		return "iiq"
	case KindERF:
		return "erf"
	case KindMEF:
		return "mef"
	case KindMOS:
		return "mos"
	case KindDCR:
		return "dcr"
	case KindMRW:
		return "mrw"
	default:
		return "unknown"
	}
}

// has reports whether b starts with s, without allocating.
func has(b []byte, s string) bool { return len(b) >= len(s) && string(b[:len(s)]) == s }

// maxIFDEntries bounds the IFD0 walk of TIFF-shaped files.
const maxIFDEntries = 512

// TIFF tags read by the sniffer.
const (
	tagNewSubfileType = 0x00FE
	tagCompression    = 0x0103
	tagPhotometric    = 0x0106
	tagMake           = 0x010F
	tagSubIFDs        = 0x014A
	tagDNGVersion     = 0xC612
)

// Sniff recognises a camera RAW file from its leading bytes, the way
// LibRaw's identify does: formats with a signature (CR2, CR3, CRW, RAF,
// ORF, RW2, X3F, MRW, IIQ) by magic bytes and ISOBMFF brands, and
// TIFF-shaped formats without one (NEF, ARW, DNG, PEF, SRW, 3FR, ERF, MEF,
// MOS, DCR) by IFD0: a DNGVersion tag, or a camera maker's Make together
// with the structure of a RAW (a reduced-resolution IFD0, SubIFDs, a
// camera-specific compression or a CFA photometric interpretation). A TIFF
// that a camera wrote as a plain RGB image is not a RAW.
//
// Only b is read (at most the first IFD0 it holds, bounded by
// maxIFDEntries), so it is safe on untrusted input of any size.
func Sniff(b []byte) (Kind, bool) {
	k := sniff(b)
	return k, k != KindUnknown
}

func sniff(b []byte) Kind {
	switch {
	case len(b) < 8:
		return KindUnknown
	case has(b, "FUJIFILMCCD-RAW"):
		return KindRAF
	case has(b, "IIRO"), has(b, "IIRS"), has(b, "MMOR"):
		return KindORF
	case has(b, "IIU\x00"):
		return KindRW2
	case has(b, "FOVb"):
		return KindX3F
	case has(b, "\x00MRM"):
		return KindMRW
	case has(b, "IIII"), has(b, "MMMM"):
		return KindIIQ
	case len(b) >= 14 && string(b[:2]) == "II" && string(b[6:14]) == "HEAPCCDR":
		return KindCRW
	case string(b[4:8]) == "ftyp":
		if isCR3(b) {
			return KindCR3
		}
		return KindUnknown
	}
	var order binary.ByteOrder
	switch string(b[:4]) {
	case "II*\x00":
		order = binary.LittleEndian
	case "MM\x00*":
		order = binary.BigEndian
	default:
		return KindUnknown
	}
	if len(b) >= 10 && b[0] == 'I' && b[8] == 'C' && b[9] == 'R' {
		return KindCR2
	}
	// LibRaw: a Phase One block inside the first 32 bytes of a TIFF.
	if head := string(b[4:min(len(b), 32)]); strings.Contains(head, "IIII") || strings.Contains(head, "MMMM") {
		return KindIIQ
	}
	return sniffTIFF(b, order)
}

// isCR3 reports whether an ISOBMFF ftyp box names the Canon "crx " brand
// (major or compatible).
func isCR3(b []byte) bool {
	size := int(binary.BigEndian.Uint32(b))
	if size < 12 || size > len(b) {
		size = len(b)
	}
	size = min(size, 256)
	if size >= 12 && string(b[8:12]) == "crx " {
		return true
	}
	for i := 16; i+4 <= size; i += 4 {
		if string(b[i:i+4]) == "crx " {
			return true
		}
	}
	return false
}

// ifd0 is what the sniffer reads from IFD0.
type ifd0 struct {
	dng, subIFDs            bool
	subfile, compression    uint32
	photometric             uint32
	makeOff                 int64
	makeLen                 uint32
	hasMake, hasCompression bool
}

func sniffTIFF(b []byte, order binary.ByteOrder) Kind {
	off := int64(order.Uint32(b[4:8]))
	if off < 8 || off+2 > int64(len(b)) {
		return KindUnknown
	}
	n := int(order.Uint16(b[off:]))
	if n == 0 || n > maxIFDEntries {
		return KindUnknown
	}
	var d ifd0
	for i := range n {
		e := off + 2 + int64(i)*12
		if e+12 > int64(len(b)) {
			break
		}
		tag := order.Uint16(b[e:])
		typ := order.Uint16(b[e+2:])
		count := order.Uint32(b[e+4:])
		val := func() uint32 {
			if typ == 3 { // SHORT
				return uint32(order.Uint16(b[e+8:]))
			}
			return order.Uint32(b[e+8:])
		}
		switch tag {
		case tagNewSubfileType:
			d.subfile = val()
		case tagCompression:
			d.compression, d.hasCompression = val(), true
		case tagPhotometric:
			d.photometric = val()
		case tagSubIFDs:
			d.subIFDs = true
		case tagDNGVersion:
			d.dng = true
		case tagMake:
			if typ == 2 && count > 0 {
				d.hasMake, d.makeLen = true, count
				if count <= 4 {
					d.makeOff = e + 8
				} else {
					d.makeOff = int64(order.Uint32(b[e+8:]))
				}
			}
		}
	}
	if d.dng {
		return KindDNG
	}
	if !d.hasMake {
		return KindUnknown
	}
	end := d.makeOff + int64(min(d.makeLen, 64))
	if end > int64(len(b)) {
		return KindUnknown
	}
	k := makeKind(string(bytes.TrimRight(b[d.makeOff:end], "\x00 ")))
	if k == KindUnknown || !d.rawShaped() {
		return KindUnknown
	}
	return k
}

// rawShaped reports whether IFD0 looks like a RAW container rather than a
// plain image: a reduced-resolution IFD0 (the preview), SubIFDs, a
// compression plain TIFF writers do not use, or CFA / linear-raw pixels.
func (d *ifd0) rawShaped() bool {
	if d.subfile&1 != 0 || d.subIFDs || d.photometric == 32803 || d.photometric == 34892 {
		return true
	}
	if !d.hasCompression {
		return false
	}
	switch d.compression {
	case 1, 5, 7, 8, 32773, 32946, 34925, 50000, 50001:
		return false // none, LZW, JPEG, deflate, PackBits, LZMA, zstd, WebP
	default:
		return true
	}
}

// prefixFold is strings.HasPrefix ignoring ASCII case.
func prefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// makeKind maps the IFD0 Make of a TIFF-shaped RAW to its kind.
func makeKind(maker string) Kind {
	switch {
	case prefixFold(maker, "NIKON"):
		return KindNEF
	case prefixFold(maker, "SONY"):
		return KindARW
	case prefixFold(maker, "PENTAX"), prefixFold(maker, "RICOH"), prefixFold(maker, "ASAHI"):
		return KindPEF
	case prefixFold(maker, "SAMSUNG"):
		return KindSRW
	case prefixFold(maker, "HASSELBLAD"):
		return Kind3FR
	case prefixFold(maker, "SEIKO EPSON"):
		return KindERF
	case prefixFold(maker, "MAMIYA"):
		return KindMEF
	case prefixFold(maker, "LEAF"):
		return KindMOS
	case prefixFold(maker, "PHASE ONE"):
		return KindIIQ
	case prefixFold(maker, "KODAK"), prefixFold(maker, "EASTMAN KODAK"):
		return KindDCR
	case prefixFold(maker, "CANON"):
		return KindCR2 // TIFF-based Canon RAW without the CR2 marker (EOS-1D TIF)
	case prefixFold(maker, "OLYMPUS"), prefixFold(maker, "OM DIGITAL"):
		return KindORF
	case prefixFold(maker, "PANASONIC"), prefixFold(maker, "LEICA"):
		return KindRW2
	case prefixFold(maker, "FUJIFILM"):
		return KindRAF
	case prefixFold(maker, "MINOLTA"), prefixFold(maker, "KONICA MINOLTA"):
		return KindMRW
	default:
		return KindUnknown
	}
}
