package plan_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

func TestCropAspectRatio(t *testing.T) {
	t.Parallel()
	nw := transform.Gravity{Type: transform.GravityNorthWest, XOffset: 30, YOffset: 40}
	for _, tc := range []struct {
		name string
		crop transform.Crop
		want [4]int // x, y, w, h of the PreCropStep; zero = none
	}{
		{name: "whole image shrinks", crop: transform.Crop{AspectRatio: 1}, want: [4]int{50, 0, 300, 300}},
		{name: "whole image enlarge fits back", crop: transform.Crop{AspectRatio: 1, AspectEnlarge: true}, want: [4]int{50, 0, 300, 300}},
		{name: "wide ratio", crop: transform.Crop{AspectRatio: 2}, want: [4]int{0, 50, 400, 200}},
		{name: "crop shrinks", crop: transform.Crop{Width: px(100), Height: px(200), AspectRatio: 1}, want: [4]int{150, 100, 100, 100}},
		{name: "crop enlarges", crop: transform.Crop{Width: px(100), Height: px(200), AspectRatio: 1, AspectEnlarge: true}, want: [4]int{100, 50, 200, 200}},
		{name: "enlarged beyond image", crop: transform.Crop{Width: px(100), Height: px(300), AspectRatio: 2, AspectEnlarge: true}, want: [4]int{0, 50, 400, 200}},
		// Gravity is not corrected (imgproxy docs: crop:100:200:nowe:30:40/crop_ar:1).
		{name: "gravity kept", crop: transform.Crop{Width: px(100), Height: px(200), Gravity: nw, AspectRatio: 1}, want: [4]int{30, 40, 100, 100}},
		{name: "matching ratio", crop: transform.Crop{AspectRatio: 4.0 / 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := tc.crop
			p, err := plan.Build(req(transform.Pipeline{Crop: &c}), jpeg(400, 300), plan.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			var got [4]int
			if pc := stepsOf[plan.PreCropStep](p.Steps); len(pc) == 1 {
				got = [4]int{pc[0].X, pc[0].Y, pc[0].W, pc[0].H}
			}
			if got != tc.want {
				t.Errorf("pre-crop = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRotatedSize(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		w, h  int
		angle float64
		ww    int
		wh    int
	}{
		{100, 50, 45, 106, 106},
		{100, 50, -45, 106, 106},
		{100, 50, 30, 112, 93},
		{100, 50, 180, 100, 50},
		{160, 120, 10, 178, 146},
		{1, 1, 45, 1, 1},
	} {
		t.Run(fmt.Sprint(tc.w, "x", tc.h, "@", tc.angle), func(t *testing.T) {
			t.Parallel()
			if w, h := plan.RotatedSize(tc.w, tc.h, tc.angle); w != tc.ww || h != tc.wh {
				t.Errorf("RotatedSize = %dx%d, want %dx%d", w, h, tc.ww, tc.wh)
			}
		})
	}
}

// TestGeometryExtrasOrder pins where the extras go in the step order and
// how they change the predicted size and alpha.
func TestGeometryExtrasOrder(t *testing.T) {
	t.Parallel()
	red := transform.Color{R: 255, A: 255}
	r := req(transform.Pipeline{
		Resize:    transform.Resize{Width: px(100), Height: px(50), Type: transform.ResizeFill, DPR: 2},
		Blur:      1,
		Sepia:     true,
		RotateAny: 45,
		Padding:   &transform.Padding{Top: 1, Right: 1, Bottom: 1, Left: 1},
		Mask:      &transform.Mask{Shape: transform.MaskCircle, Trim: true},
		Border:    &transform.Border{Top: 2, Right: 3, Bottom: 2, Left: 3, Color: red},
		Radius:    &transform.Radius{TopLeft: 5},
	})
	r.Output.Format = transform.FormatPNG
	p, err := plan.Build(r, jpeg(1000, 1000), plan.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	kinds := make([]string, 0, len(p.Steps))
	for _, s := range p.Steps {
		kinds = append(kinds, fmt.Sprintf("%T", s))
	}
	want := []string{
		"plan.ColourStep", "plan.ResizeStep", "plan.CropStep", "plan.FilterStep", "plan.ColourAdjustStep",
		"plan.RotateAnyStep", "plan.PaddingStep", "plan.MaskStep", "plan.BorderStep", "plan.RadiusStep",
	}
	if diff := cmp.Diff(want, kinds); diff != "" {
		t.Fatalf("steps (-want +got):\n%s", diff)
	}
	rot := stepsOf[plan.RotateAnyStep](p.Steps)[0]
	if rot.W != 212 || rot.H != 212 || rot.Angle != 45 || rot.Background != nil {
		t.Errorf("RotateAnyStep = %+v, want 212x212 at 45°, transparent", rot)
	}
	b := stepsOf[plan.BorderStep](p.Steps)[0]
	if b != (plan.BorderStep{Top: 4, Right: 6, Bottom: 4, Left: 6, Color: red}) {
		t.Errorf("BorderStep = %+v, want the widths × DPR 2", b)
	}
	if r := stepsOf[plan.RadiusStep](p.Steps)[0]; r != (plan.RadiusStep{TopLeft: 10}) {
		t.Errorf("RadiusStep = %+v, want TopLeft 10", r)
	}
	// 200×100 rotated: 212×212, + 2×2 padding = 216 (a square: the circle
	// trims nothing), plus the border.
	if got := [2]int{p.Output.Width, p.Output.Height}; got != [2]int{216 + 12, 216 + 8} {
		t.Errorf("output = %v", got)
	}
	if !p.Output.Alpha {
		t.Error("Output.Alpha = false, want true (PNG keeps the rounded corners)")
	}
}

func TestGeometryExtrasAlpha(t *testing.T) {
	t.Parallel()
	white := transform.Color{R: 255, G: 255, B: 255, A: 255}
	half := transform.Color{A: 128}
	for _, tc := range []struct {
		name    string
		p       transform.Pipeline
		out     transform.Format
		alpha   bool
		flatten bool
	}{
		{name: "radius png", p: transform.Pipeline{Radius: &transform.Radius{Max: true}}, out: transform.FormatPNG, alpha: true},
		{name: "radius jpeg flattens", p: transform.Pipeline{Radius: &transform.Radius{Max: true}}, out: transform.FormatJPEG, flatten: true},
		{name: "radius background", p: transform.Pipeline{Radius: &transform.Radius{TopLeft: 3}, Background: &white}, out: transform.FormatPNG, flatten: true},
		{name: "opaque border", p: transform.Pipeline{Border: &transform.Border{Top: 1, Color: white}}, out: transform.FormatPNG},
		{name: "translucent border", p: transform.Pipeline{Border: &transform.Border{Top: 1, Color: half}}, out: transform.FormatPNG, alpha: true},
		{name: "zero border", p: transform.Pipeline{Border: &transform.Border{Color: half}}, out: transform.FormatPNG},
		{name: "mask", p: transform.Pipeline{Mask: &transform.Mask{Shape: transform.MaskStar}}, out: transform.FormatPNG, alpha: true},
		{name: "mask opaque background", p: transform.Pipeline{Mask: &transform.Mask{Shape: transform.MaskStar, Background: &white}}, out: transform.FormatPNG},
		{name: "rotate transparent", p: transform.Pipeline{RotateAny: 10}, out: transform.FormatPNG, alpha: true},
		{name: "rotate background", p: transform.Pipeline{RotateAny: 10, Background: &white}, out: transform.FormatPNG, flatten: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := req(tc.p)
			r.Output.Format = tc.out
			p, err := plan.Build(r, jpeg(40, 30), plan.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			if p.Output.Alpha != tc.alpha {
				t.Errorf("Output.Alpha = %v, want %v", p.Output.Alpha, tc.alpha)
			}
			if got := len(stepsOf[plan.FlattenStep](p.Steps)) == 1; got != tc.flatten {
				t.Errorf("flatten = %v, want %v", got, tc.flatten)
			}
		})
	}
}

func TestMaskTrimRect(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		shape transform.MaskShape
		w, h  int
		want  [4]int
		ok    bool
	}{
		{shape: transform.MaskCircle, w: 200, h: 100, want: [4]int{50, 0, 100, 100}, ok: true},
		{shape: transform.MaskCircle, w: 100, h: 100, want: [4]int{0, 0, 100, 100}},
		{shape: transform.MaskEllipse, w: 200, h: 100, want: [4]int{0, 0, 200, 100}},
		{shape: transform.MaskStar, w: 200, h: 100, want: [4]int{47, 0, 106, 100}, ok: true},
		{shape: transform.MaskSquare, w: 100, h: 100, want: [4]int{0, 0, 100, 100}},
		{shape: transform.MaskTriangle, w: 100, h: 100, want: [4]int{0, 7, 100, 86}, ok: true},
		{shape: transform.MaskHeart, w: 300, h: 100, ok: true},
	} {
		t.Run(fmt.Sprint(tc.shape, tc.w, "x", tc.h), func(t *testing.T) {
			t.Parallel()
			m := plan.MaskStep{Shape: tc.shape, Trim: true}
			x, y, w, h, ok := m.TrimRect(tc.w, tc.h)
			if ok != tc.ok {
				t.Fatalf("TrimRect ok = %v, want %v (%d,%d %dx%d)", ok, tc.ok, x, y, w, h)
			}
			if tc.want != ([4]int{}) && [4]int{x, y, w, h} != tc.want {
				t.Errorf("TrimRect = %v, want %v", [4]int{x, y, w, h}, tc.want)
			}
			// Every vertex of the outline lies inside the frame, give or
			// take the rounding of wsrv's bounding box.
			for _, p := range m.Outline(tc.w, tc.h) {
				if p.X < -1 || p.Y < -1 || p.X > float64(tc.w)+1 || p.Y > float64(tc.h)+1 {
					t.Fatalf("vertex %+v outside %dx%d", p, tc.w, tc.h)
				}
			}
		})
	}
}

func TestRadiusOutline(t *testing.T) {
	t.Parallel()
	// Radii larger than the sides scale down together (CSS): 100 + 300 on
	// a 200 px side become 50 + 150.
	r := plan.RadiusStep{TopLeft: 100, TopRight: 300}
	o := r.Outline(200, 400)
	if math.Abs(o[0].X) > 1e-9 || math.Abs(o[0].Y-50) > 1e-9 {
		t.Errorf("first vertex = %+v, want (0, 50)", o[0])
	}
	minX, maxX := math.Inf(1), math.Inf(-1)
	for _, p := range o {
		minX, maxX = min(minX, p.X), max(maxX, p.X)
	}
	if minX != 0 || math.Abs(maxX-200) > 1e-9 {
		t.Errorf("x extent = %v..%v, want 0..200", minX, maxX)
	}
	// Square corners stay exact.
	sq := (&plan.RadiusStep{TopRight: 4}).Outline(10, 10)
	if last := sq[len(sq)-1]; last != (plan.Point{X: 0, Y: 10}) {
		t.Errorf("bottom-left vertex = %+v, want (0, 10)", last)
	}
}
