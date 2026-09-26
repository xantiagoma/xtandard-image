// Portions derived from imgproxy's vips/bmp.h and vips/bmpload.c,
// https://github.com/imgproxy/imgproxy, Copyright (c) 2017 Sergey
// Alexandrovich, licensed under the Apache License, Version 2.0 (see /NOTICE).
// Modifications: rewritten in pure Go over an in-memory buffer; adds
// BITMAPCOREHEADER (OS/2 1.x) and OS/2 2.x headers, 52/56-byte headers,
// arbitrary contiguous bitfield masks, BI_ALPHABITFIELDS, embedded
// BI_PNG/BI_JPEG streams, ICO DIBs with AND masks, bounds checks on every
// read and a pixel limit enforced before allocation.

// Package bmp decodes and encodes Windows/OS/2 bitmaps in pure Go. Decode
// returns 8-bit RGB/RGBA pixels as a codec.Raw, or the embedded PNG/JPEG
// stream of a BI_PNG/BI_JPEG bitmap for libvips to load itself.
//
// Supported: 1/2/4/8-bit paletted, 16-bit (5-5-5, 5-6-5 or any contiguous
// bitfields), 24-bit, 32-bit (BI_RGB, BI_BITFIELDS, BI_ALPHABITFIELDS), RLE4,
// RLE8, top-down and bottom-up rows, BITMAPCOREHEADER, OS/2 2.x, BITMAPINFO,
// V2/V3/V4/V5 headers. OS/2 Huffman 1D and RLE24 are rejected.
package bmp

