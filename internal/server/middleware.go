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

// noDialect is the metrics label of image requests that never reached a
// dialect (no route, ingress failures).
const noDialect = "none"

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

// ingress is stage 1 (request ID, URL length, method) plus dispatch. Fixed
// routes go through the ServeMux. Image paths embed a source URL
// ("…/plain/http://host/…"), which the mux would "clean" and redirect, so
// paths that are not already clean go straight to the image handler; every
// fixed route is clean, so dispatch is unchanged for them.
func (s *Server) ingress(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := requestID(r)
		r = r.WithContext(logging.WithRequestID(r.Context(), id))
		info := &requestInfo{dialect: noDialect}
		sw := &statusWriter{ResponseWriter: w, info: info}
		sw.Header().Set("X-Request-ID", id)

		isImage := r.URL.Path != "/healthz" && r.URL.Path != "/readyz"
		if isImage {
			done := s.deps.Metrics.RequestStarted()
			defer done()
		}
		defer func() {
			// A panic unwinds through here before the recoverer answers
			// 500: record that status, then let the recoverer handle it.
			v := recover()
			if v != nil && sw.status == 0 {
				sw.status = http.StatusInternalServerError
			}
			s.accessLog(r, sw, isImage, time.Since(start))
			if v != nil {
				panic(v)
			}
		}()

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

// accessLog writes the one access line per request and the request
// metrics. request_id is added by the logging handler from ctx.
func (s *Server) accessLog(r *http.Request, sw *statusWriter, isImage bool, d time.Duration) {
	status := sw.statusCode()
	info := sw.info
	if isImage {
		s.deps.Metrics.ObserveRequest(info.dialect, status, info.format, d)
	}
	attrs := make([]slog.Attr, 0, 11)
	attrs = append(attrs,
		slog.String("method", r.Method),
		slog.String("path", stripQuery(r.URL.Path)),
		slog.Int("status", status),
		slog.Int64("bytes", sw.bytes),
		slog.Duration("duration", d),
	)
	if isImage {
		attrs = append(attrs, slog.String("dialect", info.dialect))
		if info.source != "" {
			attrs = append(attrs, slog.String("source", info.source))
		}
		if info.format != "" {
			attrs = append(attrs,
				slog.String("format", info.format),
				slog.Int("width", info.width),
				slog.Int("height", info.height))
		}
		if st := info.stageAttrs(); len(st) > 0 {
			attrs = append(attrs, slog.Attr{Key: "stages", Value: slog.GroupValue(st...)})
		}
	}
	s.deps.Logger.LogAttrs(r.Context(), slog.LevelInfo, "request served", attrs...)
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

// Stages of the image handler in execution order (ARCHITECTURE §2 numbers
// in comments). stageName is the {stage} label of
// xi_stage_duration_seconds and the key in the access log's "stages" group.
const (
	stageRoute     = iota // 2–4: route, parse, authenticate
	stageNormalize        // 6: client hints, defaults, validate
	stageResolve          // 5: source allowlist
	stageFetch            // 9
	stageAdmission        // 10
	stageProbe            // 11: SVG sanitise + header probe
	stageNegotiate        // 7, after the probe because it needs source meta
	stageProcess          // 12–13
	stageRespond          // 14
	numStages
)

// stageName is a switch rather than a package-level table so there is no
// mutable global.
func stageName(i int) string {
	switch i {
	case stageRoute:
		return "route"
	case stageNormalize:
		return "normalize"
	case stageResolve:
		return "resolve"
	case stageFetch:
		return "fetch"
	case stageAdmission:
		return "admission"
	case stageProbe:
		return "probe"
	case stageNegotiate:
		return "negotiate"
	case stageProcess:
		return "process"
	default:
		return "respond"
	}
}

// requestInfo is what the image handler learned, for the access log and
// the request metrics.
type requestInfo struct {
	dialect       string
	source        string // named source; never the key or URL
	format        string // output format label
	width, height int
	stages        [numStages]time.Duration
	ran           [numStages]bool
	// job is set once the source resolved; a later failure may be
	// answered with the fallback image (special.go).
	job *job
}

func (ri *requestInfo) stageAttrs() []slog.Attr {
	var out []slog.Attr
	for i, ok := range ri.ran {
		if ok {
			out = append(out, slog.Duration(stageName(i), ri.stages[i]))
		}
	}
	return out
}

// stageTimer measures one stage into the request info and the stage
// histogram.
type stageTimer struct {
	s     *Server
	info  *requestInfo
	i     int
	start time.Time
}

func (s *Server) stage(info *requestInfo, i int) stageTimer {
	return stageTimer{s: s, info: info, i: i, start: time.Now()}
}

func (t stageTimer) done() {
	d := time.Since(t.start)
	t.info.stages[t.i] += d
	t.info.ran[t.i] = true
	t.s.deps.Metrics.ObserveStage(stageName(t.i), d)
}

// statusWriter records the status and body size for the access log and
// metrics. Unwrap keeps http.ResponseController working.
type statusWriter struct {
	http.ResponseWriter
	info   *requestInfo
	status int
	bytes  int64
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

// infoOf returns the request info when w is the ingress writer, else a
// throwaway one (handlers invoked directly in tests).
func infoOf(w http.ResponseWriter) *requestInfo {
	if sw, ok := w.(*statusWriter); ok {
		return sw.info
	}
	return &requestInfo{dialect: noDialect}
}
