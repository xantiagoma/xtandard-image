package plan

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

func TestInfoCrop(t *testing.T) {
	t.Parallel()
	px := func(n int) transform.Dim { return transform.Dim{Px: n} }
	for _, tc := range []struct {
		name         string
		c            transform.Crop
		x, y, w, h   int
		approximated bool
	}{
		// calcPosition centres on an even offset: shrinkDimEven(400-50+1, 2) = 176.
		{"centre by default", transform.Crop{Width: px(100), Height: px(50)}, 150, 176, 100, 50, false},
		{"north-west", transform.Crop{Width: px(100), Height: px(50), Gravity: transform.Gravity{Type: transform.GravityNorthWest}}, 0, 0, 100, 50, false},
		{"south-east with offsets", transform.Crop{Width: px(100), Height: px(50), Gravity: transform.Gravity{Type: transform.GravitySouthEast, XOffset: 10, YOffset: 20}}, 290, 330, 100, 50, false},
		{"relative size", transform.Crop{Width: transform.Dim{Rel: 0.5}, Height: transform.Dim{Rel: 0.25}}, 100, 150, 200, 100, false},
		{"width only", transform.Crop{Width: px(40)}, 180, 0, 40, 400, false},
		{"larger than the image", transform.Crop{Width: px(900), Height: px(900)}, 0, 0, 400, 400, false},
		{"focus point", transform.Crop{Width: px(100), Height: px(100), Gravity: transform.Gravity{Type: transform.GravityFocusPoint, FocusX: 1, FocusY: 0}}, 300, 0, 100, 100, false},
		{"smart is centred", transform.Crop{Width: px(100), Height: px(50), Gravity: transform.Gravity{Type: transform.GravitySmart}}, 150, 176, 100, 50, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			x, y, w, h, approx := InfoCrop(&tc.c, 400, 400)
			if x != tc.x || y != tc.y || w != tc.w || h != tc.h || approx != tc.approximated {
				t.Errorf("InfoCrop = %d,%d %dx%d %v, want %d,%d %dx%d %v", x, y, w, h, approx, tc.x, tc.y, tc.w, tc.h, tc.approximated)
			}
		})
	}
}
