package plan_test

import (
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// TestColourAdjust pins the ColourAdjustStep: after the filters and before
// the canvas grows, alpha added by opacity, vibrance left unplanned.
func TestColourAdjust(t *testing.T) {
	t.Parallel()
	white := transform.Color{R: 255, G: 255, B: 255, A: 255}
	p, err := plan.Build(req(transform.Pipeline{
		Blur:    2,
		Adjust:  transform.Adjust{Brightness: new(10.0), Vibrance: new(2.0)},
		Sepia:   true,
		Padding: &transform.Padding{Top: 4},
	}), jpeg(10, 10), plan.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	want := []plan.Step{
		plan.ColourStep{},
		plan.FilterStep{Blur: 2},
		plan.ColourAdjustStep{Adjust: transform.Adjust{Brightness: new(10.0)}, Sepia: true},
		plan.PaddingStep{Top: 4},
		plan.FlattenStep{Background: white},
	}
	if diff := cmp.Diff(want, p.Steps); diff != "" {
		t.Errorf("Steps (-want +got):\n%s", diff)
	}
	if !slices.Equal(p.Unplanned, []string{"vibrance"}) {
		t.Errorf("Unplanned = %v, want [vibrance]", p.Unplanned)
	}

	p, err = plan.Build(req(transform.Pipeline{Adjust: transform.Adjust{Opacity: new(0.5)}}),
		plan.SourceMeta{Width: 10, Height: 10, Format: "png"}, plan.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Output.Alpha {
		t.Error("opacity: Output.Alpha = false, want true")
	}

	p, err = plan.Build(req(transform.Pipeline{Adjust: transform.Adjust{Vibrance: new(2.0)}}), jpeg(10, 10), plan.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range p.Steps {
		if _, ok := s.(plan.ColourAdjustStep); ok {
			t.Error("vibrance alone planned a ColourAdjustStep")
		}
	}
}

// TestColourAdjustAnimated: the step runs per frame, like the filters.
func TestColourAdjustAnimated(t *testing.T) {
	t.Parallel()
	p, err := plan.Build(req(transform.Pipeline{Negate: true}), gifMeta(4), plan.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Animation == nil {
		t.Fatal("not animated")
	}
	if !slices.ContainsFunc(p.Steps, func(s plan.Step) bool { _, ok := s.(plan.ColourAdjustStep); return ok }) {
		t.Errorf("Steps = %v, want a ColourAdjustStep", p.Steps)
	}
}

// TestBackgroundAlpha: a translucent background keeps alpha in formats that
// have it and is made opaque in the others.
func TestBackgroundAlpha(t *testing.T) {
	t.Parallel()
	bg := transform.Color{R: 10, G: 20, B: 30, A: 128}
	for _, tc := range []struct {
		format transform.Format
		want   transform.Color
		alpha  bool
	}{
		{transform.FormatPNG, bg, true},
		{transform.FormatJPEG, transform.Color{R: 10, G: 20, B: 30, A: 255}, false},
	} {
		t.Run(tc.format.String(), func(t *testing.T) {
			t.Parallel()
			r := req(transform.Pipeline{Background: &bg})
			r.Output.Format = tc.format
			p, err := plan.Build(r, plan.SourceMeta{Width: 10, Height: 10, HasAlpha: true, Format: "png"}, plan.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			last := p.Steps[len(p.Steps)-1]
			if diff := cmp.Diff(plan.Step(plan.FlattenStep{Background: tc.want}), last); diff != "" {
				t.Errorf("last step (-want +got):\n%s", diff)
			}
			if p.Output.Alpha != tc.alpha {
				t.Errorf("Output.Alpha = %v, want %v", p.Output.Alpha, tc.alpha)
			}
		})
	}
}
