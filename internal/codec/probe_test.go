//go:build cgo

package codec_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// These tests need libvips from the base image: run them with
// `task docker:test`.
func TestMain(m *testing.M) {
	if err := vips.Init(new(vips.DefaultConfig())); err != nil {
		_, _ = os.Stderr.WriteString("vips init: " + err.Error() + "\n")
		os.Exit(1)
	}
	code := m.Run()
	vips.Shutdown()
	os.Exit(code)
}

func newBuffer(tb testing.TB, data []byte) *vips.Buffer {
	tb.Helper()
	buf, err := vips.NewBufferFromReader(bytes.NewReader(data), 1<<30)
	if err != nil {
		tb.Fatalf("NewBufferFromReader: %v", err)
	}
	return buf
}

// probe copies data into a Buffer and probes it inside vips.Run.
func probe(ctx context.Context, tb testing.TB, data []byte, lim codec.Limits, contentType string) (codec.ImageMeta, error) {
	tb.Helper()
	buf := newBuffer(tb, data)
	defer buf.Release()
	var m codec.ImageMeta
	err := vips.Run(ctx, func() error {
		var err error
		m, err = codec.Probe(ctx, buf, lim, contentType)
		return err
	})
	return m, err
}

func TestProbeFixtures(t *testing.T) {
	t.Parallel()

	still := func(f codec.Format, w, h int) codec.ImageMeta {
		return codec.ImageMeta{
			Format: f, Width: w, Height: h, Frames: 1, Pages: 1, PageHeight: h,
			Orientation: 1, BitDepth: 8, Loop: -1, ColorSpace: codec.ColorSpaceSRGB,
		}
	}
	with := func(m codec.ImageMeta, edit func(*codec.ImageMeta)) codec.ImageMeta {
		edit(&m)
		return m
	}
	delays := []int{100, 100, 100, 100, 100, 100}
	tests := map[string]codec.ImageMeta{
		"jpeg/baseline.jpg":    still(codec.FormatJPEG, 320, 240),
		"jpeg/progressive.jpg": still(codec.FormatJPEG, 320, 240),
		"jpeg/exif-orientation-6-icc.jpg": with(still(codec.FormatJPEG, 240, 320), func(m *codec.ImageMeta) {
			m.Orientation, m.HasICC = 6, true
		}),
		"jpeg/metadata-p3.jpg": with(still(codec.FormatJPEG, 120, 160), func(m *codec.ImageMeta) {
			m.Orientation, m.HasICC = 6, true
		}),
		"png/bordered.png":          still(codec.FormatPNG, 80, 60),
		"detect/apollo11-crew.jpg":  still(codec.FormatJPEG, 640, 503),
		"detect/astronaut-dogs.jpg": still(codec.FormatJPEG, 640, 512),
		"detect/cat-laptop.jpg":     still(codec.FormatJPEG, 640, 419),
		"dng/landscape.dng":         still(codec.FormatRAW, 192, 128),
		"dng/portrait-orientation-6.dng": with(still(codec.FormatRAW, 192, 128), func(m *codec.ImageMeta) {
			m.Orientation = 6
		}),
		"uhdr/photo-ultrahdr.jpg": with(still(codec.FormatUHDR, 320, 240), func(m *codec.ImageMeta) { m.HasICC = true }),
		"png/rgba8.png":           with(still(codec.FormatPNG, 128, 96), func(m *codec.ImageMeta) { m.HasAlpha = true }),
		"png/rgb16.png":           with(still(codec.FormatPNG, 96, 72), func(m *codec.ImageMeta) { m.BitDepth = 16 }),
		"png/palette.png":         still(codec.FormatPNG, 96, 72),
		"watermark/logo.png":      with(still(codec.FormatPNG, 48, 32), func(m *codec.ImageMeta) { m.HasAlpha = true }),
		"watermark/badge.png":     with(still(codec.FormatPNG, 40, 40), func(m *codec.ImageMeta) { m.HasAlpha = true }),
		"gif/static.gif": with(still(codec.FormatGIF, 96, 72), func(m *codec.ImageMeta) {
			m.Delays, m.Loop = []int{100}, 1
		}),
		"gif/animated.gif": with(still(codec.FormatGIF, 96, 72), func(m *codec.ImageMeta) {
			m.Frames, m.Pages, m.IsAnimated, m.Delays, m.Loop = 6, 6, true, delays, 0
		}),
		"webp/lossy.webp":    still(codec.FormatWebP, 320, 240),
		"webp/lossless.webp": still(codec.FormatWebP, 96, 72),
		"webp/animated.webp": with(still(codec.FormatWebP, 96, 72), func(m *codec.ImageMeta) {
			m.Frames, m.Pages, m.IsAnimated, m.Delays, m.Loop, m.HasAlpha = 6, 6, true, delays, 0, true
		}),
		"avif/photo.avif": still(codec.FormatAVIF, 320, 240),
		"heic/photo.heic": still(codec.FormatHEIC, 320, 240),
		// The embedded 80x60 thumbnails are not pages.
		"avif/thumbnail.avif": still(codec.FormatAVIF, 320, 240),
		"heic/thumbnail.heic": still(codec.FormatHEIC, 320, 240),
		"jxl/photo.jxl":       with(still(codec.FormatJXL, 320, 240), func(m *codec.ImageMeta) { m.HasICC = true }),
		"tiff/multipage.tif":  with(still(codec.FormatTIFF, 160, 120), func(m *codec.ImageMeta) { m.Pages = 2 }),
		"jp2/photo.jp2":       still(codec.FormatJP2, 160, 120),
		"svg/logo.svg":        with(still(codec.FormatSVG, 96, 72), func(m *codec.ImageMeta) { m.HasAlpha = true }),
		"bmp/rgb24.bmp":       still(codec.FormatBMP, 96, 72),
		"bmp/rle8.bmp":        still(codec.FormatBMP, 96, 72),
		"ico/png-entry.ico":   with(still(codec.FormatICO, 64, 64), func(m *codec.ImageMeta) { m.HasAlpha = true }),
		"ico/bmp-entry.ico":   with(still(codec.FormatICO, 32, 32), func(m *codec.ImageMeta) { m.HasAlpha = true }),
		// PDF pages render opaque (white) unless a request asks otherwise.
		"pdf/pages.pdf":        with(still(codec.FormatPDF, 200, 150), func(m *codec.ImageMeta) { m.Pages = 3 }),
		"psd/layered.psd":      with(still(codec.FormatPSD, 96, 72), func(m *codec.ImageMeta) { m.Pages = 3 }),
		"psd/psb-layered.psb":  with(still(codec.FormatPSD, 96, 72), func(m *codec.ImageMeta) { m.Pages = 3 }),
		"psd/no-composite.psd": with(still(codec.FormatPSD, 96, 72), func(m *codec.ImageMeta) { m.Pages = 3 }),
		// Scene-linear sources: HDR, no ICC; OpenEXR from the Go header.
		"exr/rgba-half.exr": with(still(codec.FormatEXR, 80, 60), func(m *codec.ImageMeta) {
			m.HasAlpha, m.BitDepth, m.ColorSpace, m.HDR = true, 16, codec.ColorSpaceScRGB, true
		}),
		"exr/rgb-float.exr": with(still(codec.FormatEXR, 48, 32), func(m *codec.ImageMeta) {
			m.BitDepth, m.ColorSpace, m.HDR = 32, codec.ColorSpaceScRGB, true
		}),
		"hdr/scene.hdr": with(still(codec.FormatRadiance, 96, 64), func(m *codec.ImageMeta) {
			m.BitDepth, m.ColorSpace, m.HDR = 32, codec.ColorSpaceScRGB, true
		}),
		"hdr/rgbe.hdr": with(still(codec.FormatRadiance, 48, 32), func(m *codec.ImageMeta) {
			m.BitDepth, m.ColorSpace, m.HDR = 32, codec.ColorSpaceScRGB, true
		}),
		"pfm/scene.pfm": with(still(codec.FormatPFM, 64, 48), func(m *codec.ImageMeta) {
			m.BitDepth, m.ColorSpace, m.HDR = 32, codec.ColorSpaceScRGB, true
		}),
		"ppm/photo.ppm": still(codec.FormatPPM, 96, 64),
	}
	// password.pdf fails by design (TestProbeErrors); videos are probed by
	// TestProbeVideo.
	videos := 0
	for _, f := range fixtureFormats() {
		if f.IsVideo() {
			videos++
		}
	}
	if len(tests) != len(fixtureFormats())-1-videos {
		t.Fatalf("probe table has %d fixtures, sniff table %d", len(tests), len(fixtureFormats()))
	}
	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := probe(t.Context(), t, readFixture(t, name), codec.Limits{}, want.Format.MIME())
			if err != nil {
				t.Fatalf("Probe: %v", err)
			}
			if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Probe mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestProbeContentTypeMismatch(t *testing.T) {
	t.Parallel()

	data := readFixture(t, "png/rgba8.png")
	for ct, want := range map[string]bool{"": false, "image/png": false, "image/jpeg": true, "text/html": true} {
		m, err := probe(t.Context(), t, data, codec.Limits{}, ct)
		if err != nil {
			t.Fatal(err)
		}
		if m.Format != codec.FormatPNG || m.ContentTypeMismatch != want {
			t.Errorf("Content-Type %q: format %v, mismatch %v; want png, %v", ct, m.Format, m.ContentTypeMismatch, want)
		}
	}
}

func TestProbeErrors(t *testing.T) {
	t.Parallel()

	jpeg := readFixture(t, "jpeg/baseline.jpg")
	png := readFixture(t, "png/rgba8.png")
	bmp := readFixture(t, "bmp/rgb24.bmp")
	svg := readFixture(t, "svg/logo.svg")
	tests := []struct {
		name string
		data []byte
		lim  codec.Limits
		want errs.Kind
	}{
		{"empty", nil, codec.Limits{}, errs.KindUnsupported},
		{"garbage", []byte("definitely not an image"), codec.Limits{}, errs.KindUnsupported},
		{"truncated pdf", []byte("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n"), codec.Limits{}, errs.KindCorrupt},
		{"truncated psd", []byte("8BPS\x00\x01\x00\x00\x00\x00\x00\x00\x00\x03"), codec.Limits{}, errs.KindCorrupt},
		{"pdf with a password", readFixture(t, "pdf/password.pdf"), codec.Limits{}, errs.KindUnsupported},
		{"pdf over page limit", readFixture(t, "pdf/pages.pdf"), codec.Limits{MaxPDFPages: 2}, errs.KindLimitExceeded},
		{"pdf over pixel limit", readFixture(t, "pdf/pages.pdf"), codec.Limits{MaxSrcPixels: 1000}, errs.KindLimitExceeded},
		{"psd without composite refused", readFixture(t, "psd/no-composite.psd"), codec.Limits{PSDRequireComposite: true}, errs.KindUnsupported},
		{"psd over pixel limit", readFixture(t, "psd/layered.psd"), codec.Limits{MaxSrcPixels: 1000}, errs.KindLimitExceeded},
		{"cr2 header only", []byte("II*\x00\x10\x00\x00\x00CR\x02\x00"), codec.Limits{}, errs.KindCorrupt},
		{"dng truncated in its header", readFixture(t, "dng/landscape.dng")[:300], codec.Limits{}, errs.KindCorrupt},
		{"dng over pixel limit", readFixture(t, "dng/landscape.dng"), codec.Limits{MaxSrcPixels: 192*128 - 1}, errs.KindLimitExceeded},
		{"mp4", isobmff("isom", "mp41"), codec.Limits{}, errs.KindUnsupported},
		{"jpeg header only", jpeg[:24], codec.Limits{}, errs.KindCorrupt},
		{"png signature only", png[:8], codec.Limits{}, errs.KindCorrupt},
		{"bmp header only", bmp[:20], codec.Limits{}, errs.KindCorrupt},
		{"ico directory only", readFixture(t, "ico/png-entry.ico")[:22], codec.Limits{}, errs.KindCorrupt},
		{"svg with dtd", []byte(`<?xml version="1.0"?><!DOCTYPE svg [<!ENTITY a "a">]><svg xmlns="http://www.w3.org/2000/svg" width="1" height="1">&a;</svg>`), codec.Limits{}, errs.KindCorrupt},
		{"svg over byte limit", svg, codec.Limits{MaxSVGBytes: 100}, errs.KindLimitExceeded},
		{"svg over element limit", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1">` + strings.Repeat("<g/>", 20) + `</svg>`), codec.Limits{MaxSVGElements: 10}, errs.KindLimitExceeded},
		{"png over pixel limit", png, codec.Limits{MaxSrcPixels: 1000}, errs.KindLimitExceeded},
		{"bmp over pixel limit", bmp, codec.Limits{MaxSrcPixels: 1000}, errs.KindLimitExceeded},
		{"animation over frame limit", readFixture(t, "gif/animated.gif"), codec.Limits{MaxAnimationFrames: 5}, errs.KindLimitExceeded},
		{"animation over frame pixel limit", readFixture(t, "webp/animated.webp"), codec.Limits{MaxAnimationFramePixels: 96*72 - 1}, errs.KindLimitExceeded},
		{"exr over pixel limit", readFixture(t, "exr/rgba-half.exr"), codec.Limits{MaxSrcPixels: 1000}, errs.KindLimitExceeded},
		{"radiance over pixel limit", readFixture(t, "hdr/scene.hdr"), codec.Limits{MaxSrcPixels: 1000}, errs.KindLimitExceeded},
		{"pfm over pixel limit", readFixture(t, "pfm/scene.pfm"), codec.Limits{MaxSrcPixels: 1000}, errs.KindLimitExceeded},
		{"ppm over pixel limit", readFixture(t, "ppm/photo.ppm"), codec.Limits{MaxSrcPixels: 1000}, errs.KindLimitExceeded},
		{"exr truncated header", readFixture(t, "exr/rgba-half.exr")[:30], codec.Limits{}, errs.KindCorrupt},
		{"exr deep", synthEXR(4, 4, 0x800, "R"), codec.Limits{}, errs.KindUnsupported},
		{"exr multi-part", synthEXR(4, 4, 0x1000, "R"), codec.Limits{}, errs.KindUnsupported},
		{"exr without colour", synthEXR(4, 4, 0, "Z"), codec.Limits{}, errs.KindUnsupported},
		{"radiance without resolution", []byte("#?RADIANCE\nFORMAT=32-bit_rle_rgbe\n\n"), codec.Limits{}, errs.KindCorrupt},
		{"ascii ppm", []byte("P3\n1 1\n255\n0 0 0\n"), codec.Limits{}, errs.KindUnsupported},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := probe(t.Context(), t, tc.data, tc.lim, "")
			if got := errs.KindOf(err); got != tc.want {
				t.Errorf("Probe error = %v (kind %v), want kind %v", err, got, tc.want)
			}
		})
	}
}

