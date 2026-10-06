//go:build cgo

package vips_test

import (
	"context"
	"image"
	"math"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/testutil/ssim"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// shadowError compares got with ref (same size, opaque): the mean absolute
// R, G, B error over every pixel, the same over the shadows (pixels whose
// reference Rec. 601 luma is below 64), and the darkest luma of got.
func shadowError(ref, got *image.NRGBA) (all, shadows, darkest float64) {
	var sum, sumShadow float64
	var n, nShadow int
	darkest = 255
	for i := 0; i+3 < len(ref.Pix); i += 4 {
		var d float64
		for c := range 3 {
			d += math.Abs(float64(ref.Pix[i+c]) - float64(got.Pix[i+c]))
		}
		sum += d
		n += 3
		luma := func(p []byte) float64 { return 0.299*float64(p[0]) + 0.587*float64(p[1]) + 0.114*float64(p[2]) }
		if luma(ref.Pix[i:]) < 64 {
			sumShadow += d
			nShadow += 3
		}
		darkest = min(darkest, luma(got.Pix[i:]))
	}
	return sum / float64(n), sumShadow / float64(max(nShadow, 1)), darkest
}

// TestCMYKBlackPointCompensation converts a press CMYK TIFF without an
// embedded profile (the photo through libvips' built-in CMYK profile,
// relative colorimetric with BPC, as Photoshop converts by default) back to
// sRGB, through Colourspace (libvips' CMYK2XYZ route) and through
// ICCImport with the same built-in profile, and compares it with the
// lossless photo. The profile's black is a dark grey: without black point
// compensation it stays that grey and every shadow is lifted and flattened
// (measured: shadow error 19, mean error 10–11, darkest luma 45 against the
// photo's 11; with it 3.5–3.9, 4.4–5.6 and 23).
func TestCMYKBlackPointCompensation(t *testing.T) {
	t.Parallel()
	ref := decodeRGBA(t, "pcx/photo.pcx")
	for _, tc := range []struct {
		name  string
		toPCS func(ctx context.Context, a *vips.Arena, img *vips.Image) (*vips.Image, error)
	}{
		{"colourspace", func(_ context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) { return img, nil }},
		{"icc_import", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.ICCImport(ctx, "cmyk", vips.IntentRelative, true)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			buf := newBuffer(t, readFixture(t, "tiff/cmyk-press.tif"))
			defer buf.Release()
			job(t, func(ctx context.Context, a *vips.Arena) error {
				img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Access: vips.AccessRandom}))
				if err != nil {
					return err
				}
				if img.Interpretation() != vips.InterpretationCMYK {
					t.Fatalf("interpretation %v, want CMYK", img.Interpretation())
				}
				if img, err = a.Add(tc.toPCS(ctx, a, img)); err != nil {
					return err
				}
				got, err := rgbaOf(ctx, a, img)
				if err != nil {
					return err
				}
				all, shadows, darkest := shadowError(ref, got)
				t.Logf("mean error %.2f, shadows %.2f, darkest luma %.0f", all, shadows, darkest)
				if all > 6 || shadows > 8 || darkest > 30 {
					t.Errorf("mean error %.2f (want <= 6), shadows %.2f (want <= 8), darkest luma %.0f (want <= 30): shadows not compensated",
						all, shadows, darkest)
				}
				r, err := ssim.Compare(ref, got)
				if err != nil {
					return err
				}
				if r.DSSIM > 0.01 {
					t.Errorf("DSSIM against the photo = %.4f, want <= 0.01", r.DSSIM)
				}
				return nil
			})
		})
	}
}
