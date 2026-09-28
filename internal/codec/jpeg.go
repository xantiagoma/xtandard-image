package codec

import (
	"encoding/binary"
	"fmt"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// maxJPEGMarkers bounds the marker walk of JPEGFrame.
const maxJPEGMarkers = 4096

// JPEG frame markers (ITU T.81 table B.1, T.87 for JPEG-LS).
const (
	sofBaseline    = 0xC0
	sofExtended    = 0xC1
	sofProgressive = 0xC2
	sofLossless    = 0xC3
	sofExtendedAC  = 0xC9
	sofProgressAC  = 0xCA
	sofLosslessAC  = 0xCB
	sofJPEGLS      = 0xF7
)

// JPEGFrame returns the marker (SOFn, or SOF55 for JPEG-LS) and the sample
// precision (bits) of the first frame header of a JPEG stream, walking the
// marker segments that precede it; ok is false when none is found before
// the scan data or the stream is malformed (the loader reports those).
func JPEGFrame(b []byte) (sof byte, bits int, ok bool) {
	if len(b) < 2 || b[0] != 0xFF || b[1] != 0xD8 {
		return 0, 0, false
	}
	i := 2
	for range maxJPEGMarkers {
		for i+1 < len(b) && b[i] == 0xFF && b[i+1] == 0xFF {
			i++ // fill bytes
		}
		if i+4 > len(b) || b[i] != 0xFF {
			return 0, 0, false
		}
		m := b[i+1]
		switch {
		case m == 0x01 || (m >= 0xD0 && m <= 0xD7):
			i += 2
			continue
		case m == 0xD8 || m == 0xD9 || m == 0xDA || m == 0x00:
			return 0, 0, false
		}
		n := int(binary.BigEndian.Uint16(b[i+2:]))
		if n < 2 || i+2+n > len(b) {
			return 0, 0, false
		}
		if (m >= 0xC0 && m <= 0xCF && m != 0xC4 && m != 0xC8 && m != 0xCC) || m == sofJPEGLS {
			if n < 3 {
				return 0, 0, false
			}
			return m, int(b[i+4]), true
		}
		i += 2 + n
	}
	return 0, 0, false
}

// checkJPEGFrame refuses the JPEG frames no loader decodes, with a clear
// 422 instead of a failed decode. libvips' jpegload reads 8-bit DCT frames
// and the shim's jpeghpload (libjpeg-turbo's 12- and 16-bit APIs, WP-106)
// 12-bit DCT frames and Huffman lossless frames of 2–16 bits (the same
// rule as xi_is_jpeghp); libjpeg-turbo has no hierarchical, arithmetic
// lossless or JPEG-LS decoder.
func checkJPEGFrame(data []byte) error {
	sof, bits, ok := JPEGFrame(data)
	if !ok {
		return nil
	}
	var public string
	switch sof {
	case sofBaseline, sofExtended, sofProgressive, sofExtendedAC, sofProgressAC:
		if bits == 8 || bits == 12 {
			return nil
		}
		public = fmt.Sprintf("%d-bit JPEG is not supported", bits)
	case sofLossless:
		if bits >= 2 && bits <= 16 {
			return nil
		}
		public = fmt.Sprintf("%d-bit lossless JPEG is not supported", bits)
	case sofLosslessAC:
		public = "arithmetic-coded lossless JPEG is not supported"
	case sofJPEGLS:
		public = "JPEG-LS is not supported"
	default:
		public = "hierarchical JPEG is not supported"
	}
	return errs.WithPublic(errs.Newf(errs.KindUnsupported, "codec: JPEG frame SOF%d, %d bits", sof&0x3F, bits), public)
}
