//go:build cgo

package encode_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"github.com/xantiagoma/xtandard-image/internal/codec/encode"
	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/testutil/leakcheck"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

const acceptModern = "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"

// bestInput is one best-format corpus image: a fixture (or synthetic PNG)
// resized to width (0 keeps the size), or rendered at density.
type bestInput struct {
	name    string
	data    func(testing.TB) []byte
	width   int
	density float64
	nPages  int // -1 loads every frame
	// wantLossless: the chosen candidate must be (or must not be) a
	// lossless save, unless anyLossless (mixed content: the sizes decide).
	wantLossless bool
	anyLossless  bool
	wantClass    []negotiate.ContentClass
}

func fixture(name string) func(testing.TB) []byte {
	return func(tb testing.TB) []byte { tb.Helper(); return readFixture(tb, name) }
}

func synthetic(f func() image.Image) func(testing.TB) []byte {
	return func(tb testing.TB) []byte {
		tb.Helper()
		var b bytes.Buffer
		if err := png.Encode(&b, f()); err != nil {
			tb.Fatal(err)
		}
		return b.Bytes()
	}
}

// flatShapes is a flat-colour illustration: a background, bands and boxes
// in six colours.
func flatShapes() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 480, 320))
	fill := func(r image.Rectangle, c color.RGBA) {
		draw.Draw(img, r, &image.Uniform{c}, image.Point{}, draw.Src)
	}
	fill(img.Bounds(), color.RGBA{0xf4, 0xf1, 0xe8, 0xff})
	fill(image.Rect(0, 0, 480, 48), color.RGBA{0x1d, 0x3a, 0x6e, 0xff})
	fill(image.Rect(40, 90, 200, 250), color.RGBA{0xe6, 0x39, 0x46, 0xff})
	fill(image.Rect(240, 90, 440, 150), color.RGBA{0x2a, 0x9d, 0x8f, 0xff})
	fill(image.Rect(240, 170, 440, 250), color.RGBA{0xe9, 0xc4, 0x6a, 0xff})
	fill(image.Rect(0, 290, 480, 320), color.RGBA{0x26, 0x46, 0x53, 0xff})
	return img
}

// textImage is black text on white (a screenshot of a document).
func textImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 480, 320))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	d := font.Drawer{Dst: img, Src: image.Black, Face: basicfont.Face7x13}
	lines := []string{
		"xtandard-image best format selection", "",
		"Graphics with flat colours and text compress", "best without loss: the encoder tries WebP",
		"lossless and PNG, and keeps the smallest.", "",
		"Photos are tried lossy: AVIF, WebP and JPEG", "at the configured or searched quality.", "",
		"0123456789 ABCDEFGHIJKLMNOPQRSTUVWXYZ", "abcdefghijklmnopqrstuvwxyz !?@#%&*()[]",
	}
	for i, l := range lines {
		d.Dot = fixed.P(16, 28+i*20)
		d.DrawString(l)
	}
	return img
}

// chart is a bar chart with axes, grid lines and labels on white.
func chart() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 480, 320))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	grey := &image.Uniform{color.RGBA{0xcc, 0xcc, 0xcc, 0xff}}
	for y := 40; y < 280; y += 40 {
		draw.Draw(img, image.Rect(40, y, 460, y+1), grey, image.Point{}, draw.Src)
	}
	draw.Draw(img, image.Rect(40, 280, 460, 282), image.Black, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(40, 30, 42, 282), image.Black, image.Point{}, draw.Src)
	cols := []color.RGBA{{0x4e, 0x79, 0xa7, 0xff}, {0xf2, 0x8e, 0x2b, 0xff}, {0x59, 0xa1, 0x4f, 0xff}}
	for i, h := range []int{120, 200, 90, 230, 160, 60, 180} {
		draw.Draw(img, image.Rect(60+i*56, 280-h, 60+i*56+36, 280), &image.Uniform{cols[i%3]}, image.Point{}, draw.Src)
	}
	d := font.Drawer{Dst: img, Src: image.Black, Face: basicfont.Face7x13}
	for i, l := range []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"} {
		d.Dot = fixed.P(64+i*56, 300)
		d.DrawString(l)
	}
	return img
}

