//go:build cgo

package exec_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec/encode"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/exec"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// TestGoldenHDR renders every HDR input format (and an 8-bit PPM) through
// the whole pipeline to 8-bit output and compares them with
// testdata/golden/exec/<name>.png. The scene has about 5% of its pixels
// above 1.0 and a sun at 40 (testdata/gen/hdr.go).
func TestGoldenHDR(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		c  goldenCase
		tm exec.ToneMap
	}{
		{c: goldenCase{name: "hdr-exr-rgba-half", fixture: "exr/rgba-half.exr", options: "rs:fit:64:0", out: transform.FormatPNG, w: 64, h: 48}},
		{c: goldenCase{name: "hdr-exr-rgb-float", fixture: "exr/rgb-float.exr", out: transform.FormatPNG, w: 48, h: 32}},
		{c: goldenCase{name: "hdr-radiance", fixture: "hdr/scene.hdr", options: "rs:fit:64:0", out: transform.FormatJPEG, w: 64, h: 43}},
		{c: goldenCase{name: "hdr-rgbe", fixture: "hdr/rgbe.hdr", out: transform.FormatPNG, w: 48, h: 32}},
		{c: goldenCase{name: "hdr-pfm", fixture: "pfm/scene.pfm", out: transform.FormatPNG, w: 64, h: 48}},
		{c: goldenCase{name: "ppm-photo", fixture: "ppm/photo.ppm", out: transform.FormatPNG, w: 96, h: 64}},
		// The other operators on the same source.
		{c: goldenCase{name: "hdr-radiance-aces", fixture: "hdr/scene.hdr", out: transform.FormatPNG, w: 96, h: 64}, tm: exec.ToneMapACES},
		{c: goldenCase{name: "hdr-radiance-clip", fixture: "hdr/scene.hdr", out: transform.FormatPNG, w: 96, h: 64}, tm: exec.ToneMapClip},
	} {
		t.Run(tc.c.name, func(t *testing.T) {
			t.Parallel()
			o := runToneMap(t, readFixture(t, tc.c.fixture), tc.c.options, tc.c.out, tc.tm)
			checkOutput(t, &tc.c, &o)
			golden.Compare(t, "exec/"+tc.c.name, o.img, golden.Options{Tolerance: tc.c.tolerance})
		})
	}
}

// TestHDRAlpha checks that OpenEXR alpha survives tone mapping (the bottom
// quarter of the fixture fades from transparent to opaque) and that a file
// without an A channel has no alpha band, although libvips loads one.
func TestHDRAlpha(t *testing.T) {
	t.Parallel()
	o := runToneMap(t, readFixture(t, "exr/rgba-half.exr"), "", transform.FormatPNG, exec.ToneMapReinhard)
	for _, p := range []struct {
		x, y int
		lo   uint8
		hi   uint8
	}{
		{0, 0, 255, 255},   // top: opaque
		{0, 59, 0, 5},      // bottom left: transparent
		{79, 59, 245, 255}, // bottom right: opaque
		{40, 59, 120, 135},
	} {
		if a := asNRGBA(t, o.img).NRGBAAt(p.x, p.y).A; a < p.lo || a > p.hi {
			t.Errorf("alpha at (%d, %d) = %d, want %d..%d", p.x, p.y, a, p.lo, p.hi)
		}
	}
	if bands := decodedBands(t, runToneMap(t, readFixture(t, "exr/rgb-float.exr"), "", transform.FormatPNG, exec.ToneMapReinhard).bytes); bands != 3 {
		t.Errorf("rgb exr gave %d bands, want 3", bands)
	}
}

