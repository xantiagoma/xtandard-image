// Package metafile converts Windows metafiles (WMF, EMF) to SVG with
// xi-metafile2svg, the program the gpl base image ships (WP-108): libwmf
// for WMF and libemf2svg for EMF (and EMF+), GPL code run out of process,
// one sandboxed subprocess per source (internal/sandbox, the Ghostscript
// sandbox of ADR 0011). The server replaces the metafile by the SVG before
// anything else parses it; the SVG then goes through the SVG sanitiser and
// librsvg like any other SVG, so it is rendered as vectors at the size a
// request needs.
//
// It is pure Go (os/exec, no cgo). Each conversion:
//
//   - takes a slot of a dedicated semaphore (XI_FORMATS_METAFILE_MAX_PROCS;
//     a full one fails fast with errs.KindOverloaded);
//   - feeds the metafile on stdin and reads one SVG document from stdout,
//     at most Config.MaxOutput bytes: no input or output file, and a
//     private TMPDIR removed afterwards;
//   - bounds the drawing's larger side by Config.MaxSide pixels (the
//     converter scales anything larger down);
//   - runs in its own process group, killed as a group on cancellation or
//     after XI_FORMATS_METAFILE_TIMEOUT, under the sandbox's resource
//     limits, no-new-privileges, the parent-death signal and an
//     unprivileged user (Linux).
package metafile

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/sandbox"
)

// Defaults of the XI_FORMATS_METAFILE_* settings (docs/CONFIG.md).
const (
	DefaultPath     = "xi-metafile2svg"
	DefaultMaxProcs = 2
	DefaultTimeout  = 10 * time.Second
	// DefaultMaxBytes bounds the metafile read by the converter.
	DefaultMaxBytes = 64 << 20
	// DefaultMaxSide bounds the SVG's larger side, in pixels.
	DefaultMaxSide = 16384
	// DefaultMaxOutput bounds the SVG document.
	DefaultMaxOutput = 64 << 20
)

// stderrLimit bounds the stderr kept for an error message.
const stderrLimit = 4 << 10

// Kind is the metafile format to convert.
type Kind string

// Kinds.
const (
	WMF Kind = "wmf"
	EMF Kind = "emf"
)

// Config configures a Converter. internal/app fills it from
// XI_FORMATS_METAFILE_* and XI_LIMITS_MAX_SRC_BYTES.
type Config struct {
	// Path is the converter: an absolute path, or a name looked up in PATH
	// (DefaultPath when empty). New resolves it and fails when it is absent.
	Path string
	// MaxProcs bounds concurrent conversions (≥ 1).
	MaxProcs int
	// Timeout bounds one conversion, wall clock (> 0).
	Timeout time.Duration
	// MaxBytes bounds the input (> 0).
	MaxBytes int64
	// MaxSide bounds the SVG's larger side in pixels (1–65535).
	MaxSide int
	// MaxOutput bounds the SVG document (> 0).
	MaxOutput int64
	// TempDir is the parent of the per-conversion TMPDIR ("" =
	// os.TempDir()).
	TempDir string
	// Helper and NoNetNamespace are sandbox.Config's (tests).
	Helper         string
	NoNetNamespace bool
	// AddressSpace configures the sandbox's RLIMIT_AS (XI_SANDBOX_*).
	AddressSpace sandbox.AddressSpace
}

func (c *Config) withDefaults() Config {
	out := *c
	if out.Path == "" {
		out.Path = DefaultPath
	}
	if out.MaxProcs < 1 {
		out.MaxProcs = DefaultMaxProcs
	}
	if out.Timeout <= 0 {
		out.Timeout = DefaultTimeout
	}
	if out.MaxBytes <= 0 {
		out.MaxBytes = DefaultMaxBytes
	}
	if out.MaxSide <= 0 || out.MaxSide > 65535 {
		out.MaxSide = DefaultMaxSide
	}
	if out.MaxOutput <= 0 {
		out.MaxOutput = DefaultMaxOutput
	}
	if out.TempDir == "" {
		out.TempDir = os.TempDir()
	}
	return out
}

// Converter runs xi-metafile2svg. It is safe for concurrent use.
type Converter struct {
	cfg     Config
	sem     *semaphore.Weighted
	sandbox *sandbox.Sandbox
	iso     sandbox.Isolation
	version string
}

// New resolves the converter, runs it once through the sandbox
// (`xi-metafile2svg --version`) and records which isolation layers hold.
// A missing or broken converter fails with an error naming it.
func New(ctx context.Context, cfg *Config) (*Converter, error) {
	c := cfg.withDefaults()
	path, err := exec.LookPath(c.Path)
	if err != nil {
		return nil, fmt.Errorf("metafile: converter %q not found: %w", c.Path, err)
	}
	c.Path = path
	sb, iso, version, err := sandbox.New(ctx, &sandbox.Config{
		Program: path, Timeout: c.Timeout, TempDir: c.TempDir, Helper: c.Helper, NoNetNamespace: c.NoNetNamespace,
		AddressSpace: c.AddressSpace,
	})
	if err != nil {
		return nil, fmt.Errorf("metafile: %s does not run: %w", path, err)
	}
	return &Converter{cfg: c, sem: semaphore.NewWeighted(int64(c.MaxProcs)), sandbox: sb, iso: iso, version: version}, nil
}

