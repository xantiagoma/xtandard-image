//go:build cgo

package app

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/security/ssrf"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
)

// TestResultCacheEndToEnd runs the real pipeline (http origin, SSRF
// client, C-memory buffers, libvips, encoder) with the memory result
// cache and no origin cache: the second identical request is a HIT that
// never reaches the origin or libvips, answers faster with the same
// headers and bytes, and the cache metrics are exported.
func TestResultCacheEndToEnd(t *testing.T) {
	t.Parallel()

	var originHits atomic.Int32
	files := http.FileServer(http.Dir(golden.ModuleRoot(t) + "/testdata/fixtures"))
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originHits.Add(1)
		files.ServeHTTP(w, r)
	}))
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
		"XI_CACHE_ORIGIN_MEMORY_BYTES=0", // every miss reaches the origin
		"XI_CACHE_RESULT_STORES=memory,disk",
		"XI_CACHE_RESULT_DISK_PATH=/nonexistent", // not implemented yet: skipped with a warning
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
	if store == nil {
		t.Fatal("result cache not built")
	}

	u := "http://" + a.public.String() + "/insecure/rs:fit:300:300/q:80/plain/" + origin.URL + "/jpeg/baseline.jpg@webp"
	type reply struct {
		status int
		header http.Header
		body   []byte
		took   time.Duration
	}
	fetchOnce := func() reply {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, u, http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return reply{resp.StatusCode, resp.Header, body, time.Since(start)}
	}

	miss := fetchOnce()
	if miss.status != http.StatusOK || miss.header.Get("X-Xi-Cache") != "MISS" {
		t.Fatalf("first: %d %q %q", miss.status, miss.header.Get("X-Xi-Cache"), miss.body)
	}
	store.Wait()
	hit := fetchOnce()
	if hit.status != http.StatusOK || hit.header.Get("X-Xi-Cache") != "HIT" {
		t.Fatalf("second: %d %q", hit.status, hit.header.Get("X-Xi-Cache"))
	}
	if n := originHits.Load(); n != 1 {
		t.Errorf("origin hit %d times, want 1", n)
	}
	if !bytes.Equal(miss.body, hit.body) || hit.header.Get("Content-Type") != "image/webp" {
		t.Errorf("hit body differs (%d vs %d bytes) or type %q", len(miss.body), len(hit.body), hit.header.Get("Content-Type"))
	}
	strip := func(h http.Header) http.Header {
		c := h.Clone()
		for _, k := range []string{"X-Xi-Cache", "X-Request-ID", "Date"} {
			c.Del(k)
		}
		return c
	}
	if diff := cmp.Diff(strip(miss.header), strip(hit.header)); diff != "" {
		t.Errorf("hit headers differ (-miss +hit):\n%s", diff)
	}
	if hit.took >= miss.took {
		t.Errorf("hit took %s, miss %s: the hit should skip fetch and libvips", hit.took, miss.took)
	}
	t.Logf("miss %s, hit %s", miss.took, hit.took)

	metricsBody := string(get(t, "http://"+a.admin.String()+"/metrics").body)
	for _, want := range []string{
		`xi_cache_requests_total{result="hit",store="memory"} 1`,
		`xi_cache_requests_total{result="miss",store="memory"} 1`,
		`xi_cache_entries{store="memory"} 1`,
		`xi_stage_duration_seconds_count{stage="process"} 1`,
		`xi_stage_duration_seconds_count{stage="cache"} 2`,
		"xi_cache_writes_dropped_total 0",
	} {
		if !strings.Contains(metricsBody, want) {
			t.Errorf("/metrics lacks %s", want)
		}
	}
}
