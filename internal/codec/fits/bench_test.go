package fits_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec/fits"
)

func BenchmarkDecodeConfig(b *testing.B) {
	data, err := readBench("mef-i32.fits")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := fits.DecodeConfig(data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecode(b *testing.B) {
	for _, name := range []string{"f32-nan.fits", "rice-f32.fits.fz"} {
		b.Run(name, func(b *testing.B) {
			data, err := readBench(name)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := fits.Decode(data, fits.Options{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func readBench(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join("..", "..", "..", "testdata", "fixtures", "fits", name))
}
