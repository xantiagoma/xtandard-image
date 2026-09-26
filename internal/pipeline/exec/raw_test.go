//go:build cgo

package exec_test

import (
	"strings"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/codec/encode"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/exec"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/testutil/rawfixtures"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

const (
	dngLandscape = "dng/landscape.dng"              // 192×128, 96×64 preview
	dngPortrait  = "dng/portrait-orientation-6.dng" // stored 192×128, shown 128×192, 48×32 preview
)

// runRaw runs options on RAW bytes with the given limits and decodes the
// result.
func runRaw(tb testing.TB, data []byte, options string, lim plan.Limits) (output, error) {
	tb.Helper()
	j, buf := job(tb, data, parse(tb, options), transform.FormatPNG)
	defer buf.Release()
	ec := encode.DefaultConfig()
	enc, err := encode.New(&ec)
	if err != nil {
		tb.Fatal(err)
	}
	e, err := exec.New(&exec.Config{Limits: lim}, enc)
	if err != nil {
		tb.Fatal(err)
	}
	res, err := e.Run(tb.Context(), j)
	if err != nil {
		return output{}, err
	}
	defer res.Body.Release()
	enc2 := append([]byte(nil), res.Body.Bytes()...)
	img, frames := decode(tb, enc2)
	return output{res: res, img: img, frames: frames, bytes: enc2}, nil
}

// TestRawGolden renders the synthetic DNGs through every decode path: the
// embedded preview (with and without JPEG shrink-on-load), the half-size
// and the full demosaic, in both orientations. The portrait fixture stores
// its pixels rotated (Orientation 6): the goldens show it upright.
func TestRawGolden(t *testing.T) {
	t.Parallel()
	noPreview := testLimits
	noPreview.RawPreviewMinRatio = 1000
	for _, tc := range []struct {
		name, fixture, options string
		lim                    plan.Limits
		path                   string
		w, h                   int
	}{
		{"raw-dng-preview", dngLandscape, "rs:fit:96:96", testLimits, exec.RawPathPreview, 96, 64},
		{"raw-dng-preview-shrink", dngLandscape, "rs:fit:40:40", testLimits, exec.RawPathPreview, 40, 27},
		{"raw-dng-demosaic", dngLandscape, "rs:fit:160:160", testLimits, exec.RawPathDemosaic, 160, 107},
		{"raw-dng-demosaic-half", dngLandscape, "rs:fit:96:96", noPreview, exec.RawPathDemosaicHalf, 96, 64},
		{"raw-dng-portrait-preview", dngPortrait, "rs:fit:48:48", testLimits, exec.RawPathPreview, 32, 48},
		{"raw-dng-portrait-demosaic-half", dngPortrait, "rs:fit:64:64", testLimits, exec.RawPathDemosaicHalf, 43, 64},
		{"raw-dng-portrait-demosaic", dngPortrait, "", testLimits, exec.RawPathDemosaic, 128, 192},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o, err := runRaw(t, readFixture(t, tc.fixture), tc.options, tc.lim)
			if err != nil {
				t.Fatal(err)
			}
			if o.res.RawPath != tc.path || o.res.Width != tc.w || o.res.Height != tc.h {
				t.Errorf("path %q, %dx%d; want %q, %dx%d", o.res.RawPath, o.res.Width, o.res.Height, tc.path, tc.w, tc.h)
			}
			golden.Compare(t, "exec/"+tc.name, o.img, golden.Options{})
		})
	}
}

// TestRawPreviewMatchesDemosaic: both paths render the same scene (the
// preview is a JPEG of what the mosaic encodes), so the decode path is an
// optimisation, not a different picture.
func TestRawPreviewMatchesDemosaic(t *testing.T) {
	t.Parallel()
	data := readFixture(t, dngLandscape)
	pv, err := runRaw(t, data, "rs:fit:64:64", testLimits)
	if err != nil {
		t.Fatal(err)
	}
	full := testLimits
	full.RawPreviewMinRatio, full.DisableShrinkOnLoad = 1000, true
	dm, err := runRaw(t, data, "rs:fit:64:64", full)
	if err != nil {
		t.Fatal(err)
	}
	if pv.res.RawPath != exec.RawPathPreview || dm.res.RawPath != exec.RawPathDemosaic {
		t.Fatalf("paths %q and %q", pv.res.RawPath, dm.res.RawPath)
	}
	// LibRaw's auto-brightness lifts the demosaic a little.
	if d := meanAbsDiff(pv.img, dm.img); d > 20 {
		t.Errorf("preview and demosaic differ by %.1f per channel", d)
	}
}

