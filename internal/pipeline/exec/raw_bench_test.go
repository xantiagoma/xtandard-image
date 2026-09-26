//go:build cgo

package exec_test

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec/encode"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/exec"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/testutil/rawfixtures"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// BenchmarkRaw compares the decode paths of a camera RAW for the same
// output (plan + execute + encode, the source already in memory): the
// embedded JPEG preview, the half-size and the full demosaic.
//
//	go test -run '^$' -bench Raw -count 10 ./internal/pipeline/exec/ > raw.txt
//	benchstat raw.txt
func BenchmarkRaw(b *testing.B) {
	for _, idx := range []int{3, 4} { // sony-ilce-7s.arw (12 MP), pentax-k-50.dng (16 MP)
		f := rawfixtures.Files()[idx]
		for _, tc := range []struct {
			name    string
			options string
			ratio   float64
			noHalf  bool
			path    string
		}{
			{"preview", "rs:fit:1024:1024", 0, false, exec.RawPathPreview},
			{"demosaic-half", "rs:fit:1024:1024", 1000, false, exec.RawPathDemosaicHalf},
			{"demosaic", "rs:fit:1024:1024", 1000, true, exec.RawPathDemosaic},
		} {
			b.Run(f.Kind+"/"+tc.name, func(b *testing.B) {
				lim := testLimits
				lim.RawPreviewMinRatio, lim.DisableShrinkOnLoad = tc.ratio, tc.noHalf
				benchRaw(b, rawfixtures.Read(b, &f), tc.options, lim, tc.path)
			})
		}
	}
}

func benchRaw(b *testing.B, data []byte, options string, lim plan.Limits, path string) {
	b.Helper()
	ec := encode.DefaultConfig()
	enc, err := encode.New(&ec)
	if err != nil {
		b.Fatal(err)
	}
	e, err := exec.New(&exec.Config{Limits: lim}, enc)
	if err != nil {
		b.Fatal(err)
	}
	j, buf := job(b, data, parse(b, options), transform.FormatJPEG)
	defer buf.Release()
	for b.Loop() {
		res, err := e.Run(b.Context(), j)
		if err != nil {
			b.Fatal(err)
		}
		if res.RawPath != path {
			b.Fatalf("path %q, want %q", res.RawPath, path)
		}
		res.Body.Release()
	}
}