// TestToneMapRamp maps a known linear ramp (a PFM, R = G = B) through each
// operator and checks every output pixel against the documented formulas:
// the white point W is the nearest-rank 99th percentile of the luminance
// (≥ 1), Reinhard is x(1 + x/W²)/(1 + x), ACES Narkowicz's curve on
// x·min(0.6, 7.2417/W), clip is min(x, 1); then the sRGB transfer to 8 bits.
func TestToneMapRamp(t *testing.T) {
	t.Parallel()
	aces := func(x float64) float64 { return x * (2.51*x + 0.03) / (x*(2.43*x+0.59) + 0.14) }
	for _, tc := range []struct {
		name string
		top  float64 // the ramp goes 0 … top in 256 steps
		tm   exec.ToneMap
		f    func(x, w float64) float64
	}{
		{"clip", 4, exec.ToneMapClip, func(x, _ float64) float64 { return x }},
		{"reinhard", 4, exec.ToneMapReinhard, func(x, w float64) float64 { return x * (1 + x/(w*w)) / (1 + x) }},
		{"reinhard sdr is identity", 1, exec.ToneMapReinhard, func(x, _ float64) float64 { return x }},
		{"aces", 4, exec.ToneMapACES, func(x, w float64) float64 { return aces(x * min(0.6, 7.241665/w)) }},
		{"aces bright", 64, exec.ToneMapACES, func(x, w float64) float64 { return aces(x * min(0.6, 7.241665/w)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			const n = 256
			ramp := make([]float64, n)
			for i := range ramp {
				ramp[i] = tc.top * float64(i) / (n - 1)
			}
			w := max(ramp[(99*n+99)/100-1], 1) // sorted already
			o := runToneMap(t, rampPFM(ramp), "", transform.FormatPNG, tc.tm)
			img := asNRGBA(t, o.img)
			if img.Rect.Dx() != n || img.Rect.Dy() != 1 {
				t.Fatalf("output %v", img.Rect)
			}
			worst := 0
			for i, x := range ramp {
				want := srgb8(math.Min(math.Max(tc.f(x, w), 0), 1))
				c := img.NRGBAAt(i, 0)
				if c.R != c.G || c.G != c.B {
					t.Fatalf("pixel %d is not grey: %v", i, c)
				}
				d := int(c.R) - want
				worst = max(worst, d, -d)
				if d > 1 || d < -1 {
					t.Errorf("x = %.4f: got %d, want %d", x, c.R, want)
				}
			}
			t.Logf("W = %.4f, worst difference %d", w, worst)
		})
	}
}

// TestPreserveHDR checks preserve_hdr: JPEG XL keeps float samples (above
// 1.0 included), AVIF keeps 12 bits, and outputs or steps that cannot hold
// them clip to 8 bits and report preserve_hdr as skipped.
func TestPreserveHDR(t *testing.T) {
	t.Parallel()
	exr := readFixture(t, "exr/rgba-half.exr")
	for _, tc := range []struct {
		name, options string
		out           transform.Format
		format        vips.BandFormat // of the decoded output
		minMax        float64         // the brightest decoded sample is at least this
		skipped       bool
	}{
		{name: "jxl float", options: "ph:1/rs:fit:64:0", out: transform.FormatJXL, format: vips.BandFormatFloat, minMax: 8},
		{name: "jxl without preserve", options: "rs:fit:64:0", out: transform.FormatJXL, format: vips.BandFormatUchar},
		{name: "avif 12-bit", options: "ph:1", out: transform.FormatAVIF, format: vips.BandFormatUshort},
		{name: "png clips", options: "ph:1", out: transform.FormatPNG, format: vips.BandFormatUchar, skipped: true},
		{name: "jxl with blur clips", options: "ph:1/bl:1", out: transform.FormatJXL, format: vips.BandFormatUchar, skipped: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			j, buf := job(t, exr, parse(t, tc.options), tc.out)
			defer buf.Release()
			res, err := newExecutor(t).Run(t.Context(), j)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Release()
			if got := slices.Contains(res.Skipped, "preserve_hdr"); got != tc.skipped {
				t.Errorf("skipped %v, want preserve_hdr skipped %v", res.Skipped, tc.skipped)
			}
			bf, top := decodedFormat(t, bytes.Clone(res.Body.Bytes()))
			if bf != tc.format {
				t.Errorf("decoded band format %v, want %v", bf, tc.format)
			}
			if top < tc.minMax {
				t.Errorf("brightest sample %g, want ≥ %g", top, tc.minMax)
			}
		})
	}
	// A source that is not HDR reports preserve_hdr as skipped.
	o := process(t, "png/rgb16.png", "ph:1", transform.FormatPNG)
	if !slices.Contains(o.res.Skipped, "preserve_hdr") {
		t.Errorf("16-bit PNG: skipped %v, want preserve_hdr", o.res.Skipped)
	}
}