func bestCorpus() []bestInput {
	photo := []negotiate.ContentClass{negotiate.ContentPhoto}
	graphic := []negotiate.ContentClass{negotiate.ContentGraphic}
	return []bestInput{
		{name: "photo/jpeg-baseline", data: fixture("jpeg/baseline.jpg"), width: 320, wantClass: photo},
		{name: "photo/jpeg-baseline-large", data: fixture("jpeg/baseline.jpg"), width: 1024, wantClass: photo},
		{name: "photo/jpeg-progressive", data: fixture("jpeg/progressive.jpg"), width: 320, wantClass: photo},
		{name: "photo/png-rgba", data: fixture("png/rgba8.png"), width: 320, wantClass: photo},
		{name: "photo/png-16bit", data: fixture("png/rgb16.png"), width: 320, wantClass: photo},
		{name: "photo/png-bordered", data: fixture("png/bordered.png"), width: 320, wantClass: photo},
		{name: "photo/webp-lossy", data: fixture("webp/lossy.webp"), width: 320, wantClass: photo},
		{
			name: "graphic/png-palette", data: fixture("png/palette.png"),
			wantLossless: true, wantClass: graphic,
		},
		{name: "graphic/gif", data: fixture("gif/static.gif"), wantLossless: true, wantClass: graphic},
		{name: "graphic/webp-lossless", data: fixture("webp/lossless.webp"), wantLossless: true, wantClass: graphic},
		{
			name: "graphic/svg-logo", data: fixture("svg/logo.svg"), density: 288,
			wantLossless: true, wantClass: graphic,
		},
		{
			// At 96×72 the anti-aliased edges and the sky gradient weigh as
			// much as the flat areas: both kinds of candidates are tried.
			name: "mixed/svg-logo-small", data: fixture("svg/logo.svg"), density: 72,
			anyLossless: true, wantClass: []negotiate.ContentClass{negotiate.ContentMixed},
		},
		{name: "graphic/flat-shapes", data: synthetic(flatShapes), wantLossless: true, wantClass: graphic},
		{name: "graphic/text", data: synthetic(textImage), wantLossless: true, wantClass: graphic},
		{name: "graphic/chart", data: synthetic(chart), wantLossless: true, wantClass: graphic},
		{
			name: "animated/gif", data: fixture("gif/animated.gif"), nPages: -1,
			wantLossless: true, wantClass: graphic,
		},
	}
}

// bestRun is one EncodeBest outcome, copied out of vips.Run.
type bestRun struct {
	res   encode.Result
	data  []byte
	pages int
}

// runBest loads in, prepares it like a processed image and runs EncodeBest
// with the decision of a format=best request under accept.
func runBest(tb testing.TB, e *encode.Encoder, in *bestInput, accept string) bestRun {
	tb.Helper()
	// Released here rather than at cleanup, so the leak test sees the
	// baseline between cycles.
	buf, err := vips.NewBufferFromReader(bytes.NewReader(in.data(tb)), 1<<30)
	if err != nil {
		tb.Fatal(err)
	}
	defer buf.Release()
	ctx := tb.Context()
	var out bestRun
	err = vips.Run(ctx, func() error {
		var a vips.Arena
		defer a.Close()
		img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Density: in.density, NPages: in.nPages}))
		if err != nil {
			return err
		}
		if in.width > 0 && img.Pages() == 1 {
			if img, err = a.Add(img.Resize(ctx, float64(in.width)/float64(img.Width()), 0, vips.KernelLanczos3)); err != nil {
				return err
			}
		}
		if img, err = a.Add(img.CopyMemory(ctx)); err != nil {
			return err
		}
		out.pages = img.Pages()
		d := bestDecision(tb, img, accept)
		t := encode.Target{
			Format:   d.Format,
			Metadata: encode.Metadata{Strip: true, KeepCopyright: true, StripColorProfile: true},
			Animated: img.Pages() > 1,
		}
		res, err := e.EncodeBest(ctx, img, &t, &d)
		if err != nil {
			return err
		}
		defer res.Blob.Release()
		out.data = bytes.Clone(res.Blob.Bytes())
		out.res = *res
		out.res.Blob = nil
		return nil
	})
	if err != nil {
		tb.Fatalf("%s: %v", in.name, err)
	}
	return out
}

