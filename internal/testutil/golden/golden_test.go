package golden

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/testutil/ssim"
)

// recorder captures failures and logs so the harness itself can be tested.
type recorder struct {
	testing.TB
	errs, logs []string
	artifacts  string
}

func (r *recorder) Helper() {}

func (r *recorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

func (r *recorder) Logf(format string, args ...any) {
	r.logs = append(r.logs, fmt.Sprintf(format, args...))
}

func (r *recorder) ArtifactDir() string { return r.artifacts }

func newRecorder(t *testing.T) *recorder {
	t.Helper()
	return &recorder{TB: t, artifacts: t.TempDir()}
}

func toRGBA(src image.Image) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, src.Bounds().Dx(), src.Bounds().Dy()))
	draw.Draw(dst, dst.Bounds(), src, src.Bounds().Min, draw.Src)
	return dst
}

func reencode(t *testing.T, src image.Image, q int) image.Image {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: q}); err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// crop returns the w x h window of src starting at (dx, dy), rebased to 0,0.
func crop(src *image.RGBA, dx, dy, w, h int) *image.RGBA {
	return toRGBA(src.SubImage(image.Rect(dx, dy, dx+w, dy+h)))
}

// TestDefaultTolerance calibrates DefaultTolerance on photo-like fixtures: a
// JPEG q85 re-encode must pass and a 1 px shift must fail.
func TestDefaultTolerance(t *testing.T) {
	t.Parallel()
	for _, fx := range []string{"jpeg/baseline.jpg", "jpeg/progressive.jpg", "webp/lossy.webp"} {
		photo := toRGBA(Decode(t, Fixture(t, fx)))
		w, h := photo.Bounds().Dx(), photo.Bounds().Dy()
		base := crop(photo, 0, 0, w-1, h-1)
		tests := []struct {
			name string
			got  image.Image
			pass bool
		}{
			{"identical", base, true},
			{"jpeg_q85", reencode(t, base, 85), true},
			{"shift_x_1px", crop(photo, 1, 0, w-1, h-1), false},
			{"shift_y_1px", crop(photo, 0, 1, w-1, h-1), false},
		}
		for _, tc := range tests {
			t.Run(fx+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				res, err := ssim.Compare(base, tc.got)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("DSSIM %.6f", res.DSSIM)
				if pass := res.DSSIM <= DefaultTolerance; pass != tc.pass {
					t.Errorf("DSSIM %.6f vs DefaultTolerance %v: pass = %v, want %v", res.DSSIM, DefaultTolerance, pass, tc.pass)
				}
			})
		}
	}
}

func TestUpdateFlow(t *testing.T) {
	t.Parallel()
	if updateRequested() {
		t.Skip("exercises compare mode; skipped while goldens are being updated")
	}
	dir := t.TempDir()
	photo := toRGBA(Decode(t, Fixture(t, "jpeg/baseline.jpg")))
	w, h := photo.Bounds().Dx()-1, photo.Bounds().Dy()
	img := crop(photo, 0, 0, w, h)
	const name = "exec/case"
	goldenPath := filepath.Join(dir, "exec", "case.png")

	// 1. Missing golden fails and says how to create it.
	r := newRecorder(t)
	Compare(r, name, img, Options{Dir: dir})
	if len(r.errs) != 1 || !strings.Contains(r.errs[0], "GOLDEN_UPDATE=1") {
		t.Fatalf("missing golden: errs = %q", r.errs)
	}

	// 2. Update writes it (creating the sub-directory) without failing.
	r = newRecorder(t)
	Compare(r, name, img, Options{Dir: dir, Update: true})
	if len(r.errs) != 0 {
		t.Fatalf("update: errs = %q", r.errs)
	}
	if _, err := os.Stat(goldenPath); err != nil {
		t.Fatalf("update did not write the golden: %v", err)
	}

	// 3. The same image and a q85 re-encode pass.
	for _, got := range []image.Image{img, reencode(t, img, 85)} {
		r = newRecorder(t)
		Compare(r, name, got, Options{Dir: dir})
		if len(r.errs) != 0 {
			t.Fatalf("compare: errs = %q", r.errs)
		}
	}

	// 4. A 1 px shift fails and dumps got/want/diff PNGs.
	r = newRecorder(t)
	Compare(r, name, crop(photo, 1, 0, w, h), Options{Dir: dir})
	if len(r.errs) != 1 || !strings.Contains(r.errs[0], "DSSIM") {
		t.Fatalf("shifted: errs = %q", r.errs)
	}
	for _, s := range []string{".got.png", ".want.png", ".diff.png"} {
		p := filepath.Join(r.artifacts, "exec", "case"+s)
		img := Decode(t, p)
		if s == ".diff.png" && img.Bounds().Dx() != w-10 {
			t.Errorf("heat map width = %d, want %d", img.Bounds().Dx(), w-10)
		}
	}

	// 5. A looser per-case tolerance accepts it.
	r = newRecorder(t)
	Compare(r, name, crop(photo, 1, 0, w, h), Options{Dir: dir, Tolerance: 0.5})
	if len(r.errs) != 0 {
		t.Fatalf("tolerance 0.5: errs = %q", r.errs)
	}

	// 6. Size mismatch fails without a heat map.
	r = newRecorder(t)
	Compare(r, name, crop(photo, 0, 0, w-1, h), Options{Dir: dir})
	if len(r.errs) != 1 || !strings.Contains(r.errs[0], "size") {
		t.Fatalf("size mismatch: errs = %q", r.errs)
	}
}

// TestEnv covers GOLDEN_UPDATE and GOLDEN_OUT; t.Setenv forbids t.Parallel.
func TestEnv(t *testing.T) {
	if *updateFlag {
		t.Skip("exercises compare mode; skipped under -update")
	}
	dir, out := t.TempDir(), t.TempDir()
	img := toRGBA(Decode(t, Fixture(t, "png/rgba8.png")))

	t.Setenv(EnvUpdate, "1")
	r := newRecorder(t)
	Compare(r, "env", img, Options{Dir: dir})
	if len(r.errs) != 0 {
		t.Fatalf("GOLDEN_UPDATE=1: errs = %q", r.errs)
	}

	t.Setenv(EnvUpdate, "")
	t.Setenv(EnvOut, out)
	r = newRecorder(t)
	Compare(r, "env", reencode(t, img, 5), Options{Dir: dir})
	if len(r.errs) != 1 {
		t.Fatalf("q5 re-encode: errs = %q", r.errs)
	}
	if _, err := os.Stat(filepath.Join(out, "env.diff.png")); err != nil {
		t.Errorf("GOLDEN_OUT not used: %v", err)
	}
}

func TestFixtureAndModuleRoot(t *testing.T) {
	t.Parallel()
	root := ModuleRoot(t)
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatal(err)
	}
	if p := Fixture(t, "gif/animated.gif"); !strings.HasPrefix(p, root) {
		t.Errorf("Fixture path %s not under %s", p, root)
	}
}
