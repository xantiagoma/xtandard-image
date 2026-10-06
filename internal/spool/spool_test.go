package spool

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

type metricsRec struct {
	mu       sync.Mutex
	bytes    int64
	files    int
	spills   map[string]int
	rejected int
}

func (m *metricsRec) Usage(b int64, f int) {
	m.mu.Lock()
	m.bytes, m.files = b, f
	m.mu.Unlock()
}

func (m *metricsRec) Spilled(kind string) {
	m.mu.Lock()
	if m.spills == nil {
		m.spills = map[string]int{}
	}
	m.spills[kind]++
	m.mu.Unlock()
}

func (m *metricsRec) Rejected() {
	m.mu.Lock()
	m.rejected++
	m.mu.Unlock()
}

func newSpool(t *testing.T, cfg Config) (*Spool, *metricsRec) {
	t.Helper()
	if cfg.Dir == "" {
		cfg.Dir = filepath.Join(t.TempDir(), "spool")
	}
	m := &metricsRec{}
	s, err := New(cfg, WithMetrics(m))
	if err != nil {
		t.Fatal(err)
	}
	return s, m
}

// names lists the directory.
func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// checkEmpty fails unless no spool file is open, nothing is reserved and
// the directory holds no spool file.
func checkEmpty(t *testing.T, s *Spool) {
	t.Helper()
	if b, f := s.Usage(); b != 0 || f != 0 {
		t.Errorf("spool holds %d bytes in %d files", b, f)
	}
	for _, n := range names(t, s.Dir()) {
		t.Errorf("file %s left in the spool directory", n)
	}
}

func TestNewCleansStaleFilesOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, n := range []string{Prefix + "input-123", Prefix + "output-9", "keep.txt", "xi-other"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, Prefix+"dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	s, _ := newSpool(t, Config{Dir: dir, Threshold: 1})
	if s.Removed() != 2 {
		t.Errorf("removed %d stale files, want 2", s.Removed())
	}
	if got, want := names(t, dir), []string{"keep.txt", "xi-other", Prefix + "dir"}; !slices.Equal(got, want) {
		t.Errorf("directory %v, want %v", got, want)
	}
}

func TestNewRejects(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		cfg  Config
	}{
		{"relative directory", Config{Dir: "spool"}},
		{"directory under a file", Config{Dir: filepath.Join(file, "spool")}},
		{"negative threshold", Config{Dir: t.TempDir(), Threshold: -1}},
		{"negative budget", Config{Dir: t.TempDir(), MaxBytes: -1}},
	} {
		if _, err := New(tc.cfg); err == nil {
			t.Errorf("%s: New succeeded", tc.name)
		}
	}
}

func TestSpills(t *testing.T) {
	t.Parallel()
	var none *Spool
	if none.Spills(1<<40) || none.Threshold() != 0 {
		t.Error("a nil spool spills")
	}
	off, _ := newSpool(t, Config{})
	on, _ := newSpool(t, Config{Threshold: 100})
	for _, tc := range []struct {
		s    *Spool
		n    int64
		want bool
	}{
		{off, 1 << 40, false}, {on, 100, false}, {on, 101, true}, {on, 0, false},
	} {
		if got := tc.s.Spills(tc.n); got != tc.want {
			t.Errorf("threshold %d, %d bytes: %v", tc.s.Threshold(), tc.n, got)
		}
	}
}

