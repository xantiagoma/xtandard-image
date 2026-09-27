package source

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

type fakeSource struct{ name, typ string }

func (f fakeSource) Name() string { return f.name }
func (f fakeSource) Type() string { return f.typ }
func (f fakeSource) Fetch(context.Context, Key, FetchOptions) (*Object, error) {
	return nil, errs.New(errs.KindNotFound, "fake")
}

func bind(s *Spec) Binding { return Binding{Spec: *s, Source: fakeSource{s.Name, s.Type}} }

// testSpecs is a configuration exercising every source type.
func testSpecs() []Binding {
	return []Binding{
		bind(&Spec{Name: "origin", Type: TypeHTTP, Allow: []string{"cdn.example.com", "*.example.org/images/", "https://secure.example.net", "img-*.example.io:8443"}}),
		bind(&Spec{Name: "web", Type: TypeHTTP, BaseURL: "https://www.example.com/static"}),
		bind(&Spec{Name: "assets", Type: TypeS3, Buckets: []string{"assets", "tenant-*"}, Bucket: "assets", Prefix: "public"}),
		bind(&Spec{Name: "archive", Type: TypeS3, Buckets: []string{"assets"}}),
		bind(&Spec{Name: "gcs", Type: TypeGCS, Bucket: "gbucket"}),
		bind(&Spec{Name: "blobs", Type: TypeAzure, Buckets: []string{"media"}, Bucket: "media"}),
		bind(&Spec{Name: "local", Type: TypeFS}),
		bind(&Spec{Name: "jailed", Type: TypeFS, Prefix: "/uploads/"}),
		bind(&Spec{Name: "ftp", Type: TypeFTP, Host: "FTP.example.com", Prefix: "pub"}),
		bind(&Spec{Name: "sftp", Type: TypeSFTP, Host: "files.example.com", Port: 2222}),
	}
}

func newTestRegistry(t *testing.T, dev bool) *Registry {
	t.Helper()
	opts := Options{Sources: testSpecs(), DevMode: dev}
	if dev {
		opts.Dev = fakeSource{DevSourceName, TypeHTTP}
	}
	r, err := NewRegistry(opts)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return r
}

