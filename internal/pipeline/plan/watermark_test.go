package plan_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// TestWatermarkPlace checks imgproxy's calcPosition with overflow on a
// 200x150 frame and a 40x30 watermark.
func TestWatermarkPlace(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		g         plan.Gravity
		dpr       float64
		left, top int
	}{
		{name: "center", g: plan.Gravity{Type: transform.GravityCenter}, left: 80, top: 60},
		{name: "north", g: plan.Gravity{Type: transform.GravityNorth}, left: 80, top: 0},
		{name: "south", g: plan.Gravity{Type: transform.GravitySouth}, left: 80, top: 120},
		{name: "east", g: plan.Gravity{Type: transform.GravityEast}, left: 160, top: 60},
		{name: "west", g: plan.Gravity{Type: transform.GravityWest}, left: 0, top: 60},
		{name: "north-east offsets", g: plan.Gravity{Type: transform.GravityNorthEast, X: 10, Y: 5}, left: 150, top: 5},
		{name: "north-west offsets", g: plan.Gravity{Type: transform.GravityNorthWest, X: 10, Y: 5}, left: 10, top: 5},
		{name: "south-east offsets", g: plan.Gravity{Type: transform.GravitySouthEast, X: 10, Y: 5}, left: 150, top: 115},
		{name: "south-west offsets", g: plan.Gravity{Type: transform.GravitySouthWest, X: 10, Y: 5}, left: 10, top: 115},
		{name: "center offsets", g: plan.Gravity{Type: transform.GravityCenter, X: -10, Y: 20}, left: 70, top: 80},
		{name: "dpr scales pixels", g: plan.Gravity{Type: transform.GravityNorthWest, X: 10, Y: 5}, dpr: 2, left: 20, top: 10},
		{name: "fractional offsets", g: plan.Gravity{Type: transform.GravityNorthWest, X: 0.1, Y: 0.2}, dpr: 2, left: 20, top: 30},
		{name: "overflow right", g: plan.Gravity{Type: transform.GravityEast, X: -500}, left: 199, top: 60},
		{name: "overflow left", g: plan.Gravity{Type: transform.GravityWest, X: -30}, left: -30, top: 60},
		{name: "overflow keeps a pixel", g: plan.Gravity{Type: transform.GravityWest, X: -500, Y: -500}, left: -39, top: -29},
		{name: "focus point", g: plan.Gravity{Type: transform.GravityFocusPoint, X: 0.9, Y: 0.1}, left: 160, top: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dpr := tc.dpr
			if dpr == 0 {
				dpr = 1
			}
			w := plan.WatermarkGeometry{Position: tc.g, DPRScale: dpr}
			left, top := w.Place(200, 150, 40, 30)
			if left != tc.left || top != tc.top {
				t.Errorf("Place = (%d, %d), want (%d, %d)", left, top, tc.left, tc.top)
			}
		})
	}
}

func TestWatermarkScale(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		scale          float64
		frameW, frameH int
		wmW, wmH       int
		box            [2]int
		ok             bool
		fit            float64
	}{
		{name: "natural size", frameW: 200, frameH: 150, wmW: 40, wmH: 30, fit: 1},
		{name: "enlarge", scale: 0.5, frameW: 200, frameH: 150, wmW: 40, wmH: 30, box: [2]int{100, 75}, ok: true, fit: 2.5},
		{name: "height bound", scale: 0.5, frameW: 400, frameH: 100, wmW: 40, wmH: 30, box: [2]int{200, 50}, ok: true, fit: 50.0 / 30},
		{name: "shrink", scale: 0.1, frameW: 200, frameH: 150, wmW: 400, wmH: 300, box: [2]int{20, 15}, ok: true, fit: 0.05},
		{name: "at least one pixel", scale: 0.01, frameW: 50, frameH: 50, wmW: 10, wmH: 10, box: [2]int{1, 1}, ok: true, fit: 0.1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := plan.WatermarkGeometry{Scale: tc.scale, DPRScale: 1}
			bw, bh, ok := w.Box(tc.frameW, tc.frameH)
			if diff := cmp.Diff(tc.box, [2]int{bw, bh}); diff != "" || ok != tc.ok {
				t.Errorf("Box = %dx%d %v, want %v %v", bw, bh, ok, tc.box, tc.ok)
			}
			if got := w.FitScale(tc.frameW, tc.frameH, tc.wmW, tc.wmH); got != tc.fit {
				t.Errorf("FitScale = %v, want %v", got, tc.fit)
			}
		})
	}
}

