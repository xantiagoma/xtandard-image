package server

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// TestCheckMetaAnimationFrames: animations with more frames than the limit
// are rejected before decoding, except with a limit of 1, which disables
// animation (the first frame is processed, as imgproxy does).
func TestCheckMetaAnimationFrames(t *testing.T) {
	t.Parallel()
	anim := SourceMeta{Format: transform.FormatGIF, Width: 10, Height: 10, Frames: 300, Pages: 300, IsAnimated: true}
	for _, tc := range []struct {
		name      string
		maxFrames int
		want      errs.Kind
	}{
		{name: "unlimited", maxFrames: 0, want: errs.KindUnknown},
		{name: "over the limit", maxFrames: 256, want: errs.KindLimitExceeded},
		{name: "two frames", maxFrames: 2, want: errs.KindLimitExceeded},
		{name: "animation disabled", maxFrames: 1, want: errs.KindUnknown},
		{name: "within the limit", maxFrames: 300, want: errs.KindUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := checkMeta(&anim, &ProbeLimits{MaxFrames: tc.maxFrames})
			if got := errs.KindOf(err); got != tc.want { // KindOf(nil) is KindUnknown
				t.Errorf("checkMeta = %v (kind %v), want kind %v", err, got, tc.want)
			}
		})
	}
}
