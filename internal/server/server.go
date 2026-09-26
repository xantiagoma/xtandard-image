// Package server is the public HTTP surface: an http.Server with every
// timeout set, a stdlib ServeMux for the fixed routes, the image handler and
// the single error → status mapping point (writeError).
//
// server never constructs concrete components. It sees fetching, admission
// and processing through the small interfaces below, which app satisfies with
// the real implementations and tests satisfy with fakes (no cgo needed).
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/obs/admin"
)

// Body is bytes with an explicit owner: source bytes in C memory, or an
// encoded vips blob. Bytes is valid until Release.
type Body interface {
	Bytes() []byte
	Release()
}

// Fetcher downloads a source URL. Errors carry an errs kind (NotFound,
// Forbidden, UpstreamError, UpstreamTimeout, SourceTooLarge, ...).
type Fetcher interface {
	Fetch(ctx context.Context, rawURL string) (Body, error)
}

// Admitter bounds concurrent processing (internal/limiter). release must be
// called exactly once after a successful Acquire.
type Admitter interface {
	Acquire(ctx context.Context, cost int64) (release func(), err error)
}

// Processor turns source bytes into an encoded image fitting width×height
// (0 = unbounded). It must not retain src after returning.
type Processor interface {
	Thumbnail(ctx context.Context, src Body, width, height int) (*Result, error)
}

// Result is an encoded image. The handler writes Body and releases it.
type Result struct {
	Body          Body
	ContentType   string
	Format        string // metric label: jpeg, png, ...
	Width, Height int
}

// Metrics receives request-level observations; *metrics.Metrics satisfies it.
type Metrics interface {
	ObserveRequest(dialect string, status int, format string, d time.Duration)
	RequestStarted() (done func())
	IncPanic()
}

// Config holds the HTTP settings (XI_SERVER_*) and the WP-00 source policy.
type Config struct {
	Addr              string
	RequestTimeout    time.Duration // whole-request budget
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
	// DevMode allows any http(s) source (SSRF rules still apply). Without
	// it every source is refused with 403 until source allowlists land.
	DevMode bool
}

// DefaultConfig returns the documented defaults (docs/CONFIG.md).
func DefaultConfig() Config {
	return Config{
		Addr:              ":8080",
		RequestTimeout:    20 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      25 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
}

// Validate reports every invalid field.
func (c Config) Validate() error {
	var list []error
	for _, f := range []struct {
		name string
		d    time.Duration
	}{
		{"request timeout", c.RequestTimeout},
		{"read header timeout", c.ReadHeaderTimeout},
		{"read timeout", c.ReadTimeout},
		{"write timeout", c.WriteTimeout},
		{"idle timeout", c.IdleTimeout},
	} {
		if f.d <= 0 {
			list = append(list, fmt.Errorf("server: %s must be > 0, got %s", f.name, f.d))
		}
	}
	if c.MaxHeaderBytes <= 0 {
		list = append(list, fmt.Errorf("server: max header bytes must be > 0, got %d", c.MaxHeaderBytes))
	}
	return errors.Join(list...)
}

// Deps are the components the server delegates to. Fetcher, Admitter,
// Processor and Logger are required.
type Deps struct {
	Fetcher   Fetcher
	Admitter  Admitter
	Processor Processor
	Logger    *slog.Logger
	Metrics   Metrics          // nil: no metrics
	Readiness *admin.Readiness // nil: /readyz is not mounted
}

// Server is the public listener's handler tree.
type Server struct {
	cfg     Config
	deps    Deps
	handler http.Handler
}

// New validates cfg and builds the handler tree.
func New(cfg Config, deps *Deps) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if deps == nil || deps.Fetcher == nil || deps.Admitter == nil || deps.Processor == nil || deps.Logger == nil {
		return nil, errors.New("server: fetcher, admitter, processor and logger are required")
	}
	s := &Server{cfg: cfg, deps: *deps}
	if s.deps.Metrics == nil {
		s.deps.Metrics = nopMetrics{}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", admin.Healthz)
	if s.deps.Readiness != nil {
		mux.Handle("GET /readyz", s.deps.Readiness)
	}
	mux.HandleFunc("GET /{path...}", s.image)
	s.handler = s.recoverer(s.route(mux))
	return s, nil
}

// Handler returns the root handler (recover → ingress → routes).
func (s *Server) Handler() http.Handler { return s.handler }

// HTTPServer returns an http.Server for the handler with every timeout set
// (go-standards §2). Request contexts derive from ctx.
func (s *Server) HTTPServer(ctx context.Context) *http.Server {
	return &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           s.handler,
		ReadHeaderTimeout: s.cfg.ReadHeaderTimeout,
		ReadTimeout:       s.cfg.ReadTimeout,
		WriteTimeout:      s.cfg.WriteTimeout,
		IdleTimeout:       s.cfg.IdleTimeout,
		MaxHeaderBytes:    s.cfg.MaxHeaderBytes,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ErrorLog:          slog.NewLogLogger(s.deps.Logger.Handler(), slog.LevelWarn),
	}
}

type nopMetrics struct{}

func (nopMetrics) ObserveRequest(string, int, string, time.Duration) {}
func (nopMetrics) RequestStarted() func()                            { return func() {} }
func (nopMetrics) IncPanic()                                         {}
