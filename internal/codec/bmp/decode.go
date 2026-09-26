// Portions derived from imgproxy's vips/bmpload.c,
// https://github.com/imgproxy/imgproxy, Copyright (c) 2017 Sergey
// Alexandrovich, licensed under the Apache License, Version 2.0 (see /NOTICE).
// Modifications: rewritten in pure Go; generic bitfield scaling instead of
// fixed shifts; all-zero alpha treated as opaque; ICO AND masks applied.

package bmp

import (
	"bytes"
	"math/bits"

	"github.com/xantiagoma/xtandard-image/internal/codec/raster"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

var (
	pngSignature  = []byte("\x89PNG\r\n\x1a\n")
	jpegSignature = []byte{0xFF, 0xD8, 0xFF}
)

// Decode decodes a BMP file. The result holds either Raw pixels or, for a
// BI_PNG/BI_JPEG bitmap, the Embedded stream. Malformed input fails with
// errs.KindCorrupt, an image over Options.MaxPixels with
// errs.KindLimitExceeded; Decode never panics.
func Decode(data []byte, o Options) (raster.Decoded, error) {
	h, err := parseFile(data)
	if err != nil {
		return raster.Decoded{}, err
	}
	return decode(data, h, o, false)
}

// DecodeIcon decodes the DIB of an ICO/CUR entry: no file header, a doubled
// height, and a 1-bit AND mask after the pixels that becomes the alpha channel
// unless the bitmap carries its own (non-zero) 32-bit alpha.
func DecodeIcon(dib []byte, o Options) (raster.Decoded, error) {
	h, err := parseDIB(dib, 0, -1, true)
	if err != nil {
		return raster.Decoded{}, err
	}
	return decode(dib, h, o, true)
}

func decode(b []byte, h *header, o Options, icon bool) (raster.Decoded, error) {
	if limit := o.maxPixels(); int64(h.width)*int64(h.height) > limit {
		return raster.Decoded{}, errs.Newf(errs.KindLimitExceeded,
			"bmp: %dx%d exceeds %d pixels", h.width, h.height, limit)
	}
	if h.embedded {
		return decodeEmbedded(b, h)
	}
	raw := &raster.Raw{
		Width:  h.width,
		Height: h.height,
		XRes:   ppmToPerMM(h.xppm),
		YRes:   ppmToPerMM(h.yppm),
	}
	var err error
	if h.rle() {
		err = decodeRLE(b[h.pixOff:rleEnd(b, h)], h, raw)
	} else {
		err = decodeUncompressed(b, h, raw, icon)
	}
	if err != nil {
		return raster.Decoded{}, err
	}
	return raster.Decoded{Raw: raw}, nil
}

func decodeEmbedded(b []byte, h *header) (raster.Decoded, error) {
	p := b[h.pixOff:]
	if n := int(h.imageSize); n > 0 && n <= len(p) {
		p = p[:n]
	}
	sig := pngSignature
	if h.compression == biJPEG {
		sig = jpegSignature
	}
	if !bytes.HasPrefix(p, sig) {
		return raster.Decoded{}, corrupt("embedded stream does not match its compression")
	}
	return raster.Decoded{Embedded: p}, nil
}

func rleEnd(b []byte, h *header) int {
	if n := int64(h.imageSize); n > 0 && int64(h.pixOff)+n <= int64(len(b)) {
		return h.pixOff + int(n)
	}
	return len(b)
}

// ppmToPerMM converts pixels per metre to pixels per millimetre.
func ppmToPerMM(ppm int64) float64 {
	if ppm <= 0 {
		return 0
	}
	return float64(ppm) / 1000
}

func decodeUncompressed(b []byte, h *header, raw *raster.Raw, icon bool) error {
	w, ht := h.width, h.height
	stride64 := (int64(w)*int64(h.bpp) + 31) / 32 * 4
	if int64(h.pixOff)+stride64*int64(ht) > int64(len(b)) {
		return corrupt("truncated pixel data")
	}
	stride := int(stride64)

	alpha := h.masks[3] != 0
	bands := 3
	if alpha || icon {
		bands = 4
	}
	out := make([]byte, w*ht*bands)
	rowLen := w * bands

	var fields [4]field
	if h.bpp == 16 || h.bpp == 32 {
		for i, m := range h.masks {
			fields[i] = newField(m)
		}
	}
	std32 := h.bpp == 32 && h.masks[0] == 0x00FF0000 && h.masks[1] == 0x0000FF00 &&
		h.masks[2] == 0x000000FF && (h.masks[3] == 0 || h.masks[3] == 0xFF000000)

	for r := range ht {
		src := b[h.pixOff+r*stride : h.pixOff+(r+1)*stride]
		dst := out[h.row(r)*rowLen:][:rowLen]
		switch {
		case h.bpp <= 8:
			paletteRow(dst, src, w, h.bpp, bands, h.palette)
		case h.bpp == 24:
			for x, i := 0, 0; x < w; x, i = x+1, i+bands {
				s := src[3*x:]
				dst[i], dst[i+1], dst[i+2] = s[2], s[1], s[0]
				if bands == 4 {
					dst[i+3] = 0xFF
				}
			}
		case std32:
			for x, i := 0, 0; x < w; x, i = x+1, i+bands {
				s := src[4*x:]
				dst[i], dst[i+1], dst[i+2] = s[2], s[1], s[0]
				if bands == 4 {
					dst[i+3] = 0xFF
					if alpha {
						dst[i+3] = s[3]
					}
				}
			}
		default:
			bitfieldRow(dst, src, w, h.bpp, bands, alpha, &fields)
		}
	}

	switch {
	case icon && (!alpha || !anyAlpha(out)):
		// No usable alpha of its own: the AND mask decides transparency.
		if applyANDMask(b, h, stride, out) {
			raw.Bands, raw.Data = 4, out
			return nil
		}
		raw.Bands, raw.Data = 3, dropAlpha(out)
	case alpha && !anyAlpha(out):
		// Many writers leave the fourth byte zero: treat it as opaque.
		raw.Bands, raw.Data = 3, dropAlpha(out)
	default:
		raw.Bands, raw.Data = bands, out
	}
	return nil
}

// row maps the r-th stored row to its image row.
func (h *header) row(r int) int {
	if h.topDown {
		return r
	}
	return h.height - 1 - r
}

func paletteRow(dst, src []byte, w, bpp, bands int, pal *[256][4]byte) {
	if bpp == 8 {
		for x, i := 0, 0; x < w; x, i = x+1, i+bands {
			copy(dst[i:i+bands], pal[src[x]][:bands])
		}
		return
	}
	perByte := 8 / bpp
	mask := byte(1<<bpp - 1)
	for x, i := 0, 0; x < w; x, i = x+1, i+bands {
		shift := 8 - bpp*(x%perByte+1)
		idx := src[x/perByte] >> shift & mask
		copy(dst[i:i+bands], pal[idx][:bands])
	}
}

func bitfieldRow(dst, src []byte, w, bpp, bands int, alpha bool, f *[4]field) {
	for x, i := 0, 0; x < w; x, i = x+1, i+bands {
		var px uint32
		if bpp == 16 {
			px = uint32(le16(src[2*x:]))
		} else {
			px = le32(src[4*x:])
		}
		dst[i], dst[i+1], dst[i+2] = f[0].value(px), f[1].value(px), f[2].value(px)
		if bands == 4 {
			dst[i+3] = 0xFF
			if alpha {
				dst[i+3] = f[3].value(px)
			}
		}
	}
}

// field extracts one channel of a bitfield pixel and scales it to 8 bits.
type field struct {
	mask  uint32
	shift int
	bits  int
	lut   [256]byte // for bits < 8: value → round(v*255/(2^bits-1))
}

func newField(mask uint32) field {
	f := field{mask: mask}
	if mask == 0 {
		return f
	}
	f.shift = bits.TrailingZeros32(mask)
	f.bits = bits.OnesCount32(mask)
	if f.bits < 8 {
		top := uint32(1)<<f.bits - 1
		for v := range top + 1 {
			f.lut[v] = u8((v*255 + top/2) / top)
		}
	}
	return f
}

func (f *field) value(px uint32) byte {
	if f.mask == 0 {
		return 0
	}
	v := (px & f.mask) >> f.shift
	if f.bits >= 8 {
		return u8(v >> (f.bits - 8))
	}
	return f.lut[v]
}

// applyANDMask sets alpha from the icon's 1-bit AND mask (1 = transparent).
// It reports whether any pixel is transparent. A missing or truncated mask
// leaves the icon opaque.
func applyANDMask(b []byte, h *header, stride int, out []byte) bool {
	w, ht := h.width, h.height
	maskStride := (w + 31) / 32 * 4
	off := int64(h.pixOff) + int64(stride)*int64(ht)
	if off+int64(maskStride)*int64(ht) > int64(len(b)) {
		return false
	}
	transparent := false
	for r := range ht {
		m := b[int(off)+r*maskStride:]
		dst := out[h.row(r)*w*4:]
		for x := range w {
			if m[x/8]>>(7-x%8)&1 == 1 {
				dst[4*x+3] = 0
				transparent = true
			} else {
				dst[4*x+3] = 0xFF
			}
		}
	}
	return transparent
}

func anyAlpha(p []byte) bool {
	for i := 3; i < len(p); i += 4 {
		if p[i] != 0 {
			return true
		}
	}
	return false
}

func allOpaque(p []byte) bool {
	for i := 3; i < len(p); i += 4 {
		if p[i] != 0xFF {
			return false
		}
	}
	return true
}

// dropAlpha compacts RGBA samples to RGB in place.
func dropAlpha(p []byte) []byte {
	n := len(p) / 4
	for i := range n {
		copy(p[3*i:3*i+3], p[4*i:4*i+3])
	}
	return p[: 3*n : 3*n]
}
