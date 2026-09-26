//go:build cgo

package exec_test

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// TestGolden runs every goldenCase and compares the decoded output (all
// frames stacked) with testdata/golden/exec/<name>.png by DSSIM.
func TestGolden(t *testing.T) {
	t.Parallel()
	for _, tc := range goldenCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := process(t, tc.fixture, tc.options, tc.out)
			checkOutput(t, &tc, &o)
			golden.Compare(t, "exec/"+tc.name, o.img, golden.Options{Tolerance: tc.tolerance})
		})
	}
}

func checkOutput(t *testing.T, tc *goldenCase, o *output) {
	t.Helper()
	frames := max(tc.frames, 1)
	if o.res.Frames != frames || o.frames != frames {
		t.Errorf("frames: result %d, decoded %d, want %d", o.res.Frames, o.frames, frames)
	}
	if o.res.Format != tc.out {
		t.Errorf("format = %s, want %s", o.res.Format, tc.out)
	}
	if tc.w > 0 && (o.res.Width != tc.w || o.res.Height != tc.h) {
		t.Errorf("size = %dx%d, want %dx%d", o.res.Width, o.res.Height, tc.w, tc.h)
	}
	b := o.img.Bounds()
	if b.Dx() != o.res.Width || b.Dy() != o.res.Height*o.frames {
		t.Errorf("decoded %dx%d, result says %dx%d × %d frames", b.Dx(), b.Dy(), o.res.Width, o.res.Height, o.frames)
	}
}

// TestTrim pads a photo with a uniform border and trims it back.
func TestTrim(t *testing.T) {
	t.Parallel()
	padded := process(t, jpegPhoto, "rs:fit:120:120/pd:20:30/bg:255:255:255", transform.FormatPNG)
	if w, h := padded.res.Width, padded.res.Height; w != 180 || h != 130 {
		t.Fatalf("padded %dx%d, want 180x130", w, h)
	}
	for _, tc := range []struct {
		name, options string
		w, h          int
	}{
		{name: "trim", options: "t:10", w: 120, h: 90},
		{name: "trim-color", options: "t:10:FFFFFF", w: 120, h: 90},
		{name: "trim-resize", options: "t:10/rs:fit:60:60", w: 60, h: 45},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := processData(t, padded.bytes, tc.options, transform.FormatPNG)
			if o.res.Width != tc.w || o.res.Height != tc.h {
				t.Errorf("size = %dx%d, want %dx%d", o.res.Width, o.res.Height, tc.w, tc.h)
			}
			golden.Compare(t, "exec/"+tc.name, o.img, golden.Options{})
		})
	}
}
