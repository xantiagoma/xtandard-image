package redis

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/cache"
)

type skipRec struct {
	cache.NopMetrics
	mu    sync.Mutex
	skips map[string]int
}

func (m *skipRec) ObserveCacheSkip(_, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.skips == nil {
		m.skips = map[string]int{}
	}
	m.skips[reason]++
}

func (m *skipRec) skipped(reason string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.skips[reason]
}

func TestNewURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, url string
		ok        bool
	}{
		{"plain", "redis://127.0.0.1:6379/0", true},
		{"tls with auth", "rediss://user:s3cret@cache.example:6380/2?dial_timeout=2s", true},
		{"empty", "", false},
		{"bad scheme leaks nothing", "http://user:s3cret@host", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, err := New(&Options{URL: tc.url})
			if (err == nil) != tc.ok {
				t.Fatalf("New(%q) error %v, want ok=%v", tc.url, err, tc.ok)
			}
			if err != nil {
				if strings.Contains(err.Error(), "s3cret") {
					t.Errorf("error leaks the password: %v", err)
				}
				return
			}
			if err := s.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		})
	}
}

// TestOversizeSkippedWithoutServer: the size check runs before any I/O,
// so it needs no server (the URL points nowhere).
func TestOversizeSkippedWithoutServer(t *testing.T) {
	t.Parallel()
	m := &skipRec{}
	s, err := New(&Options{URL: "redis://127.0.0.1:1/0", MaxValueBytes: 1024, Metrics: m})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, n := range []int{1025, 1024} { // body over the limit; body at the limit plus the frame
		b := cache.NewBuffer(make([]byte, n), nil)
		err := s.Set(t.Context(), cache.NSResult, "v1/big", &cache.Entry{Body: b, Status: http.StatusOK}, time.Hour)
		b.Release()
		if err != nil {
			t.Errorf("Set of %d bytes: %v, want a silent skip", n, err)
		}
	}
	if got := m.skipped(cache.SkipTooLarge); got != 2 {
		t.Errorf("too_large skips %d, want 2", got)
	}
}
