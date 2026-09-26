package codec

import (
	"bytes"
	"encoding/binary"
)

// EXIFThumbnail is the JPEG thumbnail a camera or editor embedded in the
// Exif APP1 segment of a JPEG (IFD1, JPEGInterchangeFormat).
type EXIFThumbnail struct {
	// Data is the complete thumbnail JPEG, a sub-slice of the source.
	Data []byte
	// Width and Height are read from the thumbnail's SOF segment, as stored
	// (the Exif orientation of the main image applies to it too).
	Width, Height int
}

// maxIFDEntries bounds the entries read from one IFD (a TIFF IFD count is
// 16 bits; real IFD1s have fewer than 20).
const maxIFDEntries = 512

// FindEXIFThumbnail returns the JPEG thumbnail embedded in the Exif data of
// the JPEG file b, reading only the header segments before the first SOS.
// ok is false when there is none, when it is not JPEG-compressed, or when
// any offset or length is out of bounds: malformed input is never an error,
// only "no thumbnail".
func FindEXIFThumbnail(b []byte) (t EXIFThumbnail, ok bool) {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return t, false
	}
	for i := 2; i+4 <= len(b); {
		if b[i] != 0xFF {
			return t, false
		}
		m := b[i+1]
		switch {
		case m == 0xFF: // fill byte
			i++
			continue
		case m == 0x01 || (m >= 0xD0 && m <= 0xD8):
			i += 2
			continue
		case m == 0xD9 || m == 0xDA:
			return t, false
		}
		n := int(binary.BigEndian.Uint16(b[i+2:]))
		if n < 2 || i+2+n > len(b) {
			return t, false
		}
		if seg := b[i+4 : i+2+n]; m == 0xE1 && bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
			if t, ok = exifThumbnail(seg[6:]); ok {
				return t, true
			}
		}
		i += 2 + n
	}
	return t, false
}

// exifThumbnail reads IFD1 of the TIFF structure tiff (the Exif payload).
func exifThumbnail(tiff []byte) (EXIFThumbnail, bool) {
	if len(tiff) < 8 {
		return EXIFThumbnail{}, false
	}
	var bo binary.ByteOrder
	switch {
	case bytes.HasPrefix(tiff, []byte("II*\x00")):
		bo = binary.LittleEndian
	case bytes.HasPrefix(tiff, []byte("MM\x00*")):
		bo = binary.BigEndian
	default:
		return EXIFThumbnail{}, false
	}
	ifd0 := int64(bo.Uint32(tiff[4:]))
	count, ok := ifdCount(tiff, ifd0, bo)
	if !ok {
		return EXIFThumbnail{}, false
	}
	next := ifd0 + 2 + 12*int64(count)
	if next+4 > int64(len(tiff)) {
		return EXIFThumbnail{}, false
	}
	ifd1 := int64(bo.Uint32(tiff[next:]))
	if ifd1 == 0 || ifd1 == ifd0 {
		return EXIFThumbnail{}, false
	}
	count, ok = ifdCount(tiff, ifd1, bo)
	if !ok {
		return EXIFThumbnail{}, false
	}
	var off, length int64 = -1, -1
	compression := 6 // absent: JPEGInterchangeFormat implies JPEG
	for k := range int64(count) {
		e := tiff[ifd1+2+12*k:]
		tag, typ := bo.Uint16(e), bo.Uint16(e[2:])
		var v int64
		switch typ {
		case 3: // SHORT
			v = int64(bo.Uint16(e[8:]))
		case 4: // LONG
			v = int64(bo.Uint32(e[8:]))
		default:
			continue
		}
		switch tag {
		case 0x0103:
			compression = int(v)
		case 0x0201:
			off = v
		case 0x0202:
			length = v
		}
	}
	if compression != 6 || off <= 0 || length < 4 || off+length > int64(len(tiff)) {
		return EXIFThumbnail{}, false
	}
	data := tiff[off : off+length]
	w, h, ok := jpegSize(data)
	if !ok {
		return EXIFThumbnail{}, false
	}
	return EXIFThumbnail{Data: data, Width: w, Height: h}, true
}

// ifdCount returns the entry count of the IFD at off, checking that all its
// entries and the next-IFD offset are inside tiff.
func ifdCount(tiff []byte, off int64, bo binary.ByteOrder) (int, bool) {
	if off < 8 || off+2 > int64(len(tiff)) {
		return 0, false
	}
	n := int(bo.Uint16(tiff[off:]))
	if n == 0 || n > maxIFDEntries || off+2+12*int64(n)+4 > int64(len(tiff)) {
		return 0, false
	}
	return n, true
}

// jpegSize reads the frame size from the first SOF segment of a JPEG.
func jpegSize(b []byte) (w, h int, ok bool) {
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
		// SOF0..SOF15 except DHT (C4), JPG (C8) and DAC (CC).
		if m >= 0xC0 && m <= 0xCF && m != 0xC4 && m != 0xC8 && m != 0xCC {
			if n < 8 {
				return 0, 0, false
			}
			h = int(binary.BigEndian.Uint16(b[i+5:]))
			w = int(binary.BigEndian.Uint16(b[i+7:]))
			return w, h, w > 0 && h > 0
		}
		i += 2 + n
	}
	return 0, 0, false
}
