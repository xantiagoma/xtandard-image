package sftpsrc

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

func testFiles() map[string]memFile {
	return map[string]memFile{
		"/srv/a.jpg":         {data: []byte("jpeg-a-bytes"), mtime: testMtime},
		"/srv/dir/sub/b.png": {data: bytes.Repeat([]byte("b"), 100_000), mtime: testMtime},
		"/srv/dir":           {dir: true, mtime: testMtime},
		"/srv/secret.jpg":    {data: []byte("x"), mtime: testMtime, denied: true},
		"/srv/slow.jpg":      {data: bytes.Repeat([]byte("s"), 1<<16), mtime: testMtime, stallAfter: 1024},
		"/srv/big.jpg":       {data: bytes.Repeat([]byte("z"), 1000), mtime: testMtime},
	}
}

func newTestSource(t *testing.T, cfg *Config) *Source {
	t.Helper()
	cfg.Root = "/srv"
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func key(p string) source.Key { return source.Key{Source: "sftp-test", Path: p} }

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
	f := newFakeSFTP(t, files, nil)
	s := newTestSource(t, new(f.config()))

	tests := []struct {
		name  string
		path  string
		opt   source.FetchOptions
		kind  errs.Kind // zero: the fetch succeeds
		body  []byte
		notMd bool
	}{
		{name: "get", path: "a.jpg", body: files["/srv/a.jpg"].data},
		{name: "nested", path: "dir/sub/b.png", body: files["/srv/dir/sub/b.png"].data},
		{name: "missing", path: "nope.jpg", kind: errs.KindNotFound},
		{name: "directory", path: "dir", kind: errs.KindNotFound},
		{name: "permission denied", path: "secret.jpg", kind: errs.KindForbidden},
		{name: "oversize", path: "big.jpg", opt: source.FetchOptions{MaxBytes: 999}, kind: errs.KindSourceTooLarge},
		{name: "exactly max", path: "big.jpg", opt: source.FetchOptions{MaxBytes: 1000}, body: files["/srv/big.jpg"].data},
		{name: "if-modified-since equal", path: "a.jpg", opt: source.FetchOptions{IfModifiedSince: testMtime}, notMd: true},
		{name: "if-modified-since older", path: "a.jpg", opt: source.FetchOptions{IfModifiedSince: testMtime.Add(-time.Second)}, body: files["/srv/a.jpg"].data},
		{name: "if-none-match", path: "a.jpg", opt: source.FetchOptions{IfNoneMatch: filemeta.ETag(12, testMtime)}, notMd: true},
		{name: "if-none-match stale", path: "a.jpg", opt: source.FetchOptions{IfNoneMatch: `W/"c-1"`}, body: files["/srv/a.jpg"].data},
	}
	// Every case shares the single pooled connection (PoolSize 1).
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
			if !obj.LastModified.Equal(testMtime) || obj.ETag == "" {
				t.Errorf("LastModified = %v, ETag = %q", obj.LastModified, obj.ETag)
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

func TestAuth(t *testing.T) {
	t.Parallel()
	otherSigner := newSigner(t)
	f := newFakeSFTP(t, testFiles(), clientPublicKey(t))
	base := f.config()

	tests := []struct {
		name   string
		mutate func(*Config)
		kind   errs.Kind // zero: the fetch succeeds
		is     error
	}{
		{name: "password", mutate: func(*Config) {}},
		{name: "private key", mutate: func(c *Config) {
			c.Password, c.PrivateKey = "", testdataFile(t, "id_ed25519")
		}},
		{name: "encrypted private key", mutate: func(c *Config) {
			c.Password, c.PrivateKey, c.PrivateKeyPassphrase = "", testdataFile(t, "id_ed25519_enc"), "pw"
		}},
		{name: "known_hosts form", mutate: func(c *Config) {
			c.HostKey = "# pinned\n[127.0.0.1]:22 " + c.HostKey + "\n"
		}},
		{name: "rotation: second pinned key matches", mutate: func(c *Config) {
			c.HostKey = authorizedKey(otherSigner.PublicKey()) + "\n" + c.HostKey
		}},
		{name: "wrong password", mutate: func(c *Config) { c.Password = "wrong" }, kind: errs.KindForbidden},
		{name: "wrong user", mutate: func(c *Config) { c.User = "mallory" }, kind: errs.KindForbidden},
		{name: "unknown host key", mutate: func(c *Config) {
			c.HostKey = authorizedKey(otherSigner.PublicKey())
		}, kind: errs.KindUpstreamError, is: ErrHostKeyMismatch},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := base
			tc.mutate(&cfg)
			s := newTestSource(t, &cfg)
			obj, err := s.Fetch(t.Context(), key("a.jpg"), source.FetchOptions{})
			if tc.kind == errs.KindUnknown {
				if err != nil {
					t.Fatal(err)
				}
				if got := readAll(t, obj); string(got) != "jpeg-a-bytes" {
					t.Fatalf("body %q", got)
				}
				return
			}
			wantKind(t, err, tc.kind)
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Fatalf("err = %v, want it to wrap %v", err, tc.is)
			}
		})
	}
}

