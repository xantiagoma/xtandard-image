package disk

import (
	"bytes"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/cache/cachetest"
)

type metricsRec struct {
	cache.NopMetrics
	mu        sync.Mutex
	hits      int
	misses    int
	evictions int
	skips     map[string]int
}

func (m *metricsRec) ObserveCache(_ string, hit bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if hit {
		m.hits++
	} else {
		m.misses++
	}
}

func (m *metricsRec) ObserveCacheEvictions(_ string, n int) {
	m.mu.Lock()
	m.evictions += n
	m.mu.Unlock()
}

func (m *metricsRec) ObserveCacheSkip(_, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.skips == nil {
		m.skips = map[string]int{}
	}
	m.skips[reason]++
}

func (m *metricsRec) evicted() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.evictions
}

func (m *metricsRec) skipped(reason string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.skips[reason]
}

func open(t *testing.T, opts Options) *Store {
	t.Helper()
	s, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() }) // idempotent
	return s
}

func TestConformance(t *testing.T) {
	t.Parallel()
	cachetest.Run(t, func(t *testing.T, now func() time.Time) cache.Store {
		t.Helper()
		return open(t, Options{Dir: t.TempDir(), MaxBytes: 1 << 20, Now: now})
	})
}

// dirBytes sums the sizes of the entry files under dir.
func dirBytes(t *testing.T, dir string) (total int64, files int) {
	t.Helper()
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		files++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return total, files
}

func TestSurvivesRestart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := bytes.Repeat([]byte{0xAB}, 4096)
	h := http.Header{"Content-Type": {"image/avif"}}

	first := open(t, Options{Dir: dir, MaxBytes: 1 << 20})
	cachetest.Set(t, first, cache.NSResult, "v1/keep", body, h, time.Hour)
	cachetest.Set(t, first, cache.NSOrigin, "v1/orig", []byte("original"), nil, 0)
	used, n := first.Usage()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second := open(t, Options{Dir: dir, MaxBytes: 1 << 20})
	if u, m := second.Usage(); u != used || m != n {
		t.Errorf("rebuilt index %d bytes/%d entries, want %d/%d", u, m, used, n)
	}
	e, got, ok := cachetest.Get(t, second, cache.NSResult, "v1/keep")
	if !ok || !bytes.Equal(got, body) || e.Header.Get("Content-Type") != "image/avif" {
		t.Fatalf("after restart: ok=%v %d bytes %v", ok, len(got), e.Header)
	}
	if _, got, ok := cachetest.Get(t, second, cache.NSOrigin, "v1/orig"); !ok || string(got) != "original" {
		t.Errorf("orig/ after restart: %q %v", got, ok)
	}
}

