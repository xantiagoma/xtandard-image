//go:build cgo

package vips_test

import (
	"context"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// geometry is what the load and op tests assert about an image.
type geometry struct {
	W, H, PageHeight, Pages, Bands int
	Orientation                    int
}

func geometryOf(img *vips.Image) geometry {
	return geometry{
		W: img.Width(), H: img.Height(), PageHeight: img.PageHeight(), Pages: img.Pages(),
		Bands: img.Bands(), Orientation: img.Orientation(),
	}
}

func TestLoadOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		fixture string
		opts    vips.LoadOptions
		want    geometry
	}{
		{"jpeg", "jpeg/baseline.jpg", vips.LoadOptions{}, geometry{320, 240, 240, 1, 3, 1}},
		{"jpeg shrink 2", "jpeg/baseline.jpg", vips.LoadOptions{Shrink: 2}, geometry{160, 120, 120, 1, 3, 1}},
		{"jpeg shrink 4", "jpeg/baseline.jpg", vips.LoadOptions{Shrink: 4}, geometry{80, 60, 60, 1, 3, 1}},
		{"jpeg shrink 8", "jpeg/baseline.jpg", vips.LoadOptions{Shrink: 8}, geometry{40, 30, 30, 1, 3, 1}},
		{"jpeg sequential", "jpeg/progressive.jpg", vips.LoadOptions{Access: vips.AccessSequential}, geometry{320, 240, 240, 1, 3, 1}},
		{"uhdr shrink 2", "uhdr/photo-ultrahdr.jpg", vips.LoadOptions{Shrink: 2}, geometry{160, 120, 120, 1, 3, 1}},
		{"exif 6 as stored", "jpeg/exif-orientation-6-icc.jpg", vips.LoadOptions{}, geometry{240, 320, 320, 1, 3, 6}},
		{"exif 6 autorotated", "jpeg/exif-orientation-6-icc.jpg", vips.LoadOptions{Autorotate: true}, geometry{320, 240, 240, 1, 3, 1}},
		{"png rgba", "png/rgba8.png", vips.LoadOptions{}, geometry{128, 96, 96, 1, 4, 1}},
		{"png shrink ignored", "png/palette.png", vips.LoadOptions{Shrink: 2}, geometry{96, 72, 72, 1, 3, 1}},
		{"webp scale", "webp/lossy.webp", vips.LoadOptions{Scale: 0.5}, geometry{160, 120, 120, 1, 3, 1}},
		{"webp all frames", "webp/animated.webp", vips.LoadOptions{NPages: -1}, geometry{96, 432, 72, 6, 4, 1}},
		{"webp frames 2..3", "webp/animated.webp", vips.LoadOptions{Page: 2, NPages: 2}, geometry{96, 144, 72, 2, 4, 1}},
		{"gif first frame", "gif/animated.gif", vips.LoadOptions{}, geometry{96, 72, 72, 1, 3, 1}},
		{"gif all frames", "gif/animated.gif", vips.LoadOptions{NPages: -1}, geometry{96, 432, 72, 6, 3, 1}},
		{"tiff page 1", "tiff/multipage.tif", vips.LoadOptions{Page: 1}, geometry{96, 72, 72, 1, 3, 1}},
		{"avif", "avif/photo.avif", vips.LoadOptions{}, geometry{320, 240, 240, 1, 3, 1}},
		{"heic", "heic/photo.heic", vips.LoadOptions{}, geometry{320, 240, 240, 1, 3, 1}},
		{"jxl", "jxl/photo.jxl", vips.LoadOptions{}, geometry{320, 240, 240, 1, 3, 1}},
		{"jp2k", "jp2/photo.jp2", vips.LoadOptions{}, geometry{160, 120, 120, 1, 3, 1}},
		{"svg", "svg/logo.svg", vips.LoadOptions{}, geometry{96, 72, 72, 1, 4, 1}},
		{"svg scale 2", "svg/logo.svg", vips.LoadOptions{Scale: 2}, geometry{192, 144, 144, 1, 4, 1}},
		{"svg 144 dpi", "svg/logo.svg", vips.LoadOptions{Density: 144}, geometry{192, 144, 144, 1, 4, 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			buf := newBuffer(t, readFixture(t, tc.fixture))
			defer buf.Release()
			job(t, func(ctx context.Context, a *vips.Arena) error {
				img, err := a.Add(vips.Load(ctx, buf, &tc.opts))
				if err != nil {
					return err
				}
				if got := geometryOf(img); got != tc.want {
					t.Errorf("Load = %+v, want %+v", got, tc.want)
				}
				// The pixels decode: the pipeline runs to the end.
				_, err = saveBytes(ctx, img, vips.FormatPNG, nil)
				return err
			})
		})
	}
}

func TestLoadErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		fixture string
		opts    vips.LoadOptions
		want    errs.Kind
	}{
		{"pixels over", "jpeg/baseline.jpg", vips.LoadOptions{MaxPixels: 320*240 - 1}, errs.KindLimitExceeded},
		{"pixels at", "jpeg/baseline.jpg", vips.LoadOptions{MaxPixels: 320 * 240}, errs.KindUnknown},
		{"pixels after shrink", "jpeg/baseline.jpg", vips.LoadOptions{Shrink: 2, MaxPixels: 160 * 120}, errs.KindUnknown},
		{"all frames count", "gif/animated.gif", vips.LoadOptions{NPages: -1, MaxPixels: 96*72*6 - 1}, errs.KindLimitExceeded},
		{"frames over", "gif/animated.gif", vips.LoadOptions{NPages: -1, MaxFrames: 5}, errs.KindLimitExceeded},
		{"frames at", "webp/animated.webp", vips.LoadOptions{NPages: -1, MaxFrames: 6}, errs.KindUnknown},
		{"one frame loaded", "gif/animated.gif", vips.LoadOptions{MaxFrames: 1}, errs.KindUnknown},
		// The fixture's pages differ in size (160×120, 96×72): no strip.
		{"tiff pages differ", "tiff/multipage.tif", vips.LoadOptions{NPages: -1}, errs.KindCorrupt},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			buf := newBuffer(t, readFixture(t, tc.fixture))
			defer buf.Release()
			err := tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
				_, err := a.Add(vips.Load(ctx, buf, &tc.opts))
				return err
			})
			if got := errs.KindOf(err); got != tc.want {
				t.Errorf("Load error kind = %v (%v), want %v", got, err, tc.want)
			}
		})
	}
}

func TestThumbnailOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		fixture string
		opts    vips.ThumbnailOptions
		want    geometry
	}{
		{"fit", "jpeg/baseline.jpg", vips.ThumbnailOptions{Width: 100, Height: 100}, geometry{100, 75, 75, 1, 3, 1}},
		{"crop centre", "jpeg/baseline.jpg", vips.ThumbnailOptions{Width: 100, Height: 100, Crop: vips.InterestingCentre}, geometry{100, 100, 100, 1, 3, 1}},
		{"crop attention", "jpeg/baseline.jpg", vips.ThumbnailOptions{Width: 64, Height: 128, Crop: vips.InterestingAttention}, geometry{64, 128, 128, 1, 3, 1}},
		{"crop entropy", "png/rgba8.png", vips.ThumbnailOptions{Width: 50, Height: 50, Crop: vips.InterestingEntropy}, geometry{50, 50, 50, 1, 4, 1}},
		{"size down never enlarges", "png/palette.png", vips.ThumbnailOptions{Width: 500, Size: vips.SizeDown}, geometry{96, 72, 72, 1, 3, 1}},
		{"size both enlarges", "png/palette.png", vips.ThumbnailOptions{Width: 192, Size: vips.SizeBoth}, geometry{192, 144, 144, 1, 3, 1}},
		{"size up never shrinks", "jpeg/baseline.jpg", vips.ThumbnailOptions{Width: 100, Size: vips.SizeUp}, geometry{320, 240, 240, 1, 3, 1}},
		{"force", "jpeg/baseline.jpg", vips.ThumbnailOptions{Width: 100, Height: 100, Size: vips.SizeForce}, geometry{100, 100, 100, 1, 3, 1}},
		{"autorotated", "jpeg/exif-orientation-6-icc.jpg", vips.ThumbnailOptions{Width: 160}, geometry{160, 120, 120, 1, 3, 1}},
		{"no rotate", "jpeg/exif-orientation-6-icc.jpg", vips.ThumbnailOptions{Width: 120, NoRotate: true}, geometry{120, 160, 160, 1, 3, 6}},
		{"linear", "jpeg/baseline.jpg", vips.ThumbnailOptions{Width: 80, Linear: true}, geometry{80, 60, 60, 1, 3, 1}},
		{"import profile", "jpeg/baseline.jpg", vips.ThumbnailOptions{Width: 80, ImportProfile: "srgb", ExportProfile: "srgb"}, geometry{80, 60, 60, 1, 3, 1}},
		{"animated all frames", "gif/animated.gif", vips.ThumbnailOptions{Width: 48, NPages: -1}, geometry{48, 216, 36, 6, 3, 1}},
		{"animated webp", "webp/animated.webp", vips.ThumbnailOptions{Width: 48, NPages: -1}, geometry{48, 216, 36, 6, 4, 1}},
		{"tiff page 1", "tiff/multipage.tif", vips.ThumbnailOptions{Width: 80, Page: 1}, geometry{80, 60, 60, 1, 3, 1}},
		{"svg renders at size", "svg/logo.svg", vips.ThumbnailOptions{Width: 480}, geometry{480, 360, 360, 1, 4, 1}},
		{"heic", "heic/photo.heic", vips.ThumbnailOptions{Width: 64}, geometry{64, 48, 48, 1, 3, 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			buf := newBuffer(t, readFixture(t, tc.fixture))
			defer buf.Release()
			job(t, func(ctx context.Context, a *vips.Arena) error {
				img, err := a.Add(vips.Thumbnail(ctx, buf, &tc.opts))
				if err != nil {
					return err
				}
				if got := geometryOf(img); got != tc.want {
					t.Errorf("Thumbnail = %+v, want %+v", got, tc.want)
				}
				_, err = saveBytes(ctx, img, vips.FormatPNG, nil)
				return err
			})
		})
	}
}
