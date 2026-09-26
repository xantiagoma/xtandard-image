//go:build cgo

package exec_test

import (
	"image"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/testutil/ssim"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

const (
	pdfPages = "pdf/pages.pdf" // 3 pages: 200×150, 120×160, 200×150 pt
	psdFile  = "psd/layered.psd"
)

// TestGoldenPDFPSD renders PDF pages and Photoshop files through the whole
// pipeline and compares them with testdata/golden/exec/<name>.png.
func TestGoldenPDFPSD(t *testing.T) {
	t.Parallel()
	for _, tc := range []goldenCase{
		// Page 0 and page N at two sizes each: rendered at the target size.
		{name: "pdf-page0-w100", fixture: pdfPages, options: "rs:fit:100:0", out: transform.FormatPNG, w: 100, h: 75},
		{name: "pdf-page0-w500", fixture: pdfPages, options: "rs:fit:500:0", out: transform.FormatPNG, w: 500, h: 375},
		{name: "pdf-page1-w60", fixture: pdfPages, options: "pg:1/rs:fit:60:0", out: transform.FormatPNG, w: 60, h: 80},
		{name: "pdf-page1-h400", fixture: pdfPages, options: "pg:1/rs:fit:0:400", out: transform.FormatPNG, w: 300, h: 400},
		// Natural size at XI_FORMATS_PDF_DPI (72: one pixel per point).
		{name: "pdf-page2-natural", fixture: pdfPages, options: "pg:2", out: transform.FormatJPEG, w: 200, h: 150},
		// All pages stacked vertically, left-aligned (the 120 pt page 1 is
		// padded on the right with the background).
		{name: "pdf-pages-all", fixture: pdfPages, options: "pgs:-1/rs:fit:0:230", out: transform.FormatPNG, w: 100, h: 230},
		{name: "pdf-pages-from-1", fixture: pdfPages, options: "pg:1/pgs:2", out: transform.FormatPNG, w: 200, h: 310},
		// Page background: the request's colour instead of white.
		{name: "pdf-background", fixture: pdfPages, options: "bg:204:0:0/rs:fit:100:0", out: transform.FormatJPEG, w: 100, h: 75},
		// Photoshop: the stored composite, the layers flattened by
		// ImageMagick when there is none, PSB, and a layer by index.
		{name: "psd-composite", fixture: psdFile, options: "rs:fit:64:0", out: transform.FormatPNG, w: 64, h: 48},
		{name: "psd-no-composite", fixture: "psd/no-composite.psd", options: "rs:fit:64:0", out: transform.FormatPNG, w: 64, h: 48},
		{name: "psb-composite", fixture: "psd/psb-layered.psb", options: "rs:fit:48:0", out: transform.FormatPNG, w: 48, h: 36},
		{name: "psd-layer2", fixture: psdFile, options: "pg:2", out: transform.FormatPNG, w: 40, h: 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := process(t, tc.fixture, tc.options, tc.out)
			checkOutput(t, &tc, &o)
			golden.Compare(t, "exec/"+tc.name, o.img, golden.Options{Tolerance: tc.tolerance})
		})
	}
}

// TestPSDCompositeStrategies checks that a file without a merged composite
// renders like the one with it: ImageMagick flattens the layers.
func TestPSDCompositeStrategies(t *testing.T) {
	t.Parallel()
	with := process(t, psdFile, "", transform.FormatPNG)
	without := process(t, "psd/no-composite.psd", "", transform.FormatPNG)
	res, err := ssim.Compare(with.img, without.img)
	if err != nil {
		t.Fatal(err)
	}
	if res.DSSIM > 0.001 {
		t.Errorf("flattened layers differ from the stored composite: DSSIM %.5f", res.DSSIM)
	}
}

