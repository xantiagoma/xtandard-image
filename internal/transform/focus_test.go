package transform

import (
	"math"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestFocusFromPixels(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		x1, y1, x2, y2 int
		want           *FocusRect // nil: rejected
		cx, cy         int
	}{
		{"point", 7, 9, 7, 9, &FocusRect{7, 9, 7, 9}, 7, 9},
		{"thumbor docs focal", 146, 206, 279, 360, &FocusRect{146, 206, 279, 360}, 212, 283},
		{"corners swapped", 279, 360, 146, 206, &FocusRect{146, 206, 279, 360}, 212, 283},
		{"mixed corners", 10, 0, 0, 10, &FocusRect{0, 0, 10, 10}, 5, 5},
		{"odd width rounds down", 0, 0, 3, 5, &FocusRect{0, 0, 3, 5}, 1, 2},
		{"max", MaxSourceCoord, 0, MaxSourceCoord, 0, &FocusRect{MaxSourceCoord, 0, MaxSourceCoord, 0}, MaxSourceCoord, 0},
		{"negative", -1, 0, 4, 4, nil, 0, 0},
		{"above max", 0, 0, 4, MaxSourceCoord + 1, nil, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g, ok := FocusFromPixels(tc.x1, tc.y1, tc.x2, tc.y2)
			if ok != (tc.want != nil) {
				t.Fatalf("ok = %v, want %v", ok, tc.want != nil)
			}
			if !ok {
				return
			}
			if diff := cmp.Diff(Gravity{Type: GravityFocusPoint, FocusPx: tc.want}, g); diff != "" {
				t.Fatalf("gravity (-want +got):\n%s", diff)
			}
			if x, y := g.FocusPx.Center(); x != tc.cx || y != tc.cy {
				t.Errorf("Center = %d,%d, want %d,%d", x, y, tc.cx, tc.cy)
			}
		})
	}
}

func TestFocusRectFraction(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		r      FocusRect
		w, h   int
		fx, fy float64
	}{
		{"centre", FocusRect{50, 30, 50, 30}, 100, 60, 0.5, 0.5},
		{"origin", FocusRect{}, 100, 60, 0, 0},
		{"far corner", FocusRect{100, 60, 100, 60}, 100, 60, 1, 1},
		{"outside clamps", FocusRect{500, 500, 900, 900}, 100, 60, 1, 1},
		{"rectangle centre", FocusRect{146, 206, 279, 360}, 1000, 800, 0.212, 283.0 / 800},
		{"no size", FocusRect{5, 5, 5, 5}, 0, -1, 0.5, 0.5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if fx, fy := tc.r.Fraction(tc.w, tc.h); fx != tc.fx || fy != tc.fy {
				t.Errorf("Fraction = %v,%v, want %v,%v", fx, fy, tc.fx, tc.fy)
			}
		})
	}
}

// FuzzFocusFromPixels checks the dialect helper: it never panics, accepts
// exactly the coordinates Validate accepts, is independent of the corner
// order, and its fraction is always inside the image.
func FuzzFocusFromPixels(f *testing.F) {
	f.Add(146, 206, 279, 360, 1000, 800)
	f.Add(0, 0, 0, 0, 1, 1)
	f.Add(-1, 5, 5, 5, 10, 10)
	f.Add(MaxSourceCoord, MaxSourceCoord, 0, 0, MaxSourceCoord, 3)
	f.Add(math.MaxInt, math.MinInt, 3, 4, 0, -7)
	f.Fuzz(func(t *testing.T, x1, y1, x2, y2, w, h int) {
		g, ok := FocusFromPixels(x1, y1, x2, y2)
		inRange := func(v int) bool { return v >= 0 && v <= MaxSourceCoord }
		if want := inRange(x1) && inRange(y1) && inRange(x2) && inRange(y2); ok != want {
			t.Fatalf("ok = %v, want %v", ok, want)
		}
		if !ok {
			return
		}
		if g2, _ := FocusFromPixels(x2, y2, x1, y1); !cmp.Equal(g, g2) {
			t.Fatalf("corner order changes the result: %+v vs %+v", g.FocusPx, g2.FocusPx)
		}
		r := Request{Source: SourceSpec{Raw: "https://example.com/a.jpg"}, Pipelines: []Pipeline{{Gravity: g}}}
		r.ApplyDefaults(StandardDefaults())
		if err := r.Validate(); err != nil {
			t.Fatalf("Validate: %v", err)
		}
		cx, cy := g.FocusPx.Center()
		if cx < g.FocusPx.X1 || cx > g.FocusPx.X2 || cy < g.FocusPx.Y1 || cy > g.FocusPx.Y2 {
			t.Fatalf("Center %d,%d outside %+v", cx, cy, *g.FocusPx)
		}
		fx, fy := g.FocusPx.Fraction(w, h)
		if !(fx >= 0 && fx <= 1 && fy >= 0 && fy <= 1) {
			t.Fatalf("Fraction(%d, %d) = %v,%v", w, h, fx, fy)
		}
	})
}
