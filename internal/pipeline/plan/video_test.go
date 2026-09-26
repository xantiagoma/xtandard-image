package plan_test

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// videoMeta is a 1280×720 video with a display orientation.
func videoMeta(orientation int) plan.SourceMeta {
	return plan.SourceMeta{Width: 1280, Height: 720, Format: "mp4", IsVideo: true, Orientation: orientation}
}

// TestBuildVideo: a video frame is scaled on load to twice the target
// (libvips does the last 2×), not at all under 2×; the display matrix
// rotates like EXIF; the decoder's memory is in the cost.
func TestBuildVideo(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		p          transform.Pipeline
		meta       plan.SourceMeta
		wantScale  float64
		wantLoad   [2]int
		wantOutput [2]int
	}{
		{"8x down", transform.Pipeline{Resize: transform.Resize{Width: px(160)}}, videoMeta(1), 0.25, [2]int{320, 180}, [2]int{160, 90}},
		{"3x down", transform.Pipeline{Resize: transform.Resize{Width: px(426)}}, videoMeta(1), 2.0 / 3, [2]int{853, 480}, [2]int{426, 240}},
		{"under 2x", transform.Pipeline{Resize: transform.Resize{Width: px(1000)}}, videoMeta(1), 0, [2]int{1280, 720}, [2]int{1000, 563}},
		{"no resize", transform.Pipeline{}, videoMeta(1), 0, [2]int{1280, 720}, [2]int{1280, 720}},
		{"rotated", transform.Pipeline{Resize: transform.Resize{Width: px(90)}}, videoMeta(6), 0.25, [2]int{320, 180}, [2]int{90, 160}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := plan.Build(req(tc.p), tc.meta, plan.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			if d := p.Load.Scale - tc.wantScale; d < -1e-9 || d > 1e-9 {
				t.Errorf("scale %v, want %v", p.Load.Scale, tc.wantScale)
			}
			if got := [2]int{p.Load.Width, p.Load.Height}; got != tc.wantLoad {
				t.Errorf("load %v, want %v", got, tc.wantLoad)
			}
			if got := size(p); got != tc.wantOutput {
				t.Errorf("output %v, want %v", got, tc.wantOutput)
			}
			if floor := int64(tc.meta.Width) * int64(tc.meta.Height) * 8; p.Cost.EstimatedBytes < floor {
				t.Errorf("cost %d bytes, want at least the decoder's %d", p.Cost.EstimatedBytes, floor)
			}
		})
	}
	noShrink, err := plan.Build(req(tests[0].p), videoMeta(1), plan.Limits{DisableShrinkOnLoad: true})
	if err != nil {
		t.Fatal(err)
	}
	if noShrink.Load.Scale != 0 {
		t.Errorf("scale %v with shrink-on-load disabled", noShrink.Load.Scale)
	}
}
