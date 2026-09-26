//go:build cgo

package exec_test

import (
	"encoding/base64"
	"encoding/binary"
	"image"
	"math"
	"slices"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/testutil/ssim"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// WP-64 request extras (dpi, style, enforce_thumbnail) and WP-62 page and
// frame selection through the executor.

const (
	svgLogo    = "svg/logo.svg"
	heicThumb  = "heic/thumbnail.heic" // 320x240 photo, 80x60 thumbnail of a different graphic
	avifThumb  = "avif/thumbnail.avif" // the same as AVIF
	staticGIF  = "gif/static.gif"      // 96x72
	tiffPages  = "tiff/multipage.tif"  // 160x120 photo, 96x72 graphic
	dssimClose = 0.02
	dssimFar   = 0.05
)

func dssim(tb testing.TB, a, b image.Image) float64 {
	tb.Helper()
	r, err := ssim.Compare(a, b)
	if err != nil {
		tb.Fatal(err)
	}
	return r.DSSIM
}

// readResolution is the resolution libvips reads back from an encoded
// image, in DPI.
func readResolution(tb testing.TB, data []byte) (x, y float64) {
	tb.Helper()
	buf := newBuffer(tb, data)
	defer buf.Release()
	err := vips.Run(tb.Context(), func() error {
		img, err := vips.Load(tb.Context(), buf, &vips.LoadOptions{})
		if err != nil {
			return err
		}
		defer img.Close()
		x, y = img.XRes()*25.4, img.YRes()*25.4
		return nil
	})
	if err != nil {
		tb.Fatal(err)
	}
	return x, y
}

// TestDPI: dpi replaces the resolution the savers write (JPEG JFIF, PNG
// pHYs, TIFF), with metadata stripped or not; without it the source's
// resolution is kept.
func TestDPI(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		options string
		out     transform.Format
		want    float64
	}{
		{"dpi:300", transform.FormatJPEG, 300},
		{"dpi:300/sm:0", transform.FormatJPEG, 300},
		{"dpi:144/rs:fit:100:100", transform.FormatPNG, 144},
		{"dpi:600", transform.FormatTIFF, 600},
	} {
		t.Run(tc.options+"/"+tc.out.String(), func(t *testing.T) {
			t.Parallel()
			o := process(t, jpegPhoto, tc.options, tc.out)
			x, y := readResolution(t, o.bytes)
			if math.Abs(x-tc.want) > 0.5 || math.Abs(y-tc.want) > 0.5 {
				t.Errorf("resolution %.2fx%.2f dpi, want %v", x, y, tc.want)
			}
		})
	}
	t.Run("unset keeps the source", func(t *testing.T) {
		t.Parallel()
		src, _ := readResolution(t, readFixture(t, jpegPhoto))
		o := process(t, jpegPhoto, "rs:fit:100:100", transform.FormatJPEG)
		if x, _ := readResolution(t, o.bytes); math.Abs(x-src) > 0.5 {
			t.Errorf("resolution %.2f dpi, want the source's %.2f", x, src)
		}
	})
}

func styleOption(css string) string {
	return "stl:" + base64.RawURLEncoding.EncodeToString([]byte(css))
}

// TestStyleSVG: style injects CSS into an SVG source before rasterisation
// (it beats the presentation attributes); external references in it are
// dropped by the sanitiser, so they change nothing; a raster source skips
// the option.
func TestStyleSVG(t *testing.T) {
	t.Parallel()
	plain := process(t, svgLogo, "", transform.FormatPNG)
	styled := process(t, svgLogo, styleOption("circle{fill:#e63946} path{fill:#e9c46a;stroke:#000;stroke-width:2}"), transform.FormatPNG)
	checkOutput(t, &goldenCase{out: transform.FormatPNG, w: 96, h: 72}, &styled)
	golden.Compare(t, "exec/svg-style", styled.img, golden.Options{})
	if d := dssim(t, plain.img, styled.img); d < dssimFar {
		t.Errorf("style changed nothing: dssim %.4f", d)
	}
	if len(styled.res.Skipped) != 0 {
		t.Errorf("Skipped = %v", styled.res.Skipped)
	}

	// External references in the CSS are neutralised by the sanitiser:
	// the @import disappears and url(https://…) becomes none, exactly as
	// if the style had said so.
	hostile := process(t, svgLogo, styleOption(`@import "https://evil.example/x.css";rect{fill:url(https://evil.example/p.svg#a)}`), transform.FormatPNG)
	none := process(t, svgLogo, styleOption("rect{fill:none}"), transform.FormatPNG)
	if d := dssim(t, none.img, hostile.img); d > 0.001 {
		t.Errorf("sanitised style differs from rect{fill:none}: dssim %.4f", d)
	}

	raster := process(t, jpegPhoto, styleOption("rect{fill:red}"), transform.FormatPNG)
	if !slices.Contains(raster.res.Skipped, "style") {
		t.Errorf("Skipped = %v, want style for a raster source", raster.res.Skipped)
	}
}

