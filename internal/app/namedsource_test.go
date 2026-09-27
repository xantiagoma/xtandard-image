//go:build cgo

package app

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/security/ssrf"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
)

// TestNamedHTTPSourceAbsoluteKey is the WP-95 owner report end to end: an
// http source with an allowlist and no base_url serves
// xi://web/<absolute url> in the plain and base64 forms like the bare URL,
// and refuses other keys with a 403 that names the failed check.
func TestNamedHTTPSourceAbsoluteKey(t *testing.T) {
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
		"XI_SERVER_ADDR=127.0.0.1:0",
		"XI_ADMIN_ADDR=127.0.0.2:0",
		"XI_SERVER_SHUTDOWN_DELAY=0s",
		"XI_SECURITY_UNSIGNED_POLICY=allow",
		"XI_SOURCES=web",
		"XI_SOURCES_WEB_TYPE=http",
		"XI_SOURCES_WEB_ALLOW=127.0.0.1",
	}, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	a := start(t, &cfg, hooks{policy: &ssrf.Policy{
		AllowCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		Ports:      []uint16{uint16(port)},
	}})
	base := "http://" + a.public.String()
	img := origin.URL + "/jpeg/baseline.jpg"
	b64 := base64.RawURLEncoding.EncodeToString([]byte("xi://web/" + img))

	for _, path := range []string{
		"/insecure/rs:fit:64:64/plain/" + img + "@jpg",
		"/insecure/rs:fit:64:64/plain/xi://web/" + img + "@jpg",
		"/insecure/rs:fit:64:64/plain/" + url.PathEscape("xi://web/"+img) + "@jpg",
		"/insecure/rs:fit:64:64/" + b64 + ".jpg",
	} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			checkJPEG(t, get(t, base+path), 64, 64)
		})
	}

	for _, tc := range []struct {
		path, body string
	}{
		{"/insecure/w:10/plain/xi://web/ftp://x", `source "web" has no base_url: the key must be an absolute http(s) url`},
		{"/insecure/w:10/plain/xi://web/jpeg/baseline.jpg", `source "web" has no base_url: the key must be an absolute http(s) url`},
		{"/insecure/w:10/plain/xi://web/https://example.com/a.jpg", `source "web": host not in the source allowlist`},
		{"/insecure/w:10/plain/xi://nope/" + img, `unknown source "nope"`},
		{"/insecure/w:10/plain/https://example.com/a.jpg", "no source allows this url"},
	} {
		got := get(t, base+tc.path)
		if got.status != http.StatusForbidden || strings.TrimSpace(string(got.body)) != tc.body {
			t.Errorf("GET %s = %d %q, want 403 %q", tc.path, got.status, got.body, tc.body)
		}
	}
}