// TestResolveOrder is the resolution-order table: every scheme and alias
// form, mount precedence, and the unmatched → Forbidden rule.
func TestResolveOrder(t *testing.T) {
	t.Parallel()
	r := newTestRegistry(t, false)
	tests := []struct {
		name  string
		raw   string
		mount string
		want  Key
		kind  errs.Kind // zero value means success
	}{
		// 1. xi:// explicit
		{name: "xi s3 uses default bucket and prefix", raw: "xi://assets/a/b.jpg", want: Key{Source: "assets", Bucket: "assets", Path: "public/a/b.jpg"}},
		{name: "xi s3 relative version", raw: "xi://assets/a.jpg?versionId=v1", want: Key{Source: "assets", Bucket: "assets", Path: "public/a.jpg", Version: "v1"}},
		{name: "xi gcs generation", raw: "xi://gcs/a.jpg?generation=5", want: Key{Source: "gcs", Bucket: "gbucket", Path: "a.jpg", Version: "5"}},
		{name: "xi http joins base url", raw: "xi://web/a%20b.jpg?v=2", want: Key{Source: "web", Path: "https://www.example.com/static/a%20b.jpg?v=2"}},
		{name: "xi fs", raw: "xi://local/a.png", want: Key{Source: "local", Path: "a.png"}},
		{name: "xi fs jailed prepends prefix", raw: "xi://jailed/a.png", want: Key{Source: "jailed", Path: "uploads/a.png"}},
		{name: "xi ftp", raw: "xi://ftp/a.png", want: Key{Source: "ftp", Path: "pub/a.png"}},
		{name: "xi overrides mount", raw: "xi://gcs/a.jpg", mount: "assets", want: Key{Source: "gcs", Bucket: "gbucket", Path: "a.jpg"}},
		{name: "xi unknown source", raw: "xi://nope/a.jpg", kind: errs.KindForbidden},
		{name: "xi http without base", raw: "xi://origin/a.jpg", kind: errs.KindForbidden},
		{name: "xi s3 without default bucket", raw: "xi://archive/a.jpg", kind: errs.KindForbidden},
		// 2. storage schemes
		{name: "s3 first matching source", raw: "s3://assets/public/a.jpg", want: Key{Source: "assets", Bucket: "assets", Path: "public/a.jpg"}},
		{name: "s3 prefix jail falls through to next source", raw: "s3://assets/private/a.jpg", want: Key{Source: "archive", Bucket: "assets", Path: "private/a.jpg"}},
		{name: "s3 mount source tried first", raw: "s3://assets/public/a.jpg", mount: "archive", want: Key{Source: "archive", Bucket: "assets", Path: "public/a.jpg"}},
		{name: "s3 mount of other type ignored", raw: "s3://assets/public/a.jpg", mount: "local", want: Key{Source: "assets", Bucket: "assets", Path: "public/a.jpg"}},
		{name: "s3 bucket glob", raw: "s3://tenant-42/public/a.jpg?versionId=x", want: Key{Source: "assets", Bucket: "tenant-42", Path: "public/a.jpg", Version: "x"}},
		{name: "s3 bucket glob no match", raw: "s3://tenant/public/a.jpg", kind: errs.KindForbidden},
		{name: "s3 bucket case-sensitive", raw: "s3://Assets/public/a.jpg", kind: errs.KindForbidden},
		{name: "s3 bucket not allowed", raw: "s3://other/public/a.jpg", kind: errs.KindForbidden},
		{name: "s3 prefix jail case-sensitive", raw: "s3://tenant-1/Public/a.jpg", kind: errs.KindForbidden},
		{name: "s3 prefix is a directory", raw: "s3://tenant-1/public-x/a.jpg", kind: errs.KindForbidden},
		{name: "gs", raw: "gs://gbucket/a.jpg?generation=9", want: Key{Source: "gcs", Bucket: "gbucket", Path: "a.jpg", Version: "9"}},
		{name: "gs bucket not allowed", raw: "gs://assets/a.jpg", kind: errs.KindForbidden},
		{name: "abs", raw: "abs://media/x/y.jpg", want: Key{Source: "blobs", Bucket: "media", Path: "x/y.jpg"}},
		{name: "az alias", raw: "az://media/x/y.jpg", want: Key{Source: "blobs", Bucket: "media", Path: "x/y.jpg"}},
		{name: "abs container not allowed", raw: "abs://other/y.jpg", kind: errs.KindForbidden},
		{name: "local", raw: "local:///img/a.png", want: Key{Source: "local", Path: "img/a.png"}},
		{name: "local collapsed", raw: "local:/img/a.png", want: Key{Source: "local", Path: "img/a.png"}},
		{name: "file alias", raw: "file:///img/a.png", want: Key{Source: "local", Path: "img/a.png"}},
		{name: "local mount jailed first", raw: "local:///uploads/a.png", mount: "jailed", want: Key{Source: "jailed", Path: "uploads/a.png"}},
		{name: "local mount jail miss falls through", raw: "local:///img/a.png", mount: "jailed", want: Key{Source: "local", Path: "img/a.png"}},
		{name: "ftp host case-insensitive", raw: "ftp://ftp.EXAMPLE.com/pub/a.jpg", want: Key{Source: "ftp", Path: "pub/a.jpg"}},
		{name: "ftp default port", raw: "ftp://ftp.example.com:21/pub/a.jpg", want: Key{Source: "ftp", Path: "pub/a.jpg"}},
		{name: "ftp outside jail", raw: "ftp://ftp.example.com/etc/a.jpg", kind: errs.KindForbidden},
		{name: "ftp wrong host", raw: "ftp://evil.com/pub/a.jpg", kind: errs.KindForbidden},
		{name: "ftp wrong port", raw: "ftp://ftp.example.com:2121/pub/a.jpg", kind: errs.KindForbidden},
		{name: "sftp port", raw: "sftp://files.example.com:2222/a.jpg", want: Key{Source: "sftp", Path: "a.jpg"}},
		{name: "sftp default port not configured one", raw: "sftp://files.example.com/a.jpg", kind: errs.KindForbidden},
		// 3. http(s)
		{name: "http exact host", raw: "https://cdn.example.com/a.jpg", want: Key{Source: "origin", Path: "https://cdn.example.com/a.jpg"}},
		{name: "http host case-insensitive", raw: "HTTP://CDN.EXAMPLE.COM/A.jpg?Q=1", want: Key{Source: "origin", Path: "http://cdn.example.com/A.jpg?Q=1"}},
		{name: "http host any port", raw: "https://cdn.example.com:8443/a.jpg", want: Key{Source: "origin", Path: "https://cdn.example.com:8443/a.jpg"}},
		{name: "http glob subdomain", raw: "https://a.b.example.org/images/x.jpg", want: Key{Source: "origin", Path: "https://a.b.example.org/images/x.jpg"}},
		{name: "http glob apex not matched", raw: "https://example.org/images/x.jpg", kind: errs.KindForbidden},
		{name: "http glob path prefix", raw: "https://a.example.org/other/x.jpg", kind: errs.KindForbidden},
		{name: "http path prefix case-sensitive", raw: "https://a.example.org/IMAGES/x.jpg", kind: errs.KindForbidden},
		{name: "http path prefix is a directory", raw: "https://a.example.org/images-private/x.jpg", kind: errs.KindForbidden},
		{name: "http encoded prefix normalised", raw: "https://a.example.org/%69mages/x.jpg", want: Key{Source: "origin", Path: "https://a.example.org/images/x.jpg"}},
		{name: "http encoded traversal", raw: "https://a.example.org/images/%2E%2E/secret.jpg", kind: errs.KindForbidden},
		{name: "http scheme-restricted pattern", raw: "https://secure.example.net/a.jpg", want: Key{Source: "origin", Path: "https://secure.example.net/a.jpg"}},
		{name: "http scheme-restricted pattern rejects http", raw: "http://secure.example.net/a.jpg", kind: errs.KindForbidden},
		{name: "http port pattern", raw: "https://img-7.example.io:8443/a.jpg", want: Key{Source: "origin", Path: "https://img-7.example.io:8443/a.jpg"}},
		{name: "http port pattern wrong port", raw: "https://img-7.example.io/a.jpg", kind: errs.KindForbidden},
		{name: "http label glob one label only", raw: "https://img-7.x.example.io:8443/a.jpg", kind: errs.KindForbidden},
		{name: "http suffix trick", raw: "https://cdn.example.com.evil.com/a.jpg", kind: errs.KindForbidden},
		{name: "http base url implicitly allowed", raw: "https://www.example.com/static/a.jpg", want: Key{Source: "web", Path: "https://www.example.com/static/a.jpg"}},
		{name: "http outside base url", raw: "https://www.example.com/private/a.jpg", kind: errs.KindForbidden},
		{name: "http mount preferred", raw: "https://www.example.com/static/a.jpg", mount: "web", want: Key{Source: "web", Path: "https://www.example.com/static/a.jpg"}},
		{name: "http unmatched", raw: "https://evil.com/a.jpg", kind: errs.KindForbidden},
		{name: "http unmatched with mount", raw: "https://evil.com/a.jpg", mount: "origin", kind: errs.KindForbidden},
		// 4. relative
		{name: "relative s3 mount", raw: "a/b.jpg", mount: "assets", want: Key{Source: "assets", Bucket: "assets", Path: "public/a/b.jpg"}},
		{name: "relative http mount", raw: "/a.jpg", mount: "web", want: Key{Source: "web", Path: "https://www.example.com/static/a.jpg"}},
		{name: "relative azure mount", raw: "a.jpg", mount: "blobs", want: Key{Source: "blobs", Bucket: "media", Path: "a.jpg"}},
		{name: "relative sftp mount", raw: "a.jpg", mount: "sftp", want: Key{Source: "sftp", Path: "a.jpg"}},
		{name: "relative without mount", raw: "a.jpg", kind: errs.KindForbidden},
		{name: "relative unknown mount", raw: "a.jpg", mount: "nope", kind: errs.KindForbidden},
		{name: "relative traversal", raw: "a/../../b.jpg", mount: "assets", kind: errs.KindForbidden},
		{name: "relative encoded traversal", raw: "%2e%2e/b.jpg", mount: "local", kind: errs.KindForbidden},
		// malformed
		{name: "invalid", raw: "https://user@cdn.example.com/a.jpg", kind: errs.KindInvalidURL},
		{name: "unsupported scheme", raw: "swift://c/o", kind: errs.KindInvalidURL},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.Resolve(transform.SourceSpec{Raw: tc.raw, Mount: tc.mount})
			if tc.kind != errs.KindUnknown {
				if err == nil {
					t.Fatalf("Resolve(%q) = %+v, want %v", tc.raw, got, tc.kind)
				}
				if !errors.Is(err, errs.Match(tc.kind)) {
					t.Fatalf("Resolve(%q) error %v, want kind %v", tc.raw, err, tc.kind)
				}
				if tc.kind == errs.KindForbidden && !errors.Is(err, ErrNotAllowed) && !errors.Is(err, ErrTraversal) {
					t.Errorf("Forbidden error %v lacks ErrNotAllowed/ErrTraversal", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve(%q): %v", tc.raw, err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("Resolve(%q, mount %q) mismatch (-want +got):\n%s", tc.raw, tc.mount, diff)
			}
			if src, ok := r.Source(got.Source); !ok || src.Name() != got.Source {
				t.Errorf("Source(%q) = %v, %v", got.Source, src, ok)
			}
		})
	}
}

