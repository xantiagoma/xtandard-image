//go:build cgo

package exec_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/exec"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/testutil/leakcheck"
	"github.com/xantiagoma/xtandard-image/internal/testutil/ssim"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// chainedCases are imgproxy's chained-pipelines examples
// (https://docs.imgproxy.net/features/chained_pipelines; imgproxy Pro, so
// there is no open-source imgproxy to compare with) and the combinations
// the work package names. The watermark is the configured one: per-request
// watermark URLs (wmu) are a later work package. Each is checked against
// testdata/golden/exec/<name>.png and, in TestChainedEqualsSequential,
// against running its pipelines as separate requests.
var chainedCases = []goldenCase{
	// Docs "multiple watermarks": .../rs:fit:500:500/wm:0.5:nowe/wmu:…/-/wm:0.7:soea/wmu:…/...
	{name: "chain-doc-watermarks", fixture: jpegPhoto, options: "rs:fit:200:200/wm:0.5:nowe:0:0:0.3/-/wm:0.7:soea:0:0:0.3", out: transform.FormatPNG, w: 200, h: 150},
	// Fill, watermark the filled image, then shrink it with its watermark.
	{name: "chain-fill-wm-fit", fixture: jpegPhoto, options: "rs:fill:200:200/-/wm:0.5:soea:0:0:0.4/-/rs:fit:100:100", out: transform.FormatPNG, w: 100, h: 100},
	// Crop, then resize, then blur.
	{name: "chain-crop-resize-blur", fixture: jpegPhoto, options: "c:200:150:nowe/-/rs:fit:100:100/-/bl:2", out: transform.FormatPNG, w: 100, h: 75},
	// Extend, then watermark the extended canvas (placed on its corner).
	{name: "chain-extend-wm", fixture: jpegPhoto, options: "rs:fit:160:160/ex:1/bg:40:40:40/-/wm:1:soea:4:4:0.3", out: transform.FormatPNG, w: 160, h: 160},
	// Rotate the result of a fill, flip it, smart-crop it.
	{name: "chain-rotate-smart", fixture: jpegPhoto, options: "rs:fill:120:90/-/rot:90/fl:1:0/-/rs:fill:60:60/g:sm", out: transform.FormatPNG, w: 60, h: 60},
	// Transparency survives a lazy (unmaterialised) boundary and a
	// materialised one.
	{name: "chain-png-alpha", fixture: pngAlpha, options: "rs:fit:96:96/-/ex:1:so/rs:fit:100:100/-/bl:1", out: transform.FormatPNG, w: 100, h: 100},
	// Animations: every pipeline runs on every frame.
	{name: "chain-gif", fixture: gifAnim, options: "rs:fill:64:48/-/c:48:48/-/rot:90/wm:1:soea:2:2:0.4", out: transform.FormatGIF, w: 48, h: 48, frames: 6},
	{name: "chain-webp-anim", fixture: webpAnim, options: "rs:fill:64:48/-/rs:fit:32:32", out: transform.FormatWebP, w: 32, h: 24, frames: 6},
}

func TestChainedGolden(t *testing.T) {
	t.Parallel()
	e := newWatermarkExecutor(t, newWatermark(t, watermarkLogo, "png", 1))
	for _, tc := range chainedCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := processWith(t, e, tc.fixture, tc.options, tc.out)
			checkOutput(t, &tc, &o)
			if len(o.res.Skipped) > 0 {
				t.Errorf("Skipped = %v", o.res.Skipped)
			}
			golden.Compare(t, "exec/"+tc.name, o.img, golden.Options{Tolerance: tc.tolerance})
		})
	}
}

