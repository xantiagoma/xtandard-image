package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

// ---- CIE L*a*b* TIFF (WP-105) -------------------------------------------------
//
// Uncompressed, single-strip TIFFs with PhotometricInterpretation 8 (CIELab):
// the photo converted from sRGB to L*a*b* relative to D50 (Bradford
// adaptation), the white point libtiff assumes for CIELab. 8-bit files store
// L* scaled to 0-255 and a*, b* as signed bytes; 16-bit files L* scaled to
// 0-65535 and a*, b* as signed 16-bit values × 256 (TIFF 6.0 section 23).

// srgbToLabD50 converts an 8-bit sRGB colour to L*a*b* (D50).
func srgbToLabD50(r, g, b uint8) (l, a, bb float64) {
	lin := func(v uint8) float64 {
		c := float64(v) / 255
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	R, G, B := lin(r), lin(g), lin(b)
	// sRGB (D65) to XYZ D50 through the Bradford-adapted matrix
	// (Lindbloom, "RGB/XYZ Matrices", sRGB D50).
	x := 0.4360747*R + 0.3850649*G + 0.1430804*B
	y := 0.2225045*R + 0.7168786*G + 0.0606169*B
	z := 0.0139322*R + 0.0971045*G + 0.7141733*B
	const xn, yn, zn = 0.96422, 1.0, 0.82521
	f := func(t float64) float64 {
		if t > 216.0/24389 {
			return math.Cbrt(t)
		}
		return (24389.0/27*t + 16) / 116
	}
	fx, fy, fz := f(x/xn), f(y/yn), f(z/zn)
	return 116*fy - 16, 500 * (fx - fy), 200 * (fy - fz)
}

// labTIFFFixture writes lab-tiff8, lab-tiff16, lab-tiff8-alpha (the alpha
// fade as an unassociated extra sample) or lab-tiff8-icc (an embedded
// identity L*a*b* ICC profile, labICC).
func labTIFFFixture(kind string, w, h int, out string) error {
	img := withAlpha(photo(w, h))
	bits, alpha, icc := 8, false, []byte(nil)
	switch kind {
	case "lab-tiff8":
	case "lab-tiff16":
		bits = 16
	case "lab-tiff8-alpha":
		alpha = true
	case "lab-tiff8-icc":
		icc = labICC()
	default:
		return fmt.Errorf("unknown Lab TIFF fixture %q", kind)
	}
	spp := 3
	if alpha {
		spp = 4
	}
	var pix []byte
	for y := range h {
		for x := range w {
			c := img.NRGBAAt(x, y)
			l, a, b := srgbToLabD50(c.R, c.G, c.B)
			if bits == 8 {
				pix = append(pix, uint8(math.Round(min(max(l, 0), 100)*255/100)),
					byte(int8(math.Round(min(max(a, -128), 127)))),
					byte(int8(math.Round(min(max(b, -128), 127)))))
				if alpha {
					pix = append(pix, c.A)
				}
				continue
			}
			pix = binary.LittleEndian.AppendUint16(pix, uint16(math.Round(min(max(l, 0), 100)*65535/100)))
			pix = binary.LittleEndian.AppendUint16(pix, uint16(int16(math.Round(min(max(a, -128), 127.99)*256))))
			pix = binary.LittleEndian.AppendUint16(pix, uint16(int16(math.Round(min(max(b, -128), 127.99)*256))))
		}
	}
	return os.WriteFile(out, labTIFF(w, h, bits, spp, alpha, icc, pix), 0o644)
}

// labTIFF lays out a little-endian TIFF: header, pixels, then IFD0 and its
// out-of-line values.
func labTIFF(w, h, bits, spp int, alpha bool, icc, pix []byte) []byte {
	le := binary.LittleEndian
	type entry struct {
		tag, typ uint16
		count    uint32
		value    []byte // inline when ≤ 4 bytes
	}
	short := func(v ...uint16) []byte {
		var b []byte
		for _, x := range v {
			b = le.AppendUint16(b, x)
		}
		return b
	}
	long := func(v uint32) []byte { return le.AppendUint32(nil, v) }
	bps := make([]uint16, spp)
	for i := range bps {
		bps[i] = uint16(bits)
	}
	const pixOff = 8
	entries := []entry{
		{256, 4, 1, long(uint32(w))},
		{257, 4, 1, long(uint32(h))},
		{258, 3, uint32(spp), short(bps...)},
		{259, 3, 1, short(1)}, // no compression
		{262, 3, 1, short(8)}, // CIELab
		{273, 4, 1, long(pixOff)},
		{277, 3, 1, short(uint16(spp))},
		{278, 4, 1, long(uint32(h))},
		{279, 4, 1, long(uint32(len(pix)))},
		{284, 3, 1, short(1)}, // chunky
	}
	if alpha {
		entries = append(entries, entry{338, 3, 1, short(2)}) // unassociated alpha
	}
	if icc != nil {
		entries = append(entries, entry{34675, 7, uint32(len(icc)), icc})
	}
	ifdOff := pixOff + len(pix)
	ifdOff += ifdOff & 1
	extra := ifdOff + 2 + 12*len(entries) + 4
	out := make([]byte, 0, extra+len(icc)+64)
	out = append(out, 'I', 'I', 42, 0)
	out = le.AppendUint32(out, uint32(ifdOff))
	out = append(out, pix...)
	for len(out) < ifdOff {
		out = append(out, 0)
	}
	out = le.AppendUint16(out, uint16(len(entries)))
	var tail []byte
	for _, e := range entries {
		out = le.AppendUint16(out, e.tag)
		out = le.AppendUint16(out, e.typ)
		out = le.AppendUint32(out, e.count)
		if len(e.value) <= 4 {
			v := make([]byte, 4)
			copy(v, e.value)
			out = append(out, v...)
			continue
		}
		out = le.AppendUint32(out, uint32(extra+len(tail)))
		tail = append(tail, e.value...)
		if len(tail)&1 == 1 {
			tail = append(tail, 0)
		}
	}
	out = le.AppendUint32(out, 0) // no next IFD
	return append(out, tail...)
}

// labICC is a minimal ICC v2 abstract profile for L*a*b* data relative to
// D50: identity lut16 A2B0 and B2A0 transforms (2-point grids), the D50
// media white point and a description, the shape of lcms's
// cmsCreateLab2Profile(NULL).
func labICC() []byte {
	be := binary.BigEndian
	s15 := func(b []byte, v float64) []byte { return be.AppendUint32(b, uint32(int32(math.Round(v*65536)))) }
	xyz := func(x, y, z float64) []byte {
		b := append([]byte("XYZ "), 0, 0, 0, 0)
		return s15(s15(s15(b, x), y), z)
	}
	desc := func(s string) []byte {
		b := append([]byte("desc"), 0, 0, 0, 0)
		b = be.AppendUint32(b, uint32(len(s)+1))
		b = append(append(b, s...), 0)
		b = append(b, make([]byte, 4+4+2+1+67)...) // no Unicode or ScriptCode text
		return b
	}
	lut := func() []byte {
		b := append([]byte("mft2"), 0, 0, 0, 0)
		b = append(b, 3, 3, 2, 0)
		for i := range 9 { // identity matrix (ignored for L*a*b* input)
			v := 0.0
			if i%4 == 0 {
				v = 1
			}
			b = s15(b, v)
		}
		b = be.AppendUint16(b, 2)
		b = be.AppendUint16(b, 2)
		for range 3 {
			b = be.AppendUint16(be.AppendUint16(b, 0), 0xFFFF)
		}
		for i := range 8 { // grid points (i>>2, i>>1&1, i&1)
			for _, bit := range []int{i >> 2 & 1, i >> 1 & 1, i & 1} {
				b = be.AppendUint16(b, uint16(bit*0xFFFF))
			}
		}
		for range 3 {
			b = be.AppendUint16(be.AppendUint16(b, 0), 0xFFFF)
		}
		return b
	}
	tags := []struct {
		sig  string
		data []byte
	}{
		{"desc", desc("L*a*b* identity (D50)")},
		{"wtpt", xyz(0.9642, 1.0, 0.8249)},
		{"A2B0", lut()},
		{"B2A0", lut()},
	}
	off := 128 + 4 + 12*len(tags)
	var table, data []byte
	table = be.AppendUint32(table, uint32(len(tags)))
	for _, t := range tags {
		for len(data)%4 != 0 {
			data = append(data, 0)
		}
		table = append(table, t.sig...)
		table = be.AppendUint32(table, uint32(off+len(data)))
		table = be.AppendUint32(table, uint32(len(t.data)))
		data = append(data, t.data...)
	}
	size := 128 + len(table) + len(data)
	h := make([]byte, 0, 128)
	h = be.AppendUint32(h, uint32(size))
	h = append(h, 0, 0, 0, 0)         // preferred CMM
	h = append(h, 2, 0x10, 0, 0)      // version 2.1
	h = append(h, "abst"...)          // abstract class
	h = append(h, "Lab "...)          // data colour space
	h = append(h, "Lab "...)          // PCS
	h = append(h, 7, 234, 0, 1, 0, 1) // 2026-01-01
	h = append(h, 0, 0, 0, 0, 0, 0)   //
	h = append(h, "acsp"...)          //
	h = append(h, make([]byte, 4+4+4+4+8+4)...)
	h = s15(s15(s15(h, 0.9642), 1.0), 0.8249) // D50 illuminant
	h = append(h, make([]byte, 128-len(h))...)
	out := append(h, table...)
	return append(out, data...)
}