func TestResolveDevMode(t *testing.T) {
	t.Parallel()
	dev := newTestRegistry(t, true)
	prod := newTestRegistry(t, false)
	tests := []struct {
		name string
		raw  string
		want Key
		kind errs.Kind
	}{
		{name: "unmatched http uses implicit source", raw: "http://anything.test/a.jpg", want: Key{Source: DevSourceName, Path: "http://anything.test/a.jpg", Dev: true}},
		{name: "unmatched https uses implicit source", raw: "https://Evil.com:8443/a.jpg?x", want: Key{Source: DevSourceName, Path: "https://evil.com:8443/a.jpg?x", Dev: true}},
		{name: "loopback still resolves; SSRF policy blocks it at dial time", raw: "http://127.0.0.1/a.jpg", want: Key{Source: DevSourceName, Path: "http://127.0.0.1/a.jpg", Dev: true}},
		{name: "configured source still wins", raw: "https://cdn.example.com/a.jpg", want: Key{Source: "origin", Path: "https://cdn.example.com/a.jpg"}},
		{name: "traversal still rejected", raw: "https://evil.com/a/../b", kind: errs.KindForbidden},
		{name: "userinfo still invalid", raw: "https://u@evil.com/a", kind: errs.KindInvalidURL},
		{name: "no implicit s3", raw: "s3://other/a.jpg", kind: errs.KindForbidden},
		{name: "no implicit xi", raw: "xi://nope/a.jpg", kind: errs.KindForbidden},
		{name: "relative still needs a mount", raw: "a.jpg", kind: errs.KindForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := dev.Resolve(transform.SourceSpec{Raw: tc.raw})
			if tc.kind != errs.KindUnknown {
				if !errors.Is(err, errs.Match(tc.kind)) {
					t.Fatalf("Resolve(%q) = %+v, %v; want kind %v", tc.raw, got, err, tc.kind)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve(%q): %v", tc.raw, err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("mismatch (-want +got):\n%s", diff)
			}
			if got.Dev {
				if _, err := prod.Resolve(transform.SourceSpec{Raw: tc.raw}); !errors.Is(err, errs.Match(errs.KindForbidden)) {
					t.Errorf("without dev mode Resolve(%q) error = %v, want Forbidden", tc.raw, err)
				}
			}
		})
	}
	if src, ok := dev.Source(DevSourceName); !ok || src.Type() != TypeHTTP {
		t.Errorf("dev Source(%q) = %v, %v", DevSourceName, src, ok)
	}
	if _, ok := prod.Source(DevSourceName); ok {
		t.Error("Source(DevSourceName) available without dev mode")
	}
}

