// Portions derived from imgproxy's vips/icosave.c,
// https://github.com/imgproxy/imgproxy, Copyright (c) 2017 Sergey
// Alexandrovich, licensed under the Apache License, Version 2.0 (see /NOTICE).
// Modifications: rewritten in pure Go over raster.Raw; several entries per
// file; entries up to 48 px are stored as DIBs (imgproxy always writes PNG).

package ico

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"math"

	"github.com/xantiagoma/xtandard-image/internal/codec/raster"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// MaxDIBSize is the largest side stored as a DIB entry; larger entries are
// PNG streams. Windows XP and older readers only understand DIB entries, and
// the classic favicon sizes (16, 24, 32, 48) are exactly those; above that
// PNG is far smaller and supported everywhere since Windows Vista.
const MaxDIBSize = 48

// Encode writes the frames as one icon file, one entry per frame in order.
// Each side must be at most 256 pixels. Frames larger than MaxDIBSize become
// PNG entries (RGB or RGBA as the frame); the others become DIB entries,
// 24-bit with an empty AND mask for 3 bands, 32-bit BGRA with an AND mask
// derived from alpha == 0 for 4 bands.
func Encode(frames ...*raster.Raw) ([]byte, error) {
	if len(frames) == 0 || len(frames) > 0xFFFF {
		return nil, errs.Newf(errs.KindInternal, "ico: %d frames, want 1..65535", len(frames))
	}
	payloads := make([][]byte, len(frames))
	for i, f := range frames {
		if err := f.Validate(); err != nil {
			return nil, err
		}
		if f.Width > maxDimension || f.Height > maxDimension {
			return nil, errs.Newf(errs.KindLimitExceeded,
				"ico: frame %dx%d exceeds %d pixels per side", f.Width, f.Height, maxDimension)
		}
		var err error
		if isPNG(f) {
			payloads[i], err = encodePNG(f)
		} else {
			payloads[i] = encodeDIB(f)
		}
		if err != nil {
			return nil, err
		}
	}

	size := dirHeaderLen + dirEntryLen*len(frames)
	for _, p := range payloads {
		size += len(p)
	}
	if int64(size) > math.MaxUint32 {
		return nil, errs.Newf(errs.KindLimitExceeded, "ico: %d bytes exceed the 4 GiB format limit", size)
	}
	buf := make([]byte, dirHeaderLen+dirEntryLen*len(frames), size)
	le := binary.LittleEndian
	le.PutUint16(buf[2:], typeIcon)
	le.PutUint16(buf[4:], u16(len(frames)))
	for i, f := range frames {
		d := buf[dirHeaderLen+dirEntryLen*i:]
		d[0] = u8(f.Width % maxDimension) // 0 means 256
		d[1] = u8(f.Height % maxDimension)
		le.PutUint16(d[4:], 1) // planes
		bitCount := uint16(32)
		if !isPNG(f) && f.Bands == 3 {
			bitCount = 24
		}
		le.PutUint16(d[6:], bitCount)
		le.PutUint32(d[8:], u32(len(payloads[i])))
		le.PutUint32(d[12:], u32(len(buf)))
		buf = append(buf, payloads[i]...)
	}
	return buf, nil
}

// u32, u16 and u8 narrow values the caller has already bounded; out-of-range
// input (a bug) yields 0 rather than a silently wrapped value.
func u32(v int) uint32 {
	if v < 0 || int64(v) > math.MaxUint32 {
		return 0
	}
	return uint32(v)
}

func u16(v int) uint16 {
	if v < 0 || v > math.MaxUint16 {
		return 0
	}
	return uint16(v)
}

func u8(v int) byte {
	if v < 0 || v > math.MaxUint8 {
		return 0
	}
	return byte(v)
}

func isPNG(f *raster.Raw) bool { return f.Width > MaxDIBSize || f.Height > MaxDIBSize }

func encodePNG(f *raster.Raw) ([]byte, error) {
	var img image.Image
	if f.Bands == 4 {
		m := image.NewNRGBA(image.Rect(0, 0, f.Width, f.Height))
		copy(m.Pix, f.Data)
		img = m
	} else {
		m := image.NewRGBA(image.Rect(0, 0, f.Width, f.Height))
		for i, j := 0, 0; i < len(f.Data); i, j = i+3, j+4 {
			m.Pix[j], m.Pix[j+1], m.Pix[j+2], m.Pix[j+3] = f.Data[i], f.Data[i+1], f.Data[i+2], 0xFF
		}
		img = m
	}
	var b bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := enc.Encode(&b, img); err != nil {
		return nil, errs.Wrapf(errs.KindInternal, err, "ico: encode png entry")
	}
	return b.Bytes(), nil
}

// encodeDIB writes a BITMAPINFOHEADER DIB with doubled height, bottom-up
// XOR pixels and the 1-bit AND mask.
func encodeDIB(f *raster.Raw) []byte {
	w, h, bands := f.Width, f.Height, f.Bands
	stride := (w*bands + 3) &^ 3
	maskStride := (w + 31) / 32 * 4
	const hdrLen = 40
	buf := make([]byte, hdrLen+stride*h+maskStride*h)
	le := binary.LittleEndian
	le.PutUint32(buf[0:], hdrLen)
	le.PutUint32(buf[4:], u32(w))
	le.PutUint32(buf[8:], u32(2*h))
	le.PutUint16(buf[12:], 1)
	le.PutUint16(buf[14:], u16(8*bands))
	le.PutUint32(buf[20:], u32(stride*h+maskStride*h))

	rowLen := w * bands
	mask := buf[hdrLen+stride*h:]
	for y := range h {
		src := f.Data[y*rowLen : (y+1)*rowLen]
		dst := buf[hdrLen+(h-1-y)*stride:]
		m := mask[(h-1-y)*maskStride:]
		for x, i := 0, 0; x < w; x, i = x+1, i+bands {
			dst[i], dst[i+1], dst[i+2] = src[i+2], src[i+1], src[i]
			if bands == 4 {
				dst[i+3] = src[i+3]
				if src[i+3] == 0 {
					m[x/8] |= 0x80 >> (x % 8)
				}
			}
		}
	}
	return buf
}
