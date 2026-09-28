// Package ghostscript renders EPS, PostScript and PostScript-based
// Illustrator pages with Ghostscript (`gs`) run out of process, one
// sandboxed subprocess per page (ADR 0011).
//
// It is pure Go (os/exec, no cgo). Ghostscript is AGPL-3.0 and only the
// gpl base image ships it; the feature is off unless
// XI_FORMATS_EPS_ENABLED=true. Each render:
//
//   - takes a slot of a dedicated semaphore (XI_FORMATS_EPS_MAX_PROCS; a
//     full one fails fast with errs.KindOverloaded) inside a request that
//     already holds a worker slot of the admission limiter;
//   - feeds the PostScript on stdin and reads one PNG page from stdout: no
//     input or output file, and a private per-render TMPDIR that is removed
//     afterwards;
//   - fixes the device to the page box at the requested resolution
//     (-g, -dFIXEDMEDIA), capped by XI_FORMATS_EPS_MAX_DPI and
//     XI_LIMITS_MAX_SRC_PIXELS, so a document cannot ask for a larger page;
//   - runs under -dSAFER (no file access outside the interpreter's own
//     resources, no %pipe%), in its own process group, killed as a group on
//     cancellation, after XI_FORMATS_EPS_TIMEOUT, or when its output
//     outgrows the page;
//   - on Linux, through a re-exec of this binary (the sandbox helper,
//     internal/sandbox, shared with the metafile converter since WP-108)
//     that sets resource limits, no-new-privileges, the parent-death signal
//     and an unprivileged user before it execs gs, in a new network
//     namespace when the kernel and the container's seccomp profile allow
//     one.
//
// Probing never runs Ghostscript: codec parses the DSC comments in Go.
package ghostscript

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/sandbox"
)

// Defaults of the XI_FORMATS_EPS_* settings (docs/CONFIG.md).
const (
	DefaultMaxProcs = 2
	DefaultTimeout  = 10 * time.Second
	DefaultMaxDPI   = 600.0
	// DefaultMaxPixels bounds a page when no pixel limit is configured
	// (XI_LIMITS_MAX_SRC_PIXELS' default).
	DefaultMaxPixels = 50_000_000
)

// stderrLimit bounds the stderr kept for an error message.
const stderrLimit = 4 << 10

// Isolation lists the layers a render runs under (internal/sandbox).
type Isolation = sandbox.Isolation

// IsSandboxHelper reports whether args (os.Args) start the sandbox helper
// (sandbox.IsHelper). main must then call RunSandboxHelper before anything
// else and exit with its status.
func IsSandboxHelper(args []string) bool { return sandbox.IsHelper(args) }

// RunSandboxHelper is sandbox.RunHelper.
func RunSandboxHelper(args []string) int { return sandbox.RunHelper(args) }

// pngMagic starts every PNG stream.
const pngMagic = "\x89PNG\r\n\x1a\n"

// Config configures a Runner. internal/app fills it from XI_FORMATS_EPS_*
// and XI_LIMITS_MAX_SRC_PIXELS.
type Config struct {
	// Path is the gs executable: an absolute path, or a name looked up in
	// PATH ("gs" when empty). New resolves it and fails when it is absent.
	Path string
	// MaxProcs bounds concurrent Ghostscript processes (≥ 1).
	MaxProcs int
	// Timeout bounds one render, wall clock (> 0); the CPU time limit of
	// the process follows it.
	Timeout time.Duration
	// MaxDPI caps the render resolution (> 0).
	MaxDPI float64
	// MaxPixels caps width × height of a rendered page (> 0): a larger
	// page renders at a lower resolution.
	MaxPixels int64
	// TempDir is the parent of the per-render TMPDIR ("" = os.TempDir()).
	TempDir string
	// Helper is the executable re-executed as the sandbox helper on Linux
	// ("" = this process's executable, which must call RunSandboxHelper
	// first thing in main; tests do it in TestMain).
	Helper string
	// NoNetNamespace skips the network-namespace attempt (tests).
	NoNetNamespace bool
}