// TestPDFCrisp checks that a page is rendered at the target size rather
// than rendered small and enlarged: the result matches a render at 8× the
// resolution downscaled, and is much closer to it than the upscaled
// natural-size render is.
func TestPDFCrisp(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, options string
		page, w, h    int
	}{
		{name: "page0", options: "rs:fit:800:0", page: 0, w: 800, h: 600},
		{name: "page1", options: "pg:1/rs:fit:480:0", page: 1, w: 480, h: 640},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := process(t, pdfPages, tc.options, transform.FormatPNG)
			ref := renderPDF(t, tc.page, 8*72, tc.w)
			naive := renderPDF(t, tc.page, 72, tc.w)
			crisp, err := ssim.Compare(ref, got.img)
			if err != nil {
				t.Fatal(err)
			}
			blurry, err := ssim.Compare(ref, naive)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("DSSIM against a 576 dpi render: pipeline %.5f, upscaled 72 dpi render %.5f", crisp.DSSIM, blurry.DSSIM)
			if crisp.DSSIM > 0.01 || crisp.DSSIM*3 > blurry.DSSIM {
				t.Errorf("pipeline DSSIM %.5f, upscaled render %.5f: not rendered at the target size",
					crisp.DSSIM, blurry.DSSIM)
			}
		})
	}
}

// renderPDF renders page at dpi on white and resizes it to width w.
func renderPDF(tb testing.TB, page int, dpi float64, w int) image.Image {
	tb.Helper()
	buf := newBuffer(tb, readFixture(tb, pdfPages))
	defer buf.Release()
	var png []byte
	err := vips.Run(tb.Context(), func() error {
		var a vips.Arena
		defer a.Close()
		ctx := tb.Context()
		img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Page: page, Density: dpi, Access: vips.AccessRandom}))
		if err != nil {
			return err
		}
		if img, err = a.Add(img.ExtractBand(ctx, 0, 3)); err != nil {
			return err
		}
		s := float64(w) / float64(img.Width())
		if img, err = a.Add(img.Resize(ctx, s, s, vips.KernelLanczos3)); err != nil {
			return err
		}
		so := vips.DefaultSaveOptions()
		blob, err := img.Save(ctx, vips.FormatPNG, &so)
		if err != nil {
			return err
		}
		defer blob.Release()
		png = append([]byte(nil), blob.Bytes()...)
		return nil
	})
	if err != nil {
		tb.Fatal(err)
	}
	img, _ := decode(tb, png)
	return img
}

// TestPDFTransparency: a request that names an alpha-capable format and no
// background keeps the page's unpainted areas transparent; otherwise pages
// are opaque (white, or the request's background).
func TestPDFTransparency(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, options string
		out           transform.Format
		corner        [4]uint8 // top-left pixel: page 0's unpainted margin
		tolerance     int
	}{
		{name: "negotiated png is white", options: "rs:fit:100:0", out: transform.FormatPNG, corner: [4]uint8{255, 255, 255, 255}},
		{name: "explicit png is transparent", options: "f:png/rs:fit:100:0", out: transform.FormatPNG, corner: [4]uint8{0, 0, 0, 0}},
		{name: "explicit webp is transparent", options: "f:webp/rs:fit:100:0", out: transform.FormatWebP, corner: [4]uint8{0, 0, 0, 0}},
		{name: "explicit jpeg is white", options: "f:jpg/rs:fit:100:0", out: transform.FormatJPEG, corner: [4]uint8{255, 255, 255, 255}, tolerance: 16},
		{name: "background on jpeg", options: "bg:0:0:255/rs:fit:100:0", out: transform.FormatJPEG, corner: [4]uint8{0, 0, 255, 255}, tolerance: 16},
		{name: "background wins", options: "f:png/bg:0:0:255/rs:fit:100:0", out: transform.FormatPNG, corner: [4]uint8{0, 0, 255, 255}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := process(t, pdfPages, tc.options, tc.out)
			nrgba, ok := o.img.(*image.NRGBA)
			if !ok {
				t.Fatalf("decoded %T", o.img)
			}
			c := nrgba.NRGBAAt(0, 0)
			got := [4]uint8{c.R, c.G, c.B, c.A}
			if got[3] == 0 {
				got = [4]uint8{} // colour under full transparency is irrelevant
			}
			for i := range 4 {
				if d := int(got[i]) - int(tc.corner[i]); d > max(tc.tolerance, 3) || d < -max(tc.tolerance, 3) {
					t.Errorf("corner = %v, want %v", got, tc.corner)
					break
				}
			}
		})
	}
}
