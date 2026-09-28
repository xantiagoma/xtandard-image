// Block decoders for BC1-BC5 and BC7 (D3D11 functional specification,
// "Block Compression"). The integer arithmetic of BC1-BC3 and the BC7 bit
// layout follow bcdec (https://github.com/iOrange/bcdec), Copyright (c)
// 2022 Sergii Kudlai, MIT License (see /NOTICE); rewritten in Go.

package dds

import "encoding/binary"

// Every decoder fills px with the 16 pixels of one 4x4 block, row-major, as
// R, G, B, A.

// colorBlock decodes the 8-byte BC1 colour block (two RGB 5:6:5 endpoints
// and 2-bit indices). Without opaque, c0 <= c1 selects the three-colour mode
// with transparent black at index 3 (BC1 only; BC2 and BC3 always use four
// colours).
func colorBlock(b []byte, px *[16][4]uint8, opaque bool) {
	le := binary.LittleEndian
	c0, c1 := uint32(le.Uint16(b)), uint32(le.Uint16(b[2:]))
	r0, g0, b0 := c0>>11&0x1F, c0>>5&0x3F, c0&0x1F
	r1, g1, b1 := c1>>11&0x1F, c1>>5&0x3F, c1&0x1F
	var ref [4][4]uint8
	ref[0] = [4]uint8{uint8((r0*527 + 23) >> 6), uint8((g0*259 + 33) >> 6), uint8((b0*527 + 23) >> 6), 255}
	ref[1] = [4]uint8{uint8((r1*527 + 23) >> 6), uint8((g1*259 + 33) >> 6), uint8((b1*527 + 23) >> 6), 255}
	if c0 > c1 || opaque {
		ref[2] = [4]uint8{
			uint8(((2*r0+r1)*351 + 61) >> 7),
			uint8(((2*g0+g1)*2763 + 1039) >> 11),
			uint8(((2*b0+b1)*351 + 61) >> 7), 255,
		}
		ref[3] = [4]uint8{
			uint8(((r0+2*r1)*351 + 61) >> 7),
			uint8(((g0+2*g1)*2763 + 1039) >> 11),
			uint8(((b0+2*b1)*351 + 61) >> 7), 255,
		}
	} else {
		ref[2] = [4]uint8{
			uint8(((r0+r1)*1053 + 125) >> 8),
			uint8(((g0+g1)*4145 + 1019) >> 11),
			uint8(((b0+b1)*1053 + 125) >> 8), 255,
		}
		// ref[3] stays transparent black.
	}
	idx := le.Uint32(b[4:])
	for i := range px {
		px[i] = ref[idx>>(2*i)&3]
	}
}

func decodeBC1(b []byte, px *[16][4]uint8, opaque bool) { colorBlock(b, px, opaque) }

// decodeBC2 decodes BC2 (DXT3): 4-bit explicit alpha, then a colour block.
func decodeBC2(b []byte, px *[16][4]uint8) {
	colorBlock(b[8:], px, true)
	a := binary.LittleEndian.Uint64(b)
	for i := range px {
		px[i][3] = uint8(a>>(4*i)&0xF) * 17
	}
}

// decodeBC3 decodes BC3 (DXT5): an interpolated alpha block, then a colour
// block.
func decodeBC3(b []byte, px *[16][4]uint8) {
	colorBlock(b[8:], px, true)
	var a [16]uint8
	alphaBlock(b, &a)
	for i := range px {
		px[i][3] = a[i]
	}
}

// alphaBlock decodes an unsigned BC3 alpha / BC4 block: two 8-bit endpoints
// and 3-bit indices.
func alphaBlock(b []byte, out *[16]uint8) {
	v := binary.LittleEndian.Uint64(b)
	a0, a1 := uint32(v&0xFF), uint32(v>>8&0xFF)
	var pal [8]uint8
	pal[0], pal[1] = uint8(a0), uint8(a1)
	if a0 > a1 {
		for i := uint32(1); i <= 6; i++ {
			pal[i+1] = uint8(((7-i)*a0 + i*a1) / 7) //nolint:gosec // G115: a weighted mean of two bytes
		}
	} else {
		for i := uint32(1); i <= 4; i++ {
			pal[i+1] = uint8(((5-i)*a0 + i*a1) / 5) //nolint:gosec // G115: a weighted mean of two bytes
		}
		pal[6], pal[7] = 0, 255
	}
	idx := v >> 16
	for i := range out {
		out[i] = pal[idx>>(3*i)&7]
	}
}

