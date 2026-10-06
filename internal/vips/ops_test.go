//go:build cgo

package vips_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"math"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

type opFunc func(ctx context.Context, a *vips.Arena, img *vips.Image) (*vips.Image, error)

func TestOpsGeometry(t *testing.T) {
	t.Parallel()

	white := []float64{255, 255, 255}
	tests := []struct {
		name    string
		fixture string
		op      opFunc
		want    geometry
	}{
		{"resize", "jpeg/baseline.jpg", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Resize(ctx, 0.5, 0, vips.KernelLanczos3)
		}, geometry{160, 120, 120, 1, 3, 1}},
		{"resize anisotropic", "jpeg/baseline.jpg", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Resize(ctx, 0.5, 0.25, vips.KernelMitchell)
		}, geometry{160, 60, 60, 1, 3, 1}},
		{"extract area", "jpeg/baseline.jpg", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.ExtractArea(ctx, image.Rect(10, 10, 110, 60))
		}, geometry{100, 50, 50, 1, 3, 1}},
		{"crop", "jpeg/baseline.jpg", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Crop(ctx, image.Rect(10, 10, 110, 60))
		}, geometry{100, 50, 50, 1, 3, 1}},
		{"embed", "jpeg/baseline.jpg", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Embed(ctx, 10, 10, 340, 260, vips.ExtendBackground, white)
		}, geometry{340, 260, 260, 1, 3, 1}},
		{"gravity crops", "jpeg/baseline.jpg", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Gravity(ctx, vips.CompassCentre, 100, 100, vips.ExtendBlack, nil)
		}, geometry{100, 100, 100, 1, 3, 1}},
		{"gravity extends", "jpeg/baseline.jpg", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Gravity(ctx, vips.CompassSouthEast, 400, 400, vips.ExtendWhite, nil)
		}, geometry{400, 400, 400, 1, 3, 1}},
		{"flip", "jpeg/baseline.jpg", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Flip(ctx, vips.DirectionHorizontal)
		}, geometry{320, 240, 240, 1, 3, 1}},
		{"rot 90", "jpeg/baseline.jpg", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Rot(ctx, vips.Angle90)
		}, geometry{240, 320, 320, 1, 3, 1}},
		{"autorot", "jpeg/exif-orientation-6-icc.jpg", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Autorot(ctx)
		}, geometry{320, 240, 240, 1, 3, 1}},
		{"smartcrop", "jpeg/baseline.jpg", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.SmartCrop(ctx, 100, 100, vips.InterestingAttention)
		}, geometry{100, 100, 100, 1, 3, 1}},
		{"flatten", "png/rgba8.png", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Flatten(ctx, white)
		}, geometry{128, 96, 96, 1, 3, 1}},
		{"flatten 16-bit", "png/rgb16.png", func(ctx context.Context, a *vips.Arena, img *vips.Image) (*vips.Image, error) {
			withAlpha, err := a.Add(img.AddAlpha(ctx))
			if err != nil {
				return nil, err
			}
			return withAlpha.Flatten(ctx, white)
		}, geometry{96, 72, 72, 1, 3, 1}},
		{"premultiply round trip", "png/rgba8.png", func(ctx context.Context, a *vips.Arena, img *vips.Image) (*vips.Image, error) {
			pre, err := a.Add(img.Premultiply(ctx))
			if err != nil {
				return nil, err
			}
			return pre.Unpremultiply(ctx)
		}, geometry{128, 96, 96, 1, 4, 1}},
		{"colourspace b-w", "jpeg/baseline.jpg", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Colourspace(ctx, vips.InterpretationBW)
		}, geometry{320, 240, 240, 1, 1, 1}},
		{"icc import export", "jpeg/exif-orientation-6-icc.jpg", func(ctx context.Context, a *vips.Arena, img *vips.Image) (*vips.Image, error) {
			lab, err := a.Add(img.ICCImport(ctx, "", vips.IntentRelative, true))
			if err != nil {
				return nil, err
			}
			return lab.ICCExport(ctx, "srgb", vips.IntentRelative, 8)
		}, geometry{240, 320, 320, 1, 3, 6}},
		{"icc transform", "jpeg/exif-orientation-6-icc.jpg", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.ICCTransform(ctx, "srgb", vips.IntentPerceptual, true, 8)
		}, geometry{240, 320, 320, 1, 3, 6}},
		{"cast", "jpeg/baseline.jpg", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Cast(ctx, vips.BandFormatUshort)
		}, geometry{320, 240, 240, 1, 3, 1}},
		{"copy page height", "jpeg/baseline.jpg", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Copy(ctx, vips.CopyOptions{PageHeight: 120, XRes: 10, YRes: 10})
		}, geometry{320, 240, 120, 2, 3, 1}},
		{"gaussblur", "jpeg/baseline.jpg", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Gaussblur(ctx, 2)
		}, geometry{320, 240, 240, 1, 3, 1}},
		{"sharpen", "jpeg/baseline.jpg", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Sharpen(ctx, vips.DefaultSharpenOptions())
		}, geometry{320, 240, 240, 1, 3, 1}},
		{"shrink", "jpeg/baseline.jpg", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Shrink(ctx, 4, 3)
		}, geometry{80, 80, 80, 1, 3, 1}},
		{"composite2", "jpeg/baseline.jpg", func(ctx context.Context, a *vips.Arena, img *vips.Image) (*vips.Image, error) {
			rgb, err := a.Add(vips.NewFromMemory(ctx, pattern(64, 48), 64, 48, 3, vips.BandFormatUchar))
			if err != nil {
				return nil, err
			}
			mark, err := a.Add(rgb.BandjoinConst(ctx, []float64{128}))
			if err != nil {
				return nil, err
			}
			return img.Composite2(ctx, mark, vips.BlendOver, 250, 190)
		}, geometry{320, 240, 240, 1, 4, 1}},
		{"replicate", "png/palette.png", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Replicate(ctx, 2, 3)
		}, geometry{192, 216, 216, 1, 3, 1}},
		{"join", "png/palette.png", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Join(ctx, img, vips.DirectionHorizontal)
		}, geometry{192, 72, 72, 1, 3, 1}},
		{"arrayjoin", "png/palette.png", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return vips.Arrayjoin(ctx, []*vips.Image{img, img, img, img}, 2)
		}, geometry{192, 144, 144, 1, 3, 1}},
		{"linear", "jpeg/baseline.jpg", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Linear(ctx, []float64{1.1}, []float64{-10})
		}, geometry{320, 240, 240, 1, 3, 1}},
		{"invert", "jpeg/baseline.jpg", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Invert(ctx)
		}, geometry{320, 240, 240, 1, 3, 1}},
		{"bandjoin", "jpeg/baseline.jpg", func(ctx context.Context, a *vips.Arena, img *vips.Image) (*vips.Image, error) {
			alpha, err := a.Add(img.ExtractBand(ctx, 0, 1))
			if err != nil {
				return nil, err
			}
			return vips.Bandjoin(ctx, []*vips.Image{img, alpha})
		}, geometry{320, 240, 240, 1, 4, 1}},
		{"bandjoin const", "jpeg/baseline.jpg", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.BandjoinConst(ctx, []float64{255})
		}, geometry{320, 240, 240, 1, 4, 1}},
		{"extract band", "png/rgba8.png", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.ExtractBand(ctx, 0, 3)
		}, geometry{128, 96, 96, 1, 3, 1}},
		{"add alpha", "jpeg/baseline.jpg", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.AddAlpha(ctx)
		}, geometry{320, 240, 240, 1, 4, 1}},
		{"similarity", "jpeg/baseline.jpg", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Similarity(ctx, 0.5, 90, white)
		}, geometry{120, 160, 160, 1, 3, 1}},
		{"affine", "jpeg/baseline.jpg", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Affine(ctx, 0.5, 0, 0, 0.5, nil)
		}, geometry{160, 120, 120, 1, 3, 1}},
		{"set orientation", "jpeg/baseline.jpg", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.SetOrientation(ctx, 3)
		}, geometry{320, 240, 240, 1, 3, 3}},
		{"remove orientation", "jpeg/exif-orientation-6-icc.jpg", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.RemoveOrientation(ctx)
		}, geometry{240, 320, 320, 1, 3, 1}},
		{"set page height", "png/palette.png", func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.SetPageHeight(ctx, 24)
		}, geometry{96, 72, 24, 3, 3, 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			buf := newBuffer(t, readFixture(t, tc.fixture))
			defer buf.Release()
			job(t, func(ctx context.Context, a *vips.Arena) error {
				img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{}))
				if err != nil {
					return err
				}
				before := geometryOf(img)
				out, err := a.Add(tc.op(ctx, a, img))
				if err != nil {
					return err
				}
				if got := geometryOf(out); got != tc.want {
					t.Errorf("result = %+v, want %+v", got, tc.want)
				}
				if got := geometryOf(img); got != before {
					t.Errorf("input changed from %+v to %+v", before, got)
				}
				// Every graph computes: pull the pixels through a saver.
				_, err = saveBytes(ctx, out, vips.FormatPNG, nil)
				return err
			})
		})
	}
}

