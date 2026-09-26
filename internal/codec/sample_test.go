//go:build cgo

package codec_test

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// sample copies the fixture into a Buffer, probes it for its format and
// samples it inside one vips.Run.
func sample(t *testing.T, name string, maxSide int) codec.Raw {
	t.Helper()
	buf := newBuffer(t, readFixture(t, name))
	defer buf.Release()
	var raw codec.Raw
	err := vips.Run(t.Context(), func() error {
		m, err := codec.Probe(t.Context(), buf, codec.Limits{}, "")
		if err != nil {
			return err
		}
		raw, err = codec.Sample(t.Context(), buf, m.Format, maxSide, codec.Limits{})
		return err
	})
	if err != nil {
		t.Fatalf("Sample(%s): %v", name, err)
	}
	return raw
}

// pixel is the RGBA pixel at x, y.
func pixel(r *codec.Raw, x, y int) [4]byte {
	i := (y*r.Width + x) * 4
	return [4]byte(r.Data[i : i+4])
}

func TestSample(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		maxSide int
		w, h    int
	}{
		{"jpeg/baseline.jpg", 512, 320, 240},
		{"jpeg/baseline.jpg", 100, 100, 75},
		{"jpeg/exif-orientation-6-icc.jpg", 512, 320, 240}, // stored 240x320, upright after orientation
		{"png/rgba8.png", 512, 128, 96},
		{"png/rgb16.png", 512, 96, 72},
		{"png/palette.png", 48, 48, 36},
		{"gif/animated.gif", 512, 96, 72}, // the first frame only
		{"webp/animated.webp", 512, 96, 72},
		{"tiff/multipage.tif", 512, 160, 120}, // the first page
		{"bmp/rgb24.bmp", 512, 96, 72},        // pure-Go loader
		{"bmp/rle8.bmp", 32, 96, 72},          // pure-Go loaders keep their size; the caller fits
		{"ico/png-entry.ico", 512, 64, 64},    // embedded PNG through libvips
		{"svg/logo.svg", 512, 96, 72},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := sample(t, tc.name, tc.maxSide)
			if raw.Width != tc.w || raw.Height != tc.h || raw.Bands != 4 {
				t.Errorf("Sample = %dx%d with %d bands, want %dx%d with 4", raw.Width, raw.Height, raw.Bands, tc.w, tc.h)
			}
			if err := raw.Validate(); err != nil {
				t.Error(err)
			}
		})
	}
}

// TestSampleAlpha: the alpha channel of rgba8.png is kept straight (not
// premultiplied) and opaque sources get alpha 255.
func TestSampleAlpha(t *testing.T) {
	t.Parallel()
	transparent := false
	raw := sample(t, "png/rgba8.png", 512)
	for y := range raw.Height {
		for x := range raw.Width {
			transparent = transparent || pixel(&raw, x, y)[3] < 255
		}
	}
	if !transparent {
		t.Error("rgba8.png sampled without transparency")
	}
	opaque := sample(t, "jpeg/baseline.jpg", 64)
	for y := range opaque.Height {
		for x := range opaque.Width {
			if a := pixel(&opaque, x, y)[3]; a != 255 {
				t.Fatalf("baseline.jpg alpha %d at %d,%d", a, x, y)
			}
		}
	}
}

// TestSampleNoLeak samples every kind of source repeatedly and checks that
// no libvips object survives. Not parallel: the counter is process-wide.
func TestSampleNoLeak(t *testing.T) {
	names := []string{"jpeg/baseline.jpg", "png/rgba8.png", "gif/animated.gif", "bmp/rgb24.bmp", "ico/png-entry.ico", "png/rgb16.png"}
	for _, n := range names {
		sample(t, n, 512) // warm up lazily created libvips state
	}
	objects := vips.LiveObjects()
	for range 5 {
		for _, n := range names {
			sample(t, n, 512)
		}
	}
	if got := vips.LiveObjects(); got != objects {
		t.Errorf("live objects %d after sampling, want %d", got, objects)
	}
}
