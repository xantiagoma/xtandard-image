package awssih_test

import (
	"net/url"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/awssih"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

// TestThumborForwardingHook checks what the SIH mount hands to the thumbor
// dialect: "/unsafe" + path (escaped, after the rewrite and the s3: bucket
// segment), no query, the bucket's source, and that SIH's own signature
// outcome replaces the thumbor one.
func TestThumborForwardingHook(t *testing.T) {
	t.Parallel()
	keys := sign.NewKeyring(sign.NewKey([]byte(secret), nil))
	v, err := sign.NewAWSSIHVerifier(sign.AWSSIHOptions{Keys: keys, Required: true})
	if err != nil {
		t.Fatal(err)
	}
	signer, err := sign.NewSigner(sign.NewAWSSIHScheme(), keys)
	if err != nil {
		t.Fatal(err)
	}
	rw, err := awssih.ParseRewrite(`/^\/legacy\/(\d+)\/(.*)$/`, "/fit-in/$1x$1/$2")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, path, query string
		rewrite           *awssih.Rewrite
		wantPath          string
		wantSource        string
	}{
		{"plain", "/fit-in/300x300/a%20b.jpg", "", nil, "/unsafe/fit-in/300x300/a%20b.jpg", mountSource},
		{"already unsafe", "/unsafe/300x300/a.jpg", "", nil, "/unsafe/300x300/a.jpg", mountSource},
		{"s3 bucket segment", "/300x0/s3:sample-bucket/dir/a.jpg", "width=5", nil, "/unsafe/300x0/dir/a.jpg", "logos"},
		{"rewrite", "/legacy/64/photos/a.png", "", rw, "/unsafe/fit-in/64x64/photos/a.png", mountSource},
		{"rewrite no match", "/other/a.png", "", rw, "/unsafe/other/a.png", mountSource},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			th := &fakeThumbor{}
			p, err := awssih.New(&awssih.Options{Buckets: buckets, Verifier: v, Thumbor: th, Rewrite: tc.rewrite, QueryParams: true})
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := url.PathUnescape(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			q, err := signer.SignAWSSIHQuery(decoded, tc.query)
			if err != nil {
				t.Fatal(err)
			}
			res, err := p.Parse(&dialect.Input{
				Path: tc.path, RawQuery: q, Query: url.Values{"x": {"y"}},
				Mount: dialect.Mount{Name: "sih", Prefix: "/sih", Source: mountSource},
			})
			if err != nil {
				t.Fatal(err)
			}
			got := th.last(t)
			if got.Path != tc.wantPath || got.RawQuery != "" || got.Query != nil {
				t.Errorf("forwarded path %q query %q %v; want %q and no query", got.Path, got.RawQuery, got.Query, tc.wantPath)
			}
			if got.Mount.Source != tc.wantSource || got.Mount.Name != "sih" {
				t.Errorf("forwarded mount %+v, want source %q", got.Mount, tc.wantSource)
			}
			if res.Signature != sign.Verified {
				t.Errorf("signature = %v, want verified (SIH's own)", res.Signature)
			}
			if res.Request.Dialect != awssih.Name || res.Request.Source.Mount != tc.wantSource {
				t.Errorf("request dialect %q mount %q", res.Request.Dialect, res.Request.Source.Mount)
			}
		})
	}

	// Unsigned thumbor paths are rejected before forwarding.
	th := &fakeThumbor{}
	p, err := awssih.New(&awssih.Options{Verifier: v, Thumbor: th})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Parse(&dialect.Input{Path: "/300x300/a.jpg"}); err == nil {
		t.Fatal("unsigned thumbor path accepted")
	}
	if th.calls != 0 {
		t.Fatal("thumbor called for a rejected request")
	}
}

func TestParseRewrite(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, pattern, repl, in, want string
		bad                           bool
	}{
		{"sih spec", "/(filters-)/gm", "filters:", "/filters-rotate(90)/filters-grayscale()/a.jpg", "/filters:rotate(90)/filters:grayscale()/a.jpg", false},
		{"first only without g", "/(filters-)/", "filters:", "/filters-a/filters-b", "/filters:a/filters-b", false},
		{"case insensitive", "/IMG/i", "x", "/img/a", "/x/a", false},
		{"groups and $&", `/(\w+)\.(png)/`, "$2-$1-[$&]-$$", "/a/b.png", "/a/png-b-[b.png]-$", false},
		{"named group", `/(?P<n>\d+)/g`, "<$<n>>", "/1/22", "/<1>/<22>", false},
		{"literal dollar", "/a/", "$x", "/a", "/$x", false},
		{"not a literal", "filters-", "x", "", "", true},
		{"bad flag", "/a/y", "x", "", "", true},
		{"lookahead", "/a(?=b)/", "x", "", "", true},
		{"backtick token", "/a/", "$`", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rw, err := awssih.ParseRewrite(tc.pattern, tc.repl)
			if tc.bad {
				if err == nil {
					t.Fatal("no error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := rw.Apply(tc.in); got != tc.want {
				t.Errorf("Apply(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNewRejectsEmptyBucketNames(t *testing.T) {
	t.Parallel()
	for _, b := range []map[string]string{{"": "images"}, {"bucket": ""}} {
		if _, err := awssih.New(&awssih.Options{Buckets: b}); err == nil {
			t.Errorf("New(%v) accepted", b)
		}
	}
	p, err := awssih.New(nil)
	if err != nil || p.Name() != awssih.Name || p.Verifier() != nil {
		t.Fatalf("New(nil) = %v, %v", p, err)
	}
}
