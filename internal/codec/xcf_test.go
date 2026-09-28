//go:build cgo

package codec_test

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/testutil/leakcheck"
	"github.com/xantiagoma/xtandard-image/internal/testutil/ssim"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// The XCF fixtures were saved by GIMP 2.10 (testdata/gen/designer.sh):
// layers.xcf is a 160×120 photo with a 70 % logo, a multiply logo, a group
// holding one logo, and a hidden logo; gray.xcf is the same converted to
// greyscale; alpha.xcf is a 48×32 logo at (8, 8) on a transparent 64×48
// canvas. testdata/golden/codec/xcf-*-gimp.png are GIMP's own flattened
// renders.

type layerSummary struct {
	X, Y, W, H     int
	Alpha, Visible bool
	Opacity        uint8
	Mode           uint32
	Group          bool
	Depth          int
}

func summarise(ls []codec.XCFLayer) []layerSummary {
	out := make([]layerSummary, 0, len(ls))
	for _, l := range ls {
		out = append(out, layerSummary{l.X, l.Y, l.Width, l.Height, l.Alpha, l.Visible, l.Opacity, l.Mode, l.Group, l.Depth})
	}
	return out
}

func TestParseXCFHeaderFixtures(t *testing.T) {
	t.Parallel()
	h, err := codec.ParseXCFHeader(readFixture(t, "xcf/layers.xcf"))
	if err != nil {
		t.Fatal(err)
	}
	if h.Version != 11 || h.Width != 160 || h.Height != 120 || h.Grey || h.PointerSize != 8 {
		t.Errorf("header %+v", h)
	}
	logo := layerSummary{W: 48, H: 32, Alpha: true, Visible: true, Opacity: 255, Mode: 28}
	with := func(l layerSummary, edit func(*layerSummary)) layerSummary {
		edit(&l)
		return l
	}
	// File order: the top of the stack first.
	want := []layerSummary{
		with(logo, func(l *layerSummary) { l.X, l.Y, l.Visible = 4, 4, false }),                       // hidden
		{X: 10, Y: 70, W: 48, H: 32, Alpha: true, Visible: true, Opacity: 255, Mode: 28, Group: true}, // group
		with(logo, func(l *layerSummary) { l.X, l.Y, l.Depth = 10, 70, 2 }),                           // child
		with(logo, func(l *layerSummary) { l.X, l.Y, l.Mode = 100, 8, 30 }),                           // multiply
		with(logo, func(l *layerSummary) { l.X, l.Y, l.Opacity = 60, 40, 179 }),                       // 70 %
		{W: 160, H: 120, Visible: true, Opacity: 255, Mode: 28},                                       // photo
	}
	if diff := cmp.Diff(want, summarise(h.Layers)); diff != "" {
		t.Errorf("layers (-want +got):\n%s", diff)
	}
	comp, idx := h.Composited()
	if diff := cmp.Diff([]int{5, 4, 3, 1}, idx); diff != "" {
		t.Errorf("composited layers (-want +got):\n%s", diff)
	}
	if len(comp) != 4 || h.HasAlpha() {
		t.Errorf("%d composited, alpha %v; want 4, false", len(comp), h.HasAlpha())
	}

	g, err := codec.ParseXCFHeader(readFixture(t, "xcf/gray.xcf"))
	if err != nil || !g.Grey || g.HasAlpha() {
		t.Errorf("gray: %+v, %v", g, err)
	}
	a, err := codec.ParseXCFHeader(readFixture(t, "xcf/alpha.xcf"))
	if err != nil || a.Width != 64 || a.Height != 48 || !a.HasAlpha() {
		t.Errorf("alpha: %+v, %v", a, err)
	}
}

// xcfProp is one property of a synthetic XCF file.
func xcfProp(typ uint32, payload ...uint32) []byte {
	b := binary.BigEndian.AppendUint32(nil, typ)
	b = binary.BigEndian.AppendUint32(b, uint32(4*len(payload))) //nolint:gosec // tiny
	for _, p := range payload {
		b = binary.BigEndian.AppendUint32(b, p)
	}
	return b
}

