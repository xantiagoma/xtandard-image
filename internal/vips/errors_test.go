//go:build cgo

package vips_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// TestErrorAttribution pins the error kind of each failure site: the kind
// comes from which operation failed (and, for a lazy pipeline, whether the
// source's decoder reported the failure), never from the message text.
func TestErrorAttribution(t *testing.T) {
	t.Parallel()

	jpeg := readFixture(t, "jpeg/baseline.jpg")
	png := readFixture(t, "png/rgba8.png")
	type step func(ctx context.Context, a *vips.Arena, buf *vips.Buffer) error
	load := func(o vips.LoadOptions) step {
		return func(ctx context.Context, a *vips.Arena, buf *vips.Buffer) error {
			_, err := a.Add(vips.Load(ctx, buf, &o))
			return err
		}
	}
	loadSave := func(f vips.Format) step {
		return func(ctx context.Context, a *vips.Arena, buf *vips.Buffer) error {
			img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{}))
			if err != nil {
				return err
			}
			_, err = saveBytes(ctx, img, f, nil)
			return err
		}
	}
	thumbnailSave := func(ctx context.Context, a *vips.Arena, buf *vips.Buffer) error {
		img, err := a.Add(vips.Thumbnail(ctx, buf, &vips.ThumbnailOptions{Width: 64}))
		if err != nil {
			return err
		}
		_, err = saveBytes(ctx, img, vips.FormatJPEG, nil)
		return err
	}
	tests := []struct {
		name string
		data []byte
		run  step
		want errs.Kind
		op   string
	}{
		{"unknown bytes", []byte("GIF? no: plain text"), load(vips.LoadOptions{}), errs.KindUnsupported, "load"},
		{"empty source", nil, load(vips.LoadOptions{}), errs.KindUnsupported, ""},
		{"jpeg header only", jpeg[:24], load(vips.LoadOptions{}), errs.KindCorrupt, "load"},
		{"png header only", png[:40], load(vips.LoadOptions{}), errs.KindCorrupt, "load"},
		// The header parses, so Load succeeds; the decoder fails lazily in
		// the saver's pipeline and flags the source.
		{"truncated jpeg on save", jpeg[:len(jpeg)/2], loadSave(vips.FormatPNG), errs.KindCorrupt, "pngsave"},
		{"truncated jpeg via thumbnail", jpeg[:len(jpeg)/2], thumbnailSave, errs.KindCorrupt, "jpegsave"},
		{"truncated png on save", png[:len(png)*3/4], loadSave(vips.FormatJPEG), errs.KindCorrupt, "jpegsave"},
		// libjpeg reads a 12-bit header, then refuses the precision when
		// decoding starts (libvips uses the 8-bit API): the input's fault.
		{"12-bit jpeg on save", jpegPrecision(t, jpeg, 12), loadSave(vips.FormatPNG), errs.KindCorrupt, "pngsave"},
		{"12-bit jpeg via thumbnail", jpegPrecision(t, jpeg, 12), thumbnailSave, errs.KindCorrupt, "jpegsave"},
		{"pixel limit", jpeg, load(vips.LoadOptions{MaxPixels: 100}), errs.KindLimitExceeded, "load"},
		{"truncated jpeg tolerated", jpeg[:len(jpeg)/2], func(ctx context.Context, a *vips.Arena, buf *vips.Buffer) error {
			img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{FailOn: vips.FailOnNone}))
			if err != nil {
				return err
			}
			_, err = saveBytes(ctx, img, vips.FormatPNG, nil)
			return err
		}, errs.KindUnknown, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			buf := newBuffer(t, tc.data)
			defer buf.Release()
			err := tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
				return tc.run(ctx, a, buf)
			})
			if got := errs.KindOf(err); got != tc.want {
				t.Fatalf("error kind = %v (%v), want %v", got, err, tc.want)
			}
			if tc.op == "" {
				return
			}
			// Decoders that fail lazily inside a sink do not always leave
			// text in the error buffer; the op name is what matters.
			if opErr, ok := errors.AsType[*vips.OpError](err); !ok || opErr.Op != tc.op {
				t.Errorf("error %v: want *vips.OpError for %q", err, tc.op)
			}
		})
	}
}

// jpegPrecision returns a copy of the baseline JPEG data whose frame header
// (SOF0) is rewritten as an extended-sequential SOF1 of the given sample
// precision; the entropy-coded data is left as it is.
func jpegPrecision(t *testing.T, data []byte, precision byte) []byte {
	t.Helper()
	out := slices.Clone(data)
	for i := 2; i+4 < len(out); {
		if out[i] != 0xFF {
			t.Fatalf("jpeg marker expected at %d", i)
		}
		if out[i+1] == 0xC0 {
			out[i+1], out[i+4] = 0xC1, precision
			return out
		}
		i += 2 + (int(out[i+2])<<8 | int(out[i+3]))
	}
	t.Fatal("no SOF0 marker")
	return nil
}

// TestSaveFailureIsInternal covers a saver rejecting a valid image (WebP's
// 16383-pixel dimension limit): nothing is wrong with the input, so the
// failure is ours (500), not the client's.
func TestSaveFailureIsInternal(t *testing.T) {
	t.Parallel()

	buf := newBuffer(t, readFixture(t, "png/palette.png"))
	defer buf.Release()
	err := tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
		img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{}))
		if err != nil {
			return err
		}
		wide, err := a.Add(img.Replicate(ctx, 200, 1)) // 19200 px wide
		if err != nil {
			return err
		}
		_, err = saveBytes(ctx, wide, vips.FormatWebP, nil)
		return err
	})
	if errs.KindOf(err) != errs.KindInternal {
		t.Fatalf("error kind = %v (%v), want KindInternal", errs.KindOf(err), err)
	}
	if opErr, ok := errors.AsType[*vips.OpError](err); !ok || opErr.Op != "webpsave" {
		t.Errorf("error %v does not name webpsave", err)
	}
}

func TestOpErrorIsInternal(t *testing.T) {
	t.Parallel()

	err := tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
		img, err := a.Add(vips.Black(ctx, 10, 10, 3))
		if err != nil {
			return err
		}
		_, err = a.Add(img.ExtractBand(ctx, 5, 1))
		return err
	})
	if errs.KindOf(err) != errs.KindInternal {
		t.Errorf("bad extract_band: %v, want KindInternal", err)
	}
}

func TestCanceledContext(t *testing.T) {
	t.Parallel()

	buf := newBuffer(t, readFixture(t, "jpeg/baseline.jpg"))
	defer buf.Release()
	ctx, cancel := context.WithCancel(t.Context())
	err := vips.Run(ctx, func() error {
		var a vips.Arena
		defer a.Close()
		img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{}))
		if err != nil {
			return err
		}
		cancel()
		if _, err := a.Add(img.Resize(ctx, 0.5, 0, vips.KernelLanczos3)); errs.KindOf(err) != errs.KindCanceled {
			t.Errorf("Resize after cancel: %v, want KindCanceled", err)
		}
		if _, err := img.Save(ctx, vips.FormatJPEG, nil); errs.KindOf(err) != errs.KindCanceled {
			t.Errorf("Save after cancel: %v, want KindCanceled", err)
		}
		if _, err := vips.Probe(ctx, buf); errs.KindOf(err) != errs.KindCanceled {
			t.Errorf("Probe after cancel: %v, want KindCanceled", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