// TestAnimatedPageHeight loads every frame of the animated fixtures and
// checks that page-height survives geometric operations and saving.
func TestAnimatedPageHeight(t *testing.T) {
	t.Parallel()

	type result struct{ W, PageHeight, Pages int }
	tests := []struct {
		name    string
		fixture string
		format  vips.Format
		op      opFunc
		want    result
	}{
		{"gif resize half", "gif/animated.gif", vips.FormatGIF, func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Resize(ctx, 0.5, 0, vips.KernelLanczos3)
		}, result{48, 36, 6}},
		{"webp resize odd scale", "webp/animated.webp", vips.FormatWebP, func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Resize(ctx, 0.37, 0, vips.KernelLanczos3)
		}, result{36, 27, 6}},
		{"webp enlarge", "webp/animated.webp", vips.FormatWebP, func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Resize(ctx, 1.51, 0, vips.KernelCubic)
		}, result{145, 109, 6}},
		{"gif crop", "gif/animated.gif", vips.FormatGIF, func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Crop(ctx, image.Rect(10, 20, 60, 60))
		}, result{50, 40, 6}},
		{"gif rot 90", "gif/animated.gif", vips.FormatGIF, func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Rot(ctx, vips.Angle90)
		}, result{72, 96, 6}},
		{"gif rot 180", "gif/animated.gif", vips.FormatGIF, func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Rot(ctx, vips.Angle180)
		}, result{96, 72, 6}},
		{"webp flip vertical", "webp/animated.webp", vips.FormatWebP, func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Flip(ctx, vips.DirectionVertical)
		}, result{96, 72, 6}},
		{"webp embed", "webp/animated.webp", vips.FormatWebP, func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Embed(ctx, 4, 4, 104, 80, vips.ExtendBackground, []float64{0, 0, 0, 0})
		}, result{104, 80, 6}},
		{"gif gravity", "gif/animated.gif", vips.FormatGIF, func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Gravity(ctx, vips.CompassNorth, 64, 64, vips.ExtendBlack, nil)
		}, result{64, 64, 6}},
		{"gif similarity", "gif/animated.gif", vips.FormatGIF, func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Similarity(ctx, 1, 90, nil)
		}, result{72, 96, 6}},
		{"webp autorot", "webp/animated.webp", vips.FormatWebP, func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Autorot(ctx)
		}, result{96, 72, 6}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			buf := newBuffer(t, readFixture(t, tc.fixture))
			defer buf.Release()
			var encoded []byte
			var delay []int
			job(t, func(ctx context.Context, a *vips.Arena) error {
				img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{NPages: -1}))
				if err != nil {
					return err
				}
				delay, _ = img.Ints(vips.FieldDelay)
				out, err := a.Add(tc.op(ctx, a, img))
				if err != nil {
					return err
				}
				if got := (result{out.Width(), out.PageHeight(), out.Pages()}); got != tc.want {
					t.Errorf("result = %+v, want %+v", got, tc.want)
				}
				if out.Height() != tc.want.PageHeight*tc.want.Pages {
					t.Errorf("strip height %d is not %d × %d", out.Height(), tc.want.Pages, tc.want.PageHeight)
				}
				o := vips.DefaultSaveOptions()
				o.GIF.KeepDuplicateFrames = true
				encoded, err = saveBytes(ctx, out, tc.format, &o)
				return err
			})
			h := probeBytes(t, encoded)
			if h.Pages != tc.want.Pages || h.Width != tc.want.W || h.Height != tc.want.PageHeight || !h.IsAnimated {
				t.Errorf("saved %s: %dx%d, %d pages, animated %v; want %dx%d, %d pages",
					tc.format, h.Width, h.Height, h.Pages, h.IsAnimated, tc.want.W, tc.want.PageHeight, tc.want.Pages)
			}
			if !slices.Equal(h.Delay, delay) {
				t.Errorf("saved delays %v, want %v", h.Delay, delay)
			}
		})
	}
}