func (c *Config) withDefaults() Config {
	out := *c
	if out.Path == "" {
		out.Path = "gs"
	}
	if out.MaxProcs < 1 {
		out.MaxProcs = DefaultMaxProcs
	}
	if out.Timeout <= 0 {
		out.Timeout = DefaultTimeout
	}
	if !(out.MaxDPI > 0) {
		out.MaxDPI = DefaultMaxDPI
	}
	if out.MaxPixels <= 0 {
		out.MaxPixels = DefaultMaxPixels
	}
	if out.TempDir == "" {
		out.TempDir = os.TempDir()
	}
	return out
}

// Runner renders pages with Ghostscript. It is safe for concurrent use.
type Runner struct {
	cfg     Config
	sem     *semaphore.Weighted
	sandbox *sandbox.Sandbox
	iso     Isolation
	version string
}

// New resolves the gs executable, runs it once through the sandbox
// (`gs --version`) and records which isolation layers hold. A missing or
// broken gs fails with an error naming it.
func New(ctx context.Context, cfg *Config) (*Runner, error) {
	c := cfg.withDefaults()
	path, err := exec.LookPath(c.Path)
	if err != nil {
		return nil, fmt.Errorf("ghostscript: gs executable %q not found: %w", c.Path, err)
	}
	c.Path = path
	sb, iso, version, err := sandbox.New(ctx, &sandbox.Config{
		Program: c.Path, Timeout: c.Timeout, TempDir: c.TempDir, Helper: c.Helper, NoNetNamespace: c.NoNetNamespace,
	})
	if err != nil {
		return nil, fmt.Errorf("ghostscript: %s does not run: %w", path, err)
	}
	return &Runner{cfg: c, sem: semaphore.NewWeighted(int64(c.MaxProcs)), sandbox: sb, iso: iso, version: version}, nil
}

// Version is the Ghostscript version (`gs --version`).
func (r *Runner) Version() string { return r.version }

// Path is the resolved gs executable.
func (r *Runner) Path() string { return r.cfg.Path }

// Isolation reports the isolation layers renders run under.
func (r *Runner) Isolation() Isolation { return r.iso }

// Page is what to render: one page of a document, in points.
type Page struct {
	// X and Y are the lower-left corner of the page box and Width and
	// Height its size, in points (1/72 inch): the document's bounding box
	// (its corner is moved to the origin of every page, the way
	// -dEPSCrop does for EPS files, but also for PostScript and Illustrator
	// files, which Ghostscript would not crop) or its media.
	X, Y, Width, Height float64
	// Number is the 1-based page to render (0 = 1).
	Number int
	// DPI is the resolution asked for (0 = 72); it is capped by
	// Config.MaxDPI and by Config.MaxPixels.
	DPI float64
}

// Raster is the geometry of a rendered page.
type Raster struct {
	Width, Height int
	DPI           float64
}

// geometry returns the device size and resolution for p: the requested
// resolution capped by MaxDPI, then lowered until the page fits MaxPixels.
func (c *Config) geometry(p *Page) (Raster, error) {
	if !(p.Width > 0 && p.Height > 0) || math.IsInf(p.Width, 0) || math.IsInf(p.Height, 0) {
		return Raster{}, errs.Newf(errs.KindCorrupt, "ghostscript: page of %gx%g points", p.Width, p.Height)
	}
	dpi := p.DPI
	if !(dpi > 0) {
		dpi = 72
	}
	dpi = min(dpi, c.MaxDPI)
	px := func(pts, dpi float64) int { return max(1, int(math.Ceil(pts*dpi/72-1e-6))) }
	w, h := px(p.Width, dpi), px(p.Height, dpi)
	for range 8 {
		if int64(w)*int64(h) <= c.MaxPixels {
			return Raster{Width: w, Height: h, DPI: dpi}, nil
		}
		dpi *= math.Sqrt(float64(c.MaxPixels)/(float64(w)*float64(h))) * 0.999
		w, h = px(p.Width, dpi), px(p.Height, dpi)
	}
	return Raster{}, errs.Newf(errs.KindLimitExceeded, "ghostscript: page of %gx%g points does not fit %d pixels",
		p.Width, p.Height, c.MaxPixels)
}

