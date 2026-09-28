//go:build cgo

package exec_test

import (
	"bytes"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/exec"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
)

const (
	// Both carry the landscape's 96×64 preview; their mosaic cannot be
	// decoded: JPEG XL raw data (a placeholder decoder without the DNG SDK)
	// and deflate data that is not deflate (LibRaw fails when unpacking).
	dngUndecodable = "dng/jxl-undecodable.dng"
	dngCorrupt     = "dng/deflate-corrupt.dng"
)

// TestRawPreviewOnly: a RAW LibRaw cannot demosaic is served from its
// embedded preview whatever the request (WP-101): the preview is the
// image, so no request is larger than it and none demosaics.
func TestRawPreviewOnly(t *testing.T) {
	t.Parallel()
	demosaicAlways := testLimits
	demosaicAlways.RawPreviewMinRatio = 100
	for _, tc := range []struct {
		name, fixture, options string
		w, h                   int
	}{
		{"undecodable, preview size", dngUndecodable, "rs:fit:96:96", 96, 64},
		{"undecodable, larger than the preview", dngUndecodable, "rs:fit:400:400", 96, 64},
		{"undecodable, no resize", dngUndecodable, "", 96, 64},
		{"undecodable, trimmed", dngUndecodable, "t:10", 96, 64},
		// The header reads, the unpack fails: planned as a demosaic, then
		// planned again on the preview.
		{"unpack fails, larger than the preview", dngCorrupt, "rs:fit:160:160", 96, 64},
		{"unpack fails, preview size", dngCorrupt, "rs:fit:96:96", 96, 64},
		{"truncated mosaic", dngLandscape, "rs:fit:160:160", 96, 64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data := readFixture(t, tc.fixture)
			if tc.fixture == dngLandscape {
				data = data[:len(data)-30_000]
			}
			o, err := runRaw(t, data, tc.options, demosaicAlways)
			if err != nil {
				t.Fatal(err)
			}
			if o.res.RawPath != exec.RawPathPreview || o.res.Width > tc.w || o.res.Height > tc.h {
				t.Errorf("path %q, %dx%d; want preview, at most %dx%d", o.res.RawPath, o.res.Width, o.res.Height, tc.w, tc.h)
			}
			if o.res.Width == 96 {
				golden.Compare(t, "exec/raw-dng-preview", o.img, golden.Options{})
			}
		})
	}
	// Shrink-on-load still applies to the preview.
	o, err := runRaw(t, readFixture(t, dngUndecodable), "rs:fit:40:40", testLimits)
	if err != nil {
		t.Fatal(err)
	}
	if o.res.RawPath != exec.RawPathPreview || o.res.Width != 40 {
		t.Errorf("40 px: path %q, width %d", o.res.RawPath, o.res.Width)
	}
	golden.Compare(t, "exec/raw-dng-preview-shrink", o.img, golden.Options{})
}

// TestRawPreviewFallbackNeedsPreview: without a usable preview, a RAW
// LibRaw cannot unpack stays a 422 (corrupt).
func TestRawPreviewFallbackNeedsPreview(t *testing.T) {
	t.Parallel()
	data := readFixture(t, dngCorrupt)
	// Break the preview's JPEG frame header, keeping the file's structure.
	i := bytes.Index(data[8:], []byte{0xFF, 0xD8, 0xFF}) + 8
	for j := i + 2; j < i+400 && j < len(data); j++ {
		data[j] = 0
	}
	_, err := runRaw(t, data, "rs:fit:160:160", testLimits)
	if k := errs.KindOf(err); k != errs.KindCorrupt {
		t.Errorf("kind = %v (%v), want corrupt", k, err)
	}
}
