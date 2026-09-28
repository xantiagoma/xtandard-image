package fits

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "fixtures", "fits", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// sampleSource returns the samples of a fixture's image, compressed or not.
func sampleSource(t *testing.T, name string) (samples, int) {
	t.Helper()
	im, err := locate(readFixture(t, name))
	if err != nil {
		t.Fatal(err)
	}
	sc, err := im.h.scaling()
	if err != nil {
		t.Fatal(err)
	}
	if !im.Compressed {
		return newRawSamples(&im, &sc), im.Width * im.Height
	}
	s, err := decodeTiles(&im, &sc)
	if err != nil {
		t.Fatal(err)
	}
	return s, im.Width * im.Height
}

// TestQuantisedTilesMatchCfitsio compares the dequantised floats with
// funpack's output (testdata/gen/fits-fpack.sh): the dither sequence, its
// seed and the per-tile scales must match cfitsio exactly.
func TestQuantisedTilesMatchCfitsio(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"rice-f32", "gzip2-f32", "dither2-f32"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, n := sampleSource(t, name+".fits.fz")
			want, _ := sampleSource(t, name+".ref.fits")
			for i := range n {
				g, w := got(0, i), want(0, i)
				if math.IsNaN(w) != math.IsNaN(g) || (!math.IsNaN(w) && float32(g) != float32(w)) {
					t.Fatalf("sample %d = %v, want %v", i, g, w)
				}
			}
		})
	}
}

func TestMTF(t *testing.T) {
	t.Parallel()
	tests := []struct{ m, x, want float64 }{
		{0.25, 0, 0},
		{0.25, 1, 1},
		{0.25, 0.25, 0.5},
		{0.5, 0.3, 0.3}, // m = 0.5 is the identity
		{0.1, 0.1, 0.5},
		{0.25, -1, 0},
		{0.25, 2, 1},
	}
	for _, tc := range tests {
		if got := mtf(tc.m, tc.x); math.Abs(got-tc.want) > 1e-12 {
			t.Errorf("mtf(%v, %v) = %v, want %v", tc.m, tc.x, got, tc.want)
		}
	}
}

func TestMedian(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   []float64
		want float64
	}{
		{[]float64{3}, 3},
		{[]float64{3, 1, 2}, 2},
		{[]float64{4, 1, 3, 2}, 2.5},
		{[]float64{5, 5, 5, 5}, 5},
		{[]float64{9, 8, 7, 6, 5, 4, 3, 2, 1}, 5},
	}
	for _, tc := range tests {
		in := append([]float64(nil), tc.in...)
		if got := median(in); got != tc.want {
			t.Errorf("median(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
	// A large sorted input stays exact (and linear).
	big := make([]float64, 100001)
	for i := range big {
		big[i] = float64(i)
	}
	if got := median(big); got != 50000 {
		t.Errorf("median of 0..100000 = %v", got)
	}
}

func TestFitSTF(t *testing.T) {
	t.Parallel()
	// A dark background with noise and a few bright stars: the background
	// median lands at a quarter of full scale.
	const n = 10000
	vals := make([]float64, n)
	for i := range vals {
		vals[i] = 100 + float64(i%17) // background 100..116
		if i%997 == 0 {
			vals[i] = 5000 // stars
		}
	}
	src := func(_, i int) float64 { return vals[i] }
	s := fitSTF(src, n, 1)
	if s.flat || s.mid <= 0 || s.mid >= 0.5 {
		t.Fatalf("stf %+v: want a brightening midtones balance", s)
	}
	bg := make([]float64, 0, n)
	for i, v := range vals {
		if i%997 != 0 {
			bg = append(bg, float64(s.apply(v)))
		}
	}
	if m := median(bg); m < 55 || m > 75 {
		t.Errorf("background median drawn at %v, want about 64", m)
	}
	if got := s.apply(5000); got != 255 {
		t.Errorf("star drawn at %d, want 255", got)
	}
	if got := s.apply(math.NaN()); got != 0 {
		t.Errorf("NaN drawn at %d, want 0", got)
	}
}

func TestFitSTFDegenerate(t *testing.T) {
	t.Parallel()
	nan := func(_, _ int) float64 { return math.NaN() }
	if s := fitSTF(nan, 4, 1); s.apply(math.NaN()) != 0 {
		t.Error("all-NaN image not black")
	}
	flat := func(_, _ int) float64 { return 7 }
	if s := fitSTF(flat, 4, 1); s.apply(7) != 128 {
		t.Error("flat image not mid-grey")
	}
	// Mostly zeros: the median is the minimum, med − c0 is 0 and the
	// stretch stays linear.
	two := func(_, i int) float64 {
		if i%100 == 0 {
			return 1
		}
		return 0
	}
	s := fitSTF(two, 100, 1)
	if s.mid != 0.5 {
		t.Errorf("degenerate balance %v, want linear 0.5", s.mid)
	}
}

func TestRandoms(t *testing.T) {
	t.Parallel()
	r := randoms()
	// fits_init_randoms checks that the 10000th seed is 1043618065.
	if got := float64(r[nRandom-1]) * 2147483647; math.Abs(got-1043618065) > 256 {
		t.Errorf("last seed = %.0f, want 1043618065", got)
	}
}
