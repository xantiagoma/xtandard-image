package imageinfo

import (
	"math"
	"slices"
	"strconv"
)

// phashSize is the side of the greyscale image the DCT runs on; the hash
// keeps the lowest phashBits×phashBits frequencies.
const (
	phashSize = 32
	phashBits = 8
)

// PerceptualHash is the 64-bit DCT perceptual hash of m (imgproxy info
// `perceptual_hash`): m is area-averaged to 32×32 luma (Rec. 601
// weights; alpha ignored), transformed with a 2-D DCT-II, and bit 63−i is
// set when the i-th of the 8×8 lowest-frequency coefficients (row-major,
// DC included) is above their median. Similar images have hashes a small
// Hamming distance apart.
func PerceptualHash(m *Image) (uint64, error) {
	if err := m.Validate(); err != nil {
		return 0, err
	}
	opaque := *m
	opaque.Pix = slices.Clone(m.Pix)
	for i := 3; i < len(opaque.Pix); i += 4 {
		opaque.Pix[i] = 255 // alpha-weighted averaging would drop hidden colour
	}
	small := resample(&opaque, phashSize, phashSize)
	var px [phashSize][phashSize]float64
	for y := range phashSize {
		for x := range phashSize {
			o := small.Pix[(y*phashSize+x)*4:]
			px[y][x] = 0.299*float64(o[0]) + 0.587*float64(o[1]) + 0.114*float64(o[2])
		}
	}
	// Separable DCT-II, only the rows and columns the hash keeps.
	var basis [phashBits][phashSize]float64
	for k := range phashBits {
		for n := range phashSize {
			basis[k][n] = math.Cos(math.Pi / phashSize * (float64(n) + 0.5) * float64(k))
		}
	}
	var rows [phashSize][phashBits]float64 // DCT along x of every row
	for y := range phashSize {
		for k := range phashBits {
			s := 0.0
			for x := range phashSize {
				s += px[y][x] * basis[k][x]
			}
			rows[y][k] = s
		}
	}
	coef := make([]float64, 0, phashBits*phashBits)
	for ky := range phashBits {
		for kx := range phashBits {
			s := 0.0
			for y := range phashSize {
				s += rows[y][kx] * basis[ky][y]
			}
			coef = append(coef, s)
		}
	}
	sorted := slices.Clone(coef)
	slices.Sort(sorted)
	median := (sorted[len(sorted)/2-1] + sorted[len(sorted)/2]) / 2
	var hash uint64
	for i, c := range coef {
		if c > median {
			hash |= 1 << (63 - i)
		}
	}
	return hash, nil
}

// FormatPerceptualHash is imgproxy's rendering: 16 lower-case hex digits.
func FormatPerceptualHash(h uint64) string {
	s := strconv.FormatUint(h, 16)
	const zeros = "0000000000000000"
	return zeros[:16-len(s)] + s
}
