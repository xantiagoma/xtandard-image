//go:build cgo

package vips_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

func TestProbeFixtures(t *testing.T) {
	t.Parallel()

	delays := []int{100, 100, 100, 100, 100, 100}
	tests := []struct {
		fixture string
		want    vips.Header
	}{
		{"jpeg/baseline.jpg", vips.Header{Loader: "jpegload_buffer", Format: vips.FormatJPEG, Width: 320, Height: 240, Bands: 3, Pages: 1, Orientation: 1, BitDepth: 8, Loop: -1}},
		{"jpeg/progressive.jpg", vips.Header{Loader: "jpegload_buffer", Format: vips.FormatJPEG, Width: 320, Height: 240, Bands: 3, Pages: 1, Orientation: 1, BitDepth: 8, Loop: -1}},
		{"jpeg/exif-orientation-6-icc.jpg", vips.Header{Loader: "jpegload_buffer", Format: vips.FormatJPEG, Width: 240, Height: 320, Bands: 3, Pages: 1, Orientation: 6, HasICC: true, BitDepth: 8, Loop: -1}},
		{"uhdr/photo-ultrahdr.jpg", vips.Header{Loader: "uhdrload_buffer", Format: vips.FormatJPEG, Width: 320, Height: 240, Bands: 3, Pages: 1, Orientation: 1, HasICC: true, BitDepth: 8, Loop: -1}},
		{"png/rgba8.png", vips.Header{Loader: "pngload_buffer", Format: vips.FormatPNG, Width: 128, Height: 96, Bands: 4, Pages: 1, Orientation: 1, HasAlpha: true, BitDepth: 8, Loop: -1}},
		{"png/rgb16.png", vips.Header{Loader: "pngload_buffer", Format: vips.FormatPNG, Width: 96, Height: 72, Bands: 3, Pages: 1, Orientation: 1, BitDepth: 16, Loop: -1}},
		{"png/palette.png", vips.Header{Loader: "pngload_buffer", Format: vips.FormatPNG, Width: 96, Height: 72, Bands: 3, Pages: 1, Orientation: 1, BitDepth: 8, Loop: -1}},
		{"gif/static.gif", vips.Header{Loader: "gifload_buffer", Format: vips.FormatGIF, Width: 96, Height: 72, Bands: 3, Pages: 1, Orientation: 1, BitDepth: 8, Delay: []int{100}, Loop: 1}},
		{"gif/animated.gif", vips.Header{Loader: "gifload_buffer", Format: vips.FormatGIF, Width: 96, Height: 72, Bands: 3, Pages: 6, Orientation: 1, BitDepth: 8, IsAnimated: true, Delay: delays, Loop: 0}},
		{"webp/lossy.webp", vips.Header{Loader: "webpload_buffer", Format: vips.FormatWebP, Width: 320, Height: 240, Bands: 3, Pages: 1, Orientation: 1, BitDepth: 8, Loop: -1}},
		{"webp/lossless.webp", vips.Header{Loader: "webpload_buffer", Format: vips.FormatWebP, Width: 96, Height: 72, Bands: 3, Pages: 1, Orientation: 1, BitDepth: 8, Loop: -1}},
		{"webp/animated.webp", vips.Header{Loader: "webpload_buffer", Format: vips.FormatWebP, Width: 96, Height: 72, Bands: 4, Pages: 6, Orientation: 1, HasAlpha: true, BitDepth: 8, IsAnimated: true, Delay: delays, Loop: 0}},
		{"avif/photo.avif", vips.Header{Loader: "heifload_buffer", Format: vips.FormatAVIF, Subformat: "av1", Width: 320, Height: 240, Bands: 3, Pages: 1, Orientation: 1, BitDepth: 8, Loop: -1}},
		{"heic/photo.heic", vips.Header{Loader: "heifload_buffer", Format: vips.FormatHEIF, Subformat: "hevc", Width: 320, Height: 240, Bands: 3, Pages: 1, Orientation: 1, BitDepth: 8, Loop: -1}},
		{"jxl/photo.jxl", vips.Header{Loader: "jxlload_buffer", Format: vips.FormatJXL, Width: 320, Height: 240, Bands: 3, Pages: 1, Orientation: 1, HasICC: true, BitDepth: 8, Loop: -1}},
		{"tiff/multipage.tif", vips.Header{Loader: "tiffload_buffer", Format: vips.FormatTIFF, Width: 160, Height: 120, Bands: 3, Pages: 2, Orientation: 1, BitDepth: 8, Loop: -1}},
		{"jp2/photo.jp2", vips.Header{Loader: "jp2kload_buffer", Format: vips.FormatJP2K, Width: 160, Height: 120, Bands: 3, Pages: 1, Orientation: 1, BitDepth: 8, Loop: -1}},
		{"svg/logo.svg", vips.Header{Loader: "svgload_buffer", Format: vips.FormatSVG, Width: 96, Height: 72, Bands: 4, Pages: 1, Orientation: 1, HasAlpha: true, BitDepth: 8, Loop: -1}},
	}
	// Fields that describe libvips' guess rather than the file are checked
	// loosely.
	ignore := cmpopts.IgnoreFields(vips.Header{}, "BandFormat", "Interpretation", "PageHeight")
	for _, tc := range tests {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			got := probeBytes(t, readFixture(t, tc.fixture))
			if diff := cmp.Diff(tc.want, got, ignore, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Probe mismatch (-want +got):\n%s", diff)
			}
			if got.PageHeight != got.Height {
				t.Errorf("PageHeight = %d, want Height %d", got.PageHeight, got.Height)
			}
			if got.Pixels() != int64(got.Width*got.Height*got.Pages) {
				t.Errorf("Pixels() = %d", got.Pixels())
			}
		})
	}
}

