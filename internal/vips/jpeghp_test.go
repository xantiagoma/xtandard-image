//go:build cgo

package vips_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/testutil/leakcheck"
	"github.com/xantiagoma/xtandard-image/internal/testutil/ssim"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// The high-precision JPEG fixtures are the 64×48 photo at 16 bits
// (testdata/gen/jpeghp.sh): 12-bit DCT and lossless files written by
// libjpeg-turbo's cjpeg, CMYK and lossless YCbCr by gen/jpeghp/encode.c,
// with djpeg's decodes in testdata/golden/codec.

func TestProbeJPEGHP(t *testing.T) {
	t.Parallel()
	hdr := func(bands int, f vips.BandFormat, in vips.Interpretation, bits int) vips.Header {
		return vips.Header{
			Loader: "jpeghpload_buffer", Format: vips.FormatJPEG, Width: 64, Height: 48, Bands: bands,
			BandFormat: f, Interpretation: in, Pages: 1, Orientation: 1, BitDepth: bits, Loop: -1,
		}
	}
	rot := hdr(3, vips.BandFormatUshort, vips.InterpretationRGB16, 12)
	rot.Width, rot.Height, rot.Orientation, rot.HasICC = 48, 64, 6, true
	cmyk12 := hdr(4, vips.BandFormatUshort, vips.InterpretationCMYK, 12)
	cmyk12.HasICC = true
	cmyk16 := hdr(4, vips.BandFormatUshort, vips.InterpretationCMYK, 16)
	cmyk16.HasICC = true
	for _, tc := range []struct {
		fixture string
		want    vips.Header
	}{
		{"jpeg/rgb12.jpg", hdr(3, vips.BandFormatUshort, vips.InterpretationRGB16, 12)},
		{"jpeg/grey12-progressive.jpg", hdr(1, vips.BandFormatUshort, vips.InterpretationGrey16, 12)},
		{"jpeg/rgb12-exif6-icc.jpg", rot},
		{"jpeg/cmyk12.jpg", cmyk12},
		{"jpeg/lossless16.jpg", hdr(3, vips.BandFormatUshort, vips.InterpretationRGB16, 16)},
		{"jpeg/lossless16-cmyk.jpg", cmyk16},
		{"jpeg/lossless12-ycc.jpg", hdr(3, vips.BandFormatUshort, vips.InterpretationRGB16, 12)},
		{"jpeg/lossless10-grey.jpg", hdr(1, vips.BandFormatUshort, vips.InterpretationGrey16, 10)},
		{"jpeg/lossless8.jpg", hdr(3, vips.BandFormatUchar, vips.InterpretationSRGB, 8)},
		{"jpeg/lossless6.jpg", hdr(3, vips.BandFormatUchar, vips.InterpretationSRGB, 6)},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			got := probeBytes(t, readFixture(t, tc.fixture))
			if diff := cmp.Diff(tc.want, got, cmpopts.IgnoreFields(vips.Header{}, "PageHeight"), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Probe mismatch (-want +got):\n%s", diff)
			}
		})
	}
	// Every 8-bit DCT JPEG still goes to libvips' jpegload.
	for _, f := range []string{"jpeg/baseline.jpg", "jpeg/progressive.jpg"} {
		if got := probeBytes(t, readFixture(t, f)); got.Loader != "jpegload_buffer" {
			t.Errorf("%s loaded by %s, want jpegload_buffer", f, got.Loader)
		}
	}
}

// readPNM parses a reference decode in testdata/golden/codec, a binary
// PGM/PPM (P5/P6) as written by djpeg: samples above 255 are big-endian
// 16-bit.
func readPNM(tb testing.TB, name string) (w, h, bands, maxval int, samples []int) {
	tb.Helper()
	data, err := os.ReadFile(filepath.Join(golden.ModuleRoot(tb), "testdata", "golden", "codec", name))
	if err != nil {
		tb.Fatal(err)
	}
	var fields []int
	i := 2
	switch string(data[:2]) {
	case "P5":
		bands = 1
	case "P6":
		bands = 3
	default:
		tb.Fatalf("%s: not a binary PNM", name)
	}
	for len(fields) < 3 {
		for data[i] == ' ' || data[i] == '\n' || data[i] == '\r' || data[i] == '\t' {
			i++
		}
		j := i
		for data[j] >= '0' && data[j] <= '9' {
			j++
		}
		v, err := strconv.Atoi(string(data[i:j]))
		if err != nil {
			tb.Fatalf("%s: header: %v", name, err)
		}
		fields = append(fields, v)
		i = j
	}
	w, h, maxval = fields[0], fields[1], fields[2]
	px := data[i+1:]
	n := w * h * bands
	samples = make([]int, n)
	for k := range n {
		if maxval > 255 {
			samples[k] = int(binary.BigEndian.Uint16(px[2*k:]))
		} else {
			samples[k] = int(px[k])
		}
	}
	return w, h, bands, maxval, samples
}

