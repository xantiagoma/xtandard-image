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

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/codec/container"
	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/router"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/obs/admin"
	"github.com/xantiagoma/xtandard-image/internal/security/svg"
	"github.com/xantiagoma/xtandard-image/internal/source"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
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
	// Frames is the number of animation frames (1 for still images,
	// multi-page files included).
	Frames int
	// Pages is the number of pages, frames or entries in the file (≥ 1):
	// it bounds the `page` option of multi-page stills (TIFF, HEIF).
	Pages       int
	HasAlpha    bool
	IsAnimated  bool
	Orientation int // EXIF orientation 1..8 (1 when absent)
	// Video describes a video source (container, streams, the display size
	// and matrix of its frames); nil for images. A video_thumbnail_tile or
	// _animation request turns the fields above into those of the sprite
	// or animation (videoComposite), and Composite is set.
	Video     *video.Meta
	Composite bool
	// Raw describes a camera RAW's decode paths (demosaic, half-size,
	// embedded preview): the source limits judge it on what can be decoded
	// (transform.SourceLimits.CheckRaw). nil for other formats.
	Raw *transform.RawSource
	// Container is set by the server (not the Prober) when the source is a
	// designer document and the fields above describe the image embedded
	// in it (codec/container, WP-107); nil otherwise.
	Container *container.Source
	// Session is what the prober kept open for the processor (a video's
	// FFmpeg handle, so the frames are not probed twice); nil when
	// nothing. The server closes it once the request is done.
	Session Session
}

// Session is a resource a Prober hands to the Processor through
// SourceMeta. Close must be idempotent.
type Session interface{ Close() }

// closeSession closes m's session, if any.
func (m *SourceMeta) closeSession() {
	if m.Session != nil {
		m.Session.Close()
	}
}

// ProbeLimits are the effective input limits of one request (config values,
// lowered — or raised, with XI_SECURITY_ALLOW_RAISING_LIMITS — by the
// request's security options). Zero means unlimited.
type ProbeLimits struct {
	MaxPixels      int64 // frames loaded (XI_LIMITS_MAX_SRC_PIXELS)
	MaxFrames      int   // XI_LIMITS_MAX_ANIMATION_FRAMES
	MaxFramePixels int64 // XI_LIMITS_MAX_ANIMATION_FRAME_PIXELS; zero is off (the total applies), not unlimited
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
	// Watermarks are the request's own watermark images, fetched and
	// sanitised like Source and owned by the server the same way.
	Watermarks []WatermarkInput
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
	// Quality is the encoder quality of Body (0 for formats without one);
	// with auto-quality, the quality the search chose. Sent as X-Xi-Quality
	// with debug headers.
	Quality int
	// Skipped lists request options the pipeline did not apply yet (later
	// work packages); the server logs them at debug level.
	Skipped []string
	// Best summarises the best-format selection of a format=best request
	// (content class, every candidate tried and its size); sent as X-Xi-Best
	// with debug headers. Empty otherwise.
	Best string
	// RawPath is how a camera RAW source was decoded ("preview",
	// "demosaic" or "demosaic-half"; empty for other formats): counted in
	// xi_raw_decodes_total and sent as X-Xi-Raw-Path with debug headers.
	RawPath string
	// VideoFrame describes the frame a video source was decoded from
	// (timestamp, keyframe, best-frame selection); sent as
	// X-Xi-Video-Frame with debug headers. Empty for images.
	VideoFrame string
	// VideoCodec is the codec of a video output ("vp9", "av1", "h264",
	// "hevc"); sent as X-Xi-Video-Codec with debug headers. Empty for
	// images.
	VideoCodec string
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
	// ObserveRawDecode counts one camera RAW decode by path
	// (ProcessOutput.RawPath).
	ObserveRawDecode(path string)
}