func TestDevModeOnlyRegistry(t *testing.T) {
	t.Parallel()
	r, err := NewRegistry(Options{DevMode: true, Dev: fakeSource{DevSourceName, TypeHTTP}})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	k, err := r.Resolve(transform.SourceSpec{Raw: "https://x.test/a.jpg"})
	if err != nil || !k.Dev || k.Source != DevSourceName {
		t.Fatalf("Resolve = %+v, %v", k, err)
	}
}

func TestResolveRedirect(t *testing.T) {
	t.Parallel()
	r := newTestRegistry(t, true)
	origin := Key{Source: "origin", Path: "https://cdn.example.com/a.jpg"}
	plain := Key{Source: "origin", Path: "http://cdn.example.com/a.jpg"}
	devKey := Key{Source: DevSourceName, Path: "https://x.test/a.jpg", Dev: true}
	tests := []struct {
		name     string
		from     Key
		location string
		want     Key
		kind     errs.Kind
	}{
		{name: "same source allowed", from: origin, location: "https://a.example.org/images/b.jpg", want: Key{Source: "origin", Path: "https://a.example.org/images/b.jpg"}},
		{name: "upgrade allowed", from: plain, location: "https://cdn.example.com/a.jpg", want: origin},
		{name: "downgrade refused", from: origin, location: "http://cdn.example.com/a.jpg", kind: errs.KindForbidden},
		{name: "other source's host refused", from: origin, location: "https://www.example.com/static/a.jpg", kind: errs.KindForbidden},
		{name: "unlisted host refused", from: origin, location: "https://evil.com/a.jpg", kind: errs.KindForbidden},
		{name: "non-http refused", from: origin, location: "s3://assets/public/a.jpg", kind: errs.KindForbidden},
		{name: "relative location refused", from: origin, location: "/b.jpg", kind: errs.KindForbidden},
		{name: "malformed", from: origin, location: "https://u@x/a", kind: errs.KindInvalidURL},
		{name: "dev any host", from: devKey, location: "https://y.test/b.jpg", want: Key{Source: DevSourceName, Path: "https://y.test/b.jpg", Dev: true}},
		{name: "dev downgrade refused", from: devKey, location: "http://y.test/b.jpg", kind: errs.KindForbidden},
		{name: "forged dev key", from: Key{Source: "origin", Path: "https://x.test/", Dev: true}, location: "https://y.test/b.jpg", kind: errs.KindForbidden},
		{name: "non-http source", from: Key{Source: "assets", Bucket: "assets", Path: "public/a"}, location: "https://cdn.example.com/a.jpg", kind: errs.KindForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.ResolveRedirect(tc.from, tc.location)
			if tc.kind != errs.KindUnknown {
				if !errors.Is(err, errs.Match(tc.kind)) {
					t.Fatalf("ResolveRedirect = %+v, %v; want kind %v", got, err, tc.kind)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveRedirect: %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("mismatch (-want +got):\n%s", diff)
			}
		})
	}
	prod := newTestRegistry(t, false)
	if _, err := prod.ResolveRedirect(devKey, "https://y.test/"); !errors.Is(err, errs.Match(errs.KindForbidden)) {
		t.Errorf("dev key redirect outside dev mode: %v", err)
	}
}