// TestFileLifecycle: a file is unlinked as soon as it is created, reads
// back what was written, and closes (returning its reservation) on its
// last Release; an aborted one leaves nothing either.
func TestFileLifecycle(t *testing.T) {
	t.Parallel()
	s, m := newSpool(t, Config{Threshold: 10, MaxBytes: 1 << 20})
	w, err := s.Create(KindInput, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n := names(t, s.Dir()); len(n) != 0 {
		t.Fatalf("created file not unlinked: %v", n)
	}
	info, err := w.File().Stat()
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, %v", info.Mode(), err)
	}
	data := bytes.Repeat([]byte("abc"), 5000)
	for chunk := range slices.Chunk(data, 1000) {
		if _, err := w.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if b, f := s.Usage(); b < int64(len(data)) || f != 1 {
		t.Errorf("usage while writing: %d bytes, %d files", b, f)
	}
	fb, err := w.Finish(w.Written())
	if err != nil {
		t.Fatal(err)
	}
	w.Abort() // no-op after Finish
	if b, _ := s.Usage(); b != int64(len(data)) {
		t.Errorf("reservation %d after Finish, want the size %d", b, len(data))
	}
	got, err := io.ReadAll(fb.Reader())
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("read back: %v", err)
	}
	fb.Retain()
	fb.Release()
	fb.Release()
	checkEmpty(t, s)
	if m.spills[KindInput] != 1 {
		t.Errorf("spills %v", m.spills)
	}

	w, err = s.Create(KindOutput, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("partial")); err != nil {
		t.Fatal(err)
	}
	w.Abort()
	checkEmpty(t, s)
	if m.spills[KindOutput] != 0 || m.bytes != 0 || m.files != 0 {
		t.Errorf("metrics after abort: %+v", m)
	}
	if _, err := w.Write([]byte("x")); err == nil {
		t.Error("write after Abort")
	}
}

// TestBudget: reservations past MaxBytes fail with a 503 that carries a
// Retry-After hint, after asking the reclaimer once; bytes come back on
// release.
func TestBudget(t *testing.T) {
	t.Parallel()
	s, m := newSpool(t, Config{Threshold: 1, MaxBytes: 1000})
	held, err := s.Create(KindInput, 800)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.SetReclaimer(func() bool { calls++; return false })

	_, err = s.Create(KindOutput, 300)
	be, ok := errors.AsType[*BudgetError](err)
	if !ok || errs.KindOf(err) != errs.KindUnavailable || errs.Status(errs.KindOf(err)) != http.StatusServiceUnavailable {
		t.Fatalf("over budget: %v", err)
	}
	if be.RetryAfterHint() != RetryAfter || be.Need != 300 || be.Max != 1000 || calls != 1 || m.rejected != 1 {
		t.Errorf("budget error %+v, reclaimer calls %d, rejections %d", be, calls, m.rejected)
	}
	if _, err := s.Create(KindOutput, 2000); err == nil || calls != 1 {
		t.Errorf("a reservation over the whole budget: %v (reclaimer calls %d)", err, calls)
	}

	// Growing past the budget while writing fails the same way.
	if _, err := held.Write(make([]byte, 900)); err != nil {
		t.Fatal(err)
	}
	if _, err := held.Write(make([]byte, 200)); errs.KindOf(err) != errs.KindUnavailable {
		t.Fatalf("growth over budget: %v", err)
	}

	// The reclaimer frees what the next reservation needs.
	s.SetReclaimer(func() bool { held.Abort(); return true })
	w, err := s.Create(KindOutput, 1000)
	if err != nil {
		t.Fatalf("after reclaiming: %v", err)
	}
	w.Abort()
	checkEmpty(t, s)
}

// TestFinishReservesTheSize: a file written from C (Finish with a size
// larger than the reservation) reserves the difference, or fails.
func TestFinishReservesTheSize(t *testing.T) {
	t.Parallel()
	s, _ := newSpool(t, Config{Threshold: 1, MaxBytes: 100})
	w, err := s.Create(KindOutput, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Finish(500); errs.KindOf(err) != errs.KindUnavailable {
		t.Fatalf("Finish over budget: %v", err)
	}
	checkEmpty(t, s)
	w, err = s.Create(KindOutput, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Finish(0); err == nil {
		t.Fatal("empty file finished")
	}
	checkEmpty(t, s)
}

// TestConcurrent: many writers share the budget without leaking it.
func TestConcurrent(t *testing.T) {
	t.Parallel()
	s, _ := newSpool(t, Config{Threshold: 1, MaxBytes: 64 << 10})
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			w, err := s.Create(KindInput, 0)
			if err != nil {
				return // over budget: a 503
			}
			if _, err := w.Write(make([]byte, 3000)); err != nil {
				w.Abort()
				return
			}
			fb, err := w.Finish(w.Written())
			if err != nil {
				return
			}
			_ = fb.Bytes()
			fb.Release()
		})
	}
	wg.Wait()
	checkEmpty(t, s)
}

var _ cache.ReaderBody = (*cache.FileBuffer)(nil)
