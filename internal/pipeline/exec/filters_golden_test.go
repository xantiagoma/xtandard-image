//go:build cgo

package exec_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// filterCases are the blur, sharpen and pixelate golden cases on JPEG, PNG
// with alpha and animations (filtered per frame). TestAgainstImgproxy runs
// them too.
var filterCases = []goldenCase{
	{name: "blur-small", fixture: jpegPhoto, options: "rs:fit:160:160/bl:0.8", out: transform.FormatPNG, w: 160, h: 120},
	{name: "blur-large", fixture: jpegPhoto, options: "rs:fit:160:160/bl:12", out: transform.FormatPNG, w: 160, h: 120},
	{name: "sharpen", fixture: jpegPhoto, options: "rs:fit:160:160/sh:1.5", out: transform.FormatPNG, w: 160, h: 120},
	{name: "pixelate-4", fixture: jpegPhoto, options: "rs:fit:160:160/pix:4", out: transform.FormatPNG, w: 160, h: 120},
	{name: "pixelate-7", fixture: jpegPhoto, options: "rs:fit:160:160/pix:7", out: transform.FormatPNG, w: 160, h: 120},
	{name: "pixelate-clamped", fixture: jpegPhoto, options: "rs:fit:160:160/pix:500", out: transform.FormatPNG, w: 160, h: 120},
	{name: "filters-jpeg-out", fixture: jpegPhoto, options: "rs:fit:160:160/bl:2/sh:1/pix:5", out: transform.FormatJPEG, w: 160, h: 120},
	{name: "png-blur-small", fixture: pngAlpha, options: "bl:1", out: transform.FormatPNG, w: 128, h: 96},
	{name: "png-blur-large", fixture: pngAlpha, options: "bl:8", out: transform.FormatPNG, w: 128, h: 96},
	{name: "png-sharpen", fixture: pngAlpha, options: "sh:2", out: transform.FormatPNG, w: 128, h: 96},
	{name: "png-pixelate-8", fixture: pngAlpha, options: "pix:8", out: transform.FormatPNG, w: 128, h: 96},
	{name: "png-pixelate-10", fixture: pngAlpha, options: "pix:10", out: transform.FormatPNG, w: 128, h: 96},
	{name: "png-filters", fixture: pngAlpha, options: "bl:1.5/sh:0.7/pix:3", out: transform.FormatPNG, w: 128, h: 96},
	{name: "gif-blur", fixture: gifAnim, options: "rs:fit:48:48/bl:2", out: transform.FormatGIF, w: 48, h: 36, frames: 6},
	{name: "gif-pixelate", fixture: gifAnim, options: "pix:6", out: transform.FormatGIF, w: 96, h: 72, frames: 6},
	{name: "gif-pixelate-pad", fixture: gifAnim, options: "pix:10", out: transform.FormatGIF, w: 96, h: 72, frames: 6},
	{name: "webp-anim-sharpen", fixture: webpAnim, options: "rs:fill:48:32/sh:1", out: transform.FormatWebP, w: 48, h: 32, frames: 6},
}

func TestGoldenFilters(t *testing.T) {
	t.Parallel()
	for _, tc := range filterCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := process(t, tc.fixture, tc.options, tc.out)
			checkOutput(t, &tc, &o)
			if len(o.res.Skipped) != 0 {
				t.Errorf("Skipped = %q", o.res.Skipped)
			}
			golden.Compare(t, "exec/"+tc.name, o.img, golden.Options{Tolerance: tc.tolerance})
		})
	}
}

// TestFilterOptions checks that imgproxy URL values reach the plan
// unchanged: blur and sharpen are vips sigmas, pixelate a block size.
func TestFilterOptions(t *testing.T) {
	t.Parallel()
	type filters struct {
		Blur, Sharpen float64
		Pixelate      int
	}
	for _, tc := range []struct {
		options string
		want    filters
	}{
		{"bl:0.8", filters{Blur: 0.8}},
		{"blur:12", filters{Blur: 12}},
		{"sh:1.5", filters{Sharpen: 1.5}},
		{"sharpen:0.3", filters{Sharpen: 0.3}},
		{"pix:7", filters{Pixelate: 7}},
		{"bl:2/sh:1/pix:5", filters{Blur: 2, Sharpen: 1, Pixelate: 5}},
	} {
		t.Run(tc.options, func(t *testing.T) {
			t.Parallel()
			p := parse(t, tc.options).Pipelines[0]
			got := filters{Blur: p.Blur, Sharpen: p.Sharpen, Pixelate: p.Pixelate}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("filters (-want +got):\n%s", diff)
			}
		})
	}
}
