//go:build cgo

package vips_test

import (
	"context"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/testutil/ssim"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// TestRawLibrary: the base image's LibRaw is the WP-103 build, a snapshot
// with X3F tools in every flavour, plus the Adobe DNG SDK and the GoPro GPR
// SDK in the gpl flavour only (owner decision 2026-09-28; the gpl images are
// the ones with share/licenses/LICENSE-GPL.md). Elsewhere (a system LibRaw)
// the build is only logged.
func TestRawLibrary(t *testing.T) {
	t.Parallel()
	l := vips.RawLibraryInfo()
	t.Logf("LibRaw %s: %d cameras, X3F %v, DNG SDK %v, GPR SDK %v", l.Version, l.Cameras, l.X3F, l.DNGSDK, l.GPRSDK)
	if l.Version == "" || l.Cameras < 1000 {
		t.Errorf("LibRaw %q with %d cameras", l.Version, l.Cameras)
	}
	if os.Getenv("XI_IN_BASE_CONTAINER") != "true" {
		return
	}
	_, err := os.Stat("/opt/xtandard/share/licenses/LICENSE-GPL.md")
	gpl := err == nil
	// 0.22.2 listed 1283 cameras with every option; the snapshot adds the
	// Canon EOS R50 V, Hasselblad X2D II 100C, Sony A1 II and Sony A7 V.
	// The nine GoPro bodies count only with the GPR SDK (gpl).
	cameras := 1278
	if gpl {
		cameras = 1287
	}
	if !strings.HasPrefix(l.Version, "0.22") || l.Cameras < cameras || !l.X3F {
		t.Errorf("base image LibRaw = %+v, want a 0.22 snapshot with >= %d cameras and X3F", l, cameras)
	}
	if l.DNGSDK != gpl || l.GPRSDK != gpl {
		t.Errorf("base image LibRaw: DNG SDK %v, GPR SDK %v; want both %v (gpl flavour %v)", l.DNGSDK, l.GPRSDK, gpl, gpl)
	}
}

// rawRGBA demosaics data at full size into an NRGBA image.
func rawRGBA(tb testing.TB, data []byte) *image.NRGBA {
	tb.Helper()
	buf := newBuffer(tb, data)
	defer buf.Release()
	var out *image.NRGBA
	job(tb, func(ctx context.Context, a *vips.Arena) error {
		img, err := a.Add(vips.RawLoad(ctx, buf, nil))
		if err != nil {
			return err
		}
		out, err = rgbaOf(ctx, a, img)
		return err
	})
	return out
}

// TestRawLoadJXLDNG decodes a DNG 1.7 whose raw mosaic is lossless JPEG
// XL (jxl-landscape.dng: landscape.dng rewritten by Adobe's dng_validate,
// testdata/gen/jxl-dng.sh) through LibRaw's Adobe DNG SDK path: same size
// and the same pixels as the uncompressed original.
func TestRawLoadJXLDNG(t *testing.T) {
	t.Parallel()
	jxl := readFixture(t, "dng/jxl-landscape.dng")
	if !vips.RawLibraryInfo().DNGSDK {
		h, err := probeRaw(t, jxl)
		if err != nil || h.Undecodable != vips.RawNoDecoder {
			t.Errorf("without the DNG SDK: undecodable %v (%v), want %v", h.Undecodable, err, vips.RawNoDecoder)
		}
		t.Skip("LibRaw without the Adobe DNG SDK")
	}
	h, err := probeRaw(t, jxl)
	if err != nil {
		t.Fatal(err)
	}
	if h.Undecodable != vips.RawDecodable || !h.DNG || h.Width != 192 || h.Height != 128 {
		t.Fatalf("probe: undecodable %v, dng %v, %dx%d; want decodable 192x128 DNG", h.Undecodable, h.DNG, h.Width, h.Height)
	}
	got := rawRGBA(t, jxl)
	ref := rawRGBA(t, readFixture(t, "dng/landscape.dng"))
	if got.Rect != ref.Rect {
		t.Fatalf("size %v, want %v", got.Rect, ref.Rect)
	}
	r, err := ssim.Compare(ref, got)
	if err != nil {
		t.Fatal(err)
	}
	if r.DSSIM > 0.002 {
		t.Errorf("DSSIM against the uncompressed DNG = %.5f, want <= 0.002 (lossless)", r.DSSIM)
	}
}

// TestRawLoadGPR decodes a GoPro GPR (VC-5 compressed DNG; hero11-crop.gpr,
// testdata/gen/gpr.sh) through the GPR SDK's VC-5 decoder.
func TestRawLoadGPR(t *testing.T) {
	t.Parallel()
	gpr := readFixture(t, "gpr/hero11-crop.gpr")
	h, err := probeRaw(t, gpr)
	if err != nil {
		t.Fatal(err)
	}
	if h.Make+" "+h.Model != "GoPro HERO11 Black" || !h.DNG || h.Width != 256 || h.Height != 192 {
		t.Errorf("probe: %s %s, dng %v, %dx%d; want GoPro HERO11 Black, DNG, 256x192", h.Make, h.Model, h.DNG, h.Width, h.Height)
	}
	if !vips.RawLibraryInfo().GPRSDK {
		if h.Undecodable != vips.RawNoDecoder {
			t.Errorf("without the GPR SDK: undecodable %v, want %v", h.Undecodable, vips.RawNoDecoder)
		}
		t.Skip("LibRaw without the GoPro GPR SDK")
	}
	if h.Undecodable != vips.RawDecodable {
		t.Fatalf("undecodable %v", h.Undecodable)
	}
	img := rawRGBA(t, gpr)
	if img.Rect.Dx() != 256 || img.Rect.Dy() != 192 {
		t.Fatalf("size %v, want 256x192", img.Rect)
	}
	// A real photo, not a flat or black frame: the VC-5 data was decoded.
	var lo, hi uint8 = 255, 0
	for i := 0; i < len(img.Pix); i += 4 {
		lo, hi = min(lo, img.Pix[i+1]), max(hi, img.Pix[i+1])
	}
	if hi-lo < 64 {
		t.Errorf("green channel spans %d..%d: not a decoded photo", lo, hi)
	}
}

// FuzzRawLoad feeds arbitrary bytes to the RAW probe and, when the header
// reads, to the demosaic (half size, 1 MP limit): LibRaw with X3F tools, the
// DNG SDK (JPEG XL) and the GPR SDK (VC-5) must fail cleanly, never crash,
// and never leave a LibRaw handle or buffer behind. The DNG seeds reach the
// TIFF/DNG parsers, the JPEG XL one the DNG SDK, the GPR one the VC-5
// decoder.
func FuzzRawLoad(f *testing.F) {
	for _, n := range []string{"dng/landscape.dng", "dng/jxl-landscape.dng", "dng/deflate-corrupt.dng", "gpr/hero11-crop.gpr"} {
		f.Add(readFixture(f, n))
	}
	// libFuzzer finds in GoPro's VC-5 decoder, fixed by
	// docker/base/patches/gpr/0002 (mutations of hero11-crop.gpr).
	dir := filepath.Join("testdata", "rawfuzz")
	regressions, err := os.ReadDir(dir)
	if err != nil {
		f.Fatal(err)
	}
	for _, e := range regressions {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte("FOVb\x00\x00\x00\x00"))
	f.Fuzz(func(t *testing.T, data []byte) {
		buf := newBuffer(t, data)
		defer buf.Release()
		err := tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
			if _, err := vips.ProbeRaw(ctx, buf); err != nil {
				return err
			}
			img, err := a.Add(vips.RawLoad(ctx, buf, &vips.RawOptions{HalfSize: true, MaxPixels: 1 << 20}))
			if err != nil {
				return err
			}
			_, err = img.WriteToMemory(ctx)
			return err
		})
		switch errs.KindOf(err) {
		case errs.KindCorrupt, errs.KindUnsupported, errs.KindLimitExceeded:
		default:
			if err != nil {
				t.Fatalf("unexpected error kind: %v", err)
			}
		}
		if n := vips.RawLive(); n < 0 {
			t.Fatalf("live counter %d", n)
		}
	})
}
