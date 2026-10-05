package playground

import (
	"bytes"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// HLS and DASH in the playground (WP-126). A stream's manifest names its
// playlists and segments under the public listener's StreamPath
// (server.StreamPath, "/_stream/<ticket>/…"), which the playground's
// origin may not serve (the admin listener). api/render and api/stream
// therefore rewrite the manifests they answer so that their parts are
// fetched through api/stream/<ticket>/…, which runs the public handler
// in-process like api/render. Media playlists name their segments
// relative to themselves and need no rewrite.

// streamPath is server.StreamPath (app tests that they agree).
const streamPath = "/_stream/"

// StreamPath returns the path under which the public listener serves the
// parts of streams; app compares it with the server's.
func StreamPath() string { return streamPath }

// maxManifest bounds a manifest the playground rewrites.
const maxManifest = 4 << 20

// manifestTypes are the content types of HLS playlists and DASH MPDs.
var manifestTypes = map[string]bool{
	"application/vnd.apple.mpegurl": true, "application/x-mpegurl": true, "audio/mpegurl": true,
	"application/dash+xml": true,
}

// stream answers GET api/stream/{part...}: a part of a stream (playlist,
// init or media segment) through the public handler, with the range and
// validator headers a player sends.
func (h *handler) stream(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.EscapedPath(), h.base+"/api/stream/")
	u := h.pathPrefix + streamPath + rest
	target, err := url.ParseRequestURI(u)
	if err != nil || rest == "" || strings.Contains(rest, "..") {
		writeJSONError(w, http.StatusBadRequest, "invalid stream part")
		return
	}
	req := (&http.Request{
		Method: r.Method, URL: target, RequestURI: u, Header: http.Header{}, Body: http.NoBody,
		Host: r.Host, RemoteAddr: r.RemoteAddr, Proto: r.Proto, ProtoMajor: r.ProtoMajor, ProtoMinor: r.ProtoMinor,
	}).WithContext(withRender(r.Context()))
	for _, name := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
		if v := r.Header.Values(name); len(v) > 0 && !manifestPath(rest) {
			req.Header[name] = v
		}
	}
	mw := h.rewriteManifests(w)
	h.image.ServeHTTP(mw, req)
	mw.finish()
}

// manifestPath reports a part that is a playlist or an MPD (never asked
// for by range: the playground rewrites them whole).
func manifestPath(p string) bool {
	return strings.HasSuffix(p, ".m3u8") || strings.HasSuffix(p, ".mpd")
}

// rewriteManifests wraps w so that a manifest written through it names its
// parts under api/stream; anything else passes through untouched.
func (h *handler) rewriteManifests(w http.ResponseWriter) *manifestWriter {
	return &manifestWriter{ResponseWriter: w, from: []byte(h.pathPrefix + streamPath), to: []byte(h.base + "/api/stream/")}
}

// manifestWriter buffers a manifest's body to rewrite it.
type manifestWriter struct {
	http.ResponseWriter
	from, to []byte
	status   int
	buf      *bytes.Buffer // set while a manifest is buffered
	passed   bool          // the response is not a manifest
}

func (m *manifestWriter) WriteHeader(status int) {
	if m.status != 0 {
		return
	}
	m.status = status
	ct, _, _ := mime.ParseMediaType(m.Header().Get("Content-Type"))
	if status == http.StatusOK && manifestTypes[ct] {
		m.buf = &bytes.Buffer{}
		return
	}
	m.passed = true
	m.ResponseWriter.WriteHeader(status)
}

func (m *manifestWriter) Write(b []byte) (int, error) {
	if m.status == 0 {
		m.WriteHeader(http.StatusOK)
	}
	if m.buf == nil {
		return m.ResponseWriter.Write(b)
	}
	if m.buf.Len()+len(b) > maxManifest {
		return 0, http.ErrContentLength
	}
	return m.buf.Write(b)
}

// finish writes a buffered manifest: rewritten, with its new length (the
// old validators describe the original bytes). The handler calls it once
// the public handler has returned.
func (m *manifestWriter) finish() {
	if m.buf == nil {
		return
	}
	body := bytes.ReplaceAll(m.buf.Bytes(), m.from, m.to)
	m.buf = nil
	hd := m.Header()
	hd.Set("Content-Length", strconv.Itoa(len(body)))
	hd.Del("ETag")
	hd.Del("Content-Range")
	m.ResponseWriter.WriteHeader(m.status)
	_, _ = m.ResponseWriter.Write(body)
}

// Unwrap keeps http.ResponseController working.
func (m *manifestWriter) Unwrap() http.ResponseWriter { return m.ResponseWriter }
