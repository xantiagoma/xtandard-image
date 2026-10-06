package memory

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/cache"
)

type skipRec struct {
	cache.NopMetrics
	skips map[string]int
}

func (m *skipRec) ObserveCacheSkip(_, reason string) { m.skips[reason]++ }

// TestSkipsFileBodies: a body spooled to a file (a large video, WP-121b)
// is not kept in memory, nor its file pinned.
func TestSkipsFileBodies(t *testing.T) {
	t.Parallel()
	m := &skipRec{skips: map[string]int{}}
	s, err := New(Options{MaxBytes: 1 << 30, Metrics: m})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f, err := os.CreateTemp(t.TempDir(), "spool-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("video"); err != nil {
		t.Fatal(err)
	}
	freed := false
	b := cache.NewFileBuffer(f, 0, 5, func() { freed = true })
	if err := s.Set(t.Context(), cache.NSResult, "k", &cache.Entry{Body: b, Status: http.StatusOK}, time.Hour); err != nil {
		t.Fatal(err)
	}
	b.Release()
	if !freed {
		t.Error("the store kept a reference to the file body")
	}
	if has(t, s, "k") || m.skips[cache.SkipTooLarge] != 1 {
		t.Errorf("file body stored; skips %v", m.skips)
	}
}
