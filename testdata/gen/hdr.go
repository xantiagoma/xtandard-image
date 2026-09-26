package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"sort"
)

// hdrPixel is the procedural HDR scene in scene-linear light: the landscape
// of scene() linearised (γ 2.2), a sun disc at 40 and a glow
// around it adding up to 3; see `gen hdr-stats`. Alpha is 1,
// except in the bottom quarter, where it ramps from 0 (left) to 1 (right).
func hdrPixel(u, v float64) (r, g, b, a float64) {
	r, g, b = scene(u, v)
	lin := func(c float64) float64 { return math.Pow(c, 2.2) }
	r, g, b = lin(r), lin(g), lin(b)
	d := math.Hypot(u-0.78, (v-0.18)*0.75)
	if glow := 1 - d/0.25; glow > 0 {
		k := 3 * glow * glow * glow
		r, g, b = r+k, g+0.8*k, b+0.5*k
	}
	if d < 0.03 {
		r, g, b = 40, 36, 30
	}
	a = 1
	if v > 0.75 {
		a = u
	}
	return r, g, b, a
}

// hdrImage samples hdrPixel at pixel centres, rows top to bottom, bands
// interleaved (3 or 4 per pixel).
func hdrImage(w, h, bands int) []float32 {
	out := make([]float32, 0, w*h*bands)
	for y := range h {
		for x := range w {
			r, g, b, a := hdrPixel((float64(x)+0.5)/float64(w), (float64(y)+0.5)/float64(h))
			out = append(out, float32(r), float32(g), float32(b))
			if bands == 4 {
				out = append(out, float32(a))
			}
		}
	}
	return out
}

// ---- OpenEXR -----------------------------------------------------------------

// writeEXR writes a single-part scanline OpenEXR file without compression:
// channels R, G, B (and A with alpha) as half or float, one scanline per
// block (OpenEXR file layout, openexr.com "OpenEXR File Layout").
func writeEXR(out string, w, h int, alpha, float bool) error {
	bands := 3
	names := []string{"B", "G", "R"} // stored in alphabetical order
	if alpha {
		bands = 4
		names = []string{"A", "B", "G", "R"}
	}
	band := map[string]int{"R": 0, "G": 1, "B": 2, "A": 3}
	pix := hdrImage(w, h, bands)
	pixelType, size := uint32(1), 2 // HALF
	if float {
		pixelType, size = 2, 4 // FLOAT
	}
	le := binary.LittleEndian
	var hd bytes.Buffer
	hd.Write([]byte{0x76, 0x2f, 0x31, 0x01})
	_ = binary.Write(&hd, le, uint32(2)) // version 2, scanline, short names
	attr := func(name, typ string, value []byte) {
		hd.WriteString(name + "\x00" + typ + "\x00")
		_ = binary.Write(&hd, le, uint32(len(value))) //nolint:gosec // small
		hd.Write(value)
	}
	var ch bytes.Buffer
	for _, n := range names {
		ch.WriteString(n + "\x00")
		_ = binary.Write(&ch, le, pixelType)
		ch.Write([]byte{0, 0, 0, 0}) // pLinear, reserved
		_ = binary.Write(&ch, le, [2]int32{1, 1})
	}
	ch.WriteByte(0)
	box := func(x0, y0, x1, y1 int32) []byte {
		var b bytes.Buffer
		_ = binary.Write(&b, le, [4]int32{x0, y0, x1, y1})
		return b.Bytes()
	}
	f32 := func(vs ...float32) []byte {
		var b bytes.Buffer
		_ = binary.Write(&b, le, vs)
		return b.Bytes()
	}
	win := box(0, 0, int32(w-1), int32(h-1)) //nolint:gosec // small
	attr("channels", "chlist", ch.Bytes())
	attr("compression", "compression", []byte{0})
	attr("dataWindow", "box2i", win)
	attr("displayWindow", "box2i", win)
	attr("lineOrder", "lineOrder", []byte{0})
	attr("pixelAspectRatio", "float", f32(1))
	attr("screenWindowCenter", "v2f", f32(0, 0))
	attr("screenWindowWidth", "float", f32(1))
	hd.WriteByte(0)

	lineBytes := w * bands * size
	blockBytes := 8 + lineBytes
	first := hd.Len() + 8*h
	for y := range h {
		_ = binary.Write(&hd, le, uint64(first+y*blockBytes)) //nolint:gosec // small
	}
	for y := range h {
		_ = binary.Write(&hd, le, [2]int32{int32(y), int32(lineBytes)}) //nolint:gosec // small
		for _, n := range names {
			for x := range w {
				v := pix[(y*w+x)*bands+band[n]]
				if float {
					_ = binary.Write(&hd, le, v)
				} else {
					_ = binary.Write(&hd, le, halfFloat(v))
				}
			}
		}
	}
	return os.WriteFile(out, hd.Bytes(), 0o644)
}

