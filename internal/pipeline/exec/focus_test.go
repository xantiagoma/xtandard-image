//go:build cgo

package exec_test

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// TestPixelFocus proves that the executor crops at a focus given in source
// pixels (thumbor focal, Cloudinary x_/y_): each case renders the pixel
// focus and the fractional focus at the same place, and both must match
// one golden. focus-point is the existing fp:0.8:0.3 golden; the others
// were recorded from the fractional request.
func TestPixelFocus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, fixture, options string
		// pixel returns the request with the pixel focus in place of the
		// fractional one.
		pixel func(p *transform.Pipeline)
		w, h  int
	}{
		{
			// 320x240: 0.8, 0.3 → 256, 72.
			name: "focus-point", fixture: jpegPhoto, options: "rs:fill:100:100/g:fp:0.8:0.3",
			pixel: func(p *transform.Pipeline) { p.Gravity = pixelFocus(256, 72, 256, 72) },
			w:     100, h: 100,
		},
		{
			// Stored 240x320 with orientation 6: the pixels are in the
			// displayed 320x240 image.
			name: "exif6-focus-point", fixture: exif6, options: "rs:fill:100:100/g:fp:0.8:0.3",
			pixel: func(p *transform.Pipeline) { p.Gravity = pixelFocus(256, 72, 256, 72) },
			w:     100, h: 100,
		},
		{
			// A thumbor-style rectangle: centre (60+100/2, 20+50/2) =
			// (110, 45) of 320x240.
			name: "focus-rect", fixture: jpegPhoto, options: "rs:fill:60:120/g:fp:0.34375:0.1875",
			pixel: func(p *transform.Pipeline) { p.Gravity = pixelFocus(60, 20, 160, 70) },
			w:     60, h: 120,
		},
		{
			// A manual crop of 200x150 at 40,60 (thumbor 40x60:240x210),
			// then a fill with the focus 168, 96 of the source =
			// 0.64, 0.24 of the crop.
			name: "pre-crop-focus-point", fixture: jpegPhoto, options: "c:200:150:nowe:40:60/rs:fill:80:80/g:fp:0.64:0.24",
			pixel: func(p *transform.Pipeline) { p.Gravity = pixelFocus(168, 96, 168, 96) },
			w:     80, h: 80,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data := readFixture(t, tc.fixture)
			e := newExecutor(t)
			pr := parse(t, tc.options)
			tc.pixel(&pr.Pipelines[0])
			if err := pr.Validate(); err != nil {
				t.Fatalf("validate pixel request: %v", err)
			}
			for _, r := range []*transform.Request{pr, parse(t, tc.options)} { // fractional last: -update records it
				o := processRequest(t, e, data, r, transform.FormatPNG)
				if o.res.Width != tc.w || o.res.Height != tc.h {
					t.Fatalf("size = %dx%d, want %dx%d", o.res.Width, o.res.Height, tc.w, tc.h)
				}
				golden.Compare(t, "exec/"+tc.name, o.img, golden.Options{})
			}
		})
	}
}

func pixelFocus(x1, y1, x2, y2 int) transform.Gravity {
	g, _ := transform.FocusFromPixels(x1, y1, x2, y2)
	return g
}
