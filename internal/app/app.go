// Package app is the composition root (ARCHITECTURE §3): it builds every
// component from config.Config, wires them together and owns the process
// lifecycle. It is the only package that constructs concrete types; cmd
// calls Run and nothing else imports app.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/limiter"
	"github.com/xantiagoma/xtandard-image/internal/obs/admin"
	"github.com/xantiagoma/xtandard-image/internal/obs/logging"
	"github.com/xantiagoma/xtandard-image/internal/obs/metrics"
	"github.com/xantiagoma/xtandard-image/internal/obs/tracing"
	"github.com/xantiagoma/xtandard-image/internal/security/ssrf"
	"github.com/xantiagoma/xtandard-image/internal/server"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// Options are the process-level inputs that do not come from config.Config.
type Options struct {
	// Version and Commit are the build metadata (xi_build_info, logs,
	// default User-Agent).
	Version string
	Commit  string
	// Tracing turns OpenTelemetry on (XI_OTEL_ENABLED or an
	// OTEL_EXPORTER_OTLP_*ENDPOINT; cmd decides from the environment).
	Tracing bool
}

// hooks are test seams; the zero value is production behaviour.
type hooks struct {
	listening func(public, admin net.Addr) // called once both listeners are bound
	policy    *ssrf.Policy                 // replaces the SSRF policy derived from Config
	// resultCache receives the result cache once built (nil when off), so
	// tests can wait for its asynchronous writes.
	resultCache func(*cache.AsyncStore)
	// keepVips skips vips.Shutdown: libvips cannot be used again after it,
	// and a test binary starts several services one after the other.
	keepVips bool
}

// Run starts the service and blocks until ctx is cancelled (SIGINT/SIGTERM
// via signal.NotifyContext in main) or a listener fails. See lifecycle.run
// for the shutdown order. A signal-triggered shutdown returns nil.
func Run(ctx context.Context, cfg *config.Config, logger *slog.Logger, opts Options) error {
	return run(ctx, cfg, logger, opts, hooks{})
}

