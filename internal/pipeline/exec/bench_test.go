//go:build cgo

package exec_test

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// largeJPEG renders the baseline photo at 2048x1536 (a typical camera-ish
// upload) so that a 400 px thumbnail exercises shrink-on-load and resize.
func largeJPEG(b *testing.B) []byte {
	b.Helper()
	src := newBuffer(b, readFixture(b, jpegPhoto))
	defer src.Release()
	var out []byte
	err := vips.Run(b.Context(), func() error {
		var a vips.Arena
		defer a.Close()
		img, err := a.Add(vips.Load(b.Context(), src, nil))
		if err != nil {
			return err
		}
		if img, err = a.Add(img.Resize(b.Context(), 6.4, 0, vips.KernelLanczos3)); err != nil {
			return err
		}
		blob, err := img.SaveJPEG(b.Context(), 90)
		if err != nil {
			return err
		}
		defer blob.Release()
		out = append([]byte(nil), blob.Bytes()...)
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	return out
}

func benchRun(b *testing.B, data []byte, options string, out transform.Format) {
	b.Helper()
	e := newExecutor(b)
	j, buf := job(b, data, parse(b, options), out)
	defer buf.Release()
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		res, err := e.Run(b.Context(), j)
		if err != nil {
			b.Fatal(err)
		}
		res.Body.Release()
	}
}

// BenchmarkRun measures a full plan + execute + encode:
//
//	go test -run '^$' -bench . -count 10 -benchmem ./internal/pipeline/exec/ > new.txt
//	benchstat new.txt
func BenchmarkRun(b *testing.B) {
	b.Run("thumbnail400/jpeg-jpeg", func(b *testing.B) {
		benchRun(b, largeJPEG(b), "rs:fit:400:400", transform.FormatJPEG)
	})
	b.Run("thumbnail400/jpeg-webp", func(b *testing.B) {
		benchRun(b, largeJPEG(b), "rs:fit:400:400", transform.FormatWebP)
	})
	b.Run("animated/gif-fill", func(b *testing.B) {
		benchRun(b, readFixture(b, gifAnim), "rs:fill:48:48", transform.FormatGIF)
	})
}
