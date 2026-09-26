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
	"strconv"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/security/ssrf"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
)

// TestRunEndToEnd boots the whole service against a local origin: fetch
// through the SSRF client into C memory, admission, vips thumbnail, JPEG,
// metrics on the admin listener, then a clean shutdown. It needs libvips
// (task docker:test).
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

	cfg, err := ConfigFromEnv(func(k string) string {
		return map[string]string{
			"XI_DEV_MODE":              "true",
			"XI_SERVER_ADDR":           "127.0.0.1:0",
			"XI_ADMIN_ADDR":            "127.0.0.1:0",
			"XI_SERVER_SHUTDOWN_DELAY": "0s",
		}[k]
	})
	if err != nil {
		t.Fatal(err)
	}

	type addrs struct{ public, admin net.Addr }
	bound := make(chan addrs, 1)
	h := hooks{
		listening: func(p, a net.Addr) { bound <- addrs{p, a} },
		policy: &ssrf.Policy{
			AllowCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
			Ports:      []uint16{uint16(port)},
		},
	}

	ctx, cancel := context.WithCancelCause(t.Context())
	done := make(chan error, 1)
	go func() { done <- run(ctx, &cfg, slog.New(slog.DiscardHandler), h) }()

	var a addrs
	select {
	case a = <-bound:
	case err := <-done:
		t.Fatalf("run exited early: %v", err)
	}

	type response struct {
		status int
		ctype  string
		body   []byte
	}
	get := func(u string) response {
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

	base := "http://" + a.public.String()
	img := get(base + "/insecure/rs:fit:64:64/plain/" + origin.URL + "/jpeg/baseline.jpg")
	if img.status != http.StatusOK || img.ctype != "image/jpeg" {
		t.Fatalf("image: %d %q", img.status, img.ctype)
	}
	dims, err := jpeg.DecodeConfig(bytes.NewReader(img.body))
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if dims.Width != 64 || dims.Height != 48 {
		t.Errorf("output %dx%d, want 64x48", dims.Width, dims.Height)
	}

	for _, tc := range []struct {
		path string
		want int
	}{
		{path: "/insecure/w:10/plain/" + origin.URL + "/missing.jpg", want: http.StatusNotFound},
		{path: "/insecure/w:10/plain/" + origin.URL + "/bmp/rgb24.bmp", want: http.StatusUnprocessableEntity},
		{path: "/insecure/w:10/plain/" + origin.URL + "/svg/logo.svg", want: http.StatusOK},
		{path: "/insecure/w:10/plain/http://169.254.169.254/latest/meta-data", want: http.StatusForbidden},
		{path: "/healthz", want: http.StatusOK},
	} {
		if got := get(base + tc.path); got.status != tc.want {
			t.Errorf("GET %s = %d, want %d", tc.path, got.status, tc.want)
		}
	}

	metricsBody := string(get("http://" + a.admin.String() + "/metrics").body)
	for _, want := range []string{"xi_requests_total", "xi_vips_mem_bytes", "xi_workers_busy"} {
		if !strings.Contains(metricsBody, want) {
			t.Errorf("/metrics lacks %s", want)
		}
	}

	cancel(errors.New("test shutdown"))
	if err := <-done; err != nil {
		t.Fatalf("run after shutdown: %v", err)
	}
}
