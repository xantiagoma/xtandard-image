package dds

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// testdata/bcdec/<format>.bin holds 288 pseudo-random blocks per format,
// each followed by its decoding by the reference decoder bcdec
// (https://github.com/iOrange/bcdec, v0.985, MIT): RGBA8 for BC1-BC3 and
// BC7, one or two 8-bit (BC4/BC5 unsigned) or signed 8-bit (BC4/BC5 SNORM,
// BCDEC_BC4BC5_PRECISE) channels, and half-float RGB for BC6H. The blocks
// cover every BC7 mode and every BC6H mode code, reserved ones included,
// and BC1's three-colour mode. testdata/bcdec/gen/main.c made them.
const refBlocks = 288

type refCase struct {
	file       string
	blockBytes int
	outBytes   int
}

func readRef(tb testing.TB, rc refCase) [][2][]byte {
	tb.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "bcdec", rc.file))
	if err != nil {
		tb.Fatal(err)
	}
	rec := rc.blockBytes + rc.outBytes
	if len(b) != refBlocks*rec {
		tb.Fatalf("%s: %d bytes, want %d", rc.file, len(b), refBlocks*rec)
	}
	out := make([][2][]byte, refBlocks)
	for i := range out {
		r := b[i*rec:]
		out[i] = [2][]byte{r[:rc.blockBytes], r[rc.blockBytes:rec]}
	}
	return out
}

func TestBlocksMatchReference8(t *testing.T) {
	t.Parallel()
	cases := []struct {
		refCase
		decode func(b []byte, px *[16][4]uint8)
	}{
		{refCase{"bc1.bin", 8, 64}, func(b []byte, px *[16][4]uint8) { decodeBC1(b, px, false) }},
		{refCase{"bc2.bin", 16, 64}, decodeBC2},
		{refCase{"bc3.bin", 16, 64}, decodeBC3},
		{refCase{"bc7.bin", 16, 64}, decodeBC7},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			for i, r := range readRef(t, tc.refCase) {
				var px [16][4]uint8
				tc.decode(r[0], &px)
				for p := range 16 {
					want := [4]uint8(r[1][4*p : 4*p+4])
					if px[p] != want {
						t.Fatalf("block %d (% x) pixel %d = %v, want %v", i, r[0], p, px[p], want)
					}
				}
			}
		})
	}
}

func TestBC4BC5MatchReference(t *testing.T) {
	t.Parallel()
	cases := []struct {
		refCase
		channels int
		signed   bool
	}{
		{refCase{"bc4u.bin", 8, 16}, 1, false},
		{refCase{"bc4s.bin", 8, 16}, 1, true},
		{refCase{"bc5u.bin", 16, 32}, 2, false},
		{refCase{"bc5s.bin", 16, 32}, 2, true},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			for i, r := range readRef(t, tc.refCase) {
				var px [16][4]uint8
				if tc.channels == 1 {
					decodeBC4(r[0], &px, tc.signed)
				} else {
					decodeBC5(r[0], &px, tc.signed)
				}
				for p := range 16 {
					for c := range tc.channels {
						want := int(r[1][p*tc.channels+c])
						if tc.signed {
							// SNORM [-127, 127] shown as [0, 255].
							want = ((int(int8(want))+127)*255 + 127) / 254 //nolint:gosec // G115: a signed byte
						}
						if d := int(px[p][c]) - want; d < -1 || d > 1 {
							t.Fatalf("block %d pixel %d channel %d = %d, want %d ±1", i, p, c, px[p][c], want)
						}
					}
				}
			}
		})
	}
}

func TestBC6HMatchesReference(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		file   string
		signed bool
	}{{"bc6hu.bin", false}, {"bc6hs.bin", true}} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			for i, r := range readRef(t, refCase{tc.file, 16, 96}) {
				var px [16][3]float32
				decodeBC6H(r[0], &px, tc.signed)
				for p := range 16 {
					for c := range 3 {
						want := halfToFloat(binary.LittleEndian.Uint16(r[1][2*(p*3+c):]))
						if px[p][c] != want {
							t.Fatalf("block %d (% x) pixel %d channel %d = %g, want %g", i, r[0], p, c, px[p][c], want)
						}
					}
				}
			}
		})
	}
}

// TestBC6HModeTable checks that every mode's fields fill the block exactly:
// 82 bits of header for two regions (46 index bits), 65 for one (63).
func TestBC6HModeTable(t *testing.T) {
	t.Parallel()
	for i, m := range bc6Modes {
		n := 5
		if i < 2 {
			n = 2
		}
		for _, r := range m.runs {
			n += int(r.n)
		}
		want := 82
		if m.regions == 1 {
			want = 65
		}
		if n != want {
			t.Errorf("mode %d: %d header bits, want %d", i, n, want)
		}
	}
}

// FuzzBlocks runs every block decoder on arbitrary 16-byte blocks: none may
// panic, and BC6H must return finite halves.
func FuzzBlocks(f *testing.F) {
	for _, rc := range []refCase{{"bc6hu.bin", 16, 96}, {"bc6hs.bin", 16, 96}, {"bc7.bin", 16, 64}} {
		for i, r := range readRef(f, rc) {
			if i%32 == 0 {
				f.Add(r[0])
			}
		}
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) < 16 {
			return
		}
		b = b[:16]
		var px [16][4]uint8
		decodeBC1(b, &px, false)
		decodeBC2(b, &px)
		decodeBC3(b, &px)
		decodeBC4(b, &px, true)
		decodeBC5(b, &px, true)
		decodeBC7(b, &px)
		var hp [16][3]float32
		for _, signed := range []bool{false, true} {
			decodeBC6H(b, &hp, signed)
			for _, p := range hp {
				for _, v := range p {
					if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) || math.Abs(float64(v)) > 65504 {
						t.Fatalf("BC6H (signed %v) gave %g for % x", signed, v, b)
					}
				}
			}
		}
	})
}

func TestHalfToFloat(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		h    uint16
		want float32
	}{
		{0x0000, 0}, {0x3C00, 1}, {0xC000, -2}, {0x7BFF, 65504}, {0x0001, 5.9604645e-08}, {0x3555, 0.33325195},
	} {
		if got := halfToFloat(tc.h); got != tc.want {
			t.Errorf("halfToFloat(%#04x) = %g, want %g", tc.h, got, tc.want)
		}
	}
}
