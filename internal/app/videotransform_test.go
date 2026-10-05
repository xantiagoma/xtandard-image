//go:build cgo

package app

import (
	"bytes"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/router"
	"github.com/xantiagoma/xtandard-image/internal/server"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
)

// videoTestRouter is the minimal internal URL syntax of video
// transformations until the dialects have theirs (WP-122, WP-125):
// /_video/<source path>?w=&h=&start=&dur=&c=&mute=1, with the source
// resolved by the imgproxy mount.
type videoTestRouter struct{ next server.Router }

func (v videoTestRouter) Route(r *http.Request) (*dialect.Result, *router.Mount, error) {
	rest, ok := strings.CutPrefix(r.URL.Path, "/_video/")
	if !ok {
		return v.next.Route(r)
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path, r2.URL.RawPath, r2.URL.RawQuery = "/unsafe/plain/"+rest, "", ""
	res, m, err := v.next.Route(r2)
	if err != nil {
		return res, m, err
	}
	q := r.URL.Query()
	num := func(k string) float64 { f, _ := strconv.ParseFloat(q.Get(k), 64); return f }
	op := &transform.VideoOp{
		Width: int(num("w")), Height: int(num("h")),
		Start:    time.Duration(num("start") * float64(time.Second)),
		Duration: time.Duration(num("dur") * float64(time.Second)),
		Mute:     q.Get("mute") == "1",
	}
	if c := q.Get("c"); c != "" {
		if op.Container, err = transform.ParseVideoContainer(c); err != nil {
			return nil, m, err
		}
	}
	res.Request.Kind, res.Request.Video, res.Request.Output = transform.KindVideo, op, transform.Output{}
	return res, m, nil
}

func videoConfig(t *testing.T, role config.Role, enabled bool) *config.Config {
	t.Helper()
	fixtures := filepath.Join(golden.ModuleRoot(t), "testdata", "fixtures")
	yml := `role: ` + string(role) + `
server:
  addr: "127.0.0.1:0"
  shutdown_delay: 0s
admin:
  addr: "127.0.0.2:0"
signing:
  required: "false"
security:
  unsigned_policy: allow
observability:
  debug_headers: true
sources:
  fixtures:
    type: fs
    root: ` + strconv.Quote(fixtures) + `
dialects:
  imgproxy:
    source: fixtures
video:
  transform:
    enabled: ` + strconv.FormatBool(enabled) + `
    workers: 2
    unsigned_policy: allow
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

func startVideo(t *testing.T, cfg *config.Config) addrs {
	t.Helper()
	return start(t, cfg, hooks{
		keepVips: true,
		router:   func(next server.Router) server.Router { return videoTestRouter{next: next} },
	})
}

const (
	videoClip  = "/_video/video/clip-vp9.webm?w=320&start=0.5&dur=1&c=webm"
	videoImage = "/unsafe/rs:fit:64:64/plain/jpeg/baseline.jpg@png"
)

// TestVideoTransformRoles: what each XI_ROLE serves, end to end.
func TestVideoTransformRoles(t *testing.T) {
	if !ffmpeg.Available() {
		t.Skip("this image has no FFmpeg")
	}
	for _, tc := range []struct {
		role         config.Role
		image, video int
	}{
		{config.RoleAll, http.StatusOK, http.StatusOK},
		{config.RoleImage, http.StatusOK, http.StatusNotFound},
		{config.RoleVideo, http.StatusNotFound, http.StatusOK},
	} {
		t.Run(string(tc.role), func(t *testing.T) {
			a := startVideo(t, videoConfig(t, tc.role, tc.role != config.RoleImage))
			base := "http://" + a.public.String()
			if r := get(t, base+videoImage); r.status != tc.image {
				t.Errorf("image: %d, want %d", r.status, tc.image)
			}
			r := get(t, base+videoClip)
			if r.status != tc.video {
				t.Fatalf("video: %d %s, want %d", r.status, r.body, tc.video)
			}
			if r := get(t, base+"/healthz"); r.status != http.StatusOK {
				t.Errorf("healthz %d", r.status)
			}
			if tc.video != http.StatusOK {
				return
			}
			if r.ctype != "video/webm" || r.header.Get("Accept-Ranges") != "bytes" || r.header.Get("X-Xi-Video-Codec") != "vp9" {
				t.Errorf("headers %v", r.header)
			}
			if !bytes.HasPrefix(r.body, []byte{0x1a, 0x45, 0xdf, 0xa3}) {
				t.Errorf("body is not Matroska: % x", r.body[:min(8, len(r.body))])
			}
		})
	}
}

// TestVideoTransformDisabled: off (the default), video transformations
// are 404 and nothing else changes; on without FFmpeg, startup fails.
func TestVideoTransformDisabled(t *testing.T) {
	t.Run("off", func(t *testing.T) {
		a := startVideo(t, videoConfig(t, config.RoleAll, false))
		if r := get(t, "http://"+a.public.String()+videoClip); r.status != http.StatusNotFound {
			t.Fatalf("video: %d, want 404", r.status)
		}
	})
	t.Run("unsigned denied by default", func(t *testing.T) {
		if !ffmpeg.Available() {
			t.Skip("this image has no FFmpeg")
		}
		cfg := videoConfig(t, config.RoleAll, true)
		cfg.Video.Transform.UnsignedPolicy = config.Defaults().Video.Transform.UnsignedPolicy
		a := startVideo(t, cfg)
		r := get(t, "http://"+a.public.String()+videoClip)
		if r.status != http.StatusForbidden || !strings.Contains(string(r.body), "unsigned request not allowed") {
			t.Fatalf("video: %d %s, want 403", r.status, r.body)
		}
		if r := get(t, "http://"+a.public.String()+videoImage); r.status != http.StatusOK {
			t.Errorf("unsigned image on the allow mount: %d, want 200", r.status)
		}
	})
	t.Run("image role builds no pool", func(t *testing.T) {
		if !ffmpeg.Available() {
			t.Skip("this image has no FFmpeg")
		}
		cfg := videoConfig(t, config.RoleImage, true)
		vt, err := buildVideoTransform(cfg, nil)
		if err != nil || vt != nil {
			t.Fatalf("buildVideoTransform = %v, %v; want no service", vt, err)
		}
	})
	t.Run("no ffmpeg", func(t *testing.T) {
		if ffmpeg.Available() {
			t.Skip("this image has FFmpeg")
		}
		cfg := videoConfig(t, config.RoleAll, true)
		err := run(t.Context(), cfg, slog.New(slog.DiscardHandler), Options{Version: "test"}, hooks{keepVips: true})
		if err == nil || !strings.Contains(err.Error(), "FFmpeg") {
			t.Fatalf("run = %v, want an FFmpeg error", err)
		}
	})
}

// TestVideoTransformService: ranges and coalescing through the real
// stack: identical concurrent requests are transcoded once.
func TestVideoTransformService(t *testing.T) {
	if !ffmpeg.Available() {
		t.Skip("this image has no FFmpeg")
	}
	a := startVideo(t, videoConfig(t, config.RoleAll, true))
	base := "http://" + a.public.String()
	full := get(t, base+videoClip)
	if full.status != http.StatusOK {
		t.Fatalf("GET: %d %s", full.status, full.body)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+videoClip, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=100-199")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var part bytes.Buffer
	_, _ = part.ReadFrom(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(part.Bytes(), full.body[100:200]) {
		t.Fatalf("range: %d, %d bytes", resp.StatusCode, part.Len())
	}

	// Another output, asked for by eight clients at once.
	const n = 8
	u := base + "/_video/video/clip-av1.mp4?w=256&c=webm&dur=1.5"
	var wg sync.WaitGroup
	bodies := make([][]byte, n)
	for i := range n {
		wg.Go(func() {
			r := get(t, u)
			if r.status != http.StatusOK {
				t.Errorf("concurrent GET: %d %s", r.status, r.body)
			}
			bodies[i] = r.body
		})
	}
	wg.Wait()
	for i := range bodies {
		if !bytes.Equal(bodies[i], bodies[0]) {
			t.Fatalf("response %d differs", i)
		}
	}
	m := string(get(t, "http://"+a.admin.String()+"/metrics").body)
	transcodes := metricValue(t, m, `xi_video_transform_duration_seconds_count{result="ok"}`)
	coalesced := metricValue(t, m, `xi_video_transform_coalesced_total`)
	// The first GET and the range each transcoded (no result cache); the
	// eight concurrent ones transcoded at least once and joined otherwise.
	if transcodes+coalesced != n+2 || coalesced < 1 {
		t.Errorf("%v transcodes and %v coalesced for %d requests", transcodes, coalesced, n+2)
	}
}

// metricValue reads one sample of a Prometheus text exposition.
func metricValue(t *testing.T, text, name string) float64 {
	t.Helper()
	m := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + ` (\S+)$`).FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("no %s in /metrics", name)
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
