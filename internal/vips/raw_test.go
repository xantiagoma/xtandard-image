//go:build cgo

package vips_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/testutil/leakcheck"
	"github.com/xantiagoma/xtandard-image/internal/testutil/rawfixtures"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

func probeRaw(tb testing.TB, data []byte) (vips.RawHeader, error) {
	tb.Helper()
	buf := newBuffer(tb, data)
	defer buf.Release()
	var h vips.RawHeader
	err := tryJob(tb.Context(), func(ctx context.Context, _ *vips.Arena) error {
		var err error
		h, err = vips.ProbeRaw(ctx, buf)
		return err
	})
	return h, err
}

func TestProbeRawSynthetic(t *testing.T) {
	t.Parallel()
	h, err := probeRaw(t, readFixture(t, "dng/landscape.dng"))
	if err != nil {
		t.Fatal(err)
	}
	want := vips.RawHeader{
		Width: 192, Height: 128, RawWidth: 192, RawHeight: 128, Flip: 0, Colors: 3, DNG: true,
		Make: "xtandard", Model: "Synthetic DNG", Software: "testdata/gen", Lens: "Synthetic 35mm F5.6",
		ISO: 200, ShutterSeconds: 0.004, Aperture: 5.6, FocalLength: 35,
		Captured:   time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC),
		Thumbnails: []vips.RawThumbnail{{JPEG: true, Width: 96, Height: 64, Offset: h.Thumbnails[0].Offset, Length: h.Thumbnails[0].Length}},
	}
	approx := cmp.Comparer(func(a, b float64) bool { d := a - b; return d < 1e-4 && d > -1e-4 })
	if diff := cmp.Diff(want, h, approx); diff != "" {
		t.Errorf("ProbeRaw (-want +got):\n%s", diff)
	}

	p, err := probeRaw(t, readFixture(t, "dng/portrait-orientation-6.dng"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Flip != 6 || p.Width != 192 || p.Height != 128 {
		t.Errorf("portrait: flip %d, %dx%d; want 6, 192x128 (stored)", p.Flip, p.Width, p.Height)
	}
}

func TestProbeRawErrors(t *testing.T) {
	t.Parallel()
	dng := readFixture(t, "dng/landscape.dng")
	for _, tc := range []struct {
		name string
		data []byte
		kind errs.Kind
	}{
		{"not a raw", readFixture(t, "jpeg/baseline.jpg"), errs.KindUnsupported},
		{"garbage", []byte("II*\x00garbage garbage garbage garbage"), errs.KindCorrupt},
		{"header only", dng[:64], errs.KindCorrupt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := probeRaw(t, tc.data)
			if k := errs.KindOf(err); k != tc.kind {
				t.Errorf("kind = %v (%v), want %v", k, err, tc.kind)
			}
			if _, ok := errors.AsType[*vips.OpError](err); !ok {
				t.Errorf("error %v carries no OpError", err)
			}
		})
	}
}

// rawLoad demosaics data and returns the image size and bands.
func rawLoad(tb testing.TB, data []byte, o *vips.RawOptions) (w, h, bands int, interp vips.Interpretation, err error) {
	tb.Helper()
	buf := newBuffer(tb, data)
	defer buf.Release()
	err = tryJob(tb.Context(), func(ctx context.Context, a *vips.Arena) error {
		img, err := a.Add(vips.RawLoad(ctx, buf, o))
		if err != nil {
			return err
		}
		// Render it: the pixels must be readable.
		if _, err := img.WriteToMemory(ctx); err != nil {
			return err
		}
		w, h, bands, interp = img.Width(), img.Height(), img.Bands(), img.Interpretation()
		return nil
	})
	return w, h, bands, interp, err
}

func TestRawLoad(t *testing.T) {
	t.Parallel()
	dng := readFixture(t, "dng/landscape.dng")
	portrait := readFixture(t, "dng/portrait-orientation-6.dng")
	for _, tc := range []struct {
		name string
		data []byte
		o    vips.RawOptions
		w, h int
	}{
		{"full", dng, vips.RawOptions{}, 192, 128},
		{"half", dng, vips.RawOptions{HalfSize: true}, 96, 64},
		{"portrait stays stored", portrait, vips.RawOptions{}, 192, 128},
		{"at the limit", dng, vips.RawOptions{MaxPixels: 192 * 128}, 192, 128},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w, h, bands, interp, err := rawLoad(t, tc.data, &tc.o)
			if err != nil {
				t.Fatal(err)
			}
			if w != tc.w || h != tc.h || bands != 3 || interp != vips.InterpretationSRGB {
				t.Errorf("got %dx%d×%d %v, want %dx%d×3 srgb", w, h, bands, interp, tc.w, tc.h)
			}
		})
	}
}

func TestRawLoadErrors(t *testing.T) {
	t.Parallel()
	dng := readFixture(t, "dng/landscape.dng")
	for _, tc := range []struct {
		name     string
		canceled bool
		data     []byte
		o        vips.RawOptions
		kind     errs.Kind
	}{
		{"over the demosaic limit", false, dng, vips.RawOptions{MaxPixels: 192*128 - 1}, errs.KindLimitExceeded},
		{"truncated mosaic", false, dng[:len(dng)-20_000], vips.RawOptions{}, errs.KindCorrupt},
		{"not a raw", false, readFixture(t, "png/rgba8.png"), vips.RawOptions{}, errs.KindUnsupported},
		{"canceled", true, dng, vips.RawOptions{}, errs.KindCanceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			buf := newBuffer(t, tc.data)
			defer buf.Release()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.canceled {
				cancel()
			}
			err := vips.Run(t.Context(), func() error {
				img, err := vips.RawLoad(ctx, buf, &tc.o)
				img.Close()
				return err
			})
			if k := errs.KindOf(err); k != tc.kind {
				t.Errorf("kind = %v (%v), want %v", k, err, tc.kind)
			}
		})
	}
}

