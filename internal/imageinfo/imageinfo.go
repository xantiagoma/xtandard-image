// Package imageinfo computes the pixel-derived fields of imgproxy's /info
// response (docs/URL-DIALECTS.md §3.3, WP-63): palette, average colour,
// dominant colours, BlurHash, ThumbHash and the perceptual hash, plus the
// source hashsums. It is pure Go and works on an 8-bit RGBA copy of the
// image that the caller has already decoded and bounded (internal/app
// downscales to at most MaxSide pixels inside one vips.Run).
package imageinfo

import (
	"errors"
	"strconv"
)

// MaxSide bounds the longest side of the copy the pixel fields are
// computed on. Every algorithm here is O(pixels) or a small multiple, so
// this keeps the cost of an info request with every option on in the
// low tens of milliseconds whatever the source size.
const MaxSide = 512

// ErrInvalidImage is returned for an Image whose Pix does not hold
// Width×Height RGBA pixels.
var ErrInvalidImage = errors.New("imageinfo: invalid image")

// Image is an 8-bit, non-premultiplied RGBA image: Pix holds Width×Height
// pixels, row by row, 4 bytes each.
type Image struct {
	Width, Height int
	Pix           []byte
}

// Validate reports whether m is a non-empty image whose Pix matches its
// size.
func (m *Image) Validate() error {
	if m == nil || m.Width <= 0 || m.Height <= 0 || len(m.Pix) != m.Width*m.Height*4 {
		return ErrInvalidImage
	}
	return nil
}

// Color is an 8-bit RGBA colour, not premultiplied.
type Color struct {
	R, G, B, A uint8
}

// Hex is the colour as "rrggbb" (alpha dropped), for logs and tests.
func (c Color) Hex() string {
	const digits = "0123456789abcdef"
	b := make([]byte, 0, 6)
	for _, v := range [...]uint8{c.R, c.G, c.B} {
		b = append(b, digits[v>>4], digits[v&15])
	}
	return string(b)
}

// String is "#rrggbbaa".
func (c Color) String() string {
	a := strconv.FormatUint(uint64(c.A)|0x100, 16)[1:]
	return "#" + c.Hex() + a
}
