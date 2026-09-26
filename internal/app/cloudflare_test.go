//go:build cgo

package app

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/security/ssrf"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
)

// TestCloudflareDialects boots the service with both Cloudflare dialects
// (WP-43, WP-79) enabled: /cdn-cgi/image/ with a relative and an absolute
// source, onerror=redirect answering 302 to a missing origin image, and
// Cloudflare Images delivery by prefix and by imagedelivery.net host, with
// a variant preset, a flexible variant and sig/exp signed URLs.
func TestCloudflareDialects(t *testing.T) {
	t.Parallel()
	fixtures := filepath.Join(golden.ModuleRoot(t), "testdata", "fixtures")
	origin := httptest.NewServer(http.FileServer(http.Dir(fixtures)))
	t.Cleanup(origin.Close)
	ou, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(ou.Port(), 10, 16)
	if err != nil {
		t.Fatal(err)
	}

	const key = "IMAGES_SIGNING_KEY"
	yml := `dev_mode: true
server:
  addr: "127.0.0.1:0"
  shutdown_delay: 0s
admin:
  addr: "127.0.0.2:0"
security:
  unsigned_policy: allow
sources:
  fixtures:
    type: fs
    root: ` + strconv.Quote(fixtures) + `
dialects:
  cloudflare:
    enabled: true
    source: fixtures
  cloudflare_images:
    enabled: true
    hosts: [imagedelivery.net]
    keys: [` + key + `]
    accounts: {acct: fixtures}
presets:
  thumbnail: "rs:fit:64:64"
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(config.LoadOptions{ConfigFile: path})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	a := start(t, &cfg, hooks{policy: &ssrf.Policy{
		AllowCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		Ports:      []uint16{uint16(port)},
	}})
	base := "http://" + a.public.String()

	signer, err := sign.NewSigner(sign.NewCloudflareScheme(), sign.NewKeyring(sign.NewKey([]byte(key), nil)))
	if err != nil {
		t.Fatal(err)
	}
	signed := func(p string, exp time.Time) string {
		q, err := signer.SignCloudflareQuery(p, "exp="+strconv.FormatInt(exp.Unix(), 10))
		if err != nil {
			t.Fatal(err)
		}
		return p + "?" + q
	}

	checkJPEG(t, get(t, base+"/cdn-cgi/image/width=64,height=64,fit=scale-down/jpeg/baseline.jpg"), 64, 64)
	checkJPEG(t, get(t, base+"/cdn-cgi/image/w=48,f=jpeg/"+origin.URL+"/jpeg/baseline.jpg"), 48, 48)
	checkJPEG(t, get(t, base+"/cdn-cgi/imagedelivery/acct/jpeg/baseline.jpg/thumbnail"), 64, 64)
	checkJPEG(t, get(t, base+"/cdn-cgi/imagedelivery/acct/jpeg/baseline.jpg/w=32"), 32, 32*4)
	checkJPEG(t, get(t, base+"/cdn-cgi/imagedelivery"+signed("/acct/jpeg/baseline.jpg/thumbnail", time.Now().Add(time.Hour))), 64, 64)

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	do := func(host, p string) (status int, location string) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+p, http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		if host != "" {
			req.Host = host
		}
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode, resp.Header.Get("Location")
	}

	for _, tc := range []struct {
		name, host, path string
		want             int
		location         string
	}{
		{"onerror redirect", "", "/cdn-cgi/image/w=64,onerror=redirect/" + origin.URL + "/missing.jpg", http.StatusFound, origin.URL + "/missing.jpg"},
		{"no onerror", "", "/cdn-cgi/image/w=64/" + origin.URL + "/missing.jpg", http.StatusNotFound, ""},
		{"onerror on a local source", "", "/cdn-cgi/image/w=64,onerror=redirect/jpeg/missing.jpg", http.StatusNotFound, ""},
		{"imagedelivery.net host", "imagedelivery.net", "/acct/jpeg/baseline.jpg/thumbnail", http.StatusOK, ""},
		{"host signed", "imagedelivery.net", signed("/acct/jpeg/baseline.jpg/thumbnail", time.Now().Add(time.Hour)), http.StatusOK, ""},
		{"host expired", "imagedelivery.net", signed("/acct/jpeg/baseline.jpg/thumbnail", time.Now().Add(-time.Hour)), http.StatusNotFound, ""},
		{"bad signature", "imagedelivery.net", "/acct/jpeg/baseline.jpg/thumbnail?exp=9999999999&sig=00", http.StatusForbidden, ""},
		{"unknown account", "", "/cdn-cgi/imagedelivery/other/jpeg/baseline.jpg/thumbnail", http.StatusForbidden, ""},
		{"unknown variant", "", "/cdn-cgi/imagedelivery/acct/jpeg/baseline.jpg/hero", http.StatusBadRequest, ""},
	} {
		status, got := do(tc.host, tc.path)
		if status != tc.want {
			t.Errorf("%s: GET %s = %d, want %d", tc.name, tc.path, status, tc.want)
		}
		if got != tc.location {
			t.Errorf("%s: Location = %q, want %q", tc.name, got, tc.location)
		}
	}
}
