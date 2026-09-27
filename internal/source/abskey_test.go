package source

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// TestResolveAbsoluteKey covers xi://<http source>/<absolute url> (WP-95):
// without a base url the key is fetched as that URL through the source's
// allowlist; with one, only URLs under the base are taken as is and the
// rest are joined to it. Refusals carry a client-safe public message.
func TestResolveAbsoluteKey(t *testing.T) {
	t.Parallel()
	r, err := NewRegistry(Options{Sources: []Binding{
		bind(&Spec{Name: "any", Type: TypeHTTP, Allow: []string{"*"}}),
		bind(&Spec{Name: "pics", Type: TypeHTTP, Allow: []string{"https://img.example.com/pub/"}}),
		bind(&Spec{Name: "cdn", Type: TypeHTTP, BaseURL: "https://cdn.example.com/static", Allow: []string{"*.example.net"}}),
		bind(&Spec{Name: "archive", Type: TypeS3, Buckets: []string{"assets"}}),
	}})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	const (
		noBase    = `source "pics" has no base_url: the key must be an absolute http(s) url`
		noBaseAny = `source "any" has no base_url: the key must be an absolute http(s) url`
		pathMiss  = `source "pics": url scheme, port or path not in the source allowlist`
	)
	tests := []struct {
		name   string
		raw    string
		mount  string
		want   Key
		kind   errs.Kind // zero value means success
		public string    // wanted client message on error ("" = not checked)
	}{
		{name: "literal https", raw: "xi://any/https://raw.githubusercontent.com/o/r/refs/heads/master/fox.avif", want: Key{Source: "any", Path: "https://raw.githubusercontent.com/o/r/refs/heads/master/fox.avif"}},
		{name: "literal http", raw: "xi://any/http://h.example.org/a.jpg", want: Key{Source: "any", Path: "http://h.example.org/a.jpg"}},
		{name: "query goes to the url", raw: "xi://any/https://h.example.org/a.jpg?w=1&t=x", want: Key{Source: "any", Path: "https://h.example.org/a.jpg?w=1&t=x"}},
		{name: "scheme and host case-insensitive", raw: "XI://any/HTTPS://H.Example.org/A.jpg", want: Key{Source: "any", Path: "https://h.example.org/A.jpg"}},
		{name: "collapsed scheme slash repaired", raw: "xi://any/https:/h.example.org/a.jpg", want: Key{Source: "any", Path: "https://h.example.org/a.jpg"}},
		{name: "inner encoding decoded once", raw: "xi://any/https://h.example.org/a%2520b%20c.jpg", want: Key{Source: "any", Path: "https://h.example.org/a%2520b%20c.jpg"}},
		{name: "key encoded as a whole", raw: "xi://any/https%3A%2F%2Fh.example.org%2Fdir%2Fa.jpg", want: Key{Source: "any", Path: "https://h.example.org/dir/a.jpg"}},
		{name: "explicit port kept", raw: "xi://any/https://h.example.org:8443/a.jpg", want: Key{Source: "any", Path: "https://h.example.org:8443/a.jpg"}},
		{name: "allowlist match", raw: "xi://pics/https://img.example.com/pub/a.jpg", want: Key{Source: "pics", Path: "https://img.example.com/pub/a.jpg"}},
		{name: "allowlist host miss", raw: "xi://pics/https://evil.com/pub/a.jpg", kind: errs.KindForbidden, public: `source "pics": host not in the source allowlist`},
		{name: "allowlist path miss", raw: "xi://pics/https://img.example.com/private/a.jpg", kind: errs.KindForbidden, public: pathMiss},
		{name: "allowlist scheme miss", raw: "xi://pics/http://img.example.com/pub/a.jpg", kind: errs.KindForbidden, public: pathMiss},
		{name: "relative key without base", raw: "xi://pics/a.jpg", kind: errs.KindForbidden, public: noBase},
		{name: "ftp key without base", raw: "xi://any/ftp://x", kind: errs.KindForbidden, public: noBaseAny},
		{name: "relative mount key without base", raw: "a.jpg", mount: "any", kind: errs.KindForbidden, public: noBaseAny},
		{name: "traversal in inner url", raw: "xi://any/https://h.example.org/a/../b.jpg", kind: errs.KindForbidden},
		{name: "encoded traversal in inner url", raw: "xi://any/https://h.example.org/a/%2E%2E/b.jpg", kind: errs.KindForbidden},
		{name: "userinfo in inner url", raw: "xi://any/https://user@h.example.org/a.jpg", kind: errs.KindInvalidURL},
		{name: "base: absolute under base taken as is", raw: "xi://cdn/https://cdn.example.com/static/a.jpg", want: Key{Source: "cdn", Path: "https://cdn.example.com/static/a.jpg"}},
		{name: "base: absolute outside base is joined", raw: "xi://cdn/https://x.example.net/a.jpg", want: Key{Source: "cdn", Path: "https://cdn.example.com/static/https://x.example.net/a.jpg"}},
		{name: "base: relative joined", raw: "xi://cdn/a.jpg?v=1", want: Key{Source: "cdn", Path: "https://cdn.example.com/static/a.jpg?v=1"}},
		{name: "unknown source", raw: "xi://nope/https://h.example.org/a.jpg", kind: errs.KindForbidden, public: `unknown source "nope"`},
		{name: "s3 without default bucket", raw: "xi://archive/a.jpg", kind: errs.KindForbidden, public: `source "archive" has no default bucket: name the bucket in the url`},
		{name: "bare url tries mount then order", raw: "https://h.example.org/a.jpg", mount: "cdn", want: Key{Source: "any", Path: "https://h.example.org/a.jpg"}},
		{name: "s3 bucket refused", raw: "s3://other/a.jpg", kind: errs.KindForbidden, public: "no source allows this s3:// reference"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.Resolve(transform.SourceSpec{Raw: tc.raw, Mount: tc.mount})
			if tc.kind == errs.KindUnknown {
				if err != nil {
					t.Fatalf("Resolve(%q): %v", tc.raw, err)
				}
				if diff := cmp.Diff(tc.want, got); diff != "" {
					t.Errorf("Resolve(%q) mismatch (-want +got):\n%s", tc.raw, diff)
				}
				return
			}
			if !errors.Is(err, errs.Match(tc.kind)) {
				t.Fatalf("Resolve(%q) = %+v, %v; want kind %v", tc.raw, got, err, tc.kind)
			}
			if tc.public == "" {
				return
			}
			pe, ok := errors.AsType[interface {
				error
				PublicMessage() string
			}](err)
			if !ok {
				t.Fatalf("Resolve(%q) error %v has no public message", tc.raw, err)
			}
			if got := pe.PublicMessage(); got != tc.public {
				t.Errorf("public message = %q, want %q", got, tc.public)
			}
			if strings.Contains(pe.PublicMessage(), "example.") {
				t.Errorf("public message %q leaks configuration", pe.PublicMessage())
			}
			if !errors.Is(err, ErrNotAllowed) {
				t.Errorf("error %v does not wrap ErrNotAllowed", err)
			}
		})
	}
}

// TestResolveAbsoluteKeyRedirect: a redirect from a key resolved through a
// named source is vetted against that source's allowlist.
func TestResolveAbsoluteKeyRedirect(t *testing.T) {
	t.Parallel()
	r, err := NewRegistry(Options{Sources: []Binding{
		bind(&Spec{Name: "pics", Type: TypeHTTP, Allow: []string{"https://img.example.com/pub/"}}),
	}})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	from, err := r.Resolve(transform.SourceSpec{Raw: "xi://pics/https://img.example.com/pub/a.jpg"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, err := r.ResolveRedirect(from, "https://img.example.com/pub/b.jpg"); err != nil {
		t.Errorf("redirect inside the allowlist: %v", err)
	}
	if _, err := r.ResolveRedirect(from, "https://evil.com/b.jpg"); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("redirect outside the allowlist error = %v, want ErrNotAllowed", err)
	}
}
