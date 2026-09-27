package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/obs/logging"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// errorRedirect is the single error-redirect path (Delivery.OnErrorRedirect,
// set by cloudflare onerror=redirect and wsrv default=). It answers an
// eligible failure (the fallbackEligible kinds, stages 9–12, so never a
// source the allowlist refused) with a 302:
//
//   - to Delivery.Fallback when set (wsrv default=<url>), provided it is an
//     absolute http(s) URL the source allowlist accepts, so the endpoint is
//     never an open redirect; a fallback URL that does not qualify means no
//     redirect;
//   - otherwise to the request's own source when it is an http(s) URL
//     (cloudflare onerror=redirect, wsrv default=1); it already passed the
//     allowlist.
//
// The redirect is decided before, and wins over, the fallback image. Like
// wsrv, the body is a JSON error, and the Cache-Control is that of the error
// (XI_CACHE_HEADERS_ERROR_TTL; no-store when it is 0). It reports whether it
// wrote the response; when no target qualifies (a non-http source, say), the
// fallback image and error paths run as usual.
func (s *Server) errorRedirect(w http.ResponseWriter, r *http.Request, info *requestInfo, cause error) bool {
	j := info.job
	kind := errs.KindOf(cause)
	if j == nil || !j.req.Delivery.OnErrorRedirect || !fallbackEligible(kind) {
		return false
	}
	target := redirectTarget(j)
	if spec := j.req.Delivery.Fallback; spec != nil {
		target = ""
		if u, ok := httpURL(spec.Raw); ok {
			if _, err := s.resolve(r.Context(), *spec); err == nil {
				target = u
			} else {
				s.deps.Logger.LogAttrs(r.Context(), slog.LevelWarn, "error redirect target not allowed", logging.Err(err))
			}
		}
	}
	if target == "" {
		return false
	}
	info.job = nil // the fallback image never follows a redirect
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

// redirectTarget is the request's own source when it is an http(s) URL: the
// resolved key, else the raw source unless it is relative to a mount.
func redirectTarget(j *job) string {
	if u, ok := httpURL(j.key.Path); ok {
		return u
	}
	if j.req.Source.Encoding != transform.SourceRelative {
		if u, ok := httpURL(j.req.Source.Raw); ok {
			return u
		}
	}
	return ""
}

// httpURL reports whether s is an absolute http(s) URL with a host a client
// can follow, and returns it re-serialised.
func httpURL(s string) (string, bool) {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}
	return u.String(), true
}