// Limits are the configured input and output limits (XI_LIMITS_*).
type Limits struct {
	MaxSrcBytes             int64
	MaxSrcPixels            int64
	MaxAnimationFrames      int
	MaxAnimationFramePixels int64
	MaxResultDimension      int
	// MaxVideoCompositeFrames caps the frames of a video sprite or
	// animation (XI_VIDEO_MAX_ANIMATION_FRAMES).
	MaxVideoCompositeFrames int
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
	// ClientHints configures the Accept-CH / Critical-CH response headers
	// (hints.go); Negotiate.EnableClientHints is the server-wide switch.
	ClientHints ClientHintHeaders
	Limits      Limits
	SVG         svg.Options // sanitiser limits (XI_LIMITS_MAX_SVG_*)
	// ETagBuster is mixed into every ETag (XI_CACHE_ETAG_BUSTER).
	ETagBuster string
	// Cache is the Cache-Control policy (XI_CACHE_HEADERS_*).
	Cache CacheHeaders
	// CacheTagsHeader names the response header carrying the cache_tags
	// option (XI_CACHE_TAGS_HEADER); empty disables it.
	CacheTagsHeader string
	// DebugHeaders adds X-Xi-Origin-*, X-Xi-Result-* and X-Xi-Ignored
	// (XI_OBSERVABILITY_DEBUG_HEADERS).
	DebugHeaders bool
	// CanonicalHeader adds `Link: <source URL>; rel="canonical"` for http
	// sources (imgproxy IMGPROXY_SET_CANONICAL_HEADER; no XI_ variable yet).
	CanonicalHeader bool
	// Fallback is the image served in place of a failed source (XI_FALLBACK_*).
	Fallback Fallback
	// Info configures the /info endpoint (info.go).
	Info InfoConfig
	// ResultCache configures the result cache (cache.go); used only when
	// Deps.ResultCache is set.
	ResultCache ResultCacheConfig
}

// CacheHeaders is the Cache-Control policy of ARCHITECTURE §9.1 and §12.
type CacheHeaders struct {
	// TTL is max-age for successes; 0 answers no-cache.
	TTL time.Duration
	// ErrorTTL is max-age for source and image failures (§12 "error TTL");
	// 0 answers no-store.
	ErrorTTL time.Duration
	// Passthrough copies the source's Cache-Control (or its Expires as
	// max-age) when the URL sets no TTL or expiry.
	Passthrough bool
}

// Fallback configures the fallback image (ARCHITECTURE §12, special.go).
type Fallback struct {
	// Source is the global fallback image as a source string
	// (XI_FALLBACK_SOURCE); it must pass the source allowlist. "" = none.
	Source string
	// Status is the status of a fallback response (XI_FALLBACK_STATUS);
	// 0 means 200.
	Status int
	// TTL is the max-age of a fallback response (XI_FALLBACK_TTL); 0 sends
	// no-store.
	TTL time.Duration
	// AllowRequestURL honours a request's own fallback image
	// (imgproxy fallback_image_url; XI_FALLBACK_ALLOW_REQUEST_URL). It still
	// passes the source allowlist.
	AllowRequestURL bool
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
		Cache:           CacheHeaders{TTL: 31_536_000 * time.Second, ErrorTTL: 10 * time.Second},
		CacheTagsHeader: "Cache-Tag",
		Fallback:        Fallback{Status: http.StatusOK, TTL: time.Minute},
		Info:            InfoConfig{CacheTTL: 365 * 24 * time.Hour},
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
	if c.Cache.TTL < 0 || c.Cache.ErrorTTL < 0 {
		list = append(list, fmt.Errorf("server: cache ttls must be >= 0, got %s and %s", c.Cache.TTL, c.Cache.ErrorTTL))
	}
	if c.CacheTagsHeader != "" && !validHeaderName(c.CacheTagsHeader) {
		list = append(list, fmt.Errorf("server: cache tags header %q is not a valid header name", c.CacheTagsHeader))
	}
	if c.MaxHeaderBytes <= 0 {
		list = append(list, fmt.Errorf("server: max header bytes must be > 0, got %d", c.MaxHeaderBytes))
	}
	if c.PathPrefix != "" && (!strings.HasPrefix(c.PathPrefix, "/") || c.PathPrefix == "/") {
		list = append(list, fmt.Errorf("server: path prefix %q must start with / and not be /", c.PathPrefix))
	}
	if s := c.Fallback.Status; s != 0 && (s < 200 || s > 599) {
		list = append(list, fmt.Errorf("server: fallback status must be in 200..599, got %d", s))
	}
	if c.ResultCache.TTL < 0 {
		list = append(list, fmt.Errorf("server: result cache ttl must be >= 0, got %s", c.ResultCache.TTL))
	}
	if c.Fallback.TTL < 0 {
		list = append(list, fmt.Errorf("server: fallback ttl must be >= 0, got %s", c.Fallback.TTL))
	}
	if err := c.Negotiate.Validate(); err != nil {
		list = append(list, err)
	}
	return errors.Join(list...)
}

