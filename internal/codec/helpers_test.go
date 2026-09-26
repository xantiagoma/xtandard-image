package codec_test

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
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

// synthEXR builds the header of a scanline OpenEXR file declaring a w×h
// data window, the given channels (half) and version flags, followed by
// no pixel data: small on disk whatever size it declares.
func synthEXR(w, h int, flags uint32, channels ...string) []byte {
	le := binary.LittleEndian
	b := []byte("v/1\x01")
	b = le.AppendUint32(b, 2|flags)
	attr := func(name, typ string, v []byte) {
		b = append(b, name+"\x00"+typ+"\x00"...)
		b = le.AppendUint32(b, uint32(len(v))) //nolint:gosec // tiny
		b = append(b, v...)
	}
	var ch []byte
	for _, c := range channels {
		ch = append(ch, c+"\x00"...)
		ch = le.AppendUint32(ch, 1)
		ch = append(ch, 0, 0, 0, 0)
		ch = le.AppendUint32(ch, 1)
		ch = le.AppendUint32(ch, 1)
	}
	ch = append(ch, 0)
	var win []byte
	for _, v := range []int{0, 0, w - 1, h - 1} {
		win = le.AppendUint32(win, uint32(int32(v))) //nolint:gosec // test values
	}
	attr("channels", "chlist", ch)
	attr("compression", "compression", []byte{0})
	attr("dataWindow", "box2i", win)
	attr("displayWindow", "box2i", win)
	attr("lineOrder", "lineOrder", []byte{0})
	return append(b, 0)
}

// synthPDF builds a PDF with n blank 100×100 pt pages in one page tree:
// small on disk however many pages it declares.
func synthPDF(n int) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	var offsets []int
	obj := func(body string) {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", len(offsets), body)
	}
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	var kids bytes.Buffer
	for i := range n {
		fmt.Fprintf(&kids, "%d 0 R ", i+3)
	}
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d /MediaBox [0 0 100 100] >>", kids.String(), n))
	for range n {
		obj("<< /Type /Page /Parent 2 0 R >>")
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return b.Bytes()
}
