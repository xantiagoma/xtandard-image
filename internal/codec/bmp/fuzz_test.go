package bmp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// fuzzMaxPixels keeps fuzz iterations cheap; limits are exercised anyway.
const fuzzMaxPixels = 1 << 16

// FuzzDecodeBMP checks that Decode never panics, fails only with Corrupt or
// LimitExceeded, returns a well-formed result, and that every Raw it returns
// (bar a fully transparent one) survives Encode → Decode unchanged.
func FuzzDecodeBMP(f *testing.F) {
	files, err := filepath.Glob(filepath.Join("testdata", "*.bmp"))
	if err != nil {
		f.Fatal(err)
	}
	for _, name := range files {
		b, err := os.ReadFile(name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add((&dib{hdrLen: 40, w: 4, h: 3, bpp: 4, compression: biRLE4, palette: pal4, pixels: []byte{
		4, 0x12, 0, 0, 0, 3, 0x30, 0xF0, 0, 2, 2, 0, 2, 0x33, 0, 1,
	}}).file())
	f.Add((&dib{
		hdrLen: 40, w: 1, h: 1, bpp: 16, compression: biAlphaBitfields,
		masks: []uint32{0x0F00, 0x00F0, 0x000F, 0xF000}, pixels: []byte{0x0F, 0x80, 0, 0},
	}).file())
	f.Add((&dib{hdrLen: 12, w: 3, h: 1, bpp: 1, palette: pal4[:2], pixels: []byte{0xA0, 0, 0, 0}}).file())
	f.Add((&dib{hdrLen: 40, w: 1, h: 1, compression: biPNG, pixels: pngSignature}).file())

	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := Decode(data, Options{MaxPixels: fuzzMaxPixels})
		if err != nil {
			if k := errs.KindOf(err); k != errs.KindCorrupt && k != errs.KindLimitExceeded {
				t.Fatalf("error kind %v: %v", k, err)
			}
			return
		}
		if d.Embedded != nil {
			if d.Raw != nil {
				t.Fatal("both Raw and Embedded set")
			}
			return
		}
		if err := d.Raw.Validate(); err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeConfig(data); err != nil {
			t.Fatalf("Decode succeeded but DecodeConfig failed: %v", err)
		}
		if d.Raw.Bands == 4 && !anyAlpha(d.Raw.Data) {
			// Fully transparent (an RLE stream that sets no pixel): Encode keeps
			// it, but Decode reads an all-zero alpha channel as opaque.
			return
		}
		enc, err := Encode(d.Raw)
		if err != nil {
			t.Fatal(err)
		}
		back, err := Decode(enc, Options{MaxPixels: fuzzMaxPixels})
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(d.Raw, back.Raw); diff != "" {
			t.Fatalf("round trip (-first +second):\n%s", diff)
		}
	})
}

// FuzzDecodeIcon covers the ICO DIB entry point (DecodeIcon).
func FuzzDecodeIcon(f *testing.F) {
	f.Add((&dib{hdrLen: 40, w: 2, h: 2, bpp: 4, numColors: 4, palette: pal4, pixels: []byte{
		0x12, 0, 0, 0, 0x30, 0, 0, 0, 0x80, 0, 0, 0, 0x40, 0, 0, 0,
	}}).icon())
	f.Add((&dib{hdrLen: 40, w: 2, h: 1, bpp: 32, pixels: []byte{1, 2, 3, 0, 4, 5, 6, 0, 0x40, 0, 0, 0}}).icon())
	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := DecodeIcon(data, Options{MaxPixels: fuzzMaxPixels})
		if err != nil {
			if k := errs.KindOf(err); k != errs.KindCorrupt && k != errs.KindLimitExceeded {
				t.Fatalf("error kind %v: %v", k, err)
			}
			return
		}
		if d.Raw != nil {
			if err := d.Raw.Validate(); err != nil {
				t.Fatal(err)
			}
		}
	})
}
