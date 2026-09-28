package ghostscript_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/ghostscript"
)

// TestMain doubles as the sandbox helper: on Linux the runner re-executes
// the test binary with the helper argument, as the server re-executes
// itself.
func TestMain(m *testing.M) {
	if ghostscript.IsSandboxHelper(os.Args) {
		os.Exit(ghostscript.RunSandboxHelper(os.Args))
	}
	os.Exit(m.Run())
}

// openDir returns a t.TempDir() that other users can traverse: when the
// tests run as root the sandbox drops to nobody, which must still reach a
// fake gs script and its TMPDIR.
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

// fakeGS writes a shell script that answers `--version` and otherwise runs
// body. The sandbox forbids fork (RLIMIT_NPROC) and file writes
// (RLIMIT_FSIZE), so bodies use shell builtins and exec only.
func fakeGS(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(openDir(t), "gs")
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 10.99.0; exit 0; fi\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil { //nolint:gosec // G306: the fake gs must be executable
		t.Fatal(err)
	}
	return path
}

// newRunner builds a runner on gs with a temp dir the test inspects
// afterwards.
func newRunner(t *testing.T, gs string, mod func(*ghostscript.Config)) (*ghostscript.Runner, string) {
	t.Helper()
	tmp := openDir(t)
	cfg := ghostscript.Config{Path: gs, MaxProcs: 2, Timeout: 5 * time.Second, TempDir: tmp, NoNetNamespace: true}
	if mod != nil {
		mod(&cfg)
	}
	r, err := ghostscript.New(t.Context(), &cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r, tmp
}

// collect reads everything gs writes, up to limit.
type collect struct{ out []byte }

func (c *collect) read(r io.Reader, limit int64) error {
	var err error
	c.out, err = io.ReadAll(io.LimitReader(r, limit))
	return err
}

// pids returns the "pid N" and "child N" lines a fake script printed.
func pids(out []byte) []int {
	var ps []int
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && (f[0] == "pid" || f[0] == "child") {
			if n, err := strconv.Atoi(f[1]); err == nil {
				ps = append(ps, n)
			}
		}
	}
	return ps
}

// assertGone fails when a process in pids still exists or the temp dir
// is not empty.
func assertGone(t *testing.T, ps []int, tmp string) {
	t.Helper()
	for _, pid := range ps {
		if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
			t.Errorf("process %d still exists after Render (kill 0: %v)", pid, err)
		}
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("temp dir not removed: %s", e.Name())
	}
}

const pngHead = `printf '\211PNG\r\n\032\n'`

func TestRenderFake(t *testing.T) {
	t.Parallel()
	page := &ghostscript.Page{Width: 10, Height: 10}
	tests := []struct {
		name    string
		body    string
		timeout time.Duration
		want    errs.Kind // KindUnknown: success
	}{
		{"png", pngHead + "; printf 'IHDR'", 0, errs.KindUnknown},
		{"not png", "printf 'GIF89a'", 0, errs.KindCorrupt},
		{"postscript error", "echo 'Error: /undefined in foo' >&2; exit 1", 0, errs.KindCorrupt},
		{"vm error", "echo 'Error: /VMerror in --string--' >&2; exit 1", 0, errs.KindLimitExceeded},
		{"killed", "echo pid $$; kill -9 $$", 0, errs.KindCorrupt},
		{"timeout", "echo pid $$; exec sleep 30", 300 * time.Millisecond, errs.KindTimeout},
		{"oversized", "echo pid $$; " + pngHead + "; while :; do printf '%s' 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef; done", 0, errs.KindLimitExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r, tmp := newRunner(t, fakeGS(t, tt.body), func(c *ghostscript.Config) {
				if tt.timeout > 0 {
					c.Timeout = tt.timeout
				}
			})
			var c collect
			g, err := r.Render(t.Context(), []byte("%!PS\n"), page, c.read)
			if tt.want == errs.KindUnknown {
				if err != nil {
					t.Fatalf("Render: %v", err)
				}
				if diff := cmp.Diff(ghostscript.Raster{Width: 10, Height: 10, DPI: 72}, g); diff != "" {
					t.Errorf("raster (-want +got):\n%s", diff)
				}
			} else if got := errs.KindOf(err); got != tt.want {
				t.Fatalf("Render error kind = %s (%v), want %s", got, err, tt.want)
			}
			assertGone(t, pids(c.out), tmp)
		})
	}
}