// TestRawLoadCancelMidway cancels while LibRaw demosaics a real 12 MP file:
// the progress callback stops it at its next stage.
func TestRawLoadCancelMidway(t *testing.T) {
	t.Parallel()
	f := rawfixtures.Files()[4] // pentax-k-50.dng, 16 MP
	buf := newBuffer(t, rawfixtures.Read(t, &f))
	defer buf.Release()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := vips.Run(t.Context(), func() error {
		img, err := vips.RawLoad(ctx, buf, nil)
		img.Close()
		return err
	})
	if k := errs.KindOf(err); k != errs.KindTimeout {
		t.Fatalf("kind = %v (%v), want timeout", k, err)
	}
	t.Logf("cancelled after %v", time.Since(start))
}

// TestProbeRawRealFiles probes every pinned camera sample: size,
// orientation, camera and a usable JPEG preview, header only.
func TestProbeRawRealFiles(t *testing.T) {
	t.Parallel()
	for _, f := range rawfixtures.Files() {
		t.Run(f.Kind, func(t *testing.T) {
			t.Parallel()
			h, err := probeRaw(t, rawfixtures.Read(t, &f))
			if err != nil {
				t.Fatal(err)
			}
			if got := h.Make + " " + h.Model; got != f.Camera {
				t.Errorf("camera %q, want %q", got, f.Camera)
			}
			if h.Width < 1000 || h.Height < 1000 || h.Colors != 3 {
				t.Errorf("header %dx%d, %d colours", h.Width, h.Height, h.Colors)
			}
			jpegs := 0
			for _, th := range h.Thumbnails {
				if th.JPEG {
					jpegs++
				}
			}
			if jpegs == 0 {
				t.Errorf("no JPEG thumbnails in %+v", h.Thumbnails)
			}
		})
	}
}

// TestProbeRawHeaderOnly: probing a 16 MP DNG allocates no libvips memory,
// leaves no LibRaw handle behind and costs a fraction of a millisecond
// (nothing is unpacked; the demosaic of the same file takes ~0.4–1 s). Not
// parallel: the counters are global.
func TestProbeRawHeaderOnly(t *testing.T) {
	f := rawfixtures.Files()[4] // pentax-k-50.dng
	buf := newBuffer(t, rawfixtures.Read(t, &f))
	defer buf.Release()
	before, live := vips.ReadMemStats(), vips.RawLive()
	const n = 20
	start := time.Now()
	job(t, func(ctx context.Context, _ *vips.Arena) error {
		for range n {
			if _, err := vips.ProbeRaw(ctx, buf); err != nil {
				return err
			}
		}
		return nil
	})
	per := time.Since(start) / n
	after := vips.ReadMemStats()
	if after.Mem != before.Mem || after.Allocs != before.Allocs || vips.RawLive() != live {
		t.Errorf("probe: tracked mem %d → %d B, allocs %d → %d, LibRaw handles %d → %d",
			before.Mem, after.Mem, before.Allocs, after.Allocs, live, vips.RawLive())
	}
	// Header only is asserted by the unchanged counters above, not by the
	// time per call (machine load decides it; BenchmarkProbeRaw has it).
	t.Logf("ProbeRaw %s: %v per call", f.Name, per)
}

// TestRawNoLeak demosaics and probes 200 times and checks that libvips'
// tracked memory and objects and the LibRaw handles and buffers the shim
// counts return to their baseline. Not parallel: the counters are global.
func TestRawNoLeak(t *testing.T) {
	const cycles = 200
	dng := readFixture(t, "dng/landscape.dng")
	cycle := func(i int) {
		buf := newBuffer(t, dng)
		defer buf.Release()
		job(t, func(ctx context.Context, a *vips.Arena) error {
			if _, err := vips.ProbeRaw(ctx, buf); err != nil {
				return err
			}
			img, err := a.Add(vips.RawLoad(ctx, buf, &vips.RawOptions{HalfSize: i%2 == 0}))
			if err != nil {
				return err
			}
			small, err := a.Add(img.Resize(ctx, 0.5, 0, vips.KernelLanczos3))
			if err != nil {
				return err
			}
			so := vips.DefaultSaveOptions()
			blob, err := small.Save(ctx, vips.FormatJPEG, &so)
			if err != nil {
				return err
			}
			blob.Release()
			return nil
		})
	}
	for i := range 4 {
		cycle(i)
	}
	before, live := leakcheck.Take(), vips.RawLive()
	for i := range cycles {
		cycle(i)
	}
	leakcheck.Check(t, before, leakcheck.Limits{
		Bytes: leakcheck.Default.Bytes, Allocs: -1,
		Objects: leakcheck.Default.Objects,
	}, "cycles")
	if liveAfter := vips.RawLive(); liveAfter != live {
		t.Errorf("after %d cycles: LibRaw handles+buffers %d → %d", cycles, live, liveAfter)
	}
}

// BenchmarkProbeRaw measures the header-only LibRaw open of real files.
func BenchmarkProbeRaw(b *testing.B) {
	for _, f := range rawfixtures.Files() {
		b.Run(f.Kind, func(b *testing.B) {
			buf := newBuffer(b, rawfixtures.Read(b, &f))
			defer buf.Release()
			err := vips.Run(b.Context(), func() error {
				for b.Loop() {
					if _, err := vips.ProbeRaw(b.Context(), buf); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				b.Fatal(err)
			}
		})
	}
}