func run(ctx context.Context, cfg *config.Config, logger *slog.Logger, opts Options, h hooks) (err error) {
	if err := cfg.Validate(); err != nil {
		return err
	}

	vcfg := vips.DefaultConfig()
	vcfg.Concurrency = cfg.Vips.Concurrency
	vcfg.HEICEncode = cfg.Formats.HEIC.Encode
	// vips.Init/Shutdown are reference-counted (internal/vips doc.go,
	// "Lifecycle invariant"): this instance's Shutdown only tears libvips
	// down when no other owner holds it, after every Run has returned.
	if err := vips.Init(&vcfg); err != nil {
		return fmt.Errorf("init vips: %w", err)
	}
	// Cleanups run in reverse order on every exit path; the lifecycle
	// empties the list itself on a normal shutdown so the drain order holds.
	var cleanups []func(context.Context) error
	defer func() {
		ctx := context.WithoutCancel(ctx)
		for _, cleanup := range slices.Backward(cleanups) {
			err = errors.Join(err, cleanup(ctx))
		}
	}()
	shutdownVips := func() {
		if !h.keepVips {
			vips.Shutdown()
		}
	}
	cleanups = append(cleanups, func(context.Context) error { shutdownVips(); return nil })

	met, err := metrics.New(metrics.Options{Version: opts.Version, Commit: opts.Commit, Vips: vipsStats{}})
	if err != nil {
		return err
	}
	lim, err := limiter.New(limiter.Config{
		Workers:      cfg.Processing.Workers,
		QueueSize:    cfg.Processing.QueueSize,
		QueueTimeout: cfg.Processing.QueueTimeout,
		MemoryBudget: int64(cfg.Processing.MemoryBudget),
	}, limiter.WithMetrics(&limiterMetrics{obs: met.Limiter()}))
	if err != nil {
		return err
	}
	// Once every request has drained, release libvips; if some are still in
	// C after the drain timeout, leave it to process exit instead of pulling
	// libvips out from under them.
	cleanups[0] = func(ctx context.Context) error {
		if n := lim.Stats().InFlight; n > 0 {
			logger.LogAttrs(ctx, slog.LevelWarn, "skipping vips shutdown with jobs in flight", slog.Int64("in_flight", n))
			return nil
		}
		shutdownVips()
		return nil
	}

	tp, err := tracing.New(ctx, tracing.Options{Enabled: opts.Tracing, ServiceVersion: opts.Version})
	if err != nil {
		return err
	}
	cleanups = append(cleanups, tp.Shutdown)

	userAgent := cfg.Fetch.UserAgent
	if userAgent == "" {
		userAgent = "xtandard-image/" + opts.Version
	}
	policy := ssrfPolicy(cfg)
	if h.policy != nil {
		policy = *h.policy
	}
	// One SSRF-safe client shared by every http source (dev included).
	client := ssrf.New(&ssrf.Options{Policy: policy, MaxRedirects: cfg.Fetch.MaxRedirects, UserAgent: userAgent})
	cleanups = append(cleanups, func(context.Context) error { client.CloseIdleConnections(); return nil })

	reg, closers, err := buildSources(ctx, cfg, client, userAgent)
	cleanups = append(cleanups, closeAll(closers))
	if err != nil {
		return fmt.Errorf("sources: %w", err)
	}
	types := map[string]string{}
	for name := range cfg.Sources {
		types[name] = string(cfg.Sources[name].Type)
	}
	types["_dev"] = "http"
	fetcher, err := fetch.New(reg, vipsAllocator, fetch.Config{
		Timeout:     cfg.Fetch.Timeout,
		MaxBytes:    int64(cfg.Limits.MaxSrcBytes),
		CacheBytes:  int64(cfg.Cache.Origin.MemoryBytes),
		CacheTTL:    cfg.Cache.Origin.TTL,
		NegativeTTL: cfg.Cache.Origin.NegativeTTL,
		Retry:       cfg.Fetch.Retry,
	}, fetch.WithMetrics(&fetchMetrics{m: met, types: types}))
	if err != nil {
		return err
	}

	rt, warnings, err := buildRouter(cfg)
	if err != nil {
		return fmt.Errorf("dialects: %w", err)
	}
	for _, w := range warnings {
		logger.LogAttrs(ctx, slog.LevelWarn, "config warning", slog.String("warning", w))
	}

	wm, err := loadWatermark(ctx, cfg, reg, fetcher)
	if err != nil {
		return err
	}
	// Requests read the watermark's pixels until they finish: like libvips
	// itself, it is released only once none is in flight.
	cleanups = append(cleanups, func(context.Context) error {
		if lim.Stats().InFlight == 0 {
			wm.Close()
		}
		return nil
	})
	proc, err := newProcessor(cfg, wm)
	if err != nil {
		return err
	}

	// The result cache may hold vips output blobs: it is closed (after the
	// drain) before libvips shuts down, since cleanups run in reverse.
	rc, err := buildResultCache(ctx, cfg, met, logger)
	if err != nil {
		return fmt.Errorf("result cache: %w", err)
	}
	if h.resultCache != nil {
		h.resultCache(rc)
	}

	ready := admin.NewReadiness()
	scfg := serverConfig(cfg)
	scfg.ResultCache.Build = opts.Version
	deps := &server.Deps{
		Router:   rt,
		Resolver: reg,
		Fetcher:  fetcher,
		Admitter: lim,
		Prober: &codecProber{svg: codec.Limits{
			MaxSVGBytes:    int64(cfg.Limits.MaxSVGBytes),
			MaxSVGDepth:    cfg.Limits.MaxSVGDepth,
			MaxSVGElements: cfg.Limits.MaxSVGElements,
		}},
		Sniffer:   codecSniffer{},
		Inspector: vipsInspector{},
		Processor: proc,
		Allocator: vipsAllocator,
		Logger:    logger,
		Metrics:   met,
		Readiness: ready,
		Tracer:    tp.Tracer("github.com/xantiagoma/xtandard-image/internal/server"),
		Middleware: func(next http.Handler) http.Handler {
			return tp.Middleware(next, "image")
		},
	}
	if rc != nil {
		deps.ResultCache = rc
		cleanups = append(cleanups, func(context.Context) error { return rc.Close() })
	}
	srv, err := server.New(&scfg, deps)
	if err != nil {
		return err
	}

	lc := &lifecycle{
		logger:     logger,
		ready:      ready,
		publicAddr: cfg.Server.Addr,
		adminAddr:  cfg.Admin.Addr,
		public:     srv.HTTPServer,
		admin: func(ctx context.Context) *http.Server {
			return adminServer(ctx, logger, adminMux(met, ready, cfg.Admin.Pprof))
		},
		shutdownDelay:   cfg.Server.ShutdownDelay,
		shutdownTimeout: cfg.Server.ShutdownTimeout,
		listening:       h.listening,
	}
	// After the drain, in go-standards §2 order: close sources, the http
	// client, flush traces, then vips.Shutdown (the reverse of construction).
	for _, cleanup := range slices.Backward(cleanups) {
		lc.afterDrain = append(lc.afterDrain, cleanup)
	}
	cleanups = nil
	return lc.run(ctx)
}

func adminMux(met *metrics.Metrics, ready *admin.Readiness, pprof bool) http.Handler {
	return admin.NewMux(admin.Options{Metrics: met.Handler(), Readiness: ready, Pprof: pprof})
}

// closeAll closes every source holding connections or file handles.
func closeAll(cs []io.Closer) func(context.Context) error {
	return func(context.Context) error {
		var list []error
		for _, c := range cs {
			if err := c.Close(); err != nil {
				list = append(list, fmt.Errorf("close source: %w", err))
			}
		}
		return errors.Join(list...)
	}
}

