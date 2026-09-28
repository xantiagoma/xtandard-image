//go:build cgo

package app

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
)

// epsConfig serves testdata/fixtures on the imgproxy mount, the Ghostscript
// hostile documents as source "hostile" and dir as source "synth", with
// EPS rendering on or off.
func epsConfig(t *testing.T, enabled bool, dir string) *config.Config {
	t.Helper()
	root := golden.ModuleRoot(t)
	yml := `server:
  addr: "127.0.0.1:0"
  shutdown_delay: 0s
admin:
  addr: "127.0.0.2:0"
security:
  unsigned_policy: allow
observability:
  debug_headers: true
formats:
  eps:
    enabled: ` + strconv.FormatBool(enabled) + `
    timeout: 2s
    max_procs: 8
sources:
  fixtures:
    type: fs
    root: ` + strconv.Quote(filepath.Join(root, "testdata", "fixtures")) + `
  hostile:
    type: fs
    root: ` + strconv.Quote(filepath.Join(root, "internal", "ghostscript", "testdata", "hostile")) + `
  synth:
    type: fs
    root: ` + strconv.Quote(dir) + `
dialects:
  imgproxy:
    source: fixtures
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(config.LoadOptions{ConfigFile: path})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return &cfg
}

// decodePNG checks a 200 image/png response and decodes it.
func decodePNG(t *testing.T, r response) image.Image {
	t.Helper()
	if r.status != http.StatusOK || r.ctype != "image/png" {
		t.Fatalf("image: %d %q %.200s", r.status, r.ctype, r.body)
	}
	img, err := png.Decode(bytes.NewReader(r.body))
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	return img
}

// close reports whether c is within 32 of want on R, G and B.
func closeTo(c color.Color, want color.NRGBA) bool {
	got := color.NRGBAModel.Convert(c).(color.NRGBA) //nolint:forcetypeassert // NRGBAModel always returns NRGBA
	d := func(a, b uint8) bool { return max(a, b)-min(a, b) < 32 }
	return d(got.R, want.R) && d(got.G, want.G) && d(got.B, want.B)
}

// TestEPSDisabled: without XI_FORMATS_EPS_ENABLED (every slim and full
// image, and gpl by default) an EPS is 422 with the message naming the
// switch, unless it is a DOS EPS with a TIFF preview.
func TestEPSDisabled(t *testing.T) {
	a := start(t, epsConfig(t, false, t.TempDir()), hooks{})
	base := "http://" + a.public.String()
	for _, src := range []string{"eps/simple.eps", "eps/pages.ps", "eps/illustrator.ai"} {
		r := get(t, base+"/insecure/plain/"+src+"@webp")
		if r.status != http.StatusUnprocessableEntity || !strings.Contains(string(r.body), codec.EPSDisabledMessage) {
			t.Errorf("%s: %d %q, want 422 %q", src, r.status, r.body, codec.EPSDisabledMessage)
		}
	}
	img := decodePNG(t, get(t, base+"/insecure/plain/eps/preview.eps@png"))
	if b := img.Bounds(); b.Dx() != 96 || b.Dy() != 72 {
		t.Errorf("preview: %dx%d, want 96x72", b.Dx(), b.Dy())
	}
}

// TestEPS renders through the real Ghostscript (the gpl builder has it;
// skipped elsewhere) and feeds it hostile documents: each fails with a 4xx
// or 504 and leaves the service working.
func TestEPS(t *testing.T) {
	if _, err := exec.LookPath("gs"); err != nil {
		t.Skip("gs not installed (only the gpl image ships Ghostscript)")
	}
	synth := t.TempDir()
	// A 30000 pt square page is 900 MP at 72 DPI.
	if err := os.WriteFile(filepath.Join(synth, "huge.eps"), []byte("%!PS-Adobe-3.0 EPSF-3.0\n%%BoundingBox: 0 0 30000 30000\n%%EndComments\nshowpage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := start(t, epsConfig(t, true, synth), hooks{})
	base := "http://" + a.public.String()

	t.Run("webp", func(t *testing.T) {
		r := get(t, base+"/insecure/plain/eps/simple.eps@webp")
		if r.status != http.StatusOK || r.ctype != "image/webp" {
			t.Fatalf("%d %q %.200s", r.status, r.ctype, r.body)
		}
	})
	t.Run("natural size", func(t *testing.T) {
		img := decodePNG(t, get(t, base+"/insecure/plain/eps/simple.eps@png"))
		if b := img.Bounds(); b.Dx() != 100 || b.Dy() != 50 {
			t.Errorf("%dx%d, want 100x50", b.Dx(), b.Dy())
		}
		if got := img.At(50, 25); !closeTo(got, color.NRGBA{0, 0, 255, 255}) {
			t.Errorf("centre %v, want blue", got)
		}
	})
	t.Run("rendered at the target size", func(t *testing.T) {
		// A vector: 4× the natural size is drawn at 288 DPI, not upscaled.
		img := decodePNG(t, get(t, base+"/insecure/rs:fit:400:400/plain/eps/simple.eps@png"))
		if b := img.Bounds(); b.Dx() != 400 || b.Dy() != 200 {
			t.Errorf("%dx%d, want 400x200", b.Dx(), b.Dy())
		}
	})
	t.Run("illustrator", func(t *testing.T) {
		img := decodePNG(t, get(t, base+"/insecure/plain/eps/illustrator.ai@png"))
		if b := img.Bounds(); b.Dx() != 160 || b.Dy() != 120 {
			t.Errorf("%dx%d, want 160x120", b.Dx(), b.Dy())
		}
		if got := img.At(80, 60); !closeTo(got, color.NRGBA{230, 0, 153, 255}) {
			t.Errorf("centre %v, want magenta", got)
		}
	})
	t.Run("page selection", func(t *testing.T) {
		img := decodePNG(t, get(t, base+"/insecure/pg:1/plain/eps/pages.ps@png"))
		if got := img.At(100, 50); !closeTo(got, color.NRGBA{0, 255, 0, 255}) {
			t.Errorf("page 2 centre %v, want green", got)
		}
	})
	t.Run("info never runs gs", func(t *testing.T) {
		r := get(t, base+"/info/insecure/s:1/plain/eps/pages.ps")
		if r.status != http.StatusOK || !strings.Contains(string(r.body), `"width":200`) {
			t.Errorf("/info: %d %s", r.status, r.body)
		}
	})
	for _, tc := range []struct {
		src  string
		want int
	}{
		{"xi://hostile/readfile.eps", http.StatusUnprocessableEntity},
		{"xi://hostile/pipe.eps", http.StatusUnprocessableEntity},
		{"xi://hostile/recursion.eps", http.StatusUnprocessableEntity},
		{"xi://hostile/memory.eps", http.StatusUnprocessableEntity},
		{"xi://hostile/loop.eps", http.StatusGatewayTimeout},
		{"xi://synth/huge.eps", http.StatusUnprocessableEntity},
		// The document asks for a 100000 pt page; the device stays at its
		// 100 pt box.
		{"xi://hostile/pagesize.eps", http.StatusOK},
	} {
		t.Run(tc.src, func(t *testing.T) {
			start := time.Now()
			r := get(t, base+"/insecure/plain/"+tc.src+"@png")
			if r.status != tc.want {
				t.Errorf("%d %.300s, want %d", r.status, r.body, tc.want)
			}
			for _, leak := range []string{"root:", "invalidfileaccess", "/etc/", "ghostscript"} {
				if r.status != http.StatusOK && strings.Contains(string(r.body), leak) {
					t.Errorf("response leaks %q: %s", leak, r.body)
				}
			}
			if r.status == http.StatusOK {
				if b := decodePNG(t, r).Bounds(); b.Dx() != 100 || b.Dy() != 100 {
					t.Errorf("%dx%d, want 100x100", b.Dx(), b.Dy())
				}
			}
			t.Logf("%s: %d %q in %s", tc.src, r.status, r.body, time.Since(start).Round(time.Millisecond))
		})
	}
	// Still serving.
	decodePNG(t, get(t, base+"/insecure/plain/eps/simple.eps@png"))
}
