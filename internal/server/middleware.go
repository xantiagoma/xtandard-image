package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"path"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/dialect/query"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/obs/logging"
	"github.com/xantiagoma/xtandard-image/internal/source"
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

// ingress is stage 1 (request ID, URL length, method) plus dispatch. A
// QUERY request becomes the equivalent GET request first (query.go). Fixed
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
		// The playground's own renders (api/render) get the stage
		// durations in Server-Timing; nobody else does.
		sw := &statusWriter{ResponseWriter: w, info: info, timing: source.IsPlaygroundRequest(r.Context())}
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

		if s.cors(sw, r) {
			return // a preflight, answered
		}
		dispatch := func(r *http.Request) {
			if r.URL.Path != "/" && path.Clean(r.URL.Path) != r.URL.Path {
				s.image(sw, r)
				return
			}
			mux.ServeHTTP(sw, r)
		}
		switch {
		case isImage && !s.authorized(r):
			s.writeError(sw, r, &httpError{status: http.StatusForbidden, msg: "invalid secret"})
		case len(r.RequestURI) > maxURLLength:
			s.writeError(sw, r, &httpError{status: http.StatusRequestURITooLong, msg: "url too long"})
		case r.Method == MethodQuery && s.cfg.Query.Enabled:
			s.serveQuery(sw, r, dispatch) // query.go
		case r.Method != http.MethodGet && r.Method != http.MethodHead:
			allow := allowGetHead
			if s.queryEndpoint(r, true) {
				allow = allowGetHeadQuery
				sw.Header().Set("Accept-Query", query.AcceptQuery)
			}
			sw.Header().Set("Allow", allow)
			s.writeError(sw, r, &httpError{status: http.StatusMethodNotAllowed, msg: "method not allowed"})
		default:
			if s.queryEndpoint(r, r.URL.Query().Has("url")) {
				sw.Header().Set("Accept-Query", query.AcceptQuery)
			}
			dispatch(r)
		}
	})
}

// authorized reports whether r carries the XI_SECURITY_SECRET bearer
// token (always true when none is set), compared in constant time. The
// playground's in-process renders (api/render) are already behind the
// listener that serves the playground and carry none.
func (s *Server) authorized(r *http.Request) bool {
	if s.authorization == nil || source.IsPlaygroundRequest(r.Context()) {
		return true
	}
	return subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), s.authorization) == 1
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
		slog.String("path", redactPasswords(stripQuery(r.URL.Path))),
		slog.Int("status", status),
		slog.Int64("bytes", sw.bytes),
		slog.Duration("duration", d),
	)
	if isImage {
		attrs = append(attrs, slog.String("dialect", info.dialect))
		if info.source != "" {
			attrs = append(attrs, slog.String("source", info.source))
		}
		if info.cache != "" {
			attrs = append(attrs, slog.String("cache", info.cache))
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

// redactedToken replaces the argument of a pdf_password path segment in
// access logs.
const redactedToken = "REDACTED"

// redactPasswords replaces the encrypted token of every pdf_password (pdp)
// path segment with "REDACTED" (WP-115): the token is ciphertext, but a
// document password has no business in logs in any form. A segment counts
// when it starts with the option name followed by any argument separator
// (not a letter or digit, so pdpx is left alone), up to a plain/ or enc/
// source. The query dialect's
// pdf_password= travels in the query string, which is never logged.
func redactPasswords(p string) string {
	if !strings.Contains(p, "pdp") && !strings.Contains(p, "pdf_password") {
		return p
	}
	segs := strings.Split(p, "/")
	for i, seg := range segs {
		if seg == "plain" || seg == "enc" {
			break // the source follows
		}
		for _, name := range [...]string{"pdf_password", "pdp"} {
			rest, ok := strings.CutPrefix(seg, name)
			if !ok || rest == "" || isAlnum(rest[0]) {
				continue
			}
			segs[i] = name + rest[:1] + redactedToken
			break
		}
	}
	return strings.Join(segs, "/")
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
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
	stageCache            // 8: result cache lookup (before the fetch, cache.go)
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
	case stageCache:
		return "cache"
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
	cache         string // result cache status: HIT, MISS, BYPASS; "" when off

	stages [numStages]time.Duration
	ran    [numStages]bool
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
	timing bool // add Server-Timing with the stage durations
	// varyOrigin adds Vary: Origin when the headers are written (a CORS
	// response for one origin, cors.go).
	varyOrigin bool
	// varyContentType adds Vary: Content-Type, and location the Location
	// of a 2xx, to a QUERY response (query.go).
	varyContentType bool
	location        string
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
		w.beforeHeaders()
	}
	w.ResponseWriter.WriteHeader(code)
}

// beforeHeaders adds what goes with every response once the handler has
// set its own headers.
func (w *statusWriter) beforeHeaders() {
	if w.timing {
		w.serverTiming()
	}
	if w.varyOrigin {
		w.Header().Add("Vary", "Origin")
	}
	if w.varyContentType {
		w.Header().Add("Vary", "Content-Type")
	}
	if w.location != "" && w.status >= 200 && w.status < 300 {
		w.Header().Set("Location", w.location)
	}
}

// serverTiming adds one Server-Timing entry per stage that ran
// ("fetch;dur=12.3").
func (w *statusWriter) serverTiming() {
	h := w.Header()
	for i, ok := range w.info.ran {
		if ok {
			ms := float64(w.info.stages[i].Microseconds()) / 1000
			h.Add("Server-Timing", stageName(i)+";dur="+strconv.FormatFloat(ms, 'f', 3, 64))
		}
	}
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
		w.beforeHeaders()
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
