//go:build cgo

package vips_test

import (
	"context"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// job runs fn inside vips.Run with an Arena that is closed afterwards, and
// fails the test on error.
func job(tb testing.TB, fn func(ctx context.Context, a *vips.Arena) error) {
	tb.Helper()
	if err := tryJob(tb.Context(), fn); err != nil {
		tb.Fatal(err)
	}
}

// tryJob is job returning the error.
func tryJob(ctx context.Context, fn func(ctx context.Context, a *vips.Arena) error) error {
	return vips.Run(ctx, func() error {
		var a vips.Arena
		defer a.Close()
		return fn(ctx, &a)
	})
}

// probeBytes probes encoded bytes (copied into a fresh Buffer).
func probeBytes(tb testing.TB, data []byte) vips.Header {
	tb.Helper()
	buf := newBuffer(tb, data)
	defer buf.Release()
	var h vips.Header
	job(tb, func(ctx context.Context, _ *vips.Arena) error {
		var err error
		h, err = vips.Probe(ctx, buf)
		return err
	})
	return h
}

// saveBytes saves img and returns a Go copy of the output.
func saveBytes(ctx context.Context, img *vips.Image, f vips.Format, o *vips.SaveOptions) ([]byte, error) {
	blob, err := img.Save(ctx, f, o)
	if err != nil {
		return nil, err
	}
	defer blob.Release()
	return append([]byte(nil), blob.Bytes()...), nil
}

// pattern returns w×h×3 uchar pixels of a smooth gradient with some
// structure, so encoders and smart crop have something to work with.
func pattern(w, h int) []byte {
	pix := make([]byte, w*h*3)
	for y := range h {
		for x := range w {
			i := (y*w + x) * 3
			pix[i] = byte(x * 255 / max(w-1, 1))   //nolint:gosec // 0..255 by construction
			pix[i+1] = byte(y * 255 / max(h-1, 1)) //nolint:gosec // 0..255 by construction
			pix[i+2] = byte((x ^ y) & 0xFF)
		}
	}
	return pix
}
