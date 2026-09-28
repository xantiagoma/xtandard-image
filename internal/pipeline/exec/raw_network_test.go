//go:build cgo && network

package exec_test

import (
	"errors"
	"math"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec/encode"
	"github.com/xantiagoma/xtandard-image/internal/codec/raw"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/exec"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/testutil/rawfixtures"
	"github.com/xantiagoma/xtandard-image/internal/testutil/ssim"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// defaultLimits are the configured defaults the WP-101/102 samples must
// render under (config.Default: 50 MP source, 120 MP demosaic).
var defaultLimits = plan.Limits{
	MaxSrcPixels:         50_000_000,
	MaxResultDimension:   8192,
	MaxAnimationFrames:   256,
	MaxRawDemosaicPixels: 120_000_000,
}

// runRawResult runs options on data as JPEG with the default limits and
// returns the result without decoding it.
func runRawResult(t *testing.T, data []byte, options string) *exec.Result {
	t.Helper()
	j, buf := job(t, data, parse(t, options), transform.FormatJPEG)
	defer buf.Release()
	ec := encode.DefaultConfig()
	enc, err := encode.New(&ec)
	if err != nil {
		t.Fatal(err)
	}
	e, err := exec.New(&exec.Config{Limits: defaultLimits}, enc)
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.Run(t.Context(), j)
	if err != nil {
		t.Fatalf("%q: %v", options, err)
	}
	res.Body.Release()
	return res
}

// previewOf returns the embedded preview LibRaw lists for data, as a JPEG.
func previewOf(t *testing.T, data []byte) []byte {
	t.Helper()
	buf := newBuffer(t, data)
	defer buf.Release()
	var p raw.Preview
	err := vips.Run(t.Context(), func() error {
		h, err := raw.Probe(t.Context(), buf)
		p = h.Preview
		if err == nil && !h.HasPreview {
			err = errors.New("no preview")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, p.Length)
	if err := raw.PreviewBytes(out, data, p); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestRawNetworkSamples renders the WP-100 gallery samples WP-101, WP-102
// and WP-103 fixed, at 160 and 2000 px with the default limits. The Nikon
// High Efficiency files come from their embedded previews: their pixels
// must match the camera's own JPEG (the Z50II decoded into garbage
// before), whatever size is asked. The 50–102 MP sensors render through the
// preview or the half-size demosaic, and a full-size demosaic of them stays
// over XI_LIMITS_MAX_SRC_PIXELS. The WP-103 samples (Sony A7 V, Sigma X3F,
// and GoPro GPR in the gpl image) demosaic: a full-size request renders the
// sensor's size.
func TestRawNetworkSamples(t *testing.T) {
	t.Parallel()
	for _, f := range rawfixtures.NetworkFiles() {
		t.Run(f.Name, func(t *testing.T) {
			t.Parallel()
			data := rawfixtures.Read(t, &f.File)
			if f.Kind == "dng" && f.Camera == "GoPro HERO11 Black" && !vips.RawLibraryInfo().GPRSDK {
				// slim and full have no GPR SDK (gpl only) and the file no
				// usable preview: 422, never a demosaic of the placeholder.
				_, err := runRaw(t, data, "rs:fit:160:160", defaultLimits)
				if errs.KindOf(err) != errs.KindUnsupported {
					t.Errorf("without the GPR SDK: %v (kind %v), want unsupported", err, errs.KindOf(err))
				}
				return
			}
			sizes := []string{"rs:fit:160:160", "rs:fit:2000:2000"}
			if f.Undecodable != "" {
				sizes = append(sizes, "") // full size: the preview's
			}
			for _, opts := range sizes {
				if opts == "" {
					// Up to 45 MP: checked without decoding the output.
					// The preview's size, within XI_LIMITS_MAX_RESULT_DIMENSION.
					w, h := f.Width, f.Height
					if w > defaultLimits.MaxResultDimension {
						w, h = defaultLimits.MaxResultDimension, int(math.Round(float64(h)*float64(defaultLimits.MaxResultDimension)/float64(w)))
					}
					res := runRawResult(t, data, opts)
					if res.RawPath != exec.RawPathPreview || res.Width != w || res.Height != h {
						t.Errorf("full size: %s %dx%d, want the preview's %dx%d", res.RawPath, res.Width, res.Height, w, h)
					}
					continue
				}
				o, err := runRaw(t, data, opts, defaultLimits)
				if err != nil {
					t.Fatalf("%q: %v", opts, err)
				}
				t.Logf("%q: %s %dx%d", opts, o.res.RawPath, o.res.Width, o.res.Height)
				if f.Undecodable == "" {
					continue
				}
				if o.res.RawPath != exec.RawPathPreview {
					t.Errorf("%q: path %q, want preview", opts, o.res.RawPath)
				}
				// The same request on the camera's JPEG preview.
				ref, err := runRaw(t, previewOf(t, data), opts, defaultLimits)
				if err != nil {
					t.Fatal(err)
				}
				res, err := ssim.Compare(ref.img, o.img)
				if err != nil {
					t.Fatal(err)
				}
				if res.DSSIM > 0.005 {
					t.Errorf("%q: DSSIM %.5f against the embedded preview", opts, res.DSSIM)
				}
			}
			if f.Undecodable != "" {
				return
			}
			if f.Name == "fujifilm-gfx100rf.raf" {
				// Larger than its 4000×3000 preview: the half-size
				// demosaic (5831×4373, 25.5 MP) of the 102 MP sensor.
				if res := runRawResult(t, data, "rs:fit:5000:5000"); res.RawPath != exec.RawPathDemosaicHalf || res.Width != 5000 {
					t.Errorf("5000 px: %s %dx%d, want demosaic-half 5000 wide", res.RawPath, res.Width, res.Height)
				}
			}
			if int64(f.Width)*int64(f.Height) <= defaultLimits.MaxSrcPixels {
				// Under the limit: the full-size demosaic renders.
				res := runRawResult(t, data, "")
				if res.RawPath != exec.RawPathDemosaic || res.Width != f.Width || res.Height != f.Height {
					t.Errorf("full size: %s %dx%d, want demosaic %dx%d", res.RawPath, res.Width, res.Height, f.Width, f.Height)
				}
				return
			}
			// A full-size demosaic of a > 50 MP sensor is over the limit.
			_, err := runRaw(t, data, "", defaultLimits)
			if errs.KindOf(err) != errs.KindLimitExceeded {
				t.Errorf("full size: %v, want limit exceeded", err)
			}
		})
	}
}
