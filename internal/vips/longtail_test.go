//go:build cgo

package vips_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/testutil/leakcheck"
	"github.com/xantiagoma/xtandard-image/internal/testutil/ssim"
	"github.com/xantiagoma/xtandard-image/internal/testutil/synth"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// loadCoder loads data with the named ImageMagick coder and returns its
// size and bands.
func loadCoder(t *testing.T, data []byte, c vips.MagickCoder) (w, h, bands int, err error) {
	t.Helper()
	buf := newBuffer(t, data)
	defer buf.Release()
	err = tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
		img, err := a.Add(vips.LoadMagickCoder(ctx, buf, c, &vips.LoadOptions{Access: vips.AccessRandom, FailOn: vips.FailOnError}))
		if err != nil {
			return err
		}
		w, h, bands = img.Width(), img.Height(), img.Bands()
		_, err = img.WriteToMemory(ctx) // decode every pixel
		return err
	})
	return w, h, bands, err
}

// TestLoadMagickCoderPICT: the PICT fixture (ImageMagick's writer, version
// 2, 72 DPI) decodes through the named coder to the photo the lossless PCX
// holds.
func TestLoadMagickCoderPICT(t *testing.T) {
	t.Parallel()
	data := readFixture(t, "pict/photo.pct")
	w, h, _, err := loadCoder(t, data, vips.MagickPICT)
	if err != nil || w != 64 || h != 48 {
		t.Fatalf("LoadMagickCoder(PICT) = %dx%d, %v; want 64x48", w, h, err)
	}
	buf := newBuffer(t, data)
	defer buf.Release()
	var got []byte
	job(t, func(ctx context.Context, a *vips.Arena) error {
		img, err := a.Add(vips.LoadMagickCoder(ctx, buf, vips.MagickPICT, nil))
		if err != nil {
			return err
		}
		pix, err := rgbaOf(ctx, a, img)
		if err != nil {
			return err
		}
		got = pix.Pix
		r, err := ssim.Compare(decodeRGBA(t, "pcx/photo.pcx"), pix)
		if err != nil {
			return err
		}
		if r.DSSIM > 0.002 {
			t.Errorf("DSSIM against the lossless photo = %.4f", r.DSSIM)
		}
		return nil
	})
	if len(got) != 64*48*4 {
		t.Errorf("decoded %d bytes", len(got))
	}
}

// TestLoadMagickCoderPCD: a synthetic Photo CD image pack decodes at its
// Base size, turned upright when the pack says it is rotated.
func TestLoadMagickCoderPCD(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		rotation byte
		w, h     int
	}{
		{0, 768, 512},
		{1, 512, 768},
		{2, 768, 512},
		{3, 512, 768},
	} {
		t.Run("rotation "+string(rune('0'+tc.rotation)), func(t *testing.T) {
			t.Parallel()
			w, h, bands, err := loadCoder(t, synth.PCD(tc.rotation), vips.MagickPCD)
			if err != nil || w != tc.w || h != tc.h || bands != 3 {
				t.Errorf("LoadMagickCoder(PCD) = %dx%dx%d, %v; want %dx%dx3", w, h, bands, err, tc.w, tc.h)
			}
		})
	}
}

// TestLoadMagickCoderRefuses: the shim checks the named coder's signature
// before ImageMagick runs, and the generic loader never hands a PICT or a
// Photo CD file to ImageMagick (libvips' sniffer would take PICT for TGA).
func TestLoadMagickCoderRefuses(t *testing.T) {
	t.Parallel()
	pict := readFixture(t, "pict/photo.pct")
	pcd := synth.PCD(0)
	emptyFrame := bytes.Clone(pict)
	binary.BigEndian.PutUint16(emptyFrame[518:], 0) // bottom = top
	for _, tc := range []struct {
		name  string
		data  []byte
		coder vips.MagickCoder
	}{
		{"pict as pcd", pict, vips.MagickPCD},
		{"pcd as pict", pcd, vips.MagickPICT},
		{"png as pict", readFixture(t, "png/rgba8.png"), vips.MagickPICT},
		{"empty pict frame", emptyFrame, vips.MagickPICT},
		{"truncated pcd", pcd[:len(pcd)-1], vips.MagickPCD},
		{"unknown coder", pict, vips.MagickCoder(99)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, _, _, err := loadCoder(t, tc.data, tc.coder); errs.KindOf(err) != errs.KindUnsupported {
				t.Errorf("LoadMagickCoder: %v (kind %v), want unsupported", err, errs.KindOf(err))
			}
		})
	}
	for name, data := range map[string][]byte{"pict": pict, "pcd": pcd} {
		t.Run("generic loader refuses "+name, func(t *testing.T) {
			t.Parallel()
			buf := newBuffer(t, data)
			defer buf.Release()
			err := tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
				_, err := a.Add(vips.Load(ctx, buf, nil))
				return err
			})
			if err == nil {
				t.Error("vips.Load decoded it")
			}
		})
	}
}

