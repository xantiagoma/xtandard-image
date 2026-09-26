package exec

import (
	"bytes"
	"math"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

func rect(x0, y0, x1, y1 float64) plan.Outline {
	return plan.Outline{{X: x0, Y: y0}, {X: x1, Y: y0}, {X: x1, Y: y1}, {X: x0, Y: y1}}
}

func TestCoverageRect(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		o    plan.Outline
		w, h int
		want []byte
	}{
		{name: "whole frame", o: rect(0, 0, 3, 2), w: 3, h: 2, want: []byte{255, 255, 255, 255, 255, 255}},
		{name: "outside clipped", o: rect(-5, -5, 10, 10), w: 2, h: 1, want: []byte{255, 255}},
		{name: "half pixel columns", o: rect(0.5, 0, 2.5, 1), w: 3, h: 1, want: []byte{128, 255, 128}},
		{name: "half pixel row", o: rect(0, 0.5, 1, 1), w: 1, h: 1, want: []byte{128}},
		{name: "quarter pixel", o: rect(0.5, 0.5, 1, 1), w: 1, h: 1, want: []byte{64}},
		{name: "inside one pixel", o: rect(1.25, 0, 1.75, 1), w: 3, h: 1, want: []byte{0, 128, 0}},
		{name: "degenerate", o: plan.Outline{{X: 0, Y: 0}, {X: 1, Y: 1}}, w: 2, h: 2, want: []byte{0, 0, 0, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := coverage(tc.o, tc.w, tc.h)
			if !bytes.Equal(got, tc.want) {
				t.Errorf("coverage = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCoverageArea checks that the mask's total coverage matches the
// shape's area and that curved edges get intermediate alpha values.
func TestCoverageArea(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		o    plan.Outline
		w, h int
		area float64
	}{
		{name: "circle", o: (&plan.MaskStep{Shape: transform.MaskCircle}).Outline(101, 101), w: 101, h: 101, area: math.Pi * 50.5 * 50.5},
		{name: "ellipse", o: (&plan.MaskStep{Shape: transform.MaskEllipse}).Outline(200, 60), w: 200, h: 60, area: math.Pi * 100 * 30},
		{name: "rounded", o: (&plan.RadiusStep{TopLeft: 20, TopRight: 20, BottomRight: 20, BottomLeft: 20}).Outline(100, 80), w: 100, h: 80, area: 100*80 - (4-math.Pi)*400},
		{name: "triangle", o: plan.Outline{{X: 0, Y: 0}, {X: 64, Y: 0}, {X: 0, Y: 64}}, w: 64, h: 64, area: 64 * 64 / 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := coverage(tc.o, tc.w, tc.h)
			var sum float64
			edge := 0
			for _, v := range m {
				sum += float64(v) / 255
				if v > 0 && v < 255 {
					edge++
				}
			}
			if rel := math.Abs(sum-tc.area) / tc.area; rel > 0.002 {
				t.Errorf("covered area = %.1f, want %.1f (%.3f%% off)", sum, tc.area, rel*100)
			}
			if edge == 0 {
				t.Error("no anti-aliased edge pixels")
			}
		})
	}
}

// TestCoverageRoundedCorner checks one anti-aliased corner in detail:
// alpha rises monotonically along the diagonal from 0 outside the arc to
// 255 inside, with intermediate values where the arc crosses a pixel, and
// the straight edges stay exactly 255.
func TestCoverageRoundedCorner(t *testing.T) {
	t.Parallel()
	const w, h = 40, 40
	m := coverage((&plan.RadiusStep{TopLeft: 16}).Outline(w, h), w, h)
	at := func(x, y int) byte { return m[y*w+x] }
	if at(0, 0) != 0 || at(2, 2) != 0 {
		t.Errorf("corner alpha = %d, %d, want 0", at(0, 0), at(2, 2))
	}
	if at(20, 0) != 255 || at(0, 20) != 255 || at(39, 0) != 255 || at(39, 39) != 255 {
		t.Error("straight edges and the square corners must be fully opaque")
	}
	var prev byte
	partial := 0
	for i := range 16 {
		v := at(i, i)
		if v < prev {
			t.Errorf("diagonal alpha decreases at %d: %d after %d", i, v, prev)
		}
		if v > 0 && v < 255 {
			partial++
		}
		prev = v
	}
	if partial == 0 {
		t.Error("no intermediate alpha on the diagonal")
	}
	// The arc crosses the diagonal at 16 − 16/√2 ≈ 4.69: pixel 4 is
	// partly covered.
	if v := at(4, 4); v == 0 || v == 255 {
		t.Errorf("alpha at (4,4) = %d, want an intermediate value", v)
	}
	// Symmetry about the diagonal, within the vertical quantisation.
	for y := range 16 {
		for x := range 16 {
			if d := int(at(x, y)) - int(at(y, x)); d < -4 || d > 4 {
				t.Fatalf("alpha (%d,%d)=%d but (%d,%d)=%d", x, y, at(x, y), y, x, at(y, x))
			}
		}
	}
}

func BenchmarkCoverage(b *testing.B) {
	o := (&plan.RadiusStep{Max: true}).Outline(1920, 1080)
	b.ReportAllocs()
	for b.Loop() {
		coverage(o, 1920, 1080)
	}
}
