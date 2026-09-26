package bmp

import (
	"bytes"
	"image/png"
	"math"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	xbmp "golang.org/x/image/bmp"

	"github.com/xantiagoma/xtandard-image/internal/codec/raster"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// TestDecodeFixtures decodes the ImageMagick corpus (testdata/gen.sh) and
// compares every pixel with ImageMagick's own decode (<name>.ref.png) and,
// where it supports the variant, with golang.org/x/image/bmp.
func TestDecodeFixtures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		bands  int
		tol    int  // per-sample tolerance against the reference
		xImage bool // golang.org/x/image/bmp decodes this variant
		// ref maps a reference sample of channel c (0..3 = R, G, B, A) to the
		// expected one, where ImageMagick widens n-bit samples by a plain
		// shift (v << (8-n)) instead of scaling to the full 0..255 range.
		ref func(c int, v byte) byte
	}{
		{name: "p1", bands: 3, xImage: true},
		{name: "p4", bands: 3, xImage: true},
		{name: "p8", bands: 3, xImage: true},
		{name: "rle8", bands: 3},
		{name: "rgb24", bands: 3, xImage: true},
		{name: "os2", bands: 3},
		{name: "os2p4", bands: 3},
		{name: "rgb565", bands: 3, tol: 1}, // ImageMagick rounds 5/6-bit scaling differently
		{name: "rgb555", bands: 3, tol: 1},
		{name: "argb1555", bands: 4, tol: 1, ref: func(c int, v byte) byte {
			if c == 3 {
				return (v >> 7) * 255 // 1-bit alpha: ImageMagick yields 128 for "opaque"
			}
			return v
		}},
		{name: "argb4444", bands: 4, ref: func(_ int, v byte) byte { return (v >> 4) * 17 }},
		{name: "v5a", bands: 4, xImage: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data := readFixture(t, tc.name+".bmp")
			if !Sniff(data) {
				t.Fatal("Sniff = false")
			}
			d, err := Decode(data, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if d.Raw == nil || d.Embedded != nil {
				t.Fatalf("got %+v, want Raw only", d)
			}
			if err := d.Raw.Validate(); err != nil {
				t.Fatal(err)
			}
			if d.Raw.Bands != tc.bands {
				t.Errorf("bands = %d, want %d", d.Raw.Bands, tc.bands)
			}
			got := rgba(d.Raw)

			w, h, want := imageRGBA(decodePNG(t, readFixture(t, tc.name+".ref.png")))
			if tc.ref != nil {
				for i, v := range want {
					want[i] = tc.ref(i%4, v)
				}
			}
			if d.Raw.Width != w || d.Raw.Height != h {
				t.Fatalf("size %dx%d, want %dx%d", d.Raw.Width, d.Raw.Height, w, h)
			}
			if s := diffPixels(w, got, want, tc.tol); s != "" {
				t.Errorf("vs ImageMagick: %s", s)
			}
			if tc.xImage {
				m, err := xbmp.Decode(bytes.NewReader(data))
				if err != nil {
					t.Fatalf("x/image/bmp: %v", err)
				}
				_, _, want := imageRGBA(m)
				if s := diffPixels(w, got, want, 0); s != "" {
					t.Errorf("vs x/image/bmp: %s", s)
				}
			}

			c, err := DecodeConfig(data)
			if err != nil {
				t.Fatal(err)
			}
			if c.Width != w || c.Height != h || c.Embedded {
				t.Errorf("DecodeConfig = %+v", c)
			}
		})
	}
}

// pal4 is the palette of the hand-built paletted bitmaps.
var pal4 = [][3]byte{{0, 0, 0}, {255, 0, 0}, {0, 255, 0}, {0, 0, 255}}

// px builds straight RGBA samples from palette indices; -1 is transparent.
func px(pal [][3]byte, idx ...int) []byte {
	out := make([]byte, 0, 4*len(idx))
	for _, i := range idx {
		if i < 0 {
			out = append(out, 0, 0, 0, 0)
			continue
		}
		c := pal[i]
		out = append(out, c[0], c[1], c[2], 0xFF)
	}
	return out
}

