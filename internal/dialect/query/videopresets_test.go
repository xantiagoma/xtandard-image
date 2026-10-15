package query_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect/query"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/preset"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// TestVideoPresets: preset= in /q/video (WP-132) expands video presets in
// query order, and unsigned requests carry the presets-only op.
func TestVideoPresets(t *testing.T) {
	t.Parallel()
	vp, err := preset.New(preset.Options{Definitions: map[string]string{"web": "w:640/q:60/f:webm"}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := query.New(&query.Options{VideoPresets: vp, Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	web := &transform.VideoOp{Width: 640, Quality: 60, Fit: transform.VideoFitScaleDown, Container: transform.VideoContainerWebM}
	tests := []struct {
		name, query string
		want        *transform.VideoOp
		preset      *transform.VideoOp
	}{
		{"preset", "url=https://e.com/a.mov&preset=web", web, web},
		{"later key wins", "url=https://e.com/a.mov&preset=web&quality=80&start=1", &transform.VideoOp{
			Start: time.Second, Width: 640, Quality: 80, Fit: transform.VideoFitScaleDown, Container: transform.VideoContainerWebM,
		}, web},
		{"preset after the key wins", "url=https://e.com/a.mov&quality=80&pr=web&format=mp4", &transform.VideoOp{
			Width: 640, Quality: 60, Fit: transform.VideoFitScaleDown,
		}, web},
		{"o= path", "url=https://e.com/a.mov&o=pr:web/w:100", &transform.VideoOp{
			Width: 100, Quality: 60, Fit: transform.VideoFitScaleDown, Container: transform.VideoContainerWebM,
		}, web},
		{"no preset", "url=https://e.com/a.mov&width=100", &transform.VideoOp{Width: 100, Fit: transform.VideoFitScaleDown}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := p.ParseURL("/video", tc.query)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, res.Request.Video); diff != "" {
				t.Errorf("op (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.preset, res.PresetVideo); diff != "" {
				t.Errorf("PresetVideo (-want +got):\n%s", diff)
			}
		})
	}
	if _, err := p.ParseURL("/video", "url=https://e.com/a.mov&preset=nope"); errs.KindOf(err) != errs.KindInvalidURL {
		t.Errorf("unknown video preset: %v, want 400", err)
	}
}
