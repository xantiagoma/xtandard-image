package main

import (
	"encoding/binary"
	"math"
	"os"
)

// ---- CMYK JPEG XL inputs (WP-105) -----------------------------------------------
//
// `gen cmyk-raw W H alpha|opaque out.cmyk out.icc` writes the photo as
// interleaved 8-bit C, M, Y, K (and alpha) samples, 0 = no ink, by naive
// under-colour removal (K = 1 − max(R, G, B) on the sRGB values), and a
// minimal CMYK ICC profile that inverts that model exactly at its grid
// points (cmykICC). testdata/gen/jxlcmyk/encode.c turns them into a
// lossless JPEG XL with the black channel as an extra channel.

// cmykOf converts an sRGB colour to naive CMYK inks in [0, 1].
func cmykOf(r, g, b uint8) (c, m, y, k float64) {
	R, G, B := float64(r)/255, float64(g)/255, float64(b)/255
	k = 1 - max(R, G, B)
	if k >= 1 {
		return 0, 0, 0, 1
	}
	return (1 - R - k) / (1 - k), (1 - G - k) / (1 - k), (1 - B - k) / (1 - k), k
}

func writeCMYKRaw(w, h int, alpha bool, rawOut, iccOut string) error {
	img := withAlpha(photo(w, h))
	var out []byte
	q := func(v float64) byte { return byte(math.Round(min(max(v, 0), 1) * 255)) }
	for y := range h {
		for x := range w {
			p := img.NRGBAAt(x, y)
			c, m, yy, k := cmykOf(p.R, p.G, p.B)
			out = append(out, q(c), q(m), q(yy), q(k))
			if alpha {
				out = append(out, p.A)
			}
		}
	}
	if err := os.WriteFile(rawOut, out, 0o644); err != nil {
		return err
	}
	return os.WriteFile(iccOut, cmykICC(), 0o644)
}

// cmykICC is a minimal ICC v2 output-class CMYK profile whose A2B0 is a
// lut16 over a 5-point grid per ink (625 points): each point's colour is
// the naive model R = (1−C)(1−K), G = (1−M)(1−K), B = (1−Y)(1−K) on sRGB
// values, converted to L*a*b* (D50, Bradford), in the lut16 L*a*b*
// encoding.
func cmykICC() []byte {
	be := binary.BigEndian
	s15 := func(b []byte, v float64) []byte { return be.AppendUint32(b, uint32(int32(math.Round(v*65536)))) }
	const n = 5
	lut := append([]byte("mft2"), 0, 0, 0, 0, 4, 3, n, 0)
	for i := range 9 {
		v := 0.0
		if i%4 == 0 {
			v = 1
		}
		lut = s15(lut, v)
	}
	lut = be.AppendUint16(be.AppendUint16(lut, 2), 2)
	for range 4 {
		lut = be.AppendUint16(be.AppendUint16(lut, 0), 0xFFFF)
	}
	// Grid order: the first input channel (C) varies slowest.
	for i := range n * n * n * n {
		ink := [4]float64{
			float64(i/(n*n*n)%n) / (n - 1), float64(i/(n*n)%n) / (n - 1),
			float64(i/n%n) / (n - 1), float64(i%n) / (n - 1),
		}
		r := uint8(math.Round((1 - ink[0]) * (1 - ink[3]) * 255))
		g := uint8(math.Round((1 - ink[1]) * (1 - ink[3]) * 255))
		b := uint8(math.Round((1 - ink[2]) * (1 - ink[3]) * 255))
		l, a, bb := srgbToLabD50(r, g, b)
		// lut16 L*a*b* (ICC v2): L 0-100 → 0-0xFF00, a/b −128 → 0, +127.996 → 0xFFFF.
		lut = be.AppendUint16(lut, uint16(math.Round(min(max(l, 0), 100)*0xFF00/100)))
		lut = be.AppendUint16(lut, uint16(math.Round((min(max(a, -128), 127.996)+128)*256)))
		lut = be.AppendUint16(lut, uint16(math.Round((min(max(bb, -128), 127.996)+128)*256)))
	}
	for range 3 {
		lut = be.AppendUint16(be.AppendUint16(lut, 0), 0xFFFF)
	}
	text := "naive CMYK (xtandard-image fixture)"
	desc := append([]byte("desc"), 0, 0, 0, 0)
	desc = be.AppendUint32(desc, uint32(len(text)+1))
	desc = append(append(desc, text...), 0)
	desc = append(desc, make([]byte, 4+4+2+1+67)...)
	wtpt := s15(s15(s15(append([]byte("XYZ "), 0, 0, 0, 0), 0.9642), 1.0), 0.8249)
	tags := []struct {
		sig  string
		data []byte
	}{{"desc", desc}, {"wtpt", wtpt}, {"A2B0", lut}}
	off := 128 + 4 + 12*len(tags)
	table := be.AppendUint32(nil, uint32(len(tags)))
	var data []byte
	for _, t := range tags {
		for len(data)%4 != 0 {
			data = append(data, 0)
		}
		table = append(table, t.sig...)
		table = be.AppendUint32(table, uint32(off+len(data)))
		table = be.AppendUint32(table, uint32(len(t.data)))
		data = append(data, t.data...)
	}
	h := be.AppendUint32(nil, uint32(128+len(table)+len(data)))
	h = append(h, 0, 0, 0, 0, 2, 0x10, 0, 0)
	h = append(h, "prtrCMYKLab "...)
	h = append(h, 7, 234, 0, 1, 0, 1, 0, 0, 0, 0, 0, 0)
	h = append(h, "acsp"...)
	h = append(h, make([]byte, 28)...)
	h = s15(s15(s15(h, 0.9642), 1.0), 0.8249)
	h = append(h, make([]byte, 128-len(h))...)
	return append(append(h, table...), data...)
}
