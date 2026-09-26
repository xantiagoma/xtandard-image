// Package admin builds the admin listener's ServeMux (ARCHITECTURE §13):
// /metrics, /healthz, /readyz, /config.schema.json, /openapi.json, /docs
// (only when enabled), the playground (only when enabled) and, only when
// enabled, pprof. app serves it on its
// own http.Server bound to XI_ADMIN_ADDR; it is never mounted on the public
// listener, and http.DefaultServeMux is never served.
package admin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/pprof"
	"sync/atomic"
	"time"
)

// State is the process readiness state.
type State int32

// Readiness states. The zero value is Starting.
const (
	Starting State = iota // not ready yet: vips or sources still initialising
	Ready                 // serving traffic
	Draining              // shutting down: load balancers must deregister us
)

// String returns the lowercase state name used in /readyz bodies.
func (s State) String() string {
	switch s {
	case Starting:
		return "starting"
	case Ready:
		return "ready"
	case Draining:
		return "draining"
	default:
		return "unknown"
	}
}

// Readiness is the /readyz state, safe for concurrent use. Transitions are
// Starting → Ready → Draining; Draining is terminal, so a late SetReady
// during shutdown cannot re-admit traffic.
type Readiness struct {
	state atomic.Int32
}

// NewReadiness returns a Readiness in the Starting state.
func NewReadiness() *Readiness { return &Readiness{} }

// SetReady marks the process ready unless it is already draining. It
// reports whether the state is Ready afterwards.
func (r *Readiness) SetReady() bool {
	return r.state.CompareAndSwap(int32(Starting), int32(Ready)) || r.State() == Ready
}

// SetDraining marks the process as draining. It is idempotent.
func (r *Readiness) SetDraining() { r.state.Store(int32(Draining)) }

// State returns the current state.
func (r *Readiness) State() State { return State(r.state.Load()) }

// IsReady reports whether the state is Ready.
func (r *Readiness) IsReady() bool { return r.State() == Ready }

// ServeHTTP answers /readyz: 200 "ready" when ready, else 503 with the state
// name. The public listener may mount it too.
func (r *Readiness) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	s := r.State()
	writeText(w, statusFor(s), s.String())
}

func statusFor(s State) int {
	if s == Ready {
		return http.StatusOK
	}
	return http.StatusServiceUnavailable
}

// Healthz answers liveness: always 200 while the process can serve HTTP. It
// makes no vips calls.
func Healthz(w http.ResponseWriter, _ *http.Request) { writeText(w, http.StatusOK, "ok") }

func writeText(w http.ResponseWriter, status int, body string) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body + "\n"))
}

// Options configures NewMux.
type Options struct {
	// Metrics serves /metrics, normally (*metrics.Metrics).Handler(). Nil
	// leaves /metrics unregistered (404).
	Metrics http.Handler
	// Readiness backs /readyz. Nil answers 503 "starting" forever.
	Readiness *Readiness
	// Pprof registers /debug/pprof/* (XI_ADMIN_PPROF). Off by default; when
	// off, every pprof path is a 404.
	Pprof bool
	// ConfigSchema is served at /config.schema.json (config.Schema(), ADR
	// 0007 decision 6). Nil leaves the route unregistered.
	ConfigSchema []byte
	// OpenAPI is the OpenAPI document served at /openapi.json with a strong
	// ETag (internal/apidoc.Spec, ADR 0008). Nil leaves /openapi.json and
	// /docs unregistered (404).
	OpenAPI []byte
	// Docs serves the Scalar API reference at /docs (XI_ADMIN_DOCS). Off by
	// default: /docs then answers 404 and points at /openapi.json.
	Docs bool
	// Playground serves every path under PlaygroundPath (XI_PLAYGROUND_*,
	// internal/playground). Nil leaves them unregistered (404).
	Playground     http.Handler
	PlaygroundPath string // "/_playground": starts with /, no trailing /
}

type muxRoute struct {
	pattern string
	h       http.Handler
}

// routes is the admin route table for opts: NewMux registers it and
// Patterns lists it, so the OpenAPI route-coverage test sees exactly what
// is served.
func routes(opts *Options) []muxRoute {
	ready := opts.Readiness
	if ready == nil {
		ready = NewReadiness()
	}
	rs := []muxRoute{
		{PatternHealthz, http.HandlerFunc(Healthz)},
		{PatternReadyz, ready},
	}
	if opts.Metrics != nil {
		rs = append(rs, muxRoute{PatternMetrics, opts.Metrics})
	}
	if opts.ConfigSchema != nil {
		rs = append(rs, muxRoute{PatternConfigSchema, schemaHandler(opts.ConfigSchema)})
	}
	if opts.OpenAPI != nil {
		rs = append(rs,
			muxRoute{PatternOpenAPI, newStatic(opts.OpenAPI, openAPIContentType)},
			muxRoute{PatternDocs, newDocs(opts.Docs)},
		)
	}
	if opts.Pprof {
		rs = append(rs, pprofRoutes()...)
	}
	if opts.Playground != nil && opts.PlaygroundPath != "" {
		// Every method: the playground has its own method patterns
		// (GET pages, POST api/sign) and answers 405 itself.
		rs = append(rs, muxRoute{opts.PlaygroundPath + "/", opts.Playground})
	}
	return rs
}

// Admin ServeMux patterns (see routes).
const (
	PatternHealthz = "GET /healthz"
	PatternReadyz  = "GET /readyz"
	PatternMetrics = "GET /metrics"
	PatternOpenAPI = "GET /openapi.json"
	PatternDocs    = "GET /docs"
	// PatternConfigSchema serves the config file JSON Schema (WP-81).
	PatternConfigSchema = "GET " + ConfigSchemaPath
)

// NewMux returns the admin ServeMux.
func NewMux(opts *Options) *http.ServeMux {
	mux := http.NewServeMux()
	for _, r := range routes(opts) {
		mux.Handle(r.pattern, r.h)
	}
	return mux
}

// ConfigSchemaPath is the admin route of the config file JSON Schema.
const ConfigSchemaPath = "/config.schema.json"

// schemaHandler serves a JSON Schema document with a strong ETag (its
// SHA-256), so editors revalidate with If-None-Match and get a 304.
func schemaHandler(schema []byte) http.Handler {
	sum := sha256.Sum256(schema)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "application/schema+json")
		h.Set("ETag", etag)
		h.Set("Cache-Control", "no-cache")
		h.Set("X-Content-Type-Options", "nosniff")
		// ServeContent answers If-None-Match (304) and HEAD from the ETag.
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(schema))
	})
}

// Patterns returns the patterns NewMux registers for opts.
func Patterns(opts *Options) []string {
	rs := routes(opts)
	ps := make([]string, len(rs))
	for i, r := range rs {
		ps[i] = r.pattern
	}
	return ps
}

// pprofRoutes are the pprof handlers, registered explicitly on the admin
// mux (never on http.DefaultServeMux). pprof.Index also serves every named
// profile under /debug/pprof/{name}, including goroutineleak (Go 1.27).
func pprofRoutes() []muxRoute {
	return []muxRoute{
		{"GET /debug/pprof/", http.HandlerFunc(pprof.Index)},
		{"GET /debug/pprof/cmdline", http.HandlerFunc(pprof.Cmdline)},
		{"GET /debug/pprof/profile", http.HandlerFunc(pprof.Profile)},
		{"GET /debug/pprof/symbol", http.HandlerFunc(pprof.Symbol)},
		{"POST /debug/pprof/symbol", http.HandlerFunc(pprof.Symbol)},
		{"GET /debug/pprof/trace", http.HandlerFunc(pprof.Trace)},
	}
}
