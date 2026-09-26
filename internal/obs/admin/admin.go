// Package admin builds the admin listener's ServeMux (ARCHITECTURE §13):
// /metrics, /healthz, /readyz and, only when enabled, pprof. app serves it on
// its own http.Server bound to XI_ADMIN_ADDR; it is never mounted on the
// public listener, and http.DefaultServeMux is never served.
package admin

import (
	"net/http"
	"net/http/pprof"
	"sync/atomic"
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
}

// NewMux returns the admin ServeMux.
func NewMux(opts Options) *http.ServeMux {
	mux := http.NewServeMux()
	ready := opts.Readiness
	if ready == nil {
		ready = NewReadiness()
	}
	mux.HandleFunc("GET /healthz", Healthz)
	mux.Handle("GET /readyz", ready)
	if opts.Metrics != nil {
		mux.Handle("GET /metrics", opts.Metrics)
	}
	if opts.Pprof {
		registerPprof(mux)
	}
	return mux
}

// registerPprof registers the pprof handlers explicitly on mux (never on
// http.DefaultServeMux). pprof.Index also serves every named profile under
// /debug/pprof/{name}, including goroutineleak (Go 1.27).
func registerPprof(mux *http.ServeMux) {
	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("POST /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
}
