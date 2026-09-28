//go:build cgo

package vips_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/testutil/leakcheck"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// The sequence fixtures (testdata/gen/heifseq) are 4 frames of 64×48: frame
// i is a solid palette colour with a white 16×16 square at x = 4 + 12i,
// y = 16, and lasts (i + 1) × 100 ms. sequence-alpha.avif has a half
// transparent left half and plays twice; the others loop forever.
var seqPalette = [][3]int{{200, 40, 40}, {40, 170, 60}, {40, 70, 200}, {220, 200, 40}}

func TestProbeHEIFSequence(t *testing.T) {
	t.Parallel()
	avif := vips.Header{
		Loader: "heifseqload_buffer", Format: vips.FormatAVIF, Subformat: "av1", Width: 64, Height: 48,
		Bands: 3, BandFormat: vips.BandFormatUchar, Interpretation: vips.InterpretationSRGB, Pages: 4,
		PageHeight: 48, Orientation: 1, BitDepth: 8, IsAnimated: true, Delay: []int{100, 200, 300, 400},
		Loop: 0,
	}
	alpha := avif
	alpha.Bands, alpha.HasAlpha, alpha.Loop = 4, true, 2
	heic := avif
	heic.Format, heic.Subformat = vips.FormatHEIF, "hevc"
	colors := vips.Header{
		Loader: "heifseqload_buffer", Format: vips.FormatAVIF, Subformat: "av1", Width: 150, Height: 150,
		Bands: 3, BandFormat: vips.BandFormatUchar, Interpretation: vips.InterpretationSRGB, Pages: 5,
		PageHeight: 150, Orientation: 1, BitDepth: 8, IsAnimated: true, Delay: []int{33, 33, 33, 33, 33},
		Loop: 1,
	}
	for _, tc := range []struct {
		fixture string
		want    vips.Header
	}{
		{"avif/sequence.avif", avif},
		{"avif/sequence-alpha.avif", alpha},
		{"heic/sequence.heic", heic},
		{"avif/libavif-colors-animated-8bpc.avif", colors},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			got := probeBytes(t, readFixture(t, tc.fixture))
			if diff := cmp.Diff(tc.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Probe mismatch (-want +got):\n%s", diff)
			}
		})
	}
	// Stills keep libvips' heifload.
	for _, fixture := range []string{"avif/photo.avif", "heic/photo.heic"} {
		if got := probeBytes(t, readFixture(t, fixture)); got.Loader != "heifload_buffer" {
			t.Errorf("%s loaded by %s, want heifload_buffer", fixture, got.Loader)
		}
	}
}

// TestLoadHEIFSequence decodes every frame, and one frame with page, and
// finds each frame's colour and square where the generator put them.
func TestLoadHEIFSequence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fixture string
		page, n int
		first   int // frame index of the first loaded frame
		frames  int
	}{
		{"avif/sequence.avif", 0, -1, 0, 4},
		{"avif/sequence-alpha.avif", 0, -1, 0, 4},
		{"heic/sequence.heic", 0, -1, 0, 4},
		{"avif/sequence.avif", 2, 1, 2, 1},
		{"avif/sequence.avif", 1, 2, 1, 2},
		{"avif/sequence.avif", 0, 0, 0, 1}, // loader default: the first frame
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			buf := newBuffer(t, readFixture(t, tc.fixture))
			defer buf.Release()
			job(t, func(ctx context.Context, a *vips.Arena) error {
				img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Page: tc.page, NPages: tc.n, Access: vips.AccessRandom}))
				if err != nil {
					return err
				}
				if img.Width() != 64 || img.Height() != 48*tc.frames || img.PageHeight() != 48 {
					t.Fatalf("got %dx%d page-height %d, want 64x%d page-height 48", img.Width(), img.Height(),
						img.PageHeight(), 48*tc.frames)
				}
				pix, err := img.WriteToMemory(ctx)
				if err != nil {
					return err
				}
				bands := img.Bands()
				at := func(x, y int) []int {
					i := (y*64 + x) * bands
					out := make([]int, bands)
					for b := range bands {
						out[b] = int(pix[i+b])
					}
					return out
				}
				for f := range tc.frames {
					k := tc.first + f
					bg := at(60, 48*f+4)
					for c := range 3 {
						if d := bg[c] - seqPalette[k][c]; d < -12 || d > 12 {
							t.Errorf("frame %d background %v, want about %v", k, bg[:3], seqPalette[k])
							break
						}
					}
					sq := at(12+12*k, 48*f+24)
					if sq[0] < 230 || sq[1] < 230 || sq[2] < 230 {
						t.Errorf("frame %d: square pixel %v, want white", k, sq[:3])
					}
					if bands == 4 {
						if l, r := at(4, 48*f+4)[3], at(60, 48*f+4)[3]; l < 110 || l > 146 || r != 255 {
							t.Errorf("frame %d alpha left %d right %d, want 128 and 255", k, l, r)
						}
					}
				}
				return nil
			})
		})
	}
}

