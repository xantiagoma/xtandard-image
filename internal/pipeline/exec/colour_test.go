//go:build cgo

package exec_test

import (
	"image"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// metadataP3 is a 120x160 JPEG (upright 160x120 with EXIF orientation 6) in
// Display P3 carrying EXIF with GPS, XMP and IPTC: the small photo that
// page 0 of tiff/multipage.tif holds in sRGB.
const metadataP3 = "jpeg/metadata-p3.jpg"

// header is what libvips reads back from an encoded output.
type header struct {
	width, height, orientation int
	text                       map[vips.MetaField]string
	blobs                      map[vips.MetaField][]byte
}

func readHeader(tb testing.TB, data []byte) header {
	tb.Helper()
	buf := newBuffer(tb, data)
	defer buf.Release()
	h := header{text: map[vips.MetaField]string{}, blobs: map[vips.MetaField][]byte{}}
	err := vips.Run(tb.Context(), func() error {
		img, err := vips.Load(tb.Context(), buf, &vips.LoadOptions{})
		if err != nil {
			return err
		}
		defer img.Close()
		h.width, h.height, h.orientation = img.Width(), img.Height(), img.Orientation()
		for _, f := range img.Fields() {
			if s, ok := img.Text(f); ok {
				h.text[f] = s
			}
			if b, ok := img.Blob(f); ok {
				h.blobs[f] = b
			}
		}
		return nil
	})
	if err != nil {
		tb.Fatalf("read header: %v", err)
	}
	return h
}

// TestICCToSRGB: an embedded Display P3 profile is converted to sRGB. The
// output matches libvips' own LittleCMS transform of the decoded pixels
// (icc_transform to srgb with the embedded profile), and differs from the P3
// values taken as sRGB.
func TestICCToSRGB(t *testing.T) {
	t.Parallel()
	data := readFixture(t, metadataP3)
	want := rotate90(iccTransformSRGB(t, data))
	raw, _ := decode(t, data)
	unconverted := meanAbsDiff(want, rotate90(raw))
	for _, options := range []string{"", "scp:0"} {
		t.Run(options, func(t *testing.T) {
			t.Parallel()
			o := process(t, metadataP3, options, transform.FormatPNG)
			if o.res.Width != 160 || o.res.Height != 120 {
				t.Fatalf("size %dx%d, want 160x120", o.res.Width, o.res.Height)
			}
			converted := meanAbsDiff(want, o.img)
			t.Logf("mean abs diff to libvips' transform: output %.3f, unconverted %.3f", converted, unconverted)
			if converted > 0.5 || converted*4 > unconverted {
				t.Errorf("mean abs diff %.3f (unconverted %.3f): not converted to sRGB", converted, unconverted)
			}
		})
	}
}

// iccTransformSRGB decodes data and converts it with vips icc_transform from
// the embedded profile to sRGB, as stored (not rotated).
func iccTransformSRGB(tb testing.TB, data []byte) *image.NRGBA {
	tb.Helper()
	buf := newBuffer(tb, data)
	defer buf.Release()
	var pix []byte
	var w, h int
	err := vips.Run(tb.Context(), func() error {
		var a vips.Arena
		defer a.Close()
		ctx := tb.Context()
		img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{}))
		if err != nil {
			return err
		}
		if img, err = a.Add(img.ICCTransform(ctx, "srgb", vips.IntentRelative, true, 8)); err != nil {
			return err
		}
		if img, err = a.Add(img.AddAlpha(ctx)); err != nil {
			return err
		}
		w, h = img.Width(), img.Height()
		pix, err = img.WriteToMemory(ctx)
		return err
	})
	if err != nil {
		tb.Fatalf("icc_transform: %v", err)
	}
	return &image.NRGBA{Pix: pix, Stride: 4 * w, Rect: image.Rect(0, 0, w, h)}
}

// TestOrientationReset: after auto-rotation the output carries no rotation
// (orientation 1 or no tag) even when EXIF is kept, so a second pass does not
// rotate again; auto_rotate=false keeps the stored pixels and still resets
// the tag.
func TestOrientationReset(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		options string
		out     transform.Format
		w, h    int
	}{
		{"sm:0", transform.FormatJPEG, 160, 120},
		{"sm:0", transform.FormatWebP, 160, 120},
		{"sm:0", transform.FormatPNG, 160, 120},
		{"sm:1/kcr:1", transform.FormatJPEG, 160, 120},
		{"ar:0/sm:0", transform.FormatJPEG, 120, 160},
	} {
		t.Run(tc.options+"/"+tc.out.String(), func(t *testing.T) {
			t.Parallel()
			first := process(t, metadataP3, tc.options, tc.out)
			h := readHeader(t, first.bytes)
			if h.width != tc.w || h.height != tc.h || h.orientation != 1 {
				t.Errorf("first pass: %dx%d orientation %d, want %dx%d orientation 1", h.width, h.height, h.orientation, tc.w, tc.h)
			}
			if s, ok := h.text["exif-ifd0-Orientation"]; ok && !strings.HasPrefix(s, "1 ") {
				t.Errorf("EXIF Orientation = %q", s)
			}
			second := processData(t, first.bytes, tc.options, tc.out)
			if second.res.Width != tc.w || second.res.Height != tc.h {
				t.Errorf("second pass: %dx%d, want %dx%d", second.res.Width, second.res.Height, tc.w, tc.h)
			}
		})
	}
}

// meanAbsDiff is the mean absolute difference of the RGB channels of two
// NRGBA images of the same size.
func meanAbsDiff(a, b image.Image) float64 {
	pa, ok := a.(*image.NRGBA)
	if !ok {
		return 255
	}
	pb, ok := b.(*image.NRGBA)
	if !ok {
		return 255
	}
	if pa.Rect != pb.Rect {
		return 255
	}
	var sum, n float64
	for i := 0; i+3 < len(pa.Pix); i += 4 {
		for c := range 3 {
			d := float64(pa.Pix[i+c]) - float64(pb.Pix[i+c])
			if d < 0 {
				d = -d
			}
			sum += d
			n++
		}
	}
	return sum / n
}

// rotate90 turns an image a quarter turn clockwise (EXIF orientation 6).
func rotate90(img image.Image) *image.NRGBA {
	src, ok := img.(*image.NRGBA)
	if !ok {
		return nil
	}
	w, h := src.Rect.Dx(), src.Rect.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, h, w))
	for y := range h {
		for x := range w {
			copy(dst.Pix[dst.PixOffset(h-1-y, x):][:4], src.Pix[src.PixOffset(x, y):][:4])
		}
	}
	return dst
}