func TestAnimatedSingleFrameOnlyOps(t *testing.T) {
	t.Parallel()

	buf := newBuffer(t, readFixture(t, "gif/animated.gif"))
	defer buf.Release()
	job(t, func(ctx context.Context, a *vips.Arena) error {
		img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{NPages: -1}))
		if err != nil {
			return err
		}
		if _, err := a.Add(img.SmartCrop(ctx, 10, 10, vips.InterestingAttention)); errs.KindOf(err) != errs.KindInternal {
			t.Errorf("SmartCrop on an animation: %v, want KindInternal", err)
		}
		if _, err := img.FindTrim(ctx, 10, nil, false); errs.KindOf(err) != errs.KindInternal {
			t.Errorf("FindTrim on an animation: %v, want KindInternal", err)
		}
		return nil
	})
}

func TestTrim(t *testing.T) {
	t.Parallel()

	job(t, func(ctx context.Context, a *vips.Arena) error {
		src, err := a.Add(vips.NewFromMemory(ctx, pattern(64, 48), 64, 48, 3, vips.BandFormatUchar))
		if err != nil {
			return err
		}
		framed, err := a.Add(src.Embed(ctx, 20, 10, 120, 80, vips.ExtendWhite, nil))
		if err != nil {
			return err
		}
		want := image.Rect(20, 10, 84, 58)
		for _, bg := range [][]float64{{255, 255, 255}, nil} { // explicit and top-left pixel
			r, err := framed.FindTrim(ctx, 10, bg, false)
			if err != nil {
				return err
			}
			if r != want {
				t.Errorf("FindTrim(bg %v) = %v, want %v", bg, r, want)
			}
		}
		trimmed, err := a.Add(framed.Trim(ctx, 10, nil, false))
		if err != nil {
			return err
		}
		if trimmed.Width() != 64 || trimmed.Height() != 48 {
			t.Errorf("Trim = %dx%d, want 64x48", trimmed.Width(), trimmed.Height())
		}
		// All background: unchanged.
		blank, err := a.Add(vips.Black(ctx, 30, 20, 3))
		if err != nil {
			return err
		}
		same, err := a.Add(blank.Trim(ctx, 10, nil, false))
		if err != nil {
			return err
		}
		if same.Width() != 30 || same.Height() != 20 {
			t.Errorf("Trim of a blank image = %dx%d, want 30x20", same.Width(), same.Height())
		}
		return nil
	})
}

func TestPixelValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		pix   []byte
		w     int
		bands int
		op    opFunc
		want  []byte
	}{
		{"invert", []byte{0, 128, 255, 10, 20, 30}, 2, 3, func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Invert(ctx)
		}, []byte{255, 127, 0, 245, 235, 225}},
		{"linear", []byte{10, 20, 30}, 1, 3, func(ctx context.Context, a *vips.Arena, img *vips.Image) (*vips.Image, error) {
			l, err := a.Add(img.Linear(ctx, []float64{2}, []float64{1}))
			if err != nil {
				return nil, err
			}
			return l.Cast(ctx, vips.BandFormatUchar)
		}, []byte{21, 41, 61}},
		{"flatten transparent on white", []byte{0, 0, 0, 0}, 1, 4, func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Flatten(ctx, []float64{255, 255, 255})
		}, []byte{255, 255, 255}},
		{"flatten opaque", []byte{1, 2, 3, 255}, 1, 4, func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Flatten(ctx, []float64{255, 255, 255})
		}, []byte{1, 2, 3}},
		{"composite over", []byte{255, 255, 255}, 1, 3, func(ctx context.Context, a *vips.Arena, img *vips.Image) (*vips.Image, error) {
			over, err := a.Add(vips.NewFromMemory(ctx, []byte{0, 0, 0, 255}, 1, 1, 4, vips.BandFormatUchar))
			if err != nil {
				return nil, err
			}
			c, err := a.Add(img.Composite2(ctx, over, vips.BlendOver, 0, 0))
			if err != nil {
				return nil, err
			}
			c8, err := a.Add(c.Cast(ctx, vips.BandFormatUchar))
			if err != nil {
				return nil, err
			}
			return c8.ExtractBand(ctx, 0, 3)
		}, []byte{0, 0, 0}},
		{"flip", []byte{1, 2, 3, 4, 5, 6}, 2, 3, func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Flip(ctx, vips.DirectionHorizontal)
		}, []byte{4, 5, 6, 1, 2, 3}},
		{"bandjoin const", []byte{7}, 1, 1, func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.BandjoinConst(ctx, []float64{9, 11})
		}, []byte{7, 9, 11}},
		{"cast round", []byte{10, 20, 254}, 1, 3, func(ctx context.Context, a *vips.Arena, img *vips.Image) (*vips.Image, error) {
			l, err := a.Add(img.Linear(ctx, []float64{1, 1, 1}, []float64{0.6, 0.4, 3}))
			if err != nil {
				return nil, err
			}
			return l.CastRound(ctx, vips.BandFormatUchar)
		}, []byte{11, 20, 255}},
		{"maplut per band", []byte{0, 10, 200, 7}, 1, 4, func(ctx context.Context, a *vips.Arena, img *vips.Image) (*vips.Image, error) {
			lut := make([]byte, 256*4)
			for i := range 256 {
				lut[4*i], lut[4*i+1], lut[4*i+2], lut[4*i+3] = byte(255-i), byte(i/2), byte(i), byte(i)
			}
			l, err := a.Add(vips.NewFromMemory(ctx, lut, 256, 1, 4, vips.BandFormatUchar))
			if err != nil {
				return nil, err
			}
			return img.MapLUT(ctx, l)
		}, []byte{255, 5, 200, 7}},
		{"maplut grey to colour", []byte{0, 100}, 2, 1, func(ctx context.Context, a *vips.Arena, img *vips.Image) (*vips.Image, error) {
			lut := make([]byte, 256*3)
			for i := range 256 {
				lut[3*i], lut[3*i+1], lut[3*i+2] = byte(i), 7, byte(255-i)
			}
			l, err := a.Add(vips.NewFromMemory(ctx, lut, 256, 1, 3, vips.BandFormatUchar))
			if err != nil {
				return nil, err
			}
			return img.MapLUT(ctx, l)
		}, []byte{0, 7, 255, 100, 7, 155}},
		{"recomb rounds and clamps", []byte{100, 150, 200}, 1, 3, func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Recomb(ctx, []float64{
				0.5, 0.5, 0, // 125
				0.2126, 0.7152, 0.0722, // 142.9 → 143
				1, 1, 0, // 250
				2, 0, 0, // clamped
			}, 4)
		}, []byte{125, 143, 250, 200}},
		{"mix", []byte{0, 100, 255}, 1, 3, func(ctx context.Context, a *vips.Arena, img *vips.Image) (*vips.Image, error) {
			other, err := a.Add(vips.NewFromMemory(ctx, []byte{200, 0, 255}, 1, 1, 3, vips.BandFormatUchar))
			if err != nil {
				return nil, err
			}
			return img.Mix(ctx, other, 0.25)
		}, []byte{50, 75, 255}},
		{"conv3 correlates and copies edges", []byte{0, 100, 200}, 3, 1, func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			// The right neighbour's weight: each pixel takes its right neighbour's value.
			return img.Conv3(ctx, &[9]float64{0, 0, 0, 0, 0, 1, 0, 0, 0})
		}, []byte{100, 200, 200}},
		{"conv3 clamps", []byte{10, 100, 250}, 3, 1, func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Conv3(ctx, &[9]float64{0, 0, 0, 0, 2, 0, 0, 0, 0})
		}, []byte{20, 200, 255}},
		{"unsharp keeps flat areas", []byte{50, 50, 50, 50}, 4, 1, func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Unsharp(ctx, 1, 3)
		}, []byte{50, 50, 50, 50}},
		{"insert", []byte{1, 2, 3}, 3, 1, func(ctx context.Context, a *vips.Arena, img *vips.Image) (*vips.Image, error) {
			sub, err := a.Add(vips.NewFromMemory(ctx, []byte{9}, 1, 1, 1, vips.BandFormatUchar))
			if err != nil {
				return nil, err
			}
			return img.Insert(ctx, sub, 1, 0)
		}, []byte{1, 9, 3}},
		{"blend mask", []byte{0, 0, 7}, 3, 1, func(ctx context.Context, a *vips.Arena, img *vips.Image) (*vips.Image, error) {
			over, err := a.Add(vips.NewFromMemory(ctx, []byte{200, 100, 100}, 3, 1, 1, vips.BandFormatUchar))
			if err != nil {
				return nil, err
			}
			mask, err := a.Add(vips.NewFromMemory(ctx, []byte{0, 51, 255}, 3, 1, 1, vips.BandFormatUchar))
			if err != nil {
				return nil, err
			}
			return img.BlendMask(ctx, mask, over)
		}, []byte{0, 20, 100}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			job(t, func(ctx context.Context, a *vips.Arena) error {
				h := len(tc.pix) / tc.w / tc.bands
				img, err := a.Add(vips.NewFromMemory(ctx, tc.pix, tc.w, h, tc.bands, vips.BandFormatUchar))
				if err != nil {
					return err
				}
				out, err := a.Add(tc.op(ctx, a, img))
				if err != nil {
					return err
				}
				got, err := out.WriteToMemory(ctx)
				if err != nil {
					return err
				}
				if diff := cmp.Diff(tc.want, got); diff != "" {
					t.Errorf("pixels (-want +got):\n%s", diff)
				}
				return nil
			})
		})
	}
}