// hpSamples is a loaded image's size, band format and samples.
type hpSamples struct {
	w, h, bands int
	format      vips.BandFormat
	samples     []int
}

// loadSamples loads a fixture unrotated and returns its samples.
func loadSamples(tb testing.TB, fixture string) (img hpSamples) {
	tb.Helper()
	buf := newBuffer(tb, readFixture(tb, fixture))
	defer buf.Release()
	job(tb, func(ctx context.Context, a *vips.Arena) error {
		im, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Access: vips.AccessSequential}))
		if err != nil {
			return err
		}
		pix, err := im.WriteToMemory(ctx)
		if err != nil {
			return err
		}
		img.w, img.h, img.bands, img.format = im.Width(), im.Height(), im.Bands(), im.BandFormat()
		if img.format == vips.BandFormatUshort {
			img.samples = make([]int, len(pix)/2)
			for k := range img.samples {
				img.samples[k] = int(binary.NativeEndian.Uint16(pix[2*k:]))
			}
			return nil
		}
		img.samples = make([]int, len(pix))
		for k, v := range pix {
			img.samples[k] = int(v)
		}
		return nil
	})
	return img
}

// TestLoadJPEGHPReference compares the loader's samples with djpeg's decode
// of the same file (libjpeg-turbo's reference decoder): brought back to the
// file's precision (the loader scales them to 8 or 16 bits), every sample
// must be equal.
func TestLoadJPEGHPReference(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ fixture, ref string }{
		{"jpeg/rgb12.jpg", "jpeg-rgb12-djpeg.ppm"},
		{"jpeg/grey12-progressive.jpg", "jpeg-grey12-progressive-djpeg.pgm"},
		{"jpeg/lossless16.jpg", "jpeg-lossless16-djpeg.ppm"},
		{"jpeg/lossless10-grey.jpg", "jpeg-lossless10-grey-djpeg.pgm"},
		{"jpeg/lossless8.jpg", "jpeg-lossless8-djpeg.ppm"},
		{"jpeg/lossless6.jpg", "jpeg-lossless6-djpeg.ppm"},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			w, h, bands, maxval, want := readPNM(t, tc.ref)
			got := loadSamples(t, tc.fixture)
			if got.w != w || got.h != h || got.bands != bands {
				t.Fatalf("loaded %dx%dx%d, reference %dx%dx%d", got.w, got.h, got.bands, w, h, bands)
			}
			full := 255
			if got.format == vips.BandFormatUshort {
				full = 65535
			}
			bad := 0
			for k, v := range got.samples {
				native := (v*maxval + full/2) / full
				if native != want[k] {
					if bad < 5 {
						t.Errorf("sample %d: %d (%d at the file's precision), djpeg %d", k, v, native, want[k])
					}
					bad++
				}
			}
			if bad > 0 {
				t.Errorf("%d of %d samples differ from djpeg", bad, len(want))
			}
		})
	}
}

// TestLoadJPEGHPCMYKLossless: the lossless 16-bit CMYK fixture holds the
// 8-bit inks of gen cmyk-raw scaled by 257 and inverted the Adobe way; the
// loader must give back exactly the inks (0 = no ink, as libvips CMYK).
func TestLoadJPEGHPCMYKLossless(t *testing.T) {
	t.Parallel()
	got := loadSamples(t, "jpeg/lossless16-cmyk.jpg")
	ref := loadSamples(t, "jpeg/cmyk12.jpg") // same inks, lossy: a sanity bound
	if got.bands != 4 || got.format != vips.BandFormatUshort {
		t.Fatalf("loaded %d bands of %v", got.bands, got.format)
	}
	for k, v := range got.samples {
		if v%257 != 0 {
			t.Fatalf("sample %d = %d, not an 8-bit ink × 257", k, v)
		}
		if d := v - ref.samples[k]; d > 4096 || d < -4096 {
			t.Fatalf("sample %d = %d, lossy 12-bit decode %d", k, v, ref.samples[k])
		}
	}
}