// TestHEIFSequenceErrors: a cut file fails cleanly (corrupt, or unsupported
// when the moov box is gone), and page past the end is refused by the
// loader.
func TestHEIFSequenceErrors(t *testing.T) {
	t.Parallel()
	data := readFixture(t, "avif/sequence.avif")
	for _, cut := range []int{len(data) / 2, len(data) - 40} {
		buf := newBuffer(t, data[:cut])
		err := tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
			img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Access: vips.AccessRandom, NPages: -1}))
			if err != nil {
				return err
			}
			_, err = img.WriteToMemory(ctx)
			return err
		})
		buf.Release()
		if k := errs.KindOf(err); k != errs.KindCorrupt && k != errs.KindUnsupported {
			t.Errorf("cut at %d: %v (kind %s), want corrupt or unsupported", cut, err, k)
		}
	}
	buf := newBuffer(t, data)
	defer buf.Release()
	err := tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
		_, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Page: 4}))
		return err
	})
	if errs.KindOf(err) != errs.KindCorrupt {
		t.Errorf("page 4 of 4: %v, want corrupt", err)
	}
	err = tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
		_, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{NPages: -1, MaxFrames: 3}))
		return err
	})
	if errs.KindOf(err) != errs.KindLimitExceeded {
		t.Errorf("4 frames with a limit of 3: %v, want limit exceeded", err)
	}
}

// TestHEIFSequenceBrokenLoop is FuzzHEIFSequence's first finding
// (testdata/heif/broken-indefinite-loop.heic: sequence.heic with an
// indefinite mvhd duration, a repeating edit list and a damaged HEVC
// stream). libheif feeds a decoder that returns no frame as many samples as
// the repetitions allow, capped by max_sequence_frames: 18 million by
// default, half a minute here. The loader caps that limit to the file's
// sample count, so the load fails at once.
func TestHEIFSequenceBrokenLoop(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("testdata", "heif", "broken-indefinite-loop.heic"))
	if err != nil {
		t.Fatal(err)
	}
	buf := newBuffer(t, data)
	defer buf.Release()
	start := time.Now()
	err = tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
		img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{NPages: -1, Access: vips.AccessRandom}))
		if err != nil {
			return err
		}
		_, err = img.WriteToMemory(ctx)
		return err
	})
	if errs.KindOf(err) != errs.KindCorrupt {
		t.Errorf("load: %v, want corrupt", err)
	}
	// Generous for a loaded machine; the uncapped decoder took 30 s.
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("failing took %v", d)
	}
}

// TestHEIFSequenceThumbnail: the thumbnail path hands page and n to the
// loader like any multi-page loader.
func TestHEIFSequenceThumbnail(t *testing.T) {
	t.Parallel()
	buf := newBuffer(t, readFixture(t, "avif/sequence.avif"))
	defer buf.Release()
	job(t, func(ctx context.Context, a *vips.Arena) error {
		img, err := a.Add(vips.Thumbnail(ctx, buf, &vips.ThumbnailOptions{Width: 32, NPages: -1}))
		if err != nil {
			return err
		}
		if img.Width() != 32 || img.Height() != 4*24 || img.PageHeight() != 24 {
			t.Errorf("thumbnail %dx%d page-height %d, want 32x96 page-height 24", img.Width(), img.Height(), img.PageHeight())
		}
		return nil
	})
}

