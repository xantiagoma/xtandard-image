package plan_test

import (
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

func req(ps ...transform.Pipeline) *transform.Request {
	r := &transform.Request{Pipelines: ps}
	r.ApplyDefaults(transform.StandardDefaults())
	return r
}

func jpeg(w, h int) plan.SourceMeta { return plan.SourceMeta{Width: w, Height: h, Format: "jpeg"} }

func px(n int) transform.Dim { return transform.Dim{Px: n} }

// pdfMeta is a 3-page PDF 200 pt wide measured at 72 DPI; selection marks
// a height the executor measured for the request's pages.
func pdfMeta(h int, selection bool) plan.SourceMeta {
	return plan.SourceMeta{
		Width: 200, Height: h, Frames: 3, Format: "pdf", IsVector: true, Density: 72, Selection: selection,
	}
}

// size is the output size of the last chained plan.
func size(p *plan.Plan) [2]int {
	if n := len(p.Chained); n > 0 {
		p = p.Chained[n-1]
	}
	return [2]int{p.Output.Width, p.Output.Height}
}

func stepsOf[T plan.Step](steps []plan.Step) []T {
	var out []T
	for _, s := range steps {
		if v, ok := s.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

// TestBuildSizes covers options imgproxy does not have (relative sizes,
// aspect ratio, adaptive, outside, max width) and output-format limits.
func TestBuildSizes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		p    transform.Pipeline
		meta plan.SourceMeta
		out  transform.Format
		want [2]int
	}{
		{"relative_width_half", transform.Pipeline{Resize: transform.Resize{Width: transform.Dim{Rel: 0.5}}}, jpeg(1000, 600), 0, [2]int{500, 300}},
		{"relative_crop_quarter", transform.Pipeline{Crop: &transform.Crop{Width: transform.Dim{Rel: 0.25}, Height: transform.Dim{Rel: 0.5}}}, jpeg(1000, 600), 0, [2]int{250, 300}},
		{"aspect_ratio_derives_height", transform.Pipeline{Resize: transform.Resize{Type: transform.ResizeFill, Width: px(400), AspectRatio: 2}}, jpeg(1000, 600), 0, [2]int{400, 200}},
		{"aspect_ratio_derives_width", transform.Pipeline{Resize: transform.Resize{Type: transform.ResizeFill, Height: px(100), AspectRatio: 1}}, jpeg(1000, 600), 0, [2]int{100, 100}},
		{"adaptive_swaps_box", transform.Pipeline{Resize: transform.Resize{Type: transform.ResizeFill, Width: px(200), Height: px(400), Adaptive: true}}, jpeg(1000, 600), 0, [2]int{400, 200}},
		{"outside_covers_without_crop", transform.Pipeline{Resize: transform.Resize{Type: transform.ResizeOutside, Width: px(300), Height: px(300)}}, jpeg(1000, 600), 0, [2]int{500, 300}},
		{"max_width_clamps", transform.Pipeline{Resize: transform.Resize{Width: px(2000), Enlarge: true, MaxWidth: 800}}, jpeg(1000, 600), 0, [2]int{800, 480}},
		{"max_height_with_dpr", transform.Pipeline{Resize: transform.Resize{Width: px(400), DPR: 2, MaxHeight: 100}}, jpeg(1000, 600), 0, [2]int{333, 200}},
		{"webp_dimension_limit", transform.Pipeline{}, plan.SourceMeta{Width: 20000, Height: 100, Format: "png"}, transform.FormatWebP, [2]int{16383, 82}},
		{"ico_dimension_limit", transform.Pipeline{}, jpeg(1000, 600), transform.FormatICO, [2]int{256, 154}},
		{"avif_dimension_limit", transform.Pipeline{}, plan.SourceMeta{Width: 100, Height: 32768, Format: "png"}, transform.FormatAVIF, [2]int{50, 16384}},
		{"strip_page_height", transform.Pipeline{Resize: transform.Resize{Width: px(100)}}, plan.SourceMeta{Width: 200, Height: 750, PageHeight: 150, Format: "png"}, 0, [2]int{100, 75}},
		{"vector_enlarges_without_flag", transform.Pipeline{Resize: transform.Resize{Width: px(900)}}, plan.SourceMeta{Width: 300, Height: 200, Format: "svg", IsVector: true}, transform.FormatPNG, [2]int{900, 600}},
		{"pdf_scale_on_load", transform.Pipeline{Resize: transform.Resize{Width: px(150)}}, plan.SourceMeta{Width: 600, Height: 800, Format: "pdf", IsVector: true}, transform.FormatPNG, [2]int{150, 200}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := req(tc.p)
			r.Output.Format = tc.out
			p, err := plan.Build(r, tc.meta, plan.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			if got := size(p); got != tc.want {
				t.Errorf("size = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildLoad(t *testing.T) {
	t.Parallel()
	fit := func(w int) transform.Pipeline { return transform.Pipeline{Resize: transform.Resize{Width: px(w)}} }
	tests := []struct {
		name   string
		p      transform.Pipeline
		meta   plan.SourceMeta
		limits plan.Limits
		want   plan.LoadStep
	}{
		{"jpeg_shrink_8", fit(400), jpeg(4000, 3000), plan.Limits{}, plan.LoadStep{ShrinkOnLoad: 8, NPages: 1, Width: 500, Height: 375}},
		{"jpeg_shrink_off_by_config", fit(400), jpeg(4000, 3000), plan.Limits{DisableShrinkOnLoad: true}, plan.LoadStep{NPages: 1, Width: 4000, Height: 3000}},
		{"jpeg_no_shrink_below_2", fit(3000), jpeg(4000, 3000), plan.Limits{}, plan.LoadStep{NPages: 1, Width: 4000, Height: 3000}},
		{"webp_scale", fit(400), plan.SourceMeta{Width: 1600, Height: 900, Format: "webp"}, plan.Limits{}, plan.LoadStep{Scale: 0.25, NPages: 1, Width: 400, Height: 225}},
		{"png_no_shrink", fit(400), plan.SourceMeta{Width: 1600, Height: 900, Format: "png"}, plan.Limits{}, plan.LoadStep{NPages: 1, Width: 1600, Height: 900}},
		{"heic_asks_thumbnail", fit(400), plan.SourceMeta{Width: 4000, Height: 3000, Format: "heif"}, plan.Limits{}, plan.LoadStep{NPages: 1, Thumbnail: true, Width: 4000, Height: 3000}},
		{"svg_vector_guard", transform.Pipeline{}, plan.SourceMeta{Width: 3000, Height: 2000, Format: "svg", IsVector: true}, plan.Limits{MaxSrcPixels: 600_000}, plan.LoadStep{Scale: 1 / 3.1622776601683795, NPages: 1, Width: 949, Height: 632}},
		{"page_selects_frame", transform.Pipeline{Page: new(2)}, plan.SourceMeta{Width: 10, Height: 10, Frames: 4, Format: "tiff"}, plan.Limits{}, plan.LoadStep{Page: 2, NPages: 1, Width: 10, Height: 10}},
		{"rotate_needs_random_access", transform.Pipeline{Rotate: 90}, jpeg(100, 100), plan.Limits{}, plan.LoadStep{NPages: 1, Width: 100, Height: 100, Access: plan.AccessRandom}},
		// PDF: rendered at the target size (scale on load, at the probe's
		// density), never upscaled from the natural size.
		{"pdf_density_and_scale", fit(400), pdfMeta(150, false), plan.Limits{}, plan.LoadStep{Scale: 2, Density: 72, NPages: 1, Width: 400, Height: 300}},
		{"pdf_natural_size", transform.Pipeline{}, pdfMeta(150, false), plan.Limits{}, plan.LoadStep{Density: 72, NPages: 1, Width: 200, Height: 150}},
		{"pdf_pages_predicted", transform.Pipeline{Pages: new(2)}, pdfMeta(150, false), plan.Limits{}, plan.LoadStep{Density: 72, NPages: 2, Width: 200, Height: 300}},
		{"pdf_pages_all_from_page", transform.Pipeline{Page: new(1), Pages: new(-1)}, pdfMeta(150, false), plan.Limits{}, plan.LoadStep{Density: 72, Page: 1, NPages: 2, Width: 200, Height: 300}},
		{"pdf_pages_measured", transform.Pipeline{Page: new(1), Pages: new(5), Resize: transform.Resize{Width: px(100)}}, pdfMeta(310, true), plan.Limits{}, plan.LoadStep{Scale: 0.5, Density: 72, Page: 1, NPages: 2, Width: 100, Height: 155}},
		{"tiff_pages_stack", transform.Pipeline{Pages: new(2)}, plan.SourceMeta{Width: 10, Height: 10, Frames: 2, Format: "tiff"}, plan.Limits{}, plan.LoadStep{NPages: 2, Width: 10, Height: 20}},
		{"psd_layers_never_stack", transform.Pipeline{Pages: new(2)}, plan.SourceMeta{Width: 10, Height: 10, Frames: 3, Format: "psd"}, plan.Limits{}, plan.LoadStep{NPages: 1, Width: 10, Height: 10}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := plan.Build(req(tc.p), tc.meta, tc.limits)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, p.Load); diff != "" {
				t.Errorf("Load (-want +got):\n%s", diff)
			}
		})
	}
}

func gifMeta(frames int) plan.SourceMeta {
	return plan.SourceMeta{Width: 200, Height: 150, Frames: frames, IsAnimated: true, Format: "gif"}
}

func TestBuildAnimation(t *testing.T) {
	t.Parallel()
	fit := transform.Resize{Width: px(100), Height: px(100)}
	tests := []struct {
		name   string
		p      transform.Pipeline
		out    transform.Format
		limits plan.Limits
		frames int // 0 = not animated
		page   int
		want   [2]int
	}{
		{"all_frames", transform.Pipeline{Resize: fit}, 0, plan.Limits{}, 5, 0, [2]int{100, 75}},
		{"max_animation_frames", transform.Pipeline{Resize: fit}, 0, plan.Limits{MaxAnimationFrames: 3}, 3, 0, [2]int{100, 75}},
		{"max_animation_frames_1_disables", transform.Pipeline{Resize: fit}, 0, plan.Limits{MaxAnimationFrames: 1}, 0, 0, [2]int{100, 75}},
		{"disable_animation", transform.Pipeline{Resize: fit, DisableAnimation: true}, 0, plan.Limits{}, 0, 0, [2]int{100, 75}},
		{"pages_2", transform.Pipeline{Resize: fit, Pages: new(2)}, 0, plan.Limits{}, 2, 0, [2]int{100, 75}},
		{"page_3_of_5", transform.Pipeline{Resize: fit, Page: new(3)}, 0, plan.Limits{}, 2, 3, [2]int{100, 75}},
		{"webp_output_keeps_animation", transform.Pipeline{Resize: fit}, transform.FormatWebP, plan.Limits{}, 5, 0, [2]int{100, 75}},
		{"png_output_drops_animation", transform.Pipeline{Resize: fit}, transform.FormatPNG, plan.Limits{}, 0, 0, [2]int{100, 75}},
		{"dpr_2_per_frame", transform.Pipeline{Resize: transform.Resize{Width: px(50), DPR: 2}}, 0, plan.Limits{}, 5, 0, [2]int{100, 75}},
		{"smart_falls_back_to_centre", transform.Pipeline{Resize: transform.Resize{Type: transform.ResizeFill, Width: px(100), Height: px(100)}, Gravity: transform.Gravity{Type: transform.GravitySmart}}, 0, plan.Limits{}, 5, 0, [2]int{100, 100}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := req(tc.p)
			r.Output.Format = tc.out
			p, err := plan.Build(r, gifMeta(5), tc.limits)
			if err != nil {
				t.Fatal(err)
			}
			if tc.frames == 0 {
				if p.Animation != nil || p.Load.NPages != 1 || p.Output.Frames != 1 {
					t.Errorf("animated: Animation=%v NPages=%d Frames=%d", p.Animation, p.Load.NPages, p.Output.Frames)
				}
			} else {
				want := &plan.AnimationStep{Frames: tc.frames, PageHeight: 150}
				if diff := cmp.Diff(want, p.Animation); diff != "" {
					t.Errorf("Animation (-want +got):\n%s", diff)
				}
				if p.Load.NPages != tc.frames || p.Load.Page != tc.page || p.Load.ShrinkOnLoad != 0 || p.Load.Scale != 0 {
					t.Errorf("Load = %+v", p.Load)
				}
				if p.Output.Frames != tc.frames {
					t.Errorf("Output.Frames = %d", p.Output.Frames)
				}
			}
			if got := size(p); got != tc.want {
				t.Errorf("size = %v, want %v", got, tc.want)
			}
			for _, c := range stepsOf[plan.CropStep](p.Steps) {
				if p.Animation != nil && c.Smart != plan.InterestingNone {
					t.Errorf("smart crop on an animation: %+v", c)
				}
			}
		})
	}
}

func TestBuildLimits(t *testing.T) {
	t.Parallel()
	big := plan.SourceMeta{Width: 8000, Height: 6000, Format: "jpeg"} // 48 MP
	tests := []struct {
		name      string
		meta      plan.SourceMeta
		overrides transform.LimitOverrides
		limits    plan.Limits
		p         transform.Pipeline
		wantErr   bool
	}{
		{"src_pixels_ok", big, transform.LimitOverrides{}, plan.Limits{MaxSrcPixels: 50_000_000}, transform.Pipeline{}, false},
		{"src_pixels_exceeded", big, transform.LimitOverrides{}, plan.Limits{MaxSrcPixels: 40_000_000}, transform.Pipeline{}, true},
		{"override_lowers", big, transform.LimitOverrides{MaxSrcResolution: 10}, plan.Limits{MaxSrcPixels: 50_000_000}, transform.Pipeline{}, true},
		{"override_cannot_raise", big, transform.LimitOverrides{MaxSrcResolution: 100}, plan.Limits{MaxSrcPixels: 40_000_000}, transform.Pipeline{}, true},
		{"override_raises_when_allowed", big, transform.LimitOverrides{MaxSrcResolution: 100}, plan.Limits{MaxSrcPixels: 40_000_000, AllowRaisingLimits: true}, transform.Pipeline{}, false},
		{"vector_skips_src_check", plan.SourceMeta{Width: 8000, Height: 6000, Format: "svg", IsVector: true}, transform.LimitOverrides{}, plan.Limits{MaxSrcPixels: 40_000_000}, transform.Pipeline{}, false},
		{"frames_count_towards_src_pixels", gifMeta(5), transform.LimitOverrides{}, plan.Limits{MaxSrcPixels: 100_000}, transform.Pipeline{}, true},
		{"frame_pixels_replace_src_pixels", gifMeta(5), transform.LimitOverrides{}, plan.Limits{MaxSrcPixels: 100_000, MaxAnimationFramePixels: 30_000}, transform.Pipeline{}, false},
		{"frame_pixels_exceeded", gifMeta(5), transform.LimitOverrides{MaxAnimationFrameResolution: 0.02}, plan.Limits{}, transform.Pipeline{}, true},
		{"memory_budget_exceeded", big, transform.LimitOverrides{}, plan.Limits{MemoryBudget: 100 << 20}, transform.Pipeline{}, true},
		{"memory_budget_ok_with_shrink_on_load", big, transform.LimitOverrides{}, plan.Limits{MemoryBudget: 100 << 20}, transform.Pipeline{Resize: transform.Resize{Width: px(500)}}, false},
		{"absurd_enlarge", jpeg(100, 100), transform.LimitOverrides{}, plan.Limits{}, transform.Pipeline{Resize: transform.Resize{Width: px(1 << 30), Enlarge: true}}, true},
		{"absurd_padding", jpeg(100, 100), transform.LimitOverrides{}, plan.Limits{}, transform.Pipeline{Padding: &transform.Padding{Top: 1 << 30}}, true},
		{"result_dimension_downscales", jpeg(1000, 600), transform.LimitOverrides{MaxResultDimension: 100}, plan.Limits{}, transform.Pipeline{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := req(tc.p)
			r.Limits = tc.overrides
			p, err := plan.Build(r, tc.meta, tc.limits)
			if tc.wantErr {
				if errs.KindOf(err) != errs.KindLimitExceeded {
					t.Fatalf("err = %v, want KindLimitExceeded", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if d := tc.overrides.MaxResultDimension; d > 0 && max(p.Output.Width, p.Output.Height) > d {
				t.Errorf("result %dx%d exceeds %d", p.Output.Width, p.Output.Height, d)
			}
		})
	}
}

func TestBuildInvalidMeta(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		meta plan.SourceMeta
		kind errs.Kind
	}{
		{"zero_width", plan.SourceMeta{Height: 10}, errs.KindCorrupt},
		{"negative_height", plan.SourceMeta{Width: 10, Height: -1}, errs.KindCorrupt},
		{"beyond_max_coord", plan.SourceMeta{Width: 20_000_000, Height: 1}, errs.KindLimitExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := plan.Build(req(), tc.meta, plan.Limits{}); errs.KindOf(err) != tc.kind {
				t.Errorf("err = %v, want %v", err, tc.kind)
			}
		})
	}
	if _, err := plan.Continue(req(), jpeg(10, 10), plan.Limits{}, plan.Observed{Loaded: true}); errs.KindOf(err) != errs.KindCorrupt {
		t.Errorf("Continue with no size: err = %v", err)
	}
	if _, err := plan.Build(nil, jpeg(10, 10), plan.Limits{}); err == nil {
		t.Error("nil request accepted")
	}
}

func TestContinue(t *testing.T) {
	t.Parallel()
	heif := plan.SourceMeta{Width: 4000, Height: 3000, Format: "heif"}
	fit400 := req(transform.Pipeline{Resize: transform.Resize{Width: px(400)}})
	tests := []struct {
		name      string
		meta      plan.SourceMeta
		obs       plan.Observed
		thumbnail bool
		resize    []float64 // ScaleX of the ResizeStep, nil = none
		want      [2]int
	}{
		// imgproxy keeps a thumbnail smaller than the source but not smaller
		// than the target.
		{"thumbnail_accepted", heif, plan.Observed{Width: 500, Height: 375, Thumbnail: true}, true, []float64{0.8}, [2]int{400, 300}},
		{"thumbnail_too_small", heif, plan.Observed{Width: 320, Height: 240, Thumbnail: true}, false, []float64{0.1}, [2]int{400, 300}},
		{"thumbnail_not_smaller", heif, plan.Observed{Width: 4000, Height: 3000, Thumbnail: true}, false, []float64{0.1}, [2]int{400, 300}},
		// A loader that rounds differently from the prediction.
		{"loader_rounding", jpeg(4000, 3000), plan.Observed{Width: 501, Height: 375, Loaded: true}, false, []float64{400.0 / 501}, [2]int{400, 300}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := plan.Continue(fit400, tc.meta, plan.Limits{}, tc.obs)
			if err != nil {
				t.Fatal(err)
			}
			if p.Load.Thumbnail != tc.thumbnail {
				t.Errorf("Load.Thumbnail = %v", p.Load.Thumbnail)
			}
			var scales []float64
			for _, r := range stepsOf[plan.ResizeStep](p.Steps) {
				scales = append(scales, r.ScaleX)
			}
			if diff := cmp.Diff(tc.resize, scales); diff != "" {
				t.Errorf("resize scales (-want +got):\n%s", diff)
			}
			if got := size(p); got != tc.want {
				t.Errorf("size = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildChained(t *testing.T) {
	t.Parallel()
	r := req(
		transform.Pipeline{Resize: transform.Resize{Width: px(500)}, Watermarks: []transform.Watermark{{Opacity: 0.5, Scale: 0.2}}},
		transform.Pipeline{Crop: &transform.Crop{Width: px(100), Height: px(100)}, Rotate: 90},
	)
	p, err := plan.Build(r, jpeg(1000, 600), plan.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Chained) != 1 {
		t.Fatalf("Chained = %d plans", len(p.Chained))
	}
	c := p.Chained[0]
	if c.Load != (plan.LoadStep{}) {
		t.Errorf("chained Load = %+v, want zero", c.Load)
	}
	if got := size(p); got != [2]int{100, 100} {
		t.Errorf("size = %v", got)
	}
	wantFirst := []plan.Step{
		plan.WatermarkStep{
			Opacity: 0.5, Position: plan.Gravity{Type: transform.GravityCenter}, Scale: 0.2, DPRScale: 1, Frames: 1, Default: true,
		},
	}
	if diff := cmp.Diff(wantFirst, p.After); diff != "" {
		t.Errorf("first After (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]plan.Step{plan.ColourStep{Stage: plan.ColourToResult}}, c.After); diff != "" {
		t.Errorf("last After (-want +got):\n%s", diff)
	}
	if got := len(stepsOf[plan.RotateStep](c.Steps)); got != 1 {
		t.Errorf("chained rotate steps = %d", got)
	}
}

func TestBuildSteps(t *testing.T) {
	t.Parallel()
	red := &transform.Color{R: 255, A: 255}
	png := plan.SourceMeta{Width: 1000, Height: 600, HasAlpha: true, Format: "png"}
	fill := transform.Resize{Type: transform.ResizeFill, Width: px(300), Height: px(300)}
	tests := []struct {
		name string
		p    transform.Pipeline
		meta plan.SourceMeta
		want []plan.Step
		out  plan.Output
	}{
		{
			"png_alpha_fill_premultiplies",
			transform.Pipeline{Resize: fill},
			png,
			[]plan.Step{
				plan.ColourStep{},
				plan.ResizeStep{ScaleX: 0.5, ScaleY: 0.5, Kernel: transform.KernelLanczos3, Premultiply: true, W: 500, H: 300},
				plan.CropStep{plan.CropGeometry{X: 100, W: 300, H: 300, Width: 300, Height: 300, Gravity: plan.Gravity{Type: transform.GravityCenter}, OffsetScale: 1}},
			},
			plan.Output{Width: 300, Height: 300, Frames: 1, Format: transform.FormatPNG, Alpha: true, DPRScale: 1},
		},
		{
			"background_flattens_and_extends",
			transform.Pipeline{Resize: transform.Resize{Width: px(400), Height: px(400)}, Extend: transform.Extend{Enabled: true}, Background: red},
			png,
			[]plan.Step{
				plan.ColourStep{},
				plan.ResizeStep{ScaleX: 0.4, ScaleY: 0.4, Kernel: transform.KernelLanczos3, Premultiply: true, W: 400, H: 240},
				plan.ExtendStep{Y: 80, W: 400, H: 400, Width: 400, Height: 400, Gravity: plan.Gravity{Type: transform.GravityCenter}, OffsetScale: 1, Background: red},
				plan.FlattenStep{Background: *red},
			},
			plan.Output{Width: 400, Height: 400, Frames: 1, Format: transform.FormatPNG, DPRScale: 1},
		},
		{
			"entropy_and_filters_and_trim",
			transform.Pipeline{Resize: fill, Gravity: transform.Gravity{Type: transform.GravityEntropy}, Blur: 2, Pixelate: 4, Trim: &transform.Trim{Threshold: 10, EqualHor: true}},
			png,
			[]plan.Step{
				plan.ColourStep{},
				plan.TrimStep{Threshold: 10, EqualHor: true},
				plan.ResizeStep{ScaleX: 0.5, ScaleY: 0.5, Kernel: transform.KernelLanczos3, Premultiply: true, W: 500, H: 300},
				plan.CropStep{plan.CropGeometry{W: 300, H: 300, Width: 300, Height: 300, Gravity: plan.Gravity{Type: transform.GravityCenter}, OffsetScale: 1, Smart: plan.InterestingEntropy}},
				plan.FilterStep{Blur: 2, Pixelate: 4},
			},
			plan.Output{Width: 300, Height: 300, Frames: 1, Format: transform.FormatPNG, Alpha: true, DPRScale: 1},
		},
		{
			"face_gravity_falls_back_to_attention",
			transform.Pipeline{Crop: &transform.Crop{Width: px(100), Height: px(100)}, Gravity: transform.Gravity{Type: transform.GravityFace}},
			jpeg(1000, 600),
			[]plan.Step{
				plan.ColourStep{},
				plan.PreCropStep{plan.CropGeometry{W: 100, H: 100, Width: 100, Height: 100, Gravity: plan.Gravity{Type: transform.GravityCenter}, OffsetScale: 1, Smart: plan.InterestingAttention}},
				plan.FlattenStep{Background: transform.Color{R: 255, G: 255, B: 255, A: 255}},
			},
			plan.Output{Width: 100, Height: 100, Frames: 1, Format: transform.FormatJPEG, DPRScale: 1},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := plan.Build(req(tc.p), tc.meta, plan.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, p.Steps); diff != "" {
				t.Errorf("Steps (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.out, p.Output); diff != "" {
				t.Errorf("Output (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCost(t *testing.T) {
	t.Parallel()
	p, err := plan.Build(req(transform.Pipeline{Resize: transform.Resize{Width: px(400)}}), jpeg(4000, 3000), plan.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	// The shrunk-on-load 500×375 image is the peak.
	if want := (plan.Cost{Pixels: 500 * 375, EstimatedBytes: 500 * 375 * 3}); p.Cost != want {
		t.Errorf("Cost = %+v, want %+v", p.Cost, want)
	}
	p, err = plan.Build(req(transform.Pipeline{Rotate: 90}), plan.SourceMeta{Width: 100, Height: 50, HasAlpha: true, Format: "png"}, plan.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if want := (plan.Cost{Pixels: 5000, EstimatedBytes: 5000 * 4 * 2}); p.Cost != want {
		t.Errorf("random-access Cost = %+v, want %+v", p.Cost, want)
	}
	p, err = plan.Build(req(transform.Pipeline{}), gifMeta(5), plan.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Cost.Pixels != 200*150*5 {
		t.Errorf("animated Cost.Pixels = %d", p.Cost.Pixels)
	}
	// Scene-linear sources: float samples (×4), read twice (random access).
	p, err = plan.Build(req(transform.Pipeline{}), plan.SourceMeta{Width: 100, Height: 50, Format: "exr", HDR: true}, plan.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if want := (plan.Cost{Pixels: 5000, EstimatedBytes: 5000 * 3 * 4 * 2}); p.Cost != want || p.Load.Access != plan.AccessRandom {
		t.Errorf("HDR Cost = %+v, access %v; want %+v, random", p.Cost, p.Load.Access, want)
	}
}

func TestUnplanned(t *testing.T) {
	t.Parallel()
	p, err := plan.Build(req(transform.Pipeline{
		Border: &transform.Border{Top: 1}, Adjust: transform.Adjust{Contrast: new(1.5), Vibrance: new(1.2)}, Sepia: true,
		RotateAny: 12, Equalize: true,
	}), jpeg(10, 10), plan.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"vibrance", "equalize"}
	if !slices.Equal(p.Unplanned, want) {
		t.Errorf("Unplanned = %v, want %v", p.Unplanned, want)
	}
}

func TestRect(t *testing.T) {
	t.Parallel()
	c := plan.CropGeometry{Width: 100, Height: 0, Gravity: plan.Gravity{Type: transform.GravitySouthEast, X: 10, Y: 0.1}, OffsetScale: 2}
	for _, tc := range []struct {
		imgW, imgH       int
		x, y, w, h       int
		ok               bool
		name             string
		geometry         plan.CropGeometry
		extend           *plan.ExtendGeometry
		extendW, extendH int
	}{
		{name: "south_east_offsets", geometry: c, imgW: 400, imgH: 300, x: 280, y: 0, w: 100, h: 300, ok: true},
		{name: "covers_image", geometry: c, imgW: 80, imgH: 300, x: 0, y: 0, w: 80, h: 300},
		{name: "focus_point_clamped", geometry: plan.CropGeometry{Width: 50, Height: 50, Gravity: plan.Gravity{Type: transform.GravityFocusPoint, X: 0.99, Y: 0.01}}, imgW: 200, imgH: 100, x: 150, y: 0, w: 50, h: 50, ok: true},
		{name: "extend_north_west", extend: &plan.ExtendGeometry{Width: 300, Height: 0, Gravity: plan.Gravity{Type: transform.GravityNorthWest, X: 5, Y: 5}, OffsetScale: 1}, imgW: 200, imgH: 100, x: 5, y: 0, w: 300, h: 100, ok: true},
		{name: "extend_nothing_to_grow", extend: &plan.ExtendGeometry{Width: 100, Height: 100}, imgW: 200, imgH: 100, w: 200, h: 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var x, y, w, h int
			var ok bool
			if tc.extend != nil {
				x, y, w, h, ok = tc.extend.Rect(tc.imgW, tc.imgH)
			} else {
				x, y, w, h, ok = tc.geometry.Rect(tc.imgW, tc.imgH)
			}
			if got, want := [5]any{x, y, w, h, ok}, [5]any{tc.x, tc.y, tc.w, tc.h, tc.ok}; got != want {
				t.Errorf("Rect = %v, want %v", got, want)
			}
		})
	}
}