func TestProbeCanceled(t *testing.T) {
	t.Parallel()

	buf := newBuffer(t, readFixture(t, "bmp/rgb24.bmp"))
	defer buf.Release()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := codec.Probe(ctx, buf, codec.Limits{}, "")
	if got := errs.KindOf(err); got != errs.KindCanceled {
		t.Errorf("Probe error = %v (kind %v), want Canceled", err, got)
	}
}

// TestProbeOversizeDoesNotDecode feeds headers that declare enormous images
// (a 100k × 100k PNG: 30 GB decoded; a GIF with 10k frames of 1000×1000:
// 40 GB; two 5000×4000 frames; a PDF of 20k pages, over the default page
// limit and past the 10k pages libvips' n-pages helper still counts) and
// checks that each is refused with
// LimitExceeded while libvips' tracked memory stays flat: nothing was
// decoded. Not parallel: the counters are process-wide.
func TestProbeOversizeDoesNotDecode(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"png 100k squared", synthPNG(t, 100_000, 100_000)},
		{"gif 10k frames", synthGIF(1000, 1000, 10_000)},
		{"gif frames over frame pixels", synthGIF(5000, 4000, 2)},
		{"pdf of 20k pages", synthPDF(20_000)},
		{"exr 1M squared", synthEXR(1_000_000, 1_000_000, 0, "B", "G", "R")},
		{"radiance 100k squared", []byte("#?RADIANCE\nFORMAT=32-bit_rle_rgbe\n\n-Y 100000 +X 100000\n")},
		{"pfm 100k squared", []byte("PF\n100000 100000\n-1.0\n")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			buf := newBuffer(t, tc.data)
			defer buf.Release()
			run := func() error {
				return vips.Run(t.Context(), func() error {
					_, err := codec.Probe(t.Context(), buf, codec.Limits{}, "")
					return err
				})
			}
			// Warm up lazily created libvips state.
			if err := run(); errs.KindOf(err) != errs.KindLimitExceeded {
				t.Fatalf("Probe error = %v (kind %v), want LimitExceeded", err, errs.KindOf(err))
			}
			before, objects := vips.ReadMemStats(), vips.LiveObjects()
			for range 20 {
				if err := run(); errs.KindOf(err) != errs.KindLimitExceeded {
					t.Fatalf("Probe error = %v, want LimitExceeded", err)
				}
			}
			after := vips.ReadMemStats()
			const slack = 64 << 10
			if after.Mem-before.Mem > slack || after.MemHighwater-before.MemHighwater > slack {
				t.Errorf("tracked memory moved from %d B (high %d) to %d B (high %d)",
					before.Mem, before.MemHighwater, after.Mem, after.MemHighwater)
			}
			if got := vips.LiveObjects(); got != objects {
				t.Errorf("live objects %d after probing, want %d", got, objects)
			}
		})
	}
}

