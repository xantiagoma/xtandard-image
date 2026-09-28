//go:build cgo

package vips_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/testutil/leakcheck"
	"github.com/xantiagoma/xtandard-image/internal/testutil/ssim"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// The CMYK JPEG XL fixtures are the 64×48 photo as naive CMYK (K = 1 −
// max(R, G, B)), lossless, K in a black extra channel, with a CMYK profile
// that inverts the model (testdata/gen/cmyk.go, testdata/gen/jxlcmyk).
func TestProbeJXLCMYK(t *testing.T) {
	t.Parallel()
	cmyk := vips.Header{
		Loader: "jxlcmykload_buffer", Format: vips.FormatJXL, Width: 64, Height: 48, Bands: 4, BitDepth: 8,
		BandFormat: vips.BandFormatUchar, Interpretation: vips.InterpretationCMYK, Pages: 1, Orientation: 1,
		HasICC: true, Loop: -1,
	}
	alpha := cmyk
	alpha.Bands, alpha.HasAlpha = 5, true
	for _, tc := range []struct {
		fixture string
		want    vips.Header
	}{
		{"jxl/cmyk.jxl", cmyk},
		{"jxl/cmyk-alpha.jxl", alpha},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			got := probeBytes(t, readFixture(t, tc.fixture))
			if diff := cmp.Diff(tc.want, got, cmpopts.IgnoreFields(vips.Header{}, "PageHeight"), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Probe mismatch (-want +got):\n%s", diff)
			}
		})
	}
	// Every other JPEG XL file still goes to libvips' jxlload.
	if got := probeBytes(t, readFixture(t, "jxl/photo.jxl")); got.Loader != "jxlload_buffer" {
		t.Errorf("RGB JPEG XL loaded by %s, want jxlload_buffer", got.Loader)
	}
}

// TestLoadJXLCMYK converts the CMYK fixtures to sRGB with their embedded
// profile and compares them with the lossless photo (PCX): inks read
// uninverted or a misplaced black channel give a large DSSIM.
func TestLoadJXLCMYK(t *testing.T) {
	t.Parallel()
	ref := decodeRGBA(t, "pcx/photo.pcx")
	for _, fixture := range []string{"jxl/cmyk.jxl", "jxl/cmyk-alpha.jxl"} {
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()
			buf := newBuffer(t, readFixture(t, fixture))
			defer buf.Release()
			job(t, func(ctx context.Context, a *vips.Arena) error {
				img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Access: vips.AccessRandom}))
				if err != nil {
					return err
				}
				if img, err = a.Add(img.ICCImport(ctx, "", vips.IntentRelative, true)); err != nil {
					return err
				}
				got, err := rgbaOf(ctx, a, img)
				if err != nil {
					return err
				}
				opaque := *got
				opaque.Pix = append([]byte(nil), got.Pix...)
				for i := 3; i < len(opaque.Pix); i += 4 {
					opaque.Pix[i] = 255
				}
				r, err := ssim.Compare(ref, &opaque)
				if err != nil {
					return err
				}
				if r.DSSIM > 0.01 {
					t.Errorf("DSSIM against the photo = %.4f, want <= 0.01", r.DSSIM)
				}
				if fixture == "jxl/cmyk-alpha.jxl" && got.Pix[3] == 255 {
					t.Error("the corner of the alpha fade is opaque")
				}
				return nil
			})
		})
	}
}

// TestJXLCMYKErrors: a cut codestream fails as corrupt (header or pixels).
func TestJXLCMYKErrors(t *testing.T) {
	t.Parallel()
	data := readFixture(t, "jxl/cmyk.jxl")
	buf := newBuffer(t, data[:len(data)/2])
	defer buf.Release()
	err := tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
		img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Access: vips.AccessRandom}))
		if err != nil {
			return err
		}
		_, err = img.WriteToMemory(ctx)
		return err
	})
	if k := errs.KindOf(err); k != errs.KindCorrupt && k != errs.KindUnsupported {
		t.Errorf("cut stream: %v (kind %s), want corrupt or unsupported", err, k)
	}
	if n := vips.JXLCMYKLive(); n < 0 {
		t.Errorf("live counter %d", n)
	}
}

// TestNoLeakJXLCMYK probes, loads and encodes the CMYK fixtures many times:
// libvips' counters and the shim's live libjxl decoders and pixel buffers
// must come back to where they were. Not parallel: the counters are
// process-wide.
func TestNoLeakJXLCMYK(t *testing.T) {
	const cycles = 300
	vips.SetLeakCheck(true)
	defer vips.SetLeakCheck(false)

	inputs := [][]byte{readFixture(t, "jxl/cmyk.jxl"), readFixture(t, "jxl/cmyk-alpha.jxl")}
	so := vips.DefaultSaveOptions()
	cycle := func(i int) {
		buf := newBuffer(t, inputs[i%len(inputs)])
		defer buf.Release()
		job(t, func(ctx context.Context, a *vips.Arena) error {
			if i%3 == 0 {
				_, err := vips.Probe(ctx, buf)
				return err
			}
			img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Access: vips.AccessSequential}))
			if err != nil {
				return err
			}
			if img, err = a.Add(img.ICCImport(ctx, "", vips.IntentRelative, true)); err != nil {
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
	if n := vips.JXLCMYKLive(); n != 0 {
		t.Errorf("%d libjxl decoders or pixel buffers still live", n)
	}
}

// FuzzJXLCMYK mutates the CMYK fixtures (keeping the JPEG XL signature):
// the shim's loader must fail cleanly and never leave a decoder behind.
func FuzzJXLCMYK(f *testing.F) {
	f.Add(readFixture(f, "jxl/cmyk.jxl"))
	f.Add(readFixture(f, "jxl/cmyk-alpha.jxl"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 2 {
			return
		}
		in := append([]byte{0xFF, 0x0A}, data[2:]...)
		if data[0] == 0 { // container signature
			in = data
		}
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
		if n := vips.JXLCMYKLive(); n < 0 {
			t.Fatalf("live counter %d", n)
		}
	})
}