// rgbaImage wraps straight RGBA pixels.
func rgbaImage(pix []byte, w, h int) *image.NRGBA {
	return &image.NRGBA{Pix: pix, Stride: w * 4, Rect: image.Rect(0, 0, w, h)}
}

// decodeBasis runs DecodeBasisKTX2 inside vips.Run.
func decodeBasis(t *testing.T, data []byte, maxPixels int64) (vips.BasisImage, error) {
	t.Helper()
	var img vips.BasisImage
	err := vips.Run(t.Context(), func() error {
		var err error
		img, err = vips.DecodeBasisKTX2(t.Context(), data, maxPixels)
		return err
	})
	return img, err
}

// TestDecodeBasisKTX2 transcodes the Basis Universal fixtures (ETC1S,
// UASTC with Zstandard, UASTC HDR), twice each: the transcoder's state
// once cached the first Zstandard level across calls.
func TestDecodeBasisKTX2(t *testing.T) {
	t.Parallel()
	photo := decodeRGBA(t, "pcx/photo.pcx")
	for _, tc := range []struct {
		fixture string
		w, h    int
		alpha   bool
		hdr     bool
	}{
		{"ktx2/etc1s.ktx2", 64, 48, false, false},
		{"ktx2/uastc-zstd.ktx2", 64, 48, true, false},
		{"ktx2/uastc-hdr.ktx2", 48, 32, false, true},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			data := readFixture(t, tc.fixture)
			for range 2 {
				img, err := decodeBasis(t, data, 0)
				if err != nil {
					t.Fatalf("DecodeBasisKTX2: %v", err)
				}
				if img.Width != tc.w || img.Height != tc.h || img.Alpha != tc.alpha || img.HDR != tc.hdr {
					t.Fatalf("decoded %+v, want %dx%d alpha %v hdr %v", img.BasisInfo, tc.w, tc.h, tc.alpha, tc.hdr)
				}
				if tc.hdr {
					if len(img.RGBAHalf) != tc.w*tc.h*4 {
						t.Fatalf("%d half floats", len(img.RGBAHalf))
					}
					continue
				}
				if len(img.RGBA8) != tc.w*tc.h*4 {
					t.Fatalf("%d bytes", len(img.RGBA8))
				}
				if tc.alpha {
					continue // the photo with alpha: compared by the golden test
				}
				got := rgbaImage(img.RGBA8, tc.w, tc.h)
				r, err := ssim.Compare(photo, got)
				if err != nil {
					t.Fatal(err)
				}
				if r.DSSIM > 0.03 { // ETC1S is lossy
					t.Errorf("DSSIM against the photo = %.4f", r.DSSIM)
				}
			}
			info := vips.BasisInfo{}
			err := vips.Run(t.Context(), func() error {
				var err error
				info, err = vips.BasisKTX2Info(t.Context(), data)
				return err
			})
			if err != nil || info.Width != tc.w || info.Height != tc.h || info.HDR != tc.hdr {
				t.Errorf("BasisKTX2Info = %+v, %v", info, err)
			}
		})
	}
}

func TestDecodeBasisKTX2Errors(t *testing.T) {
	t.Parallel()
	etc1s := readFixture(t, "ktx2/etc1s.ktx2")
	truncated := etc1s[:len(etc1s)/2]
	for _, tc := range []struct {
		name string
		data []byte
		max  int64
		kind errs.Kind
	}{
		{"over the pixel limit", etc1s, 100, errs.KindLimitExceeded},
		{"not ktx2", readFixture(t, "png/rgba8.png"), 0, errs.KindUnsupported},
		{"bc7 is not a basis payload", readFixture(t, "ktx2/bc7.ktx2"), 0, errs.KindUnsupported},
		{"truncated", truncated, 0, errs.KindUnsupported},
		{"empty", nil, 0, errs.KindUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := decodeBasis(t, tc.data, tc.max); errs.KindOf(err) != tc.kind {
				t.Errorf("DecodeBasisKTX2: %v (kind %v), want %v", err, errs.KindOf(err), tc.kind)
			}
		})
	}
}

// ktx2Level returns level 0's stored bytes and its uncompressed length.
func ktx2Level(t *testing.T, data []byte) ([]byte, int) {
	t.Helper()
	le := binary.LittleEndian
	off, n, raw := le.Uint64(data[80:]), le.Uint64(data[88:]), le.Uint64(data[96:])
	return data[off : off+n], int(raw) //nolint:gosec // fixture sizes
}

