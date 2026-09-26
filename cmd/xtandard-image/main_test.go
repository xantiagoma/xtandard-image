package main

import (
	"errors"
	"os"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// TestMain holds one libvips reference for the whole package, so the
// serve tests' own Init/Shutdown pairs (reference-counted) never release
// the last one: vips_shutdown is final for the process, and -count=N would
// otherwise find libvips gone. Without cgo, Init reports ErrUnavailable and
// the serve tests skip.
func TestMain(m *testing.M) {
	if err := vips.Init(new(vips.DefaultConfig())); err != nil && !errors.Is(err, vips.ErrUnavailable) {
		_, _ = os.Stderr.WriteString("vips init: " + err.Error() + "\n")
		os.Exit(1)
	}
	code := m.Run()
	vips.Shutdown()
	os.Exit(code)
}