// halfFloat converts v to IEEE 754 binary16, rounding to nearest (ties
// away), with overflow to infinity and gradual underflow.
func halfFloat(v float32) uint16 {
	b := math.Float32bits(v)
	sign := uint16(b>>16) & 0x8000
	exp := int((b>>23)&0xff) - 127 + 15
	mant := b & 0x7fffff
	switch {
	case exp >= 31:
		return sign | 0x7c00
	case exp <= 0:
		if exp < -10 {
			return sign
		}
		mant |= 0x800000
		shift := uint(14 - exp) //nolint:gosec // 14..24
		return sign | uint16((mant+(1<<(shift-1)))>>shift)
	default:
		h := sign | uint16(exp<<10) | uint16(mant>>13) //nolint:gosec // bounded
		if mant&0x1000 != 0 {
			h++ // may carry into the exponent, which is still correct
		}
		return h
	}
}

// ---- Radiance ------------------------------------------------------------------

// writeRadiance writes flat (not run-length encoded) RGBE scanlines under
// the given magic line ("#?RADIANCE" or "#?RGBE"), top to bottom.
func writeRadiance(out, magic string, w, h int) error {
	pix := hdrImage(w, h, 3)
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s\nFORMAT=32-bit_rle_rgbe\n\n-Y %d +X %d\n", magic, h, w)
	for i := 0; i < len(pix); i += 3 {
		r, g, bl := float64(pix[i]), float64(pix[i+1]), float64(pix[i+2])
		m := math.Max(r, math.Max(g, bl))
		if m < 1e-32 {
			b.Write([]byte{0, 0, 0, 0})
			continue
		}
		frac, e := math.Frexp(m)
		s := frac * 256 / m
		b.Write([]byte{byte(r * s), byte(g * s), byte(bl * s), byte(e + 128)}) //nolint:gosec // < 256
	}
	return os.WriteFile(out, b.Bytes(), 0o644)
}

// ---- PFM / PPM -------------------------------------------------------------------

// writePFM writes a colour PFM ("PF", little endian: scale −1), rows bottom
// to top as the format requires.
func writePFM(out string, w, h int) error {
	pix := hdrImage(w, h, 3)
	var b bytes.Buffer
	fmt.Fprintf(&b, "PF\n%d %d\n-1.0\n", w, h)
	for y := h - 1; y >= 0; y-- {
		_ = binary.Write(&b, binary.LittleEndian, pix[y*w*3:(y+1)*w*3])
	}
	return os.WriteFile(out, b.Bytes(), 0o644)
}

// writePPM writes the procedural photo as a binary 8-bit PPM ("P6").
func writePPM(out string, w, h int) error {
	img := photo(w, h)
	var b bytes.Buffer
	fmt.Fprintf(&b, "P6\n%d %d\n255\n", w, h)
	for y := range h {
		for x := range w {
			i := img.PixOffset(x, y)
			b.Write(img.Pix[i : i+3])
		}
	}
	return os.WriteFile(out, b.Bytes(), 0o644)
}

// hdrStats prints the fraction of pixels above 1.0 and the 99th percentile
// luminance of the scene, for the documentation of the fixtures.
func hdrStats(w, h int) {
	pix := hdrImage(w, h, 3)
	ys := make([]float64, 0, w*h)
	over := 0
	for i := 0; i < len(pix); i += 3 {
		y := 0.2126*float64(pix[i]) + 0.7152*float64(pix[i+1]) + 0.0722*float64(pix[i+2])
		ys = append(ys, y)
		if y > 1 {
			over++
		}
	}
	sort.Float64s(ys)
	fmt.Printf("%dx%d: %.1f%% above 1.0, p99 %.3f, max %.3f\n", w, h,
		100*float64(over)/float64(len(ys)), ys[int(math.Ceil(0.99*float64(len(ys))))-1], ys[len(ys)-1])
}
