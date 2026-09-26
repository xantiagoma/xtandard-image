package ssim

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"path/filepath"
	"testing"
)

// TestReferenceMatchesCompare checks that Reference.Compare returns exactly
// what Compare does, for opaque and translucent pairs and on repeated use.
func TestReferenceMatchesCompare(t *testing.T) {
	t.Parallel()
	ref := decodeFile(t, filepath.Join(refDir, "ref.png"))
	translucent := func(src image.Image, a uint8) *image.NRGBA {
		b := src.Bounds()
		out := image.NewNRGBA(b)
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				c := color.NRGBAModel.Convert(src.At(x, y)).(color.NRGBA) //nolint:forcetypeassert // NRGBAModel always returns NRGBA
				if x < b.Dx()/2 {
					c.A = a
				}
				out.SetNRGBA(x, y, c)
			}
		}
		return out
	}
	for _, tc := range []struct {
		name string
		a    image.Image
		bs   []image.Image
	}{
		{"opaque", ref, []image.Image{
			decodeFile(t, filepath.Join(refDir, "jpeg.png")),
			decodeFile(t, filepath.Join(refDir, "blur.png")),
			ref,
		}},
		{"translucent candidate", ref, []image.Image{translucent(ref, 128), ref}},
		{"translucent reference", translucent(ref, 90), []image.Image{ref, translucent(ref, 90), translucent(ref, 200)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, err := NewReference(tc.a)
			if err != nil {
				t.Fatal(err)
			}
			for i, b := range tc.bs {
				want, err := Compare(tc.a, b)
				if err != nil {
					t.Fatal(err)
				}
				got, err := r.Compare(b)
				if err != nil {
					t.Fatal(err)
				}
				if got.SSIM != want.SSIM || got.DSSIM != want.DSSIM || got.Map != nil {
					t.Errorf("candidate %d: SSIM %v (map %d), want %v", i, got.SSIM, len(got.Map), want.SSIM)
				}
			}
		})
	}
}

func TestReferenceErrors(t *testing.T) {
	t.Parallel()
	if _, err := NewReference(image.NewGray(image.Rect(0, 0, 0, 4))); !errors.Is(err, ErrEmpty) {
		t.Errorf("empty: %v", err)
	}
	r, err := NewReference(uniform(16, 10))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Compare(uniform(15, 10)); !errors.Is(err, ErrSizeMismatch) {
		t.Errorf("size mismatch: %v", err)
	}
}

// BenchmarkReference measures one candidate comparison at the auto-quality
// comparison size (512x384) against Compare on the same pair.
func BenchmarkReference(b *testing.B) {
	photo := toRGBA(decodeFile(b, "../../../testdata/fixtures/jpeg/baseline.jpg"))
	a := tile(photo, 2).SubImage(image.Rect(0, 0, 512, 384))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, a, &jpeg.Options{Quality: 70}); err != nil {
		b.Fatal(err)
	}
	c, err := jpeg.Decode(&buf)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("Compare", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := Compare(a, c); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("Reference", func(b *testing.B) {
		r, err := NewReference(a)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		for b.Loop() {
			if _, err := r.Compare(c); err != nil {
				b.Fatal(err)
			}
		}
	})
}