// signedAlphaBlock decodes a BC4/BC5 SNORM block and maps [-1, 1] to
// [0, 255].
func signedAlphaBlock(b []byte, out *[16]uint8) {
	v := binary.LittleEndian.Uint64(b)
	// The endpoints are signed bytes; -128 means -127.
	a0 := max(int32(int8(v&0xFF)), -127)    //nolint:gosec // G115: two's complement reinterpretation
	a1 := max(int32(int8(v>>8&0xFF)), -127) //nolint:gosec // G115: two's complement reinterpretation
	var pal [8]int32
	pal[0], pal[1] = a0, a1
	if a0 > a1 {
		for i := int32(1); i <= 6; i++ {
			pal[i+1] = roundDiv((7-i)*a0+i*a1, 7)
		}
	} else {
		for i := int32(1); i <= 4; i++ {
			pal[i+1] = roundDiv((5-i)*a0+i*a1, 5)
		}
		pal[6], pal[7] = -127, 127
	}
	idx := v >> 16
	for i := range out {
		s := pal[idx>>(3*i)&7]
		out[i] = uint8(((s+127)*255 + 127) / 254) //nolint:gosec // G115: s is in [-127, 127]
	}
}

// roundDiv divides rounding half away from zero.
func roundDiv(n, d int32) int32 {
	if n < 0 {
		return -((-n + d/2) / d)
	}
	return (n + d/2) / d
}

func channelBlock(b []byte, out *[16]uint8, signed bool) {
	if signed {
		signedAlphaBlock(b, out)
		return
	}
	alphaBlock(b, out)
}

// decodeBC4 decodes a one-channel BC4 block as grey.
func decodeBC4(b []byte, px *[16][4]uint8, signed bool) {
	var r [16]uint8
	channelBlock(b, &r, signed)
	for i := range px {
		px[i] = [4]uint8{r[i], r[i], r[i], 255}
	}
}

// decodeBC5 decodes a two-channel BC5 block as red and green. Blue is the
// missing channel's 0: 0 for UNORM, 128 (the middle of [-1, 1]) for SNORM,
// which shows a tangent-space normal map the usual way.
func decodeBC5(b []byte, px *[16][4]uint8, signed bool) {
	var r, g [16]uint8
	channelBlock(b, &r, signed)
	channelBlock(b[8:], &g, signed)
	blue := uint8(0)
	if signed {
		blue = 128
	}
	for i := range px {
		px[i] = [4]uint8{r[i], g[i], blue, 255}
	}
}

// bits reads a 128-bit block least significant bit first.
type bits struct{ lo, hi uint64 }

func newBits(b []byte) bits {
	le := binary.LittleEndian
	return bits{lo: le.Uint64(b), hi: le.Uint64(b[8:])}
}

// read returns the next n (<= 32) bits.
func (s *bits) read(n uint) uint32 {
	if n == 0 {
		return 0
	}
	v := uint32(s.lo & (1<<n - 1)) //nolint:gosec // G115: n <= 32 bits
	s.lo = s.lo>>n | s.hi<<(64-n)
	s.hi >>= n
	return v
}

// Interpolation weights of 2-, 3- and 4-bit indices (BC6H and BC7).
var (
	weights2 = [...]uint32{0, 21, 43, 64}
	weights3 = [...]uint32{0, 9, 18, 27, 37, 46, 55, 64}
	weights4 = [...]uint32{0, 4, 9, 13, 17, 21, 26, 30, 34, 38, 43, 47, 51, 55, 60, 64}
)

func weightsFor(n uint) []uint32 {
	switch n {
	case 2:
		return weights2[:]
	case 3:
		return weights3[:]
	default:
		return weights4[:]
	}
}

// interp blends two 8-bit endpoints with a weight in [0, 64].
func interp(a, b, w uint32) uint8 {
	return uint8((a*(64-w) + b*w + 32) >> 6) //nolint:gosec // G115: a weighted mean of two bytes
}

// bc7Mode describes one of the eight BC7 modes.
type bc7Mode struct {
	subsets    int
	partBits   uint // partition selector bits
	rotBits    uint // rotation bits
	selBits    uint // index selection bits
	colorBits  uint // bits per colour channel of an endpoint
	alphaBits  uint // bits of an endpoint's alpha (0: opaque)
	pbits      int  // 0, 1 (shared per subset) or 2 (one per endpoint)
	indexBits  uint
	index2Bits uint
}

