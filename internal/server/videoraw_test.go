package server

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// webmSource is the start of a WebM file (EBML header, doc type webm)
// followed by distinct bytes.
var webmSource = func() []byte {
	b := append([]byte("\x1a\x45\xdf\xa3\x9f\x42\x86\x81\x01\x42\x82\x84webm"), make([]byte, 48)...)
	for i := 16; i < len(b); i++ {
		b[i] = byte(i * 3)
	}
	return b
}()

// newRawHarness serves raw:1 video requests of webmSource.
func newRawHarness(tb testing.TB, mutate func(*harness)) (*harness, *fakeVideo) {
	tb.Helper()
	return newVideoHarness(tb, true, func(h *harness) {
		h.router.mutate = videoRequest(&transform.VideoOp{Raw: true, Width: 640, Start: 1e9})
		h.f.data = webmSource
		if mutate != nil {
			mutate(h)
		}
	})
}

// TestVideoRaw: raw:1 on /video streams the media source unmodified with
// its sniffed type, answers ranges, transcodes and caches nothing.
func TestVideoRaw(t *testing.T) {
	t.Parallel()
	h, fv := newRawHarness(t, func(h *harness) {
		h.cfg.Video.MaxSrcBytes = 1 << 30
		h.cfg.DebugHeaders = true
	})
	rec := h.do(t, http.MethodGet, videoPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	for k, v := range map[string]string{
		"Content-Type": "video/webm", "Accept-Ranges": "bytes", "Content-Length": "64",
		"X-Content-Type-Options": "nosniff", "Content-Disposition": `inline; filename="a.webm"`,
	} {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if !bytes.Equal(rec.Body.Bytes(), webmSource) {
		t.Error("the body is not the source")
	}
	if got := h.f.maxBytes.Load(); got != 1<<30 {
		t.Errorf("fetch limit %d, want XI_VIDEO_TRANSFORM_MAX_INPUT_BYTES (%d)", got, 1<<30)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}

	rng := h.do(t, http.MethodGet, videoPath, http.Header{"Range": {"bytes=4-11"}})
	if rng.Code != http.StatusPartialContent || !bytes.Equal(rng.Body.Bytes(), webmSource[4:12]) ||
		rng.Header().Get("Content-Range") != "bytes 4-11/64" {
		t.Fatalf("range: %d %q", rng.Code, rng.Header().Get("Content-Range"))
	}
	if rec := h.do(t, http.MethodGet, videoPath, http.Header{"Range": {"bytes=64-"}}); rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Errorf("unsatisfiable range: %d", rec.Code)
	}
	if rec := h.do(t, http.MethodGet, videoPath, http.Header{"Range": {"bytes=0-1"}, "If-Range": {etag}}); rec.Code != http.StatusPartialContent {
		t.Errorf("If-Range: %d", rec.Code)
	}
	if rec := h.do(t, http.MethodGet, videoPath, http.Header{"If-None-Match": {etag}}); rec.Code != http.StatusNotModified {
		t.Errorf("If-None-Match: %d", rec.Code)
	}
	head := h.do(t, http.MethodHead, videoPath, nil)
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Type") != "video/webm" {
		t.Errorf("HEAD: %d %v", head.Code, head.Header())
	}
	if fv.calls() != 0 {
		t.Errorf("%d transcodes, want none", fv.calls())
	}
	if got := rec.Header().Get(hdrXiCache); got == cacheHit {
		t.Error("a raw source came from the result cache")
	}
	if h.f.calls.Load() < 5 {
		t.Errorf("%d fetches: raw answers must not be served from the result cache", h.f.calls.Load())
	}
	h.checkReleased(t)
}

// TestVideoRawDiffersFromTranscode: the ETag of the raw source is not the
// transformation's.
func TestVideoRawDiffersFromTranscode(t *testing.T) {
	t.Parallel()
	raw, _ := newRawHarness(t, nil)
	tr, _ := newVideoHarness(t, false, func(h *harness) { h.f.data = webmSource })
	a := raw.do(t, http.MethodGet, videoPath, nil).Header().Get("ETag")
	b := tr.do(t, http.MethodGet, videoPath, nil).Header().Get("ETag")
	if a == "" || a == b {
		t.Errorf("raw ETag %q, transform ETag %q", a, b)
	}
}

