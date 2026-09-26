// Package server is the public HTTP surface: an http.Server with every
// timeout set, a stdlib ServeMux for the fixed routes, the image handler that
// runs the request lifecycle of ARCHITECTURE §2, and the single error →
// status mapping point (writeError).
//
// server never constructs concrete components. It sees routing, source
// resolution, fetching, admission, probing and processing through the small
// consumer-side interfaces below; app satisfies them with the real
// implementations and tests with fakes (no cgo needed).
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/router"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/obs/admin"
	"github.com/xantiagoma/xtandard-image/internal/security/svg"
	"github.com/xantiagoma/xtandard-image/internal/source"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Router selects the dialect mount for a request, parses and authenticates
// the URL and applies the unsigned policy (stages 2–4); *router.Router
// implements it.
type Router interface {
	Route(r *http.Request) (*dialect.Result, *router.Mount, error)
}

// Resolver maps a source spec to a source key; it is the allowlist
// (stage 5). *source.Registry implements it.
type Resolver interface {
	Resolve(spec transform.SourceSpec) (source.Key, error)
}

// Fetcher downloads originals, coalescing identical in-flight fetches
// (stage 9); *fetch.Fetcher implements it. The caller releases the result.
type Fetcher interface {
	Fetch(ctx context.Context, key source.Key, opt source.FetchOptions) (*fetch.Result, error)
}

// Admitter bounds concurrent processing (stage 10); *limiter.Limiter
// implements it. release must be called exactly once after a successful
// Acquire.
type Admitter interface {
	Acquire(ctx context.Context, cost int64) (release func(), err error)
}

// SourceMeta is what the probe (stage 11) learned from the source header
// without decoding pixels. It is the server's view of codec.ImageMeta (WP-07);
// app adapts one to the other.
type SourceMeta struct {
	// Format is the sniffed format; transform.FormatUnset for formats that
	// have no transform.Format (PDF, PSD, RAW, JPEG 2000, …).
	Format transform.Format
	// Width and Height are one frame/page in pixels.
	Width, Height int
	// Frames is the number of frames or pages (1 for still images).
	Frames      int
	HasAlpha    bool
	IsAnimated  bool
	Orientation int // EXIF orientation 1..8 (1 when absent)
}

// ProbeLimits are the effective input limits of one request (config values,
// lowered — or raised, with XI_SECURITY_ALLOW_RAISING_LIMITS — by the
// request's security options). Zero means unlimited.
type ProbeLimits struct {
	MaxPixels      int64 // all frames (XI_LIMITS_MAX_SRC_PIXELS)
	MaxFrames      int   // XI_LIMITS_MAX_ANIMATION_FRAMES
	MaxFramePixels int64 // XI_LIMITS_MAX_ANIMATION_FRAME_PIXELS
}

// Prober reads the source header (stage 11): magic-byte sniff plus a
// header-only decode. It returns errs.KindUnsupported for unknown or disabled
// formats, KindCorrupt for unreadable headers and KindLimitExceeded when a
// limit is exceeded. It must not retain src. codec (WP-07) implements it;
// the server re-checks the returned meta against lim.
type Prober interface {
	Probe(ctx context.Context, src fetch.Buffer, lim ProbeLimits) (SourceMeta, error)
}

// ProcessInput is everything the pipeline needs for one image (stages
// 12–13). Every field is read-only for the processor.
type ProcessInput struct {
	// Request has had client hints resolved, defaults applied and passed
	// Validate.
	Request *transform.Request
	// Source holds the original bytes (sanitised when the source is SVG).
	// The server owns the reference and releases it after Process returns;
	// the processor must Retain it to keep it longer.
	Source fetch.Buffer
	// Meta is the probe result.
	Meta SourceMeta
	// Decision is the negotiated output: Decision.Format is the format to
	// encode, Lossless and Quantize are encoder hints.
	Decision negotiate.Decision
	// QualityDelta lowers the effective quality for Save-Data / slow
	// connection hints (negotiate.HintResult.QualityDelta); 0 = none.
	QualityDelta int
}

// Blob is encoded output with an explicit owner (a vips blob in
// production). Bytes is valid until Release; Release is called exactly once.
type Blob interface {
	Bytes() []byte
	Release()
}

// ProcessOutput is an encoded image. The server writes Body and releases it.
type ProcessOutput struct {
	Body          Blob
	ContentType   string
	Format        transform.Format
	Width, Height int
}

// Processor plans, executes and encodes one image (stages 12–13;
// pipeline/plan + pipeline/exec + codec/encode, WP-25/26a/30). It must honour
// ctx between steps. Errors carry an errs kind: KindCorrupt (decode failed),
// KindLimitExceeded, KindUnsupported, KindInternal.
type Processor interface {
	Process(ctx context.Context, in *ProcessInput) (*ProcessOutput, error)
}

