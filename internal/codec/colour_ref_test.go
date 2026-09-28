//go:build cgo

package codec_test

import (
	"image"
	"image/color"
	"math"
	"path/filepath"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/testutil/ssim"
)

// rawImage wraps an RGBA codec.Raw as an image.Image.
func rawImage(r *codec.Raw) *image.NRGBA {
	return &image.NRGBA{Pix: r.Data, Stride: 4 * r.Width, Rect: image.Rect(0, 0, r.Width, r.Height)}
}

// colourDiff returns the mean and maximum absolute difference of the R, G
// and B channels of a and b (same size), and the same for alpha.
func colourDiff(a, b image.Image) (mean, maxDiff, alphaMax float64) {
	r := a.Bounds()
	var sum float64
	for y := range r.Dy() {
		for x := range r.Dx() {
			ca, _ := color.NRGBAModel.Convert(a.At(r.Min.X+x, r.Min.Y+y)).(color.NRGBA)
			cb, _ := color.NRGBAModel.Convert(b.At(b.Bounds().Min.X+x, b.Bounds().Min.Y+y)).(color.NRGBA)
			for _, d := range []float64{
				math.Abs(float64(ca.R) - float64(cb.R)),
				math.Abs(float64(ca.G) - float64(cb.G)),
				math.Abs(float64(ca.B) - float64(cb.B)),
			} {
				sum += d
				maxDiff = max(maxDiff, d)
			}
			alphaMax = max(alphaMax, math.Abs(float64(ca.A)-float64(cb.A)))
		}
	}
	return sum / float64(3*r.Dx()*r.Dy()), maxDiff, alphaMax
}

// opaqueImage reports whether every pixel of img is opaque.
func opaqueImage(img image.Image) bool {
	r := img.Bounds()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a != 0xFFFF {
				return false
			}
		}
	}
	return true
}

// TestColourMatchesReference decodes the WP-105 fixtures (codec.Sample:
// the pixels the pipeline and /info see) and compares them with reference
// decodes in testdata/golden/codec (made by reference.py there): the CIE
// colorimetric conversion of the L*a*b* TIFFs' samples as D50 values
// (Bradford to D65, IEC 61966-2-1 sRGB) and Pillow's DDS decoder for the
// BCn textures. The
// comparison is perceptual (luma DSSIM) and, because DSSIM ignores
// chroma, a bound on the mean and maximum channel error.
func TestColourMatchesReference(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fixture, ref string
		mean, max    float64
		alpha        bool // the fixture has meaningful alpha (not compared)
	}{
		// The ICC variant embeds an identity D50 L*a*b* profile: the same
		// colours. lcms's own 8-bit L*a*b* path (Pillow ImageCms) agrees
		// with the reference within 9 levels (mean 0.27): its 3D LUT is
		// coarser in saturated blues.
		{"tiff/lab8.tif", "lab8-ref.png", 0.05, 1, false},
		{"tiff/lab8-icc.tif", "lab8-ref.png", 0.05, 1, false},
		{"tiff/lab8-alpha.tif", "lab8-ref.png", 0.05, 1, true},
		{"tiff/lab16.tif", "lab16-ref.png", 0.05, 1, false},
		// Lossless CMYK (K as a black extra channel) through its own
		// profile; libvips' lcms transform agrees with Pillow's within
		// rounding.
		{"jxl/cmyk.jxl", "jxl-cmyk-lcms.png", 0.5, 2, false},
		{"jxl/cmyk-alpha.jxl", "jxl-cmyk-lcms.png", 0.5, 2, true},
		{"dds/bc1-dx10.dds", "dds-bc1-dx10-pillow.png", 0.5, 1, false},
		{"dds/bc4.dds", "dds-bc4-pillow.png", 0.01, 1, false},
		{"dds/bc7-alpha.dds", "dds-bc7-alpha-pillow.png", 0.01, 1, true},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			raw := sample(t, tc.fixture, 512)
			got := rawImage(&raw)
			want := golden.Decode(t, filepath.Join(golden.ModuleRoot(t), "testdata", "golden", "codec", tc.ref))
			refAlpha := !opaqueImage(want)
			if tc.alpha && !refAlpha {
				// The reference has no alpha (lcms): check that the fade is
				// there, then compare the colour alone.
				if opaqueImage(got) {
					t.Error("alpha lost")
				}
				opaque := *got
				opaque.Pix = append([]byte(nil), got.Pix...)
				for i := 3; i < len(opaque.Pix); i += 4 {
					opaque.Pix[i] = 255
				}
				got = &opaque
			}
			res, err := ssim.Compare(got, want)
			if err != nil {
				t.Fatal(err)
			}
			if res.DSSIM > 0.01 {
				t.Errorf("DSSIM = %.4f, want <= 0.01", res.DSSIM)
			}
			mean, maxDiff, alphaMax := colourDiff(got, want)
			if mean > tc.mean || maxDiff > tc.max {
				t.Errorf("channel error mean %.3f max %.0f, want <= %.2f and <= %.0f", mean, maxDiff, tc.mean, tc.max)
			}
			if alphaMax > 1 {
				t.Errorf("alpha differs by up to %.0f", alphaMax)
			}
		})
	}
}
