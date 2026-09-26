//go:build cgo

package vips_test

import (
	"context"
	"errors"
	"image"
	"os"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// magickPolicy is the ImageMagick policy of the base image (docker/base).
const magickPolicy = "/opt/xtandard/etc/ImageMagick-7/policy.xml"

func TestProbePDF(t *testing.T) {
	t.Parallel()
	data := readFixture(t, "pdf/pages.pdf")
	for _, tc := range []struct {
		name string
		dpi  float64
		w, h int
	}{
		{name: "default", w: 200, h: 150},
		{name: "72", dpi: 72, w: 200, h: 150},
		{name: "144", dpi: 144, w: 400, h: 300},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			buf := newBuffer(t, data)
			defer buf.Release()
			job(t, func(ctx context.Context, _ *vips.Arena) error {
				h, err := vips.ProbeDensity(ctx, buf, tc.dpi)
				if err != nil {
					return err
				}
				if h.Format != vips.FormatPDF || h.Width != tc.w || h.Height != tc.h || h.Pages != 3 {
					t.Errorf("header = %s %dx%d, %d pages; want pdf %dx%d, 3 pages",
						h.Format, h.Width, h.Height, h.Pages, tc.w, tc.h)
				}
				return nil
			})
		})
	}
}

func TestLoadPDF(t *testing.T) {
	t.Parallel()
	data := readFixture(t, "pdf/pages.pdf")
	for _, tc := range []struct {
		name  string
		opts  vips.LoadOptions
		w, h  int
		alpha float64 // alpha of the unpainted top-left corner of page 1
	}{
		{name: "page 0", w: 200, h: 150, alpha: 255},
		{name: "page 1", opts: vips.LoadOptions{Page: 1}, w: 120, h: 160, alpha: -1},
		{name: "page 1 scale 2", opts: vips.LoadOptions{Page: 1, Scale: 2}, w: 240, h: 320, alpha: -1},
		{name: "dpi 144", opts: vips.LoadOptions{Density: 144}, w: 400, h: 300, alpha: 255},
		{name: "all pages", opts: vips.LoadOptions{NPages: -1}, w: 200, h: 460, alpha: 255},
		{
			name: "transparent", opts: vips.LoadOptions{Background: []float64{0, 0, 0, 0}},
			w: 200, h: 150, alpha: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			buf := newBuffer(t, data)
			defer buf.Release()
			job(t, func(ctx context.Context, a *vips.Arena) error {
				img, err := a.Add(vips.Load(ctx, buf, &tc.opts))
				if err != nil {
					return err
				}
				if img.Width() != tc.w || img.Height() != tc.h || img.Bands() != 4 {
					t.Errorf("loaded %dx%d×%d, want %dx%d×4", img.Width(), img.Height(), img.Bands(), tc.w, tc.h)
				}
				if tc.alpha < 0 {
					return nil
				}
				px, err := a.Add(img.ExtractArea(ctx, image.Rect(0, 0, 1, 1)))
				if err != nil {
					return err
				}
				pix, err := px.WriteToMemory(ctx)
				if err != nil {
					return err
				}
				if got := float64(pix[3]); got != tc.alpha {
					t.Errorf("corner alpha = %g, want %g", got, tc.alpha)
				}
				return nil
			})
		})
	}
}

func TestPDFPassword(t *testing.T) {
	t.Parallel()
	buf := newBuffer(t, readFixture(t, "pdf/password.pdf"))
	defer buf.Release()
	err := tryJob(t.Context(), func(ctx context.Context, _ *vips.Arena) error {
		_, err := vips.Probe(ctx, buf)
		return err
	})
	if errs.KindOf(err) != errs.KindUnsupported {
		t.Fatalf("probe: %v (kind %s), want unsupported", err, errs.KindOf(err))
	}
	e, ok := errors.AsType[*errs.Error](err)
	if !ok || e.PublicMessage() != "password-protected documents are not supported" {
		t.Errorf("public message of %v", err)
	}
}

func TestLoadPSD(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"psd/layered.psd", "psd/no-composite.psd", "psd/psb-layered.psb"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			buf := newBuffer(t, readFixture(t, name))
			defer buf.Release()
			job(t, func(ctx context.Context, a *vips.Arena) error {
				img, err := a.Add(vips.Load(ctx, buf, nil))
				if err != nil {
					return err
				}
				if img.Width() != 96 || img.Height() != 72 {
					t.Errorf("loaded %dx%d, want the 96x72 canvas", img.Width(), img.Height())
				}
				// The sticker layer is a 40x30 flat graphic at (12, 10): the
				// composite (stored, or flattened by ImageMagick) shows it.
				layer, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Page: 2}))
				if err != nil {
					return err
				}
				if layer.Width() != 40 || layer.Height() != 30 {
					t.Errorf("layer 2 is %dx%d, want 40x30", layer.Width(), layer.Height())
				}
				c, l := pixelAt(ctx, t, a, img, 12+5, 10+5), pixelAt(ctx, t, a, layer, 5, 5)
				for i := range 3 {
					if d := int(c[i]) - int(l[i]); d > 2 || d < -2 {
						t.Errorf("composite at the sticker = %v, layer = %v", c[:3], l[:3])
						break
					}
				}
				return nil
			})
		})
	}
}