// TestLoadJPEGHPColour renders every fixture to 8-bit sRGB (CMYK through
// its embedded profile, EXIF orientation applied) and compares it with the
// lossless 16-bit decode, which is the master photo: a wrong channel order,
// YCbCr left unconverted, inks read uninverted or a wrong scale give a
// large DSSIM.
func TestLoadJPEGHPColour(t *testing.T) {
	t.Parallel()
	master := renderJPEGHP(t, "jpeg/lossless16.jpg")
	// The CMYK fixtures separate the 8-bit photo (with its noise and
	// caption, gen cmyk-raw), whose lossless copy is pcx/photo.pcx.
	photo := decodeRGBA(t, "pcx/photo.pcx")
	for _, tc := range []struct {
		fixture string
		ref     *image.NRGBA
		max     float64
	}{
		{"jpeg/rgb12.jpg", master, 0.01},
		{"jpeg/rgb12-exif6-icc.jpg", master, 0.01},
		{"jpeg/cmyk12.jpg", photo, 0.01},
		{"jpeg/lossless16-cmyk.jpg", photo, 0.01},
		{"jpeg/lossless12-ycc.jpg", master, 0.002},
		{"jpeg/lossless8.jpg", master, 0.002},
		{"jpeg/lossless6.jpg", master, 0.05},
		{"jpeg/grey12-progressive.jpg", master, 0.05}, // grey against colour: luma only
		{"jpeg/lossless10-grey.jpg", master, 0.05},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			got := renderJPEGHP(t, tc.fixture)
			r, err := ssim.Compare(tc.ref, got)
			if err != nil {
				t.Fatal(err)
			}
			if r.DSSIM > tc.max {
				t.Errorf("DSSIM against the master = %.4f, want <= %.4f", r.DSSIM, tc.max)
			}
		})
	}
}

// renderJPEGHP loads a fixture, applies its orientation and CMYK profile
// and renders it with rgbaOf.
func renderJPEGHP(tb testing.TB, fixture string) *image.NRGBA {
	tb.Helper()
	buf := newBuffer(tb, readFixture(tb, fixture))
	defer buf.Release()
	var out *image.NRGBA
	job(tb, func(ctx context.Context, a *vips.Arena) error {
		img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Access: vips.AccessRandom}))
		if err != nil {
			return err
		}
		if img.Interpretation() == vips.InterpretationCMYK {
			if img, err = a.Add(img.ICCImport(ctx, "", vips.IntentRelative, true)); err != nil {
				return err
			}
		}
		if img, err = a.Add(img.Autorot(ctx)); err != nil {
			return err
		}
		out, err = rgbaOf(ctx, a, img)
		return err
	})
	return out
}

// TestJPEGHPMetadata: EXIF (parsed by libvips: orientation, exif-ifd0
// fields) and the ICC profile come through as with jpegload.
func TestJPEGHPMetadata(t *testing.T) {
	t.Parallel()
	data := readFixture(t, "jpeg/rgb12-exif6-icc.jpg")
	buf := newBuffer(t, data)
	defer buf.Release()
	job(t, func(ctx context.Context, a *vips.Arena) error {
		img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Access: vips.AccessSequential}))
		if err != nil {
			return err
		}
		if o := img.Orientation(); o != 6 {
			t.Errorf("orientation %d, want 6", o)
		}
		if _, ok := img.Text("exif-ifd0-Orientation"); !ok {
			t.Error("EXIF not parsed: no exif-ifd0-Orientation field")
		}
		icc, ok := img.Blob(vips.FieldICC)
		if !ok || len(icc) < 128 || !bytes.Equal(icc[36:40], []byte("acsp")) {
			t.Errorf("ICC profile missing or broken (%d bytes)", len(icc))
		}
		if bits, ok := img.Int("bits-per-sample"); !ok || bits != 12 {
			t.Errorf("bits-per-sample %d, %v; want 12", bits, ok)
		}
		return nil
	})
}

