package fits

import "math/bits"

// riceDecode decodes a Rice-coded tile of nx integers of bytepix bytes (1,
// 2 or 4) in blocks of nblock (fits_rdecomp, fits_rdecomp_short and
// fits_rdecomp_byte of the FITS tile-compression convention): the first
// value verbatim, then per block a code of fsbits bits selecting all-equal,
// verbatim or Rice-coded differences. Values are returned as int64: bytes
// unsigned, 16- and 32-bit values signed.
func riceDecode(c []byte, nx, bytepix, nblock int) ([]int64, error) {
	var fsbits, fsmax uint
	switch bytepix {
	case 1:
		fsbits, fsmax = 3, 6
	case 2:
		fsbits, fsmax = 4, 14
	case 4:
		fsbits, fsmax = 5, 25
	default:
		return nil, corruptf("rice: bad BYTEPIX %d", bytepix)
	}
	if nblock < 1 || nblock > 1<<16 {
		return nil, corruptf("rice: bad BLOCKSIZE %d", nblock)
	}
	bbits := uint(8 * bytepix)
	mask := uint64(1)<<bbits - 1
	if len(c) < bytepix+1 {
		return nil, corrupt("rice: truncated tile")
	}
	var last uint64
	for k := range bytepix {
		last = last<<8 | uint64(c[k])
	}
	pos := bytepix
	// next returns the next input byte; past the end it reports overrun.
	overrun := false
	next := func() uint64 {
		if pos >= len(c) {
			overrun = true
			return 0
		}
		v := c[pos]
		pos++
		return uint64(v)
	}
	out := make([]int64, nx)
	signed := func(v uint64) int64 {
		v &= mask
		switch bytepix {
		case 1:
			return int64(v & 0xFF)
		case 2:
			return s16(uint16(v)) //nolint:gosec // G115: masked to 16 bits
		default:
			return s32(uint32(v)) //nolint:gosec // G115: masked to 32 bits
		}
	}
	undo := func(diff uint64) uint64 {
		diff &= mask
		if diff&1 == 0 {
			return diff >> 1
		}
		return ^(diff >> 1) & mask
	}
	b := next()
	nbits := 8
	for i := 0; i < nx; {
		nbits -= int(fsbits)
		for nbits < 0 {
			b = b<<8 | next()
			nbits += 8
		}
		fs := int(b>>uint(nbits)) - 1 //nolint:gosec // G115: at most fsbits (5) bits
		b &= 1<<uint(nbits) - 1
		imax := min(i+nblock, nx)
		switch {
		case fs < 0: // all differences zero
			for ; i < imax; i++ {
				out[i] = signed(last)
			}
		case uint(fs) == fsmax: // verbatim differences
			for ; i < imax; i++ {
				k := int(bbits) - nbits
				diff := b << uint(k)
				for k -= 8; k >= 0; k -= 8 {
					b = next()
					diff |= b << uint(k)
				}
				if nbits > 0 {
					b = next()
					diff |= b >> uint(-k)
					b &= 1<<uint(nbits) - 1
				} else {
					b = 0
				}
				last = (undo(diff) + last) & mask
				out[i] = signed(last)
				if overrun {
					return nil, corrupt("rice: truncated tile")
				}
			}
		case uint(fs) > fsmax:
			return nil, corrupt("rice: bad block code")
		default: // Rice-coded differences
			for ; i < imax; i++ {
				for b == 0 {
					nbits += 8
					b = next()
					if overrun || nbits > 64*8 {
						return nil, corrupt("rice: truncated tile")
					}
				}
				nzero := nbits - bits.Len64(b)
				nbits -= nzero + 1
				b ^= 1 << uint(nbits)
				nbits -= fs
				for nbits < 0 {
					b = b<<8 | next()
					nbits += 8
				}
				diff := uint64(nzero)<<uint(fs) | b>>uint(nbits) //nolint:gosec // G115: nzero is not negative
				b &= 1<<uint(nbits) - 1
				last = (undo(diff) + last) & mask
				out[i] = signed(last)
			}
		}
		if overrun {
			return nil, corrupt("rice: truncated tile")
		}
	}
	return out, nil
}
