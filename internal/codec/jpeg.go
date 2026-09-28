package codec

import (
	"encoding/binary"
	"fmt"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// maxJPEGMarkers bounds the marker walk of JPEGPrecision.
const maxJPEGMarkers = 4096

// JPEGPrecision returns the sample precision (bits) of the first frame
// header (SOFn) of a JPEG stream, walking the marker segments that precede
// it; ok is false when none is found before the scan data or the stream is
// malformed (the loader reports those).
func JPEGPrecision(b []byte) (bits int, ok bool) {
	if len(b) < 2 || b[0] != 0xFF || b[1] != 0xD8 {
		return 0, false
	}
	i := 2
	for range maxJPEGMarkers {
		for i+1 < len(b) && b[i] == 0xFF && b[i+1] == 0xFF {
			i++ // fill bytes
		}
		if i+4 > len(b) || b[i] != 0xFF {
			return 0, false
		}
		m := b[i+1]
		switch {
		case m == 0x01 || (m >= 0xD0 && m <= 0xD7):
			i += 2
			continue
		case m == 0xD8 || m == 0xD9 || m == 0xDA || m == 0x00:
			return 0, false
		}
		n := int(binary.BigEndian.Uint16(b[i+2:]))
		if n < 2 || i+2+n > len(b) {
			return 0, false
		}
		switch m {
		case 0xC0, 0xC1, 0xC2, 0xC3, 0xC5, 0xC6, 0xC7, 0xC9, 0xCA, 0xCB, 0xCD, 0xCE, 0xCF:
			if n < 3 {
				return 0, false
			}
			return int(b[i+4]), true
		}
		i += 2 + n
	}
	return 0, false
}

// checkJPEGPrecision refuses JPEG frames that are not 8 bits per sample
// (12-bit DCT, 16-bit lossless): libvips reads them with libjpeg's 8-bit
// API, which accepts their header and fails once decoding starts. Refusing
// at the probe gives a clear 422 instead of a failed decode (WP-106 adds
// the 12/16-bit decoder).
func checkJPEGPrecision(data []byte) error {
	bits, ok := JPEGPrecision(data)
	if !ok || bits == 8 {
		return nil
	}
	return errs.WithPublic(errs.Newf(errs.KindUnsupported, "codec: %d-bit JPEG", bits),
		fmt.Sprintf("%d-bit JPEG is not supported yet", bits))
}
