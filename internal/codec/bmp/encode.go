// Portions derived from imgproxy's vips/bmpsave.c,
// https://github.com/imgproxy/imgproxy, Copyright (c) 2017 Sergey
// Alexandrovich, licensed under the Apache License, Version 2.0 (see /NOTICE).
// Modifications: rewritten in pure Go over codec.Raw; RGB is written with a
// BITMAPINFOHEADER and RGBA with a BITMAPV4HEADER (instead of V5) using
// bottom-up rows for the widest reader compatibility; resolution is kept.

package bmp

import (
	"encoding/binary"
	"math"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// lcsSRGB is the LOGCOLORSPACE tag 'sRGB' of a V4 header.
const lcsSRGB = 0x73524742

// Encode writes r as an uncompressed BMP: 24-bit BI_RGB with a
// BITMAPINFOHEADER for 3 bands, 32-bit BI_BITFIELDS (BGRA masks) with a
// BITMAPV4HEADER for 4 bands so that readers honour the alpha channel.
func Encode(r *codec.Raw) ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	w, h, bands := r.Width, r.Height, r.Bands
	hdrLen := infoHeaderLen
	compression := uint32(biRGB)
	if bands == 4 {
		hdrLen, compression = v4HeaderLen, biBitfields
	}
	stride := (int64(w)*int64(bands) + 3) &^ 3
	imageSize := stride * int64(h)
	pixOff := int64(fileHeaderLen + hdrLen)
	if pixOff+imageSize > math.MaxUint32 || w > math.MaxInt32 || h > math.MaxInt32 {
		return nil, errs.Newf(errs.KindLimitExceeded, "bmp: %dx%d is too large for a BMP file", w, h)
	}
	buf := make([]byte, pixOff+imageSize)
	le := binary.LittleEndian

	buf[0], buf[1] = 'B', 'M'
	le.PutUint32(buf[2:], u32(int64(len(buf))))
	le.PutUint32(buf[10:], u32(pixOff))

	d := buf[fileHeaderLen:]
	le.PutUint32(d[0:], u32(int64(hdrLen)))
	le.PutUint32(d[4:], u32(int64(w)))
	le.PutUint32(d[8:], u32(int64(h))) // positive: bottom-up
	le.PutUint16(d[12:], 1)
	le.PutUint16(d[14:], u16(bands*8))
	le.PutUint32(d[16:], compression)
	le.PutUint32(d[20:], u32(imageSize))
	le.PutUint32(d[24:], perMMToPPM(r.XRes))
	le.PutUint32(d[28:], perMMToPPM(r.YRes))
	if bands == 4 {
		le.PutUint32(d[40:], 0x00FF0000)
		le.PutUint32(d[44:], 0x0000FF00)
		le.PutUint32(d[48:], 0x000000FF)
		le.PutUint32(d[52:], 0xFF000000)
		le.PutUint32(d[56:], lcsSRGB)
	}

	rowLen := w * bands
	for y := range h {
		src := r.Data[y*rowLen : (y+1)*rowLen]
		dst := buf[pixOff+int64(h-1-y)*stride:]
		for i := 0; i < rowLen; i += bands {
			dst[i], dst[i+1], dst[i+2] = src[i+2], src[i+1], src[i]
			if bands == 4 {
				dst[i+3] = src[i+3]
			}
		}
	}
	return buf, nil
}

// perMMToPPM converts pixels per millimetre to whole pixels per metre.
func perMMToPPM(v float64) uint32 {
	if !(v > 0) || v*1000 > math.MaxInt32 {
		return 0
	}
	return uint32(math.Round(v * 1000))
}
