//go:build cgo

package encode_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/codec/bmp"
	"github.com/xantiagoma/xtandard-image/internal/codec/encode"
	"github.com/xantiagoma/xtandard-image/internal/codec/ico"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// These tests need libvips from the base image: run them with
// `task docker:test`. HEVC encoding is enabled in libvips so that the
// encoder's own HEICEncode gate is what the gate test exercises.
func TestMain(m *testing.M) {
	cfg := vips.DefaultConfig()
	cfg.HEICEncode = true
	if err := vips.Init(&cfg); err != nil {
		_, _ = os.Stderr.WriteString("vips init: " + err.Error() + "\n")
		os.Exit(1)
	}
	code := m.Run()
	vips.Shutdown()
	os.Exit(code)
}

func readFixture(tb testing.TB, name string) []byte {
	tb.Helper()
	b, err := os.ReadFile(golden.Fixture(tb, name))
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

func newBuffer(tb testing.TB, data []byte) *vips.Buffer {
	tb.Helper()
	buf, err := vips.NewBufferFromReader(bytes.NewReader(data), 1<<30)
	if err != nil {
		tb.Fatalf("NewBufferFromReader: %v", err)
	}
	tb.Cleanup(buf.Release)
	return buf
}

func newEncoder(tb testing.TB, mutate func(*encode.Config)) *encode.Encoder {
	tb.Helper()
	cfg := encode.DefaultConfig()
	if mutate != nil {
		mutate(&cfg)
	}
	e, err := encode.New(&cfg)
	if err != nil {
		tb.Fatal(err)
	}
	return e
}

// output is an encode result copied out of the job.
type output struct {
	data []byte
	res  encode.Result
}

// encodeData loads data (all frames with nPages -1) and encodes it.
func encodeData(ctx context.Context, e *encode.Encoder, data []byte, nPages int, t *encode.Target,
	prep func(context.Context, *vips.Arena, *vips.Image) (*vips.Image, error),
) (output, error) {
	var out output
	buf, err := vips.NewBufferFromReader(bytes.NewReader(data), 1<<30)
	if err != nil {
		return out, err
	}
	defer buf.Release()
	err = vips.Run(ctx, func() error {
		var a vips.Arena
		defer a.Close()
		img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{NPages: nPages}))
		if err != nil {
			return err
		}
		if prep != nil {
			if img, err = prep(ctx, &a, img); err != nil {
				return err
			}
		}
		res, err := e.Encode(ctx, img, t)
		if err != nil {
			return err
		}
		defer res.Blob.Release()
		out.data = append([]byte(nil), res.Blob.Bytes()...)
		out.res = *res
		out.res.Blob = nil
		return nil
	})
	return out, err
}

func mustEncode(tb testing.TB, e *encode.Encoder, data []byte, nPages int, t *encode.Target,
	prep func(context.Context, *vips.Arena, *vips.Image) (*vips.Image, error),
) output {
	tb.Helper()
	out, err := encodeData(tb.Context(), e, data, nPages, t, prep)
	if err != nil {
		tb.Fatalf("encode %s: %v", t.Format, err)
	}
	return out
}

// decoded is an image read back with libvips as 8-bit sRGB pixels.
type decoded struct {
	header vips.Header
	fields []vips.MetaField
	text   map[vips.MetaField]string
	blobs  map[vips.MetaField][]byte
	pix    []byte // first frame, sRGB uchar, bands as below
	bands  int
}

// decode loads data with libvips (all frames) and reads metadata and pixels.
func decode(tb testing.TB, data []byte) decoded {
	tb.Helper()
	buf := newBuffer(tb, data)
	var d decoded
	ctx := tb.Context()
	err := vips.Run(ctx, func() error {
		var err error
		if d.header, err = vips.Probe(ctx, buf); err != nil {
			return err
		}
		var a vips.Arena
		defer a.Close()
		img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{NPages: -1}))
		if err != nil {
			return err
		}
		d.fields = img.Fields()
		d.text = map[vips.MetaField]string{}
		d.blobs = map[vips.MetaField][]byte{}
		for _, f := range d.fields {
			if s, ok := img.Text(f); ok {
				d.text[f] = s
			}
			if b, ok := img.Blob(f); ok {
				d.blobs[f] = b
			}
		}
		d.pix, d.bands, err = srgbPixels(ctx, &a, img)
		return err
	})
	if err != nil {
		tb.Fatalf("decode: %v", err)
	}
	return d
}

