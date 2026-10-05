package metafile_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/metafile"
	"github.com/xantiagoma/xtandard-image/internal/sandbox"
	"github.com/xantiagoma/xtandard-image/internal/testutil/execscript"
)

// TestMain doubles as the sandbox helper: on Linux the converter re-executes
// the test binary with the helper argument, as the server re-executes
// itself.
func TestMain(m *testing.M) {
	if sandbox.IsHelper(os.Args) {
		os.Exit(sandbox.RunHelper(os.Args))
	}
	os.Exit(m.Run())
}

// openDir returns a t.TempDir() that other users can traverse: when the
// tests run as root the sandbox drops to nobody.
func openDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, d := range []string{dir, filepath.Dir(dir)} {
		if err := os.Chmod(d, 0o755); err != nil { //nolint:gosec // G302: a test directory the sandbox user must traverse
			t.Fatal(err)
		}
	}
	return dir
}

// fakeConverter writes a shell script that answers --version and otherwise
// runs body (shell builtins and exec only: the sandbox forbids fork and
// file writes).
func fakeConverter(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(openDir(t), "xi-metafile2svg")
	execscript.Write(t, path, "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'xi-metafile2svg test'; exit 0; fi\n"+body+"\n")
	return path
}

func newConverter(t *testing.T, path string, mod func(*metafile.Config)) *metafile.Converter {
	t.Helper()
	cfg := metafile.Config{Path: path, MaxProcs: 2, Timeout: 5 * time.Second, TempDir: openDir(t), NoNetNamespace: true}
	if mod != nil {
		mod(&cfg)
	}
	c, err := metafile.New(t.Context(), &cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

const svgDoc = `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"/>`

func TestToSVG(t *testing.T) {
	t.Parallel()
	// The fake echoes its arguments into the document so the test sees
	// them, and checks that the input arrived on stdin.
	c := newConverter(t, fakeConverter(t, `read -r line; printf '<svg xmlns="http://www.w3.org/2000/svg" data-args="%s %s %s" data-in="%s"/>' "$1" "$2" "$3" "$line"`),
		func(cfg *metafile.Config) { cfg.MaxBytes, cfg.MaxSide = 1000, 512 })
	if c.Version() != "xi-metafile2svg test" {
		t.Errorf("Version = %q", c.Version())
	}
	for _, kind := range []string{"wmf", "emf"} {
		svg, err := c.ToSVG(t.Context(), kind, []byte("metafile-bytes\n"))
		if err != nil {
			t.Fatalf("ToSVG(%s): %v", kind, err)
		}
		if want := `data-args="` + kind + ` 1000 512" data-in="metafile-bytes"`; !strings.Contains(string(svg), want) {
			t.Errorf("ToSVG(%s) = %s, want %s", kind, svg, want)
		}
	}
}

func TestToSVGErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		body string
		mod  func(*metafile.Config)
		kind errs.Kind
		in   string
	}{
		{"refused", `echo 'conversion failed' >&2; exit 1`, nil, errs.KindCorrupt, "x"},
		{"no svg", `echo 'hello'`, nil, errs.KindCorrupt, "x"},
		{
			"output too large", `printf '%s' '` + svgDoc + `'; while :; do printf 'xxxxxxxxxxxxxxxx'; done`,
			func(c *metafile.Config) { c.MaxOutput = 1 << 12 }, errs.KindLimitExceeded, "x",
		},
		{"timeout", `exec sleep 30`, func(c *metafile.Config) { c.Timeout = 300 * time.Millisecond }, errs.KindTimeout, "x"},
		{"input too large", `printf '%s' '` + svgDoc + `'`, func(c *metafile.Config) { c.MaxBytes = 4 }, errs.KindLimitExceeded, "12345"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := newConverter(t, fakeConverter(t, tc.body), tc.mod)
			_, err := c.ToSVG(t.Context(), "wmf", []byte(tc.in))
			if errs.KindOf(err) != tc.kind {
				t.Fatalf("ToSVG: %v (kind %v), want %v", err, errs.KindOf(err), tc.kind)
			}
		})
	}
	c := newConverter(t, fakeConverter(t, `printf '%s' '`+svgDoc+`'`), nil)
	if _, err := c.ToSVG(t.Context(), "pict", []byte("x")); errs.KindOf(err) != errs.KindInternal {
		t.Errorf("unknown kind: %v", err)
	}
}

// TestToSVGOverloaded: a conversion beyond MaxProcs fails fast.
func TestToSVGOverloaded(t *testing.T) {
	t.Parallel()
	// The first conversion holds its slot until the test removes the flag
	// file the fake waits on (shell builtins only).
	gate := filepath.Join(openDir(t), "gate")
	if err := os.WriteFile(gate, nil, 0o644); err != nil { //nolint:gosec // G306: read by the sandbox user
		t.Fatal(err)
	}
	c := newConverter(t, fakeConverter(t, `while [ -e `+gate+` ]; do :; done; printf '%s' '`+svgDoc+`'`),
		func(cfg *metafile.Config) { cfg.MaxProcs = 1 })
	// Two conversions at once: whichever takes the slot waits on the gate,
	// so the other one returns first, and it must have been refused.
	done := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := c.ToSVG(t.Context(), "emf", []byte("x"))
			done <- err
		}()
	}
	if err := <-done; errs.KindOf(err) != errs.KindOverloaded {
		t.Errorf("first to return: %v, want overloaded", err)
	}
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Errorf("the conversion holding the slot: %v", err)
	}
}

func TestNewErrors(t *testing.T) {
	t.Parallel()
	if _, err := metafile.New(t.Context(), &metafile.Config{Path: filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Error("New with a missing converter succeeded")
	}
	broken := fakeConverter(t, "")
	execscript.Write(t, broken, "#!/bin/sh\nexit 3\n")
	if _, err := metafile.New(t.Context(), &metafile.Config{Path: broken, NoNetNamespace: true}); err == nil {
		t.Error("New with a converter failing --version succeeded")
	}
}

// TestRealConverter converts the WMF and EMF fixtures with the gpl image's
// xi-metafile2svg; skipped where it is not installed.
func TestRealConverter(t *testing.T) {
	t.Parallel()
	path, err := exec.LookPath("xi-metafile2svg")
	if err != nil {
		t.Skip("xi-metafile2svg not installed (only the gpl image ships it)")
	}
	c := newConverter(t, path, nil)
	for _, tc := range []struct{ fixture, kind string }{
		{"wmf/shapes.wmf", "wmf"},
		{"emf/shapes.emf", "emf"},
	} {
		data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", tc.fixture))
		if err != nil {
			t.Fatal(err)
		}
		svg, err := c.ToSVG(t.Context(), tc.kind, data)
		if err != nil {
			t.Fatalf("ToSVG(%s): %v", tc.fixture, err)
		}
		if !strings.Contains(string(svg), "<svg") || len(svg) < 200 {
			t.Errorf("ToSVG(%s) = %d bytes: %.200s", tc.fixture, len(svg), svg)
		}
	}
	if _, err := c.ToSVG(t.Context(), "emf", []byte("not an emf at all, not even close")); errs.KindOf(err) != errs.KindCorrupt {
		t.Errorf("garbage EMF: %v, want corrupt", err)
	}
}
