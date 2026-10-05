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
// (OPTIONS with Access-Control-Request-Method GET or HEAD) is answered
// 204. Without configured origins nothing changes.

// corsExposed are the response headers a cross-origin script may read.
const corsExposed = "Content-Length, Content-Range, Accept-Ranges, ETag, Content-Type"

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
		if m != http.MethodGet && m != http.MethodHead {
			return false
		}
		h.Set("Access-Control-Allow-Methods", "GET, HEAD")
		h.Set("Access-Control-Allow-Headers", "Range, If-None-Match, If-Modified-Since, If-Range, Accept")
		h.Set("Access-Control-Max-Age", "86400")
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	h.Set("Access-Control-Expose-Headers", corsExposed)
	return false
}
