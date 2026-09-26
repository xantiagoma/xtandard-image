package imageinfo

import "testing"

// BenchmarkAnalyze is the cost of every pixel field on a MaxSide copy of
// a photo (the flower fitted in 512 px, upscaled so the histogram is
// photo-like).
func BenchmarkAnalyze(b *testing.B) {
	flower := loadPNG(b, "thumbhash/flower.png")
	m := resample(flower, 384, MaxSide)
	o := &Options{
		Palette: 16, Average: true, AverageIgnoreTransparent: true, Dominant: true, DominantBuildMissed: true,
		BlurhashX: 9, BlurhashY: 9, Thumbhash: true, PerceptualHash: true,
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Analyze(m, o); err != nil {
			b.Fatal(err)
		}
	}
}
