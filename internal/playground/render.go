package playground

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// maxRenderURL bounds the u parameter of api/render.
const maxRenderURL = 16 << 10

// forwarded reports the request headers api/render passes to the public
// handler: content negotiation and client hints. Cookies, credentials and
// everything else stay behind.
func forwarded(name string) bool {
	switch strings.ToLower(name) {
	case "accept", "save-data", "dpr", "width", "viewport-width", "ect", "downlink", "rtt", "user-agent": //nolint:misspell // ECT is the Effective Connection Type client hint
		return true
	}
	return strings.HasPrefix(name, "Sec-Ch-")
}

// render answers GET api/render?u=<path and query>: it runs the public
// handler in-process on that URL, so the page reads the result and all its
// headers from its own origin (no CORS, no CSP exception), and the timing
// of the public handler is reported in Server-Timing. The request passes
// every public-listener check; render adds no privilege.
func (h *handler) render(w http.ResponseWriter, r *http.Request) {
	u := r.URL.Query().Get("u")
	if len(u) > maxRenderURL || !strings.HasPrefix(u, "/") || strings.HasPrefix(u, "//") {
		writeJSONError(w, http.StatusBadRequest, "u must be a path on the public listener, starting with a single /")
		return
	}
	if err := checkPart("u", u); err != nil {
		writeJSONError(w, http.StatusBadRequest, "u must be escaped (no spaces, control characters or #)")
		return
	}
	target, err := url.ParseRequestURI(u)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid u")
		return
	}
	// A server-side request for the public handler, never a client request:
	// u is a path on this process, not a URL to fetch.
	req := (&http.Request{
		Method: http.MethodGet, URL: target, RequestURI: u, Header: http.Header{}, Body: http.NoBody,
		Host: r.Host, RemoteAddr: r.RemoteAddr, Proto: r.Proto, ProtoMajor: r.ProtoMajor, ProtoMinor: r.ProtoMinor,
	}).WithContext(r.Context())
	for name, vs := range r.Header {
		if forwarded(name) {
			req.Header[name] = vs
		}
	}
	w.Header().Set("Content-Security-Policy", renderCSP)
	h.image.ServeHTTP(&timingWriter{ResponseWriter: w, start: time.Now()}, req)
}

// timingWriter adds Server-Timing with the time to the first header write.
type timingWriter struct {
	http.ResponseWriter
	start time.Time
	wrote bool
}

func (t *timingWriter) WriteHeader(status int) {
	if !t.wrote {
		t.wrote = true
		ms := float64(time.Since(t.start).Microseconds()) / 1000
		t.Header().Set("Server-Timing", "xi;desc=\"public handler\";dur="+strconv.FormatFloat(ms, 'f', 1, 64))
	}
	t.ResponseWriter.WriteHeader(status)
}

func (t *timingWriter) Write(b []byte) (int, error) {
	if !t.wrote {
		t.WriteHeader(http.StatusOK)
	}
	return t.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (t *timingWriter) Unwrap() http.ResponseWriter { return t.ResponseWriter }