// outputLimit bounds the PNG of a w×h RGBA page: the raw samples, a filter
// byte per row, zlib's stored-block overhead and the chunk headers.
func outputLimit(w, h int) int64 {
	raw := int64(w) * int64(h) * 4
	return raw + raw/64 + int64(h)*8 + 64<<10
}

// args are the Ghostscript arguments for one page read from stdin.
func args(p *Page, g Raster) []string {
	page := strconv.Itoa(max(p.Number, 1))
	a := []string{
		"-q", "-dSAFER", "-dBATCH", "-dNOPAUSE", "-dNOPROMPT",
		// PostScript `print` output would corrupt the PNG on stdout.
		"-sstdout=%stderr",
		"-sDEVICE=pngalpha", "-dTextAlphaBits=4", "-dGraphicsAlphaBits=4",
		"-r" + strconv.FormatFloat(g.DPI, 'f', 4, 64),
		"-g" + strconv.Itoa(g.Width) + "x" + strconv.Itoa(g.Height),
		// The document cannot change the page size: larger requests are
		// clipped, not rendered.
		"-dFIXEDMEDIA",
		"-dFirstPage=" + page, "-dLastPage=" + page,
		// Keep a banded page list in memory instead of temporary files.
		"-sBandListStorage=memory",
	}
	a = append(a, "-sOutputFile=-")
	if p.X != 0 || p.Y != 0 {
		// BeginPage runs after each page's initgraphics, so every page is
		// shifted, not only the first.
		a = append(a, "-c", "<< /BeginPage { pop "+psNumber(-p.X)+" "+psNumber(-p.Y)+" translate } >> setpagedevice", "-f")
	}
	return append(a, "-")
}

