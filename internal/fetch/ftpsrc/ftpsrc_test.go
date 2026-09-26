package ftpsrc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/fetch/internal/filemeta"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

var testMtime = time.Date(2026, 9, 1, 12, 30, 0, 0, time.UTC)

func testFiles() map[string]fakeFile {
	return map[string]fakeFile{
		"/srv/a.jpg":          {data: []byte("jpeg-a-bytes"), mtime: testMtime},
		"/srv/dir/sub/b.png":  {data: bytes.Repeat([]byte("b"), 4096), mtime: testMtime},
		"/srv/nomtime.gif":    {data: []byte("gif")},
		"/srv/secret.jpg":     {data: []byte("x"), denied: true},
		"/srv/slow.jpg":       {data: bytes.Repeat([]byte("s"), 1<<16), stallAfter: 1024},
		"/srv/big.jpg":        {data: bytes.Repeat([]byte("z"), 1000), mtime: testMtime},
		"/srv/with space.jpg": {data: []byte("space")},
	}
}

func newTestSource(t *testing.T, f *fakeFTP) *Source {
	t.Helper()
	cfg := f.config()
	cfg.Root = "/srv"
	s, err := New(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func key(p string) source.Key { return source.Key{Source: "ftp-test", Path: p} }

func readAll(t *testing.T, obj *source.Object) []byte {
	t.Helper()
	defer obj.Body.Close()
	b, err := io.ReadAll(obj.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return b
}

func wantKind(t *testing.T, err error, k errs.Kind) {
	t.Helper()
	if !errors.Is(err, errs.Match(k)) {
		t.Fatalf("err = %v (kind %v), want kind %v", err, errs.KindOf(err), k)
	}
}

func TestFetch(t *testing.T) {
	t.Parallel()
	files := testFiles()
	f := newFakeFTP(t, files)
	s := newTestSource(t, f)

	tests := []struct {
		name  string
		path  string
		opt   source.FetchOptions
		kind  errs.Kind // zero: the fetch succeeds
		body  []byte
		mtime time.Time
		notMd bool
	}{
		{name: "get", path: "a.jpg", body: files["/srv/a.jpg"].data, mtime: testMtime},
		{name: "nested", path: "dir/sub/b.png", body: files["/srv/dir/sub/b.png"].data, mtime: testMtime},
		{name: "space in name", path: "with space.jpg", body: []byte("space")},
		{name: "no mdtm", path: "nomtime.gif", body: []byte("gif")},
		{name: "missing", path: "nope.jpg", kind: errs.KindNotFound},
		{name: "permission denied", path: "secret.jpg", kind: errs.KindForbidden},
		{name: "oversize", path: "big.jpg", opt: source.FetchOptions{MaxBytes: 999}, kind: errs.KindSourceTooLarge},
		{name: "exactly max", path: "big.jpg", opt: source.FetchOptions{MaxBytes: 1000}, body: files["/srv/big.jpg"].data, mtime: testMtime},
		{name: "if-modified-since equal", path: "a.jpg", opt: source.FetchOptions{IfModifiedSince: testMtime}, notMd: true, mtime: testMtime},
		{name: "if-modified-since older", path: "a.jpg", opt: source.FetchOptions{IfModifiedSince: testMtime.Add(-time.Second)}, body: files["/srv/a.jpg"].data, mtime: testMtime},
		{name: "if-none-match", path: "a.jpg", opt: source.FetchOptions{IfNoneMatch: filemeta.ETag(12, testMtime)}, notMd: true, mtime: testMtime},
		{name: "if-none-match stale", path: "a.jpg", opt: source.FetchOptions{IfNoneMatch: `W/"c-1"`}, body: files["/srv/a.jpg"].data, mtime: testMtime},
	}
	// Every case shares the single pooled connection (PoolSize 1), which is
	// what proves reuse across 404s, 304s and size refusals.
	t.Cleanup(func() {
		if n := f.loginCount(); n != 1 {
			t.Errorf("%d logins, want 1 pooled connection for every fetch", n)
		}
	})
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			obj, err := s.Fetch(t.Context(), key(tc.path), tc.opt)
			if tc.kind != errs.KindUnknown {
				wantKind(t, err, tc.kind)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !obj.LastModified.Equal(tc.mtime) {
				t.Errorf("LastModified = %v, want %v", obj.LastModified, tc.mtime)
			}
			if obj.ETag == "" {
				t.Error("empty ETag")
			}
			if tc.notMd {
				if obj.Body != nil {
					_ = obj.Body.Close()
				}
				if !obj.NotModified || obj.Body != nil {
					t.Fatalf("got NotModified=%v Body=%v, want a bodiless 304", obj.NotModified, obj.Body)
				}
				return
			}
			if obj.Size != int64(len(tc.body)) {
				t.Errorf("Size = %d, want %d", obj.Size, len(tc.body))
			}
			if diff := cmp.Diff(tc.body, readAll(t, obj)); diff != "" {
				t.Errorf("body (-want +got):\n%s", diff)
			}
		})
	}
}

func TestETagStable(t *testing.T) {
	t.Parallel()
	f := newFakeFTP(t, testFiles())
	s := newTestSource(t, f)
	obj, err := s.Fetch(t.Context(), key("a.jpg"), source.FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = readAll(t, obj)
	again, err := s.Fetch(t.Context(), key("a.jpg"), source.FetchOptions{IfNoneMatch: obj.ETag})
	if err != nil {
		t.Fatal(err)
	}
	if !again.NotModified {
		t.Fatalf("If-None-Match %s did not yield NotModified", obj.ETag)
	}
}

func TestLoginRefused(t *testing.T) {
	t.Parallel()
	f := newFakeFTP(t, testFiles())
	tests := []struct {
		name string
		user string
		pass string
	}{
		{"wrong password", f.user, "wrong"},
		{"user refused", "blocked", f.pass}, // 530 in answer to USER
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := f.config()
			cfg.User, cfg.Password = tc.user, tc.pass
			s, err := New(&cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			_, err = s.Fetch(t.Context(), key("srv/a.jpg"), source.FetchOptions{})
			wantKind(t, err, errs.KindForbidden)
		})
	}
}

func TestConnectionRefused(t *testing.T) {
	t.Parallel()
	f := newFakeFTP(t, nil)
	cfg := f.config()
	_ = f.ln.Close()
	s, err := New(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.Fetch(t.Context(), key("a.jpg"), source.FetchOptions{})
	wantKind(t, err, errs.KindUpstreamError)
}

// TestCancelDuringBody proves that cancelling the fetch context unblocks a
// read from a server that stopped sending, and that the broken connection
// is not pooled.
func TestCancelDuringBody(t *testing.T) {
	t.Parallel()
	f := newFakeFTP(t, testFiles())
	s := newTestSource(t, f)

	ctx, cancel := context.WithCancel(t.Context())
	obj, err := s.Fetch(ctx, key("slow.jpg"), source.FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(obj.Body, make([]byte, 1024)); err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	cancel()
	_, err = io.ReadAll(obj.Body)
	wantKind(t, err, errs.KindCanceled)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want it to wrap context.Canceled", err)
	}
	_ = obj.Body.Close()

	obj, err = s.Fetch(t.Context(), key("a.jpg"), source.FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = readAll(t, obj)
	if n := f.loginCount(); n != 2 {
		t.Errorf("%d logins, want 2 (the canceled connection must be dropped)", n)
	}
}

func TestDeadlineDuringBody(t *testing.T) {
	t.Parallel()
	f := newFakeFTP(t, testFiles())
	s := newTestSource(t, f)
	// Ending the context with a DeadlineExceeded cause (what a fetch timeout
	// does) is reported as an upstream timeout, not a client cancel.
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	obj, err := s.Fetch(ctx, key("slow.jpg"), source.FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer obj.Body.Close()
	if _, err := io.ReadFull(obj.Body, make([]byte, 1024)); err != nil {
		t.Fatal(err)
	}
	cancel(context.DeadlineExceeded)
	_, err = io.ReadAll(obj.Body)
	wantKind(t, err, errs.KindUpstreamTimeout)
}

func TestCancelBeforeFetch(t *testing.T) {
	t.Parallel()
	f := newFakeFTP(t, testFiles())
	s := newTestSource(t, f)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := s.Fetch(ctx, key("a.jpg"), source.FetchOptions{})
	wantKind(t, err, errs.KindCanceled)
}

func TestPoolBound(t *testing.T) {
	t.Parallel()
	f := newFakeFTP(t, testFiles())
	s := newTestSource(t, f) // PoolSize 1

	held, err := s.Fetch(t.Context(), key("a.jpg"), source.FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// The only connection is leased: a fetch whose context is already done
	// fails instead of opening a second connection.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = s.Fetch(ctx, key("a.jpg"), source.FetchOptions{})
	wantKind(t, err, errs.KindCanceled)

	_ = readAll(t, held)
	obj, err := s.Fetch(t.Context(), key("dir/sub/b.png"), source.FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = readAll(t, obj)
	if n := f.loginCount(); n != 1 {
		t.Errorf("%d logins, want 1", n)
	}
}

func TestEarlyCloseDropsConnection(t *testing.T) {
	t.Parallel()
	f := newFakeFTP(t, testFiles())
	s := newTestSource(t, f)
	obj, err := s.Fetch(t.Context(), key("dir/sub/b.png"), source.FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(obj.Body, make([]byte, 10)); err != nil {
		t.Fatal(err)
	}
	if err := obj.Body.Close(); err != nil {
		t.Fatal(err)
	}
	obj, err = s.Fetch(t.Context(), key("a.jpg"), source.FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = readAll(t, obj)
	if n := f.loginCount(); n != 2 {
		t.Errorf("%d logins, want 2", n)
	}
}

// TestStaleIdleRetry: the server drops the pooled connection while it is
// idle; the next fetch retries once on a fresh connection.
func TestStaleIdleRetry(t *testing.T) {
	t.Parallel()
	f := newFakeFTP(t, testFiles())
	s := newTestSource(t, f)
	obj, err := s.Fetch(t.Context(), key("a.jpg"), source.FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = readAll(t, obj)
	f.dropAll()
	obj, err = s.Fetch(t.Context(), key("a.jpg"), source.FetchOptions{})
	if err != nil {
		t.Fatalf("fetch after idle drop: %v", err)
	}
	if got := readAll(t, obj); string(got) != "jpeg-a-bytes" {
		t.Fatalf("body %q", got)
	}
}

func TestNew(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{"ok", Config{Name: "a", Host: "ftp.example.com"}, ""},
		{"no name", Config{Host: "h"}, "empty name"},
		{"no host", Config{Name: "a"}, "bad host"},
		{"host with port", Config{Name: "a", Host: "h:21 x"}, "bad host"},
		{"bad port", Config{Name: "a", Host: "h", Port: 70000}, "bad port"},
		{"negative pool", Config{Name: "a", Host: "h", PoolSize: -1}, "negative"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, err := New(&tc.cfg)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				if s.Name() != tc.cfg.Name || s.Type() != source.TypeFTP || s.addr != "ftp.example.com:21" {
					t.Fatalf("got %s/%s at %s", s.Name(), s.Type(), s.addr)
				}
				return
			}
			if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want ErrInvalidConfig containing %q", err, tc.wantErr)
			}
		})
	}
}
