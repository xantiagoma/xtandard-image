// BC6H block decoder (D3D11 functional specification, "BC6H Format"). The
// per-mode bit layouts were transcribed from bcdec
// (https://github.com/iOrange/bcdec), Copyright (c) 2022 Sergii Kudlai, MIT
// License (see /NOTICE), into the table below; the decoder is written in Go.

package dds

import "math"

// Endpoint fields of a BC6H block: channel (r, g, b) × endpoint (w, x, y,
// z), and the partition selector d.
const (
	rw = iota
	rx
	ry
	rz
	gw
	gx
	gy
	gz
	bw
	bx
	by
	bz
	pd
)

// bc6Run is a run of n bits of a field, stored at bit shift of the field;
// rev marks the modes 12 and 13 fields stored most significant bit first.
type bc6Run struct {
	field, shift, n uint8
	rev             bool
}

// bc6Mode describes one of the 14 BC6H modes.
type bc6Mode struct {
	regions     int
	transformed bool     // x, y, z are deltas from w
	bits        [4]uint8 // endpoint precision; delta bits of r, g, b
	runs        []bc6Run
}

// bc6ModeIndex maps the 5-bit mode field (2 bits for modes 0 and 1) to an
// index into bc6Modes; -1 is reserved.
func bc6ModeIndex(code uint32) int {
	switch code {
	case 0b00010:
		return 2
	case 0b00110:
		return 3
	case 0b01010:
		return 4
	case 0b01110:
		return 5
	case 0b10010:
		return 6
	case 0b10110:
		return 7
	case 0b11010:
		return 8
	case 0b11110:
		return 9
	case 0b00011:
		return 10
	case 0b00111:
		return 11
	case 0b01011:
		return 12
	case 0b01111:
		return 13
	default:
		return -1
	}
}