// Version is the converter's --version line.
func (c *Converter) Version() string { return c.version }

// Path is the resolved converter.
func (c *Converter) Path() string { return c.cfg.Path }

// Isolation reports the isolation layers conversions run under.
func (c *Converter) Isolation() sandbox.Isolation { return c.iso }

// ErrOverloaded is the cause of a conversion refused because
// XI_FORMATS_METAFILE_MAX_PROCS conversions are running.
var ErrOverloaded = errors.New("metafile: all converter processes busy")

// errTimeout is the cause of the per-conversion deadline.
var errTimeout = errors.New("metafile: conversion timeout")

// ToSVG converts the metafile data of the given kind ("wmf" or "emf") to
// an SVG document.
//
// Errors: errs.KindOverloaded when every process slot is busy,
// errs.KindTimeout after Config.Timeout (or ctx's deadline),
// errs.KindCanceled when ctx is canceled, errs.KindLimitExceeded when the
// input or the SVG is over its limit, and errs.KindCorrupt when the
// converter refuses the file or writes no SVG. The process group is gone
// when ToSVG returns.
func (c *Converter) ToSVG(ctx context.Context, kind string, data []byte) ([]byte, error) {
	k := Kind(kind)
	if k != WMF && k != EMF {
		return nil, errs.Newf(errs.KindInternal, "metafile: unknown kind %q", kind)
	}
	if int64(len(data)) > c.cfg.MaxBytes {
		return nil, errs.Newf(errs.KindLimitExceeded, "metafile: %s of %d bytes exceeds %d", k, len(data), c.cfg.MaxBytes)
	}
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if !c.sem.TryAcquire(1) {
		return nil, errs.Wrap(errs.KindOverloaded, ErrOverloaded)
	}
	defer c.sem.Release(1)

	tmp, err := os.MkdirTemp(c.cfg.TempDir, "xi-metafile-")
	if err != nil {
		return nil, errs.Wrapf(errs.KindInternal, err, "metafile: temp dir")
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	rctx, cancel := context.WithTimeoutCause(ctx, c.cfg.Timeout, errTimeout)
	defer cancel()
	// Pdeathsig follows the thread that forked the child: keep it alive
	// (and this goroutine on it) until the child is reaped.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	args := []string{string(k), strconv.FormatInt(c.cfg.MaxBytes, 10), strconv.Itoa(c.cfg.MaxSide)}
	cmd, err := c.sandbox.Command(rctx, tmp, c.cfg.MaxOutput, args)
	if err != nil {
		return nil, errs.Wrapf(errs.KindInternal, err, "metafile: sandbox")
	}
	cmd.Stdin = bytes.NewReader(data)
	stderr := &capped{max: stderrLimit}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errs.Wrapf(errs.KindInternal, err, "metafile: stdout")
	}
	if err := cmd.Start(); err != nil {
		return nil, errs.Wrapf(errs.KindInternal, err, "metafile: start %s", c.cfg.Path)
	}
	pgid := cmd.Process.Pid
	var out bytes.Buffer
	n, readErr := io.Copy(&out, io.LimitReader(stdout, c.cfg.MaxOutput+1))
	overflow := n > c.cfg.MaxOutput
	if readErr != nil || overflow {
		sandbox.KillGroup(pgid)
	}
	waitErr := cmd.Wait()
	sandbox.KillGroup(pgid) // anything the process left in its group
	switch {
	case ctx.Err() != nil:
		return nil, ctxErr(ctx)
	case errors.Is(context.Cause(rctx), errTimeout):
		return nil, errs.WithPublic(errs.Wrapf(errs.KindTimeout, errTimeout, "metafile: no SVG after %s", c.cfg.Timeout),
			"WMF/EMF conversion timed out")
	case overflow:
		return nil, errs.Newf(errs.KindLimitExceeded, "metafile: SVG over %d bytes", c.cfg.MaxOutput)
	case waitErr != nil:
		return nil, errs.Wrapf(errs.KindCorrupt, waitErr, "metafile: %s: %s", k, stderr.String())
	case readErr != nil:
		return nil, errs.Wrapf(errs.KindCorrupt, readErr, "metafile: read SVG")
	}
	svg := out.Bytes()
	if !bytes.Contains(svg[:min(len(svg), 4096)], []byte("<svg")) {
		return nil, errs.Newf(errs.KindCorrupt, "metafile: %s: no SVG document on stdout", k)
	}
	return svg, nil
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

// capped keeps the first max bytes written to it (stderr is read by
// os/exec's copying goroutine only, before Wait returns).
type capped struct {
	buf bytes.Buffer
	max int
}

func (c *capped) Write(b []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		c.buf.Write(b[:min(len(b), room)])
	}
	return len(b), nil
}

func (c *capped) String() string { return strings.TrimSpace(c.buf.String()) }
