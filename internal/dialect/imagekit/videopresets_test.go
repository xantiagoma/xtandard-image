package imagekit_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imagekit"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/preset"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// TestVideoPresets: n-<name> on a video transformation (WP-132) starts the
// VideoOp from the video presets; the steps override them, f- and
// ik-video.mp4 win over a preset's format, and a preset's stream fills an
// ik-master stream.
func TestVideoPresets(t *testing.T) {
	t.Parallel()
	vp, err := preset.New(preset.Options{Definitions: map[string]string{
		"web":   "rs:fit:640:0/q:60/f:webm",
		"short": "vd:4",
		"hls":   "st:hls:360:240/sgd:2",
	}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := imagekit.New(&imagekit.Options{VideoPresets: vp, Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	web := transform.VideoOp{Width: 640, Quality: 60, Fit: transform.VideoFitScaleDown, Container: transform.VideoContainerWebM}
	short := transform.VideoOp{Duration: 4 * time.Second, Fit: transform.VideoFitScaleDown, AutoContainer: true}
	hls := &transform.VideoStream{Protocol: transform.StreamHLS, Renditions: []int{360, 240}, SegmentDuration: 2 * time.Second}
	tests := []struct {
		name, path, query string
		want              transform.VideoOp
		preset            *transform.VideoOp
	}{
		{"preset", "/clip.mp4", "tr=n-web", web, &web},
		{"steps override", "/clip.mp4", "tr=n-web,q-80,so-1", transform.VideoOp{
			Start: time.Second, Width: 640, Quality: 80, Fit: transform.VideoFitScaleDown, Container: transform.VideoContainerWebM,
		}, &web},
		{"f- over the preset's format", "/clip.mp4", "tr=n-web:f-mp4", transform.VideoOp{
			Width: 640, Quality: 60, Fit: transform.VideoFitScaleDown,
		}, &web},
		{"ik-video.mp4 over the preset's format", "/clip.mov/ik-video.mp4", "tr=n-web", transform.VideoOp{
			Width: 640, Quality: 60, Fit: transform.VideoFitScaleDown,
		}, &web},
		{"automatic container", "/clip.mp4", "tr=n-short", short, &short},
		{"stream preset on ik-master", "/clip.mp4/ik-master.m3u8", "tr=n-hls", transform.VideoOp{
			Fit: transform.VideoFitContain, Stream: hls,
		}, &transform.VideoOp{Fit: transform.VideoFitContain, Stream: hls}},
		{"sr- over the preset's renditions", "/clip.mp4/ik-master.m3u8", "tr=n-hls:sr-720", transform.VideoOp{
			Fit: transform.VideoFitContain, Stream: &transform.VideoStream{Protocol: transform.StreamHLS, Renditions: []int{720}, SegmentDuration: 2 * time.Second},
		}, &transform.VideoOp{Fit: transform.VideoFitContain, Stream: hls}},
		{"ik-master without a stream preset", "/clip.mp4/ik-master.mpd", "tr=n-short", transform.VideoOp{
			Duration: 4 * time.Second, Fit: transform.VideoFitContain, Stream: &transform.VideoStream{Protocol: transform.StreamDASH},
		}, &short},
		{"no preset", "/clip.mp4", "tr=w-320", transform.VideoOp{Width: 320, Fit: transform.VideoFitContain, AutoContainer: true}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := p.Parse(&dialect.Input{Path: tc.path, RawQuery: tc.query})
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
	if _, err := p.Parse(&dialect.Input{Path: "/clip.mp4", RawQuery: "tr=n-nope"}); errs.KindOf(err) != errs.KindInvalidURL {
		t.Errorf("unknown video preset: %v, want 400", err)
	}
}
