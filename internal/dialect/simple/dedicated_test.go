package simple_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/bunny"
	"github.com/xantiagoma/xtandard-image/internal/dialect/cloudflare"
	"github.com/xantiagoma/xtandard-image/internal/dialect/cloudinary"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imagekit"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgix"
	"github.com/xantiagoma/xtandard-image/internal/dialect/wsrv"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// TestUnpicDedicated runs the unpic fixtures of the providers xi implements
// as full dialects through those dialects, host-bound with an empty mount
// prefix, with the same checks as TestUnpicFixtures.
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
	cld, err := cloudinary.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	cf, err := cloudflare.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	cfi, err := cloudflare.NewImages(nil)
	if err != nil {
		t.Fatal(err)
	}
	bn, err := bunny.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := wsrv.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	dedicated := []struct {
		name string
		d    dialect.Dialect
		// source maps unpic's src to the source xi reports: the parts xi
		// takes as the mount (endpoint, cloud, delivery type, account)
		// are not part of it.
		source func(string) string
	}{
		{"imgix", ix, identity},
		{"imagekit", ik, func(s string) string { return strings.TrimPrefix(s, "ikmedia/") }},
		{"cloudinary", cld, cloudinaryPublicID},
		{"cloudflare", cf, cloudflareSource},
		{"cloudflare_images", cfi, cloudflareImageID},
		{"bunny", bn, identity},
		{"wsrv", ws, identity},
	}
	for _, tc := range dedicated {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, ok := files[tc.name]
			if !ok || len(f.Fixtures) == 0 {
				t.Fatalf("no fixtures for %s", tc.name)
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
				want := tc.source(unescape(fx.Want.Source))
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

func identity(s string) string { return s }

// cloudinaryPublicID: unpic's src is "[<cloud>/]image/upload/[v<n>/]<id>";
// the cloudinary dialect reports the public id.
func cloudinaryPublicID(s string) string {
	_, id, ok := strings.Cut(s, "image/upload/")
	if !ok {
		return s
	}
	if v, rest, ok := strings.Cut(id, "/"); ok && len(v) > 1 && v[0] == 'v' && strings.Trim(v[1:], "0123456789") == "" {
		return rest
	}
	return id
}

// cloudflareSource: a relative unpic src is a key the cloudflare dialect
// reports from the zone root.
func cloudflareSource(s string) string {
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return s
	}
	return "/" + s
}

// cloudflareImageID: unpic's src is "[cdn-cgi/imagedelivery/]<account>/<id>";
// the account hash selects the mount and the image id is the source key.
func cloudflareImageID(s string) string {
	return s[strings.LastIndexByte(s, '/'):]
}