// TestDecodeCrafted covers the variants ImageMagick does not write, with
// hand-asserted pixels (RGBA, top row first).
func TestDecodeCrafted(t *testing.T) {
	t.Parallel()
	pal16 := make([][3]byte, 16)
	copy(pal16, pal4)
	pal16[15] = [3]byte{255, 255, 255}

	tests := []struct {
		name  string
		file  []byte
		w     int
		bands int
		want  []byte
	}{
		{
			// Bottom-up 4x3 RLE4: row 2 (stored first) is a run alternating
			// indices 1,2; row 1 is an absolute run 3,0,15 then a run of 1;
			// row 0 skips two pixels with a delta, then a run of 3, then EOF.
			name: "rle4 run absolute delta",
			file: (&dib{hdrLen: 40, w: 4, h: 3, bpp: 4, compression: biRLE4, palette: pal16, pixels: []byte{
				4, 0x12, 0, 0, // run of 4: 1 2 1 2, EOL
				0, 3, 0x30, 0xF0, // absolute 3 pixels: 3 0 15 (2 bytes, even: no pad)
				1, 0x10, 0, 0, // run of 1: 1, EOL
				0, 2, 2, 0, // delta (+2, +0)
				2, 0x33, 0, 1, // run of 2: 3 3, EOF
			}}).file(),
			w: 4, bands: 4,
			want: bytes.Join([][]byte{
				px(pal16, -1, -1, 3, 3),
				px(pal16, 3, 0, 15, 1),
				px(pal16, 1, 2, 1, 2),
			}, nil),
		},
		{
			// Bottom-up 3x3 RLE8 whose delta and early EOF leave holes: they
			// decode as transparent.
			name: "rle8 holes",
			file: (&dib{hdrLen: 40, w: 3, h: 3, bpp: 8, compression: biRLE8, palette: pal4, pixels: []byte{
				0, 3, 1, 2, 3, 0, // absolute 3 bytes + pad: 1 2 3
				0, 0, // EOL
				0, 2, 1, 1, // delta (+1, +1): row 2, x 1
				1, 2, 0, 1, // run of 1: 2, EOF
			}}).file(),
			w: 3, bands: 4,
			want: bytes.Join([][]byte{
				px(pal4, -1, 2, -1),
				px(pal4, -1, -1, -1),
				px(pal4, 1, 2, 3),
			}, nil),
		},
		{
			// RLE8 that ends without EOF right after a command.
			name: "rle8 no eof",
			file: (&dib{hdrLen: 40, w: 2, h: 1, bpp: 8, compression: biRLE8, palette: pal4, pixels: []byte{2, 3}}).file(),
			w:    2, bands: 3,
			want: px(pal4, 3, 3),
		},
		{
			name: "2-bit top-down",
			file: (&dib{hdrLen: 40, w: 5, h: -2, bpp: 2, palette: pal4, pixels: []byte{
				0b00_01_10_11, 0b01_000000, 0, 0,
				0b11_10_01_00, 0b10_000000, 0, 0,
			}}).file(),
			w: 5, bands: 3,
			want: append(px(pal4, 0, 1, 2, 3, 1), px(pal4, 3, 2, 1, 0, 2)...),
		},
		{
			// Palette shorter than the indices used: missing entries are black.
			name: "8-bit short palette",
			file: (&dib{hdrLen: 40, w: 2, h: 1, bpp: 8, numColors: 2, palette: pal4[:2], pixels: []byte{1, 200, 0, 0}}).file(),
			w:    2, bands: 3,
			want: px(pal4, 1, 0),
		},
		{
			// 16-bit BI_RGB is 5-5-5; 5-bit values scale to round(v*255/31).
			name: "16-bit BI_RGB 555",
			file: (&dib{hdrLen: 40, w: 2, h: 1, bpp: 16, pixels: []byte{
				0x00, 0x7C, // red 31
				0x10, 0x42, // r=16 g=16 b=16 → 132
			}}).file(),
			w: 2, bands: 3,
			want: []byte{255, 0, 0, 255, 132, 132, 132, 255},
		},
		{
			// 32-bit BI_RGB with a 40-byte header has no alpha: the fourth byte
			// is ignored (as golang.org/x/image/bmp and browsers do).
			name: "32-bit BI_RGB no alpha",
			file: (&dib{hdrLen: 40, w: 1, h: 1, bpp: 32, pixels: []byte{1, 2, 3, 4}}).file(),
			w:    1, bands: 3,
			want: []byte{3, 2, 1, 255},
		},
		{
			// Non-standard 32-bit masks: R in the top byte, 10-bit green.
			name: "32-bit bitfields generic",
			file: (&dib{
				hdrLen: 40, w: 1, h: 1, bpp: 32, compression: biBitfields,
				masks:  []uint32{0xFF000000, 0x00FFC000, 0x00003F00},
				pixels: []byte{0x00, 0x3F, 0xFF, 0x80},
			}).file(),
			w: 1, bands: 3,
			want: []byte{0x80, 0xFF, 0xFF, 255},
		},
		{
			name: "BI_ALPHABITFIELDS",
			file: (&dib{
				hdrLen: 40, w: 1, h: 1, bpp: 16, compression: biAlphaBitfields,
				masks:  []uint32{0x0F00, 0x00F0, 0x000F, 0xF000},
				pixels: []byte{0x0F, 0x80, 0, 0},
			}).file(),
			w: 1, bands: 4,
			want: []byte{0, 0, 255, 0x88},
		},
		{
			// V3 (56-byte) header with an all-zero alpha channel: opaque.
			name: "V3 all-zero alpha is opaque",
			file: (&dib{
				hdrLen: 56, w: 1, h: 1, bpp: 32, compression: biBitfields,
				masks:  []uint32{0x00FF0000, 0x0000FF00, 0x000000FF, 0xFF000000},
				pixels: []byte{1, 2, 3, 0},
			}).file(),
			w: 1, bands: 3,
			want: []byte{3, 2, 1, 255},
		},
		{
			// V4 header with BI_RGB and a non-zero alpha mask: alpha is used.
			name: "V4 BI_RGB with alpha mask",
			file: (&dib{
				hdrLen: 108, w: 1, h: 1, bpp: 32,
				masks:  []uint32{0x00FF0000, 0x0000FF00, 0x000000FF, 0xFF000000},
				pixels: []byte{1, 2, 3, 9},
			}).file(),
			w: 1, bands: 4,
			want: []byte{3, 2, 1, 9},
		},
		{
			name: "OS/2 2.x 64-byte header",
			file: (&dib{hdrLen: 64, w: 1, h: 1, bpp: 24, pixels: []byte{1, 2, 3, 0}}).file(),
			w:    1, bands: 3,
			want: []byte{3, 2, 1, 255},
		},
		{
			name: "OS/2 2.x 16-byte header",
			file: (&dib{hdrLen: 16, w: 1, h: 1, bpp: 24, pixels: []byte{1, 2, 3, 0}}).file(),
			w:    1, bands: 3,
			want: []byte{3, 2, 1, 255},
		},
		{
			name: "OS/2 1.x 1-bit",
			file: (&dib{hdrLen: 12, w: 3, h: 1, bpp: 1, palette: pal4[:2], pixels: []byte{0b101_00000, 0, 0, 0}}).file(),
			w:    3, bands: 3,
			want: px(pal4, 1, 0, 1),
		},
		{
			// A gap between palette and pixels (bfOffBits) is honoured.
			name: "offset past headers",
			file: (&dib{hdrLen: 40, w: 1, h: 1, bpp: 24, offset: 14 + 40 + 4, pixels: []byte{9, 9, 9, 9, 1, 2, 3, 0}}).file(),
			w:    1, bands: 3,
			want: []byte{3, 2, 1, 255},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, err := Decode(tc.file, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if err := d.Raw.Validate(); err != nil {
				t.Fatal(err)
			}
			if d.Raw.Bands != tc.bands {
				t.Errorf("bands = %d, want %d", d.Raw.Bands, tc.bands)
			}
			if s := diffPixels(tc.w, rgba(d.Raw), tc.want, 0); s != "" {
				t.Error(s)
			}
		})
	}
}

