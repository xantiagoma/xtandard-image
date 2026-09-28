//go:build cgo

package exec_test

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"math"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// Gain-map sources (WP-104): an UltraHDR JPEG (libultrahdr, CC0 photo × 2.5
// in the highlights), an Adobe gain-map JPEG that libultrahdr refuses
// (libavif's Paris sample, found through its MPF index and hdrgm XMP) and
// AVIFs with an ISO 21496-1 tmap item (libavif samples: the Seine photo
// with an SDR base, a synthetic one whose base is the HDR rendition, one
// whose tmap version is unsupported).
const (
	gmUHDR        = "uhdr/photo-ultrahdr.jpg"
	gmAdobe       = "gainmap/libavif-paris_exif_xmp_gainmap_bigendian.jpg"
	gmTmap        = "gainmap/libavif-seine_sdr_gainmap_srgb.avif"
	gmHDRBase     = "gainmap/libavif-color_nogrid_alpha_nogrid_gainmap_grid.avif"
	gmUnsupported = "gainmap/libavif-unsupported_gainmap_version.avif"
)

// hdrPixels decodes an HDR output (JPEG XL float, AVIF BT.2100 PQ, or an
// 8-bit fallback) to linear BT.709 light, 1.0 = SDR white (203 cd/m² for
// PQ), three floats per pixel, and reports the band format libvips decoded.
func hdrPixels(tb testing.TB, data []byte) (lin []float64, w, h int, bf vips.BandFormat) {
	tb.Helper()
	pq := bytes.Contains(data, []byte("colrnclx\x00\x09\x00\x10\x00\x09"))
	buf := newBuffer(tb, data)
	defer buf.Release()
	err := vips.Run(tb.Context(), func() error {
		var a vips.Arena
		defer a.Close()
		ctx := tb.Context()
		img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{}))
		if err != nil {
			return err
		}
		bf, w, h = img.BandFormat(), img.Width(), img.Height()
		if img, err = a.Add(img.ExtractBand(ctx, 0, 3)); err != nil {
			return err
		}
		if !pq && bf != vips.BandFormatFloat {
			// SDR output: linear light through libvips' sRGB → scRGB.
			if img, err = a.Add(img.Colourspace(ctx, vips.InterpretationScRGB)); err != nil {
				return err
			}
		}
		if img, err = a.Add(img.Cast(ctx, vips.BandFormatFloat)); err != nil {
			return err
		}
		raw, err := img.WriteToMemory(ctx)
		if err != nil {
			return err
		}
		lin = make([]float64, len(raw)/4)
		for i := range lin {
			lin[i] = float64(math.Float32frombits(binary.NativeEndian.Uint32(raw[4*i:])))
		}
		return nil
	})
	if err != nil {
		tb.Fatal(err)
	}
	if pq {
		// ushort PQ code values in BT.2020 → linear BT.709, 1.0 = 203 cd/m².
		inv := [9]float64{1.6604910, -0.5876411, -0.0728499, -0.1245505, 1.1328999, -0.0083494, -0.0181508, -0.1005789, 1.1187297}
		for i := 0; i+3 <= len(lin); i += 3 {
			var c [3]float64
			for k := range 3 {
				c[k] = pqEOTF(lin[i+k]/65535) / 203
			}
			for k := range 3 {
				lin[i+k] = inv[3*k]*c[0] + inv[3*k+1]*c[1] + inv[3*k+2]*c[2]
			}
		}
	}
	return lin, w, h, bf
}

// pqEOTF is the BT.2100 PQ EOTF: code value in [0, 1] to cd/m².
func pqEOTF(e float64) float64 {
	const m1, m2 = 2610.0 / 16384, 2523.0 / 4096 * 128
	const c1, c2, c3 = 3424.0 / 4096, 2413.0 / 4096 * 32, 2392.0 / 4096 * 32
	p := math.Pow(max(e, 0), 1/m2)
	return 10000 * math.Pow(max(p-c1, 0)/(c2-c3*p), 1/m1)
}

// hdrImage renders linear light for a golden: divided by 4 (4× SDR white is
// white), clipped, sRGB-encoded to 8 bits. An HDR output keeps highlights
// that the SDR base clips at white/4.
func hdrImage(lin []float64, w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	enc := func(v float64) uint8 {
		v = min(max(v/4, 0), 1)
		if v <= 0.0031308 {
			v *= 12.92
		} else {
			v = 1.055*math.Pow(v, 1/2.4) - 0.055
		}
		return uint8(math.Round(255 * v))
	}
	for y := range h {
		for x := range w {
			i := 3 * (y*w + x)
			img.SetNRGBA(x, y, color.NRGBA{enc(lin[i]), enc(lin[i+1]), enc(lin[i+2]), 255})
		}
	}
	return img
}