// TestRenderFork: gs may not start processes. On Linux the sandbox's
// RLIMIT_NPROC makes fork fail (the shell gives up: a failed render);
// elsewhere the child is started and killed with the process group when
// the render times out.
func TestRenderFork(t *testing.T) {
	t.Parallel()
	r, tmp := newRunner(t, fakeGS(t, "echo pid $$; sleep 30 & echo child $!; exec sleep 30"),
		func(c *ghostscript.Config) { c.Timeout = 300 * time.Millisecond })
	var c collect
	_, err := r.Render(t.Context(), []byte("%!PS\n"), &ghostscript.Page{Width: 10, Height: 10}, c.read)
	want := errs.KindTimeout
	if runtime.GOOS == "linux" {
		want = errs.KindCorrupt
		if strings.Contains(string(c.out), "child") {
			t.Errorf("the sandbox let gs fork: %q", c.out)
		}
	}
	if got := errs.KindOf(err); got != want {
		t.Errorf("Render error kind = %s (%v), want %s", got, err, want)
	}
	assertGone(t, pids(c.out), tmp)
}

func TestRenderCancel(t *testing.T) {
	t.Parallel()
	r, tmp := newRunner(t, fakeGS(t, "echo pid $$; exec sleep 30"), nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var out []byte
	_, err := r.Render(ctx, []byte("%!PS\n"), &ghostscript.Page{Width: 10, Height: 10}, func(rd io.Reader, _ int64) error {
		line, err := bufio.NewReader(rd).ReadString('\n')
		out = append(out, line...)
		if err != nil {
			return err
		}
		cancel() // the client went away while gs runs
		rest, err := io.ReadAll(rd)
		out = append(out, rest...)
		return err
	})
	if got := errs.KindOf(err); got != errs.KindCanceled {
		t.Fatalf("Render error kind = %s (%v), want canceled", got, err)
	}
	if len(pids(out)) == 0 {
		t.Fatalf("fake gs printed no pid: %q", out)
	}
	assertGone(t, pids(out), tmp)
}

func TestRenderOverloaded(t *testing.T) {
	t.Parallel()
	r, tmp := newRunner(t, fakeGS(t, "echo pid $$; exec sleep 30"), func(c *ghostscript.Config) { c.MaxProcs = 1 })
	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})
	done := make(chan []byte)
	go func() {
		var out []byte
		_, _ = r.Render(ctx, []byte("%!PS\n"), &ghostscript.Page{Width: 10, Height: 10}, func(rd io.Reader, _ int64) error {
			line, _ := bufio.NewReader(rd).ReadString('\n')
			out = append(out, line...)
			close(started)
			_, err := io.Copy(io.Discard, rd)
			return err
		})
		done <- out
	}()
	<-started
	_, err := r.Render(t.Context(), []byte("%!PS\n"), &ghostscript.Page{Width: 10, Height: 10}, (&collect{}).read)
	if got := errs.KindOf(err); got != errs.KindOverloaded {
		t.Errorf("second Render error kind = %s (%v), want overloaded", got, err)
	}
	cancel()
	assertGone(t, pids(<-done), tmp)
}

func TestNewMissingGS(t *testing.T) {
	t.Parallel()
	_, err := ghostscript.New(t.Context(), &ghostscript.Config{Path: filepath.Join(t.TempDir(), "no-gs")})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("New with a missing gs: %v, want a not found error", err)
	}
}