// TestDecodeIcon checks the ICO DIB path: halved height, AND mask as alpha,
// and 32-bit alpha taking precedence over the mask.
func TestDecodeIcon(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		dib   []byte
		bands int
		want  []byte
	}{
		{
			// 2x2 4-bit; mask rows (bottom-up) 10, 01: diagonal transparent.
			name: "4-bit with AND mask",
			dib: (&dib{hdrLen: 40, w: 2, h: 2, bpp: 4, numColors: 4, palette: pal4, pixels: []byte{
				0x12, 0, 0, 0, // XOR row 1 (bottom): 1 2
				0x30, 0, 0, 0, // XOR row 0 (top):    3 0
				0x80, 0, 0, 0, // AND row 1: x0 transparent
				0x40, 0, 0, 0, // AND row 0: x1 transparent
			}}).icon(),
			bands: 4,
			want: []byte{
				0, 0, 255, 255, 0, 0, 0, 0, // 3, then 0 masked
				255, 0, 0, 0, 0, 255, 0, 255, // 1 masked, then 2
			},
		},
		{
			name: "24-bit empty mask is opaque",
			dib: (&dib{hdrLen: 40, w: 1, h: 1, bpp: 24, pixels: []byte{
				1, 2, 3, 0,
				0, 0, 0, 0,
			}}).icon(),
			bands: 3,
			want:  []byte{3, 2, 1, 255},
		},
		{
			name:  "24-bit missing mask is opaque",
			dib:   (&dib{hdrLen: 40, w: 1, h: 1, bpp: 24, pixels: []byte{1, 2, 3, 0}}).icon(),
			bands: 3,
			want:  []byte{3, 2, 1, 255},
		},
		{
			name: "32-bit alpha wins over mask",
			dib: (&dib{hdrLen: 40, w: 2, h: 1, bpp: 32, pixels: []byte{
				1, 2, 3, 128, 4, 5, 6, 255,
				0xC0, 0, 0, 0,
			}}).icon(),
			bands: 4,
			want:  []byte{3, 2, 1, 128, 6, 5, 4, 255},
		},
		{
			name: "32-bit zero alpha falls back to mask",
			dib: (&dib{hdrLen: 40, w: 2, h: 1, bpp: 32, pixels: []byte{
				1, 2, 3, 0, 4, 5, 6, 0,
				0x40, 0, 0, 0,
			}}).icon(),
			bands: 4,
			want:  []byte{3, 2, 1, 255, 6, 5, 4, 0},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, err := DecodeIcon(tc.dib, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if d.Raw.Bands != tc.bands {
				t.Errorf("bands = %d, want %d", d.Raw.Bands, tc.bands)
			}
			if s := diffPixels(d.Raw.Width, rgba(d.Raw), tc.want, 0); s != "" {
				t.Error(s)
			}
		})
	}
}

