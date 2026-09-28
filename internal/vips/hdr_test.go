//go:build cgo

package vips_test

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/testutil/leakcheck"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// hdrFixtures are the WP-67 inputs: OpenEXR (spooled for openexrload),
// Radiance under both magic lines, PFM and an 8-bit PPM.
type hdrFixture struct {
	name, loader string
	format       vips.Format
	w, h, bands  int
	bf           vips.BandFormat
	max          float64 // brightest sample after Decode
}

func hdrFixtures() []hdrFixture {
	return []hdrFixture{
		{"exr/rgba-half.exr", "openexrload", vips.FormatEXR, 80, 60, 4, vips.BandFormatFloat, 40},
		{"exr/rgb-float.exr", "openexrload", vips.FormatEXR, 48, 32, 4, vips.BandFormatFloat, 40},
		{"hdr/scene.hdr", "radload_buffer", vips.FormatRadiance, 96, 64, 4, vips.BandFormatUchar, 40},
		{"hdr/rgbe.hdr", "radload_buffer", vips.FormatRadiance, 48, 32, 4, vips.BandFormatUchar, 40},
		{"pfm/scene.pfm", "ppmload_buffer", vips.FormatPPM, 64, 48, 3, vips.BandFormatFloat, 40},
		{"ppm/photo.ppm", "ppmload_buffer", vips.FormatPPM, 96, 64, 3, vips.BandFormatUchar, 255},
	}
}

func TestProbeHDR(t *testing.T) {
	t.Parallel()
	for _, tc := range hdrFixtures() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := probeBytes(t, readFixture(t, tc.name))
			if h.Loader != tc.loader || h.Format != tc.format || h.Width != tc.w || h.Height != tc.h ||
				h.Bands != tc.bands || h.BandFormat != tc.bf || h.Pages != 1 {
				t.Errorf("header = %s %s %dx%d×%d %v, %d pages; want %s %s %dx%d×%d %v",
					h.Loader, h.Format, h.Width, h.Height, h.Bands, h.BandFormat, h.Pages,
					tc.loader, tc.format, tc.w, tc.h, tc.bands, tc.bf)
			}
		})
	}
}

// TestLoadHDR decodes every pixel: the sun of the procedural scene is 40
// in linear light (Radiance rounds it to its 8-bit mantissa).
func TestLoadHDR(t *testing.T) {
	t.Parallel()
	for _, tc := range hdrFixtures() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			buf := newBuffer(t, readFixture(t, tc.name))
			defer buf.Release()
			job(t, func(ctx context.Context, a *vips.Arena) error {
				img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Access: vips.AccessRandom}))
				if err != nil {
					return err
				}
				if img, err = a.Add(img.Decode(ctx)); err != nil {
					return err
				}
				if img, err = a.Add(img.Cast(ctx, vips.BandFormatFloat)); err != nil {
					return err
				}
				pix, err := img.WriteToMemory(ctx)
				if err != nil {
					return err
				}
				var top float64
				for i := 0; i+4 <= len(pix); i += 4 {
					top = max(top, float64(math.Float32frombits(binary.NativeEndian.Uint32(pix[i:]))))
				}
				if math.Abs(top-tc.max) > tc.max/64 {
					t.Errorf("brightest sample %g, want %g", top, tc.max)
				}
				return nil
			})
		})
	}
}

// TestHDRRefusals checks the loader restrictions: OpenEXR through the
// thumbnail path is unsupported, a truncated OpenEXR fails as corrupt when
// its pixels are read (the spool maps the failure back to the source), and a
// truncated header at probe.
func TestHDRRefusals(t *testing.T) {
	t.Parallel()
	exr := readFixture(t, "exr/rgba-half.exr")
	t.Run("exr thumbnail", func(t *testing.T) {
		t.Parallel()
		buf := newBuffer(t, exr)
		defer buf.Release()
		if _, err := thumbnailJPEG(t.Context(), buf, 32, 32); errs.KindOf(err) != errs.KindUnsupported {
			t.Errorf("thumbnail: %v, want unsupported", err)
		}
	})
	t.Run("exr truncated pixels", func(t *testing.T) {
		t.Parallel()
		buf := newBuffer(t, exr[:len(exr)/2])
		defer buf.Release()
		err := tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
			img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Access: vips.AccessRandom}))
			if err != nil {
				return err
			}
			_, err = img.WriteToMemory(ctx)
			return err
		})
		if errs.KindOf(err) != errs.KindCorrupt {
			t.Errorf("load: %v (kind %s), want corrupt", err, errs.KindOf(err))
		}
	})
	t.Run("exr truncated header", func(t *testing.T) {
		t.Parallel()
		buf := newBuffer(t, exr[:40])
		defer buf.Release()
		err := tryJob(t.Context(), func(ctx context.Context, _ *vips.Arena) error {
			_, err := vips.Probe(ctx, buf)
			return err
		})
		if errs.KindOf(err) != errs.KindCorrupt {
			t.Errorf("probe: %v (kind %s), want corrupt", err, errs.KindOf(err))
		}
	})
}

// openFDs counts this process's open file descriptors (Linux), or -1.
func openFDs() int {
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return -1
	}
	return len(ents)
}

// TestNoLeakHDR is TestNoLeak for the HDR loaders: OpenEXR (through its
// memfd spool, whose descriptor must close with the image), Radiance and
// PFM, decoded and encoded to JPEG. Not parallel: the counters are
// process-wide.
func TestNoLeakHDR(t *testing.T) {
	const cycles = 600
	vips.SetLeakCheck(true)
	defer vips.SetLeakCheck(false)

	inputs := [][]byte{
		readFixture(t, "exr/rgba-half.exr"), readFixture(t, "hdr/scene.hdr"), readFixture(t, "pfm/scene.pfm"),
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
			if img, err = a.Add(img.Decode(ctx)); err != nil {
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
	for i := range 6 {
		cycle(i)
	}
	before, fds := leakcheck.Take(), openFDs()
	for i := range cycles {
		cycle(i)
	}
	after := leakcheck.Check(t, before, leakcheck.Default, "cycles")
	if fdsAfter := openFDs(); fdsAfter != fds {
		t.Errorf("after %d cycles: fds %d → %d", cycles, fds, fdsAfter)
	}
	t.Logf("after %d cycles: mem %+d B, allocs %+d, objects %+d",
		cycles, after.Mem-before.Mem, after.Allocs-before.Allocs, after.Objects-before.Objects)
}
