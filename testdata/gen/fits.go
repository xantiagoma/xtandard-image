package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// ---- FITS (WP-108) ------------------------------------------------------------
//
// Uncompressed FITS fixtures for internal/codec/fits, from the photo: its
// luminance (or its channels) in each sample type, stored bottom row first
// like every FITS writer. The tile-compressed fixtures are made from these
// by cfitsio's fpack (testdata/gen/gen.sh).

// fitsCard is one 80-column header card: `KEY     = value`.
func fitsCard(key string, value any) string {
	var v string
	switch x := value.(type) {
	case bool:
		v = fmt.Sprintf("%20s", map[bool]string{true: "T", false: "F"}[x])
	case int:
		v = fmt.Sprintf("%20d", x)
	case float64:
		v = fmt.Sprintf("%20s", strings.ToUpper(fmt.Sprintf("%G", x)))
	case string:
		v = fmt.Sprintf("'%-8s'", x)
	}
	return fmt.Sprintf("%-8s= %-70s", key, v)
}

// fitsHDU is a header (cards, END, padding) followed by the padded data.
func fitsHDU(cards []string, data []byte) []byte {
	h := strings.Join(cards, "") + fmt.Sprintf("%-80s", "END")
	for len(h)%2880 != 0 {
		h += " "
	}
	out := append([]byte(h), data...)
	for len(out)%2880 != 0 {
		out = append(out, 0)
	}
	return out
}

// fitsImageCards are the structural cards of an image HDU.
func fitsImageCards(first []string, bitpix int, axes ...int) []string {
	c := append([]string{}, first...)
	c = append(c, fitsCard("BITPIX", bitpix), fitsCard("NAXIS", len(axes)))
	for i, a := range axes {
		c = append(c, fitsCard(fmt.Sprintf("NAXIS%d", i+1), a))
	}
	return c
}

// fitsPlanes returns the photo's planes, bottom row first: the luminance,
// or R, G and B, in [0, 1].
func fitsPlanes(w, h int, colour bool) [][]float64 {
	img := photo(w, h)
	n := 1
	if colour {
		n = 3
	}
	planes := make([][]float64, n)
	for p := range planes {
		planes[p] = make([]float64, w*h)
	}
	for y := range h {
		for x := range w {
			c := img.RGBAAt(x, h-1-y)
			r, g, b := float64(c.R)/255, float64(c.G)/255, float64(c.B)/255
			if colour {
				planes[0][y*w+x], planes[1][y*w+x], planes[2][y*w+x] = r, g, b
			} else {
				planes[0][y*w+x] = 0.299*r + 0.587*g + 0.114*b
			}
		}
	}
	return planes
}

// writeFITSFixtures writes the uncompressed fixtures into dir.
func writeFITSFixtures(dir string, w, h int) error {
	be := binary.BigEndian
	grey := fitsPlanes(w, h, false)[0]
	rgb := fitsPlanes(w, h, true)
	simple := []string{fitsCard("SIMPLE", true)}
	files := map[string][]byte{}

	// 8-bit: bytes are grey levels.
	d := make([]byte, w*h)
	for i, v := range grey {
		d[i] = uint8(math.Round(v * 255))
	}
	files["u8.fits"] = fitsHDU(fitsImageCards(simple, 8, w, h), d)

	// Unsigned 16-bit through BZERO = 32768.
	d = make([]byte, 2*w*h)
	for i, v := range grey {
		be.PutUint16(d[2*i:], uint16(int32(math.Round(v*65535))-32768))
	}
	files["u16.fits"] = fitsHDU(append(fitsImageCards(simple, 16, w, h),
		fitsCard("BZERO", 32768.0), fitsCard("BSCALE", 1.0)), d)

	// 32-bit float, astronomy-like: a faint gradient with the photo as
	// structure, and a 4x4 block of NaN.
	d = make([]byte, 4*w*h)
	for i, v := range grey {
		f := float32(100 + 900*v*v)
		if x, y := i%w, i/w; x >= 2 && x < 6 && y >= 2 && y < 6 {
			f = float32(math.NaN())
		}
		be.PutUint32(d[4*i:], math.Float32bits(f))
	}
	files["f32-nan.fits"] = fitsHDU(fitsImageCards(simple, -32, w, h), d)

	// 64-bit float.
	d = make([]byte, 8*w*h)
	for i, v := range grey {
		be.PutUint64(d[8*i:], math.Float64bits(-1+2*v))
	}
	files["f64.fits"] = fitsHDU(fitsImageCards(simple, -64, w, h), d)

	// 3-plane colour, 16-bit signed.
	d = make([]byte, 2*3*w*h)
	for p, plane := range rgb {
		for i, v := range plane {
			be.PutUint16(d[2*(p*w*h+i):], uint16(int16(math.Round(v*32767))))
		}
	}
	files["rgb16.fits"] = fitsHDU(fitsImageCards(simple, 16, w, h, 3), d)

	// Multi-extension: an empty primary, an ASCII-free binary table to skip,
	// then an int32 IMAGE extension with BLANK pixels in its top-left corner.
	var mef []byte
	mef = append(mef, fitsHDU(append(fitsImageCards(simple, 8), fitsCard("EXTEND", true)), nil)...)
	table := make([]byte, 4*3)
	for i := range 3 {
		be.PutUint32(table[4*i:], uint32(i+1))
	}
	mef = append(mef, fitsHDU(append(fitsImageCards([]string{fitsCard("XTENSION", "BINTABLE")}, 8, 4, 3),
		fitsCard("PCOUNT", 0), fitsCard("GCOUNT", 1), fitsCard("TFIELDS", 1), fitsCard("TFORM1", "1J"),
		fitsCard("TTYPE1", "INDEX")), table)...)
	d = make([]byte, 4*w*h)
	for i, v := range grey {
		n := int32(math.Round(v * 1_000_000))
		if x, y := i%w, i/w; x < 3 && y >= h-3 {
			n = -999
		}
		be.PutUint32(d[4*i:], uint32(n))
	}
	mef = append(mef, fitsHDU(append(fitsImageCards([]string{fitsCard("XTENSION", "IMAGE")}, 32, w, h),
		fitsCard("PCOUNT", 0), fitsCard("GCOUNT", 1), fitsCard("BLANK", -999)), d)...)
	files["mef-i32.fits"] = mef

	for name, b := range files {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}
