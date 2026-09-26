package server

import (
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/obs/logging"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// redirectOnError answers an eligible failure (the fallbackEligible kinds,
// stages 9–12) of a request with Delivery.OnErrorRedirect with a 302:
//
//   - to Delivery.Fallback when set (wsrv default=<url>), provided it is an
//     absolute http(s) URL the source allowlist accepts, so the endpoint is
//     never an open redirect;
//   - otherwise to the request's own source when it is an http(s) URL
//     (wsrv default=1, cloudflare onerror=redirect); it already passed
//     the allowlist.
//
// Like wsrv, the body is the JSON error. The Cache-Control is that of the
// error (XI_CACHE_HEADERS_ERROR_TTL). It reports whether it wrote the
// response; when no target qualifies, the fallback image and error paths
// run as usual.
func (s *Server) redirectOnError(w http.ResponseWriter, r *http.Request, info *requestInfo, cause error) bool {
	j := info.job
	if j == nil || !j.req.Delivery.OnErrorRedirect || !fallbackEligible(errs.KindOf(cause)) {
		return false
	}
	target := redirectTarget(j)
	if spec := j.req.Delivery.Fallback; spec != nil {
		target = ""
		if httpURL(spec.Raw) {
			if _, err := s.deps.Resolver.Resolve(*spec); err == nil {
				target = spec.Raw
			} else {
				s.deps.Logger.LogAttrs(r.Context(), slog.LevelWarn, "error redirect target not allowed", logging.Err(err))
			}
		}
	}
	if target == "" {
		return false
	}
	kind := errs.KindOf(cause)
	s.deps.Logger.LogAttrs(r.Context(), slog.LevelWarn, "redirecting failed request",
		slog.String("kind", kind.String()), logging.Err(cause))
	h := w.Header()
	h.Set("Location", target)
	h.Set("Cache-Control", s.errorCacheControl(kind, http.StatusFound))
	h.Set("Content-Type", "application/json")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusFound)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, redirectBody)
	}
	return true
}

// redirectBody is the static JSON body of an error redirect.
const redirectBody = `{"status":"error","code":302,"message":"source image failed; redirecting"}` + "\n"

// redirectTarget is the request's own source when it is an http(s) URL.
func redirectTarget(j *job) string {
	if p := j.key.Path; httpURL(p) {
		return p
	}
	if raw := j.req.Source.Raw; httpURL(raw) && j.req.Source.Encoding != transform.SourceRelative {
		return raw
	}
	return ""
}

func httpURL(s string) bool {
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}
