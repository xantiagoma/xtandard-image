// Package app is the composition root (ARCHITECTURE §3): it builds every
// component from Config, wires them together and owns the process
// lifecycle. It is the only package that constructs concrete types; cmd
// calls Run and nothing else imports app.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/limiter"
	"github.com/xantiagoma/xtandard-image/internal/obs/admin"
	"github.com/xantiagoma/xtandard-image/internal/obs/logging"
	"github.com/xantiagoma/xtandard-image/internal/obs/metrics"
	"github.com/xantiagoma/xtandard-image/internal/security/ssrf"
	"github.com/xantiagoma/xtandard-image/internal/server"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// Build is the binary's version metadata (xi_build_info).
type Build struct {
	Version string
	Commit  string
}

// hooks are test seams; the zero value is production behaviour.
type hooks struct {
	listening func(public, admin net.Addr) // called once both listeners are bound
	policy    *ssrf.Policy                 // replaces the SSRF policy derived from Config
}

// Run starts the service and blocks until ctx is cancelled (SIGINT/SIGTERM
// via signal.NotifyContext in main) or a listener fails. Shutdown order
// follows go-standards §2: readiness → 503, wait ServerShutdownDelay,
// http.Server.Shutdown (drains in-flight requests for up to
// ServerShutdownTimeout), then vips.Shutdown. A signal-triggered shutdown
// returns nil.
func Run(ctx context.Context, cfg *Config, logger *slog.Logger) error {
	return run(ctx, cfg, logger, hooks{})
}

func run(ctx context.Context, cfg *Config, logger *slog.Logger, h hooks) error {
	build := cfg.Build
	if err := cfg.Validate(); err != nil {
		return err
	}

	vcfg := vips.DefaultConfig()
	vcfg.Concurrency = cfg.VipsConcurrency
	if err := vips.Init(vcfg); err != nil {
		return fmt.Errorf("init vips: %w", err)
	}

	met, err := metrics.New(metrics.Options{Version: build.Version, Commit: build.Commit, Vips: vipsStats{}})
	if err != nil {
		vips.Shutdown()
		return err
	}
	lim, err := limiter.New(limiter.Config{
		Workers:      cfg.ProcessingWorkers,
		QueueSize:    cfg.ProcessingQueueSize,
		QueueTimeout: cfg.ProcessingQueueTimeout,
	}, limiter.WithMetrics(&limiterMetrics{obs: met.Limiter()}))
	if err != nil {
		vips.Shutdown()
		return err
	}
	// Once every request has drained, release libvips; if some are still in
	// C after the drain timeout, leave it to process exit instead of pulling
	// libvips out from under them.
	defer func() {
		if n := lim.Stats().InFlight; n > 0 {
			logger.LogAttrs(context.WithoutCancel(ctx), slog.LevelWarn, "skipping vips shutdown with jobs in flight",
				slog.Int64("in_flight", n))
			return
		}
		vips.Shutdown()
	}()

	policy := ssrf.Policy{}
	if cfg.FetchAllowPrivateNetworks {
		policy.Allow = ssrf.ClassPrivateNetworks
	}
	if h.policy != nil {
		policy = *h.policy
	}
	client := ssrf.New(&ssrf.Options{Policy: policy})
	defer client.CloseIdleConnections()

	ready := admin.NewReadiness()
	srv, err := server.New(server.Config{
		Addr:              cfg.ServerAddr,
		RequestTimeout:    cfg.ServerRequestTimeout,
		ReadHeaderTimeout: cfg.ServerReadHeaderTimeout,
		ReadTimeout:       cfg.ServerReadTimeout,
		WriteTimeout:      cfg.ServerWriteTimeout,
		IdleTimeout:       cfg.ServerIdleTimeout,
		MaxHeaderBytes:    cfg.ServerMaxHeaderBytes,
		DevMode:           cfg.DevMode,
	}, &server.Deps{
		Fetcher:   &httpFetcher{client: client, timeout: cfg.FetchTimeout, maxBytes: cfg.LimitsMaxSrcBytes},
		Admitter:  lim,
		Processor: &vipsProcessor{quality: cfg.JPEGQuality},
		Logger:    logger,
		Metrics:   met,
		Readiness: ready,
	})
	if err != nil {
		return err
	}

	// Requests must survive the shutdown signal so they can drain: their
	// base context drops ctx's cancellation and is cancelled only after
	// the drain timeout.
	baseCtx, cancelBase := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelBase()

	lc := net.ListenConfig{}
	publicLn, err := lc.Listen(baseCtx, "tcp", cfg.ServerAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.ServerAddr, err)
	}
	servers := []*http.Server{srv.HTTPServer(baseCtx)}
	listeners := []net.Listener{publicLn}
	var adminAddr net.Addr
	if cfg.AdminAddr != "" {
		adminLn, err := lc.Listen(baseCtx, "tcp", cfg.AdminAddr)
		if err != nil {
			_ = publicLn.Close()
			return fmt.Errorf("listen admin %s: %w", cfg.AdminAddr, err)
		}
		adminAddr = adminLn.Addr()
		servers = append(servers, adminServer(baseCtx, logger, admin.NewMux(admin.Options{
			Metrics:   met.Handler(),
			Readiness: ready,
			Pprof:     cfg.AdminPprof,
		})))
		listeners = append(listeners, adminLn)
	}

	// One goroutine per listener; each exits when its server is shut down
	// (Serve returns ErrServerClosed) or its listener fails.
	serveErr := make(chan error, len(servers))
	for i, s := range servers {
		go func() { serveErr <- serve(s, listeners[i]) }()
	}

	ready.SetReady()
	logger.LogAttrs(ctx, slog.LevelInfo, "listening",
		slog.String("addr", publicLn.Addr().String()),
		slog.String("admin_addr", addrString(adminAddr)),
		slog.Bool("dev_mode", cfg.DevMode),
		slog.String("version", build.Version))
	if h.listening != nil {
		h.listening(publicLn.Addr(), adminAddr)
	}

	var failed error
	pending := len(servers)
	select {
	case <-ctx.Done():
		logger.LogAttrs(ctx, slog.LevelInfo, "shutting down",
			slog.String("cause", context.Cause(ctx).Error()))
	case failed = <-serveErr:
		pending--
		logger.LogAttrs(ctx, slog.LevelError, "listener failed", logging.Err(failed))
	}

	// 1. readiness 503, 2. let load balancers deregister us.
	ready.SetDraining()
	if failed == nil && cfg.ServerShutdownDelay > 0 {
		t := time.NewTimer(cfg.ServerShutdownDelay)
		<-t.C
	}
	// 3. drain in-flight requests.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ServerShutdownTimeout)
	defer cancel()
	shutdownErrs := []error{failed}
	for _, s := range servers {
		shutdownErrs = append(shutdownErrs, s.Shutdown(shutdownCtx))
	}
	cancelBase()
	for range pending {
		shutdownErrs = append(shutdownErrs, <-serveErr)
	}
	// 4. vips.Shutdown runs in the deferred func above.
	return errors.Join(shutdownErrs...)
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
