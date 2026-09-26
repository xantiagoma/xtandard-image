//go:build cgo

package vips_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// TestNewFromBuffer: the image reads the Buffer's C memory without a copy
// and keeps it alive after the caller's Release, until the image closes;
// the libvips tracked memory is back where it started afterwards.
func TestNewFromBuffer(t *testing.T) {
	t.Parallel()
	const w, h = 24, 16
	pix := pattern(w, h)
	var got []byte
	job(t, func(ctx context.Context, a *vips.Arena) error {
		buf, err := vips.NewBuffer(len(pix) + 64) // longer than the pixels, like video frames
		if err != nil {
			return err
		}
		copy(buf.Bytes(), pix)
		img, err := a.Add(vips.NewFromBuffer(ctx, buf, w, h, 3, vips.BandFormatUchar))
		buf.Release() // the image holds its own reference
		if err != nil {
			return err
		}
		if img.Width() != w || img.Height() != h || img.Bands() != 3 || img.Interpretation() != vips.InterpretationSRGB {
			t.Errorf("image %dx%dx%d %v", img.Width(), img.Height(), img.Bands(), img.Interpretation())
		}
		flipped, err := a.Add(img.Flip(ctx, vips.DirectionHorizontal))
		if err != nil {
			return err
		}
		back, err := a.Add(flipped.Flip(ctx, vips.DirectionHorizontal))
		if err != nil {
			return err
		}
		got, err = back.WriteToMemory(ctx)
		return err
	})
	if !bytes.Equal(got, pix) {
		t.Error("pixels differ from the buffer's")
	}
}

func TestNewFromBufferErrors(t *testing.T) {
	t.Parallel()
	err := tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
		_, err := a.Add(vips.NewFromBuffer(ctx, nil, 2, 2, 3, vips.BandFormatUchar))
		return err
	})
	if errs.KindOf(err) != errs.KindInternal {
		t.Errorf("nil buffer: %v, want KindInternal", err)
	}
	buf, err := vips.NewBuffer(5)
	if err != nil {
		t.Fatal(err)
	}
	defer buf.Release()
	err = tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
		_, err := a.Add(vips.NewFromBuffer(ctx, buf, 2, 2, 3, vips.BandFormatUchar))
		return err
	})
	if errs.KindOf(err) != errs.KindInternal {
		t.Errorf("short buffer: %v, want KindInternal", err)
	}
}
