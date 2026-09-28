// Command xtandard-image is a self-hosted image processing proxy.
//
// main only wires process-level concerns (signals, args, environment, exit
// code) into run; everything else lives under internal/.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/xantiagoma/xtandard-image/internal/ghostscript"
)

// Build metadata, injected at link time:
//
//	go build -ldflags "-X main.version=$VERSION -X main.commit=$SHA -X main.date=$DATE" ./cmd/xtandard-image
//
// date is the RFC 3339 commit time (SOURCE_DATE_EPOCH), not the wall clock,
// so release builds stay reproducible. When empty, versionInfo falls back to
// debug.ReadBuildInfo.
var (
	version string
	commit  string
	date    string
)

func main() {
	// The Ghostscript sandbox helper is this binary re-executed: it
	// confines itself and execs gs, before anything else runs (ADR 0011).
	if ghostscript.IsSandboxHelper(os.Args) {
		os.Exit(ghostscript.RunSandboxHelper(os.Args))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Environ(), os.Stdout)
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "xtandard-image: %v\n", err)
	}
	if code := exitCode(err); code != 0 {
		os.Exit(code)
	}
}

// exitCode is the process status for run's error: 0 on success, 1 on any
// failure (config validate relies on it for CI).
func exitCode(err error) int {
	if err != nil {
		return 1
	}
	return 0
}
