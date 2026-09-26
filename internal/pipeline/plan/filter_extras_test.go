package plan_test

import (
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// filterOf is the FilterStep of p, nil when there is none.
func filterOf(t *testing.T, p *plan.Plan) *plan.FilterStep {
	t.Helper()
	fs := stepsOf[plan.FilterStep](p.Steps)
	switch len(fs) {
	case 0:
		return nil
	case 1:
		return &fs[0]
	}
	t.Fatalf("%d FilterSteps", len(fs))
	return nil
}

// TestUnsharpMode pins when unsharp masking applies: auto (also the
// default for unsharp_masking) only after a downscale and without sharpen;
// the classic parameters (imgix usm) default to always.
func TestUnsharpMode(t *testing.T) {
	t.Parallel()
	down := transform.Resize{Width: px(50)}
	for _, tc := range []struct {
		name    string
		p       transform.Pipeline
		want    *plan.UnsharpMask
		sharpen float64
	}{
		{name: "auto without resize", p: transform.Pipeline{Unsharp: &transform.Unsharp{Mode: transform.UnsharpAuto}}},
		{
			name: "auto downscaled", p: transform.Pipeline{Resize: down, Unsharp: &transform.Unsharp{Mode: transform.UnsharpAuto}},
			want: &plan.UnsharpMask{Weight: 1, Divider: 24},
		},
		{
			name: "auto upscale", p: transform.Pipeline{
				Resize: transform.Resize{Width: px(200), Enlarge: true}, Unsharp: &transform.Unsharp{Mode: transform.UnsharpAuto},
			},
		},
		{
			name: "auto with sharpen", p: transform.Pipeline{Resize: down, Sharpen: 1, Unsharp: &transform.Unsharp{Mode: transform.UnsharpAuto}},
			sharpen: 1,
		},
		{
			name: "unset mode is auto", p: transform.Pipeline{Resize: down, Unsharp: &transform.Unsharp{Weight: 2}},
			want: &plan.UnsharpMask{Weight: 2, Divider: 24},
		},
		{name: "unset mode without resize", p: transform.Pipeline{Unsharp: &transform.Unsharp{Weight: 2}}},
		{
			name: "always", p: transform.Pipeline{Unsharp: &transform.Unsharp{Mode: transform.UnsharpAlways, Divider: 8}},
			want: &plan.UnsharpMask{Weight: 1, Divider: 8},
		},
		{name: "none", p: transform.Pipeline{Resize: down, Unsharp: &transform.Unsharp{Mode: transform.UnsharpNone}}},
		{
			name: "classic", p: transform.Pipeline{Unsharp: &transform.Unsharp{Sigma: 2.5, Amount: 0.4}},
			want: &plan.UnsharpMask{Sigma: 2.5, Amount: 0.4},
		},
		{
			name: "classic default sigma", p: transform.Pipeline{Unsharp: &transform.Unsharp{Amount: -0.2}},
			want: &plan.UnsharpMask{Sigma: 1, Amount: -0.2},
		},
		{name: "classic without amount", p: transform.Pipeline{Unsharp: &transform.Unsharp{Sigma: 2.5}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := plan.Build(req(tc.p), plan.SourceMeta{Width: 100, Height: 100, Format: "png"}, plan.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			fs := filterOf(t, p)
			var got *plan.UnsharpMask
			if fs != nil {
				got = fs.Unsharp
				if fs.Sharpen != tc.sharpen {
					t.Errorf("Sharpen = %v, want %v", fs.Sharpen, tc.sharpen)
				}
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("Unsharp (-want +got):\n%s", diff)
			}
			if len(p.Unplanned) != 0 {
				t.Errorf("Unplanned = %v", p.Unplanned)
			}
		})
	}
	// A JPEG downscaled on load alone counts too.
	p, err := plan.Build(req(transform.Pipeline{
		Resize: transform.Resize{Width: px(25)}, Unsharp: &transform.Unsharp{Mode: transform.UnsharpAuto},
	}), jpeg(100, 100), plan.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Load.ShrinkOnLoad != 4 {
		t.Fatalf("ShrinkOnLoad = %d, want 4", p.Load.ShrinkOnLoad)
	}
	if fs := filterOf(t, p); fs == nil || fs.Unsharp == nil {
		t.Error("shrink-on-load: no unsharp mask")
	}
}

// relRect is a test copy of the planner's rectangle maths: clockwise
// rotation and flips of a rectangle in fractions of the image.
type relRect struct{ x0, y0, x1, y1 float64 }

func (r relRect) rot(angle int) relRect {
	switch angle {
	case 90:
		return relRect{1 - r.y1, r.x0, 1 - r.y0, r.x1}
	case 180:
		return relRect{1 - r.x1, 1 - r.y1, 1 - r.x0, 1 - r.y0}
	case 270:
		return relRect{r.y0, 1 - r.x1, r.y1, 1 - r.x0}
	}
	return r
}

func (r relRect) area() transform.Area {
	return transform.Area{Left: r.x0, Top: r.y0, Width: r.x1 - r.x0, Height: r.y1 - r.y0}
}

// TestBlurAreasMapping checks that blur areas, given relative to the
// oriented source, land on the same content after EXIF orientation, resize,
// crops, the rotate and flip options, and are clipped or dropped outside.
func TestBlurAreasMapping(t *testing.T) {
	t.Parallel()
	approx := cmpopts.EquateApprox(0, 1e-9)
	area := relRect{0.1, 0.2, 0.4, 0.3}
	ba := func(a ...transform.Area) *transform.BlurAreas { return &transform.BlurAreas{Sigma: 3, Areas: a} }

	// Orientation alone: the areas come out where they went in; the
	// rotate option turns them with the image.
	for o := 1; o <= 8; o++ {
		for _, rot := range []int{0, 90, 180, 270} {
			t.Run("orientation "+strconv.Itoa(o)+" rotate "+strconv.Itoa(rot), func(t *testing.T) {
				t.Parallel()
				meta := plan.SourceMeta{Width: 400, Height: 200, Orientation: o, Format: "png"}
				p, err := plan.Build(req(transform.Pipeline{
					Resize: transform.Resize{Width: px(100)}, Rotate: rot, BlurAreas: ba(area.area()),
				}), meta, plan.Limits{})
				if err != nil {
					t.Fatal(err)
				}
				fs := filterOf(t, p)
				if fs == nil || fs.BlurAreas == nil {
					t.Fatal("no blur areas")
				}
				want := []transform.Area{area.rot(rot).area()}
				if diff := cmp.Diff(want, fs.BlurAreas.Areas, approx); diff != "" {
					t.Errorf("Areas (-want +got):\n%s", diff)
				}
			})
		}
	}

	for _, tc := range []struct {
		name string
		p    transform.Pipeline
		want []transform.Area // nil: no blur areas
	}{
		{
			name: "flip", p: transform.Pipeline{Flip: transform.Flip{H: true}, BlurAreas: ba(area.area())},
			want: []transform.Area{{Left: 0.6, Top: 0.2, Width: 0.3, Height: 0.1}},
		},
		{
			name: "crop clips", p: transform.Pipeline{
				Crop:      &transform.Crop{Width: px(50), Gravity: transform.Gravity{Type: transform.GravityWest}},
				BlurAreas: ba(transform.Area{Left: 0.25, Top: 0, Width: 0.5, Height: 1}),
			},
			want: []transform.Area{{Left: 0.5, Top: 0, Width: 0.5, Height: 1}},
		},
		{
			name: "crop drops", p: transform.Pipeline{
				Crop:      &transform.Crop{Width: px(50), Gravity: transform.Gravity{Type: transform.GravityWest}},
				BlurAreas: ba(transform.Area{Left: 0.6, Top: 0, Width: 0.3, Height: 1}, transform.Area{Left: 0, Top: 0.5, Width: 0.1, Height: 0.5}),
			},
			want: []transform.Area{{Left: 0, Top: 0.5, Width: 0.2, Height: 0.5}},
		},
		{
			name: "crop drops all", p: transform.Pipeline{
				Crop:      &transform.Crop{Width: px(50), Gravity: transform.Gravity{Type: transform.GravityWest}},
				BlurAreas: ba(transform.Area{Left: 0.6, Top: 0, Width: 0.3, Height: 1}),
			},
		},
		{
			name: "fill crop", p: transform.Pipeline{
				Resize:    transform.Resize{Type: transform.ResizeFill, Width: px(50), Height: px(100)},
				BlurAreas: ba(transform.Area{Left: 0.5, Top: 0, Width: 0.5, Height: 0.5}),
			},
			// The 100×100 source is cropped to its centre half, x 26..76
			// (imgproxy rounds the centre up): x 50..100 becomes 24..50.
			want: []transform.Area{{Left: 0.48, Top: 0, Width: 0.52, Height: 0.5}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := plan.Build(req(tc.p), plan.SourceMeta{Width: 100, Height: 100, Format: "png"}, plan.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			var got []transform.Area
			if fs := filterOf(t, p); fs != nil && fs.BlurAreas != nil {
				got = fs.BlurAreas.Areas
			}
			if diff := cmp.Diff(tc.want, got, approx); diff != "" {
				t.Errorf("Areas (-want +got):\n%s", diff)
			}
		})
	}
}

// TestFilterExtrasOrder pins the FilterStep with every filter: one step,
// before colour adjustments; progressive blur copied from the request.
func TestFilterExtrasOrder(t *testing.T) {
	t.Parallel()
	pb := &transform.ProgressiveBlur{Sigma: 5, Direction: transform.DirectionRight, Start: 0.2, Stop: 0.8}
	p, err := plan.Build(req(transform.Pipeline{
		Blur: 1, Pixelate: 3, Negate: true,
		Unsharp:         &transform.Unsharp{Mode: transform.UnsharpAlways},
		BlurAreas:       &transform.BlurAreas{Sigma: 2, Areas: []transform.Area{{Width: 0.5, Height: 0.5}}},
		ProgressiveBlur: pb,
	}), plan.SourceMeta{Width: 10, Height: 10, Format: "png"}, plan.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	want := []plan.Step{
		plan.ColourStep{},
		plan.FilterStep{
			Blur: 1, Pixelate: 3,
			Unsharp:         &plan.UnsharpMask{Weight: 1, Divider: 24},
			BlurAreas:       &plan.BlurAreas{Sigma: 2, Areas: []transform.Area{{Width: 0.5, Height: 0.5}}},
			ProgressiveBlur: pb,
		},
		plan.ColourAdjustStep{Negate: true},
	}
	if diff := cmp.Diff(want, p.Steps); diff != "" {
		t.Errorf("Steps (-want +got):\n%s", diff)
	}
	if p.Load.Access != plan.AccessRandom {
		t.Error("filters did not ask for random access")
	}
}