// lifecycle serves the public (and optional admin) listener and drains them
// on shutdown in go-standards §2 order:
//
//  1. readiness → 503 (draining),
//  2. wait shutdownDelay so load balancers deregister us,
//  3. http.Server.Shutdown: stop accepting (listeners closed at once), then
//     wait for in-flight requests up to shutdownTimeout,
//  4. afterDrain hooks, then the caller's cleanups (close sources, flush
//     traces, vips.Shutdown once no job is in flight).
type lifecycle struct {
	logger          *slog.Logger
	ready           *admin.Readiness
	publicAddr      string
	adminAddr       string // "" disables the admin listener
	public          func(ctx context.Context) *http.Server
	admin           func(ctx context.Context) *http.Server
	shutdownDelay   time.Duration
	shutdownTimeout time.Duration
	listening       func(public, admin net.Addr)
	afterDrain      []func(context.Context) error
	onShutdown      func() // test seam: registered with every server's RegisterOnShutdown
}

func (lc *lifecycle) serveAndDrain(ctx context.Context) error {
	// Requests must survive the shutdown signal so they can drain: their
	// base context drops ctx's cancellation and is cancelled only after
	// the drain timeout.
	baseCtx, cancelBase := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelBase()

	lnc := net.ListenConfig{}
	publicLn, err := lnc.Listen(baseCtx, "tcp", lc.publicAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", lc.publicAddr, err)
	}
	servers := []*http.Server{lc.public(baseCtx)}
	listeners := []net.Listener{publicLn}
	var adminAddr net.Addr
	if lc.adminAddr != "" && lc.admin != nil {
		adminLn, err := lnc.Listen(baseCtx, "tcp", lc.adminAddr)
		if err != nil {
			_ = publicLn.Close()
			return fmt.Errorf("listen admin %s: %w", lc.adminAddr, err)
		}
		adminAddr = adminLn.Addr()
		servers = append(servers, lc.admin(baseCtx))
		listeners = append(listeners, adminLn)
	}
	if lc.onShutdown != nil {
		for _, s := range servers {
			s.RegisterOnShutdown(lc.onShutdown)
		}
	}

	// One goroutine per listener; each exits when its server is shut down
	// (Serve returns ErrServerClosed) or its listener fails.
	serveErr := make(chan error, len(servers))
	for i, s := range servers {
		go func() { serveErr <- serve(s, listeners[i]) }()
	}

	lc.ready.SetReady()
	lc.logger.LogAttrs(ctx, slog.LevelInfo, "listening",
		slog.String("addr", publicLn.Addr().String()),
		slog.String("admin_addr", addrString(adminAddr)))
	if lc.listening != nil {
		lc.listening(publicLn.Addr(), adminAddr)
	}

	var failed error
	pending := len(servers)
	select {
	case <-ctx.Done():
		lc.logger.LogAttrs(ctx, slog.LevelInfo, "shutting down",
			slog.String("cause", context.Cause(ctx).Error()))
	case failed = <-serveErr:
		pending--
		lc.logger.LogAttrs(ctx, slog.LevelError, "listener failed", logging.Err(failed))
	}

	// 1. readiness 503, 2. let load balancers deregister us.
	lc.ready.SetDraining()
	if failed == nil && lc.shutdownDelay > 0 {
		t := time.NewTimer(lc.shutdownDelay)
		<-t.C
	}
	// 3. stop accepting and drain in-flight requests.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lc.shutdownTimeout)
	defer cancel()
	errList := []error{failed}
	for _, s := range servers {
		if err := s.Shutdown(shutdownCtx); err != nil {
			errList = append(errList, fmt.Errorf("drain: %w", err))
		}
	}
	cancelBase()
	for range pending {
		errList = append(errList, <-serveErr)
	}
	lc.logger.LogAttrs(ctx, slog.LevelInfo, "drained")
	return errors.Join(errList...)
}

// run serves until shutdown, then runs the afterDrain hooks (step 4) on
// every exit path, including a listener that failed to bind.
func (lc *lifecycle) run(ctx context.Context) error {
	err := lc.serveAndDrain(ctx)
	ctx = context.WithoutCancel(ctx)
	for _, f := range lc.afterDrain {
		err = errors.Join(err, f(ctx))
	}
	return err
}

func serve(s *http.Server, ln net.Listener) error {
	if err := s.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve %s: %w", ln.Addr(), err)
	}
	return nil
}

// adminServer serves the admin mux with every timeout set; the write timeout
// leaves room for 30 s CPU profiles.
func adminServer(ctx context.Context, logger *slog.Logger, h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    32 << 10,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
}

func addrString(a net.Addr) string {
	if a == nil {
		return ""
	}
	return a.String()
}
