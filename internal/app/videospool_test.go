package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/security/ssrf"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
)

// Spool files (WP-121b) end to end: the real fetcher, spool, FFmpeg
// shim, server and caches.

// spoolConfig is videoConfig with every source and output over threshold
// bytes spooled and spooled sources shared for ttl.
func spoolConfig(t *testing.T, threshold, maxBytes int64, ttl time.Duration) *config.Config {
	t.Helper()
	cfg := videoConfig(t, config.RoleAll, true)
	cfg.Video.Transform.SpoolThreshold = config.ByteSize(threshold)
	cfg.Video.Transform.SpoolMaxBytes = config.ByteSize(maxBytes)
	cfg.Video.Transform.SpoolTTL = ttl
	return cfg
}

// startSpool starts cfg and returns its base URL, its admin URL and a
// function that waits for the result cache's writes.
func startSpool(t *testing.T, cfg *config.Config, policy *ssrf.Policy) (base, admin string, wait func()) {
	t.Helper()
	var rc *cache.AsyncStore
	a := start(t, cfg, hooks{keepVips: true, policy: policy, resultCache: func(s *cache.AsyncStore) { rc = s }})
	return "http://" + a.public.String(), "http://" + a.admin.String(), func() {
		if rc != nil {
			rc.Wait()
		}
	}
}

func scrape(t *testing.T, admin string) string {
	t.Helper()
	return string(get(t, admin+"/metrics").body)
}

// TestVideoSpool: sources and outputs over the threshold go through spool
// files and come back whole and by ranges; nothing is left once the
// responses and the cache writes are done; a full budget is 503.
func TestVideoSpool(t *testing.T) {
	t.Parallel()
	if !ffmpeg.Available() {
		t.Skip("this image has no FFmpeg")
	}
	cfg := spoolConfig(t, 4<<10, 0, 0)
	base, admin, wait := startSpool(t, cfg, nil)
	const u = "/video/unsafe/w:320/vd:1/plain/video/clip-av1.mp4@webm"
	full := get(t, base+u)
	if full.status != http.StatusOK || len(full.body) <= 4<<10 {
		t.Fatalf("GET: %d, %d bytes", full.status, len(full.body))
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+u, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=1000-4999")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var part bytes.Buffer
	_, _ = part.ReadFrom(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(part.Bytes(), full.body[1000:5000]) {
		t.Fatalf("range: %d, %d bytes", resp.StatusCode, part.Len())
	}
	wait()
	m := scrape(t, admin)
	if in, out := metricValue(t, m, `xi_video_spool_spills_total{kind="input"}`), metricValue(t, m, `xi_video_spool_spills_total{kind="output"}`); in < 1 || out < 1 {
		t.Errorf("spills: %v inputs, %v outputs", in, out)
	}
	if files, b := metricValue(t, m, "xi_video_spool_files"), metricValue(t, m, "xi_video_spool_bytes"); files != 0 || b != 0 {
		t.Errorf("%v spool files (%v bytes) left", files, b)
	}
	if entries, err := os.ReadDir(cfg.Video.Transform.SpoolPath()); err != nil || len(entries) != 0 {
		t.Errorf("spool directory: %v %v", entries, err)
	}

	t.Run("budget", func(t *testing.T) {
		t.Parallel()
		cfg := spoolConfig(t, 4<<10, 8<<10, 0) // the source alone is 39 KB
		base, admin, _ := startSpool(t, cfg, nil)
		r := get(t, base+u)
		if r.status != http.StatusServiceUnavailable || r.header.Get("Retry-After") != "5" {
			t.Fatalf("over the spool budget: %d, Retry-After %q: %s", r.status, r.header.Get("Retry-After"), r.body)
		}
		if n := metricValue(t, scrape(t, admin), "xi_video_spool_rejections_total"); n != 1 {
			t.Errorf("%v rejections", n)
		}
	})
}

// countingOrigin serves the fixtures over HTTP and counts the requests
// for each path.
type countingOrigin struct {
	srv   *httptest.Server
	paths map[string]*atomic.Int64
}

func newCountingOrigin(t *testing.T, paths ...string) (*countingOrigin, *ssrf.Policy) {
	t.Helper()
	o := &countingOrigin{paths: map[string]*atomic.Int64{}}
	for _, p := range paths {
		o.paths[p] = new(atomic.Int64)
	}
	files := http.FileServer(http.Dir(filepath.Join(golden.ModuleRoot(t), "testdata", "fixtures")))
	o.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, ok := o.paths[r.URL.Path]; ok {
			c.Add(1)
		}
		files.ServeHTTP(w, r)
	}))
	t.Cleanup(o.srv.Close)
	ou, err := url.Parse(o.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(ou.Port(), 10, 16)
	if err != nil {
		t.Fatal(err)
	}
	return o, &ssrf.Policy{AllowCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}, Ports: []uint16{uint16(port)}}
}

// TestStreamSharesSpooledSource: the manifest and every segment of an HLS
// stream need the source; with the spool they share one download (the
// origin cache is off, so nothing else could), without it every part
// downloads it again.
func TestStreamSharesSpooledSource(t *testing.T) {
	t.Parallel()
	if !ffmpeg.Available() {
		t.Skip("this image has no FFmpeg")
	}
	const file = "/video/stream-vp9-opus.webm"
	for _, tc := range []struct {
		name      string
		threshold int64
		ttl       time.Duration
		fetches   func(n int64, parts int) bool
	}{
		{"spooled and shared", 4 << 10, time.Minute, func(n int64, _ int) bool { return n == 1 }},
		{"in memory", 0, 0, func(n int64, parts int) bool { return n >= int64(parts) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			origin, policy := newCountingOrigin(t, file)
			cfg := spoolConfig(t, tc.threshold, 0, tc.ttl)
			cfg.Cache.Origin.MemoryBytes = 0
			cfg.Cache.Result.MemoryBytes = 0
			cfg.Video.Transform.StreamCacheBytes = 0
			cfg.Video.Transform.PrefetchSegments = 0
			cfg.Video.Transform.SegmentDuration = 2 * time.Second
			cfg.Sources["fixtures"] = config.Source{Type: config.SourceHTTP, BaseURL: origin.srv.URL}
			base, admin, _ := startSpool(t, cfg, policy)
			s := newVideoSigner(t)
			master := get(t, base+s.video(t, "/st:hls:240/plain"+file+"@m3u8"))
			if master.status != http.StatusOK {
				t.Fatalf("master: %d %s", master.status, master.body)
			}
			parts := 0
			for _, pl := range playlistURIs(master.body) {
				media := get(t, base+pl)
				if media.status != http.StatusOK {
					t.Fatalf("%s: %d", pl, media.status)
				}
				prefix := pl[:strings.LastIndexByte(pl, '/')+1]
				for _, u := range playlistURIs(media.body) {
					if r := get(t, base+prefix+u); r.status != http.StatusOK {
						t.Fatalf("%s: %d %s", u, r.status, r.body)
					}
					parts++
				}
			}
			n := origin.paths[file].Load()
			t.Logf("%d stream parts, %d origin downloads", parts, n)
			if !tc.fetches(n, parts) {
				t.Errorf("%d origin downloads for %d parts", n, parts)
			}
			if tc.threshold > 0 {
				m := scrape(t, admin)
				if v := metricValue(t, m, `xi_video_spool_spills_total{kind="input"}`); v != 1 {
					t.Errorf("%v sources spooled, want 1", v)
				}
			}
		})
	}
}