func TestWatermarkTileSpacing(t *testing.T) {
	t.Parallel()
	w := plan.WatermarkGeometry{Position: plan.Gravity{Type: transform.GravityCenter, X: 10, Y: 0.1}, DPRScale: 2}
	if x, y := w.TileSpacing(200, 150); x != 20 || y != 15 {
		t.Errorf("TileSpacing = (%d, %d), want (20, 15)", x, y)
	}
}

// TestWatermarkSize checks watermark_size: a Width×Height box (either side
// derived from the aspect ratio), used only when Scale is 0.
func TestWatermarkSize(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		g     plan.WatermarkGeometry
		fit   float64
		ok    bool
		boxed bool
	}{
		{name: "width", g: plan.WatermarkGeometry{Width: 80}, fit: 2},
		{name: "height", g: plan.WatermarkGeometry{Height: 15}, fit: 0.5},
		{name: "both, width bound", g: plan.WatermarkGeometry{Width: 20, Height: 300}, fit: 0.5},
		{name: "both, height bound", g: plan.WatermarkGeometry{Width: 400, Height: 60}, fit: 2},
		{name: "scale wins", g: plan.WatermarkGeometry{Scale: 0.5, Width: 400}, fit: 2.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.g.FitScale(200, 150, 40, 30); got != tc.fit {
				t.Errorf("FitScale = %v, want %v", got, tc.fit)
			}
		})
	}
}

// TestBuildWatermark checks the steps the planner emits for the configured
// watermark, a per-request image and a text watermark, with DPR applied to
// pixel sizes, and the pipeline index of chained watermarks.
func TestBuildWatermark(t *testing.T) {
	t.Parallel()
	red := &transform.Color{R: 255, A: 255}
	r := req(transform.Pipeline{
		Resize: transform.Resize{Width: px(40), DPR: 2},
		Watermarks: []transform.Watermark{
			{Opacity: 0.5, Tile: transform.TileRepeat, Position: transform.Gravity{XOffset: 4, YOffset: 6}},
			{
				Opacity: 1, Position: transform.Gravity{Type: transform.GravitySouthEast}, Source: &transform.SourceSpec{Raw: "s3://b/k.png"},
				Width: 30, Height: 20, Rotate: 45, Shadow: 2,
			},
			{
				Opacity: 0.8, Tile: transform.TileChessboard, Text: "<b>Hi</b>", Font: "sans 20", Color: red, TextWidth: 50,
				Scale: 0.3, Width: 99,
			},
		},
	})
	p, err := plan.Build(r, jpeg(100, 80), plan.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	want := []plan.Step{
		plan.WatermarkStep{
			Opacity: 0.5, Frames: 1,
			Position: plan.Gravity{Type: transform.GravityCenter, X: 4, Y: 6}, Tile: transform.TileRepeat, DPRScale: 2,
		},
		plan.WatermarkStep{
			Kind: plan.WatermarkImage, Index: 1, Opacity: 1, Frames: 1,
			Position: plan.Gravity{Type: transform.GravitySouthEast}, DPRScale: 2,
			Width: 60, Height: 40, Rotate: 45, Shadow: 4,
		},
		plan.WatermarkStep{
			Kind: plan.WatermarkText, Index: 2, Opacity: 0.8, Frames: 1,
			Position: plan.Gravity{Type: transform.GravityCenter}, Tile: transform.TileChessboard, Scale: 0.3, DPRScale: 2,
			Text: &plan.TextWatermark{Markup: "<b>Hi</b>", Font: "sans 20", Color: red, Width: 100, DPI: 144},
		},
		plan.ColourStep{Stage: plan.ColourToResult},
	}
	if diff := cmp.Diff(want, p.After); diff != "" {
		t.Errorf("After (-want +got):\n%s", diff)
	}

	chained := req(transform.Pipeline{}, transform.Pipeline{Watermarks: []transform.Watermark{{Opacity: 1, Text: "x"}}})
	c, err := plan.Build(chained, jpeg(100, 80), plan.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Chained) != 1 {
		t.Fatalf("%d chained plans", len(c.Chained))
	}
	if st, ok := c.Chained[0].After[0].(plan.WatermarkStep); !ok || st.Pipeline != 1 || st.Index != 0 || st.Kind != plan.WatermarkText {
		t.Errorf("chained watermark step = %+v", c.Chained[0].After[0])
	}
}
