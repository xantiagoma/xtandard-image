//go:build cgo

package vips_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/vips"
)

func TestToRGB8(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		pix   []byte
		bands int
		prep  func(ctx context.Context, a *vips.Arena, img *vips.Image) (*vips.Image, error)
		want  []byte
	}{
		{"rgb unchanged", []byte{1, 2, 3, 4, 5, 6}, 3, nil, []byte{1, 2, 3, 4, 5, 6}},
		{"one band replicated", []byte{10, 20}, 1, nil, []byte{10, 10, 10, 20, 20, 20}},
		{"grey and alpha", []byte{10, 255, 20, 0}, 2, nil, []byte{10, 10, 10, 255, 255, 255}},
		{"alpha flattened onto white", []byte{10, 20, 30, 255, 100, 100, 100, 0}, 4, nil, []byte{10, 20, 30, 255, 255, 255}},
		{"grey interpretation to srgb", []byte{50, 50, 50, 200, 200, 200}, 3, func(ctx context.Context, _ *vips.Arena, img *vips.Image) (*vips.Image, error) {
			return img.Colourspace(ctx, vips.InterpretationBW)
		}, []byte{50, 50, 50, 200, 200, 200}},
		{"16-bit to 8-bit", []byte{0, 0, 0, 255, 255, 255}, 3, func(ctx context.Context, a *vips.Arena, img *vips.Image) (*vips.Image, error) {
			srgb, err := a.Add(img.Copy(ctx, vips.CopyOptions{Interpretation: vips.InterpretationSRGB}))
			if err != nil {
				return nil, err
			}
			return srgb.Colourspace(ctx, vips.InterpretationRGB16)
		}, []byte{0, 0, 0, 255, 255, 255}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			job(t, func(ctx context.Context, a *vips.Arena) error {
				img, err := a.Add(vips.NewFromMemory(ctx, tc.pix, 2, 1, tc.bands, vips.BandFormatUchar))
				if err != nil {
					return err
				}
				if tc.prep != nil {
					if img, err = a.Add(tc.prep(ctx, a, img)); err != nil {
						return err
					}
				}
				blob, err := img.ToRGB8(ctx)
				if err != nil {
					return err
				}
				defer blob.Release()
				if diff := cmp.Diff(tc.want, blob.Bytes()); diff != "" {
					t.Errorf("pixels (-want +got):\n%s", diff)
				}
				return nil
			})
		})
	}
}

func TestWritePixelsCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	err := tryJob(t.Context(), func(_ context.Context, a *vips.Arena) error {
		img, err := a.Add(vips.Black(ctx, 8, 8, 3))
		if err != nil {
			return err
		}
		cancel()
		_, err = img.WritePixels(ctx)
		return err
	})
	if err == nil {
		t.Fatal("WritePixels ran with a canceled context")
	}
}