// TestHEIFGainMap reads the tmap item of the libavif samples: the gain map
// image and the payload (version 0; the unsupported sample says 99).
func TestHEIFGainMap(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fixture string
		w, h    int
		version byte
		none    bool
	}{
		{"gainmap/libavif-color_grid_gainmap_different_grid.avif", 128, 160, 0, false},
		{"gainmap/libavif-color_nogrid_alpha_nogrid_gainmap_grid.avif", 128, 160, 0, false},
		{"gainmap/libavif-unsupported_gainmap_version.avif", 50, 50, 99, false},
		{"gainmap/libavif-seine_sdr_gainmap_srgb.avif", 400, 300, 0, false},
		{"avif/photo.avif", 0, 0, 0, true}, // no tmap: nothing, no error
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			buf := newBuffer(t, readFixture(t, tc.fixture))
			defer buf.Release()
			job(t, func(ctx context.Context, _ *vips.Arena) error {
				gm, meta, err := vips.LoadHEIFGainMap(ctx, buf)
				if err != nil {
					return err
				}
				if tc.none {
					if gm != nil || meta != nil {
						gm.Close()
						t.Errorf("gain map %v, payload %d bytes, want none", gm, len(meta))
					}
					return nil
				}
				if gm == nil {
					t.Fatal("no gain map")
				}
				defer gm.Close()
				if gm.Bands() != 3 || gm.BandFormat() != vips.BandFormatUchar {
					t.Errorf("gain map %d bands %v, want 3 uchar", gm.Bands(), gm.BandFormat())
				}
				if gm.Width() != tc.w || gm.Height() != tc.h {
					t.Errorf("gain map %dx%d, want %dx%d", gm.Width(), gm.Height(), tc.w, tc.h)
				}
				if len(meta) == 0 || meta[0] != tc.version {
					t.Errorf("payload version %v, want %d", meta[:min(1, len(meta))], tc.version)
				}
				return nil
			})
		})
	}
}

// scRGBRamp is a w×h float scRGB image whose three bands all hold a ramp
// from 0 to top along x.
func scRGBRamp(ctx context.Context, a *vips.Arena, w, h int, top float32) (*vips.Image, error) {
	pix := make([]float32, w*h*3)
	for y := range h {
		for x := range w {
			v := top * float32(x) / float32(w-1)
			i := (y*w + x) * 3
			pix[i], pix[i+1], pix[i+2] = v, v, v
		}
	}
	img, err := a.Add(vips.NewFromFloats(ctx, pix, w, h, 3))
	if err != nil {
		return nil, err
	}
	return a.Add(img.Copy(ctx, vips.CopyOptions{Interpretation: vips.InterpretationScRGB}))
}

// pq is the BT.2100 PQ inverse EOTF of cd/m² (the saver's reference).
func pq(nits float64) float64 {
	const m1, m2 = 2610.0 / 16384, 2523.0 / 4096 * 128
	const c1, c2, c3 = 3424.0 / 4096, 2413.0 / 4096 * 32, 2392.0 / 4096 * 32
	p := math.Pow(nits/10000, m1)
	return math.Pow((c1+c2*p)/(1+c3*p), m2)
}

// TestSaveAVIFHDR: a float scRGB ramp to 8 (8 × 203 cd/m²) is written as a
// 12-bit PQ BT.2020 AVIF: nclx 9/16/9 and a clli box in the file, and the
// decoded code values follow the PQ curve (libvips hands them over
// unconverted, scaled to 16 bits).
func TestSaveAVIFHDR(t *testing.T) {
	t.Parallel()
	const w, h = 256, 16
	job(t, func(ctx context.Context, a *vips.Arena) error {
		img, err := scRGBRamp(ctx, a, w, h, 8)
		if err != nil {
			return err
		}
		o := vips.DefaultSaveOptions()
		o.HEIF.Quality = 95
		out, err := saveBytes(ctx, img, vips.FormatAVIF, &o)
		if err != nil {
			return err
		}
		if !bytes.Contains(out, []byte("colrnclx\x00\x09\x00\x10\x00\x09")) {
			t.Error("no BT.2020 PQ nclx colr box")
		}
		if !bytes.Contains(out, []byte("clli")) {
			t.Error("no clli box")
		}
		buf := newBuffer(t, out)
		defer buf.Release()
		h, err := vips.Probe(ctx, buf)
		if err != nil {
			return err
		}
		if h.Format != vips.FormatAVIF || h.BitDepth != 12 {
			t.Errorf("probe: %s %d-bit, want AVIF 12-bit", h.Format, h.BitDepth)
		}
		dec, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Access: vips.AccessRandom}))
		if err != nil {
			return err
		}
		raw, err := dec.WriteToMemory(ctx)
		if err != nil {
			return err
		}
		if dec.BandFormat() != vips.BandFormatUshort {
			t.Fatalf("decoded %v, want ushort", dec.BandFormat())
		}
		for _, x := range []int{16, 32, 64, 128, 250} {
			i := ((h.Height/2)*w + x) * dec.Bands() * 2
			got := float64(binary.NativeEndian.Uint16(raw[i:])) / 65535
			want := pq(8 * float64(x) / (w - 1) * 203)
			if math.Abs(got-want) > 0.02 {
				t.Errorf("x=%d: PQ code %.4f, want %.4f", x, got, want)
			}
		}
		return nil
	})
}

