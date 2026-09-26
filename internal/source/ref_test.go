package source

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

func TestParseRef(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		raw   string
		want  Ref
		canon string
	}{
		// http(s)
		{"https", "https://cdn.example.com/a/b.jpg", Ref{Scheme: SchemeHTTPS, Host: "cdn.example.com", Path: "/a/b.jpg", RawPath: "/a/b.jpg"}, "https://cdn.example.com/a/b.jpg"},
		{"http query kept, fragment dropped", "http://cdn.example.com/a.jpg?w=1&t=x#frag", Ref{Scheme: SchemeHTTP, Host: "cdn.example.com", Path: "/a.jpg", RawPath: "/a.jpg", Query: "w=1&t=x"}, "http://cdn.example.com/a.jpg?w=1&t=x"},
		{"host and scheme case folded, path case kept", "HTTPS://CDN.Example.COM/IMG/A.jpg", Ref{Scheme: SchemeHTTPS, Host: "cdn.example.com", Path: "/IMG/A.jpg", RawPath: "/IMG/A.jpg"}, "https://cdn.example.com/IMG/A.jpg"},
		{"default port dropped", "https://cdn.example.com:443/a", Ref{Scheme: SchemeHTTPS, Host: "cdn.example.com", Path: "/a", RawPath: "/a"}, "https://cdn.example.com/a"},
		{"explicit port kept", "http://cdn.example.com:08080/a", Ref{Scheme: SchemeHTTP, Host: "cdn.example.com", Port: "8080", Path: "/a", RawPath: "/a"}, "http://cdn.example.com:8080/a"},
		{"trailing dot host", "https://cdn.example.com./a", Ref{Scheme: SchemeHTTPS, Host: "cdn.example.com", Path: "/a", RawPath: "/a"}, "https://cdn.example.com/a"},
		{"empty path", "https://cdn.example.com", Ref{Scheme: SchemeHTTPS, Host: "cdn.example.com", Path: "/", RawPath: "/"}, "https://cdn.example.com/"},
		{"ipv6", "http://[2001:DB8::1]:8080/a", Ref{Scheme: SchemeHTTP, Host: "2001:db8::1", Port: "8080", Path: "/a", RawPath: "/a"}, "http://[2001:db8::1]:8080/a"},
		{"proxy-collapsed slash", "https:/cdn.example.com/a.jpg", Ref{Scheme: SchemeHTTPS, Host: "cdn.example.com", Path: "/a.jpg", RawPath: "/a.jpg"}, "https://cdn.example.com/a.jpg"},
		{"percent-encoding normalised", "https://cdn.example.com/%61%20b/%7e%2fc%3a", Ref{Scheme: SchemeHTTPS, Host: "cdn.example.com", Path: "/a b/~/c:", RawPath: "/a%20b/~%2Fc:"}, "https://cdn.example.com/a%20b/~%2Fc:"},
		{"unicode path escaped", "https://cdn.example.com/café.jpg", Ref{Scheme: SchemeHTTPS, Host: "cdn.example.com", Path: "/café.jpg", RawPath: "/caf%C3%A9.jpg"}, "https://cdn.example.com/caf%C3%A9.jpg"},
		{"single dot segment kept", "https://cdn.example.com/./a", Ref{Scheme: SchemeHTTPS, Host: "cdn.example.com", Path: "/./a", RawPath: "/./a"}, "https://cdn.example.com/./a"},
		// object stores
		{"s3", "s3://bucket/dir/key.png", Ref{Scheme: SchemeS3, Host: "bucket", Path: "dir/key.png", RawPath: "dir/key.png"}, "s3://bucket/dir/key.png"},
		{"s3 version", "s3://bucket/key.png?versionId=abc%2B1", Ref{Scheme: SchemeS3, Host: "bucket", Path: "key.png", RawPath: "key.png", Version: "abc+1"}, "s3://bucket/key.png?versionId=abc%2B1"},
		{"s3 bucket case kept", "S3://My.Bucket/Key", Ref{Scheme: SchemeS3, Host: "My.Bucket", Path: "Key", RawPath: "Key"}, "s3://My.Bucket/Key"},
		{"s3 decoded key", "s3://b/a%20b%2Bc.jpg", Ref{Scheme: SchemeS3, Host: "b", Path: "a b+c.jpg", RawPath: "a%20b+c.jpg"}, "s3://b/a%20b+c.jpg"},
		{"s3 double slash keeps one", "s3://b//a", Ref{Scheme: SchemeS3, Host: "b", Path: "/a", RawPath: "/a"}, "s3://b//a"},
		{"gs generation", "gs://bucket/k.jpg?generation=17", Ref{Scheme: SchemeGS, Host: "bucket", Path: "k.jpg", RawPath: "k.jpg", Version: "17"}, "gs://bucket/k.jpg?generation=17"},
		{"abs", "abs://container/blob.jpg", Ref{Scheme: SchemeAzure, Host: "container", Path: "blob.jpg", RawPath: "blob.jpg"}, "abs://container/blob.jpg"},
		{"az alias", "az://container/blob.jpg", Ref{Scheme: SchemeAzure, Host: "container", Path: "blob.jpg", RawPath: "blob.jpg"}, "abs://container/blob.jpg"},
		{"s3 collapsed slash", "s3:/bucket/k", Ref{Scheme: SchemeS3, Host: "bucket", Path: "k", RawPath: "k"}, "s3://bucket/k"},
		// local
		{"local triple slash", "local:///img/a.png", Ref{Scheme: SchemeLocal, Path: "img/a.png", RawPath: "img/a.png"}, "local:///img/a.png"},
		{"local single slash", "local:/img/a.png", Ref{Scheme: SchemeLocal, Path: "img/a.png", RawPath: "img/a.png"}, "local:///img/a.png"},
		{"local no slash", "local:img/a.png", Ref{Scheme: SchemeLocal, Path: "img/a.png", RawPath: "img/a.png"}, "local:///img/a.png"},
		{"local localhost", "local://localhost/img/a.png", Ref{Scheme: SchemeLocal, Path: "img/a.png", RawPath: "img/a.png"}, "local:///img/a.png"},
		{"file alias", "file:///img/a.png", Ref{Scheme: SchemeLocal, Path: "img/a.png", RawPath: "img/a.png"}, "local:///img/a.png"},
		{"local extra slashes", "local:////etc/a.png", Ref{Scheme: SchemeLocal, Path: "etc/a.png", RawPath: "etc/a.png"}, "local:///etc/a.png"},
		// ftp / sftp
		{"ftp", "ftp://FTP.example.com/pub/a.jpg", Ref{Scheme: SchemeFTP, Host: "ftp.example.com", Path: "pub/a.jpg", RawPath: "pub/a.jpg"}, "ftp://ftp.example.com/pub/a.jpg"},
		{"ftp default port", "ftp://ftp.example.com:21/a.jpg", Ref{Scheme: SchemeFTP, Host: "ftp.example.com", Path: "a.jpg", RawPath: "a.jpg"}, "ftp://ftp.example.com/a.jpg"},
		{"sftp port", "sftp://files.example.com:2222/a.jpg", Ref{Scheme: SchemeSFTP, Host: "files.example.com", Port: "2222", Path: "a.jpg", RawPath: "a.jpg"}, "sftp://files.example.com:2222/a.jpg"},
		// xi
		{"xi", "xi://assets/dir/a.jpg", Ref{Scheme: SchemeXI, Host: "assets", Path: "dir/a.jpg", RawPath: "dir/a.jpg"}, "xi://assets/dir/a.jpg"},
		{"xi name case folded", "XI://Assets/A.jpg?x=1", Ref{Scheme: SchemeXI, Host: "assets", Path: "A.jpg", RawPath: "A.jpg", Query: "x=1"}, "xi://assets/A.jpg?x=1"},
		// relative
		{"relative", "photos/a.jpg", Ref{Scheme: SchemeRelative, Path: "photos/a.jpg", RawPath: "photos/a.jpg"}, "photos/a.jpg"},
		{"relative leading slashes", "//photos/a.jpg", Ref{Scheme: SchemeRelative, Path: "photos/a.jpg", RawPath: "photos/a.jpg"}, "photos/a.jpg"},
		{"relative with colon", "2024:05/a.jpg", Ref{Scheme: SchemeRelative, Path: "2024:05/a.jpg", RawPath: "2024%3A05/a.jpg"}, "2024%3A05/a.jpg"},
		{"relative unknown scheme-like", "data:image/png", Ref{Scheme: SchemeRelative, Path: "data:image/png", RawPath: "data%3Aimage/png"}, "data%3Aimage/png"},
		{"relative query", "a.jpg?versionId=3", Ref{Scheme: SchemeRelative, Path: "a.jpg", RawPath: "a.jpg", Query: "versionId=3"}, "a.jpg?versionId=3"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseRef(tc.raw)
			if err != nil {
				t.Fatalf("ParseRef(%q): %v", tc.raw, err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ParseRef(%q) mismatch (-want +got):\n%s", tc.raw, diff)
			}
			if s := got.String(); s != tc.canon {
				t.Errorf("String() = %q, want %q", s, tc.canon)
			}
			again, err := ParseRef(got.String())
			if err != nil || !cmp.Equal(again, got) {
				t.Errorf("canonical form does not round-trip: %+v, %v", again, err)
			}
		})
	}
}

func TestParseRefErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		raw      string
		kind     errs.Kind
		sentinel error
	}{
		{"empty", "", errs.KindInvalidURL, ErrInvalidSource},
		{"too long", "https://a.com/" + strings.Repeat("a", MaxRefLen), errs.KindInvalidURL, ErrInvalidSource},
		{"control char", "https://a.com/a\nb", errs.KindInvalidURL, ErrInvalidSource},
		{"encoded NUL", "https://a.com/a%00b", errs.KindInvalidURL, ErrInvalidSource},
		{"bad escape", "https://a.com/%zz", errs.KindInvalidURL, ErrInvalidSource},
		{"userinfo", "https://user:pw@a.com/x", errs.KindInvalidURL, ErrInvalidSource},
		{"userinfo smuggle", "https://allowed.com@evil.com/x", errs.KindInvalidURL, ErrInvalidSource},
		{"empty host", "https:///x", errs.KindInvalidURL, ErrInvalidSource},
		{"no authority", "https:a.com/x", errs.KindInvalidURL, ErrInvalidSource},
		{"percent host", "https://a%2ecom/x", errs.KindInvalidURL, ErrInvalidSource},
		{"unicode host", "https://exámple.com/x", errs.KindInvalidURL, ErrInvalidSource},
		{"backslash host", "https://a.com\\@evil.com/x", errs.KindInvalidURL, ErrInvalidSource},
		{"bad port", "https://a.com:99999/x", errs.KindInvalidURL, ErrInvalidSource},
		{"zero port", "https://a.com:0/x", errs.KindInvalidURL, ErrInvalidSource},
		{"bad ipv6", "http://[fe80::1%25eth0]/x", errs.KindInvalidURL, ErrInvalidSource},
		{"unsupported scheme", "swift://container/obj", errs.KindInvalidURL, ErrInvalidSource},
		{"unsupported scheme 2", "gopher://a.com/x", errs.KindInvalidURL, ErrInvalidSource},
		{"s3 empty key", "s3://bucket/", errs.KindInvalidURL, ErrInvalidSource},
		{"s3 no key", "s3://bucket?versionId=1", errs.KindInvalidURL, ErrInvalidSource},
		{"s3 bad bucket", "s3://bu%63ket/k", errs.KindInvalidURL, ErrInvalidSource},
		{"s3 empty bucket", "s3:///k", errs.KindInvalidURL, ErrInvalidSource},
		{"gs bad generation", "gs://b/k?generation=x", errs.KindInvalidURL, ErrInvalidSource},
		{"xi bad name", "xi://as_sets/k", errs.KindInvalidURL, ErrInvalidSource},
		{"xi empty key", "xi://assets/", errs.KindInvalidURL, ErrInvalidSource},
		{"local host", "local://evil.com/a", errs.KindInvalidURL, ErrInvalidSource},
		{"local empty", "local:///", errs.KindInvalidURL, ErrInvalidSource},
		{"relative empty", "/", errs.KindInvalidURL, ErrInvalidSource},
		{"relative only query", "?a=1", errs.KindInvalidURL, ErrInvalidSource},
		// traversal
		{"dotdot http", "https://a.com/img/../secret", errs.KindForbidden, ErrTraversal},
		{"dotdot encoded", "https://a.com/img/%2e%2e/secret", errs.KindForbidden, ErrTraversal},
		{"dotdot encoded slash", "https://a.com/img%2f..%2fsecret", errs.KindForbidden, ErrTraversal},
		{"dotdot backslash", "https://a.com/img\\..\\secret", errs.KindForbidden, ErrTraversal},
		{"dotdot s3", "s3://b/a/../../k", errs.KindForbidden, ErrTraversal},
		{"dotdot local", "local:///../etc/passwd", errs.KindForbidden, ErrTraversal},
		{"dotdot ftp", "ftp://h/../x", errs.KindForbidden, ErrTraversal},
		{"dotdot xi", "xi://assets/../x", errs.KindForbidden, ErrTraversal},
		{"dotdot relative", "../x.jpg", errs.KindForbidden, ErrTraversal},
		{"dotdot relative trailing", "a/..", errs.KindForbidden, ErrTraversal},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseRef(tc.raw)
			if err == nil {
				t.Fatalf("ParseRef(%q) = %+v, want error", tc.raw, got)
			}
			if !errors.Is(err, errs.Match(tc.kind)) || !errors.Is(err, tc.sentinel) {
				t.Errorf("ParseRef(%q) error %v: want kind %v and %v", tc.raw, err, tc.kind, tc.sentinel)
			}
		})
	}
}

func TestSchemeString(t *testing.T) {
	t.Parallel()
	if got := Scheme(200).String(); got != "Scheme(200)" {
		t.Errorf("out-of-range scheme = %q", got)
	}
	if got := SchemeAzure.String(); got != "abs" {
		t.Errorf("azure = %q", got)
	}
}
