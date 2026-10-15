package client_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	xtandard "github.com/xantiagoma/xtandard-image/clients/go"
	"github.com/xantiagoma/xtandard-image/clients/go/client"
	"github.com/xantiagoma/xtandard-image/clients/go/imgproxy"
)

// standIn is a stand-in server: it records the last request and answers by path.
type standIn struct {
	srv  *httptest.Server
	last chan *http.Request
}

func newStandIn(t *testing.T) *standIn {
	t.Helper()
	s := &standIn{last: make(chan *http.Request, 16)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-State") == "draining" {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, "draining\n")
			return
		}
		_, _ = io.WriteString(w, "ready\n")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.last <- r
		switch {
		case strings.HasPrefix(r.URL.Path, "/info/"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"format":"jpeg","width":640,"height":480,"size":1234,"alpha":false,"exif":{"Make":"Canon"}}`)
		case strings.HasPrefix(r.URL.Path, "/video/") && strings.HasSuffix(r.URL.Path, ".m3u8"):
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(w, "#EXTM3U\n/_stream/t/360.m3u8\n")
		case strings.HasPrefix(r.URL.Path, "/video/"):
			w.Header().Set("Content-Type", "video/webm")
			w.Header().Set("Accept-Ranges", "bytes")
			if r.Header.Get("Range") == "bytes=0-3" {
				w.Header().Set("Content-Range", "bytes 0-3/10")
				w.WriteHeader(http.StatusPartialContent)
				_, _ = io.WriteString(w, "WEBM")
				return
			}
			_, _ = io.WriteString(w, "WEBMWEBMWE")
		case strings.Contains(r.URL.Path, "missing"), strings.HasSuffix(r.URL.Path, "bWlzc2luZw"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"kind":"not_found","message":"source image not found","request_id":"req-1"}}`)
		case strings.HasSuffix(r.URL.Path, "/plain404"):
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "source image not found\n")
		case strings.HasSuffix(r.URL.Path, "/proxy"):
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, "<html>bad gateway</html>")
		case r.URL.Path == "/_playground/api/meta":
			_, _ = io.WriteString(w, `{"version":"dev"}`)
		default:
			w.Header().Set("Content-Type", "image/webp")
			_, _ = io.WriteString(w, "RIFF")
		}
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func (s *standIn) client(t *testing.T, cfg client.Config) *client.Client {
	t.Helper()
	cfg.URLs.BaseURL = s.srv.URL
	c, err := client.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestHealth(t *testing.T) {
	t.Parallel()
	s := newStandIn(t)
	c := s.client(t, client.Config{})
	if err := c.Healthz(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, err := c.Readyz(t.Context())
	if err != nil || !r.Ready || r.State != client.Ready {
		t.Fatalf("Readyz = %+v, %v", r, err)
	}
	draining := s.client(t, client.Config{Header: http.Header{"X-State": {"draining"}}})
	r, err = draining.Readyz(t.Context())
	if err != nil || r.Ready || r.State != client.Draining {
		t.Fatalf("Readyz (draining) = %+v, %v", r, err)
	}
}

func TestInfo(t *testing.T) {
	t.Parallel()
	s := newStandIn(t)
	k, err := xtandard.ParseSigningKey("736563726574", "68656c6c6f", xtandard.DefaultSignatureSize)
	if err != nil {
		t.Fatal(err)
	}
	c := s.client(t, client.Config{URLs: imgproxy.Config{Signing: &k}})
	info, err := c.Info(t.Context(), "s3://b/cat.jpg", imgproxy.InfoParams{Options: []xtandard.InfoOption{xtandard.Opt("size", true)}})
	if err != nil {
		t.Fatal(err)
	}
	if info.Format == nil || *info.Format != "jpeg" || info.Width == nil || *info.Width != 640 || info.EXIF["Make"] != "Canon" {
		t.Errorf("info = %+v", info)
	}
	req := <-s.last
	want, _ := c.URLs().InfoURL("s3://b/cat.jpg", imgproxy.InfoParams{Options: []xtandard.InfoOption{xtandard.Opt("size", true)}})
	if got := s.srv.URL + req.URL.Path; got != want {
		t.Errorf("requested %s, want %s", got, want)
	}
	if req.Header.Get("Accept") != "application/json" {
		t.Errorf("Accept = %q", req.Header.Get("Accept"))
	}
	if _, err := c.InfoAt(t.Context(), strings.TrimPrefix(want, s.srv.URL)); err != nil {
		t.Errorf("InfoAt: %v", err)
	}
}

func TestImageAndHeaders(t *testing.T) {
	t.Parallel()
	s := newStandIn(t)
	c := s.client(t, client.Config{Header: http.Header{"X-Api-Key": {"k"}}})
	res, err := c.Image(t.Context(), "https://e.com/cat.jpg", imgproxy.Params{
		Options: []xtandard.ProcessingOption{xtandard.Opt("w", 300)},
	}, client.Accept("image/avif,image/webp"), client.WithHeader("X-Trace", "1"))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if string(body) != "RIFF" || res.Header.Get("Content-Type") != "image/webp" {
		t.Errorf("body %q type %q", body, res.Header.Get("Content-Type"))
	}
	req := <-s.last
	if req.URL.Path != "/insecure/w:300/aHR0cHM6Ly9lLmNvbS9jYXQuanBn" {
		t.Errorf("path %s", req.URL.Path)
	}
	for k, want := range map[string]string{"Accept": "image/avif,image/webp", "X-Api-Key": "k", "X-Trace": "1"} {
		if got := req.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestVideoRangeAndHead(t *testing.T) {
	t.Parallel()
	s := newStandIn(t)
	c := s.client(t, client.Config{})
	p := imgproxy.VideoParams{Options: []xtandard.VideoOption{xtandard.Opt("vd", 1)}, Extension: "webm"}
	res, err := c.Video(t.Context(), "s3://m/a.mov", p, client.WithRange(xtandard.ByteRange{Start: 0, End: 3}))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusPartialContent || string(body) != "WEBM" {
		t.Errorf("status %d body %q", res.StatusCode, body)
	}
	if r := <-s.last; r.Header.Get("Range") != "bytes=0-3" {
		t.Errorf("Range = %q", r.Header.Get("Range"))
	}
	head, err := c.Video(t.Context(), "s3://m/a.mov", p, client.Head())
	if err != nil {
		t.Fatal(err)
	}
	_ = head.Body.Close()
	if r := <-s.last; r.Method != http.MethodHead || head.Header.Get("Content-Type") != "video/webm" {
		t.Errorf("method %s type %q", r.Method, head.Header.Get("Content-Type"))
	}
	res, err = c.Video(t.Context(), "s3://m/a.mov", p, client.WithRange(xtandard.ByteRange{Start: 5, End: 4}))
	if err == nil {
		_ = res.Body.Close()
	}
	if !errors.Is(err, xtandard.ErrInvalidRange) {
		t.Errorf("bad range: %v", err)
	}
}

func TestStream(t *testing.T) {
	t.Parallel()
	s := newStandIn(t)
	c := s.client(t, client.Config{})
	m, err := c.Stream(t.Context(), "s3://m/a.mov", imgproxy.StreamParams{Protocol: xtandard.StreamProtocolHLS, Renditions: []int{360}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(m.Body, "#EXTM3U") || m.ContentType != "application/vnd.apple.mpegurl" || !strings.HasSuffix(m.URL, ".m3u8") {
		t.Errorf("manifest %+v", m)
	}
	if r := <-s.last; !strings.HasPrefix(r.Header.Get("Accept"), "application/vnd.apple.mpegurl") {
		t.Errorf("Accept = %q", r.Header.Get("Accept"))
	}
	res, err := c.Media(t.Context(), "/_stream/t/360.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	_, err = c.Stream(t.Context(), "s3://m/a.mov", imgproxy.StreamParams{Protocol: "smooth"})
	if !errors.Is(err, xtandard.ErrInvalidRequest) {
		t.Errorf("unknown protocol: %v", err)
	}
}

func TestErrors(t *testing.T) {
	t.Parallel()
	s := newStandIn(t)
	c := s.client(t, client.Config{URLs: imgproxy.Config{SourceEncoding: xtandard.SourceEncodingPlain}})
	res, err := c.Image(t.Context(), "local:///missing.jpg", imgproxy.Params{})
	if err == nil {
		_ = res.Body.Close()
	}
	he, ok := errors.AsType[*client.HTTPError](err)
	if !ok {
		t.Fatalf("got %T %v", err, err)
	}
	if he.Status != http.StatusNotFound || he.Kind != xtandard.ErrorKindNotFound || he.RequestID != "req-1" ||
		he.Message != "source image not found" || he.Body == nil || he.Method != http.MethodGet {
		t.Errorf("HTTPError %+v", he)
	}
	if !errors.Is(err, xtandard.ErrorKindNotFound) || errors.Is(err, xtandard.ErrorKindForbidden) {
		t.Errorf("errors.Is on kinds: %v", err)
	}
	res, err = c.Media(t.Context(), "/proxy")
	if err == nil {
		_ = res.Body.Close()
	}
	he, ok = errors.AsType[*client.HTTPError](err)
	if !ok || he.Status != http.StatusBadGateway || he.Kind != xtandard.ErrorKindUnknown || he.Message != "<html>bad gateway</html>" || he.Body != nil {
		t.Errorf("non-envelope error: %+v", he)
	}
	res, err = c.Media(t.Context(), "/plain404")
	if err == nil {
		_ = res.Body.Close()
	}
	he, ok = errors.AsType[*client.HTTPError](err)
	if !ok || he.Kind != xtandard.ErrorKindNotFound || he.Message != "source image not found" || he.Body != nil {
		t.Errorf("plain-text error: %+v", he)
	}
	res, err = c.Image(t.Context(), "", imgproxy.Params{})
	if err == nil {
		_ = res.Body.Close()
	}
	if !errors.Is(err, xtandard.ErrInvalidSource) {
		t.Errorf("empty source: %v", err)
	}
}

func TestTransportErrors(t *testing.T) {
	t.Parallel()
	s := newStandIn(t)
	c := s.client(t, client.Config{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := c.Healthz(ctx)
	te, ok := errors.AsType[*client.TransportError](err)
	if !ok || !errors.Is(err, context.Canceled) || te.Method != http.MethodGet {
		t.Errorf("cancelled: %T %v", err, err)
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	dead, err := client.New(client.Config{URLs: imgproxy.Config{BaseURL: closed.URL}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dead.Readyz(t.Context()); !isTransport(err) {
		t.Errorf("closed server: %T %v", err, err)
	}
}

func isTransport(err error) bool {
	_, ok := errors.AsType[*client.TransportError](err)
	return ok
}

func TestPlaygroundMetaAndConfig(t *testing.T) {
	t.Parallel()
	s := newStandIn(t)
	c := s.client(t, client.Config{})
	meta, err := c.PlaygroundMeta(t.Context())
	if err != nil || meta["version"] != "dev" {
		t.Errorf("meta %v, %v", meta, err)
	}
	if _, err := client.New(client.Config{}); err == nil {
		t.Error("New accepted an empty base URL")
	}
	if _, err := client.New(client.Config{URLs: imgproxy.Config{BaseURL: "http://x", Separator: "/"}}); !errors.Is(err, xtandard.ErrInvalidSeparator) {
		t.Errorf("bad separator: %v", err)
	}
}