// TestFloatOpsKeepFractions checks that ops returning the input's band
// format keep fractional results for float inputs (scRGB, HDR) instead of
// rounding them like integer outputs.
func TestFloatOpsKeepFractions(t *testing.T) {
	t.Parallel()

	floats := func(vs ...float32) []byte {
		b := make([]byte, 0, 4*len(vs))
		for _, v := range vs {
			b = binary.NativeEndian.AppendUint32(b, math.Float32bits(v))
		}
		return b
	}
	tests := []struct {
		name  string
		pix   []float32
		bands int
		op    opFunc
		want  []float32
	}{
		{"recomb luminance", []float32{0.25, 0.5, 0.125}, 3, func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Recomb(ctx, []float64{0.2126, 0.7152, 0.0722}, 1)
		}, []float32{0.2126*0.25 + 0.7152*0.5 + 0.0722*0.125}},
		{"recomb above one", []float32{1.75, 3.5, 0.25}, 3, func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Recomb(ctx, []float64{0.5, 0.5, 0, 0, 0, 1}, 2)
		}, []float32{2.625, 0.25}},
		{"mix", []float32{0.1, 2.5, 0}, 3, func(ctx context.Context, a *vips.Arena, img *vips.Image) (*vips.Image, error) {
			other, err := a.Add(vips.NewFromMemory(ctx, floats(0.9, 0.5, 0.3), 1, 1, 3, vips.BandFormatFloat))
			if err != nil {
				return nil, err
			}
			return img.Mix(ctx, other, 0.25)
		}, []float32{0.3, 2, 0.075}},
		{"cast round to float keeps fractions", []float32{0.4, 1.6}, 2, func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.CastRound(ctx, vips.BandFormatFloat)
		}, []float32{0.4, 1.6}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			job(t, func(ctx context.Context, a *vips.Arena) error {
				img, err := a.Add(vips.NewFromMemory(ctx, floats(tc.pix...), 1, 1, tc.bands, vips.BandFormatFloat))
				if err != nil {
					return err
				}
				out, err := a.Add(tc.op(ctx, a, img))
				if err != nil {
					return err
				}
				if out.BandFormat() != vips.BandFormatFloat {
					t.Errorf("band format = %v, want float", out.BandFormat())
				}
				raw, err := out.WriteToMemory(ctx)
				if err != nil {
					return err
				}
				got := make([]float32, len(raw)/4)
				for i := range got {
					got[i] = math.Float32frombits(binary.NativeEndian.Uint32(raw[4*i:]))
				}
				if diff := cmp.Diff(tc.want, got, cmpopts.EquateApprox(0, 1e-6)); diff != "" {
					t.Errorf("pixels (-want +got):\n%s", diff)
				}
				return nil
			})
		})
	}
}