// TestZstdDecompress: the Zstandard level of rgba8-zstd.ktx2 decompresses
// to the raw level of rgba8.ktx2; a wrong size or garbage is corrupt.
func TestZstdDecompress(t *testing.T) {
	t.Parallel()
	src, n := ktx2Level(t, readFixture(t, "ktx2/rgba8-zstd.ktx2"))
	want, _ := ktx2Level(t, readFixture(t, "ktx2/rgba8.ktx2"))
	dst := make([]byte, n)
	if err := vips.ZstdDecompress(dst, src); err != nil {
		t.Fatalf("ZstdDecompress: %v", err)
	}
	if !bytes.Equal(dst, want) {
		t.Error("decompressed level differs from the raw one")
	}
	for name, tc := range map[string]struct{ dst, src []byte }{
		"smaller dst":  {make([]byte, n-1), src},
		"larger dst":   {make([]byte, n+1), src},
		"garbage":      {make([]byte, n), []byte("definitely not a zstd frame")},
		"empty source": {make([]byte, n), nil},
		"truncated":    {make([]byte, n), src[:len(src)/2]},
	} {
		if err := vips.ZstdDecompress(tc.dst, tc.src); errs.KindOf(err) != errs.KindCorrupt {
			t.Errorf("%s: %v, want corrupt", name, err)
		}
	}
}

// TestNoLeakLongTail repeats the WP-108 C paths: PICT and Photo CD through
// ImageMagick's named coders (spool included), Basis Universal transcodes
// and Zstandard. Not parallel: the counters are process-wide.
func TestNoLeakLongTail(t *testing.T) {
	const cycles = 60
	vips.SetLeakCheck(true)
	defer vips.SetLeakCheck(false)
	pict, pcd := readFixture(t, "pict/photo.pct"), synth.PCD(1)
	etc1s, uastc := readFixture(t, "ktx2/etc1s.ktx2"), readFixture(t, "ktx2/uastc-zstd.ktx2")
	so := vips.DefaultSaveOptions()
	cycle := func(i int) {
		data, coder := pict, vips.MagickPICT
		if i%4 == 3 {
			data, coder = pcd, vips.MagickPCD
		}
		buf := newBuffer(t, data)
		defer buf.Release()
		job(t, func(ctx context.Context, a *vips.Arena) error {
			img, err := a.Add(vips.LoadMagickCoder(ctx, buf, coder, &vips.LoadOptions{Access: vips.AccessSequential}))
			if err != nil {
				return err
			}
			blob, err := img.Save(ctx, vips.FormatJPEG, &so)
			if err != nil {
				return err
			}
			blob.Release()
			for _, k := range [][]byte{etc1s, uastc} {
				if _, err := vips.DecodeBasisKTX2(ctx, k, 0); err != nil {
					return err
				}
			}
			return nil
		})
	}
	for i := range 4 {
		cycle(i)
	}
	before := leakcheck.Take()
	for i := range cycles {
		cycle(i)
	}
	leakcheck.Check(t, before, leakcheck.Default, "cycles")
}

// FuzzBasisKTX2 feeds arbitrary bytes to the Basis Universal transcoder
// through the shim (the libFuzzer campaign with ASan is in
// docker/base/README.md): nothing may crash, and a success must be an
// image of the size the header declares.
func FuzzBasisKTX2(f *testing.F) {
	for _, name := range []string{"etc1s", "uastc-zstd", "uastc-hdr"} {
		f.Add(readFixture(f, "ktx2/"+name+".ktx2"))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var img vips.BasisImage
		err := vips.Run(t.Context(), func() error {
			var err error
			img, err = vips.DecodeBasisKTX2(t.Context(), data, 1<<20)
			return err
		})
		if err != nil {
			return
		}
		if img.Width*img.Height <= 0 || img.Width*img.Height > 1<<20 ||
			(len(img.RGBA8) != img.Width*img.Height*4 && len(img.RGBAHalf) != img.Width*img.Height*4) {
			t.Fatalf("decoded %+v with %d/%d samples", img.BasisInfo, len(img.RGBA8), len(img.RGBAHalf))
		}
	})
}

// FuzzMagickPICT feeds mutated PICT opcode streams to ImageMagick's PICT
// coder through the shim, behind a valid 528-byte header (the shim refuses
// anything else before ImageMagick runs).
func FuzzMagickPICT(f *testing.F) {
	pict := readFixture(f, "pict/photo.pct")
	f.Add(pict[528:])
	f.Fuzz(func(t *testing.T, body []byte) {
		data := append(bytes.Clone(pict[:528]), body...)
		buf, err := vips.NewBuffer(len(data))
		if err != nil {
			t.Fatal(err)
		}
		defer buf.Release()
		copy(buf.Bytes(), data)
		_ = tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
			img, err := a.Add(vips.LoadMagickCoder(ctx, buf, vips.MagickPICT, &vips.LoadOptions{MaxPixels: 1 << 22}))
			if err != nil {
				return err
			}
			_, err = img.WriteToMemory(ctx)
			return err
		})
	})
}
