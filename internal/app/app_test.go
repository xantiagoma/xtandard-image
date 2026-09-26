//go:build cgo

package app

import (
	"bytes"
	"context"
	"errors"
	"image/jpeg"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/security/ssrf"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
)

type addrs struct{ public, admin net.Addr }

// start runs the service with cfg until the test ends and returns its
// addresses.
func start(t *testing.T, cfg *config.Config, h hooks) addrs {
	t.Helper()
	bound := make(chan addrs, 1)
	h.listening = func(p, a net.Addr) { bound <- addrs{p, a} }
	ctx, cancel := context.WithCancelCause(context.WithoutCancel(t.Context()))
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg, slog.New(slog.DiscardHandler), Options{Version: "test"}, h) }()
	t.Cleanup(func() {
		cancel(errors.New("test shutdown"))
		if err := <-done; err != nil {
			t.Errorf("run after shutdown: %v", err)
		}
	})
	select {
	case a := <-bound:
		return a
	case err := <-done:
		t.Fatalf("run exited early: %v", err)
		return addrs{}
	}
}

type response struct {
	status int
	ctype  string
	body   []byte
}

func get(t *testing.T, u string) response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, u, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response{status: resp.StatusCode, ctype: resp.Header.Get("Content-Type"), body: body}
}

func checkJPEG(t *testing.T, r response, maxW, maxH int) {
	t.Helper()
	if r.status != http.StatusOK || r.ctype != "image/jpeg" {
		t.Fatalf("image: %d %q %q", r.status, r.ctype, r.body)
	}
	dims, err := jpeg.DecodeConfig(bytes.NewReader(r.body))
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if dims.Width > maxW || dims.Height > maxH || dims.Width == 0 {
		t.Errorf("output %dx%d does not fit %dx%d", dims.Width, dims.Height, maxW, maxH)
	}
}

// TestConfigDrivenFSSource wires the service from a minimal YAML file (one
// fs source bound to the imgproxy mount) through config.Load, the way cmd
// does, and serves a real thumbnail from testdata/fixtures.
func TestConfigDrivenFSSource(t *testing.T) {
	t.Parallel()

	fixtures := filepath.Join(golden.ModuleRoot(t), "testdata", "fixtures")
	yml := `server:
  addr: "127.0.0.1:0"
  shutdown_delay: 0s
admin:
  addr: "127.0.0.2:0" # must differ from server.addr
sources:
  fixtures:
    type: fs
    root: ` + strconv.Quote(fixtures) + `
dialects:
  imgproxy:
    source: fixtures
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, meta, err := config.Load(config.LoadOptions{ConfigFile: path})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if meta.File != path {
		t.Errorf("meta.File = %q", meta.File)
	}

	a := start(t, &cfg, hooks{})
	base := "http://" + a.public.String()
	checkJPEG(t, get(t, base+"/insecure/rs:fit:64:64/plain/jpeg/baseline.jpg"), 64, 64)

	for _, tc := range []struct {
		path string
		want int
	}{
		{path: "/insecure/w:64/plain/jpeg/missing.jpg", want: http.StatusNotFound},
		{path: "/insecure/w:64/plain/../../etc/passwd", want: http.StatusForbidden},
		{path: "/insecure/w:65/plain/jpeg/baseline.jpg", want: http.StatusForbidden}, // restricted: 65 is not an allowed width
		{path: "/insecure/w:64/plain/http://example.com/a.jpg", want: http.StatusForbidden},
		{path: "/insecure/bogus", want: http.StatusBadRequest},
		{path: "/healthz", want: http.StatusOK},
		{path: "/readyz", want: http.StatusOK},
	} {
		if got := get(t, base+tc.path); got.status != tc.want {
			t.Errorf("GET %s = %d %q, want %d", tc.path, got.status, got.body, tc.want)
		}
	}

	metricsBody := string(get(t, "http://"+a.admin.String()+"/metrics").body)
	for _, want := range []string{
		`xi_requests_total{dialect="imgproxy",format="jpeg",status="200"}`,
		`xi_source_fetch_duration_seconds_count{source="fixtures",type="fs"}`,
		`xi_stage_duration_seconds_count{stage="process"}`,
		"xi_vips_mem_bytes", "xi_workers_busy",
	} {
		if !strings.Contains(metricsBody, want) {
			t.Errorf("/metrics lacks %s", want)
		}
	}
}

// TestRunEndToEnd boots the service in dev mode against a local http origin:
// fetch through the shared SSRF client into C memory, admission, vips
// thumbnail, JPEG, SVG sanitising, and the error statuses of each stage.
func TestRunEndToEnd(t *testing.T) {
	t.Parallel()

	origin := httptest.NewServer(http.FileServer(http.Dir(golden.ModuleRoot(t) + "/testdata/fixtures")))
	t.Cleanup(origin.Close)
	ou, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(ou.Port(), 10, 16)
	if err != nil {
		t.Fatal(err)
	}

	cfg, _, err := config.Load(config.LoadOptions{Environ: []string{
		"XI_DEV_MODE=true",
		"XI_SERVER_ADDR=127.0.0.1:0",
		"XI_ADMIN_ADDR=127.0.0.2:0",
		"XI_SERVER_SHUTDOWN_DELAY=0s",
		"XI_SECURITY_UNSIGNED_POLICY=allow",
	}, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	a := start(t, &cfg, hooks{policy: &ssrf.Policy{
		AllowCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		Ports:      []uint16{uint16(port)},
	}})

	base := "http://" + a.public.String()
	checkJPEG(t, get(t, base+"/insecure/rs:fit:64:64/plain/"+origin.URL+"/jpeg/baseline.jpg"), 64, 64)

	for _, tc := range []struct {
		path string
		want int
	}{
		{path: "/insecure/w:10/plain/" + origin.URL + "/missing.jpg", want: http.StatusNotFound},
		{path: "/insecure/w:10/plain/" + origin.URL + "/bmp/rgb24.bmp", want: http.StatusUnprocessableEntity},
		{path: "/insecure/w:10/plain/" + origin.URL + "/svg/logo.svg", want: http.StatusOK},
		{path: "/insecure/w:10/plain/http://169.254.169.254/latest/meta-data", want: http.StatusForbidden},
	} {
		if got := get(t, base+tc.path); got.status != tc.want {
			t.Errorf("GET %s = %d %q, want %d", tc.path, got.status, got.body, tc.want)
		}
	}

	// raw streams the original bytes; the Content-Type is sniffed (the
	// file server's header is not what decides it).
	want, err := os.ReadFile(golden.ModuleRoot(t) + "/testdata/fixtures/png/rgba8.png")
	if err != nil {
		t.Fatal(err)
	}
	if got := get(t, base+"/insecure/raw:1/plain/"+origin.URL+"/png/rgba8.png"); got.status != http.StatusOK || got.ctype != "image/png" || !bytes.Equal(got.body, want) {
		t.Errorf("raw: %d %q, %d bytes; want 200 image/png with the %d source bytes", got.status, got.ctype, len(got.body), len(want))
	}
}