// cancelOrder is one order of a cancellation during the body: the next
// read already waiting on the server when the context's AfterFunc closes
// the connection, or issued only after it did. The second is a send on a
// closed connection, which pkg/sftp reports as a wrapped io.EOF that Read
// once took for a clean end of file; which order happened depended on
// scheduling, so closedFirst forces it.
type cancelOrder struct {
	name        string
	closedFirst bool
}

func cancelOrders() []cancelOrder {
	return []cancelOrder{{"read in flight", false}, {"connection already closed", true}}
}

// cancelBody runs cancel and, when closedFirst, applies the context's
// AfterFunc at once instead of waiting for its goroutine (abort is
// idempotent and keeps the first cause).
func cancelBody(ctx context.Context, obj *source.Object, cancel func(), closedFirst bool) {
	cancel()
	if closedFirst {
		obj.Body.(*body).c.abort(context.Cause(ctx)) //nolint:forcetypeassert // this package's body
	}
}

func TestCancelDuringBody(t *testing.T) {
	t.Parallel()
	for _, tc := range cancelOrders() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFakeSFTP(t, testFiles(), nil)
			s := newTestSource(t, new(f.config()))

			ctx, cancel := context.WithCancel(t.Context())
			obj, err := s.Fetch(ctx, key("slow.jpg"), source.FetchOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.ReadFull(obj.Body, make([]byte, 1024)); err != nil {
				t.Fatalf("first chunk: %v", err)
			}
			cancelBody(ctx, obj, cancel, tc.closedFirst)
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
		})
	}
}

func TestDeadlineDuringBody(t *testing.T) {
	t.Parallel()
	for _, tc := range cancelOrders() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFakeSFTP(t, testFiles(), nil)
			s := newTestSource(t, new(f.config()))
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
			cancelBody(ctx, obj, func() { cancel(context.DeadlineExceeded) }, tc.closedFirst)
			_, err = io.ReadAll(obj.Body)
			wantKind(t, err, errs.KindUpstreamTimeout)
		})
	}
}

func TestCancelBeforeFetch(t *testing.T) {
	t.Parallel()
	f := newFakeSFTP(t, testFiles(), nil)
	s := newTestSource(t, new(f.config()))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := s.Fetch(ctx, key("a.jpg"), source.FetchOptions{})
	wantKind(t, err, errs.KindCanceled)
}

func TestPoolBound(t *testing.T) {
	t.Parallel()
	f := newFakeSFTP(t, testFiles(), nil)
	s := newTestSource(t, new(f.config())) // PoolSize 1

	held, err := s.Fetch(t.Context(), key("a.jpg"), source.FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
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
	f := newFakeSFTP(t, testFiles(), nil)
	s := newTestSource(t, new(f.config()))
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

func TestStaleIdleRetry(t *testing.T) {
	t.Parallel()
	f := newFakeSFTP(t, testFiles(), nil)
	s := newTestSource(t, new(f.config()))
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
	hostKey := newSigner(t)
	pin := authorizedKey(hostKey.PublicKey())
	ok := Config{Name: "a", Host: "sftp.example.com", User: "u", Password: "p", HostKey: pin}
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"ok", func(*Config) {}, ""},
		{"no host key (no TOFU)", func(c *Config) { c.HostKey = "" }, "host key is required"},
		{"comment-only host key", func(c *Config) { c.HostKey = "# nothing\n" }, "host key is required"},
		{"garbage host key", func(c *Config) { c.HostKey = "ssh-ed25519 !!!" }, "bad host key"},
		{"no credentials", func(c *Config) { c.Password = "" }, "password or private key"},
		{"bad private key", func(c *Config) { c.PrivateKey = "nope" }, "private key"},
		{"wrong passphrase", func(c *Config) {
			c.PrivateKey, c.PrivateKeyPassphrase = testdataFile(t, "id_ed25519_enc"), "nope"
		}, "private key"},
		{"encrypted key without passphrase", func(c *Config) { c.PrivateKey = testdataFile(t, "id_ed25519_enc") }, "private key"},
		{"no user", func(c *Config) { c.User = "" }, "empty user"},
		{"no name", func(c *Config) { c.Name = "" }, "empty name"},
		{"bad host", func(c *Config) { c.Host = "" }, "bad host"},
		{"bad port", func(c *Config) { c.Port = -1 }, "bad port"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := ok
			tc.mutate(&cfg)
			s, err := New(&cfg)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				if s.Name() != "a" || s.Type() != source.TypeSFTP || s.addr != "sftp.example.com:22" {
					t.Fatalf("got %s/%s at %s", s.Name(), s.Type(), s.addr)
				}
				if diff := cmp.Diff([]string{"ssh-ed25519"}, s.ssh.HostKeyAlgorithms); diff != "" {
					t.Errorf("HostKeyAlgorithms (-want +got):\n%s", diff)
				}
				return
			}
			if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want ErrInvalidConfig containing %q", err, tc.wantErr)
			}
		})
	}
}
