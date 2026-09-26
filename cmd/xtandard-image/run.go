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
	"slices"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/apidoc"
	"github.com/xantiagoma/xtandard-image/internal/app"
	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/obs/logging"
	"github.com/xantiagoma/xtandard-image/internal/obs/tracing"
)

var (
	errUnknownCommand = errors.New("unknown command")
	// errInvalidConfig is returned by config validate after it has printed
	// the violations; main exits 1.
	errInvalidConfig = errors.New("invalid configuration")
)

const usageText = `usage: xtandard-image <command> [flags]

commands:
  serve            start the image proxy (default)
  config print     print the effective configuration (secrets redacted)
  config validate  check a config file (and the environment) with the real loader
  config schema    print the config file JSON Schema
  openapi          print the OpenAPI 3.1 document of the HTTP API
  health           probe a running instance's health endpoint
  version          print version information
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
		return configCommand(args, environ, stdout)
	case "openapi":
		return printOpenAPI(args, stdout)
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
	configFile := fs.String("config", "", configFlagUsage)
	if err := fs.Parse(args); err != nil {
		return ignoreHelp(err)
	}
	cfg, meta, err := config.Load(config.LoadOptions{Environ: environ, ConfigFile: *configFile})
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	logger := logging.New(stdout, logging.Options{Level: cfg.Observability.LogLevel.Level()})
	logMeta(ctx, logger, &meta)

	info := versionInfo(version, commit, date)
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

// configFlagUsage documents --config for every command that loads it.
const configFlagUsage = "config file, .yaml/.yml/.json/.toml (default $XI_CONFIG_FILE, else " + config.DefaultConfigFiles + " if present)"

// configCommand dispatches the config subcommands.
func configCommand(args, environ []string, stdout io.Writer) error {
	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "print":
		return configPrint(args, environ, stdout)
	case "validate":
		return configValidate(args, environ, stdout)
	case "schema":
		return configSchema(args, stdout)
	default:
		return fmt.Errorf("%w %q\n%s", errUnknownCommand, strings.TrimSpace("config "+sub), usageText)
	}
}

// configValidate loads the configuration exactly like serve (file, then
// IMGPROXY_* and XI_* unless --no-env, then Validate) and prints ok or
// every violation with its key path and variable.
func configValidate(args, environ []string, stdout io.Writer) error {
	fs := newFlagSet("config validate", stdout)
	configFile := fs.String("config", "", configFlagUsage)
	noEnv := fs.Bool("no-env", false, "ignore XI_* and IMGPROXY_* variables: check the file alone")
	if err := fs.Parse(args); err != nil {
		return ignoreHelp(err)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("config validate: unexpected argument %q", fs.Arg(0))
	}
	if *noEnv {
		environ = slices.DeleteFunc(slices.Clone(environ), func(kv string) bool {
			return strings.HasPrefix(kv, "XI_") || strings.HasPrefix(kv, "IMGPROXY_")
		})
	}
	_, meta, err := config.Load(config.LoadOptions{Environ: environ, ConfigFile: *configFile})
	file := meta.File
	if file == "" {
		file = "(no config file)"
	}
	var out strings.Builder
	for _, w := range meta.Warnings {
		out.WriteString("warning: " + w + "\n")
	}
	if len(meta.UnknownEnv) > 0 {
		out.WriteString("warning: unknown environment variables ignored: " + strings.Join(meta.UnknownEnv, ",") + "\n")
	}
	if err != nil {
		out.WriteString(file + ": invalid\n" + err.Error() + "\n")
	} else {
		out.WriteString(file + ": ok\n")
	}
	if _, werr := io.WriteString(stdout, out.String()); werr != nil {
		return werr
	}
	if err != nil {
		return fmt.Errorf("config validate: %w", errInvalidConfig)
	}
	return nil
}

// configSchema prints the embedded config JSON Schema: the same bytes the
// admin listener serves at /config.schema.json.
func configSchema(args []string, stdout io.Writer) error {
	fs := newFlagSet("config schema", stdout)
	if err := fs.Parse(args); err != nil {
		return ignoreHelp(err)
	}
	_, err := stdout.Write(config.Schema())
	return err
}

// printOpenAPI prints the embedded OpenAPI document: the same bytes the
// admin listener serves at /openapi.json and Pages publishes (WP-81b).
func printOpenAPI(args []string, stdout io.Writer) error {
	fs := newFlagSet("openapi", stdout)
	if err := fs.Parse(args); err != nil {
		return ignoreHelp(err)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("openapi: unexpected argument %q", fs.Arg(0))
	}
	_, err := stdout.Write(apidoc.Spec())
	return err
}

// configPrint prints the effective configuration as redacted YAML. It
// prints what could be loaded even when validation fails, then fails.
func configPrint(args, environ []string, stdout io.Writer) error {
	fs := newFlagSet("config print", stdout)
	configFile := fs.String("config", "", configFlagUsage)
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
	info := versionInfo(version, commit, date)
	_, err := fmt.Fprintf(stdout, "xtandard-image %s (commit %s, built %s, %s, %s/%s)\n",
		info.Version, info.Commit, info.Date, info.GoVersion, runtime.GOOS, runtime.GOARCH)
	return err
}

// buildInfo is the resolved version metadata of the running binary.
type buildInfo struct {
	Version   string
	Commit    string
	Date      string
	GoVersion string
}

// versionInfo prefers link-time values (-ldflags -X, used by Docker builds
// that have no .git) and falls back to the module and VCS data embedded by
// the go command.
func versionInfo(ldVersion, ldCommit, ldDate string) buildInfo {
	bi, _ := debug.ReadBuildInfo()
	return resolveBuildInfo(ldVersion, ldCommit, ldDate, bi)
}

func resolveBuildInfo(ldVersion, ldCommit, ldDate string, bi *debug.BuildInfo) buildInfo {
	out := buildInfo{Version: ldVersion, Commit: ldCommit, Date: ldDate, GoVersion: runtime.Version()}
	if bi != nil {
		out.GoVersion = bi.GoVersion
		if out.Version == "" {
			out.Version = bi.Main.Version
		}
		var rev, vcsTime string
		var dirty bool
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				dirty = s.Value == "true"
			case "vcs.time":
				vcsTime = s.Value
			}
		}
		if out.Commit == "" {
			if rev != "" && dirty {
				rev += "-dirty"
			}
			out.Commit = rev
		}
		if out.Date == "" {
			out.Date = vcsTime
		}
	}
	if out.Version == "" {
		out.Version = "(devel)"
	}
	if out.Commit == "" {
		out.Commit = "unknown"
	}
	if out.Date == "" {
		out.Date = "unknown"
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