// srgbPixels returns the first frame of img as 8-bit sRGB.
func srgbPixels(ctx context.Context, a *vips.Arena, img *vips.Image) ([]byte, int, error) {
	var err error
	if img.Pages() > 1 {
		if img, err = a.Add(img.ExtractArea(ctx, image.Rect(0, 0, img.Width(), img.PageHeight()))); err != nil {
			return nil, 0, err
		}
	}
	if img.Interpretation() != vips.InterpretationSRGB {
		if img, err = a.Add(img.Colourspace(ctx, vips.InterpretationSRGB)); err != nil {
			return nil, 0, err
		}
	}
	if img.BandFormat() != vips.BandFormatUchar {
		if img, err = a.Add(img.Cast(ctx, vips.BandFormatUchar)); err != nil {
			return nil, 0, err
		}
	}
	pix, err := img.WriteToMemory(ctx)
	return pix, img.Bands(), err
}

// onWhite returns 3-band pixels: RGB as is, RGBA composited onto white (so
// colour under transparent pixels, which encoders may drop, is ignored).
func onWhite(p []byte, bands int) []byte {
	if bands == 3 {
		return p
	}
	out := make([]byte, 0, len(p)/bands*3)
	for i := 0; i+bands <= len(p); i += bands {
		al := 1.0
		if bands == 4 || bands == 2 {
			al = float64(p[i+bands-1]) / 255
		}
		for c := range 3 {
			v := p[i+min(c, bands-2)]
			if bands == 1 {
				v = p[i]
			}
			out = append(out, byte(float64(v)*al+255*(1-al)+0.5))
		}
	}
	return out
}

// meanAbsDiff compares two pixel buffers of the same geometry after
// compositing onto white.
func meanAbsDiff(tb testing.TB, a []byte, ab int, b []byte, bb int) float64 {
	tb.Helper()
	x, y := onWhite(a, ab), onWhite(b, bb)
	if len(x) != len(y) {
		tb.Fatalf("pixel counts differ: %d vs %d", len(x)/3, len(y)/3)
	}
	var sum float64
	for i := range x {
		d := int(x[i]) - int(y[i])
		if d < 0 {
			d = -d
		}
		sum += float64(d)
	}
	return sum / float64(len(x))
}

func vipsFormatOf(f transform.Format) vips.Format {
	switch f {
	case transform.FormatJPEG:
		return vips.FormatJPEG
	case transform.FormatPNG:
		return vips.FormatPNG
	case transform.FormatWebP:
		return vips.FormatWebP
	case transform.FormatAVIF:
		return vips.FormatAVIF
	case transform.FormatHEIC:
		return vips.FormatHEIF
	case transform.FormatJXL:
		return vips.FormatJXL
	case transform.FormatGIF:
		return vips.FormatGIF
	case transform.FormatTIFF:
		return vips.FormatTIFF
	default:
		return vips.FormatUnknown
	}
}

