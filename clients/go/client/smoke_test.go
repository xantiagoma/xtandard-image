package client_test

// Smoke test against a running server, skipped unless XI_SMOKE_URL is set.
// The playground demo (compose.playground.yml, unsigned URLs allowed) works:
//
//	XI_SMOKE_URL=http://127.0.0.1:8080 go test ./client -run Smoke -v
//
// It needs the demo's local fixtures (xi://local/…) and, for the /video
// cases, a full or gpl image with XI_VIDEO_TRANSFORM_ENABLED.

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	xtandard "github.com/xantiagoma/xtandard-image/clients/go"
	"github.com/xantiagoma/xtandard-image/clients/go/client"
	"github.com/xantiagoma/xtandard-image/clients/go/imgproxy"
	"github.com/xantiagoma/xtandard-image/clients/go/info"
	"github.com/xantiagoma/xtandard-image/clients/go/processing"
	"github.com/xantiagoma/xtandard-image/clients/go/query"
	"github.com/xantiagoma/xtandard-image/clients/go/video"
)

func smoke(t *testing.T, cfg imgproxy.Config) *client.Client {
	t.Helper()
	base := os.Getenv("XI_SMOKE_URL")
	if base == "" {
		t.Skip("set XI_SMOKE_URL to run against a server")
	}
	cfg.BaseURL = base
	c, err := client.New(client.Config{URLs: cfg})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func source(t *testing.T, key string) string {
	t.Helper()
	s, err := xtandard.XiSource("local", key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSmokeHealthAndImage(t *testing.T) {
	t.Parallel()
	c := smoke(t, imgproxy.Config{SourceEncoding: xtandard.SourceEncodingPlain})
	if err := c.Healthz(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r, err := c.Readyz(t.Context()); err != nil || !r.Ready {
		t.Fatalf("Readyz = %+v, %v", r, err)
	}
	img := source(t, "jpeg/baseline.jpg")
	res, err := c.Image(t.Context(), img, imgproxy.Params{
		Options:   []xtandard.ProcessingOption{processing.Resize{Type: processing.ResizeTypeFit, Width: new(64), Height: new(64)}},
		Extension: "webp",
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.Header.Get("Content-Type") != "image/webp" || !strings.HasPrefix(string(b), "RIFF") {
		t.Errorf("type %q, %d bytes", res.Header.Get("Content-Type"), len(b))
	}
	in, err := c.Info(t.Context(), img, imgproxy.InfoParams{Options: []xtandard.InfoOption{
		info.Size{Size: new(true)}, info.Format{Format: new(true)}, info.Dimensions{Dimensions: new(true)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if in.Format == nil || *in.Format != "jpeg" || in.Width == nil || *in.Width <= 0 || in.Size == nil || *in.Size <= 0 {
		t.Errorf("info %+v", in)
	}
	res, err = c.Image(t.Context(), source(t, "jpeg/missing.jpg"), imgproxy.Params{})
	if err == nil {
		_ = res.Body.Close()
	}
	if he, ok := errors.AsType[*client.HTTPError](err); !ok || he.Status != http.StatusNotFound || !errors.Is(err, xtandard.ErrorKindNotFound) {
		t.Errorf("missing source: %v", err)
	}
}

func TestSmokeQueryDialect(t *testing.T) {
	t.Parallel()
	c := smoke(t, imgproxy.Config{})
	q, err := query.New(query.Config{BaseURL: os.Getenv("XI_SMOKE_URL")})
	if err != nil {
		t.Fatal(err)
	}
	url, err := q.URL(source(t, "jpeg/baseline.jpg"), query.Params{Params: []query.Param{query.P("w", 32), query.P("f", "png")}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Media(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.Header.Get("Content-Type") != "image/png" {
		t.Errorf("type %q", res.Header.Get("Content-Type"))
	}
}

func TestSmokeVideoRangeAndStream(t *testing.T) {
	t.Parallel()
	c := smoke(t, imgproxy.Config{})
	vid := source(t, "video/bbb-h264-aac.mp4")
	p := imgproxy.VideoParams{
		Options:   []xtandard.VideoOption{video.Duration{Seconds: 1}, video.Width{Width: 160}},
		Extension: "webm",
	}
	res, err := c.Video(t.Context(), vid, p, client.WithRange(xtandard.ByteRange{Start: 0, End: 99}))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusPartialContent {
		t.Errorf("status %d", res.StatusCode)
	}
	head, err := c.Video(t.Context(), vid, p, client.Head())
	if err != nil {
		t.Fatal(err)
	}
	_ = head.Body.Close()
	if !strings.HasPrefix(head.Header.Get("Content-Type"), "video/webm") {
		t.Errorf("HEAD type %q", head.Header.Get("Content-Type"))
	}
	m, err := c.Stream(t.Context(), vid, imgproxy.StreamParams{Protocol: xtandard.StreamProtocolHLS, Renditions: []int{240}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(m.Body, "#EXTM3U") {
		t.Fatalf("manifest %q", m.Body)
	}
	var child string
	for sc := bufio.NewScanner(strings.NewReader(m.Body)); sc.Scan(); {
		if l := sc.Text(); l != "" && !strings.HasPrefix(l, "#") {
			child = l
			break
		}
	}
	res, err = c.Media(t.Context(), child)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("child %s: status %d", child, res.StatusCode)
	}
}