func TestMetadata(t *testing.T) {
	t.Parallel()

	buf := newBuffer(t, readFixture(t, "jpeg/exif-orientation-6-icc.jpg"))
	defer buf.Release()
	job(t, func(ctx context.Context, a *vips.Arena) error {
		img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{}))
		if err != nil {
			return err
		}
		icc, ok := img.Blob(vips.FieldICC)
		if !ok || len(icc) < 128 {
			t.Fatalf("ICC profile: %d bytes, present %v", len(icc), ok)
		}
		if _, ok := img.Blob(vips.FieldEXIF); !ok {
			t.Error("EXIF missing")
		}
		if o, ok := img.Int(vips.FieldOrientation); !ok || o != 6 {
			t.Errorf("orientation = %d, %v; want 6", o, ok)
		}
		if _, ok := img.Blob(vips.FieldXMP); ok {
			t.Error("fixture unexpectedly has XMP")
		}

		xmp := []byte(`<x:xmpmeta xmlns:x="adobe:ns:meta/"/>`)
		withXMP, err := a.Add(img.SetBlob(ctx, vips.FieldXMP, xmp))
		if err != nil {
			return err
		}
		if got, _ := withXMP.Blob(vips.FieldXMP); !bytes.Equal(got, xmp) {
			t.Errorf("XMP = %q, want %q", got, xmp)
		}
		if _, ok := img.Blob(vips.FieldXMP); ok {
			t.Error("SetBlob modified its input")
		}

		stripped, err := a.Add(withXMP.RemoveMetadata(ctx, vips.FieldEXIF, vips.FieldICC, vips.FieldXMP, "missing-field"))
		if err != nil {
			return err
		}
		for _, f := range []vips.MetaField{vips.FieldEXIF, vips.FieldICC, vips.FieldXMP} {
			if _, ok := stripped.Blob(f); ok {
				t.Errorf("%s still present after RemoveMetadata", f)
			}
			if f != vips.FieldXMP {
				if _, ok := img.Blob(f); !ok {
					t.Errorf("RemoveMetadata removed %s from its input", f)
				}
			}
		}

		// Keep flags decide what the saver writes.
		keepAll := vips.DefaultSaveOptions()
		keepAll.Keep = vips.KeepICC | vips.KeepEXIF
		kept, err := saveBytes(ctx, img, vips.FormatJPEG, &keepAll)
		if err != nil {
			return err
		}
		none, err := saveBytes(ctx, img, vips.FormatJPEG, nil)
		if err != nil {
			return err
		}
		if h := probeBytes(t, kept); !h.HasICC || h.Orientation != 6 {
			t.Errorf("KeepICC|KeepEXIF: HasICC %v orientation %d, want true and 6", h.HasICC, h.Orientation)
		}
		if h := probeBytes(t, none); h.HasICC || h.Orientation != 1 {
			t.Errorf("KeepNone: HasICC %v orientation %d, want false and 1", h.HasICC, h.Orientation)
		}
		return nil
	})
}

