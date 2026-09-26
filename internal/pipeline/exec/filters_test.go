package exec

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
)

// TestFilterParams pins the mapping of a FilterStep onto libvips parameters
// (imgproxy vips_apply_filters): the URL sigma is the vips sigma, sharpen
// keeps the other vips_sharpen defaults, the pixelate block is clamped to
// the larger side and the image is mirrored out to whole blocks.
func TestFilterParams(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		step  plan.FilterStep
		w, h  int
		alpha bool
		want  filterParams
	}{
		{"off", plan.FilterStep{}, 100, 80, false, filterParams{PadW: 100, PadH: 80}},
		{"pixelate 1 is off", plan.FilterStep{Pixelate: 1}, 100, 80, false, filterParams{PadW: 100, PadH: 80}},
		{"negative is off", plan.FilterStep{Blur: -1, Sharpen: -2, Pixelate: -3}, 10, 10, true, filterParams{PadW: 10, PadH: 10}},
		{"blur", plan.FilterStep{Blur: 0.5}, 100, 80, false, filterParams{Blur: 0.5, PadW: 100, PadH: 80}},
		{"blur alpha premultiplies", plan.FilterStep{Blur: 30}, 100, 80, true, filterParams{Premultiply: true, Blur: 30, PadW: 100, PadH: 80}},
		{"sharpen alpha premultiplies", plan.FilterStep{Sharpen: 1.5}, 100, 80, true, filterParams{Premultiply: true, Sharpen: 1.5, PadW: 100, PadH: 80}},
		{"pixelate alpha does not premultiply", plan.FilterStep{Pixelate: 10}, 100, 80, true, filterParams{Pixelate: 10, PadW: 100, PadH: 80}},
		{"pixelate pads to whole blocks", plan.FilterStep{Pixelate: 7}, 100, 80, false, filterParams{Pixelate: 7, PadW: 105, PadH: 84}},
		{"pixelate clamped to the larger side", plan.FilterStep{Pixelate: 500}, 100, 80, false, filterParams{Pixelate: 100, PadW: 100, PadH: 100}},
		{"pixelate on a 1px image is off", plan.FilterStep{Pixelate: 4}, 1, 1, false, filterParams{PadW: 1, PadH: 1}},
		{"all", plan.FilterStep{Blur: 2, Sharpen: 1, Pixelate: 3}, 10, 9, true, filterParams{Premultiply: true, Blur: 2, Sharpen: 1, Pixelate: 3, PadW: 12, PadH: 9}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := newFilterParams(&tc.step, tc.w, tc.h, tc.alpha)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("params (-want +got):\n%s", diff)
			}
		})
	}
}