// TestRoundTrip encodes a photo and an RGBA graphic to every libvips output
// format and reads them back: format, geometry and pixels must survive.
func TestRoundTrip(t *testing.T) {
	t.Parallel()
	e := newEncoder(t, func(c *encode.Config) { c.HEICEncode = true })
	type input struct {
		name  string
		file  string
		w, h  int
		alpha bool
	}
	photo := input{"photo", "jpeg/baseline.jpg", 320, 240, false}
	rgba := input{"rgba", "png/rgba8.png", 128, 96, true}
	for _, tc := range []struct {
		format   transform.Format
		opts     encode.Options
		keeps    bool    // alpha survives
		tol      float64 // mean abs difference per sample
		alphaTol float64 // tol for the RGBA input when different
		contains string  // content type
		quality  int
	}{
		{format: transform.FormatJPEG, tol: 6, contains: "image/jpeg", quality: 80},
		{format: transform.FormatPNG, keeps: true, tol: 0.01, contains: "image/png"},
		{format: transform.FormatPNG, opts: encode.Options{Quantize: true}, keeps: true, tol: 8, contains: "image/png"},
		{format: transform.FormatWebP, keeps: true, tol: 6, contains: "image/webp", quality: 80},
		{format: transform.FormatWebP, opts: encode.Options{Lossless: new(true)}, keeps: true, tol: 0.01, contains: "image/webp"},
		{format: transform.FormatAVIF, keeps: true, tol: 6, contains: "image/avif", quality: 60},
		{format: transform.FormatAVIF, opts: encode.Options{Lossless: new(true)}, keeps: true, tol: 1, contains: "image/avif"},
		{format: transform.FormatHEIC, keeps: true, tol: 8, contains: "image/heic", quality: 60},
		{format: transform.FormatJXL, keeps: true, tol: 6, contains: "image/jxl", quality: 75},
		{format: transform.FormatJXL, opts: encode.Options{Lossless: new(true)}, keeps: true, tol: 0.01, contains: "image/jxl"},
		// GIF alpha is 1-bit: the graded alpha of rgba8.png is thresholded.
		{format: transform.FormatGIF, keeps: true, tol: 12, alphaTol: 40, contains: "image/gif"},
		{format: transform.FormatTIFF, keeps: true, tol: 6, contains: "image/tiff", quality: 80},
	} {
		for _, in := range []input{photo, rgba} {
			name := tc.format.String() + "/" + in.name
			if tc.opts.Lossless != nil {
				name += "/lossless"
			}
			if tc.opts.Quantize {
				name += "/quantised"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				src := readFixture(t, in.file)
				out := mustEncode(t, e, src, 0, &encode.Target{
					Format: tc.format, Options: tc.opts,
					Metadata: encode.Metadata{Strip: true, KeepCopyright: true, StripColorProfile: true},
				}, nil)
				if !strings.HasPrefix(out.res.ContentType, tc.contains) || out.res.Format != tc.format {
					t.Errorf("format, content type = %v, %q", out.res.Format, out.res.ContentType)
				}
				wantQ := tc.quality
				if tc.format == transform.FormatTIFF && in.alpha {
					wantQ = 0 // deflate
				}
				if out.res.Quality != wantQ || out.res.Attempts != 1 || out.res.OverBudget {
					t.Errorf("quality, attempts, over = %d, %d, %v; want %d, 1, false",
						out.res.Quality, out.res.Attempts, out.res.OverBudget, wantQ)
				}
				got := decode(t, out.data)
				h := got.header
				if h.Format != vipsFormatOf(tc.format) || h.Width != in.w || h.Height != in.h || h.Pages != 1 {
					t.Fatalf("header = %+v, want %v %dx%d, 1 page", h, tc.format, in.w, in.h)
				}
				wantAlpha := in.alpha && tc.keeps
				if h.HasAlpha != wantAlpha {
					t.Errorf("alpha = %v, want %v", h.HasAlpha, wantAlpha)
				}
				want := decode(t, src)
				tol := tc.tol
				if in.alpha && tc.alphaTol > 0 {
					tol = tc.alphaTol
				}
				if d := meanAbsDiff(t, want.pix, want.bands, got.pix, got.bands); d > tol {
					t.Errorf("mean abs difference %.2f > %.2f", d, tol)
				}
			})
		}
	}
}

// TestJPEGFlattensOnWhite checks that transparent pixels become white.
func TestJPEGFlattensOnWhite(t *testing.T) {
	t.Parallel()
	e := newEncoder(t, nil)
	src := readFixture(t, "png/rgba8.png")
	out := mustEncode(t, e, src, 0, &encode.Target{Format: transform.FormatJPEG, Quality: 95}, nil)
	got := decode(t, out.data)
	want := decode(t, src)
	if d := meanAbsDiff(t, want.pix, want.bands, got.pix, got.bands); d > 4 {
		t.Errorf("mean abs difference to the white composite %.2f > 4", d)
	}
}

