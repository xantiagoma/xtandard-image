package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/app"
	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/obs/logging"
	"github.com/xantiagoma/xtandard-image/internal/obs/tracing"
)

var errUnknownCommand = errors.New("unknown command")

const usageText = `usage: xtandard-image <command> [flags]

commands:
  serve          start the image proxy (default)
  config print   print the effective configuration (secrets redacted)
  health         probe a running instance's health endpoint
  version        print version information
`

// run is the testable entry point: it owns no process state and receives
// everything it needs (args without argv[0], the environment as KEY=value
// pairs, output).
func run(ctx context.Context, args []string, environ []string, stdout io.Writer) error {
	cmd := "serve"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		return serve(ctx, args, environ, stdout)
	case "config":
		if len(args) == 0 || args[0] != "print" {
			return fmt.Errorf("%w %q\n%s", errUnknownCommand, strings.TrimSpace("config "+strings.Join(args, " ")), usageText)
		}
		return configPrint(args[1:], environ, stdout)
	case "health":
		return health(ctx, args, stdout)
	case "version":
		return printVersion(args, stdout)
	case "help", "-h", "--help":
		_, err := io.WriteString(stdout, usageText)
		return err
	default:
		return fmt.Errorf("%w %q\n%s", errUnknownCommand, cmd, usageText)
	}
}

// serve runs the image proxy until ctx is cancelled (SIGINT/SIGTERM).
func serve(ctx context.Context, args []string, environ []string, stdout io.Writer) error {
	fs := newFlagSet("serve", stdout)
	configFile := fs.String("config", "", "YAML config file (default $XI_CONFIG_FILE, else "+config.DefaultConfigFile+" if present)")
	if err := fs.Parse(args); err != nil {
		return ignoreHelp(err)
	}
	cfg, meta, err := config.Load(config.LoadOptions{Environ: environ, ConfigFile: *configFile})
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	logger := logging.New(stdout, logging.Options{Level: cfg.Observability.LogLevel.Level()})
	logMeta(ctx, logger, &meta)

	info := versionInfo(version, commit)
	getenv := lookup(environ)
	return app.Run(ctx, &cfg, logger, app.Options{
		Version: info.Version,
		Commit:  info.Commit,
		Tracing: cfg.OTel.Enabled || tracing.EnabledFromEnv(getenv),
	})
}

// logMeta reports what config.Load tolerated: warnings, unknown XI_*
// variables (XI_ALLOW_UNKNOWN_ENV) and untranslated IMGPROXY_* variables.
func logMeta(ctx context.Context, logger *slog.Logger, meta *config.Meta) {
	if meta.File != "" {
		logger.LogAttrs(ctx, slog.LevelInfo, "config file loaded", slog.String("file", meta.File))
	}
	for _, w := range meta.Warnings {
		logger.LogAttrs(ctx, slog.LevelWarn, "config warning", slog.String("warning", w))
	}
	if len(meta.UnknownEnv) > 0 {
		logger.LogAttrs(ctx, slog.LevelWarn, "unknown environment variables ignored",
			slog.String("variables", strings.Join(meta.UnknownEnv, ",")))
	}
	if len(meta.UnmappedImgproxyEnv) > 0 {
		logger.LogAttrs(ctx, slog.LevelWarn, "imgproxy environment variables not translated",
			slog.String("variables", strings.Join(meta.UnmappedImgproxyEnv, ",")))
	}
}

// configPrint prints the effective configuration as redacted YAML. It
// prints what could be loaded even when validation fails, then fails.
func configPrint(args, environ []string, stdout io.Writer) error {
	fs := newFlagSet("config print", stdout)
	configFile := fs.String("config", "", "YAML config file")
	if err := fs.Parse(args); err != nil {
		return ignoreHelp(err)
	}
	cfg, _, err := config.Load(config.LoadOptions{Environ: environ, ConfigFile: *configFile})
	if _, werr := io.WriteString(stdout, cfg.Redacted()); werr != nil {
		return werr
	}
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return nil
}

// lookup turns KEY=value pairs into a getenv function (for the OTEL_*
// variables the tracing package reads).
func lookup(environ []string) func(string) string {
	m := make(map[string]string, len(environ))
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return func(k string) string { return m[k] }
}

// health performs a GET against a running instance and fails unless it
// answers 2xx. It backs the container HEALTHCHECK (no curl in the image).
func health(ctx context.Context, args []string, stdout io.Writer) error {
	fs := newFlagSet("health", stdout)
	url := fs.String("url", "http://127.0.0.1:8080/healthz", "health endpoint to probe")
	timeout := fs.Duration("timeout", 3*time.Second, "overall probe timeout")
	if err := fs.Parse(args); err != nil {
		return ignoreHelp(err)
	}

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, *url, http.NoBody)
	if err != nil {
		return fmt.Errorf("health: build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("health: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("health: %s returned %d", *url, resp.StatusCode)
	}
	return nil
}

func printVersion(args []string, stdout io.Writer) error {
	fs := newFlagSet("version", stdout)
	if err := fs.Parse(args); err != nil {
		return ignoreHelp(err)
	}
	info := versionInfo(version, commit)
	_, err := fmt.Fprintf(stdout, "xtandard-image %s (commit %s, %s, %s/%s)\n",
		info.Version, info.Commit, info.GoVersion, runtime.GOOS, runtime.GOARCH)
	return err
}

// buildInfo is the resolved version metadata of the running binary.
type buildInfo struct {
	Version   string
	Commit    string
	GoVersion string
}

// versionInfo prefers link-time values (-ldflags -X, used by Docker builds
// that have no .git) and falls back to the module and VCS data embedded by
// the go command.
func versionInfo(ldVersion, ldCommit string) buildInfo {
	bi, _ := debug.ReadBuildInfo()
	return resolveBuildInfo(ldVersion, ldCommit, bi)
}

func resolveBuildInfo(ldVersion, ldCommit string, bi *debug.BuildInfo) buildInfo {
	out := buildInfo{Version: ldVersion, Commit: ldCommit, GoVersion: runtime.Version()}
	if bi != nil {
		out.GoVersion = bi.GoVersion
		if out.Version == "" {
			out.Version = bi.Main.Version
		}
		if out.Commit == "" {
			var rev string
			var dirty bool
			for _, s := range bi.Settings {
				switch s.Key {
				case "vcs.revision":
					rev = s.Value
				case "vcs.modified":
					dirty = s.Value == "true"
				}
			}
			if rev != "" && dirty {
				rev += "-dirty"
			}
			out.Commit = rev
		}
	}
	if out.Version == "" {
		out.Version = "(devel)"
	}
	if out.Commit == "" {
		out.Commit = "unknown"
	}
	return out
}

func newFlagSet(name string, out io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(out)
	return fs
}

// ignoreHelp turns -h/--help into a successful exit; the flag package has
// already printed the usage.
func ignoreHelp(err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return fmt.Errorf("parse flags: %w", err)
}