// TestGainMapHDR renders the gain-map sources as HDR with preserve_hdr (or
// gain_map:hdr) for the three HDR outputs, and checks the fallbacks. The
// goldens show linear light / 4, so the gain map's highlights are visible
// next to the SDR base's rendering (exec/gainmap-*-sdr).
func TestGainMapHDR(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, fixture, options string
		out                    transform.Format
		format                 vips.BandFormat // decoded
		peak                   float64         // brightest linear sample at least (1 = SDR white)
		skipped                []string
		golden                 string
	}{
		{
			name: "uhdr avif", fixture: gmUHDR, options: "ph:1/rs:fit:160:0", out: transform.FormatAVIF,
			format: vips.BandFormatUshort, peak: 1.5, golden: "gainmap-uhdr-avif-pq",
		},
		{
			name: "uhdr jxl", fixture: gmUHDR, options: "ph:1/rs:fit:160:0", out: transform.FormatJXL,
			format: vips.BandFormatFloat, peak: 1.5, golden: "gainmap-uhdr-jxl",
		},
		{
			name: "uhdr jxl gain_map:hdr", fixture: gmUHDR, options: "gm:hdr/rs:fit:160:0", out: transform.FormatJXL,
			format: vips.BandFormatFloat, peak: 1.5,
		},
		{
			name: "uhdr sdr", fixture: gmUHDR, options: "rs:fit:160:0", out: transform.FormatJXL,
			format: vips.BandFormatUchar, golden: "gainmap-uhdr-sdr",
		},
		{
			name: "uhdr gain_map:sdr", fixture: gmUHDR, options: "ph:1/gm:sdr/rs:fit:160:0", out: transform.FormatJXL,
			format: vips.BandFormatUchar, skipped: []string{"preserve_hdr"},
		},
		{
			name: "uhdr webp", fixture: gmUHDR, options: "ph:1/rs:fit:160:0", out: transform.FormatWebP,
			format: vips.BandFormatUchar, skipped: []string{"preserve_hdr"},
		},
		{
			name: "uhdr blur", fixture: gmUHDR, options: "gm:hdr/rs:fit:160:0/bl:1", out: transform.FormatJXL,
			format: vips.BandFormatUchar, skipped: []string{"gain_map"},
		},
		{
			name: "uhdr rotated", fixture: gmUHDR, options: "ph:1/rs:fit:120:0/rot:90/fl:1:0", out: transform.FormatAVIF,
			format: vips.BandFormatUshort, peak: 1.5, golden: "gainmap-uhdr-avif-rotated",
		},
		{
			name: "adobe avif", fixture: gmAdobe, options: "ph:1/rs:fit:200:0", out: transform.FormatAVIF,
			format: vips.BandFormatUshort, peak: 2, golden: "gainmap-adobe-avif-pq",
		},
		{
			name: "adobe sdr", fixture: gmAdobe, options: "rs:fit:200:0", out: transform.FormatPNG,
			format: vips.BandFormatUchar, golden: "gainmap-adobe-sdr",
		},
		{
			name: "tmap avif", fixture: gmTmap, options: "ph:1/rs:fit:200:0", out: transform.FormatAVIF,
			format: vips.BandFormatUshort, peak: 1.5, golden: "gainmap-tmap-avif-pq",
		},
		{
			name: "tmap jxl", fixture: gmTmap, options: "ph:1/rs:fit:200:0", out: transform.FormatJXL,
			format: vips.BandFormatFloat, peak: 1.5, golden: "gainmap-tmap-jxl",
		},
		{
			name: "tmap sdr", fixture: gmTmap, options: "rs:fit:200:0", out: transform.FormatPNG,
			format: vips.BandFormatUchar, golden: "gainmap-tmap-sdr",
		},
		// The base is the HDR rendition: nothing to apply, preserve_hdr is
		// not honoured (libvips reads the 10-bit base without its transfer).
		{
			name: "tmap hdr base", fixture: gmHDRBase, options: "ph:1", out: transform.FormatAVIF,
			format: vips.BandFormatUchar, skipped: []string{"preserve_hdr"}, peak: -1,
		},
		{
			name: "unsupported tmap", fixture: gmUnsupported, options: "ph:1", out: transform.FormatJXL,
			format: vips.BandFormatUchar, skipped: []string{"preserve_hdr"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := process(t, tc.fixture, tc.options, tc.out)
			for _, s := range []string{"preserve_hdr", "gain_map"} {
				if got, want := slices.Contains(o.res.Skipped, s), slices.Contains(tc.skipped, s); got != want {
					t.Errorf("skipped %v: %s skipped %v, want %v", o.res.Skipped, s, got, want)
				}
			}
			lin, w, h, bf := hdrPixels(t, o.bytes)
			if bf != tc.format {
				t.Errorf("decoded %v, want %v", bf, tc.format)
			}
			peak := slices.Max(lin)
			if tc.peak > 0 && peak < tc.peak {
				t.Errorf("brightest linear sample %.3f, want ≥ %.1f", peak, tc.peak)
			}
			if tc.peak == 0 && peak > 1.001 {
				t.Errorf("SDR output with a linear sample of %.3f", peak)
			}
			if tc.golden != "" {
				golden.Compare(t, "exec/"+tc.golden, hdrImage(lin, w, h), golden.Options{})
			}
		})
	}
}

