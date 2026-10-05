package playground

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// streamImage is a public handler serving a fake stream: an HLS master
// naming its parts under /img/_stream/ (the test path prefix), a media
// playlist and a segment.
type streamImage struct{ got []*http.Request }

func (s *streamImage) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.got = append(s.got, r)
	switch {
	case strings.HasSuffix(r.URL.Path, "@m3u8"):
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("ETag", `"master"`)
		_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\n/img/_stream/TICKET/v0.m3u8\n"))
	case strings.HasSuffix(r.URL.Path, "@mpd"):
		w.Header().Set("Content-Type", "application/dash+xml")
		_, _ = w.Write([]byte("<MPD><BaseURL>/img/_stream/TICKET/</BaseURL></MPD>"))
	case strings.HasSuffix(r.URL.Path, ".m3u8"):
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-MAP:URI=\"v0/init.mp4\"\nv0/0.m4s\n"))
	default:
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("moof"))
	}
}

func TestStreamRewrite(t *testing.T) {
	t.Parallel()
	img := &streamImage{}
	h, err := New(Config{Path: DefaultPath}, &Deps{Meta: testMeta(), Image: img, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{h: h}

	// The master through api/render names its parts through api/stream.
	res, body := f.do(t, get(t, "/_playground/api/render?u=/video/sig/st:hls/plain/a.mp4@m3u8"))
	want := "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\n/_playground/api/stream/TICKET/v0.m3u8\n"
	if res.StatusCode != http.StatusOK || string(body) != want || res.Header.Get("Content-Length") != strconv.Itoa(len(want)) ||
		res.Header.Get("ETag") != "" || res.Header.Get("Server-Timing") == "" {
		t.Fatalf("master: %d %v %q", res.StatusCode, res.Header, body)
	}
	_, body = f.do(t, get(t, "/_playground/api/render?u=/video/sig/st:dash/plain/a.mp4@mpd"))
	if string(body) != "<MPD><BaseURL>/_playground/api/stream/TICKET/</BaseURL></MPD>" {
		t.Fatalf("mpd %q", body)
	}

	// A media playlist through api/stream: relative parts, unchanged; the
	// manifest is never asked for by range.
	req := get(t, "/_playground/api/stream/TICKET/v0.m3u8")
	req.Header.Set("Range", "bytes=0-3")
	res, body = f.do(t, req)
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "v0/0.m4s") {
		t.Fatalf("playlist: %d %q", res.StatusCode, body)
	}
	if got := img.got[len(img.got)-1]; got.URL.Path != "/img/_stream/TICKET/v0.m3u8" || got.Header.Get("Range") != "" {
		t.Fatalf("public handler got %q with Range %q", got.URL.Path, got.Header.Get("Range"))
	}
	// A segment passes through with its range and validators.
	req = get(t, "/_playground/api/stream/TICKET/v0/0.m4s")
	req.Header.Set("Range", "bytes=0-3")
	req.Header.Set("Cookie", "secret")
	res, body = f.do(t, req)
	got := img.got[len(img.got)-1]
	if res.StatusCode != http.StatusPartialContent || string(body) != "moof" || got.URL.Path != "/img/_stream/TICKET/v0/0.m4s" ||
		got.Header.Get("Range") != "bytes=0-3" || got.Header.Get("Cookie") != "" {
		t.Fatalf("segment: %d %q, public handler got %q %v", res.StatusCode, body, got.URL.Path, got.Header)
	}
	if res, _ := f.do(t, get(t, "/_playground/api/stream/")); res.StatusCode != http.StatusBadRequest && res.StatusCode != http.StatusNotFound {
		t.Fatalf("empty part: %d", res.StatusCode)
	}
}
