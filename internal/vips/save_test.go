//go:build cgo

package vips_test

import (
	"context"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// TestSaveRoundTrip encodes with every saver and every notable option and
// reads the result back with the matching loader.
func TestSaveRoundTrip(t *testing.T) {
	t.Parallel()

	type mod func(o *vips.SaveOptions)
	tests := []struct {
		name    string
		fixture string
		format  vips.Format
		mod     mod
		alpha   bool
	}{
		{"jpeg default", "jpeg/baseline.jpg", vips.FormatJPEG, nil, false},
		{"jpeg progressive 4:4:4", "jpeg/baseline.jpg", vips.FormatJPEG, func(o *vips.SaveOptions) {
			o.JPEG = vips.JPEGOptions{Quality: 92, Interlace: true, OptimizeCoding: true, Subsample: vips.SubsampleOff, RestartInterval: 4}
		}, false},
		{"jpeg from alpha", "png/rgba8.png", vips.FormatJPEG, nil, false},
		{"png default", "png/rgba8.png", vips.FormatPNG, nil, true},
		{"png palette interlaced", "png/rgba8.png", vips.FormatPNG, func(o *vips.SaveOptions) {
			o.PNG = vips.PNGOptions{Compression: 9, Interlace: true, Filter: vips.PNGFilterAll, Palette: true, Quality: 80, Dither: 0.5, BitDepth: 8, Effort: 3}
		}, true},
		{"png 16-bit", "png/rgb16.png", vips.FormatPNG, func(o *vips.SaveOptions) { o.PNG.BitDepth = 16 }, false},
		{"webp lossy", "jpeg/baseline.jpg", vips.FormatWebP, nil, false},
		{"webp lossless", "png/rgba8.png", vips.FormatWebP, func(o *vips.SaveOptions) {
			o.WebP.Lossless = true
			o.WebP.Effort = 6
			o.WebP.Exact = true
		}, true},
		{"webp near lossless", "png/palette.png", vips.FormatWebP, func(o *vips.SaveOptions) {
			o.WebP.NearLossless = true
			o.WebP.Quality = 60
			o.WebP.Preset = vips.WebPPresetDrawing
		}, false},
		{"webp smart", "jpeg/baseline.jpg", vips.FormatWebP, func(o *vips.SaveOptions) {
			o.WebP.SmartSubsample = true
			o.WebP.SmartDeblock = true
			o.WebP.Effort = 0
			o.WebP.AlphaQuality = 50
		}, false},
		{"gif", "png/palette.png", vips.FormatGIF, nil, false},
		{"gif low depth", "jpeg/baseline.jpg", vips.FormatGIF, func(o *vips.SaveOptions) {
			o.GIF = vips.GIFOptions{Dither: 0, Effort: 1, BitDepth: 4, Interlace: true, InterframeMaxError: 8, InterpaletteMaxError: 40}
		}, false},
		{"avif", "jpeg/baseline.jpg", vips.FormatAVIF, func(o *vips.SaveOptions) { o.HEIF.Effort = 0 }, false},
		{"avif alpha 4:4:4", "png/rgba8.png", vips.FormatAVIF, func(o *vips.SaveOptions) {
			o.HEIF = vips.HEIFOptions{Quality: 70, Effort: 1, Subsample: vips.SubsampleOff, BitDepth: 8}
		}, true},
		{"avif lossless", "png/palette.png", vips.FormatAVIF, func(o *vips.SaveOptions) {
			o.HEIF.Lossless = true
			o.HEIF.Effort = 0
		}, false},
		{"jxl", "jpeg/baseline.jpg", vips.FormatJXL, func(o *vips.SaveOptions) { o.JXL.Effort = 3 }, false},
		{"jxl distance", "png/rgba8.png", vips.FormatJXL, func(o *vips.SaveOptions) {
			o.JXL = vips.JXLOptions{Distance: 2.5, Effort: 1, Tier: 2}
		}, true},
		{"jxl lossless", "png/palette.png", vips.FormatJXL, func(o *vips.SaveOptions) {
			o.JXL.Lossless = true
			o.JXL.Effort = 1
		}, false},
		{"tiff", "jpeg/baseline.jpg", vips.FormatTIFF, nil, false},
		{"tiff jpeg", "jpeg/baseline.jpg", vips.FormatTIFF, func(o *vips.SaveOptions) {
			o.TIFF.Compression = vips.TIFFCompressionJPEG
			o.TIFF.Quality = 60
		}, false},
		{"tiff deflate", "png/rgba8.png", vips.FormatTIFF, func(o *vips.SaveOptions) {
			o.TIFF = vips.TIFFOptions{Compression: vips.TIFFCompressionDeflate, Predictor: vips.TIFFPredictorHorizontal, Level: 9}
		}, true},
		{"tiff webp", "png/palette.png", vips.FormatTIFF, func(o *vips.SaveOptions) {
			o.TIFF.Compression = vips.TIFFCompressionWebP
			o.TIFF.Lossless = true
		}, false},
		{"tiff lzw 1-bit", "png/palette.png", vips.FormatTIFF, func(o *vips.SaveOptions) {
			o.TIFF.Compression = vips.TIFFCompressionLZW
			o.TIFF.BitDepth = 1
		}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			buf := newBuffer(t, readFixture(t, tc.fixture))
			defer buf.Release()
			o := vips.DefaultSaveOptions()
			if tc.mod != nil {
				tc.mod(&o)
			}
			var w, h int
			var encoded []byte
			job(t, func(ctx context.Context, a *vips.Arena) error {
				img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{}))
				if err != nil {
					return err
				}
				if tc.format == vips.FormatJPEG && img.HasAlpha() {
					if img, err = a.Add(img.Flatten(ctx, []float64{255, 255, 255})); err != nil {
						return err
					}
				}
				w, h = img.Width(), img.Height()
				encoded, err = saveBytes(ctx, img, tc.format, &o)
				return err
			})
			got := probeBytes(t, encoded)
			if got.Format != tc.format || got.Width != w || got.Height != h || got.HasAlpha != tc.alpha {
				t.Errorf("round trip = %s %dx%d alpha %v, want %s %dx%d alpha %v",
					got.Format, got.Width, got.Height, got.HasAlpha, tc.format, w, h, tc.alpha)
			}
			// The loader decodes the whole output.
			decoded := newBuffer(t, encoded)
			defer decoded.Release()
			job(t, func(ctx context.Context, a *vips.Arena) error {
				img, err := a.Add(vips.Load(ctx, decoded, &vips.LoadOptions{}))
				if err != nil {
					return err
				}
				_, err = img.WriteToMemory(ctx)
				return err
			})
		})
	}
}

