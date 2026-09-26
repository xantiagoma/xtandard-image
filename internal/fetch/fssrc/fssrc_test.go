package fssrc

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

var testMtime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// fixture lays out, under a fresh temp dir:
//
//	outside.png              (outside the root)
//	root/a.png               "hello"
//	root/dir/sub/b.jpg       "nested"
//	root/big.bin             100 bytes
//	root/emptydir/
//	root/link-in.png     ->  a.png
//	root/linkdir         ->  dir
//	root/link-out.png    ->  ../outside.png
//	root/link-abs.png    ->  <abs>/root/a.png
//
// and returns the root path.
func fixture(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "root")
	mustMkdir(t, filepath.Join(root, "dir", "sub"))
	mustMkdir(t, filepath.Join(root, "emptydir"))
	mustWrite(t, filepath.Join(base, "outside.png"), "secret")
	mustWrite(t, filepath.Join(root, "a.png"), "hello")
	mustWrite(t, filepath.Join(root, "dir", "sub", "b.jpg"), "nested")
	mustWrite(t, filepath.Join(root, "big.bin"), strings.Repeat("x", 100))
	mustSymlink(t, "a.png", filepath.Join(root, "link-in.png"))
	mustSymlink(t, "dir", filepath.Join(root, "linkdir"))
	mustSymlink(t, filepath.Join("..", "outside.png"), filepath.Join(root, "link-out.png"))
	mustSymlink(t, filepath.Join(root, "a.png"), filepath.Join(root, "link-abs.png"))
	return root
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o750); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, testMtime, testMtime); err != nil {
		t.Fatal(err)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func newSource(t *testing.T, root string, follow bool) *Source {
	t.Helper()
	s, err := New(Config{Name: "local", Root: root, FollowSymlinks: follow})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func key(p string) source.Key { return source.Key{Source: "local", Path: p} }

// readAll drains and closes obj.Body.
func readAll(t *testing.T, obj *source.Object) string {
	t.Helper()
	if obj.Body == nil {
		t.Fatal("nil Body")
	}
	defer func() { _ = obj.Body.Close() }()
	b, err := io.ReadAll(obj.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFetch(t *testing.T) {
	t.Parallel()
	root := fixture(t)
	strict := newSource(t, root, false)
	follow := newSource(t, root, true)

	tests := []struct {
		name     string
		src      *Source
		path     string
		wantKind errs.Kind // KindUnknown means success
		wantBody string
		wantType string
	}{
		{name: "normal file", src: strict, path: "a.png", wantBody: "hello", wantType: "image/png"},
		{name: "nested dirs", src: strict, path: "dir/sub/b.jpg", wantBody: "nested", wantType: "image/jpeg"},
		{name: "unknown extension", src: strict, path: "big.bin", wantBody: strings.Repeat("x", 100)},
		{name: "missing", src: strict, path: "nope.png", wantKind: errs.KindNotFound},
		{name: "missing dir", src: strict, path: "nope/x.png", wantKind: errs.KindNotFound},
		{name: "dotdot escape", src: strict, path: "../outside.png", wantKind: errs.KindForbidden},
		{name: "dotdot escape follow", src: follow, path: "../outside.png", wantKind: errs.KindForbidden},
		{name: "nested dotdot escape", src: follow, path: "dir/../../outside.png", wantKind: errs.KindForbidden},
		{name: "absolute path", src: follow, path: filepath.Join(filepath.Dir(root), "outside.png"), wantKind: errs.KindForbidden},
		{name: "absolute etc", src: follow, path: "/etc/passwd", wantKind: errs.KindForbidden},
		{name: "empty path", src: follow, path: "", wantKind: errs.KindForbidden},
		{name: "nul byte", src: follow, path: "a.png\x00.jpg", wantKind: errs.KindForbidden},
		{name: "directory", src: strict, path: "emptydir", wantKind: errs.KindNotFound},
		{name: "root itself", src: follow, path: ".", wantKind: errs.KindNotFound},
		{name: "symlink outside strict", src: strict, path: "link-out.png", wantKind: errs.KindForbidden},
		{name: "symlink outside follow", src: follow, path: "link-out.png", wantKind: errs.KindForbidden},
		{name: "absolute symlink follow", src: follow, path: "link-abs.png", wantKind: errs.KindForbidden},
		{name: "symlink inside strict", src: strict, path: "link-in.png", wantKind: errs.KindForbidden},
		{name: "symlink inside follow", src: follow, path: "link-in.png", wantBody: "hello", wantType: "image/png"},
		{name: "symlinked dir strict", src: strict, path: "linkdir/sub/b.jpg", wantKind: errs.KindForbidden},
		{name: "symlinked dir follow", src: follow, path: "linkdir/sub/b.jpg", wantBody: "nested", wantType: "image/jpeg"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			obj, err := tc.src.Fetch(t.Context(), key(tc.path), source.FetchOptions{})
			if tc.wantKind != errs.KindUnknown {
				if got := errs.KindOf(err); got != tc.wantKind {
					t.Fatalf("Fetch(%q) kind = %v (err %v), want %v", tc.path, got, err, tc.wantKind)
				}
				if obj != nil {
					t.Fatalf("Fetch(%q) returned object with error", tc.path)
				}
				return
			}
			if err != nil {
				t.Fatalf("Fetch(%q): %v", tc.path, err)
			}
			if obj.NotModified {
				t.Fatal("unexpected NotModified")
			}
			if got := readAll(t, obj); got != tc.wantBody {
				t.Errorf("body = %q, want %q", got, tc.wantBody)
			}
			if obj.Size != int64(len(tc.wantBody)) {
				t.Errorf("Size = %d, want %d", obj.Size, len(tc.wantBody))
			}
			if obj.ContentType != tc.wantType {
				t.Errorf("ContentType = %q, want %q", obj.ContentType, tc.wantType)
			}
			if !obj.LastModified.Equal(testMtime) {
				t.Errorf("LastModified = %v, want %v", obj.LastModified, testMtime)
			}
			if !strings.HasPrefix(obj.ETag, `W/"`) || !strings.HasSuffix(obj.ETag, `"`) {
				t.Errorf("ETag = %q, want a weak entity tag", obj.ETag)
			}
		})
	}
}

func TestFetchMaxBytes(t *testing.T) {
	t.Parallel()
	s := newSource(t, fixture(t), false)
	tests := []struct {
		name     string
		max      int64
		wantKind errs.Kind
	}{
		{name: "unlimited", max: 0},
		{name: "exact", max: 100},
		{name: "oversize", max: 99, wantKind: errs.KindSourceTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			obj, err := s.Fetch(t.Context(), key("big.bin"), source.FetchOptions{MaxBytes: tc.max})
			if got := errs.KindOf(err); got != tc.wantKind {
				t.Fatalf("kind = %v (err %v), want %v", got, err, tc.wantKind)
			}
			if err == nil {
				readAll(t, obj)
			}
		})
	}
}

func TestFetchConditional(t *testing.T) {
	t.Parallel()
	s := newSource(t, fixture(t), false)
	first, err := s.Fetch(t.Context(), key("a.png"), source.FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	readAll(t, first)

	tests := []struct {
		name string
		opt  source.FetchOptions
		want bool // NotModified
	}{
		{name: "none", opt: source.FetchOptions{}, want: false},
		{name: "ims equal", opt: source.FetchOptions{IfModifiedSince: testMtime}, want: true},
		{name: "ims later", opt: source.FetchOptions{IfModifiedSince: testMtime.Add(time.Hour)}, want: true},
		{name: "ims earlier", opt: source.FetchOptions{IfModifiedSince: testMtime.Add(-time.Second)}, want: false},
		{name: "inm match", opt: source.FetchOptions{IfNoneMatch: first.ETag}, want: true},
		{name: "inm strong form", opt: source.FetchOptions{IfNoneMatch: strings.TrimPrefix(first.ETag, "W/")}, want: true},
		{name: "inm list", opt: source.FetchOptions{IfNoneMatch: `"x", ` + first.ETag}, want: true},
		{name: "inm star", opt: source.FetchOptions{IfNoneMatch: "*"}, want: true},
		{
			name: "inm mismatch beats ims",
			opt:  source.FetchOptions{IfNoneMatch: `W/"other"`, IfModifiedSince: testMtime},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			obj, err := s.Fetch(t.Context(), key("a.png"), tc.opt)
			if err != nil {
				t.Fatal(err)
			}
			if obj.NotModified != tc.want {
				t.Fatalf("NotModified = %v, want %v", obj.NotModified, tc.want)
			}
			if tc.want {
				if obj.Body != nil {
					t.Fatal("NotModified object has a Body")
				}
				if obj.ETag != first.ETag || !obj.LastModified.Equal(testMtime) {
					t.Errorf("validators = %q %v, want %q %v", obj.ETag, obj.LastModified, first.ETag, testMtime)
				}
				return
			}
			readAll(t, obj)
		})
	}
}

func TestETag(t *testing.T) {
	t.Parallel()
	root := fixture(t)
	p := filepath.Join(root, "a.png")
	etagOf := func(s *Source) string {
		t.Helper()
		obj, err := s.Fetch(t.Context(), key("a.png"), source.FetchOptions{})
		if err != nil {
			t.Fatal(err)
		}
		readAll(t, obj)
		return obj.ETag
	}
	s := newSource(t, root, false)
	e1 := etagOf(s)
	if e2 := etagOf(s); e2 != e1 {
		t.Fatalf("ETag not stable across fetches: %q vs %q", e1, e2)
	}
	if e3 := etagOf(newSource(t, root, false)); e3 != e1 {
		t.Fatalf("ETag not stable across sources: %q vs %q", e1, e3)
	}

	later := testMtime.Add(time.Minute)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	e4 := etagOf(s)
	if e4 == e1 {
		t.Fatal("ETag unchanged after mtime change")
	}
	mustWrite(t, p, "hello, world") // new size, same mtime as e1
	if e5 := etagOf(s); e5 == e1 || e5 == e4 {
		t.Fatalf("ETag unchanged after rewrite: %q", e5)
	}
}

func TestFetchPermissionDenied(t *testing.T) {
	t.Parallel()
	root := fixture(t)
	p := filepath.Join(root, "a.png")
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	// root (and CAP_DAC_OVERRIDE, as in some containers) ignores mode bits.
	if f, err := os.Open(p); err == nil {
		_ = f.Close()
		t.Skip("mode bits are not enforced for this user")
	}
	s := newSource(t, root, false)
	_, err := s.Fetch(t.Context(), key("a.png"), source.FetchOptions{})
	if got := errs.KindOf(err); got != errs.KindForbidden {
		t.Fatalf("kind = %v (err %v), want Forbidden", got, err)
	}
}

func TestFetchCanceled(t *testing.T) {
	t.Parallel()
	s := newSource(t, fixture(t), false)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := s.Fetch(ctx, key("a.png"), source.FetchOptions{})
	if got := errs.KindOf(err); got != errs.KindCanceled {
		t.Fatalf("kind = %v (err %v), want Canceled", got, err)
	}
}

func TestNew(t *testing.T) {
	t.Parallel()
	root := fixture(t)
	s := newSource(t, root, false)
	if diff := cmp.Diff([]string{"local", source.TypeFS}, []string{s.Name(), s.Type()}); diff != "" {
		t.Errorf("Name/Type (-want +got):\n%s", diff)
	}
	if s.escapeErr == nil {
		t.Error("escape error not captured")
	}
	for _, bad := range []string{"", filepath.Join(root, "missing"), filepath.Join(root, "a.png")} {
		if _, err := New(Config{Name: "local", Root: bad}); err == nil {
			t.Errorf("New(%q) succeeded, want error", bad)
		}
	}
}

func TestLocalName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "a.png", want: "a.png"},
		{in: "a/b/c.png", want: filepath.Join("a", "b", "c.png")},
		{in: "a//b/./c.png", want: filepath.Join("a", "b", "c.png")},
		{in: "", wantErr: true},
		{in: "/a.png", wantErr: true},
		{in: "..", wantErr: true},
		{in: "../a.png", wantErr: true},
		{in: "a/../../b", wantErr: true},
		{in: "a\x00b", wantErr: true},
	}
	for _, tc := range tests {
		got, err := localName(tc.in)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("localName(%q) = %q, %v; want %q, err=%v", tc.in, got, err, tc.want, tc.wantErr)
		}
	}
}

func FuzzLocalName(f *testing.F) {
	for _, s := range []string{"a.png", "a/b.png", "../x", "/etc/passwd", "a/../../b", "./a", "a\x00"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, p string) {
		name, err := localName(p)
		if err != nil {
			return
		}
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) ||
			strings.IndexByte(name, 0) >= 0 {
			t.Fatalf("localName(%q) = %q escapes", p, name)
		}
	})
}
