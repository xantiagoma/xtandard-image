//go:build cgo

package exec_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// jpegFrame reads the SOF marker of a JPEG and the sampling factors of its
// first (luma) component.
func jpegFrame(tb testing.TB, b []byte) (sof byte, lumaSampling byte) {
	tb.Helper()
	for i := 2; i+4 <= len(b); {
		m := b[i+1]
		n := int(binary.BigEndian.Uint16(b[i+2:]))
		if m >= 0xC0 && m <= 0xCF && m != 0xC4 && m != 0xC8 && m != 0xCC {
			return m, b[i+4+7] // precision, h, w, ncomp, then id + sampling
		}
		i += 2 + n
	}
	tb.Fatal("no SOF marker")
	return 0, 0
}

// pngHeader returns the IHDR colour type and interlace method and the
// number of PLTE entries.
func pngHeader(tb testing.TB, b []byte) (colourType, interlace byte, palette int) {
	tb.Helper()
	if !bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")) {
		tb.Fatal("not a png")
	}
	for i := 8; i+8 <= len(b); {
		n := int(binary.BigEndian.Uint32(b[i:]))
		switch string(b[i+4 : i+8]) {
		case "IHDR":
			colourType, interlace = b[i+8+9], b[i+8+12]
		case "PLTE":
			palette = n / 3
		}
		i += 12 + n
	}
	return colourType, interlace, palette
}

// webpChunk is the first chunk of a WebP after RIFF/WEBP (VP8 lossy, VP8L
// lossless, VP8X extended).
func webpChunk(b []byte) string {
	if len(b) < 16 {
		return ""
	}
	return string(b[12:16])
}

// av1Subsampling reads chroma_subsampling_x/y from the av1C box of an AVIF.
func av1Subsampling(tb testing.TB, b []byte) (x, y byte) {
	tb.Helper()
	i := bytes.Index(b, []byte("av1C"))
	if i < 0 || i+7 > len(b) {
		tb.Fatal("no av1C box")
	}
	f := b[i+6] // high_bitdepth, twelve_bit, monochrome, subsampling_x, subsampling_y, sample position
	return f >> 3 & 1, f >> 2 & 1
}

// TestEncoderOptionsOutput runs jpeg_options, png_options, webp_options and
// avif_options from the URL to the encoded bytes and inspects them.
func TestEncoderOptionsOutput(t *testing.T) {
	t.Parallel()
	const (
		sof0, sof2 = 0xC0, 0xC2
		h1v1, h2v2 = 0x11, 0x22
	)
	for _, tc := range []struct {
		options   string
		sof, samp byte
	}{
		{"q:80", sof2, h2v2}, // config default: progressive, 4:2:0 below q 90
		{"jpgo:0", sof0, h2v2},
		{"jpgo:1:1", sof2, h1v1},
		{"jpgo:0:1", sof0, h1v1},
		// The mozjpeg-only arguments are accepted; libjpeg-turbo ignores them.
		{"jpgo:1:0:1:1:1:3", sof2, h2v2},
	} {
		t.Run("jpeg/"+tc.options, func(t *testing.T) {
			t.Parallel()
			o := process(t, jpegPhoto, tc.options, transform.FormatJPEG)
			sof, samp := jpegFrame(t, o.bytes)
			if sof != tc.sof || samp != tc.samp {
				t.Errorf("SOF %#x sampling %#x, want %#x %#x", sof, samp, tc.sof, tc.samp)
			}
		})
	}
	for _, tc := range []struct {
		options    string
		colourType byte // 2 RGB, 3 palette, 6 RGBA
		interlace  byte
		maxPalette int
	}{
		{"pngo:0", 6, 0, 0},
		{"pngo:1", 6, 1, 0},
		{"pngo:0:1", 3, 0, 256},
		{"pngo:1:1:16", 3, 1, 16},
		{"pngo:0:1:4", 3, 0, 4},
	} {
		t.Run("png/"+tc.options, func(t *testing.T) {
			t.Parallel()
			o := process(t, pngAlpha, tc.options, transform.FormatPNG)
			ct, il, pal := pngHeader(t, o.bytes)
			if ct != tc.colourType || il != tc.interlace {
				t.Errorf("colour type %d interlace %d, want %d %d", ct, il, tc.colourType, tc.interlace)
			}
			if tc.colourType == 3 && (pal == 0 || pal > tc.maxPalette) {
				t.Errorf("%d palette entries, want 1..%d", pal, tc.maxPalette)
			}
		})
	}
	for _, tc := range []struct{ options, chunk string }{
		{"webpo:lossy", "VP8 "},
		{"webpo:lossless", "VP8L"},
		{"webpo:near_lossless", "VP8L"},
		{"webpo:lossy:1:photo", "VP8 "},
	} {
		t.Run("webp/"+tc.options, func(t *testing.T) {
			t.Parallel()
			o := process(t, jpegPhoto, tc.options, transform.FormatWebP)
			if got := webpChunk(o.bytes); got != tc.chunk {
				t.Errorf("first chunk %q, want %q", got, tc.chunk)
			}
		})
	}
	for _, tc := range []struct {
		options string
		sub     byte // 1: 4:2:0, 0: 4:4:4
	}{
		{"avifo:on", 1},
		{"avifo:off", 0},
		{"q:95/avifo:auto", 0}, // auto subsamples below quality 90 only
		{"q:60/avifo:auto", 1},
	} {
		t.Run("avif/"+tc.options, func(t *testing.T) {
			t.Parallel()
			o := process(t, jpegPhoto, "rs:fit:64:64/"+tc.options, transform.FormatAVIF)
			if x, y := av1Subsampling(t, o.bytes); x != tc.sub || y != tc.sub {
				t.Errorf("subsampling x=%d y=%d, want %d", x, y, tc.sub)
			}
		})
	}
}