func bestDecision(tb testing.TB, img *vips.Image, accept string) negotiate.Decision {
	tb.Helper()
	req := &transform.Request{Pipelines: []transform.Pipeline{{}}}
	req.Output.Format = transform.FormatBest
	cfg := negotiate.DefaultConfig()
	d, err := negotiate.Decide(req, negotiate.ParseAccept(accept), &cfg, negotiate.SourceMeta{
		Format: transform.FormatPNG, HasAlpha: img.HasAlpha(), IsAnimated: img.Pages() > 1, FrameCount: img.Pages(),
		Pixels: int64(img.Width()) * int64(img.Height()),
	})
	if err != nil {
		tb.Fatal(err)
	}
	return d
}

// checkBest asserts the invariants of every selection: the chosen try is
// the returned output, it meets the bar when any try does, and no other
// qualifying try is smaller.
func checkBest(t *testing.T, name string, out *bestRun) *encode.BestTry {
	t.Helper()
	br := out.res.Best
	if br == nil || br.Chosen < 0 || br.Chosen >= len(br.Tries) {
		t.Fatalf("%s: no selection: %+v", name, br)
	}
	chosen := &br.Tries[br.Chosen]
	if chosen.Candidate.Format != out.res.Format || chosen.Bytes != len(out.data) {
		t.Errorf("%s: result %s/%d B is not the chosen try %+v", name, out.res.Format, len(out.data), *chosen)
	}
	for i, try := range br.Tries {
		if i == br.Chosen || try.Unsupported {
			continue
		}
		if try.Met && !chosen.Met {
			t.Errorf("%s: chose unmet %+v over met %+v", name, *chosen, try)
		}
		if try.Met == chosen.Met && try.Bytes < chosen.Bytes {
			t.Errorf("%s: chose %+v over smaller %+v", name, *chosen, try)
		}
	}
	return chosen
}

// TestEncodeBestCorpus is the WP-57 done criterion: photos pick a lossy
// format and graphics (flat colours, text, charts, the graphic fixtures)
// pick a lossless one, and the chosen output is never larger than another
// qualifying candidate. It runs with the configured quality and with the
// dssim auto-quality.
func TestEncodeBestCorpus(t *testing.T) {
	t.Parallel()
	encoders := map[string]*encode.Encoder{
		"quality": newEncoder(t, nil),
		"dssim": newEncoder(t, func(c *encode.Config) {
			c.AutoQuality.Method = transform.AutoQualityDSSIM
		}),
	}
	for _, in := range bestCorpus() {
		for mode, e := range encoders {
			t.Run(in.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				out := runBest(t, e, &in, acceptModern)
				chosen := checkBest(t, in.name, &out)
				an := out.res.Best.Analysis
				t.Logf("class %s complexity %.2f (gradient entropy %.2f, colours %d, edges %.3f, alpha %v): tries %+v, chose %s lossless=%v %d B",
					an.Class, an.Complexity, an.GradientEntropy, an.Colors, an.EdgeDensity, an.AlphaUsed,
					out.res.Best.Tries, chosen.Candidate.Format, chosen.Candidate.Lossless, chosen.Bytes)
				classOK := false
				for _, c := range in.wantClass {
					classOK = classOK || an.Class == c
				}
				if !classOK {
					t.Errorf("class %s (complexity %.2f), want one of %v", an.Class, an.Complexity, in.wantClass)
				}
				lossless := chosen.Candidate.Lossless || chosen.Candidate.Format == transform.FormatPNG || chosen.Candidate.Format == transform.FormatGIF
				if !in.anyLossless && lossless != in.wantLossless {
					t.Errorf("chose %+v (lossless %v), want lossless %v", *chosen, lossless, in.wantLossless)
				}
				if out.pages > 1 {
					if f := chosen.Candidate.Format; f != transform.FormatWebP && f != transform.FormatGIF {
						t.Errorf("animation chose %s", f)
					}
					for _, try := range out.res.Best.Tries {
						if f := try.Candidate.Format; f != transform.FormatWebP && f != transform.FormatGIF && f != transform.FormatAVIF {
							t.Errorf("animation tried still-only %s", f)
						}
					}
				}
			})
		}
	}
}

