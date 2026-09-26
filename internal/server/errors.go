package server

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/limiter"
	"github.com/xantiagoma/xtandard-image/internal/obs/logging"
)

// statusError is implemented by *errs.Error and by the ingress httpError.
type statusError interface {
	error
	HTTPStatus() int
	PublicMessage() string
}

// httpError is an ingress failure that has a status but no errs kind
// (414, 405).
type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string         { return e.msg }
func (e *httpError) HTTPStatus() int       { return e.status }
func (e *httpError) PublicMessage() string { return e.msg }

// writeError is the single error → HTTP status mapping point (go-standards
// §6, ARCHITECTURE §12). The status and body come from the statusError in
// the chain, else from errs.KindOf (context errors), else 500. Internal text
// is logged, never sent. A client disconnect (499) is logged and recorded
// but not written.
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	kind := errs.KindOf(err)
	status, msg := errs.Status(kind), errs.PublicMessage(kind)
	if se, ok := errors.AsType[statusError](err); ok {
		status, msg = se.HTTPStatus(), se.PublicMessage()
	}

	level := slog.LevelWarn
	if status >= http.StatusInternalServerError {
		level = slog.LevelError
	}
	s.deps.Logger.LogAttrs(r.Context(), level, "request failed",
		slog.Int("status", status),
		slog.String("kind", kind.String()),
		logging.Err(err))

	if status == errs.StatusClientClosedRequest {
		if sw, ok := w.(*statusWriter); ok {
			sw.status = status
		}
		return
	}

	h := w.Header()
	h.Del("Content-Length")
	h.Del("ETag")
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	if oe, ok := errors.AsType[*limiter.OverloadError](err); ok {
		h.Set("Retry-After", strconv.Itoa(int(oe.RetryAfter/time.Second)))
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(msg + "\n")) //nolint:gosec // G705: static public message, text/plain with nosniff
	}
}
