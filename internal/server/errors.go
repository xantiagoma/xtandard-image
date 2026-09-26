package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/limiter"
	"github.com/xantiagoma/xtandard-image/internal/obs/logging"
)

// errRequestTimeout is the cause of the per-request budget, so our own
// deadline is told apart from the client's (go-standards §2).
var errRequestTimeout = errors.New("server: request timeout")

// statusError is implemented by *errs.Error, *transform.ValidationError, the
// ingress httpError and notImplementedError.
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

// notImplementedError marks request kinds whose handling lands in a later
// work package: raw / skip-processing / passthrough streaming (WP-34c) or
// /info when no Inspector is wired (WP-35). It wraps KindUnsupported so logs and fallback
// eligibility see an unsupported request, but answers 501 so clients can
// tell "not built yet" from "bad image".
type notImplementedError struct {
	what string
}

func (e *notImplementedError) Error() string         { return "server: " + e.what + " is not implemented yet" }
func (e *notImplementedError) HTTPStatus() int       { return http.StatusNotImplemented }
func (e *notImplementedError) PublicMessage() string { return e.what + " is not implemented yet" }
func (e *notImplementedError) Unwrap() error         { return errs.New(errs.KindUnsupported, e.what) }

// notFoundCompat is the body of 400/403 answers turned into 404 by
// XI_COMPAT_IMGPROXY_STATUS.
const notFoundCompat = "not found"

// classify re-classifies err with what the contexts say (go-standards §6):
// a client that went away is 499 whatever failed; our own request budget
// expiring turns context-shaped failures (canceled, timeout, or a bare cause)
// into KindTimeout (504). Other kinds are kept, so an origin 404 that
// arrived just before the deadline is still a 404.
func classify(budget context.Context, r *http.Request, err error) error {
	if r.Context().Err() != nil {
		return errs.Wrap(errs.KindCanceled, err)
	}
	if budget == nil || budget.Err() == nil || !errors.Is(context.Cause(budget), errRequestTimeout) {
		return err
	}
	switch errs.KindOf(err) {
	case errs.KindCanceled, errs.KindTimeout, errs.KindInternal, errs.KindUnknown:
		return errs.Wrap(errs.KindTimeout, err)
	default:
		return err
	}
}

// writeError is the single error → HTTP status mapping point (go-standards
// §6, ARCHITECTURE §12). The status and body come from the first
// statusError in the chain (errors.AsType), else from errs.KindOf (context
// errors), else 500. Internal text is logged, never sent. A client
// disconnect (499) is logged and recorded but not written. Overload errors
// carry Retry-After. With XI_COMPAT_IMGPROXY_STATUS, 400 and KindForbidden
// answer 404 like imgproxy. Cache-Control follows the ARCHITECTURE §12
// column (errorCacheControl).
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	kind := errs.KindOf(err)
	status, msg := errs.Status(kind), errs.PublicMessage(kind)
	if se, ok := errors.AsType[statusError](err); ok {
		status, msg = se.HTTPStatus(), se.PublicMessage()
	}
	if s.cfg.CompatImgproxyStatus && (status == http.StatusBadRequest || (status == http.StatusForbidden && kind == errs.KindForbidden)) {
		status, msg = http.StatusNotFound, notFoundCompat
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
	body := []byte(msg + "\n")
	if wantsJSON(r) {
		h.Set("Content-Type", "application/json")
		body = errorJSON(kind, msg, logging.RequestID(r.Context()))
	} else {
		h.Set("Content-Type", "text/plain; charset=utf-8")
	}
	h.Set("Cache-Control", s.errorCacheControl(kind, status))
	h.Add("Vary", "Accept")
	h.Set("X-Content-Type-Options", "nosniff")
	if oe, ok := errors.AsType[*limiter.OverloadError](err); ok {
		h.Set("Retry-After", strconv.Itoa(int(max(oe.RetryAfter, time.Second)/time.Second)))
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body) //nolint:gosec // G705: public error text (errs/PublicMessage) as text/plain or JSON, with nosniff
	}
}

// wantsJSON reports an Accept header that lists application/json
// (ARCHITECTURE §12: JSON error bodies on request).
func wantsJSON(r *http.Request) bool {
	for _, v := range r.Header.Values("Accept") {
		for part := range strings.SplitSeq(v, ",") {
			mt, _, _ := strings.Cut(part, ";")
			if strings.EqualFold(strings.TrimSpace(mt), "application/json") {
				return true
			}
		}
	}
	return false
}

// errorJSON renders {"error":{"kind":…,"message":…,"request_id":…}}.
func errorJSON(kind errs.Kind, msg, requestID string) []byte {
	type body struct {
		Kind      string `json:"kind"`
		Message   string `json:"message"`
		RequestID string `json:"request_id,omitempty"`
	}
	b, err := json.Marshal(struct {
		Error body `json:"error"`
	}{body{Kind: kind.String(), Message: msg, RequestID: requestID}})
	if err != nil { // unreachable: only strings
		return []byte(`{"error":{"kind":"internal","message":"internal server error"}}`)
	}
	return append(b, '\n')
}
