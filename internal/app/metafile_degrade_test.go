//go:build cgo

package app

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/sandbox"
)

// brokenConverter writes a converter that is found but fails its startup
// self-check the way the gpl image's did under Rosetta with a 1 GiB
// RLIMIT_AS (WP-118).
func brokenConverter(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, d := range []string{dir, filepath.Dir(dir)} {
		if err := os.Chmod(d, 0o755); err != nil { //nolint:gosec // G302: a test directory the sandbox user must traverse
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "xi-metafile2svg")
	script := "#!/bin/sh\necho 'rosetta error: mmap_anonymous_rw mmap failed' >&2\nexit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil { //nolint:gosec // G306: the fake converter must be executable
		t.Fatal(err)
	}
	return path
}

// TestMetafileAutoDegrades: with XI_FORMATS_METAFILE_ENABLED=auto a
// converter that fails its self-check never stops startup; the server warns
// and answers WMF/EMF with the 422 of an image without the converter. Only
// an explicit true is fatal.
func TestMetafileAutoDegrades(t *testing.T) {
	t.Parallel()
	converter := brokenConverter(t)

	t.Run("auto", func(t *testing.T) {
		t.Parallel()
		cfg := designerConfig(t, t.TempDir())
		cfg.Formats.Metafile.Enabled, cfg.Formats.Metafile.Path = config.Auto, converter
		var logs lockedBuffer
		a, _ := startLogged(t, cfg, hooks{}, slog.New(slog.NewJSONHandler(&logs, nil)))
		base := "http://" + a.public.String()
		for _, p := range []string{"wmf/shapes.wmf", "emf/shapes.emf"} {
			r := get(t, base+"/insecure/plain/"+p+"@png")
			if r.status != http.StatusUnprocessableEntity || !strings.Contains(string(r.body), "gpl image") {
				t.Errorf("%s: %d %s, want 422 naming the gpl image", p, r.status, r.body)
			}
			if strings.Contains(string(r.body), "rosetta") {
				t.Errorf("%s: the converter's error reached the client: %s", p, r.body)
			}
		}
		out := logs.String()
		if !strings.Contains(out, `"level":"WARN","msg":"metafile conversion unavailable"`) ||
			!strings.Contains(out, "mmap_anonymous_rw") {
			t.Errorf("no warning naming the failed check in the log:\n%s", out)
		}
	})

	t.Run("true", func(t *testing.T) {
		t.Parallel()
		cfg := designerConfig(t, t.TempDir())
		cfg.Formats.Metafile.Enabled, cfg.Formats.Metafile.Path = config.True, converter
		mf, err := buildMetafile(t.Context(), cfg, slog.New(slog.DiscardHandler))
		if err == nil || mf != nil {
			t.Fatalf("buildMetafile = %v, %v; want a startup error", mf, err)
		}
		if !strings.Contains(err.Error(), "XI_FORMATS_METAFILE_ENABLED") {
			t.Errorf("error %q does not name the setting", err)
		}
	})
}

// TestSandboxAddressSpace maps XI_SANDBOX_* onto the sandbox settings.
func TestSandboxAddressSpace(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		maxAS  config.ByteSize
		detect bool
		want   sandbox.AddressSpace
	}{
		{"default", 1 << 30, true, sandbox.AddressSpace{Base: 1 << 30}},
		{"off", 0, true, sandbox.AddressSpace{Off: true}},
		{"kept under emulation", 2 << 30, false, sandbox.AddressSpace{Base: 2 << 30, KeepUnderEmulation: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Defaults()
			cfg.Sandbox.MaxAS, cfg.Sandbox.DetectEmulation = tc.maxAS, tc.detect
			if diff := cmp.Diff(tc.want, sandboxAddressSpace(&cfg)); diff != "" {
				t.Errorf("sandboxAddressSpace (-want +got):\n%s", diff)
			}
		})
	}
}