// TestChainedEqualsSequential is the oracle for the goldens: a chain gives
// the same pixels as its pipelines run one after the other as separate
// requests, each on the previous (lossless PNG or GIF/WebP) output.
func TestChainedEqualsSequential(t *testing.T) {
	t.Parallel()
	e := newWatermarkExecutor(t, newWatermark(t, watermarkLogo, "png", 1))
	for _, tc := range chainedCases {
		if tc.out != transform.FormatPNG {
			continue // lossy intermediates
		}
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			chained := processWith(t, e, tc.fixture, tc.options, tc.out)
			data := readFixture(t, tc.fixture)
			var seq output
			for p := range strings.SplitSeq(tc.options, "/-/") {
				seq = processDataWith(t, e, data, p, tc.out)
				data = seq.bytes
			}
			if cb, sb := chained.img.Bounds(), seq.img.Bounds(); cb != sb {
				t.Fatalf("chained %v, sequential %v", cb, sb)
			}
			r, err := ssim.Compare(chained.img, seq.img)
			if err != nil {
				t.Fatal(err)
			}
			if r.DSSIM > 0.002 {
				t.Errorf("DSSIM(chained, sequential) = %.5f", r.DSSIM)
			}
		})
	}
}

// padded is a 180x130 PNG: the 320x240 photo fitted to 120x90 on a white
// border of 20 px top and bottom, 30 px left and right.
func padded(t *testing.T) []byte {
	t.Helper()
	o := process(t, jpegPhoto, "rs:fit:120:120/pd:20:30/bg:255:255:255", transform.FormatPNG)
	if o.res.Width != 180 || o.res.Height != 130 {
		t.Fatalf("padded %dx%d, want 180x130", o.res.Width, o.res.Height)
	}
	return o.bytes
}

// TestChainedObservedSize: a chained pipeline is planned on the size the
// previous pipeline really produced. A trim's result cannot be predicted,
// so a relative crop or a fit after it proves it: planned on Build's
// prediction (the untrimmed 90x65 instead of the trimmed 62x47; resampling
// softened the border, so the trim keeps a pixel of it) the crop would be
// 45 px wide and the fit 21x16.
func TestChainedObservedSize(t *testing.T) {
	t.Parallel()
	src := padded(t)
	for _, tc := range []struct {
		name, options string
		w, h          int
	}{
		// Docs "fast trim": .../rs:fit:500:500/-/trim:10/...
		{name: "chain-doc-trim", options: "rs:fit:90:90/-/t:10", w: 62, h: 47},
		// Pipeline 2's crop is half of pipeline 1's output (90x65), not of
		// the source.
		{name: "chain-relative-crop", options: "rs:fit:90:90/-/c:0.5:0.5:nowe", w: 45, h: 33},
		// Pipeline 3's crop is half of the trimmed output of pipeline 2.
		{name: "chain-trim-relative-crop", options: "rs:fit:90:90/-/t:10/-/c:0.5:0.5:nowe", w: 31, h: 24},
		// A resize after a chained trim scales the trimmed size.
		{name: "chain-trim-fit", options: "rs:fit:90:90/-/t:10/rs:fit:30:30", w: 30, h: 23},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := processData(t, src, tc.options, transform.FormatPNG)
			if o.res.Width != tc.w || o.res.Height != tc.h {
				t.Errorf("size = %dx%d, want %dx%d", o.res.Width, o.res.Height, tc.w, tc.h)
			}
			golden.Compare(t, "exec/"+tc.name, o.img, golden.Options{})
		})
	}
}

// TestChainedAnimationTiming: frames, delays and loop count survive every
// pipeline of a chain.
func TestChainedAnimationTiming(t *testing.T) {
	t.Parallel()
	for _, src := range []string{gifAnim, webpAnim} {
		want := readTiming(t, readFixture(t, src))
		for _, options := range []string{
			"rs:fill:64:48/-/c:48:48/-/rot:90",
			"rs:fit:48:48/-/bl:1/-/pd:2",
		} {
			t.Run(src+"/"+options, func(t *testing.T) {
				t.Parallel()
				o := process(t, src, options, transform.FormatGIF)
				if diff := cmp.Diff(want, readTiming(t, o.bytes)); diff != "" {
					t.Errorf("timing (-source +output):\n%s", diff)
				}
				if o.res.Frames != want.Frames {
					t.Errorf("Result.Frames = %d, want %d", o.res.Frames, want.Frames)
				}
			})
		}
	}
}

