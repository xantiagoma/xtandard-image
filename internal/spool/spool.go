// Package spool keeps large video sources and outputs in temporary files
// instead of memory (WP-121b, ADR 0012 §5).
//
// A Spool owns one directory (XI_VIDEO_TRANSFORM_SPOOL_DIR). Create opens
// a file there (os.CreateTemp, mode 0600, name prefix Prefix) and unlinks
// it at once: the open descriptor is the only reference, so the file
// disappears when the last user closes it, on every path (success, error,
// cancellation, a panic, even a crash of the process), and nothing in the
// directory has to be cleaned up afterwards. New still removes files with
// the prefix that a previous process left between create and unlink.
//
// A Writer fills the file (from Go with Write, or from C through its
// descriptor) and Finish turns it into a read-only *cache.FileBuffer: a
// refcounted Buffer that the fetcher, the transcoder, the result cache
// and http.ServeContent read with ReadAt, and that closes the file on its
// last Release.
//
// Disk use is bounded by an optional budget (XI_VIDEO_TRANSFORM_SPOOL_
// MAX_BYTES), a weighted semaphore of bytes: a Writer reserves what it
// will write (the declared length of a download, the output limit of an
// encode) or grows its reservation while writing, and Finish shrinks it to
// the file's size; the buffer's last Release returns it. A reservation
// that does not fit fails at once with a *BudgetError (503 with
// Retry-After), after asking the reclaimer (the fetcher's cache of
// spooled sources) to drop idle files.
package spool

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// Prefix starts the name of every spool file.
const Prefix = "xi-spool-"

// RetryAfter is the Retry-After hint of a request refused for the budget.
const RetryAfter = 5 * time.Second

// growStep is the least a Writer adds to its reservation when a write
// outgrows it (bounded below by the write).
const growStep = 4 << 20

// Kinds of spool files (the "kind" label of the spill counter).
const (
	KindInput  = "input"
	KindOutput = "output"
)

// Config sizes a Spool.
type Config struct {
	// Dir holds the files; it is created (0700) if missing and must be
	// writable.
	Dir string
	// Threshold: data larger than this goes to a file (Spills); 0 keeps
	// everything in memory.
	Threshold int64
	// MaxBytes bounds the bytes of every spool file at once; 0 is no
	// bound.
	MaxBytes int64
}

// Metrics receives spool observations; implementations must be safe for
// concurrent use and must not block.
type Metrics interface {
	// Usage reports the bytes reserved by and the number of open spool
	// files.
	Usage(bytes int64, files int)
	// Spilled counts data of kind (KindInput, KindOutput) kept in a file
	// (a finished spool file).
	Spilled(kind string)
	// Rejected counts a reservation refused for the budget.
	Rejected()
}

type nopMetrics struct{}

func (nopMetrics) Usage(int64, int) {}
func (nopMetrics) Spilled(string)   {}
func (nopMetrics) Rejected()        {}

// Option configures New.
type Option func(*Spool)

// WithMetrics installs m (nil keeps none).
func WithMetrics(m Metrics) Option {
	return func(s *Spool) {
		if m != nil {
			s.metrics = m
		}
	}
}

// Spool creates and accounts for spool files. It is safe for concurrent
// use; a nil *Spool spills nothing.
type Spool struct {
	dir       string
	threshold int64
	max       int64
	budget    *semaphore.Weighted // nil: no bound
	metrics   Metrics

	removed int // stale files New removed

	mu        sync.Mutex
	reserved  int64
	files     int
	reclaimer func() bool
}

