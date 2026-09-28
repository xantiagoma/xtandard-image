package ghostscript_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/ghostscript"
)

// These tests run the real Ghostscript: the gpl builder image ships it
// (/opt/xtandard/bin/gs); elsewhere they are skipped.

func realGS(t *testing.T, mod func(*ghostscript.Config)) (*ghostscript.Runner, string) {
	t.Helper()
	path, err := exec.LookPath("gs")
	if err != nil {
		t.Skip("gs not installed (only the gpl image ships Ghostscript)")
	}
	return newRunner(t, path, mod)
}

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// renderPNG renders page of file and decodes the PNG.
func renderPNG(t *testing.T, r *ghostscript.Runner, file string, page *ghostscript.Page) (image.Image, []byte, error) {
	t.Helper()
	var c collect
	_, err := r.Render(t.Context(), readFixture(t, file), page, c.read)
	if err != nil {
		return nil, c.out, err
	}
	img, err := png.Decode(bytes.NewReader(c.out))
	if err != nil {
		t.Fatalf("decode %s: %v", file, err)
	}
	return img, c.out, nil
}

// near reports whether c is within 24 of want on every channel.
func near(c color.Color, want color.NRGBA) bool {
	got := color.NRGBAModel.Convert(c).(color.NRGBA) //nolint:forcetypeassert // NRGBAModel always returns NRGBA
	d := func(a, b uint8) bool { return max(a, b)-min(a, b) < 24 }
	return d(got.R, want.R) && d(got.G, want.G) && d(got.B, want.B) && d(got.A, want.A)
}

func TestRealRender(t *testing.T) {
	t.Parallel()
	r, tmp := realGS(t, func(c *ghostscript.Config) { c.MaxProcs = 16 })
	fx := filepath.Join("..", "..", "testdata", "fixtures", "eps")
	red := color.NRGBA{255, 0, 0, 255}
	blue := color.NRGBA{0, 0, 255, 255}
	green := color.NRGBA{0, 255, 0, 255}
	tests := []struct {
		name  string
		file  string
		page  ghostscript.Page
		w, h  int
		probe map[image.Point]color.NRGBA
	}{
		// BoundingBox 10 20 110 70 cropped: the circle centre (60, 45)
		// lands at (50, 25) pt = (100, 50) px at 144 dpi.
		{
			"eps at 4x", "simple.eps",
			ghostscript.Page{X: 10, Y: 20, Width: 100, Height: 50, DPI: 288},
			400, 200,
			map[image.Point]color.NRGBA{{200, 100}: blue, {8, 8}: red},
		},
		{
			"eps at 2x", "simple.eps",
			ghostscript.Page{X: 10, Y: 20, Width: 100, Height: 50, DPI: 144},
			200, 100,
			map[image.Point]color.NRGBA{{100, 50}: blue, {4, 4}: red},
		},
		{
			"illustrator", "illustrator.ai",
			ghostscript.Page{X: 100, Y: 200, Width: 160, Height: 120, DPI: 72},
			160, 120,
			map[image.Point]color.NRGBA{{80, 60}: {230, 0, 153, 255}, {5, 5}: {0, 153, 51, 255}},
		},
		{
			"page 2 of 3", "pages.ps",
			ghostscript.Page{Width: 200, Height: 100, Number: 2},
			200, 100,
			map[image.Point]color.NRGBA{{100, 50}: green},
		},
		{
			"page 3 of 3", "pages.ps",
			ghostscript.Page{Width: 200, Height: 100, Number: 3},
			200, 100,
			map[image.Point]color.NRGBA{{100, 50}: blue},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			img, _, err := renderPNG(t, r, filepath.Join(fx, tt.file), &tt.page)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if b := img.Bounds(); b.Dx() != tt.w || b.Dy() != tt.h {
				t.Fatalf("size %dx%d, want %dx%d", b.Dx(), b.Dy(), tt.w, tt.h)
			}
			for p, want := range tt.probe {
				if got := img.At(p.X, p.Y); !near(got, want) {
					t.Errorf("pixel %v = %v, want %v", p, got, want)
				}
			}
		})
	}
	t.Cleanup(func() { assertGone(t, nil, tmp) })
}

