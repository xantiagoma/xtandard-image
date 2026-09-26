//go:build cgo

package encode_test

import (
	"context"
	"image"
	"strconv"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec/encode"
	"github.com/xantiagoma/xtandard-image/internal/testutil/ssim"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// corpusImage is one auto-quality corpus input: a fixture rendered at a
// given width.
type corpusImage struct {
	name  string
	file  string
	width int
	// density renders a vector at this DPI instead of resizing.
	density float64
}

// aqCorpus is the fixture corpus of the auto-quality done criteria: a photo,
// a graphic and an image with graded alpha, each at a size below the
// comparison cap (compared at full size) and one above it (compared on the
// 512 px copy).
func aqCorpus() []corpusImage {
	out := make([]corpusImage, 0, 6)
	for _, w := range []int{256, 1024} {
		out = append(out,
			corpusImage{name: "photo", file: "jpeg/baseline.jpg", width: w},
			corpusImage{name: "graphic", file: "svg/logo.svg", width: w, density: 72 * float64(w) / 96},
			corpusImage{name: "alpha", file: "png/rgba8.png", width: w},
		)
	}
	return out
}

var aqFormats = []transform.Format{transform.FormatJPEG, transform.FormatWebP, transform.FormatAVIF}

// aqOutput is an auto-quality encode with the pixels it started from.
type aqOutput struct {
	data       []byte
	res        encode.Result
	ref        []byte // pre-encode pixels, 8-bit sRGB
	refBands   int
	refW, refH int
}

// encodeCorpus renders in and encodes it with t.
func encodeCorpus(tb testing.TB, e *encode.Encoder, in corpusImage, t *encode.Target) aqOutput {
	tb.Helper()
	buf := newBuffer(tb, readFixture(tb, in.file))
	var out aqOutput
	ctx := tb.Context()
	err := vips.Run(ctx, func() error {
		var a vips.Arena
		defer a.Close()
		img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Density: in.density}))
		if err != nil {
			return err
		}
		if in.density == 0 {
			if img, err = a.Add(img.Resize(ctx, float64(in.width)/float64(img.Width()), 0, vips.KernelLanczos3)); err != nil {
				return err
			}
		}
		if img, err = a.Add(img.CopyMemory(ctx)); err != nil {
			return err
		}
		if out.ref, out.refBands, err = srgbPixels(ctx, &a, img); err != nil {
			return err
		}
		out.refW, out.refH = img.Width(), img.Height()
		res, err := e.Encode(ctx, img, t)
		if err != nil {
			return err
		}
		defer res.Blob.Release()
		out.data = append([]byte(nil), res.Blob.Bytes()...)
		out.res = *res
		out.res.Blob = nil
		return nil
	})
	if err != nil {
		tb.Fatalf("encode %s: %v", in.name, err)
	}
	return out
}

// rgbImage wraps 3-band pixels.
func rgbImage(p []byte, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range w * h {
		copy(img.Pix[4*i:4*i+3], p[3*i:3*i+3])
		img.Pix[4*i+3] = 0xff
	}
	return img
}

// fullDSSIM is the DSSIM between the pre-encode pixels and the decoded
// output at full size, both composited onto white.
func fullDSSIM(tb testing.TB, out *aqOutput) float64 {
	tb.Helper()
	got := decode(tb, out.data)
	if got.header.Width != out.refW || got.header.Height != out.refH {
		tb.Fatalf("decoded %dx%d, want %dx%d", got.header.Width, got.header.Height, out.refW, out.refH)
	}
	r, err := ssim.Compare(
		rgbImage(onWhite(out.ref, out.refBands), out.refW, out.refH),
		rgbImage(onWhite(got.pix, got.bands), out.refW, out.refH))
	if err != nil {
		tb.Fatal(err)
	}
	return r.DSSIM
}

var aqMeta = encode.Metadata{Strip: true, KeepCopyright: true, StripColorProfile: true}

// TestAutoQualityDSSIMCorpus is the WP-56 done criterion: for every corpus
// image and format the chosen quality meets the DSSIM target within the
// allowed error (or is the top of the range when nothing does), in at most
// six saves. The reported metric is re-checked at full size: equal below
// the comparison cap, and within the documented gap above it.
func TestAutoQualityDSSIMCorpus(t *testing.T) {
	t.Parallel()
	e := newEncoder(t, nil)
	// The default target, and a strict one that lands inside the ranges.
	for _, target := range []float64{encode.DefaultAutoQuality().Target, 0.006} {
		for _, in := range aqCorpus() {
			for _, f := range aqFormats {
				name := f.String() + "/" + in.name + "/" + strconv.Itoa(in.width) + "/" + strconv.FormatFloat(target, 'g', -1, 64)
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					checkDSSIMCorpus(t, e, in, f, target)
				})
			}
		}
	}
}

