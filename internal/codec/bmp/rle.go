// Portions derived from imgproxy's vips/bmpload.c
// (vips_foreign_load_bmp_rle_generate_strip),
// https://github.com/imgproxy/imgproxy, Copyright (c) 2017 Sergey
// Alexandrovich, licensed under the Apache License, Version 2.0 (see /NOTICE).
// Modifications: rewritten in pure Go over a byte slice; pixels skipped by
// delta/EOL/EOF codes decode as transparent (as browsers do) instead of
// black, and the image stays RGB when no pixel was skipped.

package bmp

import "github.com/xantiagoma/xtandard-image/internal/codec/raster"

// RLE escape codes (first byte 0).
const (
	rleEOL   = 0
	rleEOF   = 1
	rleDelta = 2
)

// decodeRLE expands RLE8/RLE4 data. The stream may end without an EOF code
// after a complete command; a command cut short is corrupt.
func decodeRLE(src []byte, h *header, raw *raster.Raw) error {
	w, ht := h.width, h.height
	out := make([]byte, w*ht*4) // alpha 0 marks pixels the stream never set
	rle4 := h.compression == biRLE4
	pal := h.palette

	put := func(x, r int, idx byte) {
		if x < w && r < ht {
			i := (h.row(r)*w + x) * 4
			copy(out[i:i+4], pal[idx][:])
		}
	}

	x, r, pos := 0, 0, 0
loop:
	for r < ht && pos < len(src) {
		if pos+2 > len(src) {
			return corrupt("truncated RLE command")
		}
		n, v := int(src[pos]), src[pos+1]
		pos += 2
		if n > 0 { // encoded run
			for i := range n {
				idx := v
				if rle4 {
					idx = nibble(v, i)
				}
				put(x, r, idx)
				x++
			}
			continue
		}
		switch v {
		case rleEOL:
			x, r = 0, r+1
		case rleEOF:
			break loop
		case rleDelta:
			if pos+2 > len(src) {
				return corrupt("truncated RLE delta")
			}
			x += int(src[pos])
			r += int(src[pos+1])
			pos += 2
		default: // absolute run of v literal pixels, padded to 16 bits
			count := int(v)
			size := count
			if rle4 {
				size = (count + 1) / 2
			}
			if pos+size > len(src) {
				return corrupt("truncated RLE literal run")
			}
			lit := src[pos : pos+size]
			for i := range count {
				var idx byte
				if rle4 {
					idx = nibble(lit[i/2], i)
				} else {
					idx = lit[i]
				}
				put(x, r, idx)
				x++
			}
			pos += size + size&1
		}
	}

	if allOpaque(out) {
		raw.Bands, raw.Data = 3, dropAlpha(out)
	} else {
		raw.Bands, raw.Data = 4, out
	}
	return nil
}

// nibble returns the i-th 4-bit index packed in b, high nibble first.
func nibble(b byte, i int) byte {
	if i%2 == 0 {
		return b >> 4
	}
	return b & 0x0F
}