var bc6Modes = [14]bc6Mode{
	{2, true, [4]uint8{10, 5, 5, 5}, []bc6Run{
		{gy, 4, 1, false},
		{by, 4, 1, false},
		{bz, 4, 1, false},
		{rw, 0, 10, false},
		{gw, 0, 10, false},
		{bw, 0, 10, false},
		{rx, 0, 5, false},
		{gz, 4, 1, false},
		{gy, 0, 4, false},
		{gx, 0, 5, false},
		{bz, 0, 1, false},
		{gz, 0, 4, false},
		{bx, 0, 5, false},
		{bz, 1, 1, false},
		{by, 0, 4, false},
		{ry, 0, 5, false},
		{bz, 2, 1, false},
		{rz, 0, 5, false},
		{bz, 3, 1, false},
		{pd, 0, 5, false},
	}},
	{2, true, [4]uint8{7, 6, 6, 6}, []bc6Run{
		{gy, 5, 1, false},
		{gz, 4, 1, false},
		{gz, 5, 1, false},
		{rw, 0, 7, false},
		{bz, 0, 1, false},
		{bz, 1, 1, false},
		{by, 4, 1, false},
		{gw, 0, 7, false},
		{by, 5, 1, false},
		{bz, 2, 1, false},
		{gy, 4, 1, false},
		{bw, 0, 7, false},
		{bz, 3, 1, false},
		{bz, 5, 1, false},
		{bz, 4, 1, false},
		{rx, 0, 6, false},
		{gy, 0, 4, false},
		{gx, 0, 6, false},
		{gz, 0, 4, false},
		{bx, 0, 6, false},
		{by, 0, 4, false},
		{ry, 0, 6, false},
		{rz, 0, 6, false},
		{pd, 0, 5, false},
	}},
	{2, true, [4]uint8{11, 5, 4, 4}, []bc6Run{
		{rw, 0, 10, false},
		{gw, 0, 10, false},
		{bw, 0, 10, false},
		{rx, 0, 5, false},
		{rw, 10, 1, false},
		{gy, 0, 4, false},
		{gx, 0, 4, false},
		{gw, 10, 1, false},
		{bz, 0, 1, false},
		{gz, 0, 4, false},
		{bx, 0, 4, false},
		{bw, 10, 1, false},
		{bz, 1, 1, false},
		{by, 0, 4, false},
		{ry, 0, 5, false},
		{bz, 2, 1, false},
		{rz, 0, 5, false},
		{bz, 3, 1, false},
		{pd, 0, 5, false},
	}},
	{2, true, [4]uint8{11, 4, 5, 4}, []bc6Run{
		{rw, 0, 10, false},
		{gw, 0, 10, false},
		{bw, 0, 10, false},
		{rx, 0, 4, false},
		{rw, 10, 1, false},
		{gz, 4, 1, false},
		{gy, 0, 4, false},
		{gx, 0, 5, false},
		{gw, 10, 1, false},
		{gz, 0, 4, false},
		{bx, 0, 4, false},
		{bw, 10, 1, false},
		{bz, 1, 1, false},
		{by, 0, 4, false},
		{ry, 0, 4, false},
		{bz, 0, 1, false},
		{bz, 2, 1, false},
		{rz, 0, 4, false},
		{gy, 4, 1, false},
		{bz, 3, 1, false},
		{pd, 0, 5, false},
	}},
	{2, true, [4]uint8{11, 4, 4, 5}, []bc6Run{
		{rw, 0, 10, false},
		{gw, 0, 10, false},
		{bw, 0, 10, false},
		{rx, 0, 4, false},
		{rw, 10, 1, false},
		{by, 4, 1, false},
		{gy, 0, 4, false},
		{gx, 0, 4, false},
		{gw, 10, 1, false},
		{bz, 0, 1, false},
		{gz, 0, 4, false},
		{bx, 0, 5, false},
		{bw, 10, 1, false},
		{by, 0, 4, false},
		{ry, 0, 4, false},
		{bz, 1, 1, false},
		{bz, 2, 1, false},
		{rz, 0, 4, false},
		{bz, 4, 1, false},
		{bz, 3, 1, false},
		{pd, 0, 5, false},
	}},
	{2, true, [4]uint8{9, 5, 5, 5}, []bc6Run{
		{rw, 0, 9, false},
		{by, 4, 1, false},
		{gw, 0, 9, false},
		{gy, 4, 1, false},
		{bw, 0, 9, false},
		{bz, 4, 1, false},
		{rx, 0, 5, false},
		{gz, 4, 1, false},
		{gy, 0, 4, false},
		{gx, 0, 5, false},
		{bz, 0, 1, false},
		{gz, 0, 4, false},
		{bx, 0, 5, false},
		{bz, 1, 1, false},
		{by, 0, 4, false},
		{ry, 0, 5, false},
		{bz, 2, 1, false},
		{rz, 0, 5, false},
		{bz, 3, 1, false},
		{pd, 0, 5, false},
	}},
	{2, true, [4]uint8{8, 6, 5, 5}, []bc6Run{
		{rw, 0, 8, false},
		{gz, 4, 1, false},
		{by, 4, 1, false},
		{gw, 0, 8, false},
		{bz, 2, 1, false},
		{gy, 4, 1, false},
		{bw, 0, 8, false},
		{bz, 3, 1, false},
		{bz, 4, 1, false},
		{rx, 0, 6, false},
		{gy, 0, 4, false},
		{gx, 0, 5, false},
		{bz, 0, 1, false},
		{gz, 0, 4, false},
		{bx, 0, 5, false},
		{bz, 1, 1, false},
		{by, 0, 4, false},
		{ry, 0, 6, false},
		{rz, 0, 6, false},
		{pd, 0, 5, false},
	}},
	{2, true, [4]uint8{8, 5, 6, 5}, []bc6Run{
		{rw, 0, 8, false},
		{bz, 0, 1, false},
		{by, 4, 1, false},
		{gw, 0, 8, false},
		{gy, 5, 1, false},
		{gy, 4, 1, false},
		{bw, 0, 8, false},
		{gz, 5, 1, false},
		{bz, 4, 1, false},
		{rx, 0, 5, false},
		{gz, 4, 1, false},
		{gy, 0, 4, false},
		{gx, 0, 6, false},
		{gz, 0, 4, false},
		{bx, 0, 5, false},
		{bz, 1, 1, false},
		{by, 0, 4, false},
		{ry, 0, 5, false},
		{bz, 2, 1, false},
		{rz, 0, 5, false},
		{bz, 3, 1, false},
		{pd, 0, 5, false},
	}},
	{2, true, [4]uint8{8, 5, 5, 6}, []bc6Run{
		{rw, 0, 8, false},
		{bz, 1, 1, false},
		{by, 4, 1, false},
		{gw, 0, 8, false},
		{by, 5, 1, false},
		{gy, 4, 1, false},
		{bw, 0, 8, false},
		{bz, 5, 1, false},
		{bz, 4, 1, false},
		{rx, 0, 5, false},
		{gz, 4, 1, false},
		{gy, 0, 4, false},
		{gx, 0, 5, false},
		{bz, 0, 1, false},
		{gz, 0, 4, false},
		{bx, 0, 6, false},
		{by, 0, 4, false},
		{ry, 0, 5, false},
		{bz, 2, 1, false},
		{rz, 0, 5, false},
		{bz, 3, 1, false},
		{pd, 0, 5, false},
	}},
	{2, false, [4]uint8{6, 6, 6, 6}, []bc6Run{
		{rw, 0, 6, false},
		{gz, 4, 1, false},
		{bz, 0, 1, false},
		{bz, 1, 1, false},
		{by, 4, 1, false},
		{gw, 0, 6, false},
		{gy, 5, 1, false},
		{by, 5, 1, false},
		{bz, 2, 1, false},
		{gy, 4, 1, false},
		{bw, 0, 6, false},
		{gz, 5, 1, false},
		{bz, 3, 1, false},
		{bz, 5, 1, false},
		{bz, 4, 1, false},
		{rx, 0, 6, false},
		{gy, 0, 4, false},
		{gx, 0, 6, false},
		{gz, 0, 4, false},
		{bx, 0, 6, false},
		{by, 0, 4, false},
		{ry, 0, 6, false},
		{rz, 0, 6, false},
		{pd, 0, 5, false},
	}},
	{1, false, [4]uint8{10, 10, 10, 10}, []bc6Run{
		{rw, 0, 10, false},
		{gw, 0, 10, false},
		{bw, 0, 10, false},
		{rx, 0, 10, false},
		{gx, 0, 10, false},
		{bx, 0, 10, false},
	}},
	{1, true, [4]uint8{11, 9, 9, 9}, []bc6Run{
		{rw, 0, 10, false},
		{gw, 0, 10, false},
		{bw, 0, 10, false},
		{rx, 0, 9, false},
		{rw, 10, 1, false},
		{gx, 0, 9, false},
		{gw, 10, 1, false},
		{bx, 0, 9, false},
		{bw, 10, 1, false},
	}},
	{1, true, [4]uint8{12, 8, 8, 8}, []bc6Run{
		{rw, 0, 10, false},
		{gw, 0, 10, false},
		{bw, 0, 10, false},
		{rx, 0, 8, false},
		{rw, 10, 2, true},
		{gx, 0, 8, false},
		{gw, 10, 2, true},
		{bx, 0, 8, false},
		{bw, 10, 2, true},
	}},
	{1, true, [4]uint8{16, 4, 4, 4}, []bc6Run{
		{rw, 0, 10, false},
		{gw, 0, 10, false},
		{bw, 0, 10, false},
		{rx, 0, 4, false},
		{rw, 10, 6, true},
		{gx, 0, 4, false},
		{gw, 10, 6, true},
		{bx, 0, 4, false},
		{bw, 10, 6, true},
	}},
}