// thumbnailRef decodes the embedded HEIF thumbnail of a fixture.
func thumbnailRef(tb testing.TB, fixture string) image.Image {
	tb.Helper()
	return loadRef(tb, readFixture(tb, fixture), &vips.LoadOptions{HEIFThumbnail: true})
}

// loadRef decodes data with opts as 8-bit sRGBA (frames stacked).
func loadRef(tb testing.TB, data []byte, opts *vips.LoadOptions) image.Image {
	tb.Helper()
	buf := newBuffer(tb, data)
	defer buf.Release()
	var png []byte
	err := vips.Run(tb.Context(), func() error {
		img, err := vips.Load(tb.Context(), buf, opts)
		if err != nil {
			return err
		}
		defer img.Close()
		out, err := img.Save(tb.Context(), vips.FormatPNG, new(vips.DefaultSaveOptions()))
		if err != nil {
			return err
		}
		defer out.Release()
		png = slices.Clone(out.Bytes())
		return nil
	})
	if err != nil {
		tb.Fatal(err)
	}
	img, _ := decode(tb, png)
	return img
}

// TestEnforceThumbnailHEIF: enforce_thumbnail always decodes the embedded
// thumbnail of a HEIF/AVIF (its pixels differ from the image's in the
// fixture), whatever the target size; without it the thumbnail is used only
// when it is large enough.
func TestEnforceThumbnailHEIF(t *testing.T) {
	t.Parallel()
	for _, fixture := range []string{heicThumb, avifThumb} {
		thumb := thumbnailRef(t, fixture)
		for _, tc := range []struct {
			options string
			thumb   bool
			w, h    int
		}{
			{"eth:1", true, 80, 60},
			{"eth:1/rs:fit:160:120", true, 80, 60}, // never enlarged
			{"eth:1/rs:fit:40:30", true, 40, 30},
			{"eth:0/rs:fit:160:120", false, 160, 120},
			{"rs:fit:160:120", false, 160, 120},
			{"eth:0/rs:fit:40:30", true, 40, 30}, // large enough: used anyway
		} {
			t.Run(fixture+"/"+tc.options, func(t *testing.T) {
				t.Parallel()
				o := process(t, fixture, tc.options, transform.FormatPNG)
				if o.res.Thumbnail != tc.thumb || o.res.Width != tc.w || o.res.Height != tc.h {
					t.Fatalf("thumbnail %v %dx%d, want %v %dx%d", o.res.Thumbnail, o.res.Width, o.res.Height, tc.thumb, tc.w, tc.h)
				}
				if tc.w == 80 {
					if d := dssim(t, thumb, o.img); d > dssimClose {
						t.Errorf("not the embedded thumbnail: dssim %.4f", d)
					}
				}
			})
		}
	}
}

// jpegWithEXIFThumbnail returns main with an Exif APP1 segment holding
// thumb as its IFD1 JPEG thumbnail.
func jpegWithEXIFThumbnail(main, thumb []byte) []byte {
	bo := binary.LittleEndian
	t := []byte("II*\x00")
	t = bo.AppendUint32(t, 8)
	entry := func(b []byte, tag, typ uint16, v uint32) []byte {
		b = bo.AppendUint16(b, tag)
		b = bo.AppendUint16(b, typ)
		b = bo.AppendUint32(b, 1)
		if typ == 3 {
			return append(bo.AppendUint16(b, uint16(v)), 0, 0) //nolint:gosec // G115: test values are small
		}
		return bo.AppendUint32(b, v)
	}
	t = bo.AppendUint16(t, 1)
	t = entry(t, 0x0112, 3, 1) // orientation
	t = bo.AppendUint32(t, 26) // IFD1
	t = bo.AppendUint16(t, 3)
	thumbOff := uint32(26 + 2 + 3*12 + 4)
	t = entry(t, 0x0103, 3, 6)
	t = entry(t, 0x0201, 4, thumbOff)
	t = entry(t, 0x0202, 4, uint32(len(thumb))) //nolint:gosec // G115: test values are small
	t = bo.AppendUint32(t, 0)
	t = append(t, thumb...)
	seg := append([]byte("Exif\x00\x00"), t...)
	app1 := binary.BigEndian.AppendUint16([]byte{0xFF, 0xE1}, uint16(len(seg)+2)) //nolint:gosec // G115: test values are small
	return slices.Concat(main[:2], app1, seg, main[2:])
}