// FuzzProbe checks that Probe never crashes on arbitrary input, fails only
// with the probe error kinds, and that what it accepts is within the limits.
// Run it in the base image (task docker:test with -fuzz).
func FuzzProbe(f *testing.F) {
	for name := range fixtureFormats() {
		f.Add(readFixture(f, name))
	}
	f.Add(synthPNG(f, 100_000, 100_000))
	f.Add(synthGIF(16, 16, 300))
	f.Add(isobmff("mif1", "avif"))
	f.Add(synthPDF(3))
	f.Add(synthEXR(16, 16, 0, "A", "B", "G", "R"))
	lim := codec.Limits{
		MaxSrcPixels: 1 << 20, MaxAnimationFrames: 64, MaxAnimationFramePixels: 1 << 18, MaxSVGBytes: 64 << 10,
		MaxPDFPages: 8,
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := probe(t.Context(), t, data, lim, "")
		if err != nil {
			switch errs.KindOf(err) {
			case errs.KindUnsupported, errs.KindCorrupt, errs.KindLimitExceeded:
				return
			default:
				t.Fatalf("Probe error %v has kind %v", err, errs.KindOf(err))
			}
		}
		if !m.Format.Supported() || m.Width <= 0 || m.Height <= 0 || m.Pixels() > lim.MaxSrcPixels ||
			m.Frames > lim.MaxAnimationFrames || m.Frames < 1 {
			t.Fatalf("Probe accepted %+v", m)
		}
	})
}
