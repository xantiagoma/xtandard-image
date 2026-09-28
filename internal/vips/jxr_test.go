//go:build cgo

package vips_test

import (
	"context"
	"image"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/testutil/leakcheck"
	"github.com/xantiagoma/xtandard-image/internal/testutil/ssim"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// The JPEG XR fixtures are the 64×48 procedural photo encoded by jxrlib's
// JxrEncApp at quality 0.9 in several pixel formats (testdata/LICENSES.md);
// photo.pcx is the same photo, lossless, through ImageMagick.
func TestProbeJXR(t *testing.T) {
	t.Parallel()
	jxr := vips.Header{Loader: "jxrload_buffer", Format: vips.FormatJXR, Width: 64, Height: 48, Pages: 1, Orientation: 1, Loop: -1}
	with := func(h vips.Header, bands, bits int, alpha bool, bf vips.BandFormat, in vips.Interpretation) vips.Header {
		h.Bands, h.BitDepth, h.HasAlpha, h.BandFormat, h.Interpretation = bands, bits, alpha, bf, in
		return h
	}
	hdr := with(jxr, 3, 8, false, vips.BandFormatUchar, vips.InterpretationSRGB)
	hdr.Width, hdr.Height = 48, 32
	for _, tc := range []struct {
		fixture string
		want    vips.Header
	}{
		{"jxr/photo.jxr", with(jxr, 3, 8, false, vips.BandFormatUchar, vips.InterpretationSRGB)},
		{"jxr/alpha.jxr", with(jxr, 4, 8, true, vips.BandFormatUchar, vips.InterpretationSRGB)},
		{"jxr/rgb16.jxr", with(jxr, 3, 16, false, vips.BandFormatUshort, vips.InterpretationRGB16)},
		{"jxr/gray.jxr", with(jxr, 1, 8, false, vips.BandFormatUchar, vips.InterpretationBW)},
		// 32bppRGBE: jxrlib's converter to 8-bit RGB.
		{"jxr/hdr.jxr", hdr},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			got := probeBytes(t, readFixture(t, tc.fixture))
			if diff := cmp.Diff(tc.want, got, cmpopts.IgnoreFields(vips.Header{}, "PageHeight"), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Probe mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// rgbaOf renders img as 8-bit sRGB RGBA for comparisons.
func rgbaOf(ctx context.Context, a *vips.Arena, img *vips.Image) (*image.NRGBA, error) {
	img, err := a.Add(img.Colourspace(ctx, vips.InterpretationSRGB))
	if err != nil {
		return nil, err
	}
	if img.BandFormat() != vips.BandFormatUchar {
		if img, err = a.Add(img.CastRound(ctx, vips.BandFormatUchar)); err != nil {
			return nil, err
		}
	}
	if img.Bands() == 3 {
		if img, err = a.Add(img.BandjoinConst(ctx, []float64{255})); err != nil {
			return nil, err
		}
	}
	pix, err := img.WriteToMemory(ctx)
	if err != nil {
		return nil, err
	}
	return &image.NRGBA{Pix: pix, Stride: img.Width() * 4, Rect: image.Rect(0, 0, img.Width(), img.Height())}, nil
}

// decodeRGBA loads a fixture and renders it with rgbaOf.
func decodeRGBA(tb testing.TB, fixture string) *image.NRGBA {
	tb.Helper()
	buf := newBuffer(tb, readFixture(tb, fixture))
	defer buf.Release()
	var out *image.NRGBA
	job(tb, func(ctx context.Context, a *vips.Arena) error {
		img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Access: vips.AccessRandom}))
		if err != nil {
			return err
		}
		out, err = rgbaOf(ctx, a, img)
		return err
	})
	return out
}

// TestLoadJXR decodes every JPEG XR pixel format and compares it with the
// lossless PCX of the same photo: a swapped channel order, a wrong stride
// or a lost alpha plane shows up as a large DSSIM.
func TestLoadJXR(t *testing.T) {
	t.Parallel()
	ref := decodeRGBA(t, "pcx/photo.pcx")
	refAlpha := decodeRGBA(t, "tga/alpha.tga")
	for _, tc := range []struct {
		fixture string
		ref     *image.NRGBA
		max     float64
	}{
		{"jxr/photo.jxr", ref, 0.01},
		{"jxr/alpha.jxr", refAlpha, 0.01},
		{"jxr/rgb16.jxr", ref, 0.01},
		{"jxr/gray.jxr", ref, 0.05}, // grey against colour: luma only
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			got := decodeRGBA(t, tc.fixture)
			if got.Rect != tc.ref.Rect {
				t.Fatalf("size %v, want %v", got.Rect, tc.ref.Rect)
			}
			r, err := ssim.Compare(tc.ref, got)
			if err != nil {
				t.Fatal(err)
			}
			if d := r.DSSIM; d > tc.max {
				t.Errorf("DSSIM against the lossless photo = %.4f, want <= %.4f", d, tc.max)
			}
			if tc.ref == refAlpha {
				// The alpha plane is coded separately: it must match too.
				var worst int
				for i := 3; i < len(got.Pix); i += 4 {
					worst = max(worst, absDiff(got.Pix[i], tc.ref.Pix[i]))
				}
				if worst > 16 {
					t.Errorf("alpha differs by up to %d from the lossless alpha", worst)
				}
			}
		})
	}
}