func TestSaveUnsupported(t *testing.T) {
	t.Parallel()

	for _, f := range []vips.Format{vips.FormatHEIF, vips.FormatSVG, vips.FormatJP2K, vips.FormatUnknown, vips.Format(99)} {
		t.Run(f.String(), func(t *testing.T) {
			t.Parallel()
			err := tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
				img, err := a.Add(vips.Black(ctx, 8, 8, 3))
				if err != nil {
					return err
				}
				_, err = saveBytes(ctx, img, f, nil)
				return err
			})
			// HEVC stays off unless Config.HEICEncode (TestMain uses the
			// default config).
			if errs.KindOf(err) != errs.KindUnsupported {
				t.Errorf("Save(%s) = %v, want KindUnsupported", f, err)
			}
		})
	}
}

func TestBlobRefcount(t *testing.T) {
	t.Parallel()

	job(t, func(ctx context.Context, a *vips.Arena) error {
		img, err := a.Add(vips.Black(ctx, 8, 8, 3))
		if err != nil {
			return err
		}
		blob, err := img.Save(ctx, vips.FormatPNG, nil)
		if err != nil {
			return err
		}
		n := blob.Len()
		blob.Retain()
		blob.Release()
		if blob.Len() != n || len(blob.Bytes()) != n {
			t.Errorf("blob changed while a reference is held")
		}
		blob.Release()
		if blob.Bytes() != nil || blob.Len() != 0 {
			t.Errorf("Bytes after the final Release is not empty")
		}
		blob.Release() // extra releases are no-ops
		blob.Retain()  // and so is retaining a released blob
		return nil
	})
}