// reverse reverses the low n bits of v.
func reverse(v uint32, n uint8) uint32 {
	var r uint32
	for range n {
		r = r<<1 | v&1
		v >>= 1
	}
	return r
}

// signExtend sign-extends the low n bits of v.
func signExtend(v int32, n uint8) int32 {
	s := 32 - n
	return v << s >> s
}

// unquantize scales an endpoint of n bits to the 16-bit interpolation range.
func unquantize(v int32, n uint8, signed bool) int32 {
	if !signed {
		switch {
		case n >= 15, v == 0:
			return v
		case v == 1<<n-1:
			return 0xFFFF
		default:
			return (v<<16 + 0x8000) >> n
		}
	}
	if n >= 16 {
		return v
	}
	neg := v < 0
	if neg {
		v = -v
	}
	switch {
	case v == 0:
	case v >= 1<<(n-1)-1:
		v = 0x7FFF
	default:
		v = (v<<15 + 0x4000) >> (n - 1)
	}
	if neg {
		return -v
	}
	return v
}

// finish scales an interpolated value to half-float bits (the magnitude by
// 31/64 unsigned, 31/32 signed, sign in bit 15).
func finish(v int32, signed bool) uint16 {
	if !signed {
		return uint16(v * 31 >> 6) //nolint:gosec // G115: v is in [0, 0xFFFF]
	}
	if v < 0 {
		return 0x8000 | uint16(-v*31>>5) //nolint:gosec // G115: -v is in [0, 0x7FFF]
	}
	return uint16(v * 31 >> 5) //nolint:gosec // G115: v is in [0, 0x7FFF]
}

