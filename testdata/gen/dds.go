package main

import (
	"encoding/binary"
	"fmt"
	"image"
	"math"
	"os"
)

// ---- DDS (WP-105) -----------------------------------------------------------
//
// Minimal block-compression encoders for the DX10 DDS fixtures: BC1 and BC4
// with bounding-box endpoints, BC7 mode 6 and BC6H mode 11 (one region,
// 10-bit endpoints, 4-bit indices). They favour simplicity over quality;
// the decoders under test are internal/codec/dds.

// DXGI formats written.
const (
	dxgiBC1UNorm = 71
	dxgiBC4UNorm = 80
	dxgiBC6HUF16 = 95
	dxgiBC7UNorm = 98
)

// writeDDS writes a DX10 DDS file with one surface (no mipmaps).
func writeDDS(out string, w, h int, dxgi uint32, data []byte) error {
	le := binary.LittleEndian
	b := make([]byte, 4+124+20, 4+124+20+len(data))
	copy(b, "DDS ")
	le.PutUint32(b[4:], 124)
	le.PutUint32(b[8:], 0x1|0x2|0x4|0x1000|0x80000) // CAPS, HEIGHT, WIDTH, PIXELFORMAT, LINEARSIZE
	le.PutUint32(b[12:], uint32(h))
	le.PutUint32(b[16:], uint32(w))
	le.PutUint32(b[20:], uint32(len(data)))
	le.PutUint32(b[28:], 1)       // mipmaps
	le.PutUint32(b[76:], 32)      // DDS_PIXELFORMAT size
	le.PutUint32(b[80:], 0x4)     // DDPF_FOURCC
	copy(b[84:], "DX10")          //
	le.PutUint32(b[108:], 0x1000) // DDSCAPS_TEXTURE
	le.PutUint32(b[128:], dxgi)
	le.PutUint32(b[132:], 3) // DDS_DIMENSION_TEXTURE2D
	le.PutUint32(b[140:], 1) // array size
	return os.WriteFile(out, append(b, data...), 0o644)
}

// blocks calls enc for every 4x4 block of a w×h image (edge blocks repeat
// the last row and column) and concatenates the results.
func blocks(w, h int, enc func(x0, y0 int, px func(i int) (x, y int)) []byte) []byte {
	var out []byte
	for by := 0; by < h; by += 4 {
		for bx := 0; bx < w; bx += 4 {
			px := func(i int) (int, int) { return min(bx+i%4, w-1), min(by+i/4, h-1) }
			out = append(out, enc(bx, by, px)...)
		}
	}
	return out
}

// bitWriter packs fields least significant bit first into a 128-bit block.
type bitWriter struct {
	b [16]byte
	n int
}

func (bw *bitWriter) put(v uint32, n int) {
	for i := range n {
		if v>>i&1 != 0 {
			bw.b[bw.n/8] |= 1 << (bw.n % 8)
		}
		bw.n++
	}
}

func rgb565(c [3]int) uint16 {
	return uint16(c[0]>>3)<<11 | uint16(c[1]>>2)<<5 | uint16(c[2]>>3)
}

func from565(v uint16) [3]int {
	r, g, b := int(v>>11&31), int(v>>5&63), int(v&31)
	return [3]int{r<<3 | r>>2, g<<2 | g>>4, b<<3 | b>>2}
}

// encodeBC1 encodes an opaque RGB image.
func encodeBC1(img *image.RGBA) []byte {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	return blocks(w, h, func(_, _ int, px func(int) (int, int)) []byte {
		var c [16][3]int
		lo, hi := [3]int{255, 255, 255}, [3]int{}
		for i := range 16 {
			p := img.RGBAAt(px(i))
			c[i] = [3]int{int(p.R), int(p.G), int(p.B)}
			for k := range 3 {
				lo[k], hi[k] = min(lo[k], c[i][k]), max(hi[k], c[i][k])
			}
		}
		c0, c1 := rgb565(hi), rgb565(lo)
		if c0 < c1 {
			c0, c1 = c1, c0
		}
		out := make([]byte, 8)
		binary.LittleEndian.PutUint16(out, c0)
		binary.LittleEndian.PutUint16(out[2:], c1)
		if c0 == c1 {
			return out // one colour: every index 0
		}
		e0, e1 := from565(c0), from565(c1)
		var pal [4][3]int
		for k := range 3 {
			pal[0][k], pal[1][k] = e0[k], e1[k]
			pal[2][k], pal[3][k] = (2*e0[k]+e1[k])/3, (e0[k]+2*e1[k])/3
		}
		var idx uint32
		for i := range 16 {
			best, bd := 0, math.MaxInt
			for j, p := range pal {
				d := 0
				for k := range 3 {
					d += (c[i][k] - p[k]) * (c[i][k] - p[k])
				}
				if d < bd {
					best, bd = j, d
				}
			}
			idx |= uint32(best) << (2 * i)
		}
		binary.LittleEndian.PutUint32(out[4:], idx)
		return out
	})
}