// New prepares cfg.Dir: created if missing, checked writable, and
// cleared of the spool files a previous process left (only names with
// Prefix; nothing else in the directory is touched).
func New(cfg Config, opts ...Option) (*Spool, error) {
	if cfg.Threshold < 0 || cfg.MaxBytes < 0 {
		return nil, fmt.Errorf("spool: negative threshold or max bytes (%d, %d)", cfg.Threshold, cfg.MaxBytes)
	}
	if !filepath.IsAbs(cfg.Dir) {
		return nil, fmt.Errorf("spool: directory %q must be absolute", cfg.Dir)
	}
	s := &Spool{dir: filepath.Clean(cfg.Dir), threshold: cfg.Threshold, max: cfg.MaxBytes, metrics: nopMetrics{}}
	if cfg.MaxBytes > 0 {
		s.budget = semaphore.NewWeighted(cfg.MaxBytes)
	}
	for _, o := range opts {
		o(s)
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, fmt.Errorf("spool: %w (mount a writable volume, e.g. a tmpfs or emptyDir)", err)
	}
	probe, err := os.CreateTemp(s.dir, Prefix+"probe-*")
	if err != nil {
		return nil, fmt.Errorf("spool: directory %s not writable: %w (mount a writable volume, e.g. a tmpfs or emptyDir)", s.dir, err)
	}
	_ = probe.Close()           // empty: nothing to flush
	_ = os.Remove(probe.Name()) // removed by the sweep below if this fails
	if s.removed, err = s.clean(); err != nil {
		return nil, err
	}
	s.metrics.Usage(0, 0)
	return s, nil
}

// clean removes the spool files of earlier processes: regular files named
// with Prefix directly in the directory. It returns how many it removed.
func (s *Spool) clean() (int, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return 0, fmt.Errorf("spool: %w", err)
	}
	n := 0
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasPrefix(e.Name(), Prefix) {
			continue
		}
		if err := os.Remove(filepath.Join(s.dir, e.Name())); err == nil || errors.Is(err, fs.ErrNotExist) {
			n++
		}
	}
	return n, nil
}

// Dir is the spool directory.
func (s *Spool) Dir() string { return s.dir }

// Removed is how many files of an earlier process New removed.
func (s *Spool) Removed() int { return s.removed }

// Threshold is the size above which data is spooled (0: never).
func (s *Spool) Threshold() int64 {
	if s == nil {
		return 0
	}
	return s.threshold
}

// Spills reports whether n bytes go to a file.
func (s *Spool) Spills(n int64) bool { return s != nil && s.threshold > 0 && n > s.threshold }

// SetReclaimer installs f, called when a reservation does not fit the
// budget: it should release idle spooled data and report whether it freed
// anything (the reservation is then tried once more).
func (s *Spool) SetReclaimer(f func() bool) {
	s.mu.Lock()
	s.reclaimer = f
	s.mu.Unlock()
}

// Usage reports the bytes reserved and the open files.
func (s *Spool) Usage() (bytes int64, files int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reserved, s.files
}

// BudgetError is a reservation the spool budget cannot hold now.
type BudgetError struct {
	Need, Max int64
}

func (e *BudgetError) Error() string {
	return fmt.Sprintf("spool: %d bytes do not fit the budget of %d", e.Need, e.Max)
}

// RetryAfterHint is the Retry-After of the 503 (server.writeError).
func (*BudgetError) RetryAfterHint() time.Duration { return RetryAfter }

// budgetError is the request error of a refused reservation: 503 with a
// public message and the *BudgetError (Retry-After) in its chain.
func budgetError(need, max int64) error {
	return &errs.Error{
		Kind: errs.KindUnavailable, Public: "temporarily out of video spool space",
		Msg: "spool: over budget", Err: &BudgetError{Need: need, Max: max},
	}
}

// reserve takes n bytes of the budget, reclaiming idle files once when
// they do not fit.
func (s *Spool) reserve(n int64) error {
	if n <= 0 {
		return nil
	}
	if s.budget != nil && !s.budget.TryAcquire(n) {
		s.mu.Lock()
		reclaim := s.reclaimer
		s.mu.Unlock()
		if n > s.max || reclaim == nil || !reclaim() || !s.budget.TryAcquire(n) {
			s.metrics.Rejected()
			return budgetError(n, s.max)
		}
	}
	s.account(n, 0)
	return nil
}

// tryReserve takes n bytes if they fit now, without reclaiming or
// counting a rejection.
func (s *Spool) tryReserve(n int64) bool {
	if s.budget != nil && !s.budget.TryAcquire(n) {
		return false
	}
	s.account(n, 0)
	return true
}

// unreserve returns n bytes to the budget.
func (s *Spool) unreserve(n int64) {
	if n <= 0 {
		return
	}
	if s.budget != nil {
		s.budget.Release(n)
	}
	s.account(-n, 0)
}