// synthXCF is a v011 RGB file with the given image properties and one
// 4×4 layer of type typ (no pixels: the header reader never needs them).
func synthXCF(version string, base, precision uint32, imageProps []byte, layerType uint32) []byte {
	be := binary.BigEndian
	b := []byte("gimp xcf " + version + "\x00")
	b = be.AppendUint32(b, 16)
	b = be.AppendUint32(b, 8)
	b = be.AppendUint32(b, base)
	if version != "file" && version >= "v004" {
		b = be.AppendUint32(b, precision)
	}
	b = append(b, imageProps...)
	b = append(b, 0, 0, 0, 0, 0, 0, 0, 0) // PROP_END
	ptr := 8
	if version < "v011" {
		ptr = 4
	}
	table := len(b)
	b = append(b, make([]byte, 3*ptr)...) // layer pointer, 0, no channels (0)
	layer := len(b)
	if ptr == 8 {
		be.PutUint64(b[table:], uint64(layer))
	} else {
		be.PutUint32(b[table:], uint32(layer)) //nolint:gosec // tiny
	}
	b = be.AppendUint32(b, 4)
	b = be.AppendUint32(b, 4)
	b = be.AppendUint32(b, layerType)
	b = be.AppendUint32(b, 2)
	b = append(b, "L\x00"...)
	b = append(b, xcfProp(8, 1)...)              // visible
	b = append(b, xcfProp(15, 0xFFFFFFFE, 3)...) // offsets -2, 3
	b = append(b, 0, 0, 0, 0, 0, 0, 0, 0)
	return append(b, make([]byte, 2*ptr)...) // hierarchy, mask
}

func TestParseXCFHeaderSynthetic(t *testing.T) {
	t.Parallel()
	rle := append(xcfProp(17), 1) // PROP_COMPRESSION, one byte
	rle[7] = 1
	zlib := bytes.Clone(rle)
	zlib[8] = 2
	none := bytes.Clone(rle)
	none[8] = 0
	good := synthXCF("v011", 0, 150, rle, 1)
	h, err := codec.ParseXCFHeader(good)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Layers) != 1 || h.Layers[0].X != -2 || h.Layers[0].Y != 3 || !h.Layers[0].Alpha || !h.HasAlpha() {
		t.Errorf("layers %+v", h.Layers)
	}
	if _, err := codec.ParseXCFHeader(synthXCF("file", 1, 0, rle, 2)); err != nil {
		t.Errorf("v0 grey: %v", err)
	}
	tests := []struct {
		name string
		data []byte
		kind errs.Kind
	}{
		{"indexed image", synthXCF("v011", 2, 150, rle, 4), errs.KindUnsupported},
		{"indexed layer", synthXCF("v011", 0, 150, rle, 5), errs.KindUnsupported},
		{"16-bit", synthXCF("v011", 0, 250, rle, 1), errs.KindUnsupported},
		{"zlib tiles", synthXCF("v011", 0, 150, zlib, 1), errs.KindUnsupported},
		{"uncompressed tiles", synthXCF("v011", 0, 150, none, 1), errs.KindUnsupported},
		{"no compression property", synthXCF("v011", 0, 150, nil, 1), errs.KindUnsupported},
		{"32-bit offsets read as 64-bit", synthXCF("v008", 0, 150, rle, 1), errs.KindUnsupported},
		{"bad base", synthXCF("v011", 7, 150, rle, 1), errs.KindCorrupt},
		{"bad layer type", synthXCF("v011", 0, 150, rle, 9), errs.KindCorrupt},
		{"bad version", append([]byte("gimp xcf vxyz\x00"), good[14:]...), errs.KindCorrupt},
		{"truncated", good[:40], errs.KindCorrupt},
		{"layer past the end", good[:len(good)-30], errs.KindCorrupt},
		{"property too long", synthXCF("v011", 0, 150, []byte{0, 0, 0, 21, 0xFF, 0xFF, 0xFF, 0xFF}, 1), errs.KindCorrupt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := codec.ParseXCFHeader(tt.data)
			if k := errs.KindOf(err); err == nil || k != tt.kind {
				t.Fatalf("error %v (kind %v), want kind %v", err, k, tt.kind)
			}
		})
	}
}