func TestNewRegistryErrors(t *testing.T) {
	t.Parallel()
	good := bind(&Spec{Name: "ok", Type: TypeFS})
	tests := []struct {
		name string
		opts Options
	}{
		{"no sources no dev", Options{}},
		{"dev without dev source", Options{DevMode: true}},
		{"dev source wrong type", Options{DevMode: true, Dev: fakeSource{DevSourceName, TypeS3}}},
		{"bad name", Options{Sources: []Binding{bind(&Spec{Name: "Bad_Name", Type: TypeFS})}}},
		{"reserved dev name", Options{Sources: []Binding{bind(&Spec{Name: DevSourceName, Type: TypeHTTP, Allow: []string{"a.com"}})}}},
		{"duplicate", Options{Sources: []Binding{good, good}}},
		{"no backend", Options{Sources: []Binding{{Spec: Spec{Name: "x", Type: TypeFS}}}}},
		{"backend mismatch", Options{Sources: []Binding{{Spec: Spec{Name: "x", Type: TypeFS}, Source: fakeSource{"x", TypeS3}}}}},
		{"unknown type", Options{Sources: []Binding{bind(&Spec{Name: "x", Type: "swift"})}}},
		{"http without allow", Options{Sources: []Binding{bind(&Spec{Name: "x", Type: TypeHTTP})}}},
		{"http bad pattern", Options{Sources: []Binding{bind(&Spec{Name: "x", Type: TypeHTTP, Allow: []string{"a[b].com"}})}}},
		{"http bad pattern scheme", Options{Sources: []Binding{bind(&Spec{Name: "x", Type: TypeHTTP, Allow: []string{"ftp://a.com"}})}}},
		{"http bad pattern port", Options{Sources: []Binding{bind(&Spec{Name: "x", Type: TypeHTTP, Allow: []string{"a.com:x"}})}}},
		{"http bad pattern userinfo", Options{Sources: []Binding{bind(&Spec{Name: "x", Type: TypeHTTP, Allow: []string{"u@a.com"}})}}},
		{"http bad pattern path", Options{Sources: []Binding{bind(&Spec{Name: "x", Type: TypeHTTP, Allow: []string{"a.com/x/../y"}})}}},
		{"http bad base", Options{Sources: []Binding{bind(&Spec{Name: "x", Type: TypeHTTP, BaseURL: "s3://b/k"})}}},
		{"http base with query", Options{Sources: []Binding{bind(&Spec{Name: "x", Type: TypeHTTP, BaseURL: "https://a.com/?k=1"})}}},
		{"bad bucket glob", Options{Sources: []Binding{bind(&Spec{Name: "x", Type: TypeS3, Buckets: []string{"a/b"}})}}},
		{"bad bucket", Options{Sources: []Binding{bind(&Spec{Name: "x", Type: TypeS3, Bucket: "a b"})}}},
		{"bad prefix", Options{Sources: []Binding{bind(&Spec{Name: "x", Type: TypeFS, Prefix: "a/../b"})}}},
		{"ftp no host", Options{Sources: []Binding{bind(&Spec{Name: "x", Type: TypeFTP})}}},
		{"ftp host with port", Options{Sources: []Binding{bind(&Spec{Name: "x", Type: TypeFTP, Host: "h.com:2121"})}}},
		{"sftp bad port", Options{Sources: []Binding{bind(&Spec{Name: "x", Type: TypeSFTP, Host: "h.com", Port: 70000})}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, err := NewRegistry(tc.opts)
			if err == nil || r != nil {
				t.Fatalf("NewRegistry = %v, %v; want error", r, err)
			}
			if !errors.Is(err, ErrInvalidSpec) {
				t.Errorf("error %v does not wrap ErrInvalidSpec", err)
			}
		})
	}
}