func (s *Spool) account(bytes int64, files int) {
	s.mu.Lock()
	s.reserved += bytes
	s.files += files
	b, f := s.reserved, s.files
	s.mu.Unlock()
	s.metrics.Usage(b, f)
}

// Create opens a new spool file for kind (KindInput or KindOutput),
// reserving reserve bytes of the budget up front (0 when the size is
// unknown: Write reserves as it goes). Errors: a *BudgetError chain
// (errs.KindUnavailable) when the reservation does not fit, an internal
// error when the file cannot be created.
func (s *Spool) Create(kind string, reserve int64) (*Writer, error) {
	if s == nil {
		return nil, errs.New(errs.KindInternal, "spool: not configured")
	}
	if err := s.reserve(reserve); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(s.dir, Prefix+kind+"-*")
	if err != nil {
		s.unreserve(reserve)
		return nil, errs.Wrapf(errs.KindInternal, err, "spool: create")
	}
	// Unlinked at once: the descriptor is the only reference left.
	// (Another process starting on the same directory may have removed it
	// first: the descriptor still works.)
	if err := os.Remove(f.Name()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		_ = f.Close() // the remove error is what matters
		s.unreserve(reserve)
		return nil, errs.Wrapf(errs.KindInternal, err, "spool: unlink")
	}
	s.account(0, 1)
	return &Writer{s: s, f: f, kind: kind, reserved: reserve}, nil
}

// Writer fills one spool file. It is not safe for concurrent use. Every
// Writer ends with exactly one Finish or Abort.
type Writer struct {
	s        *Spool
	f        *os.File
	kind     string
	reserved int64
	n        int64 // bytes written with Write
	done     bool
}

// Write appends p, growing the reservation when it outgrows it.
func (w *Writer) Write(p []byte) (int, error) {
	if w.done {
		return 0, errors.New("spool: write after finish")
	}
	if err := w.Reserve(w.n + int64(len(p))); err != nil {
		return 0, err
	}
	n, err := w.f.Write(p)
	w.n += int64(n)
	if err != nil {
		return n, errs.Wrapf(errs.KindInternal, err, "spool: write")
	}
	return n, nil
}

// Reserve grows the reservation to at least n bytes (in steps, so small
// writes do not each touch the budget).
func (w *Writer) Reserve(n int64) error {
	if n <= w.reserved {
		return nil
	}
	need := n - w.reserved
	if step := max(need, growStep); step > need && w.s.tryReserve(step) {
		w.reserved += step
		return nil
	}
	if err := w.s.reserve(need); err != nil {
		return err
	}
	w.reserved += need
	return nil
}

// File is the file being written, for a writer in C (the FFmpeg shim
// writes through a duplicate of its descriptor). It must not be closed.
func (w *Writer) File() *os.File { return w.f }

// Written is the bytes Write appended.
func (w *Writer) Written() int64 { return w.n }

// Finish ends writing: the file holds size bytes (Written for a Go
// writer, what C reports for the shim), the reservation shrinks to it,
// and the file becomes a read-only buffer with one reference whose last
// Release closes it and returns the reservation.
func (w *Writer) Finish(size int64) (*cache.FileBuffer, error) {
	if w.done {
		return nil, errors.New("spool: finish twice")
	}
	if size <= 0 {
		w.Abort()
		return nil, errs.New(errs.KindInternal, "spool: empty file")
	}
	if size > w.reserved {
		if err := w.Reserve(size); err != nil {
			w.Abort()
			return nil, err
		}
	}
	w.done = true
	s, keep := w.s, size
	s.unreserve(w.reserved - keep)
	s.metrics.Spilled(w.kind)
	return cache.NewFileBuffer(w.f, 0, size, func() {
		s.unreserve(keep)
		s.account(0, -1)
	}), nil
}

// Abort discards the file and its reservation. It is a no-op after
// Finish or Abort.
func (w *Writer) Abort() {
	if w.done {
		return
	}
	w.done = true
	_ = w.f.Close() // discarded: nothing to flush
	w.s.unreserve(w.reserved)
	w.s.account(0, -1)
}
