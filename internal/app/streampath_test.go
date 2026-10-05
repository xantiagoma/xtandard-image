//go:build cgo

package app

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/playground"
	"github.com/xantiagoma/xtandard-image/internal/server"
)

// TestStreamPathAgrees: the playground rewrites the path under which the
// server serves the parts of streams.
func TestStreamPathAgrees(t *testing.T) {
	t.Parallel()
	if playground.StreamPath() != server.StreamPath {
		t.Fatalf("playground %q, server %q", playground.StreamPath(), server.StreamPath)
	}
}
