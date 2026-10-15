package cloudinary_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect/cloudinary"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/preset"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// TestVideoPresets: t_<name> on a video transformation (WP-132) starts the
// VideoOp from the video presets; the URL's parameters override them, a
// preset's format wins over the public ID's extension and f_ over it.
func TestVideoPresets(t *testing.T) {
	t.Parallel()
	vp, err := preset.New(preset.Options{Definitions: map[string]string{
		"web":     "rs:fit:640:0/q:60/f:webm",
		"short":   "vd:4",
		"hls":     "st:hls:360:240",
		"default": "ki:2",
	}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := cloudinary.New(&cloudinary.Options{VideoPresets: vp, Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	ki := 2 * time.Second
	tests := []struct {
		name, path string
		want       transform.VideoOp
		preset     *transform.VideoOp // PresetVideo
	}{
		{
			"preset", "/demo/video/upload/t_web/clip.mp4",
			transform.VideoOp{KeyframeInterval: ki, Width: 640, Quality: 60, Fit: transform.VideoFitScaleDown, Container: transform.VideoContainerWebM},
			&transform.VideoOp{KeyframeInterval: ki, Width: 640, Quality: 60, Fit: transform.VideoFitScaleDown, Container: transform.VideoContainerWebM},
		},
		{
			"parameters override", "/demo/video/upload/t_web,q_80,so_1/clip.mp4",
			transform.VideoOp{KeyframeInterval: ki, Start: time.Second, Width: 640, Quality: 80, Fit: transform.VideoFitScaleDown, Container: transform.VideoContainerWebM},
			&transform.VideoOp{KeyframeInterval: ki, Width: 640, Quality: 60, Fit: transform.VideoFitScaleDown, Container: transform.VideoContainerWebM},
		},
		{
			"f_ over the preset's format", "/demo/video/upload/t_web/f_mp4/clip.mp4",
			transform.VideoOp{KeyframeInterval: ki, Width: 640, Quality: 60, Fit: transform.VideoFitScaleDown},
			&transform.VideoOp{KeyframeInterval: ki, Width: 640, Quality: 60, Fit: transform.VideoFitScaleDown, Container: transform.VideoContainerWebM},
		},
		{
			"two presets", "/demo/video/upload/t_short.web/clip.mov",
			transform.VideoOp{KeyframeInterval: ki, Duration: 4 * time.Second, Width: 640, Quality: 60, Fit: transform.VideoFitScaleDown, Container: transform.VideoContainerWebM},
			&transform.VideoOp{KeyframeInterval: ki, Duration: 4 * time.Second, Width: 640, Quality: 60, Fit: transform.VideoFitScaleDown, Container: transform.VideoContainerWebM},
		},
		{
			"extension without a preset format", "/demo/video/upload/t_short/clip.webm",
			transform.VideoOp{KeyframeInterval: ki, Duration: 4 * time.Second, Fit: transform.VideoFitScaleDown, Container: transform.VideoContainerWebM},
			&transform.VideoOp{KeyframeInterval: ki, Duration: 4 * time.Second, Fit: transform.VideoFitScaleDown},
		},
		{
			"stream preset", "/demo/video/upload/t_hls/clip.m3u8",
			transform.VideoOp{KeyframeInterval: ki, Fit: transform.VideoFitContain, Stream: &transform.VideoStream{Protocol: transform.StreamHLS, Renditions: []int{360, 240}}},
			&transform.VideoOp{KeyframeInterval: ki, Fit: transform.VideoFitContain, Stream: &transform.VideoStream{Protocol: transform.StreamHLS, Renditions: []int{360, 240}}},
		},
		{
			"no preset", "/demo/video/upload/w_320/clip.mp4",
			transform.VideoOp{KeyframeInterval: ki, Width: 320, Fit: transform.VideoFitContain},
			nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := p.ParsePath(tc.path, "media")
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(&tc.want, res.Request.Video); diff != "" {
				t.Errorf("op (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.preset, res.PresetVideo); diff != "" {
				t.Errorf("PresetVideo (-want +got):\n%s", diff)
			}
		})
	}
	if _, err := p.ParsePath("/demo/video/upload/t_nope/clip.mp4", "media"); errs.KindOf(err) != errs.KindInvalidURL {
		t.Errorf("unknown video preset: %v, want 400", err)
	}
	// Without video presets t_ stays unsupported on videos.
	bare, err := cloudinary.New(&cloudinary.Options{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bare.ParsePath("/demo/video/upload/t_web/clip.mp4", "media"); errs.KindOf(err) != errs.KindInvalidURL {
		t.Errorf("t_ without video presets in strict mode: %v, want 400", err)
	}
}