// Sniffed is the magic-byte identity of source bytes (codec.Sniff; WP-07).
type Sniffed struct {
	// Format is the matching output format; transform.FormatUnset for
	// formats that cannot be written (JPEG 2000, PSD, PDF, RAW, …).
	Format transform.Format
	// ContentType is the MIME type of the sniffed format. It is the only
	// source of an unprocessed response's Content-Type; the origin's
	// header is never trusted.
	ContentType string
	// Ext is the file extension without a dot ("jpg"); "" when unknown.
	Ext string
}

// Sniffer detects the format of source bytes from magic bytes only (never
// the extension or the upstream Content-Type). ok is false for
// unrecognised input. app adapts codec.Sniff to it.
type Sniffer interface {
	Sniff(data []byte) (Sniffed, bool)
}

// Deps are the components the server delegates to. Router, Resolver,
// Fetcher, Admitter, Prober, Processor, Sniffer, Allocator and Logger are
// required.
type Deps struct {
	Router    Router
	Resolver  Resolver
	Fetcher   Fetcher
	Admitter  Admitter
	Prober    Prober
	Processor Processor
	// Sniffer identifies the bytes of raw and skip-processing responses.
	Sniffer Sniffer
	// Inspector reads source metadata for /info requests; nil: they answer
	// 501.
	Inspector Inspector
	// Allocator holds sanitised SVG documents (vips.NewBuffer in
	// production, so libvips never sees Go memory).
	Allocator fetch.Allocator
	// ResultCache stores encoded responses (cache.go; a cache.AsyncStore
	// over the configured tiers in production, so Set never blocks); nil
	// disables the result cache.
	ResultCache cache.Store
	Logger      *slog.Logger
	Metrics     Metrics          // nil: no metrics
	Readiness   *admin.Readiness // nil: /readyz is not mounted
	// Tracer creates the fetch, probe and process spans; nil: no spans.
	Tracer trace.Tracer
	// Middleware wraps the whole handler inside panic recovery (OTel
	// otelhttp when tracing is on); nil: none.
	Middleware func(http.Handler) http.Handler
}

// Public listener ServeMux patterns. Every image dialect and /info is
// reached through PatternImage (dialect/router picks the mount).
const (
	PatternHealthz = "GET /healthz"
	PatternReadyz  = "GET /readyz" // only when Deps.Readiness is set
	PatternImage   = "GET /{path...}"
)

// Patterns returns every pattern New can register on the public ServeMux.
// The OpenAPI route-coverage test (internal/apidoc) maps each to a
// documented operation.
func Patterns() []string { return []string{PatternHealthz, PatternReadyz, PatternImage} }

// Server is the public listener's handler tree.
type Server struct {
	cfg     Config
	deps    Deps
	handler http.Handler
	// now is the clock for expires and Cache-Control; tests replace it.
	now func() time.Time
	// fallbackKey is the resolved XI_FALLBACK_SOURCE; nil when unset.
	fallbackKey *source.Key
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
		deps.Prober == nil || deps.Processor == nil || deps.Sniffer == nil || deps.Allocator == nil || deps.Logger == nil {
		return nil, errors.New("server: router, resolver, fetcher, admitter, prober, processor, sniffer, allocator and logger are required")
	}
	s := &Server{cfg: *cfg, deps: *deps, now: time.Now}
	if cfg.Fallback.Source != "" {
		key, err := deps.Resolver.Resolve(transform.SourceSpec{Raw: cfg.Fallback.Source})
		if err != nil {
			return nil, fmt.Errorf("server: fallback source: %w", err)
		}
		s.fallbackKey = &key
	}
	if s.deps.Metrics == nil {
		s.deps.Metrics = nopMetrics{}
	}
	if s.deps.Tracer == nil {
		s.deps.Tracer = noop.NewTracerProvider().Tracer("")
	}

	mux := http.NewServeMux()
	mux.HandleFunc(PatternHealthz, admin.Healthz)
	if s.deps.Readiness != nil {
		mux.Handle(PatternReadyz, s.deps.Readiness)
	}
	mux.HandleFunc(PatternImage, s.image)
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

// validHeaderName reports an RFC 9110 token.
func validHeaderName(s string) bool {
	for i := range len(s) {
		c := s[i]
		if c <= 0x20 || c >= 0x7f || strings.IndexByte(`"(),/:;<=>?@[\]{}`, c) >= 0 {
			return false
		}
	}
	return s != ""
}

type nopMetrics struct{}

func (nopMetrics) ObserveRequest(string, int, string, time.Duration) {}
func (nopMetrics) RequestStarted() func()                            { return func() {} }
func (nopMetrics) ObserveStage(string, time.Duration)                {}
func (nopMetrics) IncPanic()                                         {}
func (nopMetrics) ObserveRawDecode(string)                           {}
