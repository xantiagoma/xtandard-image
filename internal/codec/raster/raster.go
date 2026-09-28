// Package raster holds the cgo-free pixel types the own loaders
// (codec/bmp, codec/ico, codec/dds, ...) hand to internal/vips, which wraps
// them with vips_image_new_from_memory (Raw, Float) or loads them as a buffer
// (Decoded.Embedded). It is a leaf so that package codec can import the
// loaders for probing without an import cycle; codec re-exports the types
// as aliases.
package raster

import (
	"math"

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

// Float is a decoded scene-linear image (HDR textures such as DDS BC6H) as
// float32 samples, ready for vips_image_new_from_memory (VIPS_FORMAT_FLOAT,
// scRGB): the pipeline tone-maps it like OpenEXR.
type Float struct {
	Width  int
	Height int
	// Bands is 3 (RGB) or 4 (RGBA).
	Bands int
	// Data holds Width*Height*Bands samples, row-major and top-down,
	// interleaved in R, G, B[, A] order: linear light, 1.0 = diffuse white;
	// alpha is straight in [0, 1].
	Data []float32
}

// Validate reports whether f is well formed: positive dimensions, 3 or 4
// bands and a Data slice of exactly Width*Height*Bands samples.
func (f *Float) Validate() error {
	if f.Width <= 0 || f.Height <= 0 {
		return errs.Newf(errs.KindInternal, "codec float: invalid dimensions %dx%d", f.Width, f.Height)
	}
	if f.Bands != 3 && f.Bands != 4 {
		return errs.Newf(errs.KindInternal, "codec float: %d bands, want 3 or 4", f.Bands)
	}
	want := int64(f.Width) * int64(f.Height) * int64(f.Bands)
	if int64(len(f.Data)) != want {
		return errs.Newf(errs.KindInternal, "codec float: data is %d samples, want %d", len(f.Data), want)
	}
	return nil
}

// ClippedSRGB converts f to 8-bit sRGB by clipping the linear samples to
// [0, 1] and applying the sRGB transfer function, without tone mapping: a
// cheap preview for /info sampling and detection (the pipeline tone-maps).
func (f *Float) ClippedSRGB() *Raw {
	out := &Raw{Width: f.Width, Height: f.Height, Bands: f.Bands, Data: make([]byte, len(f.Data))}
	for i, v := range f.Data {
		x := float64(min(max(v, 0), 1)) // NaN clips to 0
		if f.Bands == 4 && i%4 == 3 {
			out.Data[i] = uint8(x*255 + 0.5)
			continue
		}
		if x <= 0.0031308 {
			x *= 12.92
		} else {
			x = 1.055*math.Pow(x, 1/2.4) - 0.055
		}
		out.Data[i] = uint8(x*255 + 0.5)
	}
	return out
}

// Decoded is the result of an own loader. Exactly one of Raw, Float and
// Embedded is set: Embedded is a complete PNG or JPEG stream found inside
// the container (BMP BI_PNG/BI_JPEG, PNG-in-ICO) that libvips must load
// itself; it aliases the input buffer.
type Decoded struct {
	Raw      *Raw
	Float    *Float
	Embedded []byte
}

// DefaultMaxPixels bounds Width*Height when a loader's option leaves the limit
// at zero. It matches imgproxy's IMGPROXY_MAX_SRC_RESOLUTION default (50 MP).
const DefaultMaxPixels = 50_000_000
