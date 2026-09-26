//go:build cgo

package app

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/security/ssrf"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
)

// TestRequestExtrasEndToEnd runs WP-64 options through the real service
// (imgproxy dialect, http origin, result cache, libvips, encoder): the
// hashsum is checked on the fetched bytes (a mismatch is 422), cache_tags
// becomes the Cache-Tag header on the miss and on the cache hit, dpi
// reaches the JPEG bytes with the metadata stripped, and none of these is
// reported as ignored.
func TestRequestExtrasEndToEnd(t *testing.T) {
	t.Parallel()

	fixtures := golden.ModuleRoot(t) + "/testdata/fixtures"
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
	cfg, _, err := config.Load(config.LoadOptions{Environ: []string{
		"XI_DEV_MODE=true",
		"XI_SERVER_ADDR=127.0.0.1:0",
		"XI_ADMIN_ADDR=127.0.0.2:0",
		"XI_SERVER_SHUTDOWN_DELAY=0s",
		"XI_SECURITY_UNSIGNED_POLICY=allow",
		"XI_OBSERVABILITY_DEBUG_HEADERS=true",
		"XI_CACHE_RESULT_STORES=memory",
		"XI_CACHE_RESULT_MEMORY_BYTES=8MiB",
	}, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	var store *cache.AsyncStore
	a := start(t, &cfg, hooks{
		policy: &ssrf.Policy{
			AllowCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
			Ports:      []uint16{uint16(port)},
		},
		resultCache: func(s *cache.AsyncStore) { store = s },
	})

	src, err := os.ReadFile(fixtures + "/jpeg/baseline.jpg")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(src)
	digest := hex.EncodeToString(sum[:])
	do := func(options string) (int, http.Header, []byte) {
		t.Helper()
		u := "http://" + a.public.String() + "/insecure/" + options + "/plain/" + origin.URL + "/jpeg/baseline.jpg@jpg"
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
		return resp.StatusCode, resp.Header, body
	}

	opts := "hs:sha256:" + digest + "/ct:product-1:sale/dpi:300/jpgo:1:1/rs:fit:100:100"
	for _, want := range []string{"MISS", "HIT"} {
		status, h, body := do(opts)
		if status != http.StatusOK || h.Get("X-Xi-Cache") != want {
			t.Fatalf("%s: status %d, X-Xi-Cache %q, body %q", want, status, h.Get("X-Xi-Cache"), body)
		}
		if got := h.Get("Cache-Tag"); got != "product-1,sale" {
			t.Errorf("%s: Cache-Tag = %q", want, got)
		}
		if got := h.Get("X-Xi-Ignored"); got != "" {
			t.Errorf("%s: X-Xi-Ignored = %q, want nothing ignored", want, got)
		}
		if d := jfifDPI(body); d != 300 {
			t.Errorf("%s: JFIF density %d dpi, want 300", want, d)
		}
		store.Wait()
	}

	bad := "hs:sha256:" + strings.Repeat("0", 64) + "/rs:fit:100:100"
	if status, h, body := do(bad); status != http.StatusUnprocessableEntity || !strings.Contains(string(body), "hash mismatch") {
		t.Errorf("wrong hashsum: status %d body %q, want 422", status, body)
	} else if h.Get("Cache-Control") != "no-store" {
		t.Errorf("wrong hashsum: Cache-Control %q, want no-store", h.Get("Cache-Control"))
	}
}

// jfifDPI reads the density of a JFIF APP0 in dots per inch (0 when absent
// or in other units).
func jfifDPI(b []byte) int {
	if len(b) < 18 || b[2] != 0xFF || b[3] != 0xE0 || string(b[6:11]) != "JFIF\x00" || b[13] != 1 {
		return 0
	}
	return int(binary.BigEndian.Uint16(b[14:]))
}
