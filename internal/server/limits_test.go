package server

import (
	"errors"
	"net/http"
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

// TestCheckMetaPixels: the probe check rejects a frame no limit could
// admit; the totals over frames are the planner's (imgproxy's
// CheckDimensions on the frames loaded), so an animation within the
// per-frame limit but over XI_LIMITS_MAX_SRC_PIXELS in total passes here.
func TestCheckMetaPixels(t *testing.T) {
	t.Parallel()
	def := ProbeLimits{MaxPixels: 50_000_000, MaxFrames: 256, MaxFramePixels: 16_000_000}
	frameOff := def
	frameOff.MaxFramePixels = 0
	noAnim := def
	noAnim.MaxFrames = 1
	anim := func(w, h, n int) SourceMeta {
		return SourceMeta{Format: transform.FormatGIF, Width: w, Height: h, Frames: n, Pages: n, IsAnimated: true}
	}
	still := func(w, h int) SourceMeta {
		return SourceMeta{Format: transform.FormatPNG, Width: w, Height: h, Frames: 1, Pages: 1}
	}
	for _, tc := range []struct {
		name string
		meta SourceMeta
		lim  ProbeLimits
		want string // public message; "" is no error
	}{
		{"cards.gif: 84.5 MP in all, 0.48 MP a frame", anim(800, 600, 176), def, ""},
		{"frame limit off: the planner counts the frames loaded", anim(800, 600, 176), frameOff, ""},
		{"animation disabled", anim(800, 600, 1000), noAnim, ""},
		{"still over the total", still(8000, 8000), def, "source is 8000×8000 (64 MP); limit XI_LIMITS_MAX_SRC_PIXELS is 50 MP"},
		{"frame over every limit", anim(8000, 8000, 2), def, "source frame is 8000×8000 (64 MP); limit XI_LIMITS_MAX_SRC_PIXELS is 50 MP"},
		{"frame over, animation disabled", anim(8000, 8000, 2), noAnim, "source is 8000×8000 (64 MP); limit XI_LIMITS_MAX_SRC_PIXELS is 50 MP"},
		{"too many frames", anim(10, 10, 300), def, "source has 300 frames; limit XI_LIMITS_MAX_ANIMATION_FRAMES is 256"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := checkMeta(&tc.meta, &tc.lim)
			got := ""
			if err != nil {
				if k := errs.KindOf(err); k != errs.KindLimitExceeded {
					t.Fatalf("checkMeta = %v (kind %v), want limit_exceeded", err, k)
				}
				se, ok := errors.AsType[statusError](err)
				if !ok {
					t.Fatalf("checkMeta = %v carries no public message", err)
				}
				if se.HTTPStatus() != http.StatusUnprocessableEntity {
					t.Errorf("status = %d, want 422", se.HTTPStatus())
				}
				got = se.PublicMessage()
			}
			if got != tc.want {
				t.Errorf("checkMeta public message = %q, want %q", got, tc.want)
			}
		})
	}
}