// TestSaveUltraHDR: a float scRGB image with highlights above 1 is written
// as UltraHDR JPEG (a gain map that uhdrload finds), and UHDRGainMap
// reads it back.
func TestSaveUltraHDR(t *testing.T) {
	t.Parallel()
	job(t, func(ctx context.Context, a *vips.Arena) error {
		img, err := scRGBRamp(ctx, a, 128, 32, 6)
		if err != nil {
			return err
		}
		out, err := saveBytes(ctx, img, vips.FormatJPEG, nil)
		if err != nil {
			return err
		}
		buf := newBuffer(t, out)
		defer buf.Release()
		h, err := vips.Probe(ctx, buf)
		if err != nil {
			return err
		}
		if h.Loader != "uhdrload_buffer" {
			t.Fatalf("loader %s, want uhdrload_buffer", h.Loader)
		}
		dec, err := a.Add(vips.Load(ctx, buf, nil))
		if err != nil {
			return err
		}
		gm, meta, ok, err := dec.UHDRGainMap(ctx)
		if err != nil || !ok {
			t.Fatalf("UHDRGainMap: ok %v, %v", ok, err)
		}
		defer gm.Close()
		if meta.MaxContentBoost[0] <= 1 || meta.CapacityMax <= 1 {
			t.Errorf("gain map metadata %+v, want a boost above 1", meta)
		}
		if gm.Width() == 0 || gm.BandFormat() != vips.BandFormatUchar {
			t.Errorf("gain map %dx%d %v", gm.Width(), gm.Height(), gm.BandFormat())
		}
		// No gain map on a plain JPEG.
		plain := newBuffer(t, readFixture(t, "jpeg/baseline.jpg"))
		defer plain.Release()
		p, err := a.Add(vips.Load(ctx, plain, nil))
		if err != nil {
			return err
		}
		if _, _, ok, err := p.UHDRGainMap(ctx); ok || err != nil {
			t.Errorf("baseline.jpg: gain map %v, %v", ok, err)
		}
		return nil
	})
}

