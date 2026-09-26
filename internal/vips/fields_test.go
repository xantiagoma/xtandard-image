//go:build cgo

package vips_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/vips"
)

func TestFieldsTextCopyMemory(t *testing.T) {
	t.Parallel()

	buf := newBuffer(t, readFixture(t, "jpeg/exif-orientation-6-icc.jpg"))
	defer buf.Release()
	job(t, func(ctx context.Context, a *vips.Arena) error {
		img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Access: vips.AccessSequential}))
		if err != nil {
			return err
		}
		fields := img.Fields()
		for _, f := range []vips.MetaField{vips.FieldEXIF, vips.FieldICC, "exif-ifd0-Orientation"} {
			if !slices.Contains(fields, f) {
				t.Errorf("Fields lacks %s: %v", f, fields)
			}
		}
		if s, ok := img.Text("exif-ifd0-Orientation"); !ok || !strings.HasPrefix(s, "6") {
			t.Errorf("Text(exif-ifd0-Orientation) = %q, %v", s, ok)
		}
		if _, ok := img.Text(vips.FieldEXIF); ok {
			t.Error("Text read a blob field")
		}
		if _, ok := img.Text("missing-field"); ok {
			t.Error("Text read a missing field")
		}

		// A sequential image rendered to memory can be saved repeatedly.
		mem, err := a.Add(img.CopyMemory(ctx))
		if err != nil {
			return err
		}
		if mem.Width() != img.Width() || mem.Height() != img.Height() || !slices.Contains(mem.Fields(), vips.FieldICC) {
			t.Errorf("CopyMemory changed the image: %dx%d, fields %v", mem.Width(), mem.Height(), mem.Fields())
		}
		for range 3 {
			blob, err := mem.SaveJPEG(ctx, 70)
			if err != nil {
				return err
			}
			blob.Release()
		}
		return nil
	})
}

func TestCopyMemoryCanceled(t *testing.T) {
	t.Parallel()

	buf := newBuffer(t, readFixture(t, "jpeg/baseline.jpg"))
	defer buf.Release()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := tryJob(t.Context(), func(_ context.Context, a *vips.Arena) error {
		img, err := a.Add(vips.Load(t.Context(), buf, nil))
		if err != nil {
			return err
		}
		_, err = a.Add(img.CopyMemory(ctx))
		return err
	})
	if err == nil {
		t.Fatal("CopyMemory ran with a canceled context")
	}
}