var bc7Modes = [8]bc7Mode{
	{subsets: 3, partBits: 4, colorBits: 4, pbits: 2, indexBits: 3},
	{subsets: 2, partBits: 6, colorBits: 6, pbits: 1, indexBits: 3},
	{subsets: 3, partBits: 6, colorBits: 5, indexBits: 2},
	{subsets: 2, partBits: 6, colorBits: 7, pbits: 2, indexBits: 2},
	{subsets: 1, rotBits: 2, selBits: 1, colorBits: 5, alphaBits: 6, indexBits: 2, index2Bits: 3},
	{subsets: 1, rotBits: 2, colorBits: 7, alphaBits: 8, indexBits: 2, index2Bits: 2},
	{subsets: 1, colorBits: 7, alphaBits: 7, pbits: 2, indexBits: 4},
	{subsets: 2, partBits: 6, colorBits: 5, alphaBits: 5, pbits: 2, indexBits: 2},
}

// subsetOf returns the subset of pixel i and whether it is an anchor
// (fix-up) pixel, whose index is stored with one bit less.
func subsetOf(subsets, part, i int) (int, bool) {
	switch subsets {
	case 2:
		s := int(bc7Partitions2[part][i] - '0')
		return s, i == 0 || i == int(bc7Anchors2[part])
	case 3:
		s := int(bc7Partitions3[part][i] - '0')
		return s, i == 0 || i == int(bc7Anchors3[part][0]) || i == int(bc7Anchors3[part][1])
	default:
		return 0, i == 0
	}
}

// decodeBC7 decodes a BC7 block. Reserved mode bytes (0x00) decode to
// transparent black.
func decodeBC7(b []byte, px *[16][4]uint8) {
	s := newBits(b)
	mode := 0
	for mode < 8 && s.read(1) == 0 {
		mode++
	}
	if mode == 8 {
		*px = [16][4]uint8{}
		return
	}
	m := &bc7Modes[mode]
	part := int(s.read(m.partBits))
	rot := s.read(m.rotBits)
	sel := s.read(m.selBits)
	n := m.subsets * 2
	var ep [6][4]uint32
	for c := range 3 {
		for e := range n {
			ep[e][c] = s.read(m.colorBits)
		}
	}
	if m.alphaBits > 0 {
		for e := range n {
			ep[e][3] = s.read(m.alphaBits)
		}
	}
	cb, ab := m.colorBits, m.alphaBits
	switch m.pbits {
	case 1: // one p-bit per subset, shared by its two endpoints
		for sub := range m.subsets {
			p := s.read(1)
			for c := range 3 {
				ep[2*sub][c] = ep[2*sub][c]<<1 | p
				ep[2*sub+1][c] = ep[2*sub+1][c]<<1 | p
			}
		}
		cb++
	case 2:
		for e := range n {
			p := s.read(1)
			for c := range 4 {
				ep[e][c] = ep[e][c]<<1 | p
			}
		}
		cb++
		if ab > 0 {
			ab++
		}
	default:
	}
	for e := range n {
		for c := range 3 {
			ep[e][c] = expand(ep[e][c], cb)
		}
		if ab > 0 {
			ep[e][3] = expand(ep[e][3], ab)
		} else {
			ep[e][3] = 255
		}
	}
	var idx, idx2 [16]uint32
	for i := range 16 {
		_, anchor := subsetOf(m.subsets, part, i)
		nb := m.indexBits
		if anchor {
			nb--
		}
		idx[i] = s.read(nb)
	}
	if m.index2Bits > 0 {
		for i := range 16 {
			nb := m.index2Bits
			if i == 0 {
				nb--
			}
			idx2[i] = s.read(nb)
		}
	}
	w1, w2 := weightsFor(m.indexBits), weightsFor(m.index2Bits)
	for i := range 16 {
		sub, _ := subsetOf(m.subsets, part, i)
		e0, e1 := &ep[2*sub], &ep[2*sub+1]
		cw, aw := w1[idx[i]], w1[idx[i]]
		if m.index2Bits > 0 {
			if sel == 0 {
				aw = w2[idx2[i]]
			} else {
				cw, aw = w2[idx2[i]], w1[idx[i]]
			}
		}
		p := [4]uint8{interp(e0[0], e1[0], cw), interp(e0[1], e1[1], cw), interp(e0[2], e1[2], cw), interp(e0[3], e1[3], aw)}
		switch rot {
		case 1:
			p[0], p[3] = p[3], p[0]
		case 2:
			p[1], p[3] = p[3], p[1]
		case 3:
			p[2], p[3] = p[3], p[2]
		}
		px[i] = p
	}
}

// expand widens an n-bit value to 8 bits by replicating its high bits.
func expand(v uint32, n uint) uint32 {
	v <<= 8 - n
	return v | v>>n
}