func TestIsolationReported(t *testing.T) {
	t.Parallel()
	r, _ := newRunner(t, fakeGS(t, "exit 0"), nil)
	iso := r.Isolation()
	if r.Version() != "10.99.0" {
		t.Errorf("Version = %q, want 10.99.0", r.Version())
	}
	if !iso.ProcessGroup {
		t.Error("process group not reported")
	}
	if runtime.GOOS != "linux" {
		return
	}
	if !iso.Helper || !iso.NoNewPrivs || !iso.ParentDeathSignal || !iso.Rlimits {
		t.Errorf("isolation %+v: want helper, no_new_privs, parent death signal and rlimits", iso)
	}
	if iso.UID == 0 {
		t.Errorf("gs would run as root: %+v", iso)
	}
}

func TestGeometry(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		page  ghostscript.Page
		cfg   ghostscript.Config
		want  ghostscript.Raster
		wantK errs.Kind
	}{
		{"natural size", ghostscript.Page{Width: 100, Height: 50}, ghostscript.Config{}, ghostscript.Raster{Width: 100, Height: 50, DPI: 72}, 0},
		{"double", ghostscript.Page{Width: 100, Height: 50, DPI: 144}, ghostscript.Config{}, ghostscript.Raster{Width: 200, Height: 100, DPI: 144}, 0},
		{"fraction rounds up", ghostscript.Page{Width: 100.2, Height: 50, DPI: 72}, ghostscript.Config{}, ghostscript.Raster{Width: 101, Height: 50, DPI: 72}, 0},
		{"max dpi", ghostscript.Page{Width: 72, Height: 72, DPI: 5000}, ghostscript.Config{MaxDPI: 300}, ghostscript.Raster{Width: 300, Height: 300, DPI: 300}, 0},
		{"no page", ghostscript.Page{}, ghostscript.Config{}, ghostscript.Raster{}, errs.KindCorrupt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ghostscript.Geometry(&tt.cfg, &tt.page)
			if tt.wantK != 0 {
				if errs.KindOf(err) != tt.wantK {
					t.Fatalf("Geometry error %v, want kind %s", err, tt.wantK)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Geometry (-want +got):\n%s", diff)
			}
		})
	}
	// A huge page renders at a lower resolution within MaxPixels.
	g, err := ghostscript.Geometry(&ghostscript.Config{MaxPixels: 1_000_000}, &ghostscript.Page{Width: 20000, Height: 10000, DPI: 600})
	if err != nil {
		t.Fatal(err)
	}
	if px := int64(g.Width) * int64(g.Height); px > 1_000_000 || px < 900_000 {
		t.Errorf("huge page: %dx%d = %d px at %g dpi, want just under 1 MP", g.Width, g.Height, px, g.DPI)
	}
}

func TestArgs(t *testing.T) {
	t.Parallel()
	r := ghostscript.Raster{Width: 10, Height: 20, DPI: 72}
	a := strings.Join(ghostscript.Args(&ghostscript.Page{Width: 10, Height: 20, Number: 3}, r), " ")
	for _, want := range []string{"-dSAFER", "-dBATCH", "-dNOPAUSE", "-dFIXEDMEDIA", "-g10x20", "-dFirstPage=3", "-dLastPage=3", "-sDEVICE=pngalpha", "-sOutputFile=- -"} {
		if !strings.Contains(a, want) {
			t.Errorf("args %q lack %q", a, want)
		}
	}
	if strings.Contains(a, "NOSAFER") || strings.Contains(a, "BeginPage") {
		t.Errorf("args %q contain NOSAFER or an origin shift", a)
	}
	shifted := strings.Join(ghostscript.Args(&ghostscript.Page{X: 10.5, Y: -20, Width: 10, Height: 20}, r), " ")
	if want := "-sOutputFile=- -c << /BeginPage { pop -10.5 20 translate } >> setpagedevice -f -"; !strings.HasSuffix(shifted, want) {
		t.Errorf("args %q, want suffix %q", shifted, want)
	}
}
