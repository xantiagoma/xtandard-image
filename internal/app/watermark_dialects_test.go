//go:build cgo

package app

import (
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
)

// dialectWatermarkConfig mounts imgproxy, cloudinary (cloud "demo") and
// imagekit on the fs source over testdata/, unsigned, with the logo as
// the configured watermark.
func dialectWatermarkConfig(t *testing.T) *config.Config {
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
dialects:
  imgproxy:
    source: td
  cloudinary:
    enabled: true
    clouds:
      demo: td
  imagekit:
    enabled: true
    source: td
security:
  unsigned_policy: allow
processing:
  watermark_source: "xi://td/fixtures/watermark/logo.png"
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

func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

// TestWatermarkDialects serves a per-request image watermark and a text
// watermark through every dialect with overlays, end to end (router,
// resolver, fetcher, executor), and compares each response with its
// golden (testdata/golden/app/). The badge is fetched from the fs source
// like any source. The imgix dialect has no app mount yet (its mark and
// txt are covered by the executor's dialect goldens).
func TestWatermarkDialects(t *testing.T) {
	// Not parallel: run re-applies libvips' process-global allowlists.
	a := start(t, dialectWatermarkConfig(t), hooks{})
	base := "http://" + a.public.String()
	const photo = "fixtures/jpeg/baseline.jpg"
	for _, tc := range []struct{ name, path string }{
		{"wm-imgproxy-url", "/insecure/rs:fit:200:200/wm:0.9:soea:4:4/wmu:" + b64("xi://td/fixtures/watermark/badge.png") + "/wms:40:0/wmr:20/plain/" + photo + "@png"},
		{"wm-imgproxy-text", "/insecure/rs:fit:200:200/wm:1:nowe:6:6/wmt:" + b64(`<span foreground="white" size="18pt"><b>xtandard</b></span>`) + "/wmsh:2/plain/" + photo + "@png"},
		{"wm-imgproxy-chessboard", "/insecure/rs:fit:200:200/wm:0.8:ch:4:4:0.12/plain/" + photo + "@png"},
		{"wm-cloudinary-layer", "/cloudinary/demo/image/upload/w_200,f_png/l_fixtures:watermark:badge.png,w_40,g_south_east,x_4,y_4,o_90/fl_layer_apply/" + photo},
		{"wm-cloudinary-text", "/cloudinary/demo/image/upload/w_200,f_png/l_text:DejaVu%20Sans_18_bold:Hello,co_white,g_north_west,x_6,y_6/fl_layer_apply/" + photo},
		{"wm-imagekit-image", "/imagekit/" + photo + "?tr=w-200,f-png:l-image,i-fixtures@@watermark@@badge.png,w-40,lx-N4,ly-N4,rt-20,l-end"},
		{"wm-imagekit-text", "/imagekit/" + photo + "?tr=w-200,f-png:l-text,i-Hello,fs-18,co-FFFFFF,tg-b,lx-6,ly-6,l-end"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img := getPNG(t, base+tc.path)
			golden.Compare(t, "app/"+tc.name, img, golden.Options{Tolerance: 0.03})
		})
	}

	// A watermark source outside the allowlist, and one that does not
	// exist, fail the request.
	for _, tc := range []struct {
		name, path string
		status     int
	}{
		{"not allowed", "/insecure/rs:fit:200:200/wm:1:ce/wmu:" + b64("s3://bucket/w.png") + "/plain/" + photo + "@png", http.StatusForbidden},
		{"missing", "/insecure/rs:fit:200:200/wm:1:ce/wmu:" + b64("xi://td/fixtures/watermark/missing.png") + "/plain/" + photo + "@png", http.StatusNotFound},
		{"not an image", "/insecure/rs:fit:200:200/wm:1:ce/wmu:" + b64("xi://td/README.md") + "/plain/" + photo + "@png", http.StatusUnprocessableEntity},
		{"bad markup", "/insecure/rs:fit:200:200/wm:1:ce/wmt:" + b64("<img/>") + "/plain/" + photo + "@png", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if r := get(t, base+tc.path); r.status != tc.status {
				t.Errorf("status = %d, want %d (%q)", r.status, tc.status, r.body)
			}
		})
	}
}
