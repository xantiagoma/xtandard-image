//go:build cgo

package vips_test

import (
	"context"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// TestNoLeak runs 10,000 load+resize+save cycles (alternating the Load and
// Thumbnail paths and the JPEG and WebP savers) with libvips leak checking
// on, and checks that tracked memory, live allocations, open files and live
// VipsObjects return to their baseline.
//
// Tolerance: libvips' worker threads keep per-thread buffer caches that are
// freed asynchronously when the threads go idle, so tracked memory wobbles
// by up to a few hundred KiB and a few dozen allocations around the
// baseline. A per-cycle leak of a single allocation or object shows up as
// ≥ 10,000, and one leaked source buffer as ~200 MB, far above the bounds.
//
// Not parallel: the counters are process-wide, and non-parallel top-level
// tests run while every parallel test is paused.
func TestNoLeak(t *testing.T) {
	const (
		cycles      = 10_000
		slackBytes  = 1 << 20
		slackAllocs = 64
		slackObjs   = 16
	)
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

	before, objects := vips.ReadMemStats(), vips.LiveObjects()
	for i := range cycles {
		cycle(i)
	}
	after, objectsAfter := vips.ReadMemStats(), vips.LiveObjects()
	if after.Mem-before.Mem > slackBytes || after.Allocs-before.Allocs > slackAllocs ||
		after.Files != before.Files || objectsAfter-objects > slackObjs {
		t.Errorf("after %d cycles: mem %d → %d B, allocs %d → %d, files %d → %d, objects %d → %d "+
			"(tolerance %d B / %d allocs / %d objects)",
			cycles, before.Mem, after.Mem, before.Allocs, after.Allocs, before.Files, after.Files,
			objects, objectsAfter, slackBytes, slackAllocs, slackObjs)
	}
	if after.MemHighwater <= 0 {
		t.Errorf("highwater = %d, want > 0", after.MemHighwater)
	}
	t.Logf("after %d cycles: mem %+d B, allocs %+d, objects %+d, highwater %d B",
		cycles, after.Mem-before.Mem, after.Allocs-before.Allocs, objectsAfter-objects, after.MemHighwater)
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
