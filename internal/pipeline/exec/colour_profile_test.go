//go:build cgo

package exec_test

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec/encode"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/exec"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// profileDir writes libvips' built-in CMYK profile as custom.icc into a
// temporary directory.
func profileDir(tb testing.TB) string {
	tb.Helper()
	var icc []byte
	err := vips.Run(tb.Context(), func() error {
		var err error
		icc, err = vips.ProfileLoad(tb.Context(), "cmyk")
		return err
	})
	if err != nil || len(icc) == 0 {
		tb.Fatalf("load cmyk profile: %v", err)
	}
	dir := tb.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "custom.icc"), icc, 0o600); err != nil {
		tb.Fatal(err)
	}
	return dir
}

// TestColorProfile: color_profile converts the result to srgb, cmyk or an
// ICC file from the profiles directory and embeds the profile.
func TestColorProfile(t *testing.T) {
	t.Parallel()
	ec := encode.DefaultConfig()
	enc, err := encode.New(&ec)
	if err != nil {
		t.Fatal(err)
	}
	e, err := exec.New(&exec.Config{Limits: testLimits, ColorProfilesDir: profileDir(t)}, enc)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		options string
		out     transform.Format
		interp  vips.Interpretation
		icc     bool
		skipped bool
	}{
		{"srgb", "rs:fit:64:64/cp:srgb", transform.FormatPNG, vips.InterpretationSRGB, true, false},
		{"cmyk jpeg", "rs:fit:64:64/cp:cmyk", transform.FormatJPEG, vips.InterpretationCMYK, true, false},
		{"cmyk png skipped", "rs:fit:64:64/cp:cmyk", transform.FormatPNG, vips.InterpretationSRGB, false, true},
		{"file", "rs:fit:64:64/icc:custom.icc", transform.FormatJPEG, vips.InterpretationCMYK, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			j, buf := job(t, readFixture(t, jpegPhoto), parse(t, tc.options), tc.out)
			defer buf.Release()
			res, err := e.Run(t.Context(), j)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			defer res.Body.Release()
			if got := slices.Contains(res.Skipped, "color_profile"); got != tc.skipped {
				t.Errorf("Skipped = %q", res.Skipped)
			}
			ob := newBuffer(t, bytes.Clone(res.Body.Bytes()))
			defer ob.Release()
			var h vips.Header
			if err := vips.Run(t.Context(), func() error {
				var err error
				h, err = vips.Probe(t.Context(), ob)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if h.Interpretation != tc.interp || h.HasICC != tc.icc {
				t.Errorf("output interpretation %v, ICC %v; want %v, %v", h.Interpretation, h.HasICC, tc.interp, tc.icc)
			}
		})
	}

	t.Run("unknown file", func(t *testing.T) {
		t.Parallel()
		j, buf := job(t, readFixture(t, jpegPhoto), parse(t, "cp:nope.icc"), transform.FormatJPEG)
		defer buf.Release()
		_, err := e.Run(t.Context(), j)
		if errs.KindOf(err) != errs.KindInvalidURL {
			t.Errorf("err = %v, want KindInvalidURL", err)
		}
	})
	t.Run("no directory", func(t *testing.T) {
		t.Parallel()
		o := process(t, jpegPhoto, "rs:fit:32:32/cp:custom.icc", transform.FormatJPEG)
		if !slices.Contains(o.res.Skipped, "color_profile") {
			t.Errorf("Skipped = %q, want color_profile", o.res.Skipped)
		}
	})
}