// decodeBC6H decodes a BC6H block to linear float RGB. Reserved modes
// decode to black.
func decodeBC6H(b []byte, px *[16][3]float32, signed bool) {
	s := newBits(b)
	code := s.read(2)
	mi := int(code)
	if code > 1 {
		mi = bc6ModeIndex(code | s.read(3)<<2)
	}
	if mi < 0 {
		*px = [16][3]float32{}
		return
	}
	m := &bc6Modes[mi]
	var f [13]uint32
	for _, r := range m.runs {
		v := s.read(uint(r.n))
		if r.rev {
			v = reverse(v, r.n)
		}
		f[r.field] |= v << r.shift
	}
	part := int(f[pd])
	n := 2 * m.regions
	var ep [3][4]int32
	for c := range 3 {
		for e := range n {
			ep[c][e] = int32(f[c*4+e]) //nolint:gosec // G115: at most 16 bits
		}
		wb := m.bits[0]
		if signed {
			ep[c][0] = signExtend(ep[c][0], wb)
		}
		if m.transformed || signed {
			for e := 1; e < n; e++ {
				ep[c][e] = signExtend(ep[c][e], m.bits[c+1])
			}
		}
		if m.transformed {
			mask := int32(1)<<wb - 1
			for e := 1; e < n; e++ {
				ep[c][e] = (ep[c][e] + ep[c][0]) & mask
				if signed {
					ep[c][e] = signExtend(ep[c][e], wb)
				}
			}
		}
		for e := range n {
			ep[c][e] = unquantize(ep[c][e], wb, signed)
		}
	}
	ib := uint(4)
	if m.regions == 2 {
		ib = 3
	}
	w := weightsFor(ib)
	for i := range 16 {
		sub, anchor := 0, i == 0
		if m.regions == 2 {
			sub, anchor = subsetOf(2, part, i)
		}
		nb := ib
		if anchor {
			nb--
		}
		wi := int32(w[s.read(nb)]) //nolint:gosec // G115: a weight in [0, 64]
		for c := range 3 {
			a, z := ep[c][2*sub], ep[c][2*sub+1]
			v := (a*(64-wi) + z*wi + 32) >> 6
			px[i][c] = halfToFloat(finish(v, signed))
		}
	}
}

// halfToFloat converts IEEE 754 binary16 bits to float32.
func halfToFloat(h uint16) float32 {
	sign := uint32(h>>15) << 31
	exp := uint32(h >> 10 & 0x1F)
	man := uint32(h & 0x3FF)
	switch exp {
	case 0:
		if man == 0 {
			return math.Float32frombits(sign)
		}
		// Subnormal: man × 2^-24.
		f := float32(man) / (1 << 24)
		if sign != 0 {
			f = -f
		}
		return f
	case 0x1F:
		return math.Float32frombits(sign | 0xFF<<23 | man<<13)
	default:
		return math.Float32frombits(sign | (exp+127-15)<<23 | man<<13)
	}
}

func float32frombits(v uint32) float32 { return math.Float32frombits(v) }
