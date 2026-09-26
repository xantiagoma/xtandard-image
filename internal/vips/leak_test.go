//go:build cgo

package vips_test

import (
	"context"
	"image"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/testutil/leakcheck"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// TestNoLeak runs 10,000 load+resize+save cycles (alternating the Load and
// Thumbnail paths and the JPEG and WebP savers) with libvips leak checking
// on, and checks that tracked memory, live allocations, open files and live
// VipsObjects return to their baseline.
//
// Tolerance (leakcheck.Default, checked once the counters settle; see
// package leakcheck for why they need to): a per-cycle leak of a single
// allocation or object shows up as ≥ 10,000, and one leaked source buffer
// as ~200 MB, far above the bounds.
//
// Not parallel: the counters are process-wide, and non-parallel top-level
// tests run while every parallel test is paused.
func TestNoLeak(t *testing.T) {
	const cycles = 10_000
	vips.SetLeakCheck(true)
	defer vips.SetLeakCheck(false)

	data := readFixture(t, "jpeg/baseline.jpg")
	so := vips.DefaultSaveOptions()
	so.WebP.Effort = 0
	cycle := func(i int) {
		buf := newBuffer(t, data)
		defer buf.Release()
		job(t, func(ctx context.Context, a *vips.Arena) error {
			var img *vips.Image
			var err error
			if i%2 == 0 {
				img, err = a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Shrink: 2, Access: vips.AccessSequential}))
			} else {
				img, err = a.Add(vips.Thumbnail(ctx, buf, &vips.ThumbnailOptions{Width: 120}))
			}
			if err != nil {
				return err
			}
			small, err := a.Add(img.Resize(ctx, 0.5, 0, vips.KernelLanczos3))
			if err != nil {
				return err
			}
			if i%4 == 3 {
				if small, err = filterExtrasChain(ctx, a, small); err != nil {
					return err
				}
			}
			if i%4 == 1 {
				if small, err = colourChain(ctx, a, small); err != nil {
					return err
				}
			}
			f := vips.FormatJPEG
			if i%3 == 0 {
				f = vips.FormatWebP
			}
			blob, err := small.Save(ctx, f, &so)
			if err != nil {
				return err
			}
			blob.Release()
			return nil
		})
	}
	for i := range 4 {
		cycle(i) // warm up lazily initialised libvips state
	}

	before := leakcheck.Take()
	for i := range cycles {
		cycle(i)
	}
	after := leakcheck.Check(t, before, leakcheck.Default, "cycles")
	if after.MemHighwater <= 0 {
		t.Errorf("highwater = %d, want > 0", after.MemHighwater)
	}
	t.Logf("after %d cycles: mem %+d B, allocs %+d, objects %+d, highwater %d B",
		cycles, after.Mem-before.Mem, after.Allocs-before.Allocs, after.Objects-before.Objects, after.MemHighwater)
}

// filterExtrasChain runs the filter-extras operations (WP-61): a 3×3
// convolution, an unsharp mask, a blurred area inserted back and a blend
// through a gradient mask.
func filterExtrasChain(ctx context.Context, a *vips.Arena, img *vips.Image) (*vips.Image, error) {
	k := 1.0 / 24
	sharp, err := a.Add(img.Conv3(ctx, &[9]float64{-k, -k, -k, -k, 1 + 8*k, -k, -k, -k, -k}))
	if err != nil {
		return nil, err
	}
	usm, err := a.Add(sharp.Unsharp(ctx, 1.5, 0.5))
	if err != nil {
		return nil, err
	}
	area, err := a.Add(usm.ExtractArea(ctx, image.Rect(0, 0, usm.Width()/2, usm.Height()/2)))
	if err != nil {
		return nil, err
	}
	blurred, err := a.Add(area.Gaussblur(ctx, 3))
	if err != nil {
		return nil, err
	}
	inserted, err := a.Add(usm.Insert(ctx, blurred, 0, 0))
	if err != nil {
		return nil, err
	}
	mask, err := a.Add(vips.GradientMask(ctx, img.Width(), img.Height(), 0, 1, 0, 1))
	if err != nil {
		return nil, err
	}
	return a.Add(usm.BlendMask(ctx, mask, inserted))
}

// colourChain runs the colour operations (WP-59) on a three-band image:
// a LUT, a matrix, a mix and a gradient mask whose pixels are read back.
func colourChain(ctx context.Context, a *vips.Arena, img *vips.Image) (*vips.Image, error) {
	table := make([]byte, 256)
	for i := range table {
		table[i] = byte(255 - i)
	}
	lut, err := a.Add(vips.NewFromMemory(ctx, table, 256, 1, 1, vips.BandFormatUchar))
	if err != nil {
		return nil, err
	}
	mapped, err := a.Add(img.MapLUT(ctx, lut))
	if err != nil {
		return nil, err
	}
	sepia, err := a.Add(mapped.Recomb(ctx, []float64{
		0.3588, 0.7044, 0.1368,
		0.2990, 0.5870, 0.1140,
		0.2392, 0.4696, 0.0912,
	}, 3))
	if err != nil {
		return nil, err
	}
	mask, err := a.Add(vips.GradientMask(ctx, img.Width(), img.Height(), 0, 1, -10, 0.5))
	if err != nil {
		return nil, err
	}
	if _, err := mask.WriteToMemory(ctx); err != nil {
		return nil, err
	}
	return a.Add(img.Mix(ctx, sepia, 0.5))
}