// TestRealOffsetPages: the bounding box origin shift applies to every
// page of a PostScript document, not only the first.
func TestRealOffsetPages(t *testing.T) {
	t.Parallel()
	r, _ := realGS(t, nil)
	doc := []byte("%!PS-Adobe-3.0\n%%BoundingBox: 50 50 150 100\n%%Pages: 2\n%%EndComments\n" +
		"%%Page: 1 1\n1 0 0 setrgbcolor 50 50 100 50 rectfill showpage\n" +
		"%%Page: 2 2\n0 0 1 setrgbcolor 100 50 50 50 rectfill showpage\n%%EOF\n")
	var c collect
	if _, err := r.Render(t.Context(), doc, &ghostscript.Page{X: 50, Y: 50, Width: 100, Height: 50, Number: 2}, c.read); err != nil {
		t.Fatalf("Render: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(c.out))
	if err != nil {
		t.Fatal(err)
	}
	if got := img.At(75, 25); !near(got, color.NRGBA{0, 0, 255, 255}) {
		t.Errorf("page 2 right half = %v, want blue", got)
	}
	if _, _, _, a := img.At(25, 25).RGBA(); a != 0 {
		got := img.At(25, 25)
		t.Errorf("page 2 left half = %v, want transparent", got)
	}
}

// TestRealHostile feeds documents that attack the interpreter. Each must
// fail (or, for the page size, stay at the declared size), leak nothing of
// the file system into the output or the error, leave no process behind
// and write nothing.
func TestRealHostile(t *testing.T) {
	t.Parallel()
	r, tmp := realGS(t, func(c *ghostscript.Config) { c.Timeout, c.MaxProcs = 2*time.Second, 16 })
	dir := filepath.Join("testdata", "hostile")
	tests := []struct {
		file string
		want errs.Kind
	}{
		{"readfile.eps", errs.KindCorrupt},     // /invalidfileaccess (-dSAFER)
		{"pipe.eps", errs.KindCorrupt},         // %pipe% refused (-dSAFER)
		{"loop.eps", errs.KindTimeout},         // killed after XI_FORMATS_EPS_TIMEOUT
		{"recursion.eps", errs.KindCorrupt},    // /dictstackoverflow
		{"memory.eps", errs.KindLimitExceeded}, // /VMerror under RLIMIT_AS
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			t.Parallel()
			start := time.Now()
			_, out, err := renderPNG(t, r, filepath.Join(dir, tt.file), &ghostscript.Page{Width: 100, Height: 100})
			if got := errs.KindOf(err); got != tt.want {
				t.Errorf("error kind = %s (%v), want %s", got, err, tt.want)
			}
			for _, leak := range []string{"root:", "/bin/", "uid="} {
				if bytes.Contains(out, []byte(leak)) || (err != nil && strings.Contains(err.Error(), leak)) {
					t.Errorf("output or error leaks %q", leak)
				}
			}
			t.Logf("%s: %v after %s", tt.file, err, time.Since(start).Round(time.Millisecond))
		})
	}
	t.Run("page size", func(t *testing.T) {
		t.Parallel()
		img, _, err := renderPNG(t, r, filepath.Join(dir, "pagesize.eps"), &ghostscript.Page{Width: 100, Height: 100})
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		if b := img.Bounds(); b.Dx() != 100 || b.Dy() != 100 {
			t.Errorf("setpagedevice changed the page to %dx%d, want the fixed 100x100", b.Dx(), b.Dy())
		}
	})
	if _, err := os.Stat("/tmp/xi-gs-pwned"); err == nil {
		t.Error("the pipe device document ran a command")
	}
	t.Cleanup(func() { assertGone(t, nil, tmp) })
}
