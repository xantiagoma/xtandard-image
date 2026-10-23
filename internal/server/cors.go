package server

import (
	"net/http"
	"slices"
)

// CORS (XI_SERVER_CORS_ORIGINS): browsers may read responses, and HLS and
// DASH players in a page of another origin may load playlists and
// segments (WP-126), from the configured origins. Every response to a
// request whose Origin is allowed carries Access-Control-Allow-Origin
// ("*" when the list holds "*", else the origin, with Vary: Origin) and
// exposes the range and validator headers players read; a preflight
// (OPTIONS with Access-Control-Request-Method GET or HEAD, or QUERY on a
// query dialect endpoint while XI_SERVER_QUERY_METHOD is on) is answered
// 204. A QUERY response also exposes Location and Accept-Query. Without
// configured origins nothing changes.

// corsExposed are the response headers a cross-origin script may read.
const corsExposed = "Content-Length, Content-Range, Accept-Ranges, ETag, Content-Type"

// corsAllowed are the request headers a preflight admits; a QUERY
// preflight adds Content-Type.
const corsAllowed = "Range, If-None-Match, If-Modified-Since, If-Range, Accept"

// corsOrigin returns the Access-Control-Allow-Origin value for origin, ""
// when it is not allowed.
func (s *Server) corsOrigin(origin string) string {
	if origin == "" || len(s.cfg.CORSOrigins) == 0 {
		return ""
	}
	if slices.Contains(s.cfg.CORSOrigins, "*") {
		return "*"
	}
	if slices.Contains(s.cfg.CORSOrigins, origin) {
		return origin
	}
	return ""
}

// cors sets the CORS headers of w for r and reports whether r was a
// preflight it answered.
func (s *Server) cors(w *statusWriter, r *http.Request) (preflight bool) {
	allow := s.corsOrigin(r.Header.Get("Origin"))
	if allow == "" {
		return false
	}
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", allow)
	if allow != "*" {
		w.varyOrigin = true // added when the headers are written: handlers set Vary themselves
	}
	if m := r.Header.Get("Access-Control-Request-Method"); r.Method == http.MethodOptions && m != "" {
		switch {
		case m == http.MethodGet || m == http.MethodHead:
			h.Set("Access-Control-Allow-Methods", "GET, HEAD")
			h.Set("Access-Control-Allow-Headers", corsAllowed)
		case m == MethodQuery && s.queryEndpoint(r, true):
			h.Set("Access-Control-Allow-Methods", allowGetHeadQuery)
			h.Set("Access-Control-Allow-Headers", corsAllowed+", Content-Type")
		default:
			return false
		}
		h.Set("Access-Control-Max-Age", "86400")
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	if r.Method == MethodQuery && s.queryEndpoint(r, true) {
		h.Set("Access-Control-Expose-Headers", corsExposed+", Location, Accept-Query")
		return false
	}
	h.Set("Access-Control-Expose-Headers", corsExposed)
	return false
}
