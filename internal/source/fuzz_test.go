package source

import (
	"errors"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// FuzzParseRef is the ParseKey fuzz target: attacker-controlled source
// strings must never panic, must fail only as InvalidURL or Forbidden
// traversal, and anything accepted must be traversal-free, canonical (its
// String() re-parses to the same Ref) and resolvable without panicking.
// Seeds: f.Add below plus testdata/fuzz/FuzzParseRef.
func FuzzParseRef(f *testing.F) {
	for _, s := range []string{
		"https://cdn.example.com/a.jpg?w=1", "http://[::1]:8080/a", "HTTPS:/Cdn.Example.com./%2e%2E/x",
		"s3://bucket/key?versionId=1", "gs://b/k?generation=2", "abs://c/b", "az://c/b",
		"local:///a/b.png", "local:a", "file://localhost/a", "ftp://h:21/a", "sftp://h:2222/a",
		"xi://assets/a%2Fb", "photos/a.jpg", "../a", "a\\..\\b", "%zz", "data:image/png;base64,AAAA",
	} {
		f.Add(s)
	}
	reg, err := NewRegistry(Options{Sources: testSpecs(), DevMode: true, Dev: fakeSource{DevSourceName, TypeHTTP}})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		ref, err := ParseRef(raw)
		if err != nil {
			switch {
			case errors.Is(err, errs.Match(errs.KindInvalidURL)) && errors.Is(err, ErrInvalidSource):
			case errors.Is(err, errs.Match(errs.KindForbidden)) && errors.Is(err, ErrTraversal):
			default:
				t.Fatalf("ParseRef(%q) unexpected error %v", raw, err)
			}
			return
		}
		if hasDotDot(ref.Path) || hasCtl(ref.Path) {
			t.Fatalf("ParseRef(%q) accepted unsafe path %q", raw, ref.Path)
		}
		if ref.Scheme != SchemeHTTP && ref.Scheme != SchemeHTTPS && (ref.Path == "" || ref.Scheme == SchemeLocal && strings.HasPrefix(ref.Path, "/")) {
			t.Fatalf("ParseRef(%q) accepted bad key %q", raw, ref.Path)
		}
		canon := ref.String()
		if len(canon) > MaxRefLen {
			return // escaping grew it past the input limit; nothing to round-trip
		}
		again, err := ParseRef(canon)
		if err != nil {
			t.Fatalf("canonical %q of %q does not parse: %v", canon, raw, err)
		}
		if again != ref {
			t.Fatalf("canonical form not stable for %q:\n first %+v\nsecond %+v", raw, ref, again)
		}
		for _, mount := range []string{"", "assets", "web", "local"} {
			k, err := reg.Resolve(transform.SourceSpec{Raw: raw, Mount: mount})
			if err != nil {
				continue
			}
			p, _, _ := strings.Cut(k.Path, "?") // an http query may hold anything
			if k.Source == "" || hasDotDot(p) {
				t.Fatalf("Resolve(%q, %q) = %+v", raw, mount, k)
			}
		}
	})
}