// Metrics receives request-level observations; *metrics.Metrics satisfies it.
type Metrics interface {
	ObserveRequest(dialect string, status int, format string, d time.Duration)
	RequestStarted() (done func())
	ObserveStage(stage string, d time.Duration)
	IncPanic()
}

// Limits are the configured input and output limits (XI_LIMITS_*).
type Limits struct {
	MaxSrcBytes             int64
	MaxSrcPixels            int64
	MaxAnimationFrames      int
	MaxAnimationFramePixels int64
	MaxResultDimension      int
	// AllowRaising lets request security options raise limits above the
	// configured values (XI_SECURITY_ALLOW_RAISING_LIMITS); otherwise they
	// can only lower them.
	AllowRaising bool
}

// Config holds the HTTP settings (XI_SERVER_*) and the per-request policy
// the handler applies.
type Config struct {
	Addr              string
	PathPrefix        string        // XI_SERVER_PATH_PREFIX; stripped before routing
	RequestTimeout    time.Duration // whole-request budget
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int

	// CompatImgproxyStatus answers invalid URLs (400) and forbidden sources
	// or options (403 KindForbidden) with 404 like imgproxy
	// (XI_COMPAT_IMGPROXY_STATUS).
	CompatImgproxyStatus bool

	Defaults  transform.Defaults
	Negotiate negotiate.Config
	Limits    Limits
	SVG       svg.Options // sanitiser limits (XI_LIMITS_MAX_SVG_*)
	// ETagBuster is mixed into every ETag (XI_CACHE_ETAG_BUSTER).
	ETagBuster string
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
		Defaults:          transform.StandardDefaults(),
		Negotiate:         negotiate.DefaultConfig(),
		Limits: Limits{
			MaxSrcBytes:             50 << 20,
			MaxSrcPixels:            50_000_000,
			MaxAnimationFrames:      256,
			MaxAnimationFramePixels: 16_000_000,
			MaxResultDimension:      8192,
		},
	}
}

// Validate reports every invalid field.
func (c *Config) Validate() error {
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
	if c.PathPrefix != "" && (!strings.HasPrefix(c.PathPrefix, "/") || c.PathPrefix == "/") {
		list = append(list, fmt.Errorf("server: path prefix %q must start with / and not be /", c.PathPrefix))
	}
	if err := c.Negotiate.Validate(); err != nil {
		list = append(list, err)
	}
	return errors.Join(list...)
}

// Deps are the components the server delegates to. Router, Resolver,
// Fetcher, Admitter, Prober, Processor, Allocator and Logger are required.
type Deps struct {
	Router    Router
	Resolver  Resolver
	Fetcher   Fetcher
	Admitter  Admitter
	Prober    Prober
	Processor Processor
	// Allocator holds sanitised SVG documents (vips.NewBuffer in
	// production, so libvips never sees Go memory).
	Allocator fetch.Allocator
	Logger    *slog.Logger
	Metrics   Metrics          // nil: no metrics
	Readiness *admin.Readiness // nil: /readyz is not mounted
	// Tracer creates the fetch, probe and process spans; nil: no spans.
	Tracer trace.Tracer
	// Middleware wraps the whole handler inside panic recovery (OTel
	// otelhttp when tracing is on); nil: none.
	Middleware func(http.Handler) http.Handler
}

// Server is the public listener's handler tree.
type Server struct {
	cfg     Config
	deps    Deps
	handler http.Handler
}

// New validates cfg and builds the handler tree.
func New(cfg *Config, deps *Deps) (*Server, error) {
	if cfg == nil {
		return nil, errors.New("server: nil config")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if deps == nil || deps.Router == nil || deps.Resolver == nil || deps.Fetcher == nil || deps.Admitter == nil ||
		deps.Prober == nil || deps.Processor == nil || deps.Allocator == nil || deps.Logger == nil {
		return nil, errors.New("server: router, resolver, fetcher, admitter, prober, processor, allocator and logger are required")
	}
	s := &Server{cfg: *cfg, deps: *deps}
	if s.deps.Metrics == nil {
		s.deps.Metrics = nopMetrics{}
	}
	if s.deps.Tracer == nil {
		s.deps.Tracer = noop.NewTracerProvider().Tracer("")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", admin.Healthz)
	if s.deps.Readiness != nil {
		mux.Handle("GET /readyz", s.deps.Readiness)
	}
	mux.HandleFunc("GET /{path...}", s.image)
	h := s.ingress(mux)
	if s.deps.Middleware != nil {
		h = s.deps.Middleware(h)
	}
	s.handler = s.recoverer(h)
	return s, nil
}

// Handler returns the root handler (recover → [middleware] → ingress →
// routes).
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
func (nopMetrics) ObserveStage(string, time.Duration)                {}
func (nopMetrics) IncPanic()                                         {}
