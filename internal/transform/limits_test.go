package transform

import (
	"testing"
)

// publicOf is the client message of le ("" for nil).
func publicOf(t *testing.T, le *LimitError) string {
	t.Helper()
	if le == nil {
		return ""
	}
	if le.Error() != le.Public {
		t.Errorf("Error() = %q, want the public message %q", le.Error(), le.Public)
	}
	return le.Public
}

var defaults = SourceLimits{MaxSrcPixels: 50_000_000, MaxFramePixels: 16_000_000, MaxAnimationPixels: 200_000_000}

// TestCheckDimensions is imgproxy's security.CheckDimensions: the frame
// limit replaces the total for animations; 0 turns it off and the total
// applies to every frame.
func TestCheckDimensions(t *testing.T) {
	t.Parallel()
	frameOff := defaults
	frameOff.MaxFramePixels = 0
	for _, tc := range []struct {
		name         string
		lim          SourceLimits
		w, h, frames int
		want         string
	}{
		{"cards.gif within the frame limit", defaults, 800, 600, 176, ""},
		{"still within", defaults, 5000, 10_000, 1, ""},
		{"still over", defaults, 10_000, 5001, 1, "source is 10000×5001 (50010000 pixels); limit XI_LIMITS_MAX_SRC_PIXELS is 50000000 pixels"},
		{"still over in MP", defaults, 8000, 8000, 1, "source is 8000×8000 (64 MP); limit XI_LIMITS_MAX_SRC_PIXELS is 50 MP"},
		{"frame over", defaults, 5000, 4000, 2, "source frame is 5000×4000 (20 MP); limit XI_LIMITS_MAX_ANIMATION_FRAME_PIXELS is 16 MP"},
		{"frame at the limit", defaults, 4000, 4000, 12, ""},
		{"animation total over", defaults, 1920, 1080, 256, "source has 256 frames of 1920×1080 (530.8 MP); limit XI_LIMITS_MAX_ANIMATION_PIXELS is 200 MP"},
		{"frame limit off: total applies", frameOff, 800, 600, 176, "source has 176 frames of 800×600 (84.5 MP); limit XI_LIMITS_MAX_SRC_PIXELS is 50 MP"},
		{"frame limit off: within the total", frameOff, 800, 600, 100, ""},
		{"frame limit off: no animation cap", SourceLimits{MaxSrcPixels: 1e9, MaxAnimationPixels: 1}, 100, 100, 10, ""},
		{"no animation cap", SourceLimits{MaxFramePixels: 16_000_000}, 4000, 4000, 256, ""},
		{"unlimited", SourceLimits{}, 100_000, 100_000, 1, ""},
		{"zero frames is one", defaults, 8000, 8000, 0, "source is 8000×8000 (64 MP); limit XI_LIMITS_MAX_SRC_PIXELS is 50 MP"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := publicOf(t, tc.lim.CheckDimensions(tc.w, tc.h, tc.frames)); got != tc.want {
				t.Errorf("CheckDimensions(%d, %d, %d) = %q, want %q", tc.w, tc.h, tc.frames, got, tc.want)
			}
		})
	}
}

// TestCheckFrame: the probe rejects only a frame no limit could admit.
func TestCheckFrame(t *testing.T) {
	t.Parallel()
	raised := defaults
	raised.MaxFramePixels = 80_000_000
	frameOff := defaults
	frameOff.MaxFramePixels = 0
	for _, tc := range []struct {
		name     string
		lim      SourceLimits
		w, h     int
		animated bool
		want     string
	}{
		{"cards.gif", defaults, 800, 600, true, ""},
		{"cards.gif, frame limit off", frameOff, 800, 600, true, ""},
		{"still within", defaults, 7000, 7000, false, ""},
		{"still over", defaults, 8000, 8000, false, "source is 8000×8000 (64 MP); limit XI_LIMITS_MAX_SRC_PIXELS is 50 MP"},
		// Over the frame limit but it may be processed as a still.
		{"animation frame over the frame limit only", defaults, 5000, 4000, true, ""},
		{"animation frame over both", defaults, 8000, 8000, true, "source frame is 8000×8000 (64 MP); limit XI_LIMITS_MAX_SRC_PIXELS is 50 MP"},
		{"raised frame limit admits", raised, 8000, 8000, true, ""},
		{"raised frame limit names itself", raised, 10_000, 9000, true, "source frame is 10000×9000 (90 MP); limit XI_LIMITS_MAX_ANIMATION_FRAME_PIXELS is 80 MP"},
		{"raised frame limit does not apply to stills", raised, 8000, 8000, false, "source is 8000×8000 (64 MP); limit XI_LIMITS_MAX_SRC_PIXELS is 50 MP"},
		{"frame limit off", frameOff, 8000, 8000, true, "source frame is 8000×8000 (64 MP); limit XI_LIMITS_MAX_SRC_PIXELS is 50 MP"},
		{"unlimited", SourceLimits{}, 100_000, 100_000, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := publicOf(t, tc.lim.CheckFrame(tc.w, tc.h, tc.animated)); got != tc.want {
				t.Errorf("CheckFrame(%d, %d, %v) = %q, want %q", tc.w, tc.h, tc.animated, got, tc.want)
			}
		})
	}
}

func TestCheckFrames(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		frames, limit int
		want          string
	}{
		{176, 256, ""},
		{300, 256, "source has 300 frames; limit XI_LIMITS_MAX_ANIMATION_FRAMES is 256"},
		{300, 1, ""}, // 1 disables animation
		{300, 0, ""}, // unlimited
		{1, 1, ""},
	} {
		if got := publicOf(t, CheckFrames(tc.frames, tc.limit)); got != tc.want {
			t.Errorf("CheckFrames(%d, %d) = %q, want %q", tc.frames, tc.limit, got, tc.want)
		}
	}
}

func TestMegapixels(t *testing.T) {
	t.Parallel()
	for px, want := range map[int64]string{
		0: "0", 49_999: "0", 50_000: "0.1", 84_480_000: "84.5", 50_000_000: "50", 16_777_216: "16.8", 999_950_000: "1000",
	} {
		if got := megapixels(px); got != want {
			t.Errorf("megapixels(%d) = %q, want %q", px, got, want)
		}
	}
}