// TestMagickPSDOnly checks both defences around ImageMagick: the shim never
// hands anything but a PSD/PSB signature to it, and the base image's
// policy.xml refuses every other coder even when ImageMagick is called
// directly.
func TestMagickPSDOnly(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"bmp/rgb24.bmp", "png/rgba8.png", "gif/static.gif", "svg/logo.svg", "pdf/pages.pdf"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data := readFixture(t, name)
			buf := newBuffer(t, data)
			defer buf.Release()
			err := tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
				img, err := a.Add(vips.LoadWith(ctx, "VipsForeignLoadMagick7Buffer", buf, nil))
				if err != nil {
					return err
				}
				_, err = img.WriteToMemory(ctx)
				return err
			})
			switch {
			case err != nil:
				t.Logf("magick refused %s: %v", name, err)
			case fileExists(magickPolicy):
				t.Errorf("ImageMagick decoded %s despite %s", name, magickPolicy)
			default:
				t.Logf("no %s here (not the base image): ImageMagick's policy is not checked", magickPolicy)
			}
		})
	}
	t.Run("bmp through detection", func(t *testing.T) {
		t.Parallel()
		// BMP has no libvips loader: libvips' own dispatch would hand it to
		// ImageMagick (lowest priority), which the shim refuses.
		buf := newBuffer(t, readFixture(t, "bmp/rgb24.bmp"))
		defer buf.Release()
		err := tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
			_, err := a.Add(vips.Load(ctx, buf, nil))
			return err
		})
		if errs.KindOf(err) != errs.KindUnsupported {
			t.Errorf("load bmp: %v (kind %s), want unsupported", err, errs.KindOf(err))
		}
	})
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// pixelAt reads the pixel at (x, y) of img.
func pixelAt(ctx context.Context, tb testing.TB, a *vips.Arena, img *vips.Image, x, y int) []byte {
	tb.Helper()
	px, err := a.Add(img.ExtractArea(ctx, image.Rect(x, y, x+1, y+1)))
	if err != nil {
		tb.Fatal(err)
	}
	pix, err := px.WriteToMemory(ctx)
	if err != nil {
		tb.Fatal(err)
	}
	return pix
}

// TestNoLeakPDFPSD is TestNoLeak for the M2 loaders: PDF pages rendered
// scaled (PDFium) and PSD composites (ImageMagick), encoded to JPEG. The
// libraries' own heaps are not tracked by libvips; the images, regions and
// source buffers are. Not parallel: the counters are process-wide.
func TestNoLeakPDFPSD(t *testing.T) {
	const (
		cycles      = 600
		slackBytes  = 1 << 20
		slackAllocs = 64
		slackObjs   = 16
	)
	vips.SetLeakCheck(true)
	defer vips.SetLeakCheck(false)

	pdf, psd := readFixture(t, "pdf/pages.pdf"), readFixture(t, "psd/no-composite.psd")
	so := vips.DefaultSaveOptions()
	cycle := func(i int) {
		data, opts := pdf, &vips.LoadOptions{Page: i % 3, Scale: 1.5, Access: vips.AccessSequential}
		if i%2 == 1 {
			data, opts = psd, &vips.LoadOptions{Access: vips.AccessSequential}
		}
		buf := newBuffer(t, data)
		defer buf.Release()
		job(t, func(ctx context.Context, a *vips.Arena) error {
			img, err := a.Add(vips.Load(ctx, buf, opts))
			if err != nil {
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
	for i := range 4 {
		cycle(i)
	}
	before, objects := vips.ReadMemStats(), vips.LiveObjects()
	for i := range cycles {
		cycle(i)
	}
	after, objectsAfter := vips.ReadMemStats(), vips.LiveObjects()
	if after.Mem-before.Mem > slackBytes || after.Allocs-before.Allocs > slackAllocs ||
		after.Files != before.Files || objectsAfter-objects > slackObjs {
		t.Errorf("after %d cycles: mem %d → %d B, allocs %d → %d, files %d → %d, objects %d → %d",
			cycles, before.Mem, after.Mem, before.Allocs, after.Allocs, before.Files, after.Files,
			objects, objectsAfter)
	}
	t.Logf("after %d cycles: mem %+d B, allocs %+d, objects %+d",
		cycles, after.Mem-before.Mem, after.Allocs-before.Allocs, objectsAfter-objects)
}
