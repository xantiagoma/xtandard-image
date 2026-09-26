//go:build cgo

package vips_test

import (
	"context"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

func TestText(t *testing.T) {
	t.Parallel()
	job(t, func(ctx context.Context, a *vips.Arena) error {
		small, dpi, err := vips.Text(ctx, &vips.TextOptions{Markup: "<b>Hi</b> there", Font: "sans 16", DPI: 72})
		if small, err = a.Add(small, err); err != nil {
			return err
		}
		if dpi != 72 || small.Bands() != 4 || small.BandFormat() != vips.BandFormatUchar || small.Interpretation() != vips.InterpretationSRGB {
			t.Errorf("text: dpi %d, %d bands %v %v", dpi, small.Bands(), small.BandFormat(), small.Interpretation())
		}
		if small.Width() < 40 || small.Height() < 10 || small.Height() > 40 {
			t.Errorf("text at 16 pt: %dx%d", small.Width(), small.Height())
		}
		double, _, err := vips.Text(ctx, &vips.TextOptions{Markup: "<b>Hi</b> there", Font: "sans 16", DPI: 144})
		if double, err = a.Add(double, err); err != nil {
			return err
		}
		if w := double.Width(); w < 2*small.Width()-4 || w > 2*small.Width()+4 {
			t.Errorf("144 dpi width %d, 72 dpi %d", w, small.Width())
		}
		wrapped, _, err := vips.Text(ctx, &vips.TextOptions{Markup: "word word word word word", Font: "sans 16", Width: 60})
		if wrapped, err = a.Add(wrapped, err); err != nil {
			return err
		}
		if wrapped.Width() > 60 || wrapped.Height() < 2*small.Height() {
			t.Errorf("wrapped at 60: %dx%d", wrapped.Width(), wrapped.Height())
		}
		fit, fdpi, err := vips.Text(ctx, &vips.TextOptions{Markup: "fit me into the box", Font: "sans 16", Width: 50, Height: 20})
		if fit, err = a.Add(fit, err); err != nil {
			return err
		}
		if fit.Width() > 50 || fit.Height() > 20 || fdpi >= 72 {
			t.Errorf("autofit 50x20: %dx%d at %d dpi", fit.Width(), fit.Height(), fdpi)
		}
		return nil
	})
}

func TestTextErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		o    *vips.TextOptions
		want errs.Kind
	}{
		{name: "nil", o: nil, want: errs.KindInternal},
		{name: "empty", o: &vips.TextOptions{}, want: errs.KindInternal},
		{name: "height without width", o: &vips.TextOptions{Markup: "x", Height: 10}, want: errs.KindInternal},
		{name: "bad markup", o: &vips.TextOptions{Markup: "<b>x"}, want: errs.KindInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
				img, _, err := vips.Text(ctx, tc.o)
				_, err = a.Add(img, err)
				return err
			})
			if err == nil || errs.KindOf(err) != tc.want {
				t.Errorf("err = %v, want kind %v", err, tc.want)
			}
		})
	}
}