// TestGoEncoders checks BMP and ICO: decoded with the pure-Go decoders, the
// pixels equal the source's exactly (both are lossless).
func TestGoEncoders(t *testing.T) {
	t.Parallel()
	e := newEncoder(t, nil)
	resize := func(scale float64) func(context.Context, *vips.Arena, *vips.Image) (*vips.Image, error) {
		return func(ctx context.Context, a *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return a.Add(img.Resize(ctx, scale, 0, vips.KernelLanczos3))
		}
	}
	for _, tc := range []struct {
		name   string
		format transform.Format
		file   string
		prep   func(context.Context, *vips.Arena, *vips.Image) (*vips.Image, error)
		bands  int
	}{
		{"bmp rgb", transform.FormatBMP, "jpeg/baseline.jpg", nil, 3},
		{"bmp rgba", transform.FormatBMP, "png/rgba8.png", nil, 4},
		{"bmp 16-bit", transform.FormatBMP, "png/rgb16.png", nil, 3},
		{"ico png entry", transform.FormatICO, "png/rgba8.png", nil, 4},
		{"ico dib entry", transform.FormatICO, "png/rgba8.png", resize(0.25), 4},
		{"ico rgb", transform.FormatICO, "jpeg/baseline.jpg", resize(0.5), 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := readFixture(t, tc.file)
			// The expected pixels: the same pipeline, read as sRGB uchar.
			var want []byte
			var wantBands, w, h int
			err := vips.Run(t.Context(), func() error {
				ctx := t.Context()
				var a vips.Arena
				defer a.Close()
				img, err := a.Add(vips.Load(ctx, newBuffer(t, src), nil))
				if err != nil {
					return err
				}
				if tc.prep != nil {
					if img, err = tc.prep(ctx, &a, img); err != nil {
						return err
					}
				}
				w, h = img.Width(), img.Height()
				want, wantBands, err = srgbPixels(ctx, &a, img)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			out := mustEncode(t, e, src, 0, &encode.Target{Format: tc.format}, tc.prep)
			if out.res.Format != tc.format || out.res.ContentType == "" || out.res.Quality != 0 || out.res.Attempts != 1 {
				t.Errorf("result = %+v", out.res)
			}
			var dec codec.Decoded
			if tc.format == transform.FormatBMP {
				dec, err = bmp.Decode(out.data, bmp.Options{})
			} else {
				dec, err = ico.Decode(out.data, ico.Options{})
			}
			if err != nil {
				t.Fatalf("Go decoder: %v", err)
			}
			var gotPix []byte
			var gotBands, gw, gh int
			if dec.Raw != nil {
				gotPix, gotBands, gw, gh = dec.Raw.Data, dec.Raw.Bands, dec.Raw.Width, dec.Raw.Height
			} else {
				d := decode(t, dec.Embedded)
				gotPix, gotBands, gw, gh = d.pix, d.bands, d.header.Width, d.header.Height
			}
			if gw != w || gh != h || gotBands != tc.bands || wantBands != tc.bands {
				t.Fatalf("got %dx%d×%d, want %dx%d×%d (source bands %d)", gw, gh, gotBands, w, h, tc.bands, wantBands)
			}
			if !bytes.Equal(gotPix, want) {
				t.Errorf("pixels differ (mean abs %.3f)", meanAbsDiff(t, want, wantBands, gotPix, gotBands))
			}
		})
	}
	t.Run("ico too large", func(t *testing.T) {
		t.Parallel()
		_, err := encodeData(t.Context(), e, readFixture(t, "jpeg/baseline.jpg"), 0,
			&encode.Target{Format: transform.FormatICO}, nil)
		if !errors.Is(err, errs.Match(errs.KindLimitExceeded)) {
			t.Fatalf("err = %v, want limit exceeded", err)
		}
	})
}

// TestAnimation keeps every frame for animatable formats and falls back to
// the first frame elsewhere.
func TestAnimation(t *testing.T) {
	t.Parallel()
	e := newEncoder(t, nil)
	for _, file := range []string{"gif/animated.gif", "webp/animated.webp"} {
		for _, tc := range []struct {
			format   transform.Format
			animated bool
			pages    int
		}{
			{transform.FormatGIF, true, 6},
			{transform.FormatWebP, true, 6},
			{transform.FormatJXL, true, 6},
			{transform.FormatGIF, false, 1},
			{transform.FormatWebP, false, 1},
			{transform.FormatPNG, true, 1},
			{transform.FormatJPEG, true, 1},
			{transform.FormatAVIF, true, 1},
			{transform.FormatBMP, true, 1},
		} {
			name := file + "/" + tc.format.String()
			if !tc.animated {
				name += "/still"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				src := readFixture(t, file)
				in := decode(t, src)
				out := mustEncode(t, e, src, -1, &encode.Target{Format: tc.format, Animated: tc.animated}, nil)
				var w, h, pages int
				if tc.format == transform.FormatBMP {
					dec, err := bmp.Decode(out.data, bmp.Options{})
					if err != nil {
						t.Fatal(err)
					}
					w, h, pages = dec.Raw.Width, dec.Raw.Height, 1
				} else {
					got := decode(t, out.data)
					w, h, pages = got.header.Width, got.header.Height, got.header.Pages
					if tc.pages > 1 && !got.header.IsAnimated {
						t.Errorf("output is not animated: %+v", got.header)
					}
				}
				if pages != tc.pages || w != in.header.Width || h != in.header.Height {
					t.Errorf("got %dx%d, %d pages; want %dx%d, %d pages",
						w, h, pages, in.header.Width, in.header.Height, tc.pages)
				}
			})
		}
	}
}

