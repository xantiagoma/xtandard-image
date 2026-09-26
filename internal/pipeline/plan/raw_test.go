package plan_test

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// rawMeta is a 6000×4000 RAW with a pw×ph preview.
func rawMeta(pw, ph, orientation int) plan.SourceMeta {
	return plan.SourceMeta{Width: 6000, Height: 4000, Format: "raw", Orientation: orientation, PreviewWidth: pw, PreviewHeight: ph}
}

// TestBuildRaw covers the decode decision for camera RAW (FORMATS.md §2):
// embedded preview when it is large enough (with JPEG shrink-on-load on
// it), else a half-size or full demosaic.
func TestBuildRaw(t *testing.T) {
	t.Parallel()
	type load struct {
		preview, half bool
		shrink        int
		w, h          int
	}
	width := func(n int) transform.Pipeline { return transform.Pipeline{Resize: transform.Resize{Width: px(n)}} }
	tests := []struct {
		name  string
		p     transform.Pipeline
		meta  plan.SourceMeta
		lim   plan.Limits
		force bool
		want  load
		out   [2]int
	}{
		{"preview exactly large enough", width(1600), rawMeta(1600, 1067, 1), plan.Limits{}, false, load{true, false, 0, 1600, 1067}, [2]int{1600, 1067}},
		{"preview shrinks on load", width(400), rawMeta(1600, 1067, 1), plan.Limits{}, false, load{true, false, 2, 800, 533}, [2]int{400, 267}},
		{"preview shrinks on load by 8", width(200), rawMeta(1600, 1067, 1), plan.Limits{}, false, load{true, false, 8, 200, 133}, [2]int{200, 133}},
		{"full-size preview, no resize", transform.Pipeline{}, rawMeta(6000, 4000, 1), plan.Limits{}, false, load{true, false, 0, 6000, 4000}, [2]int{6000, 4000}},
		{"preview too small: half demosaic", width(2000), rawMeta(1600, 1067, 1), plan.Limits{}, false, load{false, true, 0, 3000, 2000}, [2]int{2000, 1333}},
		{"preview too small: full demosaic", width(4000), rawMeta(1600, 1067, 1), plan.Limits{}, false, load{false, false, 0, 6000, 4000}, [2]int{4000, 2667}},
		{"no preview", width(400), rawMeta(0, 0, 1), plan.Limits{}, false, load{false, true, 0, 3000, 2000}, [2]int{400, 267}},
		{"ratio 2 wants a larger preview", width(1000), rawMeta(1600, 1067, 1), plan.Limits{RawPreviewMinRatio: 2}, false, load{false, true, 0, 3000, 2000}, [2]int{1000, 667}},
		{"ratio 0.5 accepts a smaller preview", width(3000), rawMeta(1600, 1067, 1), plan.Limits{RawPreviewMinRatio: 0.5}, false, load{true, false, 0, 1600, 1067}, [2]int{3000, 2000}},
		{"enforce_thumbnail", width(4000), rawMeta(1600, 1067, 1), plan.Limits{}, true, load{true, false, 0, 1600, 1067}, [2]int{4000, 2667}},
		{"shrink-on-load disabled", width(400), rawMeta(0, 0, 1), plan.Limits{DisableShrinkOnLoad: true}, false, load{false, false, 0, 6000, 4000}, [2]int{400, 267}},
		// Portrait: the preview is stored like the sensor (6000×4000 → shown 4000×6000).
		{"portrait preview", width(1000), rawMeta(1620, 1080, 6), plan.Limits{}, false, load{true, false, 0, 1620, 1080}, [2]int{1000, 1500}},
		{"portrait too small", width(1200), rawMeta(1620, 1080, 6), plan.Limits{}, false, load{false, true, 0, 3000, 2000}, [2]int{1200, 1800}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := req(tc.p)
			r.Output.Format = transform.FormatJPEG
			r.Output.EnforceThumbnail = &tc.force
			p, err := plan.Build(r, tc.meta, tc.lim)
			if err != nil {
				t.Fatal(err)
			}
			l := p.Load
			got := load{l.Preview, l.HalfSize, l.ShrinkOnLoad, l.Width, l.Height}
			if got != tc.want {
				t.Errorf("load = %+v, want %+v", got, tc.want)
			}
			if got := size(p); got != tc.out {
				t.Errorf("size = %v, want %v", got, tc.out)
			}
		})
	}
}

func TestBuildRawLimits(t *testing.T) {
	t.Parallel()
	r := req(transform.Pipeline{Resize: transform.Resize{Width: px(2000)}})
	r.Output.Format = transform.FormatJPEG
	_, err := plan.Build(r, rawMeta(1600, 1067, 1), plan.Limits{MaxRawDemosaicPixels: 20_000_000})
	if errs.KindOf(err) != errs.KindLimitExceeded {
		t.Errorf("demosaic over the limit: %v, want limit exceeded", err)
	}
	// The preview path is not bound by the demosaic limit.
	small := req(transform.Pipeline{Resize: transform.Resize{Width: px(800)}})
	small.Output.Format = transform.FormatJPEG
	p, err := plan.Build(small, rawMeta(1600, 1067, 1), plan.Limits{MaxRawDemosaicPixels: 20_000_000})
	if err != nil || !p.Load.Preview {
		t.Errorf("preview under a demosaic limit: %+v, %v", p, err)
	}
	// The demosaic's working memory counts against the budget.
	full, err := plan.Build(r, rawMeta(0, 0, 1), plan.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if full.Cost.EstimatedBytes < 6000*4000*2 {
		t.Errorf("demosaic cost %d does not include LibRaw's buffers", full.Cost.EstimatedBytes)
	}
	// The observed demosaic size replaces the predicted one.
	obs, err := plan.Continue(r, rawMeta(0, 0, 1), plan.Limits{}, plan.Observed{Width: 3002, Height: 2001, Loaded: true})
	if err != nil {
		t.Fatal(err)
	}
	if !obs.Load.HalfSize || obs.Load.Width != 3002 || size(obs) != [2]int{2000, 1333} {
		t.Errorf("observed half demosaic: load %+v, size %v", obs.Load, size(obs))
	}
}