// flatten runs FlattenXCF on a fixture and returns the result as an image.
func flatten(tb testing.TB, name string, maxPixels int64) (image.Image, error) {
	tb.Helper()
	buf := newBuffer(tb, readFixture(tb, name))
	defer buf.Release()
	var out image.Image
	err := vips.Run(tb.Context(), func() error {
		var a vips.Arena
		defer a.Close()
		img, err := codec.FlattenXCF(tb.Context(), &a, buf, maxPixels, vips.FailOnError)
		if err != nil {
			return err
		}
		blob, err := img.Save(tb.Context(), vips.FormatPNG, &vips.SaveOptions{})
		if err != nil {
			return err
		}
		defer blob.Release()
		out, err = png.Decode(bytes.NewReader(blob.Bytes()))
		return err
	})
	return out, err
}

// TestFlattenXCF compares the flattened fixtures with GIMP's renders.
func TestFlattenXCF(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		tol  float64
	}{{"layers", 0.02}, {"gray", 0.02}, {"alpha", 0.001}} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := flatten(t, "xcf/"+tt.name+".xcf", 0)
			if err != nil {
				t.Fatal(err)
			}
			want := gimpRender(t, tt.name)
			if tt.name == "alpha" {
				// GIMP saved the layer; place it on the 64×48 canvas.
				c := image.NewNRGBA(image.Rect(0, 0, 64, 48))
				draw.Draw(c, image.Rect(8, 8, 56, 40), want, image.Point{}, draw.Src)
				want = c
				if _, _, _, a := got.At(1, 1).RGBA(); a != 0 {
					t.Errorf("uncovered canvas alpha %d, want transparent", a)
				}
			}
			res, err := ssim.Compare(want, got)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("DSSIM %.5f against GIMP's render", res.DSSIM)
			if res.DSSIM > tt.tol {
				t.Errorf("DSSIM %.5f against GIMP's render, want at most %.5f", res.DSSIM, tt.tol)
			}
		})
	}
}

func TestFlattenXCFLimits(t *testing.T) {
	t.Parallel()
	if _, err := flatten(t, "xcf/layers.xcf", 160*120-1); errs.KindOf(err) != errs.KindLimitExceeded {
		t.Errorf("canvas over the limit: %v", err)
	}
}

// gimpRender is GIMP's flattened render of xcf/<name>.xcf. It is read, not
// compared through golden.Compare, so -update never replaces it with ours.
func gimpRender(tb testing.TB, name string) image.Image {
	tb.Helper()
	b, err := os.ReadFile(filepath.Join(golden.ModuleRoot(tb), "testdata", "golden", "codec", "xcf-"+name+"-gimp.png"))
	if err != nil {
		tb.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		tb.Fatal(err)
	}
	return img
}

// TestFlattenXCFNoLeak flattens the fixtures repeatedly: every layer
// decode, scratch copy and intermediate composite is released.
func TestFlattenXCFNoLeak(t *testing.T) {
	run := func() {
		for _, n := range []string{"xcf/layers.xcf", "xcf/gray.xcf", "xcf/alpha.xcf"} {
			if _, err := flatten(t, n, 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	for range 3 {
		run()
	}
	before := leakcheck.Take()
	for range 60 {
		run()
	}
	leakcheck.Check(t, before, leakcheck.Default, "flattens")
}

func FuzzParseXCFHeader(f *testing.F) {
	rle := append(xcfProp(17), 1)
	rle[7] = 1
	f.Add(synthXCF("v011", 0, 150, rle, 1))
	f.Add(synthXCF("file", 1, 0, rle, 2))
	f.Add(readFixture(f, "xcf/alpha.xcf"))
	f.Fuzz(func(t *testing.T, data []byte) {
		h, err := codec.ParseXCFHeader(data)
		if err != nil {
			if k := errs.KindOf(err); k != errs.KindCorrupt && k != errs.KindUnsupported && k != errs.KindLimitExceeded {
				t.Fatalf("error kind %v: %v", k, err)
			}
			return
		}
		if h.Width <= 0 || h.Height <= 0 || len(h.Layers) == 0 {
			t.Fatalf("accepted %+v", h)
		}
		dst := make([]byte, len(data))
		for i := range h.Layers {
			if err := codec.XCFLayerFile(dst, data, &h, i); err != nil && errs.KindOf(err) != errs.KindCorrupt {
				t.Fatal(err)
			}
		}
	})
}