// TestMaxBytes encodes a photo under three budgets: the output meets the
// budget or is the smallest attempt with OverBudget set, in at most six saves.
func TestMaxBytes(t *testing.T) {
	t.Parallel()
	e := newEncoder(t, nil)
	src := readFixture(t, "jpeg/baseline.jpg")
	for _, f := range []transform.Format{transform.FormatJPEG, transform.FormatWebP, transform.FormatAVIF} {
		full := mustEncode(t, e, src, 0, &encode.Target{Format: f}, nil)
		sizes := []int{len(full.data) + 1, len(full.data) / 2, 200}
		for i, budget := range sizes {
			t.Run(f.String()+"/"+[]string{"fits", "half", "tiny"}[i], func(t *testing.T) {
				t.Parallel()
				out := mustEncode(t, e, src, 0, &encode.Target{Format: f, MaxBytes: budget}, nil)
				r := out.res
				if r.Attempts < 1 || r.Attempts > encode.MaxSaves {
					t.Errorf("attempts = %d, want 1..%d", r.Attempts, encode.MaxSaves)
				}
				if len(out.data) > budget && !r.OverBudget {
					t.Errorf("%d bytes > budget %d without OverBudget", len(out.data), budget)
				}
				if r.OverBudget && len(out.data) <= budget {
					t.Errorf("OverBudget with %d bytes ≤ %d", len(out.data), budget)
				}
				switch i {
				case 0:
					if r.Attempts != 1 || r.Quality != full.res.Quality || r.OverBudget {
						t.Errorf("fitting budget: %+v", r)
					}
				case 1:
					if r.OverBudget || r.Quality >= full.res.Quality {
						t.Errorf("half budget: %+v (%d bytes)", r, len(out.data))
					}
				default:
				}
				if r.OverBudget {
					// The smallest attempt: quality 1 or near it.
					if r.Quality > 5 {
						t.Errorf("over budget at quality %d, want the lowest tried", r.Quality)
					}
				}
				if d := decode(t, out.data); d.header.Width != 320 || d.header.Height != 240 {
					t.Errorf("decoded %dx%d", d.header.Width, d.header.Height)
				}
			})
		}
	}
	t.Run("png flagged", func(t *testing.T) {
		t.Parallel()
		out := mustEncode(t, e, src, 0, &encode.Target{Format: transform.FormatPNG, MaxBytes: 1000}, nil)
		if !out.res.OverBudget || out.res.Attempts != 1 {
			t.Errorf("png over budget: %+v", out.res)
		}
	})
}