func TestScanRemovesTempFilesAndIgnoresStrangers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := open(t, Options{Dir: dir, MaxBytes: 1 << 20})
	cachetest.Set(t, s, cache.NSResult, "v1/a", []byte("a"), nil, time.Hour)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	name := fileName(string(cache.NSResult) + "v1/a")
	shard := filepath.Dir(s.path(name))
	tmp := filepath.Join(shard, name+".123.tmp")
	stranger := filepath.Join(dir, "README")
	for _, p := range []string{tmp, stranger} {
		if err := os.WriteFile(p, []byte("junk"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	s2 := open(t, Options{Dir: dir, MaxBytes: 1 << 20})
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("temporary file survived the scan: %v", err)
	}
	if _, err := os.Stat(stranger); err != nil {
		t.Errorf("unrelated file removed: %v", err)
	}
	if _, n := s2.Usage(); n != 1 {
		t.Errorf("%d entries indexed, want 1", n)
	}
}

func TestCorruptEntries(t *testing.T) {
	t.Parallel()
	valid := func(s *Store, key string) string {
		cachetest.Set(t, s, cache.NSResult, key, bytes.Repeat([]byte("z"), 512), nil, time.Hour)
		return s.path(fileName(string(cache.NSResult) + key))
	}
	tests := []struct {
		name    string
		corrupt func(t *testing.T, s *Store, p string)
	}{
		{"truncated body", func(t *testing.T, _ *Store, p string) {
			t.Helper()
			if err := os.Truncate(p, 100); err != nil {
				t.Fatal(err)
			}
		}},
		{"flipped body byte", func(t *testing.T, _ *Store, p string) {
			t.Helper()
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			b[len(b)-1] ^= 0xFF
			if err := os.WriteFile(p, b, 0o600); err != nil { //nolint:gosec // G703: test file under t.TempDir
				t.Fatal(err)
			}
		}},
		{"garbage", func(t *testing.T, _ *Store, p string) {
			t.Helper()
			if err := os.WriteFile(p, []byte("not a cache entry"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"empty", func(t *testing.T, _ *Store, p string) {
			t.Helper()
			if err := os.Truncate(p, 0); err != nil {
				t.Fatal(err)
			}
		}},
		{"another key's file", func(t *testing.T, s *Store, p string) {
			t.Helper()
			other := valid(s, "v1/other")
			if err := os.Rename(other, p); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := &metricsRec{}
			s := open(t, Options{Dir: t.TempDir(), MaxBytes: 1 << 20, Metrics: m})
			p := valid(s, "v1/victim")
			tc.corrupt(t, s, p)
			if _, _, ok := cachetest.Get(t, s, cache.NSResult, "v1/victim"); ok {
				t.Fatal("corrupt entry served")
			}
			if got := m.skipped(cache.SkipCorrupt); got != 1 {
				t.Errorf("corrupt skips %d, want 1", got)
			}
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Errorf("corrupt file kept: %v", err)
			}
			// The key works again afterwards.
			valid(s, "v1/victim")
			if _, _, ok := cachetest.Get(t, s, cache.NSResult, "v1/victim"); !ok {
				t.Error("miss after rewriting the key")
			}
		})
	}
}

func TestTooLargeSkipped(t *testing.T) {
	t.Parallel()
	m := &metricsRec{}
	s := open(t, Options{Dir: t.TempDir(), MaxBytes: 1024, Metrics: m})
	cachetest.Set(t, s, cache.NSResult, "v1/big", make([]byte, 2048), nil, time.Hour)
	if _, _, ok := cachetest.Get(t, s, cache.NSResult, "v1/big"); ok {
		t.Error("entry over MaxBytes stored")
	}
	if got := m.skipped(cache.SkipTooLarge); got != 1 {
		t.Errorf("too_large skips %d, want 1", got)
	}
}

func TestNewRejectsUnwritableDir(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	_, err := New(Options{Dir: dir, MaxBytes: 1 << 20})
	if err == nil || !strings.Contains(err.Error(), "not writable") {
		t.Errorf("New on a read-only dir: %v", err)
	}
	if _, err := New(Options{Dir: "relative", MaxBytes: 1}); err == nil {
		t.Error("relative dir accepted")
	}
	if _, err := New(Options{Dir: dir, MaxBytes: 0}); err == nil {
		t.Error("zero MaxBytes accepted")
	}
}

// TestJanitorBoundsUsage writes far more than MaxBytes; after every write
// the janitor (kicked by the write) brings the index and the files on disk
// back under the bound, evicting least recently used entries first.
func TestJanitorBoundsUsage(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		const maxBytes = 16 << 10
		m := &metricsRec{}
		s, err := New(Options{Dir: dir, MaxBytes: maxBytes, Metrics: m})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		body := make([]byte, 1000)
		for i := range 100 {
			cachetest.Set(t, s, cache.NSResult, "v1/"+strconv.Itoa(i), body, nil, time.Hour)
			synctest.Wait() // the janitor has swept
			// Keep the first entry hot: recency, not age, decides.
			if _, _, ok := cachetest.Get(t, s, cache.NSResult, "v1/0"); !ok {
				t.Fatalf("after write %d: the most recently read entry was evicted", i)
			}
			if used, _ := s.Usage(); used > maxBytes {
				t.Fatalf("after write %d: index holds %d bytes > %d", i, used, maxBytes)
			}
			if onDisk, _ := dirBytes(t, dir); onDisk > maxBytes {
				t.Fatalf("after write %d: %d bytes on disk > %d", i, onDisk, maxBytes)
			}
		}
		if _, _, ok := cachetest.Get(t, s, cache.NSResult, "v1/99"); !ok {
			t.Error("newest entry evicted")
		}
		if _, _, ok := cachetest.Get(t, s, cache.NSResult, "v1/1"); ok {
			t.Error("oldest entry survived")
		}
		if m.evicted() == 0 {
			t.Error("no evictions counted")
		}
		used, n := s.Usage()
		onDisk, files := dirBytes(t, dir)
		if used != onDisk || n != files {
			t.Errorf("index %d bytes/%d entries, disk %d bytes/%d files", used, n, onDisk, files)
		}
	})
}

// TestJanitorSweepsExpired removes entries past their TTL on its tick,
// without a Get.
func TestJanitorSweepsExpired(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		s, err := New(Options{Dir: dir, MaxBytes: 1 << 20, JanitorInterval: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		cachetest.Set(t, s, cache.NSResult, "v1/short", []byte("short"), nil, 30*time.Second)
		cachetest.Set(t, s, cache.NSResult, "v1/long", []byte("long"), nil, time.Hour)
		synctest.Sleep(61 * time.Second)
		if _, n := s.Usage(); n != 1 {
			t.Errorf("%d entries after the sweep, want 1", n)
		}
		if _, files := dirBytes(t, dir); files != 1 {
			t.Errorf("%d files after the sweep, want 1", files)
		}
	})
}

// TestConcurrent hammers one small store from several goroutines (run
// with -race).
func TestConcurrent(t *testing.T) {
	t.Parallel()
	s := open(t, Options{Dir: t.TempDir(), MaxBytes: 64 << 10})
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 50 {
				key := "v1/" + strconv.Itoa((g*7+i)%20)
				switch i % 3 {
				case 0:
					cachetest.Set(t, s, cache.NSResult, key, make([]byte, 2000+i), nil, time.Hour)
				case 1:
					_, _, _ = s.Get(t.Context(), cache.NSResult, key)
				default:
					_ = s.Delete(t.Context(), cache.NSResult, key)
				}
			}
		})
	}
	wg.Wait()
}

func BenchmarkGetHit(b *testing.B) {
	for _, size := range []int{16 << 10, 256 << 10} {
		b.Run(strconv.Itoa(size>>10)+"KiB", func(b *testing.B) {
			s, err := New(Options{Dir: b.TempDir(), MaxBytes: 1 << 30})
			if err != nil {
				b.Fatal(err)
			}
			defer s.Close()
			body := cache.NewBuffer(bytes.Repeat([]byte{7}, size), nil)
			defer body.Release()
			h := http.Header{"Content-Type": {"image/webp"}, "Etag": {`"xi1-abcdefghijklmnopqrstuv"`}}
			if err := s.Set(b.Context(), cache.NSResult, "v1/bench", &cache.Entry{Body: body, Status: 200, Header: h}, time.Hour); err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for b.Loop() {
				e, ok, err := s.Get(b.Context(), cache.NSResult, "v1/bench")
				if err != nil || !ok {
					b.Fatal(ok, err)
				}
				e.Release()
			}
		})
	}
}
