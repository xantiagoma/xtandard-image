package raw

import "encoding/binary"

// maxJPEGSegments bounds the marker walk of JPEGSize.
const maxJPEGSegments = 4096

// JPEGSize reads the frame header of a JPEG stream the way LibRaw's
// unpack_thumb reads an embedded preview: the first two bytes are taken as
// the SOI marker whatever they are (LibRaw overwrites them, since some
// cameras store previews without it). It returns the frame size and the
// number of components, and ok only for a decodable 8-bit baseline,
// extended or progressive Huffman frame (SOF0, SOF1, SOF2) with 1 or 3
// components and a non-zero size. Lossless, arithmetic-coded and 12-bit
// frames, H.265 previews and anything unparsable report false.
//
// Only the segment headers before the frame header are read.
func JPEGSize(b []byte) (width, height, components int, ok bool) {
	i := 2
	for range maxJPEGSegments {
		// Skip fill bytes before a marker.
		for i < len(b) && b[i] == 0xFF && i+1 < len(b) && b[i+1] == 0xFF {
			i++
		}
		if i+4 > len(b) || b[i] != 0xFF {
			return 0, 0, 0, false
		}
		m := b[i+1]
		switch {
		case m == 0x01 || (m >= 0xD0 && m <= 0xD7): // no payload
			i += 2
			continue
		case m == 0xD8 || m == 0xD9 || m == 0xDA || m == 0x00:
			return 0, 0, 0, false // SOI again, EOI or SOS before a frame
		}
		n := int(binary.BigEndian.Uint16(b[i+2:]))
		if n < 2 || i+2+n > len(b) {
			return 0, 0, 0, false
		}
		switch m {
		case 0xC0, 0xC1, 0xC2:
			seg := b[i+4 : i+2+n]
			if len(seg) < 6 || seg[0] != 8 {
				return 0, 0, 0, false
			}
			h := int(binary.BigEndian.Uint16(seg[1:]))
			w := int(binary.BigEndian.Uint16(seg[3:]))
			c := int(seg[5])
			if w == 0 || h == 0 || (c != 1 && c != 3) || len(seg) < 6+3*c {
				return 0, 0, 0, false
			}
			return w, h, c, true
		case 0xC3, 0xC5, 0xC6, 0xC7, 0xC9, 0xCA, 0xCB, 0xCD, 0xCE, 0xCF:
			return 0, 0, 0, false // lossless, hierarchical or arithmetic coding
		}
		i += 2 + n
	}
	return 0, 0, 0, false
}