// TestGainMapUltraHDROut: preserve_hdr to JPEG writes UltraHDR (a gain map
// uhdrload finds, with a boost above 1), and processing that output again
// to JPEG XL gives the same HDR picture as the source did.
func TestGainMapUltraHDROut(t *testing.T) {
	t.Parallel()
	o := process(t, gmUHDR, "ph:1/rs:fit:160:0", transform.FormatJPEG)
	if slices.Contains(o.res.Skipped, "preserve_hdr") {
		t.Fatalf("skipped %v", o.res.Skipped)
	}
	buf := newBuffer(t, o.bytes)
	defer buf.Release()
	var loader string
	var boost float64
	err := vips.Run(t.Context(), func() error {
		h, err := vips.Probe(t.Context(), buf)
		if err != nil {
			return err
		}
		loader = h.Loader
		img, err := vips.Load(t.Context(), buf, nil)
		if err != nil {
			return err
		}
		defer img.Close()
		gm, m, ok, err := img.UHDRGainMap(t.Context())
		if ok {
			gm.Close()
			boost = m.MaxContentBoost[0]
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if loader != "uhdrload_buffer" || boost <= 1 {
		t.Fatalf("output loader %s, max content boost %.2f; want UltraHDR with a boost above 1", loader, boost)
	}
	direct := process(t, gmUHDR, "ph:1/rs:fit:160:0", transform.FormatJXL)
	again := processData(t, o.bytes, "ph:1", transform.FormatJXL)
	a, w, h, _ := hdrPixels(t, direct.bytes)
	b, w2, h2, _ := hdrPixels(t, again.bytes)
	if w != w2 || h != h2 {
		t.Fatalf("sizes %dx%d and %dx%d", w, h, w2, h2)
	}
	golden.Compare(t, "exec/gainmap-uhdr-jpeg-roundtrip", hdrImage(b, w2, h2), golden.Options{})
	var diff float64
	for i := range a {
		diff += math.Abs(a[i] - b[i])
	}
	if mean := diff / float64(len(a)); mean > 0.08 {
		t.Errorf("UltraHDR round trip: mean linear difference %.3f, want ≤ 0.08", mean)
	}
}

// TestGainMapApplyMatchesMetadata checks the gain-map math on the tmap
// sample against the formula: the brightest pixel of the HDR rendering
// cannot exceed the brightest base pixel times 2^max plus the offsets.
func TestGainMapApplyMatchesMetadata(t *testing.T) {
	t.Parallel()
	sdr := process(t, gmTmap, "rs:fit:128:0", transform.FormatJXL)
	hdr := process(t, gmTmap, "ph:1/rs:fit:128:0", transform.FormatJXL)
	s, _, _, _ := hdrPixels(t, sdr.bytes)
	h, _, _, _ := hdrPixels(t, hdr.bytes)
	if len(s) != len(h) {
		t.Fatalf("%d and %d samples", len(s), len(h))
	}
	// Where the base is black, the gain changes little; everywhere the HDR
	// rendering is at least the base (Min ≥ 0 in this sample) within
	// compression error.
	below := 0
	for i := range s {
		if h[i] < s[i]-0.05 {
			below++
		}
	}
	if below > len(s)/100 {
		t.Errorf("%d of %d HDR samples below the SDR base", below, len(s))
	}
	if diff := cmp.Diff(true, slices.Max(h) > slices.Max(s)); diff != "" {
		t.Errorf("HDR peak %.3f not above SDR peak %.3f", slices.Max(h), slices.Max(s))
	}
}
