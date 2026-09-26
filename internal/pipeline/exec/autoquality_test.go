//go:build cgo

package exec_test

import (
	"slices"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec/encode"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// TestAutoQuality runs imgproxy's autoquality option through the executor:
// the chosen quality and the number of saves reach the Result, ml is
// reported as skipped, and an animation keeps its frames.
func TestAutoQuality(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, fixture, options string
		format                 transform.Format
		check                  func(t *testing.T, o output)
	}{
		{
			name: "dssim", fixture: "jpeg/baseline.jpg", options: "rs:fit:300:300/aq:dssim", format: transform.FormatJPEG,
			check: func(t *testing.T, o output) {
				if o.res.Quality < 40 || o.res.Quality > 90 || o.res.Saves < 1 || o.res.Saves > encode.MaxSaves {
					t.Errorf("quality %d in %d saves", o.res.Quality, o.res.Saves)
				}
			},
		},
		{
			name: "dssim request bounds", fixture: "jpeg/baseline.jpg", options: "aq:dssim:0.02:70:75", format: transform.FormatWebP,
			check: func(t *testing.T, o output) {
				if o.res.Quality < 70 || o.res.Quality > 75 || o.res.Saves > encode.MaxSaves {
					t.Errorf("quality %d in %d saves, want 70..75", o.res.Quality, o.res.Saves)
				}
			},
		},
		{
			name: "size", fixture: "jpeg/baseline.jpg", options: "aq:size:6000", format: transform.FormatJPEG,
			check: func(t *testing.T, o output) {
				if len(o.bytes) > 6000 || o.res.Saves > encode.MaxSaves {
					t.Errorf("%d bytes at quality %d in %d saves, want ≤ 6000", len(o.bytes), o.res.Quality, o.res.Saves)
				}
			},
		},
		{
			name: "ml ignored", fixture: "jpeg/baseline.jpg", options: "aq:ml", format: transform.FormatJPEG,
			check: func(t *testing.T, o output) {
				if o.res.Quality != 80 || o.res.Saves != 1 || !slices.Contains(o.res.Skipped, encode.IgnoredML) {
					t.Errorf("quality %d, saves %d, skipped %v", o.res.Quality, o.res.Saves, o.res.Skipped)
				}
			},
		},
		{
			name: "animation", fixture: "gif/animated.gif", options: "aq:dssim", format: transform.FormatWebP,
			check: func(t *testing.T, o output) {
				if o.frames != 6 || o.res.Frames != 6 || o.res.Saves < 2 || o.res.Saves > encode.MaxSaves+1 {
					t.Errorf("frames %d/%d, saves %d", o.frames, o.res.Frames, o.res.Saves)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.check(t, process(t, tc.fixture, tc.options, tc.format))
		})
	}
}