// encodeBC4 encodes the luminance of img (Rec. 601 weights).
func encodeBC4(img *image.RGBA) []byte {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	return blocks(w, h, func(_, _ int, px func(int) (int, int)) []byte {
		var l [16]int
		lo, hi := 255, 0
		for i := range 16 {
			p := img.RGBAAt(px(i))
			l[i] = (299*int(p.R) + 587*int(p.G) + 114*int(p.B) + 500) / 1000
			lo, hi = min(lo, l[i]), max(hi, l[i])
		}
		out := make([]byte, 8)
		out[0], out[1] = byte(hi), byte(lo)
		if hi == lo {
			return out
		}
		var pal [8]int
		pal[0], pal[1] = hi, lo
		for i := 1; i <= 6; i++ {
			pal[i+1] = ((7-i)*hi + i*lo) / 7
		}
		var idx uint64
		for i := range 16 {
			best, bd := 0, math.MaxInt
			for j, p := range pal {
				if d := (l[i] - p) * (l[i] - p); d < bd {
					best, bd = j, d
				}
			}
			idx |= uint64(best) << (3 * i)
		}
		for i := range 6 {
			out[2+i] = byte(idx >> (8 * i))
		}
		return out
	})
}

// encodeBC7 encodes an RGBA image with mode 6 only: one subset, RGBA
// endpoints of 7 bits plus a p-bit, 4-bit indices.
func encodeBC7(img *image.NRGBA) []byte {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	return blocks(w, h, func(_, _ int, px func(int) (int, int)) []byte {
		var c [16][4]float64
		lo, hi := [4]int{255, 255, 255, 255}, [4]int{}
		for i := range 16 {
			p := img.NRGBAAt(px(i))
			v := [4]int{int(p.R), int(p.G), int(p.B), int(p.A)}
			for k := range 4 {
				c[i][k] = float64(v[k])
				lo[k], hi[k] = min(lo[k], v[k]), max(hi[k], v[k])
			}
		}
		// Endpoint 0 rounds down (p-bit 0), endpoint 1 up (p-bit 1).
		var e [2][4]int
		for k := range 4 {
			e[0][k], e[1][k] = lo[k]>>1, hi[k]>>1
		}
		p := [2]uint32{0, 1}
		full := func(j, k int) float64 { return float64(e[j][k]<<1 | int(p[j])) }
		var idx [16]int
		var dd float64
		for k := range 4 {
			d := full(1, k) - full(0, k)
			dd += d * d
		}
		for i := range 16 {
			if dd == 0 {
				break
			}
			var t float64
			for k := range 4 {
				t += (c[i][k] - full(0, k)) * (full(1, k) - full(0, k))
			}
			idx[i] = int(math.Round(min(max(t/dd, 0), 1) * 15))
		}
		if idx[0] >= 8 { // the anchor index has no high bit: swap the endpoints
			e[0], e[1] = e[1], e[0]
			p[0], p[1] = p[1], p[0]
			for i := range idx {
				idx[i] = 15 - idx[i]
			}
		}
		var bw bitWriter
		bw.put(1<<6, 7) // mode 6
		for k := range 4 {
			bw.put(uint32(e[0][k]), 7)
			bw.put(uint32(e[1][k]), 7)
		}
		bw.put(p[0], 1)
		bw.put(p[1], 1)
		for i := range 16 {
			n := 4
			if i == 0 {
				n = 3
			}
			bw.put(uint32(idx[i]), n)
		}
		return bw.b[:]
	})
}

