//go:build cgo

package encode_test

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec/encode"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// BenchmarkEncode measures one Encode of an 800 px wide photo (baseline.jpg
// upscaled 2.5×, rendered to memory once so only the encoder is timed) at the
// configured defaults. Results (benchstat, -count=10): testdata/bench.txt.
func BenchmarkEncode(b *testing.B) {
	e := newEncoder(b, nil)
	buf := newBuffer(b, readFixture(b, "jpeg/baseline.jpg"))
	for _, f := range []transform.Format{transform.FormatJPEG, transform.FormatWebP, transform.FormatAVIF} {
		b.Run(f.String(), func(b *testing.B) {
			ctx := b.Context()
			err := vips.Run(ctx, func() error {
				var a vips.Arena
				defer a.Close()
				img, err := a.Add(vips.Load(ctx, buf, nil))
				if err != nil {
					return err
				}
				if img, err = a.Add(img.Resize(ctx, 2.5, 0, vips.KernelLanczos3)); err != nil {
					return err
				}
				if img, err = a.Add(img.CopyMemory(ctx)); err != nil {
					return err
				}
				t := encode.Target{Format: f, Metadata: encode.Metadata{Strip: true, KeepCopyright: true, StripColorProfile: true}}
				b.ReportAllocs()
				for b.Loop() {
					res, err := e.Encode(ctx, img, &t)
					if err != nil {
						return err
					}
					b.SetBytes(int64(res.Blob.Len()))
					res.Blob.Release()
				}
				return nil
			})
			if err != nil {
				b.Fatal(err)
			}
		})
	}
}
