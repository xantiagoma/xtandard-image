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
	"time"
)

var errUnknownCommand = errors.New("unknown command")

const usageText = `usage: xtandard-image <command> [flags]

commands:
  serve     start the image proxy (default)
  health    probe a running instance's health endpoint
  version   print version information
`

// run is the testable entry point: it owns no process state and receives
// everything it needs (args without argv[0], environment lookup, output).
func run(ctx context.Context, args []string, getenv func(string) string, stdout io.Writer) error {
	cmd := "serve"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		return serve(ctx, args, getenv, stdout)
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

// serve is a stub until the server and app packages land (WP-00). It logs
// and blocks until ctx is cancelled (SIGINT/SIGTERM), then exits cleanly.
func serve(ctx context.Context, args []string, getenv func(string) string, stdout io.Writer) error {
	fs := newFlagSet("serve", stdout)
	if err := fs.Parse(args); err != nil {
		return ignoreHelp(err)
	}
	_ = getenv // handed to internal/config once it exists (WP-03)

	logger := slog.New(slog.NewJSONHandler(stdout, nil))
	info := versionInfo(version, commit)
	logger.LogAttrs(ctx, slog.LevelWarn, "serve is a stub; waiting for shutdown signal",
		slog.String("version", info.Version), slog.String("commit", info.Commit))
	<-ctx.Done()
	logger.LogAttrs(ctx, slog.LevelInfo, "shutting down",
		slog.String("cause", context.Cause(ctx).Error()))
	return nil
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