func TestParseToneMap(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]exec.ToneMap{
		"": exec.ToneMapReinhard, "reinhard": exec.ToneMapReinhard, "ACES": exec.ToneMapACES, "clip": exec.ToneMapClip,
	} {
		got, err := exec.ParseToneMap(in)
		if err != nil || got != want {
			t.Errorf("ParseToneMap(%q) = %v, %v; want %v", in, got, err, want)
		}
		if in != "" && got.String() != strings.ToLower(in) {
			t.Errorf("%v.String() = %q", got, got.String())
		}
	}
	if _, err := exec.ParseToneMap("filmic"); err == nil {
		t.Error("ParseToneMap(filmic) succeeded")
	}
}

// asNRGBA is the decoded output as the NRGBA image decode returns.
func asNRGBA(tb testing.TB, img image.Image) *image.NRGBA {
	tb.Helper()
	n, ok := img.(*image.NRGBA)
	if !ok {
		tb.Fatalf("decoded %T", img)
	}
	return n
}

// runToneMap processes data with an executor using tm.
func runToneMap(tb testing.TB, data []byte, options string, out transform.Format, tm exec.ToneMap) output {
	tb.Helper()
	j, buf := job(tb, data, parse(tb, options), out)
	defer buf.Release()
	ec := encode.DefaultConfig()
	enc, err := encode.New(&ec)
	if err != nil {
		tb.Fatal(err)
	}
	e, err := exec.New(&exec.Config{Limits: testLimits, ToneMap: tm}, enc)
	if err != nil {
		tb.Fatal(err)
	}
	res, err := e.Run(tb.Context(), j)
	if err != nil {
		tb.Fatalf("run %q: %v", options, err)
	}
	defer res.Body.Release()
	enc2 := bytes.Clone(res.Body.Bytes())
	img, frames := decode(tb, enc2)
	return output{res: res, img: img, frames: frames, bytes: enc2}
}

// rampPFM is a one-row colour PFM with R = G = B = ramp[i] at pixel i.
func rampPFM(ramp []float64) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "PF\n%d 1\n-1.0\n", len(ramp))
	for _, v := range ramp {
		for range 3 {
			_ = binary.Write(&b, binary.LittleEndian, float32(v))
		}
	}
	return b.Bytes()
}

// srgb8 is the sRGB transfer function (IEC 61966-2-1) to 8 bits.
func srgb8(v float64) int {
	if v <= 0.0031308 {
		v *= 12.92
	} else {
		v = 1.055*math.Pow(v, 1/2.4) - 0.055
	}
	return int(math.Round(255 * v))
}

// decodedBands is the band count of an encoded image as libvips loads it.
func decodedBands(tb testing.TB, data []byte) int {
	tb.Helper()
	buf := newBuffer(tb, data)
	defer buf.Release()
	var bands int
	err := vips.Run(tb.Context(), func() error {
		var a vips.Arena
		defer a.Close()
		img, err := a.Add(vips.Load(tb.Context(), buf, &vips.LoadOptions{}))
		if err != nil {
			return err
		}
		bands = img.Bands()
		return nil
	})
	if err != nil {
		tb.Fatal(err)
	}
	return bands
}

// decodedFormat loads an encoded image and returns its band format and
// brightest colour sample (as float, in the band format's own units for
// integer formats, linear light for float).
func decodedFormat(tb testing.TB, data []byte) (vips.BandFormat, float64) {
	tb.Helper()
	buf := newBuffer(tb, data)
	defer buf.Release()
	var bf vips.BandFormat
	var top float64
	err := vips.Run(tb.Context(), func() error {
		var a vips.Arena
		defer a.Close()
		ctx := tb.Context()
		img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{}))
		if err != nil {
			return err
		}
		bf = img.BandFormat()
		if img, err = a.Add(img.ExtractBand(ctx, 0, 3)); err != nil {
			return err
		}
		if img, err = a.Add(img.Cast(ctx, vips.BandFormatFloat)); err != nil {
			return err
		}
		pix, err := img.WriteToMemory(ctx)
		if err != nil {
			return err
		}
		for i := 0; i+4 <= len(pix); i += 4 {
			top = max(top, float64(math.Float32frombits(binary.NativeEndian.Uint32(pix[i:]))))
		}
		return nil
	})
	if err != nil {
		tb.Fatal(err)
	}
	return bf, top
}
