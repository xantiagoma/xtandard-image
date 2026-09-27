// Package wbmp decodes Wireless Bitmap (WBMP, WAP 1.x "type 0") images in
// pure Go: a two-byte header, the width and height as multi-byte integers
// and one bit per pixel, rows padded to a byte, 1 = white.
//
// WBMP has no magic number. Sniff therefore accepts a file only when its
// length is exactly what the header declares, so it needs the whole file.
// ImageMagick reads WBMP too, but it cannot recognise the format by content
// (libvips' magickload never picks it), hence this loader.
package wbmp

import (
	"github.com/xantiagoma/xtandard-image/internal/codec/raster"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// maxSide bounds each dimension (WBMP is a phone-screen format; larger
// values are corrupt or hostile).
const maxSide = 1 << 16

// Config is what the header declares.
type Config struct {
	Width, Height int
	// offset is where the pixel rows start.
	offset int
}

// Options control Decode.
type Options struct {
	// MaxPixels bounds Width*Height (errs.KindLimitExceeded). Zero means
	// raster.DefaultMaxPixels.
	MaxPixels int64
}

// uintvar reads a WAP multi-byte integer (7 bits per byte, high bit set on
// every byte but the last) at b[i:], returning it and the index after it.
func uintvar(b []byte, i int) (int, int, bool) {
	v := 0
	for n := 0; i < len(b) && n < 4; n++ {
		c := b[i]
		i++
		v = v<<7 | int(c&0x7F)
		if c&0x80 == 0 {
			return v, i, true
		}
	}
	return 0, i, false
}

// DecodeConfig parses the header of a complete WBMP file. The pixel data
// must be exactly as long as the header says (no trailing bytes: nothing
// else identifies the format).
func DecodeConfig(b []byte) (Config, error) {
	// Type 0 and a fixed header with no extension headers.
	if len(b) < 5 || b[0] != 0 || b[1] != 0 {
		return Config{}, errs.New(errs.KindCorrupt, "wbmp: not a type 0 WBMP")
	}
	w, i, ok := uintvar(b, 2)
	if !ok {
		return Config{}, errs.New(errs.KindCorrupt, "wbmp: bad width")
	}
	h, i, ok := uintvar(b, i)
	if !ok {
		return Config{}, errs.New(errs.KindCorrupt, "wbmp: bad height")
	}
	if w <= 0 || h <= 0 || w > maxSide || h > maxSide {
		return Config{}, errs.Newf(errs.KindCorrupt, "wbmp: %dx%d", w, h)
	}
	if want := (w + 7) / 8 * h; len(b)-i != want {
		return Config{}, errs.Newf(errs.KindCorrupt, "wbmp: %d bytes of pixels, want %d", len(b)-i, want)
	}
	return Config{Width: w, Height: h, offset: i}, nil
}

// Sniff reports whether b is a complete, self-consistent WBMP file.
func Sniff(b []byte) bool {
	_, err := DecodeConfig(b)
	return err == nil
}

// Decode decodes a WBMP file to 8-bit RGB (black and white).
func Decode(b []byte, o Options) (raster.Decoded, error) {
	c, err := DecodeConfig(b)
	if err != nil {
		return raster.Decoded{}, err
	}
	limit := o.MaxPixels
	if limit <= 0 {
		limit = raster.DefaultMaxPixels
	}
	if int64(c.Width)*int64(c.Height) > limit {
		return raster.Decoded{}, errs.Newf(errs.KindLimitExceeded, "wbmp: %dx%d exceeds %d pixels", c.Width, c.Height, limit)
	}
	stride := (c.Width + 7) / 8
	out := make([]byte, c.Width*c.Height*3)
	for y := range c.Height {
		row := b[c.offset+y*stride:]
		for x := range c.Width {
			if row[x>>3]&(0x80>>(x&7)) != 0 {
				p := (y*c.Width + x) * 3
				out[p], out[p+1], out[p+2] = 255, 255, 255
			}
		}
	}
	return raster.Decoded{Raw: &raster.Raw{Width: c.Width, Height: c.Height, Bands: 3, Data: out}}, nil
}
