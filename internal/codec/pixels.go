// Package codec holds format detection, header probing and the pure-Go
// loaders and savers (ARCHITECTURE §3). This file defines the cgo-free types
// the own loaders (codec/bmp, codec/ico) hand to internal/vips, which wraps
// them with vips_image_new_from_memory (Raw) or loads them as a buffer
// (Decoded.Embedded).
package codec

import (
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// Raw is a decoded image as 8-bit interleaved samples, ready for
// vips_image_new_from_memory (VIPS_FORMAT_UCHAR, sRGB).
type Raw struct {
	Width  int
	Height int
	// Bands is 3 (RGB) or 4 (RGBA).
	Bands int
	// Data holds Width*Height*Bands bytes, row-major and top-down, samples
	// interleaved in R, G, B[, A] order. Alpha is straight (not
	// premultiplied).
	Data []byte
	// XRes and YRes are the resolution in pixels per millimetre, the unit of
	// VipsImage xres/yres (DPI / 25.4). Zero means the file does not say.
	XRes float64
	YRes float64
}

// Validate reports whether r is well formed: positive dimensions, 3 or 4
// bands and a Data slice of exactly Width*Height*Bands bytes.
func (r *Raw) Validate() error {
	if r.Width <= 0 || r.Height <= 0 {
		return errs.Newf(errs.KindInternal, "codec raw: invalid dimensions %dx%d", r.Width, r.Height)
	}
	if r.Bands != 3 && r.Bands != 4 {
		return errs.Newf(errs.KindInternal, "codec raw: %d bands, want 3 or 4", r.Bands)
	}
	want := int64(r.Width) * int64(r.Height) * int64(r.Bands)
	if int64(len(r.Data)) != want {
		return errs.Newf(errs.KindInternal, "codec raw: data is %d bytes, want %d", len(r.Data), want)
	}
	return nil
}

// Decoded is the result of an own loader. Exactly one of Raw and Embedded is
// set: Embedded is a complete PNG or JPEG stream found inside the container
// (BMP BI_PNG/BI_JPEG, PNG-in-ICO) that libvips must load itself; it aliases
// the input buffer.
type Decoded struct {
	Raw      *Raw
	Embedded []byte
}

// DefaultMaxPixels bounds Width*Height when a loader's option leaves the limit
// at zero. It matches imgproxy's IMGPROXY_MAX_SRC_RESOLUTION default (50 MP).
const DefaultMaxPixels = 50_000_000