func checkDSSIMCorpus(t *testing.T, e *encode.Encoder, in corpusImage, f transform.Format, target float64) {
	t.Helper()
	aq := encode.DefaultAutoQuality()
	aq.Target = target
	out := encodeCorpus(t, e, in, &encode.Target{
		Format: f, Metadata: aqMeta,
		AutoQuality: &transform.AutoQuality{Method: transform.AutoQualityDSSIM, Target: target},
	})
	r := out.res
	lo, hi := aq.FormatMin[f], aq.FormatMax[f]
	if r.AutoQuality.Method != transform.AutoQualityDSSIM || r.AutoQuality.Target != aq.Target {
		t.Fatalf("auto-quality = %+v", r.AutoQuality)
	}
	if r.Attempts < 1 || r.Attempts > encode.MaxSaves {
		t.Errorf("attempts = %d, want 1..%d", r.Attempts, encode.MaxSaves)
	}
	if r.Quality < lo || r.Quality > hi {
		t.Errorf("quality %d outside %d..%d", r.Quality, lo, hi)
	}
	limit := aq.Target + aq.AllowedError
	switch {
	case r.AutoQuality.Met && r.AutoQuality.Metric > limit:
		t.Errorf("met with DSSIM %.5f > %.5f", r.AutoQuality.Metric, limit)
	case !r.AutoQuality.Met && r.Quality != hi:
		t.Errorf("unmet at quality %d, want the top %d", r.Quality, hi)
	}
	full := fullDSSIM(t, &out)
	t.Logf("q%d in %d saves, %d bytes: DSSIM %.5f (compared), %.5f (full size), met %v",
		r.Quality, r.Attempts, len(out.data), r.AutoQuality.Metric, full, r.AutoQuality.Met)
	if in.width <= encode.CompareMaxDimension {
		// Compared at full size: the metrics agree up to the alpha
		// handling (white only here, white and black in the encoder)
		// and the rounding of this test's own flatten.
		if full > r.AutoQuality.Metric*1.05+1e-5 {
			t.Errorf("full-size DSSIM %.5f > reported %.5f", full, r.AutoQuality.Metric)
		}
	} else if r.AutoQuality.Met && full > 3*limit {
		t.Errorf("full-size DSSIM %.5f is over 3× the target", full)
	}
}

// TestAutoQualitySizeCorpus: the size method returns a quality in range
// whose output fits the byte target (the largest among the attempts), or
// the smallest attempt flagged over budget, in at most six saves.
func TestAutoQualitySizeCorpus(t *testing.T) {
	t.Parallel()
	e := newEncoder(t, nil)
	aq := encode.DefaultAutoQuality()
	for _, in := range aqCorpus() {
		for _, f := range aqFormats {
			t.Run(f.String()+"/"+in.name+"/"+strconv.Itoa(in.width), func(t *testing.T) {
				t.Parallel()
				plain := encodeCorpus(t, e, in, &encode.Target{Format: f, Metadata: aqMeta})
				lo, hi := aq.FormatMin[f], aq.FormatMax[f]
				for _, budget := range []int{len(plain.data) * 2 / 3, 64} {
					out := encodeCorpus(t, e, in, &encode.Target{
						Format: f, Metadata: aqMeta,
						AutoQuality: &transform.AutoQuality{Method: transform.AutoQualitySize, Target: float64(budget)},
					})
					r := out.res
					if r.Attempts < 1 || r.Attempts > encode.MaxSaves {
						t.Errorf("budget %d: attempts = %d", budget, r.Attempts)
					}
					if r.Quality < lo || r.Quality > hi {
						t.Errorf("budget %d: quality %d outside %d..%d", budget, r.Quality, lo, hi)
					}
					fits := len(out.data) <= budget
					if fits == r.OverBudget || fits != r.AutoQuality.Met || r.AutoQuality.Metric != float64(len(out.data)) {
						t.Errorf("budget %d: %d bytes, result %+v", budget, len(out.data), r)
					}
					// Over budget: the smallest attempt, which is at or near
					// the minimum quality (sizes are not strictly monotone).
					if !fits && r.Quality > lo+5 {
						t.Errorf("budget %d: over budget at quality %d, want near the minimum %d", budget, r.Quality, lo)
					}
					t.Logf("budget %d: q%d, %d bytes in %d saves", budget, r.Quality, len(out.data), r.Attempts)
				}
			})
		}
	}
}

// TestAutoQualityAnimation searches on the first frame and saves every
// frame once more at the chosen quality.
func TestAutoQualityAnimation(t *testing.T) {
	t.Parallel()
	e := newEncoder(t, nil)
	src := readFixture(t, "webp/animated.webp")
	out := mustEncode(t, e, src, -1, &encode.Target{
		Format: transform.FormatWebP, Animated: true, Metadata: aqMeta,
		AutoQuality: &transform.AutoQuality{Method: transform.AutoQualityDSSIM},
	}, nil)
	r := out.res
	if r.Attempts < 2 || r.Attempts > encode.MaxSaves+1 || r.AutoQuality.Method != transform.AutoQualityDSSIM {
		t.Errorf("result = %+v", r)
	}
	if got := decode(t, out.data).header.Pages; got != 6 {
		t.Errorf("pages = %d, want 6", got)
	}
}

