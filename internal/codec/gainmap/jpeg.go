package gainmap

import (
	"bytes"
	"encoding/binary"
)

// JPEG segment signatures.
const (
	sigMPF = "MPF\x00"
	sigXMP = "http://ns.adobe.com/xap/1.0/\x00"
	sigISO = "urn:iso:std:iso:ts:21496:-1\x00"
)

// JPEG is the gain map of a JPEG file: the byte range of the MPF secondary
// image that holds it (a complete JPEG) and its metadata.
type JPEG struct {
	Offset, Length int
	Meta           Metadata
}

// FindJPEG finds the gain map of a JPEG: the second image of its MPF index
// (APP2 "MPF"), when that image carries ISO 21496-1 metadata (APP2) or
// Adobe hdrgm XMP (APP1). This is the layout of UltraHDR, Adobe and Apple
// (iOS 18) gain-map JPEGs; files that libultrahdr reads come through vips
// uhdrload instead, this covers the rest (Adobe's, for one). ok is false
// when there is no gain map; err reports one whose metadata is unusable.
func FindJPEG(src []byte) (g JPEG, ok bool, err error) {
	start, end, found := jpegSegment(src, 0, 0xE2, sigMPF)
	if !found {
		return JPEG{}, false, nil
	}
	off, n, found := mpfSecond(src, start+len(sigMPF), end)
	if !found {
		return JPEG{}, false, nil
	}
	img := src[off : off+n]
	// ISO 21496-1 first: it is the standard, XMP the fallback.
	if s, e, found := jpegSegment(img, 0, 0xE2, sigISO); found && e-s > len(sigISO)+4 {
		m, err := ParseISO(img[s+len(sigISO) : e])
		if err != nil {
			return JPEG{}, true, err
		}
		return JPEG{Offset: off, Length: n, Meta: m}, true, nil
	}
	for at := 0; ; {
		s, e, found := jpegSegment(img, at, 0xE1, sigXMP)
		if !found {
			return JPEG{}, false, nil
		}
		m, has, err := ParseXMP(img[s+len(sigXMP) : e])
		if err != nil {
			return JPEG{}, true, err
		}
		if has {
			return JPEG{Offset: off, Length: n, Meta: m}, true, nil
		}
		at = e
	}
}

// jpegSegment finds the first APPn segment (marker) starting at or after
// byte from of the JPEG header in b whose payload starts with sig, and
// returns the payload's range (signature included). Only the header before
// SOS is walked.
func jpegSegment(b []byte, from int, marker byte, sig string) (start, end int, ok bool) {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 0, 0, false
	}
	for i := 2; i+4 <= len(b); {
		if b[i] != 0xFF {
			return 0, 0, false
		}
		m := b[i+1]
		switch {
		case m == 0xFF:
			i++
			continue
		case m == 0x01 || (m >= 0xD0 && m <= 0xD8):
			i += 2
			continue
		case m == 0xD9 || m == 0xDA:
			return 0, 0, false
		}
		n := int(binary.BigEndian.Uint16(b[i+2:]))
		if n < 2 || i+2+n > len(b) {
			return 0, 0, false
		}
		if m == marker && i >= from && bytes.HasPrefix(b[i+4:i+2+n], []byte(sig)) {
			return i + 4, i + 2 + n, true
		}
		i += 2 + n
	}
	return 0, 0, false
}

// MP Format tags (CIPA DC-007).
const (
	mpTagNumberOfImages = 0xB001
	mpTagEntry          = 0xB002
	mpEntrySize         = 16
	maxMPImages         = 64
)

// mpfSecond reads the MP index whose TIFF header spans src[base:end] (the
// APP2 payload after "MPF\0") and returns the byte range in src of the
// second image. MP offsets count from that TIFF header.
func mpfSecond(src []byte, base, end int) (off, n int, ok bool) {
	t := src[base:end]
	if len(t) < 8 {
		return 0, 0, false
	}
	var bo binary.ByteOrder
	switch string(t[:4]) {
	case "II*\x00":
		bo = binary.LittleEndian
	case "MM\x00*":
		bo = binary.BigEndian
	default:
		return 0, 0, false
	}
	ifd := int(bo.Uint32(t[4:]))
	if ifd < 8 || ifd > len(t)-2 {
		return 0, 0, false
	}
	count := int(bo.Uint16(t[ifd:]))
	images, entryOff := 0, -1
	for k := range min(count, 64) {
		e := ifd + 2 + 12*k
		if e > len(t)-12 {
			return 0, 0, false
		}
		switch bo.Uint16(t[e:]) {
		case mpTagNumberOfImages:
			images = int(bo.Uint32(t[e+8:]))
		case mpTagEntry:
			if bo.Uint32(t[e+4:]) >= 2*mpEntrySize {
				entryOff = int(bo.Uint32(t[e+8:]))
			}
		}
	}
	if images < 2 || images > maxMPImages || entryOff < 0 || entryOff > len(t)-2*mpEntrySize {
		return 0, 0, false
	}
	second := t[entryOff+mpEntrySize:]
	size, start := int(bo.Uint32(second[4:])), int(bo.Uint32(second[8:]))
	off = base + start
	if start == 0 || size < 4 || off > len(src)-4 || size > len(src)-off {
		return 0, 0, false
	}
	if src[off] != 0xFF || src[off+1] != 0xD8 {
		return 0, 0, false
	}
	return off, size, true
}