// TestVideoRawChecks: raw keeps every check of a video request: signing
// and the unsigned policy, the role, the allowlist, the fetch's limits,
// and it serves only media.
func TestVideoRawChecks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(h *harness)
		status int
		body   string
	}{
		{"unsigned denied", func(h *harness) { h.router.sig = sign.Unsigned }, http.StatusForbidden, "unsigned request not allowed"},
		{"unsigned allowed", func(h *harness) {
			h.router.sig = sign.Unsigned
			h.cfg.Video.AllowUnsigned = true
		}, http.StatusOK, ""},
		{"image role", func(h *harness) { h.cfg.Role = RoleImage }, http.StatusNotFound, ""},
		{"video off", func(h *harness) { h.video = nil }, http.StatusNotFound, ""},
		{"source not allowed", func(h *harness) { h.res.deny = map[string]bool{"http://origin/a.jpg": true} }, http.StatusForbidden, ""},
		{"source too large", func(h *harness) { h.f.err = errs.New(errs.KindLimitExceeded, "fetch: too large") }, http.StatusUnprocessableEntity, ""},
		{"an image is not served", func(h *harness) { h.f.data = jpegSource }, http.StatusUnprocessableEntity, ""},
		{"svg is not served", func(h *harness) { h.f.data = []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>x</script></svg>`) }, http.StatusUnprocessableEntity, ""},
		{"a stream option is ignored", func(h *harness) {
			h.router.mutate = videoRequest(&transform.VideoOp{Raw: true, Stream: &transform.VideoStream{}})
		}, http.StatusOK, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h, fv := newRawHarness(t, tc.mutate)
			rec := h.do(t, http.MethodGet, videoPath, nil)
			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			if tc.body != "" && !strings.Contains(rec.Body.String(), tc.body) {
				t.Errorf("body %q, want %q", rec.Body.String(), tc.body)
			}
			if tc.status == http.StatusOK && !bytes.Equal(rec.Body.Bytes(), webmSource) {
				t.Error("the body is not the source")
			}
			if fv != nil && fv.calls() != 0 {
				t.Error("a raw request transcoded")
			}
			h.checkReleased(t)
		})
	}
}

func TestSniffMedia(t *testing.T) {
	t.Parallel()
	pad := func(s string) []byte { return append([]byte(s), make([]byte, 200)...) }
	ts := make([]byte, 400)
	ts[0], ts[188] = 0x47, 0x47
	tests := []struct {
		name    string
		in      []byte
		ct, ext string
	}{
		{"mp4", pad("\x00\x00\x00\x20ftypisom"), "video/mp4", "mp4"},
		{"m4a", pad("\x00\x00\x00\x20ftypM4A "), "audio/mp4", "m4a"},
		{"mov", pad("\x00\x00\x00\x14ftypqt  "), "video/quicktime", "mov"},
		{"webm", webmSource, "video/webm", "webm"},
		{"mkv", pad("\x1a\x45\xdf\xa3\x9f\x42\x86\x81\x01\x42\x82\x88matroska"), "video/x-matroska", "mkv"},
		{"ogg", pad("OggS\x00"), "audio/ogg", "ogg"},
		{"flac", pad("fLaC"), "audio/flac", "flac"},
		{"wav", pad("RIFF\x24\x00\x00\x00WAVEfmt "), "audio/wav", "wav"},
		{"avi", pad("RIFF\x24\x00\x00\x00AVI LIST"), "video/x-msvideo", "avi"},
		{"mp3 id3", pad("ID3\x04"), "audio/mpeg", "mp3"},
		{"mp3 frame", pad("\xff\xfb\x90\x64"), "audio/mpeg", "mp3"},
		{"adts", pad("\xff\xf1\x50\x80"), "audio/aac", "aac"},
		{"mpeg-ts", ts, "video/mp2t", "ts"},
		{"jpeg", jpegSource, "", ""},
		{"png", pad("\x89PNG\r\n\x1a\n"), "", ""},
		{"html", pad("<html>"), "", ""},
		{"empty", nil, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ct, ext, ok := sniffMedia(tc.in)
			if ct != tc.ct || ext != tc.ext || ok != (tc.ct != "") {
				t.Errorf("sniffMedia = %q %q %v, want %q %q", ct, ext, ok, tc.ct, tc.ext)
			}
		})
	}
}

func FuzzSniffMedia(f *testing.F) {
	f.Add(webmSource)
	f.Add([]byte("ID3"))
	f.Add([]byte("\xff\xfb"))
	f.Fuzz(func(t *testing.T, b []byte) {
		ct, ext, ok := sniffMedia(b)
		if ok != (ct != "") || ok != (ext != "") {
			t.Fatalf("inconsistent %q %q %v", ct, ext, ok)
		}
		if ok && !strings.HasPrefix(ct, "video/") && !strings.HasPrefix(ct, "audio/") {
			t.Fatalf("not a media type: %q", ct)
		}
	})
}
