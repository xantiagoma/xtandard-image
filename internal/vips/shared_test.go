//go:build cgo

package vips_test

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// TestShared renders a watermark-like image once and reads it from several
// goroutines, each in its own Run, blending it over its own image. The
// shared image carries no metadata and outlives the Run that made it.
func TestShared(t *testing.T) {
	t.Parallel()
	src := newBuffer(t, readFixture(t, "jpeg/exif-orientation-6-icc.jpg")) // ICC and EXIF
	var s *vips.Shared
	job(t, func(ctx context.Context, a *vips.Arena) error {
		img, err := a.Add(vips.Load(ctx, src, &vips.LoadOptions{}))
		if err != nil {
			return err
		}
		if s, err = vips.Share(ctx, img); err != nil {
			return err
		}
		return nil
	})
	src.Release()
	t.Cleanup(s.Close)
	if s.Width() != 240 || s.Height() != 320 || s.Bands() != 3 || s.HasAlpha() {
		t.Fatalf("shared %dx%dx%d alpha=%v", s.Width(), s.Height(), s.Bands(), s.HasAlpha())
	}

	job(t, func(context.Context, *vips.Arena) error {
		img := s.Image()
		defer img.Close()
		header := []vips.MetaField{
			"width", "height", "bands", "format", "coding", "interpretation",
			"xoffset", "yoffset", "xres", "yres", "filename",
		}
		for _, f := range img.Fields() {
			if !slices.Contains(header, f) {
				t.Errorf("shared image has metadata field %q", f)
			}
		}
		return nil
	})

	base := newBuffer(t, readFixture(t, "jpeg/baseline.jpg"))
	t.Cleanup(base.Release)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				err := tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
					img, err := a.Add(vips.Load(ctx, base, &vips.LoadOptions{}))
					if err != nil {
						return err
					}
					mark := a.Track(s.Image())
					small, err := a.Add(mark.Resize(ctx, 0.5, 0, vips.KernelLanczos3))
					if err != nil {
						return err
					}
					out, err := a.Add(img.Composite2(ctx, small, vips.BlendOver, 10, 10))
					if err != nil {
						return err
					}
					_, err = saveBytes(ctx, out, vips.FormatPNG, nil)
					return err
				})
				if err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
}