// TestChainedICCOnce: the source profile is imported by the first pipeline
// only; a chained pipeline must not apply it again to sRGB pixels.
func TestChainedICCOnce(t *testing.T) {
	t.Parallel()
	single := process(t, metadataP3, "rs:fit:120:120", transform.FormatPNG)
	for _, options := range []string{
		"rs:fit:120:120/-/rs:fit:120:120",
		"rs:fit:120:120/-/rot:180/-/rot:180",
	} {
		t.Run(options, func(t *testing.T) {
			t.Parallel()
			o := process(t, metadataP3, options, transform.FormatPNG)
			if d := meanAbsDiff(single.img, o.img); d > 0.5 {
				t.Errorf("mean abs diff to the single pipeline %.3f: colours converted twice", d)
			}
		})
	}
}

// TestChainedErrors: a failure in a chained pipeline names it, keeps its
// errs kind and leaves nothing behind.
func TestChainedErrors(t *testing.T) {
	t.Parallel()
	e := executorWithLimits(t, &plan.Limits{})
	// 65536 × zoom 1000 is past libvips' coordinate limit.
	j, buf := job(t, readFixture(t, jpegPhoto), parse(t, "rs:fit:10:10/-/rs:fit:65536:65536:1/z:1000"), transform.FormatPNG)
	defer buf.Release()
	_, err := e.Run(t.Context(), j)
	if errs.KindOf(err) != errs.KindLimitExceeded {
		t.Fatalf("err = %v, want KindLimitExceeded", err)
	}
	if !strings.Contains(err.Error(), "pipeline 1") {
		t.Errorf("err = %q, want it to name pipeline 1", err)
	}
}

// TestChainedNoLeak runs chains that materialise, trim, smart-crop,
// watermark and animate with libvips leak checking on, and checks that
// tracked memory, allocations and live objects return to their baseline
// (tolerances as vips.TestNoLeak). Not parallel: the counters are global.
func TestChainedNoLeak(t *testing.T) {
	const cycles = 300
	vips.SetLeakCheck(true)
	defer vips.SetLeakCheck(false)
	e := newWatermarkExecutor(t, newWatermark(t, watermarkLogo, "png", 1))
	src := padded(t)
	jobs := []struct {
		data    []byte
		options string
		out     transform.Format
	}{
		{src, "rs:fit:90:90/-/t:10/-/c:0.5:0.5/rs:fill:20:20/g:sm", transform.FormatPNG},
		{readFixture(t, jpegPhoto), "rs:fill:200:200/-/wm:0.5:soea/-/rs:fit:100:100/-/bl:2", transform.FormatJPEG},
		{readFixture(t, gifAnim), "rs:fill:64:48/-/c:48:48/-/rot:90", transform.FormatGIF},
	}
	cycle := func(i int) {
		jb := jobs[i%len(jobs)]
		j, buf := job(t, jb.data, parse(t, jb.options), jb.out)
		defer buf.Release()
		res, err := e.Run(t.Context(), j)
		if err != nil {
			t.Fatalf("run %q: %v", jb.options, err)
		}
		res.Body.Release()
	}
	for i := range 2 * len(jobs) {
		cycle(i)
	}
	before := leakcheck.Take()
	for i := range cycles {
		cycle(i)
	}
	after := leakcheck.Check(t, before, leakcheck.Default, "chained runs")
	t.Logf("after %d chained runs: mem %+d B, allocs %+d, objects %+d, highwater %d B",
		cycles, after.Mem-before.Mem, after.Allocs-before.Allocs, after.Objects-before.Objects, after.MemHighwater)
}

// processDataWith runs options on data with e and decodes the result.
func processDataWith(tb testing.TB, e *exec.Executor, data []byte, options string, out transform.Format) output {
	tb.Helper()
	j, buf := job(tb, data, parse(tb, options), out)
	defer buf.Release()
	res, err := e.Run(tb.Context(), j)
	if err != nil {
		tb.Fatalf("run %q: %v", options, err)
	}
	defer res.Body.Release()
	enc := bytes.Clone(res.Body.Bytes())
	img, frames := decode(tb, enc)
	return output{res: res, img: img, frames: frames, bytes: enc}
}