// TestHEICGate refuses HEIC output unless the config enables it.
func TestHEICGate(t *testing.T) {
	t.Parallel()
	src := readFixture(t, "jpeg/baseline.jpg")
	off := newEncoder(t, nil)
	_, err := encodeData(t.Context(), off, src, 0, &encode.Target{Format: transform.FormatHEIC}, nil)
	if !errors.Is(err, errs.Match(errs.KindUnsupported)) {
		t.Fatalf("heic with encode off: err = %v, want unsupported", err)
	}
	on := newEncoder(t, func(c *encode.Config) { c.HEICEncode = true })
	out := mustEncode(t, on, src, 0, &encode.Target{Format: transform.FormatHEIC}, nil)
	if h := decode(t, out.data).header; h.Format != vips.FormatHEIF || h.Width != 320 {
		t.Errorf("heic header = %+v", h)
	}
	for _, f := range []transform.Format{transform.FormatSVG, transform.FormatMP4, transform.FormatJSON, transform.FormatUnset} {
		if _, err := encodeData(t.Context(), on, src, 0, &encode.Target{Format: f}, nil); !errors.Is(err, errs.Match(errs.KindUnsupported)) {
			t.Errorf("%q: err = %v, want unsupported", f, err)
		}
	}
}

// ---- metadata -------------------------------------------------------------

// tiffWriter builds a little-endian EXIF (TIFF) structure.
type ifdEntry struct {
	tag, typ uint16
	count    uint32
	data     []byte // value bytes; pointers are patched in
	ifd      int    // for pointer tags: index of the sub-IFD
}

func asciiEntry(tag uint16, s string) ifdEntry {
	return ifdEntry{tag: tag, typ: 2, count: uint32(len(s) + 1), data: append([]byte(s), 0), ifd: -1} //nolint:gosec // tiny test fixture
}

func rationals(tag uint16, v ...uint32) ifdEntry {
	b := make([]byte, 0, len(v)*8)
	for _, x := range v {
		b = binary.LittleEndian.AppendUint32(b, x)
		b = binary.LittleEndian.AppendUint32(b, 1)
	}
	return ifdEntry{tag: tag, typ: 5, count: uint32(len(v)), data: b, ifd: -1} //nolint:gosec // tiny test fixture
}

// buildEXIF returns an APP1 EXIF payload: IFD0 with Make, Model, Artist,
// Copyright and pointers to an Exif IFD (DateTimeOriginal) and a GPS IFD.
func buildEXIF() []byte {
	ifds := [][]ifdEntry{
		{
			asciiEntry(0x010F, "TestMake"),
			asciiEntry(0x0110, "TestModel"),
			asciiEntry(0x013B, "Jane Artist"),
			asciiEntry(0x8298, "(c) Jane"),
			{tag: 0x8769, typ: 4, count: 1, ifd: 1},
			{tag: 0x8825, typ: 4, count: 1, ifd: 2},
		},
		{asciiEntry(0x9003, "2024:01:02 03:04:05")},
		{
			{tag: 0x0000, typ: 1, count: 4, data: []byte{2, 2, 0, 0}, ifd: -1},
			asciiEntry(0x0001, "N"),
			rationals(0x0002, 52, 31, 0),
			asciiEntry(0x0003, "E"),
			rationals(0x0004, 13, 24, 0),
		},
	}
	// Layout: header, then each IFD followed by its out-of-line data.
	offsets := make([]uint32, len(ifds))
	off := uint32(8)
	for i, ifd := range ifds {
		offsets[i] = off
		off += 2 + uint32(len(ifd))*12 + 4 //nolint:gosec // tiny test fixture
		for _, e := range ifd {
			if len(e.data) > 4 {
				off += uint32(len(e.data) + len(e.data)%2) //nolint:gosec // tiny test fixture
			}
		}
	}
	b := []byte("II*\x00\x08\x00\x00\x00")
	for i, ifd := range ifds {
		data := offsets[i] + 2 + uint32(len(ifd))*12 + 4 //nolint:gosec // tiny test fixture
		var extra []byte
		b = binary.LittleEndian.AppendUint16(b, uint16(len(ifd))) //nolint:gosec // tiny test fixture
		for _, e := range ifd {
			b = binary.LittleEndian.AppendUint16(b, e.tag)
			b = binary.LittleEndian.AppendUint16(b, e.typ)
			b = binary.LittleEndian.AppendUint32(b, e.count)
			switch {
			case e.ifd >= 0:
				b = binary.LittleEndian.AppendUint32(b, offsets[e.ifd])
			case len(e.data) > 4:
				b = binary.LittleEndian.AppendUint32(b, data+uint32(len(extra))) //nolint:gosec // tiny test fixture
				extra = append(extra, e.data...)
				if len(e.data)%2 == 1 {
					extra = append(extra, 0)
				}
			default:
				v := make([]byte, 4)
				copy(v, e.data)
				b = append(b, v...)
			}
		}
		b = binary.LittleEndian.AppendUint32(b, 0) // no next IFD
		b = append(b, extra...)
	}
	return append([]byte("Exif\x00\x00"), b...)
}

