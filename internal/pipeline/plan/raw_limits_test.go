package plan_test

import (
	"errors"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// publicMsg is the client message of err ("" without an *errs.Error).
func publicMsg(err error) string {
	if e, ok := errors.AsType[*errs.Error](err); ok {
		return e.PublicMessage()
	}
	return ""
}

// defaultRawLimits are the default XI_LIMITS_* the RAW rule involves.
var defaultRawLimits = plan.Limits{MaxSrcPixels: 50_000_000, MaxRawDemosaicPixels: 120_000_000}

// TestBuildRawPixelLimit is the RAW-aware source limit (WP-102): a RAW is
// judged on what the planned load decodes (preview, half-size or full
// demosaic), and a demosaic also on its sensor.
func TestBuildRawPixelLimit(t *testing.T) {
	t.Parallel()
	// Fujifilm GFX100RF (102 MP, 4000×3000 preview), Hasselblad X1D II
	// (51.4 MP, no JPEG preview), Google Pixel 8 Pro (50.1 MP, 2560×1928).
	gfx := plan.SourceMeta{Width: 11662, Height: 8746, Format: "raw", PreviewWidth: 4000, PreviewHeight: 3000}
	x1d := plan.SourceMeta{Width: 8278, Height: 6208, Format: "raw"}
	pixel := plan.SourceMeta{Width: 8160, Height: 6144, Format: "raw", PreviewWidth: 2560, PreviewHeight: 1928}
	sensor := plan.SourceMeta{Width: 14204, Height: 10652, Format: "raw", PreviewWidth: 1600, PreviewHeight: 1200}
	fit := func(n int) transform.Pipeline {
		return transform.Pipeline{Resize: transform.Resize{Type: transform.ResizeFit, Width: px(n), Height: px(n)}}
	}
	type path struct{ preview, half bool }
	for _, tc := range []struct {
		name string
		meta plan.SourceMeta
		p    transform.Pipeline
		want path
		err  string // public message; "" = no error
	}{
		{"GFX100RF 160 px: preview", gfx, fit(160), path{preview: true}, ""},
		{"GFX100RF 2000 px: preview", gfx, fit(2000), path{preview: true}, ""},
		{"GFX100RF 5000 px: half-size demosaic (25.5 MP)", gfx, fit(5000), path{half: true}, ""},
		{
			"GFX100RF full size: full demosaic over the limit", gfx,
			transform.Pipeline{},
			path{},
			"source is 11662×8746 (102 MP); limit XI_LIMITS_MAX_SRC_PIXELS is 50 MP",
		},
		{"X1D II 160 px: half-size demosaic", x1d, fit(160), path{half: true}, ""},
		{"X1D II 4000 px: half-size demosaic", x1d, fit(4000), path{half: true}, ""},
		{
			"X1D II 5000 px: full demosaic over the limit", x1d, fit(5000),
			path{},
			"source is 8278×6208 (51.4 MP); limit XI_LIMITS_MAX_SRC_PIXELS is 50 MP",
		},
		{"Pixel 8 Pro 2000 px: preview", pixel, fit(2000), path{preview: true}, ""},
		{"Pixel 8 Pro 3000 px: half-size demosaic", pixel, fit(3000), path{half: true}, ""},
		{"151 MP sensor 800 px: preview", sensor, fit(800), path{preview: true}, ""},
		{
			"151 MP sensor 3000 px: over the demosaic limit", sensor, fit(3000),
			path{},
			"camera RAW sensor is 14204×10652 (151.3 MP); limit XI_LIMITS_MAX_RAW_DEMOSAIC_PIXELS is 120 MP",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := req(tc.p)
			r.Output.Format = transform.FormatJPEG
			p, err := plan.Build(r, tc.meta, defaultRawLimits)
			if tc.err != "" {
				if errs.KindOf(err) != errs.KindLimitExceeded || publicMsg(err) != tc.err {
					t.Fatalf("err = %v (%q), want limit exceeded %q", err, publicMsg(err), tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := (path{p.Load.Preview, p.Load.HalfSize}); got != tc.want {
				t.Errorf("path = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestBuildRawPreviewOnly: a RAW LibRaw cannot demosaic (Nikon High
// Efficiency, a camera it does not know) is loaded from its preview
// whatever the request or the preview ratio; its size is the preview's.
func TestBuildRawPreviewOnly(t *testing.T) {
	t.Parallel()
	he := plan.SourceMeta{Width: 5568, Height: 3712, Format: "raw", PreviewWidth: 5568, PreviewHeight: 3712, PreviewOnly: true}
	for _, tc := range []struct {
		name   string
		p      transform.Pipeline
		lim    plan.Limits
		shrink int
		out    [2]int
	}{
		{"160 px", transform.Pipeline{Resize: transform.Resize{Width: px(160)}}, defaultRawLimits, 8, [2]int{160, 107}},
		{"2000 px", transform.Pipeline{Resize: transform.Resize{Width: px(2000)}}, defaultRawLimits, 2, [2]int{2000, 1333}},
		{"full size", transform.Pipeline{}, defaultRawLimits, 0, [2]int{5568, 3712}},
		{"larger than the preview", transform.Pipeline{Resize: transform.Resize{Width: px(8000)}}, defaultRawLimits, 0, [2]int{5568, 3712}},
		{
			"a preview ratio that would demosaic",
			transform.Pipeline{Resize: transform.Resize{Width: px(4000)}},
			plan.Limits{MaxSrcPixels: 50_000_000, RawPreviewMinRatio: 100},
			0,
			[2]int{4000, 2667},
		},
		{"trim (no shrink-on-load)", transform.Pipeline{Trim: &transform.Trim{Threshold: 10}}, defaultRawLimits, 0, [2]int{5568, 3712}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := req(tc.p)
			r.Output.Format = transform.FormatJPEG
			p, err := plan.Build(r, he, tc.lim)
			if err != nil {
				t.Fatal(err)
			}
			if p.Load.HalfSize || p.Load.ShrinkOnLoad != tc.shrink {
				t.Errorf("load %+v, want the preview with shrink %d", p.Load, tc.shrink)
			}
			if tc.p.Trim == nil && !p.Load.Preview {
				t.Errorf("load %+v is not the preview", p.Load)
			}
			if p.Cost.EstimatedBytes >= 5568*3712*2+5568*3712*8 {
				t.Errorf("cost %d counts a demosaic", p.Cost.EstimatedBytes)
			}
			if got := size(p); got != tc.out {
				t.Errorf("size = %v, want %v", got, tc.out)
			}
		})
	}
	// Over the limit, the preview is what is named.
	big := plan.SourceMeta{Width: 9000, Height: 6000, Format: "raw", PreviewWidth: 9000, PreviewHeight: 6000, PreviewOnly: true}
	r := req(transform.Pipeline{Resize: transform.Resize{Width: px(400)}})
	r.Output.Format = transform.FormatJPEG
	_, err := plan.Build(r, big, defaultRawLimits)
	const want = "camera RAW embedded preview is 9000×6000 (54 MP); limit XI_LIMITS_MAX_SRC_PIXELS is 50 MP"
	if errs.KindOf(err) != errs.KindLimitExceeded || publicMsg(err) != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}
