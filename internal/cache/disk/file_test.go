package disk

import (
	"bytes"
	"io"
	"os"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/cache/cachetest"
)

// TestConformanceFileBodies runs the store conformance suite with every
// hit answered from the entry's file.
func TestConformanceFileBodies(t *testing.T) {
	t.Parallel()
	cachetest.Run(t, func(t *testing.T, now func() time.Time) cache.Store {
		t.Helper()
		return open(t, Options{Dir: t.TempDir(), MaxBytes: 1 << 20, Now: now, FileBodyBytes: 1})
	})
}

// spooled returns data as a cache.FileBuffer (a spooled video output).
func spooled(t *testing.T, data []byte) *cache.FileBuffer {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "spool-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	return cache.NewFileBuffer(f, 0, int64(len(data)), nil)
}

// TestFileBodies: a file body is streamed into the store without mapping
// it, and a large hit is a FileBuffer over the entry file that serves a
// range without reading the rest; small hits stay in memory.
func TestFileBodies(t *testing.T) {
	t.Parallel()
	data := bytes.Repeat([]byte("video bytes "), 10_000) // 120 KB
	tests := []struct {
		name     string
		fileMin  int64
		wantFile bool
	}{
		{"large hit from the file", 64 << 10, true},
		{"small hit in memory", 1 << 20, false},
		{"file bodies off", -1, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := open(t, Options{Dir: t.TempDir(), MaxBytes: 1 << 20, FileBodyBytes: tc.fileMin})
			body := spooled(t, data)
			e := cache.Entry{Body: body, Status: 200, Header: map[string][]string{"Content-Type": {"video/mp4"}}}
			if err := s.Set(t.Context(), cache.NSResult, "v1/video", &e, time.Hour); err != nil {
				t.Fatal(err)
			}
			if body.Mapped() {
				t.Error("Set mapped the spooled body")
			}
			body.Release()

			got, ok, err := s.Get(t.Context(), cache.NSResult, "v1/video")
			if err != nil || !ok {
				t.Fatalf("Get = %v, %v", ok, err)
			}
			defer got.Release()
			fb, isFile := got.Body.(*cache.FileBuffer)
			if isFile != tc.wantFile {
				t.Fatalf("hit body %T, want a file body: %v", got.Body, tc.wantFile)
			}
			if cache.BodyLen(got.Body) != int64(len(data)) || got.Header.Get("Content-Type") != "video/mp4" {
				t.Fatalf("hit of %d bytes, %v", cache.BodyLen(got.Body), got.Header)
			}
			r := cache.BodyReader(got.Body)
			if _, err := r.Seek(1000, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			part := make([]byte, 100)
			if _, err := io.ReadFull(r, part); err != nil || !bytes.Equal(part, data[1000:1100]) {
				t.Fatalf("range: %v %q", err, part)
			}
			if isFile && fb.Mapped() {
				t.Error("a range read mapped the entry")
			}
			all, err := io.ReadAll(cache.BodyReader(got.Body))
			if err != nil || !bytes.Equal(all, data) {
				t.Fatalf("body: %v, %d bytes", err, len(all))
			}
		})
	}
}

// TestFileBodyCorrupt: a large entry whose file length disagrees with its
// frame is a corrupt miss and is removed.
func TestFileBodyCorrupt(t *testing.T) {
	t.Parallel()
	m := &metricsRec{}
	s := open(t, Options{Dir: t.TempDir(), MaxBytes: 1 << 20, FileBodyBytes: 1024, Metrics: m})
	cachetest.Set(t, s, cache.NSResult, "v1/big", bytes.Repeat([]byte("z"), 8192), nil, time.Hour)
	p := s.path(fileName(string(cache.NSResult) + "v1/big"))
	if err := os.Truncate(p, 4000); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := cachetest.Get(t, s, cache.NSResult, "v1/big"); ok {
		t.Fatal("truncated entry served")
	}
	if got := m.skipped(cache.SkipCorrupt); got != 1 {
		t.Errorf("corrupt skips %d, want 1", got)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("corrupt file kept: %v", err)
	}
}