// TestEncodeBestAccept checks that the candidates follow Accept: a legacy
// client gets JPEG for a photo and PNG for a graphic.
func TestEncodeBestAccept(t *testing.T) {
	t.Parallel()
	e := newEncoder(t, nil)
	legacy := "image/png,image/*;q=0.8,*/*;q=0.5"
	for _, tc := range []struct {
		in   bestInput
		want transform.Format
	}{
		{bestInput{name: "photo", data: fixture("jpeg/baseline.jpg"), width: 320}, transform.FormatJPEG},
		{bestInput{name: "graphic", data: synthetic(flatShapes)}, transform.FormatPNG},
	} {
		t.Run(tc.in.name, func(t *testing.T) {
			t.Parallel()
			out := runBest(t, e, &tc.in, legacy)
			checkBest(t, tc.in.name, &out)
			if out.res.Format != tc.want {
				t.Errorf("format %s, want %s", out.res.Format, tc.want)
			}
			for _, try := range out.res.Best.Tries {
				if f := try.Candidate.Format; f == transform.FormatAVIF || f == transform.FormatWebP {
					t.Errorf("tried %s without Accept", f)
				}
			}
		})
	}
}

// TestEncodeBestMaxPixels checks the XI_FORMATS_BEST_MAX_PIXELS cap: above
// it the negotiated format is encoded once, without analysis.
func TestEncodeBestMaxPixels(t *testing.T) {
	t.Parallel()
	e := newEncoder(t, func(c *encode.Config) { c.Best.MaxPixels = 100 * 100 })
	out := runBest(t, e, &bestInput{name: "graphic", data: synthetic(flatShapes)}, acceptModern)
	br := out.res.Best
	if br == nil || br.Skipped != encode.BestSkippedPixels || br.Chosen != -1 || len(br.Tries) != 0 {
		t.Fatalf("best = %+v, want skipped by pixels", br)
	}
	if out.res.Format != transform.FormatAVIF || out.res.Attempts != 1 {
		t.Errorf("format %s after %d saves, want the negotiated avif once", out.res.Format, out.res.Attempts)
	}
}

// TestEncodeBestThresholdZero checks that a zero complexity threshold skips
// the analysis and tries the photo row, like imgproxy.
func TestEncodeBestThresholdZero(t *testing.T) {
	t.Parallel()
	e := newEncoder(t, func(c *encode.Config) { c.Best.ComplexityThreshold = 0 })
	out := runBest(t, e, &bestInput{name: "graphic", data: synthetic(flatShapes)}, acceptModern)
	checkBest(t, "threshold-0", &out)
	if an := out.res.Best.Analysis; an.Class != negotiate.ContentPhoto || an.Colors != 0 {
		t.Errorf("analysis %+v, want an unanalysed photo", an)
	}
	for _, try := range out.res.Best.Tries {
		if try.Candidate.Lossless {
			t.Errorf("tried lossless %+v", try)
		}
	}
}