// TestRawMetadata: with metadata kept, the output carries the RAW's camera
// EXIF (not the preview's) and no rotation.
func TestRawMetadata(t *testing.T) {
	t.Parallel()
	for _, options := range []string{"rs:fit:48:48/sm:0", "rs:fit:100:100/sm:0"} {
		t.Run(options, func(t *testing.T) {
			t.Parallel()
			o, err := runRaw(t, readFixture(t, dngPortrait), options, testLimits)
			if err != nil {
				t.Fatal(err)
			}
			h := readHeader(t, o.bytes)
			for field, want := range map[string]string{
				"exif-ifd0-Make":             "xtandard",
				"exif-ifd0-Model":            "Synthetic DNG",
				"exif-ifd2-ExposureTime":     "1/250",
				"exif-ifd2-FNumber":          "56/10",
				"exif-ifd2-ISOSpeedRatings":  "200",
				"exif-ifd2-FocalLength":      "350/10",
				"exif-ifd2-DateTimeOriginal": "2024:05:06 07:08:09",
				"exif-ifd2-LensModel":        "Synthetic 35mm F5.6",
			} {
				if got := h.text[vips.MetaField(field)]; !strings.HasPrefix(got, want+" ") {
					t.Errorf("%s = %q, want %q", field, got, want)
				}
			}
			if h.orientation != 1 || h.height <= h.width {
				t.Errorf("%dx%d orientation %d, want upright portrait, orientation 1", h.width, h.height, h.orientation)
			}
		})
	}
	stripped, err := runRaw(t, readFixture(t, dngPortrait), "rs:fit:48:48", testLimits)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := readHeader(t, stripped.bytes).text[vips.MetaField("exif-ifd0-Make")]; ok {
		t.Error("EXIF kept although metadata is stripped by default")
	}
}

func TestRawErrors(t *testing.T) {
	t.Parallel()
	dng := readFixture(t, dngLandscape)
	for _, tc := range []struct {
		name    string
		data    []byte
		options string
		lim     plan.Limits
		kind    errs.Kind
	}{
		{"demosaic over the limit", dng, "rs:fit:160:160", plan.Limits{MaxRawDemosaicPixels: 1000}, errs.KindLimitExceeded},
		{"truncated mosaic", dng[:len(dng)-30_000], "rs:fit:160:160", testLimits, errs.KindCorrupt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := runRaw(t, tc.data, tc.options, tc.lim)
			if k := errs.KindOf(err); k != tc.kind {
				t.Errorf("kind = %v (%v), want %v", k, err, tc.kind)
			}
		})
	}
}

// TestRawCameraGolden renders every pinned camera sample (downloaded on
// first use, see rawfixtures) at 160 px through the preview path, and one
// through the half-size demosaic. The Canon 350D sample is a portrait shot
// (orientation 8): its golden is upright.
func TestRawCameraGolden(t *testing.T) {
	t.Parallel()
	for _, f := range rawfixtures.Files() {
		t.Run(f.Kind, func(t *testing.T) {
			t.Parallel()
			o, err := runRaw(t, rawfixtures.Read(t, &f), "rs:fit:160:160", testLimits)
			if err != nil {
				t.Fatal(err)
			}
			if o.res.RawPath != exec.RawPathPreview {
				t.Errorf("path %q, want preview", o.res.RawPath)
			}
			if portrait := o.res.Height > o.res.Width; portrait != (f.Orientation >= 5) {
				t.Errorf("%dx%d for orientation %d", o.res.Width, o.res.Height, f.Orientation)
			}
			golden.Compare(t, "exec/raw-"+f.Kind, o.img, golden.Options{})
		})
	}
	t.Run("nef-demosaic-half", func(t *testing.T) {
		t.Parallel()
		f := rawfixtures.Files()[2]
		lim := testLimits
		lim.RawPreviewMinRatio = 1000
		o, err := runRaw(t, rawfixtures.Read(t, &f), "rs:fit:160:160", lim)
		if err != nil {
			t.Fatal(err)
		}
		if o.res.RawPath != exec.RawPathDemosaicHalf {
			t.Errorf("path %q, want demosaic-half", o.res.RawPath)
		}
		golden.Compare(t, "exec/raw-nef-demosaic-half", o.img, golden.Options{})
	})
}

// TestRawPreviewFaster proves the point of the preview path on a real
// 16 MP DNG: for the same 320 px output, decoding the embedded JPEG is far
// cheaper than the half-size demosaic the planner would pick without it.
// Only Executor.Run is timed (best of three); BenchmarkRaw has the numbers.
func TestRawPreviewFaster(t *testing.T) {
	t.Parallel()
	f := rawfixtures.Files()[4] // pentax-k-50.dng
	data := rawfixtures.Read(t, &f)
	best := func(ratio float64, path string) time.Duration {
		lim := testLimits
		lim.RawPreviewMinRatio = ratio
		ec := encode.DefaultConfig()
		enc, err := encode.New(&ec)
		if err != nil {
			t.Fatal(err)
		}
		e, err := exec.New(&exec.Config{Limits: lim}, enc)
		if err != nil {
			t.Fatal(err)
		}
		j, buf := job(t, data, parse(t, "rs:fit:320:320"), transform.FormatJPEG)
		defer buf.Release()
		d := time.Duration(1 << 62)
		for range 3 {
			start := time.Now()
			res, err := e.Run(t.Context(), j)
			if err != nil {
				t.Fatal(err)
			}
			d = min(d, time.Since(start))
			res.Body.Release()
			if res.RawPath != path {
				t.Fatalf("path %q, want %q", res.RawPath, path)
			}
		}
		return d
	}
	preview, demosaic := best(0, exec.RawPathPreview), best(1000, exec.RawPathDemosaicHalf)
	t.Logf("320 px from %s: preview %v, half-size demosaic %v (%.1f×)", f.Name, preview, demosaic, float64(demosaic)/float64(preview))
	if preview*3 > demosaic {
		t.Errorf("preview %v is not much faster than the half-size demosaic %v", preview, demosaic)
	}
}