func TestAnimationMetadata(t *testing.T) {
	t.Parallel()

	buf := newBuffer(t, readFixture(t, "gif/animated.gif"))
	defer buf.Release()
	var encoded []byte
	job(t, func(ctx context.Context, a *vips.Arena) error {
		img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{NPages: -1}))
		if err != nil {
			return err
		}
		delay, ok := img.Ints(vips.FieldDelay)
		if !ok || len(delay) != 6 {
			t.Fatalf("delay = %v, %v", delay, ok)
		}
		faster, err := a.Add(img.SetInts(ctx, vips.FieldDelay, []int{50, 60, 70, 80, 90, 100}))
		if err != nil {
			return err
		}
		looped, err := a.Add(faster.SetInt(ctx, vips.FieldLoop, 3))
		if err != nil {
			return err
		}
		encoded, err = saveBytes(ctx, looped, vips.FormatGIF, nil)
		return err
	})
	h := probeBytes(t, encoded)
	if want := []int{50, 60, 70, 80, 90, 100}; !slices.Equal(h.Delay, want) || h.Loop != 3 {
		t.Errorf("saved delay %v loop %d, want %v and 3", h.Delay, h.Loop, want)
	}
}

func TestNewFromMemoryErrors(t *testing.T) {
	t.Parallel()

	err := tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
		_, err := a.Add(vips.NewFromMemory(ctx, []byte{1, 2, 3}, 2, 2, 3, vips.BandFormatUchar))
		return err
	})
	if errs.KindOf(err) != errs.KindInternal {
		t.Errorf("short pixel buffer: %v, want KindInternal", err)
	}
	err = tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
		_, err := a.Add(vips.NewFromMemory(ctx, nil, 0, 0, 3, vips.BandFormatUchar))
		return err
	})
	if errs.KindOf(err) != errs.KindInternal {
		t.Errorf("empty pixel buffer: %v, want KindInternal", err)
	}
}

