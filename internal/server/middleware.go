package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"path"
	"runtime/debug"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/obs/logging"
)

// maxURLLength is the ingress limit on the request target (ARCHITECTURE §2).
const maxURLLength = 8 << 10

// dialectLabel is the metrics label of the WP-00 parser.
const dialectLabel = "imgproxy"

// recoverer is the outermost layer: a Go panic becomes a counted, logged
// 500. http.ErrAbortHandler is re-panicked so net/http aborts the response.
// It cannot catch a SIGSEGV in C.
func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(v)
			}
			s.deps.Metrics.IncPanic()
			s.deps.Logger.LogAttrs(r.Context(), slog.LevelError, "handler panic",
				slog.Any("panic", v), slog.String("stack", string(debug.Stack())))
			s.writeError(w, r, errs.New(errs.KindInternal, "handler panic"))
		}()
		next.ServeHTTP(w, r)
	})
}

// route is ingress (request ID, URL length, method) plus dispatch. Fixed
// routes go through the ServeMux. Image paths embed a source URL
// ("…/plain/http://host/…"), which the mux would "clean" and redirect, so
// paths that are not already clean go straight to the image handler; every
// fixed route is clean, so dispatch is unchanged for them.
func (s *Server) route(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := requestID(r)
		r = r.WithContext(logging.WithRequestID(r.Context(), id))
		sw := &statusWriter{ResponseWriter: w}
		sw.Header().Set("X-Request-ID", id)

		isImage := r.URL.Path != "/healthz" && r.URL.Path != "/readyz"
		if isImage {
			done := s.deps.Metrics.RequestStarted()
			defer done()
		}
		defer func() { s.accessLog(r, sw, isImage, time.Since(start)) }()

		switch {
		case len(r.RequestURI) > maxURLLength:
			s.writeError(sw, r, &httpError{status: http.StatusRequestURITooLong, msg: "url too long"})
		case r.Method != http.MethodGet && r.Method != http.MethodHead:
			sw.Header().Set("Allow", "GET, HEAD")
			s.writeError(sw, r, &httpError{status: http.StatusMethodNotAllowed, msg: "method not allowed"})
		case r.URL.Path != "/" && path.Clean(r.URL.Path) != r.URL.Path:
			s.image(sw, r)
		default:
			mux.ServeHTTP(sw, r)
		}
	})
}

func (s *Server) accessLog(r *http.Request, sw *statusWriter, isImage bool, d time.Duration) {
	status := sw.statusCode()
	if isImage {
		s.deps.Metrics.ObserveRequest(dialectLabel, status, sw.format, d)
	}
	s.deps.Logger.LogAttrs(r.Context(), slog.LevelInfo, "request served",
		slog.String("method", r.Method),
		slog.String("path", stripQuery(r.URL.Path)),
		slog.Int("status", status),
		slog.Int64("bytes", sw.bytes),
		slog.Duration("duration", d))
}

// stripQuery drops anything after '?' so decoded source URLs never log their
// query strings (tokens, signatures).
func stripQuery(p string) string {
	p, _, _ = strings.Cut(p, "?")
	return p
}

// requestID reuses a well-formed incoming X-Request-Id, else makes one.
func requestID(r *http.Request) string {
	if id := r.Header.Get("X-Request-ID"); validRequestID(id) {
		return id
	}
	var b [8]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails (Go 1.24+)
	return hex.EncodeToString(b[:])
}

func validRequestID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for i := range len(id) {
		c := id[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.'
		if !ok {
			return false
		}
	}
	return true
}

// statusWriter records the status, body size and output format for the
// access log and metrics. Unwrap keeps http.ResponseController working.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
	format string
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}
