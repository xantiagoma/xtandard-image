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
)

// Build metadata, injected at link time:
//
//	go build -ldflags "-X main.version=$VERSION -X main.commit=$SHA" ./cmd/xtandard-image
//
// When empty, versionInfo falls back to debug.ReadBuildInfo.
var (
	version string
	commit  string
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Environ(), os.Stdout)
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "xtandard-image: %v\n", err)
		os.Exit(1)
	}
}