// TestEnforceThumbnailJPEG: with enforce_thumbnail a JPEG is processed from
// its Exif thumbnail (here a different picture, the static GIF) when the
// thumbnail is at least as large as the result needs; otherwise from the
// image.
func TestEnforceThumbnailJPEG(t *testing.T) {
	t.Parallel()
	thumb := process(t, staticGIF, "", transform.FormatJPEG) // 96x72, same 4:3 as the photo
	data := jpegWithEXIFThumbnail(readFixture(t, jpegPhoto), thumb.bytes)
	want := process(t, staticGIF, "rs:fit:64:48", transform.FormatPNG)
	photo := process(t, jpegPhoto, "rs:fit:64:48", transform.FormatPNG)
	for _, tc := range []struct {
		options string
		thumb   bool
	}{
		{"eth:1/rs:fit:64:48", true},
		{"eth:1/rs:fill:48:48", true},
		{"rs:fit:64:48", false},
		{"eth:1/rs:fit:200:150", false}, // thumbnail too small
		{"eth:1", false},                // full size: never from the thumbnail
		{"eth:1/c:300:200", false},      // absolute crop larger than the thumbnail
	} {
		t.Run(tc.options, func(t *testing.T) {
			t.Parallel()
			o := processData(t, data, tc.options, transform.FormatPNG)
			if o.res.Thumbnail != tc.thumb {
				t.Fatalf("Thumbnail = %v, want %v", o.res.Thumbnail, tc.thumb)
			}
			if tc.options == "eth:1/rs:fit:64:48" || tc.options == "rs:fit:64:48" {
				ref := photo.img
				if tc.thumb {
					ref = want.img
				}
				if d := dssim(t, ref, o.img); d > dssimClose {
					t.Errorf("dssim %.4f against the expected picture", d)
				}
			}
		})
	}
}

// TestPagesFrames is the WP-62 matrix on real files: animated GIF and WebP
// keep an animation from `page` on for `pages` frames when the output can
// animate; otherwise `page` selects one frame and `pages` stacks frames
// vertically. A multi-page TIFF selects and stacks its pages.
func TestPagesFrames(t *testing.T) {
	t.Parallel()
	for _, src := range []string{gifAnim, webpAnim} {
		data := readFixture(t, src)
		frame := func(page, n int) image.Image {
			return loadRef(t, data, &vips.LoadOptions{Page: page, NPages: n})
		}
		for _, tc := range []struct {
			options      string
			out          transform.Format
			frames, h    int
			page, npages int // reference frames; npages 0 = no pixel check
		}{
			{"pg:2", transform.FormatGIF, 4, 72, 0, 0},
			{"pgs:3", transform.FormatWebP, 3, 72, 0, 0},
			{"pg:1/pgs:2", transform.FormatGIF, 2, 72, 0, 0},
			{"pg:4/pgs:1", transform.FormatGIF, 1, 72, 4, 1},
			{"pg:3", transform.FormatPNG, 1, 72, 3, 1},
			{"pg:5", transform.FormatJPEG, 1, 72, 5, 1},
			{"pgs:2", transform.FormatPNG, 1, 144, 0, 2},
			{"pg:4/pgs:-1", transform.FormatPNG, 1, 144, 4, 2},
			{"pg:2/pgs:3/da:1", transform.FormatWebP, 1, 216, 2, 3},
		} {
			t.Run(src+"/"+tc.options+"/"+tc.out.String(), func(t *testing.T) {
				t.Parallel()
				o := process(t, src, tc.options, tc.out)
				if o.res.Frames != tc.frames || o.frames != tc.frames || o.res.Width != 96 || o.res.Height != tc.h {
					t.Fatalf("%d frames (%d decoded) of %dx%d, want %d of 96x%d", o.res.Frames, o.frames, o.res.Width, o.res.Height, tc.frames, tc.h)
				}
				if tc.npages == 0 {
					return
				}
				tol := dssimClose
				if tc.out == transform.FormatJPEG || tc.out == transform.FormatGIF {
					tol = 0.04 // lossy or palette re-encode
				}
				if d := dssim(t, frame(tc.page, tc.npages), o.img); d > tol {
					t.Errorf("not frames %d..%d: dssim %.4f", tc.page, tc.page+tc.npages-1, d)
				}
			})
		}
	}
	for _, tc := range []struct {
		options string
		w, h    int
	}{
		{"pg:1", 96, 72},
		{"pgs:-1", 160, 192},
		{"pg:1/pgs:5", 96, 72},
		{"pgs:2/rs:fit:80:0", 80, 96},
	} {
		t.Run(tiffPages+"/"+tc.options, func(t *testing.T) {
			t.Parallel()
			o := process(t, tiffPages, tc.options, transform.FormatPNG)
			if o.res.Width != tc.w || o.res.Height != tc.h {
				t.Fatalf("%dx%d, want %dx%d", o.res.Width, o.res.Height, tc.w, tc.h)
			}
			if tc.options == "pgs:-1" {
				// Left-aligned, the gap beside the narrower page transparent.
				golden.Compare(t, "exec/tiff-pages-all", o.img, golden.Options{})
			}
		})
	}
}
