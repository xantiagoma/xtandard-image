package plan_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

func rel(f float64) transform.Dim { return transform.Dim{Rel: f} }

// TestChainPlansObservedInput: a chained pipeline's geometry follows the
// input it is given, not Build's prediction of it.
func TestChainPlansObservedInput(t *testing.T) {
	t.Parallel()
	r := req(
		transform.Pipeline{Resize: transform.Resize{Width: px(300)}},
		transform.Pipeline{Crop: &transform.Crop{Width: rel(0.5), Height: rel(0.5)}},
		transform.Pipeline{Resize: transform.Resize{Type: transform.ResizeFit, Width: px(50), Height: px(50)}},
	)
	p, err := plan.Build(r, jpeg(600, 400), plan.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if got := size(p); got != [2]int{50, 33} {
		t.Fatalf("predicted size = %v", got)
	}
	predicted := stepsOf[plan.PreCropStep](p.Chained[0].Steps)
	if len(predicted) != 1 || predicted[0].W != 150 || predicted[0].H != 100 {
		t.Fatalf("predicted crop = %+v, want 150x100 of the predicted 300x200", predicted)
	}

	// The executor saw 240x160 (for instance after a trim in pipeline 0).
	c, err := plan.Chain(r, jpeg(600, 400), plan.Limits{}, 1, plan.Input{Width: 240, Height: 160}, nil)
	if err != nil {
		t.Fatal(err)
	}
	crops := stepsOf[plan.PreCropStep](c.Steps)
	if len(crops) != 1 {
		t.Fatalf("crops = %+v", crops)
	}
	if got := [4]int{crops[0].X, crops[0].Y, crops[0].W, crops[0].H}; got != [4]int{60, 40, 120, 80} {
		t.Errorf("crop = %v, want 60,40 120x80 (half of the observed 240x160, centred)", got)
	}
	if c.Output.Width != 120 || c.Output.Height != 80 {
		t.Errorf("output %dx%d, want 120x80", c.Output.Width, c.Output.Height)
	}
	if c.Load != (plan.LoadStep{}) || len(c.Chained) != 0 {
		t.Errorf("chained plan has Load %+v and %d chained", c.Load, len(c.Chained))
	}
	if got := stepsOf[plan.RotateStep](c.Steps); len(got) != 0 {
		t.Errorf("chained plan rotates %+v: orientation belongs to the first pipeline", got)
	}

	last, err := plan.Chain(r, jpeg(600, 400), plan.Limits{}, 2, plan.Input{Width: 120, Height: 80}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rs := stepsOf[plan.ResizeStep](last.Steps)
	if len(rs) != 1 || rs[0].W != 50 || rs[0].H != 33 {
		t.Errorf("last resize = %+v, want 50x33", rs)
	}
	if diff := cmp.Diff([]plan.Step{plan.ColourStep{Stage: plan.ColourToResult}}, last.After); diff != "" {
		t.Errorf("last After (-want +got):\n%s", diff)
	}
}

// TestChainOrientationReset: EXIF orientation is applied once, by the first
// pipeline; a chained pipeline sees an upright image.
func TestChainOrientationReset(t *testing.T) {
	t.Parallel()
	r := req(transform.Pipeline{}, transform.Pipeline{Resize: transform.Resize{Width: px(100)}})
	meta := jpeg(400, 300)
	meta.Orientation = 6
	p, err := plan.Build(r, meta, plan.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if got := stepsOf[plan.RotateStep](p.Steps); len(got) != 1 {
		t.Errorf("root rotates %+v, want once", got)
	}
	if got := stepsOf[plan.RotateStep](p.Chained[0].Steps); len(got) != 0 {
		t.Errorf("chained rotates %+v", got)
	}
	if got := size(p); got != [2]int{100, 133} {
		t.Errorf("size = %v, want 100x133 (upright 300x400)", got)
	}
}

// TestChainTrimmed: a trim inside a chained pipeline is continued with the
// trimmed size, like the root's.
func TestChainTrimmed(t *testing.T) {
	t.Parallel()
	r := req(
		transform.Pipeline{Resize: transform.Resize{Width: px(200)}},
		transform.Pipeline{Trim: &transform.Trim{Threshold: 10}, Resize: transform.Resize{Type: transform.ResizeFit, Width: px(50), Height: px(50)}},
	)
	in := plan.Input{Width: 200, Height: 100}
	before, err := plan.Chain(r, jpeg(400, 200), plan.Limits{}, 1, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !before.Materialize {
		t.Error("trim does not materialise its input")
	}
	after, err := plan.Chain(r, jpeg(400, 200), plan.Limits{}, 1, in, &plan.Observed{Width: 100, Height: 80, Trimmed: true})
	if err != nil {
		t.Fatal(err)
	}
	rs := stepsOf[plan.ResizeStep](after.Steps)
	if len(rs) != 1 || rs[0].ScaleX != 0.5 || rs[0].W != 50 || rs[0].H != 40 {
		t.Errorf("resize after trim = %+v, want ×0.5 to 50x40", rs)
	}
}

func TestChainMaterialize(t *testing.T) {
	t.Parallel()
	first := transform.Pipeline{Resize: transform.Resize{Width: px(300)}}
	tests := []struct {
		name string
		p    transform.Pipeline
		want bool
	}{
		{"resize_lazy", transform.Pipeline{Resize: transform.Resize{Width: px(100)}}, false},
		{"watermark_lazy", transform.Pipeline{Watermarks: []transform.Watermark{{Opacity: 0.5}}}, false},
		{"crop_lazy", transform.Pipeline{Crop: &transform.Crop{Width: px(100), Height: px(100)}}, false},
		{"rotate", transform.Pipeline{Rotate: 90}, true},
		{"blur", transform.Pipeline{Blur: 2}, true},
		{"trim", transform.Pipeline{Trim: &transform.Trim{Threshold: 10}}, true},
		{"smart_crop", transform.Pipeline{Crop: &transform.Crop{Width: px(100), Height: px(100), Gravity: transform.Gravity{Type: transform.GravitySmart}}}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := plan.Build(req(first, tc.p), jpeg(600, 400), plan.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			if got := p.Chained[0].Materialize; got != tc.want {
				t.Errorf("Materialize = %v, want %v", got, tc.want)
			}
			if p.Materialize {
				t.Error("root plan Materialize is set")
			}
		})
	}
}

// TestChainCost: the root's cost covers every pipeline plus the materialised
// results still held while it runs, and the memory budget applies to it.
func TestChainCost(t *testing.T) {
	t.Parallel()
	blur := transform.Pipeline{Blur: 1}
	one, err := plan.Build(req(transform.Pipeline{Resize: transform.Resize{Width: px(500)}}), jpeg(500, 500), plan.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	r := req(transform.Pipeline{Resize: transform.Resize{Width: px(500)}}, blur, blur, blur)
	p, err := plan.Build(r, jpeg(500, 500), plan.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	// Pipelines 1..3 each blur a materialised 500x500 RGB copy (×2 for
	// random access); 3 runs while the copies made for 1 and 2 are held.
	const img = 500 * 500 * 3
	if want := int64(2*img + 2*img); p.Cost.EstimatedBytes != want {
		t.Errorf("EstimatedBytes = %d, want %d", p.Cost.EstimatedBytes, want)
	}
	if p.Cost.EstimatedBytes <= one.Cost.EstimatedBytes {
		t.Errorf("chained cost %d ≤ single %d", p.Cost.EstimatedBytes, one.Cost.EstimatedBytes)
	}
	_, err = plan.Build(r, jpeg(500, 500), plan.Limits{MemoryBudget: p.Cost.EstimatedBytes - 1})
	if errs.KindOf(err) != errs.KindLimitExceeded {
		t.Errorf("over budget: err = %v, want KindLimitExceeded", err)
	}
}

// TestChainAnimation: a chained pipeline processes every frame of an
// animated result.
func TestChainAnimation(t *testing.T) {
	t.Parallel()
	r := req(
		transform.Pipeline{Resize: transform.Resize{Type: transform.ResizeFill, Width: px(64), Height: px(48)}},
		transform.Pipeline{Crop: &transform.Crop{Width: px(48), Height: px(48)}},
	)
	r.Output.Format = transform.FormatGIF
	meta := plan.SourceMeta{Width: 96, Height: 72 * 6, PageHeight: 72, Frames: 6, IsAnimated: true, Format: "gif"}
	p, err := plan.Build(r, meta, plan.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	c := p.Chained[0]
	if c.Animation == nil || c.Animation.Frames != 6 || c.Animation.PageHeight != 48 {
		t.Errorf("chained animation = %+v, want 6 frames of 48", c.Animation)
	}
	if c.Output.Frames != 6 || c.Output.Width != 48 || c.Output.Height != 48 {
		t.Errorf("chained output = %+v", c.Output)
	}
	again, err := plan.Chain(r, meta, plan.Limits{}, 1, plan.Input{Width: 64, Height: 48, Frames: 6}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(c, again); diff != "" {
		t.Errorf("Chain on the predicted input differs from Build (-build +chain):\n%s", diff)
	}
}

func TestChainErrors(t *testing.T) {
	t.Parallel()
	r := req(transform.Pipeline{}, transform.Pipeline{Resize: transform.Resize{Width: px(10)}})
	in := plan.Input{Width: 10, Height: 10}
	tests := []struct {
		name string
		req  *transform.Request
		i    int
		in   plan.Input
		obs  *plan.Observed
		want errs.Kind
	}{
		{"nil_request", nil, 1, in, nil, errs.KindInternal},
		{"root_index", r, 0, in, nil, errs.KindInternal},
		{"past_the_end", r, 2, in, nil, errs.KindInternal},
		{"empty_input", r, 1, plan.Input{}, nil, errs.KindCorrupt},
		{"huge_input", r, 1, plan.Input{Width: 20_000_000, Height: 1}, nil, errs.KindCorrupt},
		{"not_trimmed", r, 1, in, &plan.Observed{Width: 5, Height: 5, Loaded: true}, errs.KindCorrupt},
		{"empty_trim", r, 1, in, &plan.Observed{Trimmed: true}, errs.KindCorrupt},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := plan.Chain(tc.req, jpeg(10, 10), plan.Limits{}, tc.i, tc.in, tc.obs)
			if got := errs.KindOf(err); got != tc.want {
				t.Errorf("err = %v (kind %v), want kind %v", err, got, tc.want)
			}
		})
	}
}