func TestDecodeEmbedded(t *testing.T) {
	t.Parallel()
	var p bytes.Buffer
	raw := testRaw(3, 2, 4)
	if err := png.Encode(&p, rawImage(raw)); err != nil {
		t.Fatal(err)
	}
	stream := p.Bytes()
	file := (&dib{hdrLen: 124, w: 3, h: 2, compression: biPNG, imageSize: u32(int64(len(stream))), pixels: append(stream, 0, 0)}).file()
	d, err := Decode(file, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Raw != nil || !bytes.Equal(d.Embedded, stream) {
		t.Fatalf("Embedded = %d bytes, Raw = %v; want the %d-byte PNG stream", len(d.Embedded), d.Raw, len(stream))
	}
	c, err := DecodeConfig(file)
	if err != nil || !c.Embedded || c.Width != 3 || c.Height != 2 {
		t.Errorf("DecodeConfig = %+v, %v", c, err)
	}

	jpeg := (&dib{hdrLen: 40, w: 1, h: 1, compression: biJPEG, pixels: []byte{0xFF, 0xD8, 0xFF, 0xE0}}).file()
	if d, err := Decode(jpeg, Options{}); err != nil || len(d.Embedded) != 4 {
		t.Errorf("BI_JPEG: %v, %d bytes", err, len(d.Embedded))
	}
	bad := (&dib{hdrLen: 40, w: 1, h: 1, compression: biJPEG, pixels: stream}).file()
	_, err = Decode(bad, Options{})
	wantKind(t, err, errs.KindCorrupt)
}

func TestDecodeErrors(t *testing.T) {
	t.Parallel()
	ok := &dib{hdrLen: 40, w: 2, h: 2, bpp: 24, pixels: make([]byte, 16)}
	tests := []struct {
		name string
		file []byte
		opts Options
		kind errs.Kind
	}{
		{name: "empty", file: nil},
		{name: "signature", file: []byte("BX" + strings.Repeat("\x00", 60))},
		{name: "file header only", file: ok.file()[:14]},
		{name: "truncated info header", file: ok.file()[:30]},
		{name: "header length", file: (&dib{hdrLen: 128, w: 1, h: 1, bpp: 24, pixels: make([]byte, 4)}).file()},
		{name: "zero width", file: (&dib{hdrLen: 40, w: 0, h: 1, bpp: 24}).file()},
		{name: "negative width", file: (&dib{hdrLen: 40, w: -1, h: 1, bpp: 24}).file()},
		{name: "zero height", file: (&dib{hdrLen: 40, w: 1, h: 0, bpp: 24}).file()},
		{name: "min int32 height", file: (&dib{hdrLen: 40, w: 1, h: math.MinInt32, bpp: 24}).file(), kind: errs.KindLimitExceeded},
		{name: "planes", file: (&dib{hdrLen: 40, w: 1, h: 1, bpp: 24, planes: 2, pixels: make([]byte, 4)}).file()},
		{name: "bit depth", file: (&dib{hdrLen: 40, w: 1, h: 1, bpp: 7, pixels: make([]byte, 4)}).file()},
		{name: "compression", file: (&dib{hdrLen: 40, w: 1, h: 1, bpp: 24, compression: 9, pixels: make([]byte, 4)}).file()},
		{name: "rle8 with 4 bpp", file: (&dib{hdrLen: 40, w: 1, h: 1, bpp: 4, compression: biRLE8, palette: pal4}).file()},
		{name: "bitfields with 24 bpp", file: (&dib{hdrLen: 40, w: 1, h: 1, bpp: 24, compression: biBitfields, masks: []uint32{1, 2, 4}}).file()},
		{name: "truncated masks", file: (&dib{hdrLen: 40, w: 1, h: 1, bpp: 16, compression: biBitfields}).file()},
		{name: "non-contiguous mask", file: (&dib{hdrLen: 40, w: 1, h: 1, bpp: 16, compression: biBitfields, masks: []uint32{0x5, 0x10, 0x20}, pixels: make([]byte, 4)}).file()},
		{name: "16-bit mask too wide", file: (&dib{hdrLen: 40, w: 1, h: 1, bpp: 16, compression: biBitfields, masks: []uint32{0xF0000, 0x10, 0x20}, pixels: make([]byte, 4)}).file()},
		{name: "OS/2 huffman", file: (&dib{hdrLen: 64, w: 1, h: 1, bpp: 1, compression: 3, pixels: make([]byte, 4)}).file()},
		{name: "OS/2 rle24", file: (&dib{hdrLen: 64, w: 1, h: 1, bpp: 24, compression: 4, pixels: make([]byte, 4)}).file()},
		{name: "missing palette", file: (&dib{hdrLen: 40, w: 1, h: 1, bpp: 8, pixels: nil}).file()},
		{name: "offset inside header", file: (&dib{hdrLen: 40, w: 1, h: 1, bpp: 24, offset: 20, pixels: make([]byte, 4)}).file()},
		{name: "offset past end", file: (&dib{hdrLen: 40, w: 1, h: 1, bpp: 24, offset: 1 << 20, pixels: make([]byte, 4)}).file()},
		{name: "truncated pixels", file: ok.file()[:len(ok.file())-1]},
		{name: "truncated rle command", file: (&dib{hdrLen: 40, w: 2, h: 1, bpp: 8, compression: biRLE8, palette: pal4, pixels: []byte{2}}).file()},
		{name: "truncated rle literal", file: (&dib{hdrLen: 40, w: 4, h: 1, bpp: 8, compression: biRLE8, palette: pal4, pixels: []byte{0, 4, 1, 2}}).file()},
		{name: "truncated rle delta", file: (&dib{hdrLen: 40, w: 4, h: 1, bpp: 8, compression: biRLE8, palette: pal4, pixels: []byte{0, 2, 1}}).file()},
		{name: "pixel limit", file: ok.file(), opts: Options{MaxPixels: 3}, kind: errs.KindLimitExceeded},
		{
			// A 1 GB claim in a tiny file fails before any allocation.
			name: "huge dimensions", kind: errs.KindLimitExceeded,
			file: (&dib{hdrLen: 40, w: 1 << 15, h: 1 << 15, bpp: 32, pixels: make([]byte, 4)}).file(),
		},
		{
			name: "huge dimensions within limit", opts: Options{MaxPixels: math.MaxInt64},
			file: (&dib{hdrLen: 40, w: 1 << 30, h: 1 << 30, bpp: 32, pixels: make([]byte, 4)}).file(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			want := tc.kind
			if want == errs.KindUnknown {
				want = errs.KindCorrupt
			}
			_, err := Decode(tc.file, tc.opts)
			wantKind(t, err, want)
		})
	}
}

func TestSniff(t *testing.T) {
	t.Parallel()
	valid := (&dib{hdrLen: 40, w: 1, h: 1, bpp: 24, pixels: make([]byte, 4)}).file()
	tests := []struct {
		name string
		head []byte
		want bool
	}{
		{name: "empty", head: nil},
		{name: "B", head: []byte("B")},
		{name: "BM only", head: []byte("BM"), want: true},
		{name: "valid", head: valid, want: true},
		{name: "core header", head: (&dib{hdrLen: 12, w: 1, h: 1, bpp: 24}).file(), want: true},
		{name: "V5", head: readFixture(t, "v5a.bmp"), want: true},
		{name: "bad header length", head: append([]byte("BM"), make([]byte, 20)...)},
		{name: "png", head: []byte("\x89PNG\r\n\x1a\n")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Sniff(tc.head); got != tc.want {
				t.Errorf("Sniff = %v, want %v", got, tc.want)
			}
		})
	}
}