// TestEncodeBestCanceled checks that a canceled context stops the
// selection with the context's kind and leaks nothing.
func TestEncodeBestCanceled(t *testing.T) {
	t.Parallel()
	e := newEncoder(t, nil)
	buf := newBuffer(t, readFixture(t, "jpeg/baseline.jpg"))
	ctx, cancel := context.WithCancel(t.Context())
	err := vips.Run(ctx, func() error {
		var a vips.Arena
		defer a.Close()
		img, err := a.Add(vips.Load(ctx, buf, nil))
		if err != nil {
			return err
		}
		d := bestDecision(t, img, acceptModern)
		cancel()
		res, err := e.EncodeBest(ctx, img, &encode.Target{Format: d.Format}, &d)
		if err == nil {
			res.Blob.Release()
		}
		return err
	})
	if err == nil {
		t.Fatal("EncodeBest succeeded on a canceled context")
	}
}

// TestEncodeBestNoLeak runs selections (still, animated, skipped by pixels)
// with libvips leak checking and checks that tracked memory, allocations and
// live objects return to their baseline. Not parallel: the counters are
// global.
func TestEncodeBestNoLeak(t *testing.T) {
	const cycles = 60
	vips.SetLeakCheck(true)
	defer vips.SetLeakCheck(false)
	e := newEncoder(t, func(c *encode.Config) { c.AutoQuality.Method = transform.AutoQualityDSSIM })
	capped := newEncoder(t, func(c *encode.Config) { c.Best.MaxPixels = 1000 })
	inputs := []bestInput{
		{name: "photo", data: fixture("jpeg/baseline.jpg"), width: 160},
		{name: "graphic", data: synthetic(textImage)},
		{name: "animated", data: fixture("gif/animated.gif"), nPages: -1},
	}
	for i := range inputs {
		inputs[i].data = func(b []byte) func(testing.TB) []byte {
			return func(testing.TB) []byte { return b }
		}(inputs[i].data(t))
	}
	cycle := func(i int) {
		enc := e
		if i%4 == 3 {
			enc = capped
		}
		runBest(t, enc, &inputs[i%len(inputs)], acceptModern)
	}
	for i := range 2 * len(inputs) {
		cycle(i)
	}
	before := leakcheck.Take()
	for i := range cycles {
		cycle(i)
	}
	leakcheck.Check(t, before, leakcheck.Default, "selections")
}

// BenchmarkEncodeBest compares format=best with plain negotiation (one
// encode of the negotiated format) on a 640 px photo and graphic, with the
// configured quality. Results (benchstat, -count=10):
// testdata/bench-best.txt.
func BenchmarkEncodeBest(b *testing.B) {
	e := newEncoder(b, nil)
	inputs := []bestInput{
		{name: "photo", data: fixture("jpeg/baseline.jpg"), width: 640},
		{name: "graphic", data: synthetic(chart), width: 640},
	}
	for _, in := range inputs {
		data := in.data(b)
		for _, mode := range []string{"negotiate", "best"} {
			b.Run("img="+in.name+"/mode="+mode, func(b *testing.B) {
				buf := newBuffer(b, data)
				ctx := b.Context()
				err := vips.Run(ctx, func() error {
					var a vips.Arena
					defer a.Close()
					img, err := a.Add(vips.Load(ctx, buf, nil))
					if err != nil {
						return err
					}
					if img, err = a.Add(img.Resize(ctx, float64(in.width)/float64(img.Width()), 0, vips.KernelLanczos3)); err != nil {
						return err
					}
					if img, err = a.Add(img.CopyMemory(ctx)); err != nil {
						return err
					}
					d := bestDecision(b, img, acceptModern)
					t := encode.Target{Format: d.Format, Metadata: encode.Metadata{Strip: true, KeepCopyright: true, StripColorProfile: true}}
					b.ReportAllocs()
					size := 0
					for b.Loop() {
						var res *encode.Result
						if mode == "best" {
							res, err = e.EncodeBest(ctx, img, &t, &d)
						} else {
							res, err = e.Encode(ctx, img, &t)
						}
						if err != nil {
							return err
						}
						size = res.Blob.Len()
						res.Blob.Release()
					}
					b.ReportMetric(float64(size), "out-B/op")
					return nil
				})
				if err != nil {
					b.Fatal(err)
				}
			})
		}
	}
}