// TestAutoQualityWithMaxBytes: max_bytes still bounds a dssim result.
func TestAutoQualityWithMaxBytes(t *testing.T) {
	t.Parallel()
	e := newEncoder(t, nil)
	in := corpusImage{name: "photo", file: "jpeg/baseline.jpg", width: 320}
	aqt := &transform.AutoQuality{Method: transform.AutoQualityDSSIM}
	free := encodeCorpus(t, e, in, &encode.Target{Format: transform.FormatJPEG, Metadata: aqMeta, AutoQuality: aqt})
	budget := len(free.data) / 2
	out := encodeCorpus(t, e, in, &encode.Target{
		Format: transform.FormatJPEG, Metadata: aqMeta, AutoQuality: aqt, MaxBytes: budget,
	})
	r := out.res
	if len(out.data) > budget && !r.OverBudget {
		t.Errorf("%d bytes > %d without OverBudget", len(out.data), budget)
	}
	if r.Quality >= free.res.Quality || r.AutoQuality.Met || r.Attempts <= free.res.Attempts {
		t.Errorf("with max_bytes: %+v; without: %+v", r, free.res)
	}
}

// TestAutoQualityIgnored: ml runs as no auto-quality and says so; config
// dssim applies without a request option.
func TestAutoQualityIgnored(t *testing.T) {
	t.Parallel()
	in := corpusImage{name: "photo", file: "jpeg/baseline.jpg", width: 320}
	t.Run("ml", func(t *testing.T) {
		t.Parallel()
		out := encodeCorpus(t, newEncoder(t, nil), in, &encode.Target{
			Format: transform.FormatJPEG, Metadata: aqMeta,
			AutoQuality: &transform.AutoQuality{Method: transform.AutoQualityML},
		})
		if r := out.res; r.Quality != 80 || r.Attempts != 1 || r.AutoQuality.Ignored != encode.IgnoredML {
			t.Errorf("ml: %+v", r)
		}
	})
	t.Run("config dssim", func(t *testing.T) {
		t.Parallel()
		e := newEncoder(t, func(c *encode.Config) { c.AutoQuality.Method = transform.AutoQualityDSSIM })
		out := encodeCorpus(t, e, in, &encode.Target{Format: transform.FormatWebP, Metadata: aqMeta})
		if r := out.res; r.AutoQuality.Method != transform.AutoQualityDSSIM || r.Attempts < 1 {
			t.Errorf("config dssim: %+v", r)
		}
		explicit := encodeCorpus(t, e, in, &encode.Target{Format: transform.FormatWebP, Quality: 50, Metadata: aqMeta})
		if r := explicit.res; r.Quality != 50 || r.Attempts != 1 || r.AutoQuality.Method != transform.AutoQualityUnset {
			t.Errorf("explicit quality: %+v", r)
		}
	})
}

// TestAutoQualityCanceled stops the search when ctx ends.
func TestAutoQualityCanceled(t *testing.T) {
	t.Parallel()
	e := newEncoder(t, nil)
	buf := newBuffer(t, readFixture(t, "jpeg/baseline.jpg"))
	ctx, cancel := context.WithCancel(t.Context())
	err := vips.Run(ctx, func() error {
		img, err := vips.Load(ctx, buf, nil)
		if err != nil {
			return err
		}
		defer img.Close()
		cancel()
		_, err = e.Encode(ctx, img, &encode.Target{
			Format: transform.FormatJPEG, AutoQuality: &transform.AutoQuality{Method: transform.AutoQualityDSSIM},
		})
		return err
	})
	if err == nil {
		t.Fatal("canceled encode succeeded")
	}
}

// BenchmarkAutoQuality compares a plain encode with the dssim search of a
// 1024 px photo (compared on the 512 px copy) and a 320 px one.
// Results (benchstat, -count=10): testdata/bench-autoquality.txt.
func BenchmarkAutoQuality(b *testing.B) {
	e := newEncoder(b, nil)
	buf := newBuffer(b, readFixture(b, "jpeg/baseline.jpg"))
	for _, width := range []int{320, 1024} {
		for _, f := range aqFormats {
			for _, method := range []transform.AutoQualityMethod{transform.AutoQualityNone, transform.AutoQualityDSSIM} {
				b.Run(strconv.Itoa(width)+"/"+f.String()+"/"+method.String(), func(b *testing.B) {
					ctx := b.Context()
					err := vips.Run(ctx, func() error {
						var a vips.Arena
						defer a.Close()
						img, err := a.Add(vips.Load(ctx, buf, nil))
						if err != nil {
							return err
						}
						if img, err = a.Add(img.Resize(ctx, float64(width)/320, 0, vips.KernelLanczos3)); err != nil {
							return err
						}
						if img, err = a.Add(img.CopyMemory(ctx)); err != nil {
							return err
						}
						t := encode.Target{Format: f, Metadata: aqMeta, AutoQuality: &transform.AutoQuality{Method: method}}
						b.ReportAllocs()
						saves := 0
						for b.Loop() {
							res, err := e.Encode(ctx, img, &t)
							if err != nil {
								return err
							}
							saves += res.Attempts
							res.Blob.Release()
						}
						b.ReportMetric(float64(saves)/float64(b.N), "saves/op")
						return nil
					})
					if err != nil {
						b.Fatal(err)
					}
				})
			}
		}
	}
}
