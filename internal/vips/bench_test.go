//go:build cgo

package vips_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// benchSource encodes a 2400×1600 synthetic photo-like JPEG (q90) once.
func benchSource(b *testing.B) []byte {
	b.Helper()
	const w, h = 2400, 1600
	var out []byte
	job(b, func(ctx context.Context, a *vips.Arena) error {
		img, err := a.Add(vips.NewFromMemory(ctx, pattern(w, h), w, h, 3, vips.BandFormatUchar))
		if err != nil {
			return err
		}
		o := vips.DefaultSaveOptions()
		o.JPEG.Quality = 90
		out, err = saveBytes(ctx, img, vips.FormatJPEG, &o)
		return err
	})
	return out
}

// BenchmarkThumbnailJPEG measures one full request-shaped job: thumbnail
// (shrink-on-load) of a 2400×1600 JPEG to three widths, then JPEG save.
//
//	go test -run '^$' -bench ThumbnailJPEG -count 10 ./internal/vips | benchstat -
func BenchmarkThumbnailJPEG(b *testing.B) {
	src := benchSource(b)
	for _, width := range []int{100, 400, 1200} {
		b.Run(fmt.Sprintf("w%d", width), func(b *testing.B) {
			buf := newBuffer(b, src)
			defer buf.Release()
			o := vips.DefaultSaveOptions()
			o.JPEG.Quality = 80
			o.JPEG.OptimizeCoding = true
			b.SetBytes(int64(len(src)))
			b.ReportAllocs()
			for b.Loop() {
				job(b, func(ctx context.Context, a *vips.Arena) error {
					img, err := a.Add(vips.Thumbnail(ctx, buf, &vips.ThumbnailOptions{Width: width}))
					if err != nil {
						return err
					}
					blob, err := img.Save(ctx, vips.FormatJPEG, &o)
					if err != nil {
						return err
					}
					blob.Release()
					return nil
				})
			}
		})
	}
}