import (
	"encoding/binary"
	"math"
	"math/bits"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

const (
	fileHeaderLen  = 14  // BITMAPFILEHEADER
	coreHeaderLen  = 12  // BITMAPCOREHEADER (OS/2 1.x, Windows 2.x)
	infoHeaderLen  = 40  // BITMAPINFOHEADER
	v2HeaderLen    = 52  // BITMAPV2INFOHEADER: RGB masks in the header
	v3HeaderLen    = 56  // BITMAPV3INFOHEADER: RGBA masks in the header
	os2v2HeaderLen = 64  // OS/2 2.x BITMAPINFOHEADER2
	v4HeaderLen    = 108 // BITMAPV4HEADER
	v5HeaderLen    = 124 // BITMAPV5HEADER

	biRGB            = 0
	biRLE8           = 1
	biRLE4           = 2
	biBitfields      = 3
	biJPEG           = 4
	biPNG            = 5
	biAlphaBitfields = 6
)

// Options bound the decoder.
type Options struct {
	// MaxPixels rejects images whose Width*Height exceeds it, with
	// errs.KindLimitExceeded, before any pixel buffer is allocated. Zero
	// means codec.DefaultMaxPixels.
	MaxPixels int64
}

func (o Options) maxPixels() int64 {
	if o.MaxPixels > 0 {
		return o.MaxPixels
	}
	return codec.DefaultMaxPixels
}

// Config is the header-only view of a bitmap, for probing.
type Config struct {
	Width  int
	Height int
	// BitCount is the stored bits per pixel (0 for an embedded stream).
	BitCount int
	// Alpha reports whether the pixel format carries alpha. Decode still
	// returns 3 bands when every alpha sample turns out to be zero.
	Alpha bool
	// Embedded reports a BI_PNG/BI_JPEG bitmap whose pixels libvips decodes.
	Embedded bool
}

// Sniff reports whether head starts like a BMP file: the "BM" signature and,
// when enough bytes are present, a known info-header length.
func Sniff(head []byte) bool {
	if len(head) < 2 || head[0] != 'B' || head[1] != 'M' {
		return false
	}
	if len(head) < fileHeaderLen+4 {
		return true
	}
	return validHeaderLen(le32(head[fileHeaderLen:]))
}

// DecodeConfig parses the headers of a BMP file without decoding pixels.
func DecodeConfig(data []byte) (Config, error) {
	h, err := parseFile(data)
	if err != nil {
		return Config{}, err
	}
	return h.config(), nil
}

// IconConfig parses the DIB of an ICO/CUR entry (no file header, doubled
// height) without decoding pixels.
func IconConfig(dib []byte) (Config, error) {
	h, err := parseDIB(dib, 0, -1, true)
	if err != nil {
		return Config{}, err
	}
	c := h.config()
	c.Alpha = true // the AND mask may add transparency
	return c, nil
}

// header is the parsed, validated description of a DIB.
type header struct {
	width, height int
	topDown       bool
	bpp           int
	compression   uint32
	imageSize     uint32
	xppm, yppm    int64
	masks         [4]uint32 // R, G, B, A for 16/32 bpp; A == 0 means no alpha
	palette       *[256][4]byte
	pixOff        int
	embedded      bool
}

func (h *header) config() Config {
	return Config{
		Width:    h.width,
		Height:   h.height,
		BitCount: h.bpp,
		Alpha:    h.masks[3] != 0,
		Embedded: h.embedded,
	}
}

func (h *header) rle() bool { return h.compression == biRLE4 || h.compression == biRLE8 }

func validHeaderLen(n uint32) bool {
	return n == coreHeaderLen || (n >= 16 && n <= os2v2HeaderLen && n%4 == 0) ||
		n == v4HeaderLen || n == v5HeaderLen
}

// windowsHeaderLen reports header lengths that only Windows writes; for the
// others (OS/2 2.x) compression values 3 and 4 mean Huffman 1D and RLE24.
func windowsHeaderLen(n uint32) bool {
	switch n {
	case infoHeaderLen, v2HeaderLen, v3HeaderLen, v4HeaderLen, v5HeaderLen:
		return true
	default:
		return false
	}
}

func corrupt(msg string) error { return errs.New(errs.KindCorrupt, "bmp: "+msg) }

func corruptf(format string, args ...any) error {
	return errs.Newf(errs.KindCorrupt, "bmp: "+format, args...)
}

func le16(b []byte) uint16 { return binary.LittleEndian.Uint16(b) }
func le32(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }

// les32 reads a little-endian two's-complement int32.
func les32(b []byte) int64 {
	v := int64(le32(b))
	if v >= 1<<31 {
		v -= 1 << 32
	}
	return v
}

// u32, u16 and u8 narrow values the caller has already bounded; out-of-range
// input (a bug) yields 0 rather than a silently wrapped value.
func u32(v int64) uint32 {
	if v < 0 || v > math.MaxUint32 {
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

func u8(v uint32) byte {
	if v > math.MaxUint8 {
		return 0
	}
	return byte(v)
}

// parseFile validates the BITMAPFILEHEADER and parses the DIB after it.
func parseFile(data []byte) (*header, error) {
	if len(data) < fileHeaderLen || data[0] != 'B' || data[1] != 'M' {
		return nil, corrupt("missing BM signature")
	}
	return parseDIB(data, fileHeaderLen, int64(le32(data[10:])), false)
}

// parseDIB parses the info header starting at b[dib:]. pixOff is the pixel
// data offset from the file header, or -1 when the pixels follow the palette
// directly (ICO entries). icon halves the stored height (XOR + AND bitmaps).
func parseDIB(b []byte, dib int, pixOff int64, icon bool) (*header, error) {
	if len(b) < dib+4 {
		return nil, corrupt("truncated info header")
	}
	hlen := le32(b[dib:])
	if !validHeaderLen(hlen) {
		return nil, corruptf("unsupported info header length %d", hlen)
	}
	end := dib + int(hlen)
	if len(b) < end {
		return nil, corrupt("truncated info header")
	}

	h := &header{}
	var (
		w, ht     int64
		planes    uint16
		numColors uint32
		core      = hlen == coreHeaderLen
	)
	if core {
		w = int64(le16(b[dib+4:]))
		ht = int64(le16(b[dib+6:]))
		planes = le16(b[dib+8:])
		h.bpp = int(le16(b[dib+10:]))
	} else {
		// Short OS/2 2.x headers omit trailing fields; they default to zero.
		var f [v5HeaderLen]byte
		copy(f[:], b[dib:end])
		w = les32(f[4:])
		ht = les32(f[8:])
		planes = le16(f[12:])
		h.bpp = int(le16(f[14:]))
		h.compression = le32(f[16:])
		h.imageSize = le32(f[20:])
		h.xppm = les32(f[24:])
		h.yppm = les32(f[28:])
		numColors = le32(f[32:])
		if windowsHeaderLen(hlen) && hlen >= v2HeaderLen {
			h.masks[0], h.masks[1], h.masks[2] = le32(f[40:]), le32(f[44:]), le32(f[48:])
		}
		if windowsHeaderLen(hlen) && hlen >= v3HeaderLen {
			h.masks[3] = le32(f[52:])
		}
	}
	if ht < 0 {
		ht, h.topDown = -ht, true
	}
	if icon {
		ht /= 2
	}
	if w <= 0 || ht <= 0 {
		return nil, corruptf("invalid dimensions %dx%d", w, ht)
	}
	h.width, h.height = int(w), int(ht)
	if planes != 1 {
		return nil, corruptf("%d planes", planes)
	}

	// Colour masks after a 40-byte header (BI_BITFIELDS, BI_ALPHABITFIELDS).
	maskBytes := 0
	switch h.compression {
	case biRGB:
		if err := h.setDefaultMasks(hlen, icon); err != nil {
			return nil, err
		}
	case biRLE8, biRLE4:
		if (h.compression == biRLE8 && h.bpp != 8) || (h.compression == biRLE4 && h.bpp != 4) {
			return nil, corruptf("compression %d with %d bpp", h.compression, h.bpp)
		}
	case biBitfields, biAlphaBitfields:
		if !windowsHeaderLen(hlen) {
			return nil, corrupt("OS/2 Huffman 1D compression is not supported")
		}
		if h.bpp != 16 && h.bpp != 32 {
			return nil, corruptf("bitfields with %d bpp", h.bpp)
		}
		if hlen == infoHeaderLen {
			n := 3
			if h.compression == biAlphaBitfields {
				n = 4
			}
			maskBytes = 4 * n
			if len(b) < end+maskBytes {
				return nil, corrupt("truncated colour masks")
			}
			for i := range n {
				h.masks[i] = le32(b[end+4*i:])
			}
		}
		if h.compression == biBitfields && hlen < v3HeaderLen {
			h.masks[3] = 0
		}
		if err := h.checkMasks(); err != nil {
			return nil, err
		}
	case biJPEG, biPNG:
		if !windowsHeaderLen(hlen) {
			return nil, corrupt("OS/2 RLE24 compression is not supported")
		}
		h.embedded = true
	default:
		return nil, corruptf("unsupported compression %d", h.compression)
	}

	// Palette, and the pixel offset of ICO entries.
	start := end + maskBytes
	entrySize := 4
	if core {
		entrySize = 3
	}
	if h.bpp <= 8 && !h.embedded {
		n := int64(numColors)
		if n == 0 || n > 1<<h.bpp {
			n = 1 << h.bpp
		}
		limit := int64(len(b))
		if pixOff >= 0 {
			limit = min(limit, pixOff)
		}
		avail := max(0, (limit-int64(start))/int64(entrySize))
		if pixOff < 0 && avail < n {
			return nil, corrupt("truncated palette")
		}
		n = min(n, avail)
		if n == 0 {
			return nil, corrupt("missing palette")
		}
		h.palette = readPalette(b[start:], int(n), entrySize)
		if pixOff < 0 {
			pixOff = int64(start) + n*int64(entrySize)
		}
	} else if pixOff < 0 {
		// Truecolour DIBs may carry an optional colour table; skip it.
		skip := int64(numColors) * 4
		if int64(start)+skip > int64(len(b)) {
			return nil, corrupt("truncated colour table")
		}
		pixOff = int64(start) + skip
	}
	if pixOff < int64(end) || pixOff > int64(len(b)) {
		return nil, corruptf("pixel data offset %d out of range", pixOff)
	}
	h.pixOff = int(pixOff)
	return h, nil
}

// setDefaultMasks sets the implicit masks of BI_RGB 16/32-bit pixels. A
// 32-bit BI_RGB bitmap has alpha in the fourth byte only when a V3+ header
// says so with a non-zero alpha mask (as imgproxy) or when it is an icon.
func (h *header) setDefaultMasks(hlen uint32, icon bool) error {
	switch h.bpp {
	case 1, 2, 4, 8, 24:
		h.masks = [4]uint32{}
	case 16:
		h.masks = [4]uint32{0x7C00, 0x03E0, 0x001F, 0}
	case 32:
		alpha := uint32(0)
		if icon || (windowsHeaderLen(hlen) && hlen >= v3HeaderLen && h.masks[3] != 0) {
			alpha = 0xFF000000
		}
		h.masks = [4]uint32{0x00FF0000, 0x0000FF00, 0x000000FF, alpha}
	default:
		return corruptf("unsupported bit depth %d", h.bpp)
	}
	return nil
}

// checkMasks requires each mask to be a contiguous run of bits that fits the
// pixel size.
func (h *header) checkMasks() error {
	for _, m := range h.masks {
		if m == 0 {
			continue
		}
		if h.bpp == 16 && m > 0xFFFF {
			return corruptf("colour mask %#x exceeds 16 bits", m)
		}
		v := m >> bits.TrailingZeros32(m)
		if v&(v+1) != 0 {
			return corruptf("non-contiguous colour mask %#x", m)
		}
	}
	return nil
}

func readPalette(b []byte, n, entrySize int) *[256][4]byte {
	var p [256][4]byte
	for i := range p {
		p[i][3] = 0xFF // indices past the palette decode as opaque black
	}
	for i := range n {
		e := b[i*entrySize:]
		p[i] = [4]byte{e[2], e[1], e[0], 0xFF}
	}
	return &p
}