const testXMP = `<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` +
	`<rdf:Description rdf:about="" xmlns:exif="http://ns.adobe.com/exif/1.0/" xmlns:dc="http://purl.org/dc/elements/1.1/" ` +
	`exif:GPSLatitude="52,31.0N" exif:GPSLongitude="13,24.0E">` +
	`<dc:rights><rdf:Alt><rdf:li xml:lang="x-default">(c) Jane</rdf:li></rdf:Alt></dc:rights>` +
	`</rdf:Description></rdf:RDF></x:xmpmeta>`

// buildIPTC returns an APP13 Photoshop payload with an IPTC copyright notice.
func buildIPTC() []byte {
	iptc := make([]byte, 0, 13)
	iptc = append(iptc, 0x1C, 0x02, 0x74, 0x00, 0x08)
	iptc = append(iptc, "(c) Jane"...)
	b := []byte("Photoshop 3.0\x008BIM\x04\x04\x00\x00")
	b = binary.BigEndian.AppendUint32(b, uint32(len(iptc))) //nolint:gosec // tiny test fixture
	b = append(b, iptc...)
	if len(iptc)%2 == 1 {
		b = append(b, 0)
	}
	return b
}

// metadataJPEG returns a JPEG carrying EXIF (with GPS), XMP (with GPS) and
// IPTC segments.
func metadataJPEG(tb testing.TB) []byte {
	tb.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for y := range 48 {
		for x := range 64 {
			img.Set(x, y, color.RGBA{uint8(x * 4), uint8(y * 5), 128, 255})
		}
	}
	var enc bytes.Buffer
	if err := jpeg.Encode(&enc, img, &jpeg.Options{Quality: 90}); err != nil {
		tb.Fatal(err)
	}
	segment := func(marker byte, payload []byte) []byte {
		s := []byte{0xFF, marker}
		s = binary.BigEndian.AppendUint16(s, uint16(len(payload)+2)) //nolint:gosec // tiny test fixture
		return append(s, payload...)
	}
	out := []byte{0xFF, 0xD8}
	out = append(out, segment(0xE1, buildEXIF())...)
	out = append(out, segment(0xE1, append([]byte("http://ns.adobe.com/xap/1.0/\x00"), testXMP...))...)
	out = append(out, segment(0xED, buildIPTC())...)
	return append(out, enc.Bytes()[2:]...)
}

func hasPrefix(fields []vips.MetaField, prefix string) bool {
	for _, f := range fields {
		if strings.HasPrefix(string(f), prefix) {
			return true
		}
	}
	return false
}

func has(fields []vips.MetaField, name vips.MetaField) bool {
	return slices.Contains(fields, name)
}

