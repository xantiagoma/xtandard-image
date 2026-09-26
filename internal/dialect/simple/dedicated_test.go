package simple_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imagekit"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgix"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// TestUnpicDedicated runs the unpic fixtures of the providers xi implements
// as full dialects through those dialects, host-bound with an empty mount
// prefix, with the same checks as TestUnpicFixtures. Dialects that are not
// on this branch yet are listed so the fixture file is not orphaned.
func TestUnpicDedicated(t *testing.T) {
	t.Parallel()
	files := loadFixtures(t)
	ix, err := imgix.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	ik, err := imagekit.New(&imagekit.Options{EndpointPath: "/ikmedia"})
	if err != nil {
		t.Fatal(err)
	}
	dedicated := []struct {
		name      string
		d         dialect.Dialect
		srcPrefix string // unpic's src path part xi takes as the mount endpoint
	}{
		{"imgix", ix, ""},
		{"imagekit", ik, "ikmedia/"},
		// Full dialects on their own branches (WP-41, WP-43/79, WP-44,
		// WP-46); their packages run these fixtures.
		{"cloudinary", nil, ""},
		{"cloudflare", nil, ""},
		{"cloudflare_images", nil, ""},
		{"bunny", nil, ""},
		{"wsrv", nil, ""},
	}
	for _, tc := range dedicated {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, ok := files[tc.name]
			if !ok || len(f.Fixtures) == 0 {
				t.Fatalf("no fixtures for %s", tc.name)
			}
			if tc.d == nil {
				t.Skipf("%d fixtures; the %s dialect is not on this branch", len(f.Fixtures), tc.name)
			}
			for _, fx := range f.Fixtures {
				u, err := url.Parse(fx.URL)
				if err != nil {
					t.Fatal(err)
				}
				res, err := tc.d.Parse(&dialect.Input{
					Path: u.EscapedPath(), RawQuery: u.RawQuery, Query: u.Query(), Host: u.Host,
				})
				if err != nil {
					t.Errorf("%s: %v", fx.URL, err)
					continue
				}
				r := res.Request
				pl := &r.Pipelines[0]
				if got, want := pl.Resize.Width.Px, round(fx.Want.Width); got != want {
					t.Errorf("%s: width %d, unpic %d", fx.URL, got, want)
				}
				if got, want := pl.Resize.Height.Px, round(fx.Want.Height); got != want {
					t.Errorf("%s: height %d, unpic %d", fx.URL, got, want)
				}
				if fx.Want.Quality != nil && r.Output.Quality != round(fx.Want.Quality) {
					t.Errorf("%s: quality %d, unpic %v", fx.URL, r.Output.Quality, *fx.Want.Quality)
				}
				if fx.Want.Format != nil {
					if want, ok := wantFormat(*fx.Want.Format); ok && r.Output.Format != want {
						t.Errorf("%s: format %v, unpic %q", fx.URL, r.Output.Format, *fx.Want.Format)
					}
				}
				want := strings.TrimPrefix(unescape(fx.Want.Source), tc.srcPrefix)
				if got := unescape(res.SourceString); got != want {
					t.Errorf("%s: source %q, unpic %q", fx.URL, got, want)
				}
				r.ApplyDefaults(transform.StandardDefaults())
				if err := r.Validate(); err != nil {
					t.Errorf("%s: invalid request: %v", fx.URL, err)
				}
			}
		})
	}
}