// floatToHalf converts a non-negative float to IEEE binary16 bits,
// saturating at the largest finite half.
func floatToHalf(f float64) uint16 {
	if !(f > 0) {
		return 0
	}
	if f >= 65504 {
		return 0x7BFF
	}
	e := math.Floor(math.Log2(f))
	if e < -14 { // subnormal
		return uint16(math.Round(f / math.Ldexp(1, -24)))
	}
	m := math.Round((f/math.Ldexp(1, int(e)) - 1) * 1024)
	if m == 1024 {
		m, e = 0, e+1
	}
	return uint16(int(e)+15)<<10 | uint16(m)
}

// bc6Unq is the decoder's 16-bit value of a 10-bit unsigned endpoint.
func bc6Unq(q int) int {
	switch q {
	case 0:
		return 0
	case 1023:
		return 0xFFFF
	default:
		return (q<<16 + 0x8000) >> 10
	}
}

// encodeBC6H encodes linear float RGB (bands 3 or 4, alpha ignored) with
// BC6H mode 11, unsigned. Values are interpolated in the half-float bit
// domain like the decoder: u = half × 64/31.
func encodeBC6H(pix []float32, w, h, bands int) []byte {
	return blocks(w, h, func(_, _ int, px func(int) (int, int)) []byte {
		var u [16][3]float64
		lo, hi := [3]float64{math.Inf(1), math.Inf(1), math.Inf(1)}, [3]float64{}
		for i := range 16 {
			x, y := px(i)
			for k := range 3 {
				u[i][k] = float64(floatToHalf(float64(pix[(y*w+x)*bands+k]))) * 64 / 31
				lo[k], hi[k] = min(lo[k], u[i][k]), max(hi[k], u[i][k])
			}
		}
		var e [2][3]int
		for k := range 3 {
			e[0][k] = int(math.Floor(lo[k] * 1023 / 65535))
			e[1][k] = min(int(math.Ceil(hi[k]*1023/65535)), 1023)
		}
		var idx [16]int
		var dd float64
		for k := range 3 {
			d := float64(bc6Unq(e[1][k]) - bc6Unq(e[0][k]))
			dd += d * d
		}
		for i := range 16 {
			if dd == 0 {
				break
			}
			var t float64
			for k := range 3 {
				t += (u[i][k] - float64(bc6Unq(e[0][k]))) * float64(bc6Unq(e[1][k])-bc6Unq(e[0][k]))
			}
			idx[i] = int(math.Round(min(max(t/dd, 0), 1) * 15))
		}
		if idx[0] >= 8 {
			e[0], e[1] = e[1], e[0]
			for i := range idx {
				idx[i] = 15 - idx[i]
			}
		}
		var bw bitWriter
		bw.put(0b11, 2) // mode code 0b00011: low two bits, then three zeros
		bw.put(0, 3)
		for j := range 2 {
			for k := range 3 {
				bw.put(uint32(e[j][k]), 10)
			}
		}
		for i := range 16 {
			n := 4
			if i == 0 {
				n = 3
			}
			bw.put(uint32(idx[i]), n)
		}
		return bw.b[:]
	})
}

// ddsFixture writes one of the DDS fixtures: dds-bc1, dds-bc4 and dds-bc7
// from the photo (BC7 with the alpha fade), dds-bc6h from the HDR scene.
func ddsFixture(kind string, w, h int, out string) error {
	switch kind {
	case "dds-bc1":
		return writeDDS(out, w, h, dxgiBC1UNorm, encodeBC1(photo(w, h)))
	case "dds-bc4":
		return writeDDS(out, w, h, dxgiBC4UNorm, encodeBC4(photo(w, h)))
	case "dds-bc7":
		return writeDDS(out, w, h, dxgiBC7UNorm, encodeBC7(withAlpha(photo(w, h))))
	case "dds-bc6h":
		return writeDDS(out, w, h, dxgiBC6HUF16, encodeBC6H(hdrImage(w, h, 3), w, h, 3))
	default:
		return fmt.Errorf("unknown DDS fixture %q", kind)
	}
}