// TestMetadata checks the policy of FORMATS §4 on outputs read back with
// libvips.
func TestMetadata(t *testing.T) {
	t.Parallel()
	e := newEncoder(t, nil)
	src := metadataJPEG(t)
	in := decode(t, src)
	// The fixture must carry everything the cases below remove.
	for _, f := range []vips.MetaField{"exif-ifd0-Make", "exif-ifd0-Copyright", "exif-ifd0-Artist", "exif-ifd2-DateTimeOriginal", "exif-ifd3-GPSLatitude", vips.FieldXMP, vips.FieldIPTC} {
		if !has(in.fields, f) {
			t.Fatalf("fixture lacks %s: %v", f, in.fields)
		}
	}
	for _, format := range []transform.Format{transform.FormatJPEG, transform.FormatWebP} {
		for _, tc := range []struct {
			name     string
			m        encode.Metadata
			present  []vips.MetaField
			absent   []vips.MetaField
			noPrefix []string
		}{
			{
				name:     "strip all",
				m:        encode.Metadata{Strip: true, StripColorProfile: true},
				absent:   []vips.MetaField{vips.FieldEXIF, vips.FieldXMP, vips.FieldIPTC, vips.FieldICC},
				noPrefix: []string{"exif-ifd"},
			},
			{
				name:     "keep copyright",
				m:        encode.Metadata{Strip: true, KeepCopyright: true, StripColorProfile: true},
				present:  []vips.MetaField{vips.FieldEXIF, "exif-ifd0-Copyright", "exif-ifd0-Artist"},
				absent:   []vips.MetaField{"exif-ifd0-Make", "exif-ifd0-Model", "exif-ifd2-DateTimeOriginal", vips.FieldXMP, vips.FieldIPTC, vips.FieldICC},
				noPrefix: []string{"exif-ifd3-"},
			},
			{
				name:     "keep all but gps",
				m:        encode.Metadata{StripColorProfile: true},
				present:  []vips.MetaField{"exif-ifd0-Make", "exif-ifd0-Copyright", "exif-ifd2-DateTimeOriginal", vips.FieldXMP},
				noPrefix: []string{"exif-ifd3-"},
			},
		} {
			t.Run(format.String()+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				out := mustEncode(t, e, src, 0, &encode.Target{Format: format, Metadata: tc.m}, nil)
				got := decode(t, out.data)
				for _, f := range tc.present {
					if !has(got.fields, f) {
						t.Errorf("%s missing; fields %v", f, got.fields)
					}
				}
				for _, f := range tc.absent {
					if has(got.fields, f) {
						t.Errorf("%s survived", f)
					}
				}
				for _, p := range tc.noPrefix {
					if hasPrefix(got.fields, p) {
						t.Errorf("a %s* field survived: %v", p, got.fields)
					}
				}
				if c, ok := got.text["exif-ifd0-Copyright"]; ok && !strings.HasPrefix(c, "(c) Jane") {
					t.Errorf("copyright = %q", c)
				}
				if x, ok := got.blobs[vips.FieldXMP]; ok && bytes.Contains(x, []byte("GPS")) {
					t.Errorf("GPS survived in XMP: %s", x)
				}
			})
		}
	}
	t.Run("color profile", func(t *testing.T) {
		t.Parallel()
		src := readFixture(t, "jpeg/exif-orientation-6-icc.jpg")
		for _, strip := range []bool{true, false} {
			out := mustEncode(t, e, src, 0, &encode.Target{
				Format:   transform.FormatJPEG,
				Metadata: encode.Metadata{Strip: true, StripColorProfile: strip},
			}, nil)
			if got := decode(t, out.data).header.HasICC; got == strip {
				t.Errorf("strip_color_profile=%v: has ICC = %v", strip, got)
			}
		}
	})
}

func TestCanceled(t *testing.T) {
	t.Parallel()
	e := newEncoder(t, nil)
	src := readFixture(t, "jpeg/baseline.jpg")
	buf := newBuffer(t, src)
	ctx, cancel := context.WithCancel(t.Context())
	err := vips.Run(ctx, func() error {
		img, err := vips.Load(ctx, buf, nil)
		if err != nil {
			return err
		}
		defer img.Close()
		cancel()
		_, err = e.Encode(ctx, img, &encode.Target{Format: transform.FormatJPEG, MaxBytes: 1000})
		return err
	})
	if !errors.Is(err, errs.Match(errs.KindCanceled)) {
		t.Fatalf("err = %v, want canceled", err)
	}
}