// TestJPEGHPErrors: a cut stream fails as corrupt under the service's
// fail_on (the lazy decode error is attributed to the source); a stream
// whose header is cut before the frame is not claimed at all.
func TestJPEGHPErrors(t *testing.T) {
	t.Parallel()
	for _, fixture := range []string{"jpeg/rgb12.jpg", "jpeg/lossless16.jpg", "jpeg/grey12-progressive.jpg"} {
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()
			data := readFixture(t, fixture)
			buf := newBuffer(t, data[:len(data)*2/3])
			defer buf.Release()
			err := tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
				img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Access: vips.AccessSequential}))
				if err != nil {
					return err
				}
				_, err = img.WriteToMemory(ctx)
				return err
			})
			if k := errs.KindOf(err); k != errs.KindCorrupt {
				t.Errorf("cut stream: %v (kind %s), want corrupt", err, k)
			}
		})
	}
}

// TestNoLeakJPEGHP probes, loads and encodes the fixtures many times,
// failed decodes included: libvips' counters and the shim's live libjpeg
// decompressors must come back to where they were. Not parallel: the
// counters are process-wide.
func TestNoLeakJPEGHP(t *testing.T) {
	const cycles = 300
	vips.SetLeakCheck(true)
	defer vips.SetLeakCheck(false)

	names := []string{"rgb12", "grey12-progressive", "cmyk12", "lossless16", "lossless12-ycc", "lossless6", "rgb12-exif6-icc"}
	inputs := make([][]byte, 0, len(names))
	for _, f := range names {
		inputs = append(inputs, readFixture(t, "jpeg/"+f+".jpg"))
	}
	cut := inputs[0][:len(inputs[0])/2]
	so := vips.DefaultSaveOptions()
	cycle := func(i int) {
		in := inputs[i%len(inputs)]
		if i%7 == 6 {
			in = cut
		}
		buf := newBuffer(t, in)
		defer buf.Release()
		_ = tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
			if i%3 == 0 {
				_, err := vips.Probe(ctx, buf)
				return err
			}
			img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Access: vips.AccessSequential}))
			if err != nil {
				return err
			}
			if img.Interpretation() == vips.InterpretationCMYK {
				if img, err = a.Add(img.ICCImport(ctx, "", vips.IntentRelative, true)); err != nil {
					return err
				}
			}
			if img, err = a.Add(img.Colourspace(ctx, vips.InterpretationSRGB)); err != nil {
				return err
			}
			if i%5 == 4 { // stop reading halfway: the decoder is still freed
				half, err := a.Add(img.ExtractArea(ctx, image.Rect(0, 0, img.Width(), img.Height()/2)))
				if err != nil {
					return err
				}
				_, err = half.WriteToMemory(ctx)
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
	for i := range 14 {
		cycle(i)
	}
	before := leakcheck.Take()
	for i := range cycles {
		cycle(i)
	}
	leakcheck.Check(t, before, leakcheck.Default, "cycles")
	if n := vips.JPEGHPLive(); n != 0 {
		t.Errorf("%d libjpeg decompressors still live", n)
	}
}

// FuzzJPEG12 mutates the high-precision fixtures (keeping the SOI marker):
// the loader (or jpegload, when the mutation leaves an 8-bit frame) must
// fail cleanly and never leave a decompressor behind.
func FuzzJPEG12(f *testing.F) {
	for _, name := range []string{"rgb12", "grey12-progressive", "cmyk12", "lossless16", "lossless12-ycc", "lossless10-grey", "lossless6", "rgb12-exif6-icc"} {
		f.Add(readFixture(f, "jpeg/"+name+".jpg"))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 2 {
			return
		}
		in := append([]byte{0xFF, 0xD8}, data[2:]...)
		buf := newBuffer(t, in)
		defer buf.Release()
		err := tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
			img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Access: vips.AccessSequential, MaxPixels: 1 << 20}))
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
		if n := vips.JPEGHPLive(); n < 0 {
			t.Fatalf("live counter %d", n)
		}
	})
}
