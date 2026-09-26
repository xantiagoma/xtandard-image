package codec_test

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"os"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
)

func readFixture(tb testing.TB, name string) []byte {
	tb.Helper()
	b, err := os.ReadFile(golden.Fixture(tb, name))
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

// pngChunk appends one PNG chunk with its CRC.
func pngChunk(dst []byte, typ string, data []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(data))) //nolint:gosec // test data is small
	start := len(dst)
	dst = append(dst, typ...)
	dst = append(dst, data...)
	return binary.BigEndian.AppendUint32(dst, crc32.ChecksumIEEE(dst[start:]))
}

// synthPNG is a well-formed PNG header claiming w×h 8-bit RGB pixels, with a
// single tiny IDAT: a decoder would need w×h×3 bytes, the header costs
// nothing.
func synthPNG(tb testing.TB, w, h uint32) []byte {
	tb.Helper()
	b := []byte("\x89PNG\r\n\x1a\n")
	ihdr := binary.BigEndian.AppendUint32(nil, w)
	ihdr = binary.BigEndian.AppendUint32(ihdr, h)
	ihdr = append(ihdr, 8, 2, 0, 0, 0) // 8-bit, RGB, deflate, adaptive, no interlace
	b = pngChunk(b, "IHDR", ihdr)
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	if _, err := zw.Write(make([]byte, 64)); err != nil {
		tb.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		tb.Fatal(err)
	}
	b = pngChunk(b, "IDAT", z.Bytes())
	return pngChunk(b, "IEND", nil)
}

// synthGIF is an animated GIF of frames w×h frames of 100 ms each, looping
// forever. Each frame's LZW data covers a single pixel, so the file costs a
// few dozen bytes per frame whatever the size its headers declare.
func synthGIF(w, h uint16, frames int) []byte {
	var dim []byte
	dim = binary.LittleEndian.AppendUint16(dim, w)
	dim = binary.LittleEndian.AppendUint16(dim, h)
	b := append([]byte("GIF89a"), dim...)
	// Global colour table of 2 entries (black, white), then the NETSCAPE2.0
	// loop extension: loop forever.
	b = append(b, "\x80\x00\x00\x00\x00\x00\xff\xff\xff\x21\xff\x0bNETSCAPE2.0\x03\x01\x00\x00\x00"...)
	for range frames {
		// Graphic control (10 cs delay), then a frame at 0,0.
		b = append(b, "\x21\xf9\x04\x00\x0a\x00\x00\x00\x2c\x00\x00\x00\x00"...)
		// Size; no local colour table, not interlaced; LZW with min code
		// size 2: clear, index 0, end of information.
		b = append(append(b, dim...), 0, 2, 2, 0x44, 0x01, 0)
	}
	return append(b, 0x3B)
}

// isobmff is an ftyp box with the given major and compatible brands.
func isobmff(major string, compat ...string) []byte {
	size := 16 + 4*len(compat)
	b := binary.BigEndian.AppendUint32(nil, uint32(size)) //nolint:gosec // tiny
	b = append(b, "ftyp"...)
	b = append(b, major...)
	b = append(b, 0, 0, 0, 0)
	for _, c := range compat {
		b = append(b, c...)
	}
	return b
}

// jpegWith is a JPEG header (SOI, the given APPn segments, then SOS).
func jpegWith(segs ...[]byte) []byte {
	b := []byte{0xFF, 0xD8}
	for _, s := range segs {
		b = append(b, s...)
	}
	return append(b, 0xFF, 0xDA, 0, 2)
}

// app is an APPn segment (marker 0xE0+n) with payload p.
func app(n byte, p string) []byte {
	b := []byte{0xFF, 0xE0 + n}
	b = binary.BigEndian.AppendUint16(b, uint16(len(p)+2)) //nolint:gosec // tiny
	return append(b, p...)
}
