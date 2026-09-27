//go:build cgo

package vips_test

import (
	"bytes"
	"context"
	"image"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// TestSmartCropOffset: a smart crop's image origin (FieldXOffset,
// FieldYOffset) is minus the rectangle it cut, which the executor uses to
// map detections through it (WP-92): extracting that rectangle gives the
// same pixels.
func TestSmartCropOffset(t *testing.T) {
	t.Parallel()
	// A dark 200×100 image with a bright, busy square at 140..180 × 50..90:
	// attention crops towards it.
	const w, h = 200, 100
	pix := make([]byte, w*h*3)
	for y := byte(50); y < 90; y++ {
		for x := byte(140); x < 180; x++ {
			i := (int(y)*w + int(x)) * 3
			pix[i], pix[i+1], pix[i+2] = 255, x*7, y*13
		}
	}
	job(t, func(ctx context.Context, a *vips.Arena) error {
		img, err := a.Add(vips.NewFromMemory(ctx, pix, w, h, 3, vips.BandFormatUchar))
		if err != nil {
			return err
		}
		sc, err := a.Add(img.SmartCrop(ctx, 60, 60, vips.InterestingAttention))
		if err != nil {
			return err
		}
		ox, okX := sc.Int(vips.FieldXOffset)
		oy, okY := sc.Int(vips.FieldYOffset)
		if !okX || !okY || ox >= 0 || oy >= 0 {
			t.Fatalf("offset %d, %d (%v %v): want the negative left and top of an off-origin crop", ox, oy, okX, okY)
		}
		r := image.Rect(-ox, -oy, -ox+60, -oy+60)
		ex, err := a.Add(img.ExtractArea(ctx, r))
		if err != nil {
			return err
		}
		got, err := sc.WriteToMemory(ctx)
		if err != nil {
			return err
		}
		want, err := ex.WriteToMemory(ctx)
		if err != nil {
			return err
		}
		if !bytes.Equal(got, want) {
			t.Errorf("smart crop pixels differ from the area at %v", r)
		}
		return nil
	})
}