// psNumber formats v as a PostScript number (no exponent).
func psNumber(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// ErrOverloaded is the cause of a render refused because
// XI_FORMATS_EPS_MAX_PROCS renders are running.
var ErrOverloaded = errors.New("ghostscript: all ghostscript processes busy")

// Render renders one page of the PostScript program ps and hands the PNG
// Ghostscript writes to read, which must consume it and not keep r past
// its return (vips.NewBufferFromReader copies it into C memory). limit is
// the most bytes read may expect; more is an error of Render's own.
//
// Errors: errs.KindOverloaded when every process slot is busy,
// errs.KindTimeout after Config.Timeout (or ctx's deadline),
// errs.KindCanceled when ctx is canceled, errs.KindLimitExceeded when the
// output outgrows the page or the interpreter runs out of memory, and
// errs.KindCorrupt when Ghostscript fails (a PostScript error, a -dSAFER
// refusal) or writes no PNG. The process group is gone when Render
// returns.
func (r *Runner) Render(ctx context.Context, ps []byte, p *Page, read func(r io.Reader, limit int64) error) (Raster, error) {
	g, err := r.cfg.geometry(p)
	if err != nil {
		return Raster{}, err
	}
	if err := ctxErr(ctx); err != nil {
		return Raster{}, err
	}
	if !r.sem.TryAcquire(1) {
		return Raster{}, errs.Wrap(errs.KindOverloaded, ErrOverloaded)
	}
	defer r.sem.Release(1)

	tmp, err := os.MkdirTemp(r.cfg.TempDir, "xi-gs-")
	if err != nil {
		return Raster{}, errs.Wrapf(errs.KindInternal, err, "ghostscript: temp dir")
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	rctx, cancel := context.WithTimeoutCause(ctx, r.cfg.Timeout, errRenderTimeout)
	defer cancel()
	// Pdeathsig follows the thread that forked the child: keep it alive
	// (and this goroutine on it) until the child is reaped.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	limit := outputLimit(g.Width, g.Height)
	cmd, err := r.sandbox.Command(rctx, tmp, limit, args(p, g))
	if err != nil {
		return Raster{}, errs.Wrapf(errs.KindInternal, err, "ghostscript: sandbox")
	}
	cmd.Stdin = bytes.NewReader(ps)
	stderr := &cappedBuffer{max: stderrLimit}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Raster{}, errs.Wrapf(errs.KindInternal, err, "ghostscript: stdout")
	}
	if err := cmd.Start(); err != nil {
		return Raster{}, errs.Wrapf(errs.KindInternal, err, "ghostscript: start %s", r.cfg.Path)
	}
	pgid := cmd.Process.Pid
	out := &pngReader{r: stdout, left: limit}
	readErr := read(out, limit+1)
	if readErr != nil {
		sandbox.KillGroup(pgid)
	} else if _, err := io.Copy(io.Discard, out); err != nil {
		sandbox.KillGroup(pgid)
	}
	waitErr := cmd.Wait()
	// Anything the process left in its group goes with it.
	sandbox.KillGroup(pgid)

	switch {
	case ctx.Err() != nil:
		return Raster{}, ctxErr(ctx)
	case errors.Is(context.Cause(rctx), errRenderTimeout):
		return Raster{}, errs.WithPublic(errs.Wrapf(errs.KindTimeout, errRenderTimeout,
			"ghostscript: no page after %s", r.cfg.Timeout), "EPS rendering timed out")
	case out.overflow:
		return Raster{}, errs.Newf(errs.KindLimitExceeded, "ghostscript: output over %d bytes for a %dx%d page",
			limit, g.Width, g.Height)
	case waitErr != nil:
		return Raster{}, exitError(waitErr, stderr.String())
	case readErr != nil:
		return Raster{}, errs.Wrapf(errs.KindCorrupt, readErr, "ghostscript: read page")
	case !out.png:
		return Raster{}, errs.Newf(errs.KindCorrupt, "ghostscript: no PNG on stdout (%s)", stderr.String())
	}
	return g, nil
}

// errRenderTimeout is the cause of the per-render deadline.
var errRenderTimeout = errors.New("ghostscript: render timeout")

// exitError classifies a failed Ghostscript run from its stderr.
func exitError(err error, stderr string) error {
	msg := strings.TrimSpace(stderr)
	if len(msg) > 512 {
		msg = msg[:512]
	}
	kind := errs.KindCorrupt
	if strings.Contains(msg, "VMerror") || strings.Contains(msg, "limitcheck") {
		kind = errs.KindLimitExceeded
	}
	return errs.Wrapf(kind, err, "ghostscript: %s", msg)
}

// ctxErr maps a finished ctx to its errs kind.
func ctxErr(ctx context.Context) error {
	err := ctx.Err()
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.DeadlineExceeded):
		return errs.Wrap(errs.KindTimeout, context.Cause(ctx))
	default:
		return errs.Wrap(errs.KindCanceled, context.Cause(ctx))
	}
}

// pngReader passes stdout through, checks the PNG signature and fails
// once more than left bytes arrive.
type pngReader struct {
	r        io.Reader
	left     int64
	head     []byte
	png      bool
	overflow bool
}

var errOverflow = errors.New("ghostscript: output too large")

func (p *pngReader) Read(b []byte) (int, error) {
	if p.overflow {
		return 0, errOverflow
	}
	n, err := p.r.Read(b)
	if int64(n) > p.left {
		p.overflow = true
		return 0, errOverflow
	}
	p.left -= int64(n)
	if len(p.head) < len(pngMagic) {
		p.head = append(p.head, b[:min(n, len(pngMagic)-len(p.head))]...)
		p.png = string(p.head) == pngMagic
	}
	return n, err
}

// cappedBuffer keeps the first max bytes written to it.
type cappedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (c *cappedBuffer) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if room := c.max - c.buf.Len(); room > 0 {
		c.buf.Write(b[:min(len(b), room)])
	}
	return len(b), nil
}

func (c *cappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}
