//go:build cgo

package app

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
)

// watermarkConfig is a service with an fs source on testdata/ ("td"), one
// on tmp ("tmp") and the given watermark source string.
func watermarkConfig(t *testing.T, watermark, tmp string) *config.Config {
	t.Helper()
	root := filepath.Join(golden.ModuleRoot(t), "testdata")
	yml := `server:
  addr: "127.0.0.1:0"
  shutdown_delay: 0s
admin:
  addr: ""
sources:
  td:
    type: fs
    root: ` + strconv.Quote(root) + `
  tmp:
    type: fs
    root: ` + strconv.Quote(tmp) + `
dialects:
  imgproxy:
    source: td
security:
  unsigned_policy: allow
processing:
  watermark_source: ` + strconv.Quote(watermark) + `
  watermark_opacity: 0.9
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

// TestWatermarkStartupFailure checks that a configured watermark that
// cannot be loaded stops the service before it listens.
func TestWatermarkStartupFailure(t *testing.T) {
	// Not parallel: run re-applies libvips' process-global allowlists.
	tmp := t.TempDir()
	photo, err := os.ReadFile(golden.Fixture(t, "jpeg/baseline.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"truncated.jpg": photo[:len(photo)/3],
		"broken.svg":    []byte(`<svg xmlns="http://www.w3.org/2000/svg"><g></svg>`),
	} {
		if err := os.WriteFile(filepath.Join(tmp, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, source, want string
	}{
		{name: "missing", source: "xi://td/fixtures/watermark/missing.png", want: "watermark fetch"},
		{name: "no source allows it", source: "s3://bucket/watermark.png", want: "watermark source"},
		{name: "not an image", source: "xi://td/README.md", want: "unrecognised image format"},
		{name: "corrupt", source: "xi://tmp/truncated.jpg", want: "watermark decode"},
		{name: "malformed svg", source: "xi://tmp/broken.svg", want: "sanitise svg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := watermarkConfig(t, tc.source, tmp)
			listened := false
			err := run(context.WithoutCancel(t.Context()), cfg, slog.New(slog.DiscardHandler), Options{Version: "test"},
				hooks{keepVips: true, listening: func(_, _ net.Addr) { listened = true }})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("run = %v, want an error containing %q", err, tc.want)
			}
			if listened {
				t.Error("the service listened")
			}
		})
	}
}

// TestWatermarkServed boots the service with a watermark from the fs
// source and checks that wm places it and leaves the rest untouched.
func TestWatermarkServed(t *testing.T) {
	// Not parallel: run re-applies libvips' process-global allowlists.
	a := start(t, watermarkConfig(t, "xi://td/fixtures/watermark/logo.png", t.TempDir()), hooks{})
	base := "http://" + a.public.String() + "/insecure/rs:fit:200:200"
	plain := getPNG(t, base+"/plain/fixtures/jpeg/baseline.jpg@png")
	marked := getPNG(t, base+"/wm:1:soea:0:0/plain/fixtures/jpeg/baseline.jpg@png")
	if plain.Bounds() != image.Rect(0, 0, 200, 150) || marked.Bounds() != plain.Bounds() {
		t.Fatalf("sizes %v and %v, want 200x150", plain.Bounds(), marked.Bounds())
	}
	// The logo's navy bar sits in the bottom-right 48x32 corner.
	if same(plain, marked, 180, 140) {
		t.Error("bottom-right corner unchanged: no watermark")
	}
	if !same(plain, marked, 10, 10) {
		t.Error("top-left corner changed")
	}
}

func getPNG(t *testing.T, u string) image.Image {
	t.Helper()
	r := get(t, u)
	if r.status != http.StatusOK || r.ctype != "image/png" {
		t.Fatalf("GET %s: %d %q %q", u, r.status, r.ctype, r.body)
	}
	img, err := png.Decode(bytes.NewReader(r.body))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func same(a, b image.Image, x, y int) bool {
	ar, ag, ab, _ := a.At(x, y).RGBA()
	br, bg, bb, _ := b.At(x, y).RGBA()
	return ar == br && ag == bg && ab == bb
}