// testRaw returns a deterministic w×h image with varied samples.
func testRaw(w, h, bands int) *raster.Raw {
	r := &raster.Raw{Width: w, Height: h, Bands: bands, Data: make([]byte, w*h*bands)}
	var v, row byte // byte(i*37 + i/bands*11) without narrowing conversions
	for i := range r.Data {
		if i > 0 && i%bands == 0 {
			row += 11
		}
		r.Data[i] = v*37 + row
		v++
	}
	return r
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  *raster.Raw
	}{
		{name: "rgb 1x1", raw: testRaw(1, 1, 3)},
		{name: "rgb 13x7", raw: testRaw(13, 7, 3)},
		{name: "rgba 13x7", raw: testRaw(13, 7, 4)},
		{name: "rgba 4x3", raw: testRaw(4, 3, 4)},
		{name: "rgb with resolution", raw: func() *raster.Raw {
			r := testRaw(5, 2, 3)
			r.XRes, r.YRes = 2.835, 11.811
			return r
		}()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b, err := Encode(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			d, err := Decode(b, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.raw, d.Raw); diff != "" {
				t.Errorf("round trip (-want +got):\n%s", diff)
			}
			// An independent reader agrees, alpha included (V4 header).
			m, err := xbmp.Decode(bytes.NewReader(b))
			if err != nil {
				t.Fatalf("x/image/bmp: %v", err)
			}
			_, _, want := imageRGBA(m)
			if s := diffPixels(tc.raw.Width, rgba(tc.raw), want, 0); s != "" {
				t.Errorf("x/image/bmp: %s", s)
			}
		})
	}
}

func TestEncodeInvalid(t *testing.T) {
	t.Parallel()
	for _, r := range []*raster.Raw{
		{Width: 0, Height: 1, Bands: 3},
		{Width: 1, Height: 1, Bands: 2, Data: make([]byte, 2)},
		{Width: 2, Height: 1, Bands: 3, Data: make([]byte, 3)},
	} {
		if _, err := Encode(r); err == nil {
			t.Errorf("Encode(%+v) succeeded", r)
		}
	}
}