func TestSourceUnknown(t *testing.T) {
	t.Parallel()
	r := newTestRegistry(t, false)
	if _, ok := r.Source("nope"); ok {
		t.Error("Source(nope) found")
	}
}

func TestResolvePlayground(t *testing.T) {
	t.Parallel()
	pg := fakeSource{PlaygroundSourceName, "memory"}
	with, err := NewRegistry(Options{DevMode: true, Dev: fakeSource{DevSourceName, TypeHTTP}, Playground: pg})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	without := newTestRegistry(t, false)
	tests := []struct {
		name string
		raw  string
		want Key
		kind errs.Kind
	}{
		{name: "upload", raw: "playground://uploads/0123abcd", want: Key{Source: PlaygroundSourceName, Path: "uploads/0123abcd"}},
		{name: "sample, area lower-cased", raw: "playground://Samples/photo.jpg", want: Key{Source: PlaygroundSourceName, Path: "samples/photo.jpg"}},
		{name: "traversal", raw: "playground://uploads/../x", kind: errs.KindForbidden},
		{name: "bad area", raw: "playground://up_loads/x", kind: errs.KindInvalidURL},
		{name: "empty key", raw: "playground://uploads/", kind: errs.KindInvalidURL},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := with.Resolve(transform.SourceSpec{Raw: tc.raw})
			if tc.kind != errs.KindUnknown {
				if !errors.Is(err, errs.Match(tc.kind)) {
					t.Fatalf("Resolve(%q) = %+v, %v; want kind %v", tc.raw, got, err, tc.kind)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve(%q): %v", tc.raw, err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("mismatch (-want +got):\n%s", diff)
			}
			if _, err := without.Resolve(transform.SourceSpec{Raw: tc.raw}); !errors.Is(err, errs.Match(errs.KindForbidden)) {
				t.Errorf("without a playground store Resolve(%q) error = %v, want Forbidden", tc.raw, err)
			}
		})
	}
	if src, ok := with.Source(PlaygroundSourceName); !ok || src != Source(pg) {
		t.Errorf("Source(%q) = %v, %v", PlaygroundSourceName, src, ok)
	}
	if _, ok := without.Source(PlaygroundSourceName); ok {
		t.Error("Source(PlaygroundSourceName) available without a playground store")
	}
	if got := SchemePlayground.String(); got != "playground" {
		t.Errorf("SchemePlayground = %q", got)
	}
}