// TestPowExp2 pins the two math2 wrappers the gain-map math uses.
func TestPowExp2(t *testing.T) {
	t.Parallel()
	job(t, func(ctx context.Context, a *vips.Arena) error {
		img, err := a.Add(vips.NewFromFloats(ctx, []float32{0.25, 1, 3}, 3, 1, 1))
		if err != nil {
			return err
		}
		p, err := a.Add(img.Pow(ctx, []float64{0.5, 2, 1}))
		if err != nil {
			return err
		}
		e, err := a.Add(img.Exp2(ctx))
		if err != nil {
			return err
		}
		got := func(im *vips.Image) []float64 {
			raw, err := im.WriteToMemory(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var out []float64
			for i := 0; i+4 <= len(raw); i += 4 {
				out = append(out, float64(math.Float32frombits(binary.NativeEndian.Uint32(raw[i:]))))
			}
			return out
		}
		approx := cmpopts.EquateApprox(0, 1e-5)
		// One band with three exponents gives three bands per pixel.
		if diff := cmp.Diff([]float64{0.5, 0.0625, 0.25, 1, 1, 1, 1.7320508, 9, 3}, got(p), approx); diff != "" {
			t.Errorf("Pow (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]float64{1.1892071, 2, 8}, got(e), approx); diff != "" {
			t.Errorf("Exp2 (-want +got):\n%s", diff)
		}
		return nil
	})
}

// TestNoLeakHEIF probes and loads the sequences, reads a tmap gain map and
// writes HDR AVIF and UltraHDR many times: libvips' counters and the
// shim's live libheif contexts and buffers must come back. Not parallel:
// the counters are process-wide.
func TestNoLeakHEIF(t *testing.T) {
	const cycles = 200
	vips.SetLeakCheck(true)
	defer vips.SetLeakCheck(false)

	seqs := [][]byte{
		readFixture(t, "avif/sequence.avif"), readFixture(t, "avif/sequence-alpha.avif"),
		readFixture(t, "heic/sequence.heic"),
	}
	tmap := readFixture(t, "gainmap/libavif-color_nogrid_alpha_nogrid_gainmap_grid.avif")
	so := vips.DefaultSaveOptions()
	so.HEIF.Effort = 0
	cycle := func(i int) {
		job(t, func(ctx context.Context, a *vips.Arena) error {
			switch i % 5 {
			case 0:
				buf := newBuffer(t, seqs[i%len(seqs)])
				defer buf.Release()
				_, err := vips.Probe(ctx, buf)
				return err
			case 1, 2:
				buf := newBuffer(t, seqs[i%len(seqs)])
				defer buf.Release()
				img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{NPages: -1, Access: vips.AccessSequential}))
				if err != nil {
					return err
				}
				blob, err := img.Save(ctx, vips.FormatWebP, &so)
				if err != nil {
					return err
				}
				blob.Release()
				return nil
			case 3:
				buf := newBuffer(t, tmap)
				defer buf.Release()
				gm, _, err := vips.LoadHEIFGainMap(ctx, buf)
				if err != nil {
					return err
				}
				_, err = a.Add(gm, nil)
				return err
			default:
				img, err := scRGBRamp(ctx, a, 48, 16, 4)
				if err != nil {
					return err
				}
				f := vips.FormatAVIF
				if i%2 == 0 {
					f = vips.FormatJPEG
				}
				blob, err := img.Save(ctx, f, &so)
				if err != nil {
					return err
				}
				blob.Release()
				return nil
			}
		})
	}
	for i := range 10 {
		cycle(i)
	}
	before := leakcheck.Take()
	for i := range cycles {
		cycle(i)
	}
	leakcheck.Check(t, before, leakcheck.Default, "cycles")
	if n := vips.HEIFLive(); n != 0 {
		t.Errorf("%d libheif contexts or pixel buffers still live", n)
	}
}

// FuzzHEIFSequence mutates the sequence fixtures behind their ftyp box (so
// that the shim's loader keeps being chosen): the loader and its sample
// table walk must fail cleanly and never leave a libheif context behind.
func FuzzHEIFSequence(f *testing.F) {
	for _, name := range []string{
		"avif/sequence.avif", "avif/sequence-alpha.avif", "heic/sequence.heic",
		"avif/libavif-colors-animated-8bpc.avif",
	} {
		f.Add(readFixture(f, name))
	}
	ftyp := readFixture(f, "avif/sequence.avif")[:32]
	f.Fuzz(func(t *testing.T, data []byte) {
		in := data
		if len(in) < 32 || !bytes.Equal(in[4:8], []byte("ftyp")) {
			in = append(append([]byte(nil), ftyp...), data...)
		}
		buf := newBuffer(t, in)
		defer buf.Release()
		err := tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
			if _, err := vips.Probe(ctx, buf); err != nil {
				return err
			}
			img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{
				NPages: -1, Access: vips.AccessRandom,
				MaxPixels: 1 << 22, MaxFrames: 64,
			}))
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
		if n := vips.HEIFLive(); n < 0 {
			t.Fatalf("live counter %d", n)
		}
	})
}

// FuzzHEIFGainMap mutates the tmap samples: xi_heif_gainmap must fail
// cleanly or return an image and a payload.
func FuzzHEIFGainMap(f *testing.F) {
	for _, name := range []string{
		"gainmap/libavif-color_grid_gainmap_different_grid.avif",
		"gainmap/libavif-color_nogrid_alpha_nogrid_gainmap_grid.avif",
	} {
		f.Add(readFixture(f, name))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			return
		}
		buf := newBuffer(t, data)
		defer buf.Release()
		err := tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
			gm, meta, err := vips.LoadHEIFGainMap(ctx, buf)
			if err != nil {
				return err
			}
			if gm == nil {
				return nil
			}
			if _, err := a.Add(gm, nil); err != nil {
				return err
			}
			if len(meta) == 0 {
				t.Fatal("gain map without a payload")
			}
			_, err = gm.WriteToMemory(ctx)
			return err
		})
		switch errs.KindOf(err) {
		case errs.KindCorrupt, errs.KindUnsupported:
		default:
			if err != nil {
				t.Fatalf("unexpected error kind: %v", err)
			}
		}
	})
}