func TestProbeErrors(t *testing.T) {
	t.Parallel()

	baseline := readFixture(t, "jpeg/baseline.jpg")
	tests := []struct {
		name string
		data []byte
		want errs.Kind
	}{
		{"bmp is not a libvips format here", readFixture(t, "bmp/rgb24.bmp"), errs.KindUnsupported},
		{"garbage", []byte("definitely not an image"), errs.KindUnsupported},
		{"jpeg header only", baseline[:24], errs.KindCorrupt},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			buf := newBuffer(t, tc.data)
			defer buf.Release()
			err := tryJob(t.Context(), func(ctx context.Context, _ *vips.Arena) error {
				_, err := vips.Probe(ctx, buf)
				return err
			})
			if got := errs.KindOf(err); got != tc.want {
				t.Errorf("Probe error kind = %v (%v), want %v", got, err, tc.want)
			}
		})
	}
}

// TestProbeDoesNotDecode proves that Probe reads only the header: a JPEG
// whose entropy-coded data is cut off probes fine (decoding it with
// fail_on=warning fails, see TestErrorAttribution), and libvips' tracked
// memory, allocation count and live objects stay at their baseline across
// many probes of every fixture. Not parallel: the counters are
// process-wide.
func TestProbeDoesNotDecode(t *testing.T) {
	jpeg := readFixture(t, "jpeg/baseline.jpg")
	truncated := newBuffer(t, jpeg[:len(jpeg)/2])
	defer truncated.Release()
	job(t, func(ctx context.Context, _ *vips.Arena) error {
		h, err := vips.Probe(ctx, truncated)
		if err != nil {
			return err
		}
		if h.Width != 320 || h.Height != 240 {
			t.Errorf("truncated probe = %dx%d, want 320x240", h.Width, h.Height)
		}
		return nil
	})

	fixtures := []string{
		"jpeg/baseline.jpg", "png/rgba8.png", "gif/animated.gif", "webp/animated.webp",
		"avif/photo.avif", "heic/photo.heic", "jxl/photo.jxl", "tiff/multipage.tif",
		"jp2/photo.jp2", "svg/logo.svg", "uhdr/photo-ultrahdr.jpg",
	}
	bufs := make([]*vips.Buffer, len(fixtures))
	for i, f := range fixtures {
		bufs[i] = newBuffer(t, readFixture(t, f))
	}
	defer func() {
		for _, b := range bufs {
			b.Release()
		}
	}()
	probeAll := func() {
		job(t, func(ctx context.Context, _ *vips.Arena) error {
			for _, b := range bufs {
				if _, err := vips.Probe(ctx, b); err != nil {
					return err
				}
			}
			return nil
		})
	}
	probeAll() // warm up lazily created libvips state (types, profiles)

	before, objects := vips.ReadMemStats(), vips.LiveObjects()
	for range 50 {
		probeAll()
	}
	after := vips.ReadMemStats()
	// A decoded 320×240 RGB frame alone is 230 KB of tracked memory.
	const slack = 64 << 10
	if after.Mem-before.Mem > slack || after.Allocs != before.Allocs {
		t.Errorf("tracked memory moved from %d B / %d allocs to %d B / %d allocs over 550 probes",
			before.Mem, before.Allocs, after.Mem, after.Allocs)
	}
	if got := vips.LiveObjects(); got != objects {
		t.Errorf("live objects %d after probing, want %d", got, objects)
	}
}