func TestArena(t *testing.T) {
	t.Parallel()

	job(t, func(ctx context.Context, _ *vips.Arena) error {
		var a vips.Arena
		img, err := a.Add(vips.Black(ctx, 4, 4, 1))
		if err != nil {
			return err
		}
		a.Track(nil)
		if _, err := a.Add(img.ExtractArea(ctx, image.Rect(0, 0, 10, 10))); err == nil {
			t.Error("ExtractArea outside the image succeeded")
		}
		if a.Len() != 1 {
			t.Errorf("Len = %d, want 1 (failed results are not tracked)", a.Len())
		}
		a.Close()
		a.Close() // idempotent
		if a.Len() != 0 {
			t.Errorf("Len after Close = %d", a.Len())
		}
		return nil
	})
}

func TestGradientMask(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name               string
		w, h               int
		ax, ay, b, opacity float64
		want               []byte
	}{
		{"horizontal", 4, 1, 85, 0, 0, 1, []byte{0, 85, 170, 255}},
		{"opacity", 4, 1, 85, 0, 0, 0.4, []byte{0, 34, 68, 102}},
		{"clamped", 4, 1, 85, 0, -100, 1, []byte{0, 0, 70, 155}},
		{"vertical reversed", 1, 3, 0, -200, 300, 1, []byte{255, 100, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			job(t, func(ctx context.Context, a *vips.Arena) error {
				m, err := a.Add(vips.GradientMask(ctx, tc.w, tc.h, tc.ax, tc.ay, tc.b, tc.opacity))
				if err != nil {
					return err
				}
				if m.Bands() != 1 || m.BandFormat() != vips.BandFormatUchar {
					t.Errorf("mask has %d bands of %v", m.Bands(), m.BandFormat())
				}
				got, err := m.WriteToMemory(ctx)
				if err != nil {
					return err
				}
				if diff := cmp.Diff(tc.want, got); diff != "" {
					t.Errorf("pixels (-want +got):\n%s", diff)
				}
				return nil
			})
		})
	}
}
