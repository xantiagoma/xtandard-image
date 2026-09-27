//go:build cgo

package vips_test

import (
	"context"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/testutil/leakcheck"
	"github.com/xantiagoma/xtandard-image/internal/testutil/ssim"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// legacyFixtures are the WP-97 formats libvips loads through ImageMagick
// (magickload, restricted by the shim and the base image's policy.xml) and
// the ASCII and bitmap Netpbm variants ppmload reads.
var legacyFixtures = []struct {
	fixture   string
	loader    string
	format    vips.Format
	subformat string
	w, h      int
	bands     int
}{
	{"tga/alpha.tga", "magickload_buffer", vips.FormatMagick, "TGA", 64, 48, 4},
	{"tga/photo-rle.tga", "magickload_buffer", vips.FormatMagick, "TGA", 64, 48, 3},
	{"pcx/photo.pcx", "magickload_buffer", vips.FormatMagick, "PCX", 64, 48, 3},
	{"dds/photo-dxt1.dds", "magickload_buffer", vips.FormatMagick, "DDS", 64, 48, 3},
	{"dds/alpha-dxt5.dds", "magickload_buffer", vips.FormatMagick, "DDS", 64, 48, 4},
	{"qoi/alpha.qoi", "magickload_buffer", vips.FormatMagick, "QOI", 64, 48, 4},
	{"sgi/photo.sgi", "magickload_buffer", vips.FormatMagick, "SGI", 64, 48, 3},
	{"sgi/alpha.sgi", "magickload_buffer", vips.FormatMagick, "SGI", 64, 48, 4},
	{"sun/photo.ras", "magickload_buffer", vips.FormatMagick, "SUN", 64, 48, 3},
	{"xbm/graphic.xbm", "magickload_buffer", vips.FormatMagick, "XBM", 48, 36, 3},
	{"xpm/graphic.xpm", "magickload_buffer", vips.FormatMagick, "XPM", 48, 36, 3},
	{"pam/alpha.pam", "magickload_buffer", vips.FormatMagick, "PAM", 64, 48, 4},
	{"ppm/photo-ascii.ppm", "ppmload_buffer", vips.FormatPPM, "", 64, 48, 3},
	{"ppm/photo-ascii.pgm", "ppmload_buffer", vips.FormatPPM, "", 64, 48, 1},
	{"ppm/graphic.pbm", "ppmload_buffer", vips.FormatPPM, "", 48, 36, 1},
	{"ppm/graphic-ascii.pbm", "ppmload_buffer", vips.FormatPPM, "", 48, 36, 1},
}

// TestLoadLegacy probes and decodes every legacy fixture. The photo ones
// are compared with the lossless PCX of the same photo, or the TGA of the
// photo with alpha (DXT1 and DXT5 are lossy block compression, grey loses
// the colour), which catches swapped channels, flipped rows and wrong
// strides.
func TestLoadLegacy(t *testing.T) {
	t.Parallel()
	photo, alpha := decodeRGBA(t, "pcx/photo.pcx"), decodeRGBA(t, "tga/alpha.tga")
	for _, tc := range legacyFixtures {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			h := probeBytes(t, readFixture(t, tc.fixture))
			if h.Loader != tc.loader || h.Format != tc.format || h.Subformat != tc.subformat ||
				h.Width != tc.w || h.Height != tc.h || h.Bands != tc.bands {
				t.Errorf("probe = %s %v %q %dx%d×%d, want %s %v %q %dx%d×%d", h.Loader, h.Format, h.Subformat,
					h.Width, h.Height, h.Bands, tc.loader, tc.format, tc.subformat, tc.w, tc.h, tc.bands)
			}
			got := decodeRGBA(t, tc.fixture)
			if tc.w != 64 {
				return // the graphic, not the photo
			}
			ref := photo
			if tc.bands == 4 {
				ref = alpha
			}
			r, err := ssim.Compare(ref, got)
			if err != nil {
				t.Fatal(err)
			}
			limit := 0.002
			if tc.bands == 1 || tc.subformat == "DDS" {
				limit = 0.05
			}
			if r.DSSIM > limit {
				t.Errorf("DSSIM against the lossless photo = %.4f, want <= %.4f", r.DSSIM, limit)
			}
		})
	}
}

// TestNoLeakLegacy loads and encodes the ImageMagick and Netpbm legacy
// fixtures many times. Not parallel: the counters are process-wide.
func TestNoLeakLegacy(t *testing.T) {
	const cycles = 300
	vips.SetLeakCheck(true)
	defer vips.SetLeakCheck(false)

	inputs := make([][]byte, len(legacyFixtures))
	for i, f := range legacyFixtures {
		inputs[i] = readFixture(t, f.fixture)
	}
	so := vips.DefaultSaveOptions()
	cycle := func(i int) {
		buf := newBuffer(t, inputs[i%len(inputs)])
		defer buf.Release()
		job(t, func(ctx context.Context, a *vips.Arena) error {
			img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Access: vips.AccessSequential}))
			if err != nil {
				return err
			}
			if img, err = a.Add(img.Colourspace(ctx, vips.InterpretationSRGB)); err != nil {
				return err
			}
			blob, err := img.Save(ctx, vips.FormatJPEG, &so)
			if err != nil {
				return err
			}
			blob.Release()
			return nil
		})
	}
	for i := range inputs {
		cycle(i)
	}
	before := leakcheck.Take()
	for i := range cycles {
		cycle(i)
	}
	leakcheck.Check(t, before, leakcheck.Default, "cycles")
}