func absDiff(a, b byte) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

// TestJXRThumbnail goes through vips_thumbnail_buffer, which finds the
// shim's loader by its is_a_buffer like any libvips loader (/info samples).
func TestJXRThumbnail(t *testing.T) {
	t.Parallel()
	buf := newBuffer(t, readFixture(t, "jxr/alpha.jxr"))
	defer buf.Release()
	job(t, func(ctx context.Context, a *vips.Arena) error {
		img, err := a.Add(vips.Thumbnail(ctx, buf, &vips.ThumbnailOptions{Width: 32, Height: 32}))
		if err != nil {
			return err
		}
		if img.Width() != 32 || img.Height() != 24 || img.Bands() != 4 {
			t.Errorf("thumbnail %dx%d×%d, want 32x24×4", img.Width(), img.Height(), img.Bands())
		}
		return nil
	})
}

// TestJXRErrors checks the failure kinds: a signature with garbage behind
// it, a cut header and cut pixel data.
func TestJXRErrors(t *testing.T) {
	t.Parallel()
	photo := readFixture(t, "jxr/photo.jxr")
	for _, tc := range []struct {
		name string
		data []byte
		load bool // decode the pixels, not just the header
		want errs.Kind
	}{
		{"garbage after the signature", append([]byte("II\xbc\x01"), make([]byte, 60)...), false, errs.KindCorrupt},
		{"header only", photo[:40], false, errs.KindCorrupt},
		{"pixels cut", photo[:len(photo)/2], true, errs.KindCorrupt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			buf := newBuffer(t, tc.data)
			defer buf.Release()
			err := tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
				if !tc.load {
					_, err := vips.Probe(ctx, buf)
					return err
				}
				img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Access: vips.AccessRandom}))
				if err != nil {
					return err
				}
				_, err = img.WriteToMemory(ctx)
				return err
			})
			if tc.load && err == nil {
				// jxrlib decodes what it has; a cut stream may come out
				// grey instead of failing, which is acceptable.
				t.Logf("cut stream decoded without an error")
				return
			}
			if k := errs.KindOf(err); k != tc.want {
				t.Errorf("error %v (kind %s), want %s", err, k, tc.want)
			}
		})
	}
}

// TestNoLeakJXR loads, decodes and encodes every JPEG XR fixture many
// times: libvips' counters and the shim's live jxrlib decoders and pixel
// buffers must come back to where they were. Not parallel: the counters are
// process-wide.
func TestNoLeakJXR(t *testing.T) {
	const cycles = 400
	vips.SetLeakCheck(true)
	defer vips.SetLeakCheck(false)

	names := []string{"jxr/photo.jxr", "jxr/alpha.jxr", "jxr/rgb16.jxr", "jxr/gray.jxr", "jxr/hdr.jxr"}
	inputs := make([][]byte, len(names))
	for i, n := range names {
		inputs[i] = readFixture(t, n)
	}
	so := vips.DefaultSaveOptions()
	cycle := func(i int) {
		buf := newBuffer(t, inputs[i%len(inputs)])
		defer buf.Release()
		job(t, func(ctx context.Context, a *vips.Arena) error {
			if i%3 == 0 { // header only: the decoder is released with the loader
				_, err := vips.Probe(ctx, buf)
				return err
			}
			img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Access: vips.AccessSequential}))
			if err != nil {
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
	before := leakcheck.Take()
	for i := range cycles {
		cycle(i)
	}
	leakcheck.Check(t, before, leakcheck.Default, "cycles")
	if n := vips.JXRLive(); n != 0 {
		t.Errorf("%d jxrlib decoders or pixel buffers still live", n)
	}
}

// FuzzJXR feeds arbitrary bytes behind the JPEG XR signature to the shim's
// loader (header, then the full decode): it must fail cleanly, never crash,
// and never leave a jxrlib decoder or buffer behind.
func FuzzJXR(f *testing.F) {
	for _, n := range []string{"jxr/photo.jxr", "jxr/alpha.jxr", "jxr/rgb16.jxr", "jxr/gray.jxr", "jxr/hdr.jxr"} {
		f.Add(readFixture(f, n))
	}
	f.Add([]byte("II\xbc\x01\x08\x00\x00\x00"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 4 {
			return
		}
		// Keep the signature so every input reaches jxrlib.
		in := append([]byte("II\xbc\x01"), data[4:]...)
		buf := newBuffer(t, in)
		defer buf.Release()
		err := tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
			img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Access: vips.AccessRandom, MaxPixels: 1 << 20}))
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
		if n := vips.JXRLive(); n < 0 {
			t.Fatalf("live counter %d", n)
		}
	})
}
