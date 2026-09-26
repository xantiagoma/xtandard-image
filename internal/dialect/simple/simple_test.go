package simple_test

import (
	"errors"
	"net/url"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/simple"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

func parser(tb testing.TB, name string, o *simple.Options) *simple.Parser {
	tb.Helper()
	spec := simple.Lookup(name)
	if spec == nil {
		tb.Fatalf("no provider %s", name)
	}
	p, err := simple.New(spec, o)
	if err != nil {
		tb.Fatal(err)
	}
	return p
}

// parse parses a provider-native URL ("/path?query") host-bound.
func parse(tb testing.TB, p *simple.Parser, rawURL string) (*dialect.Result, error) {
	tb.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		tb.Fatal(err)
	}
	return p.Parse(&dialect.Input{Path: u.EscapedPath(), RawQuery: u.RawQuery, Mount: dialect.Mount{Source: "origin"}})
}

// got is the part of a request the engine tests compare.
type got struct {
	Source   string
	Encoding transform.SourceEncoding
	Mount    string
	W, H     int
	Type     transform.ResizeType
	Enlarge  bool
	Extend   bool
	Gravity  transform.GravityType
	Quality  int
	Format   transform.Format
	DPR      float64
	Ignored  []transform.Ignored
}

func summarize(res *dialect.Result) got {
	r := res.Request
	pl := &r.Pipelines[0]
	return got{
		Source: res.SourceString, Encoding: r.Source.Encoding, Mount: r.Source.Mount,
		W: pl.Resize.Width.Px, H: pl.Resize.Height.Px, Type: pl.Resize.Type, Enlarge: pl.Resize.Enlarge,
		Extend: pl.Extend.Enabled, Gravity: pl.Gravity.Type, Quality: r.Output.Quality, Format: r.Output.Format,
		DPR: pl.Resize.DPR, Ignored: r.Ignored,
	}
}

func TestParse(t *testing.T) {
	t.Parallel()
	rel := transform.SourceRelative
	plain := transform.SourcePlain
	tests := []struct {
		name, provider, url string
		want                got
	}{
		{
			"nextjs_relative", "nextjs", "/_next/image?url=%2F_next%2Fstatic%2Fa%20b.jpg&w=640&q=75",
			got{Source: "_next/static/a%20b.jpg", Encoding: rel, Mount: "origin", W: 640, Type: transform.ResizeFit, Quality: 75},
		},
		{
			"nextjs_absolute", "nextjs", "/_next/image?url=https%3A%2F%2Fcdn.example.com%2Fa.jpg%3Fv%3D1&w=640",
			got{Source: "https://cdn.example.com/a.jpg?v=1", Encoding: plain, W: 640, Type: transform.ResizeFit},
		},
		{
			"nextjs_protocol_relative", "nextjs", "/_next/image?url=%2F%2Fcdn.example.com%2Fa.jpg&w=16",
			got{Source: "https://cdn.example.com/a.jpg", Encoding: plain, W: 16, Type: transform.ResizeFit},
		},
		{
			"netlify_fit_position", "netlify", "/.netlify/images?url=/a.jpg&w=300&h=200&fit=cover&position=top&fm=avif",
			got{Source: "a.jpg", Encoding: rel, Mount: "origin", W: 300, H: 200, Type: transform.ResizeFill, Gravity: transform.GravityNorth, Format: transform.FormatAVIF},
		},
		{
			"netlify_blurhash_ignored", "netlify", "/.netlify/images?url=/a.jpg&fm=blurhash",
			got{Source: "a.jpg", Encoding: rel, Mount: "origin", Ignored: []transform.Ignored{{Option: "fm", Reason: "unsupported format"}}},
		},
		{
			"ipx_size_contain", "ipx", "/_ipx/s_200x100,fit_contain,pos_left%20top,f_webp,q_80/images/a.jpg",
			got{Source: "images/a.jpg", Encoding: rel, Mount: "origin", W: 200, H: 100, Type: transform.ResizeFit, Extend: true, Gravity: transform.GravityNorthWest, Format: transform.FormatWebP, Quality: 80},
		},
		{
			"ipx_no_modifiers_remote", "ipx", "/_ipx/_/https://example.com/a.jpg",
			got{Source: "https://example.com/a.jpg", Encoding: plain},
		},
		{
			"directus_keeps_token", "directus", "/assets/abc?width=100&height=50&fit=inside&withoutEnlargement=true&access_token=t",
			got{Source: "assets/abc?access_token=t", Encoding: rel, Mount: "origin", W: 100, H: 50, Type: transform.ResizeFit},
		},
		{
			"directus_unsupported", "directus", "/assets/abc?transforms=%5B%5D",
			got{Source: "assets/abc", Encoding: rel, Mount: "origin", Ignored: []transform.Ignored{{Option: "transforms", Reason: "unsupported"}}},
		},
		{
			"astro_unknown", "astro", "/_image?href=%2Fa.png&w=100&zzz=1",
			got{Source: "a.png", Encoding: rel, Mount: "origin", W: 100, Type: transform.ResizeFill, Ignored: []transform.Ignored{{Option: "zzz", Reason: "unknown option"}}},
		},
		{
			"contentful_pad", "contentful", "/space/asset/tok/a.jpg?w=500&h=250&fit=pad&bg=rgb:ffffff&f=face",
			got{Source: "space/asset/tok/a.jpg", Encoding: rel, Mount: "origin", W: 500, H: 250, Type: transform.ResizeFit, Extend: true, Gravity: transform.GravityFace},
		},
		{
			"shopify_suffix", "shopify", "/s/files/1/products/shirt_200x300_crop_top.jpg?v=3",
			got{Source: "s/files/1/products/shirt.jpg", Encoding: rel, Mount: "origin", W: 200, H: 300, Type: transform.ResizeFill, Gravity: transform.GravityNorth},
		},
		{
			"storyblok_flip_filters", "storyblok", "/f/1/2x2/h/a.jpg/m/fit-in/-300x0/filters:format(webp):quality(70):fill(ffffff)",
			got{Source: "f/1/2x2/h/a.jpg", Encoding: rel, Mount: "origin", W: 300, Type: transform.ResizeFit, Enlarge: true, Format: transform.FormatWebP, Quality: 70},
		},
		{
			"storyblok_legacy", "storyblok", "/200x0/smart/filters:format(png)/f/1/2x2/h/a.jpg",
			got{Source: "f/1/2x2/h/a.jpg", Encoding: rel, Mount: "origin", W: 200, Type: transform.ResizeFill, Enlarge: true, Gravity: transform.GravitySmart, Format: transform.FormatPNG},
		},
		{
			"supabase_render_to_object", "supabase", "/storage/v1/render/image/public/b/a.jpg?width=10&resize=contain",
			got{Source: "storage/v1/object/public/b/a.jpg", Encoding: rel, Mount: "origin", W: 10, Type: transform.ResizeFit},
		},
		{
			"uploadcare_ops", "uploadcare", "/uuid-1/-/scale_crop/300x200/center/-/quality/best/-/format/png/photo.jpg",
			got{Source: "uuid-1/", Encoding: rel, Mount: "origin", W: 300, H: 200, Type: transform.ResizeFill, Enlarge: true, Gravity: transform.GravityCenter, Quality: 95, Format: transform.FormatPNG},
		},
		{
			"hygraph_tasks", "hygraph", "/env/resize=fit:max,width:400/output=format:webp/handle",
			got{Source: "env/handle", Encoding: rel, Mount: "origin", W: 400, Type: transform.ResizeFit, Format: transform.FormatWebP},
		},
		{
			"imageengine_directives", "imageengine", "/a.jpg?imgeng=/w_300/h_200/m_letterbox/cmpr_30&ref=x",
			got{Source: "a.jpg?ref=x", Encoding: rel, Mount: "origin", W: 300, H: 200, Type: transform.ResizeFit, Extend: true, Quality: 70},
		},
		{
			"cloudimage_encoded", "cloudimage", "/v7/example.com%2Fa.jpg%3Fx%3D1?ci_url_encoded=1&w=100&org_if_sml=1&func=face",
			got{Source: "https://example.com/a.jpg?x=1", Encoding: plain, W: 100, Type: transform.ResizeFill, Gravity: transform.GravityFace},
		},
		{
			"scene7_fit_upscale", "scene7", "/is/image/co/asset?wid=400&hei=300&fit=crop,1&qlt=80,1&fmt=png-alpha",
			got{Source: "is/image/co/asset", Encoding: rel, Mount: "origin", W: 400, H: 300, Type: transform.ResizeFill, Enlarge: true, Quality: 80, Format: transform.FormatPNG},
		},
		{
			"scene7_default_pad", "scene7", "/is/image/co/asset?wid=400&hei=300",
			got{Source: "is/image/co/asset", Encoding: rel, Mount: "origin", W: 400, H: 300, Type: transform.ResizeFit, Extend: true},
		},
		{
			"kontentai_focal", "kontentai", "/p/a/x.jpg?w=100&h=100&fit=crop&fp-x=0.2&dpr=2",
			got{Source: "p/a/x.jpg", Encoding: rel, Mount: "origin", W: 100, H: 100, Type: transform.ResizeFill, Gravity: transform.GravityFocusPoint, DPR: 2},
		},
		{
			"appwrite_project", "appwrite", "/v1/storage/buckets/b/files/f/preview?project=p&width=10&gravity=top-left",
			got{Source: "v1/storage/buckets/b/files/f/preview?project=p", Encoding: rel, Mount: "origin", W: 10, Type: transform.ResizeFill, Gravity: transform.GravityNorthWest},
		},
		{
			"colon_in_first_segment", "keycdn", "/a:b/c.jpg?width=10",
			got{Source: "a%3Ab/c.jpg", Encoding: rel, Mount: "origin", W: 10, Type: transform.ResizeFill},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res, err := parse(t, parser(t, tt.provider, nil), tt.url)
			if err != nil {
				t.Fatal(err)
			}
			if d := cmp.Diff(tt.want, summarize(res)); d != "" {
				t.Errorf("mismatch (-want +got):\n%s", d)
			}
			if res.Request.Dialect != "simple:"+tt.provider {
				t.Errorf("dialect %q", res.Request.Dialect)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, provider, url string
		strict              bool
		field               string // ArgumentError field; "" = grammar error
		sentinel            error
	}{
		{"missing_url", "nextjs", "/_next/image?w=10", false, "", dialect.ErrMalformedURL},
		{"duplicate_url", "nextjs", "/_next/image?url=/a&url=/b", false, "", dialect.ErrMalformedURL},
		{"bad_width", "nextjs", "/_next/image?url=/a&w=abc", false, "w", nil},
		{"negative_width", "netlify", "/.netlify/images?url=/a&w=-1", false, "w", nil},
		{"huge_width", "netlify", "/.netlify/images?url=/a&w=99999999", false, "w", nil},
		{"bad_quality", "nextjs", "/_next/image?url=/a&q=101", false, "q", nil},
		{"bad_fit", "netlify", "/.netlify/images?url=/a&fit=zoom", false, "fit", nil},
		{"bad_gravity", "netlify", "/.netlify/images?url=/a&position=up", false, "position", nil},
		{"bad_color", "contentful", "/a.jpg?bg=zzz", false, "bg", nil},
		{"bad_escape", "nextjs", "/_next/image?url=%zz", false, "", dialect.ErrMalformedURL},
		{"strict_unknown", "nextjs", "/_next/image?url=/a&zzz=1", true, "", dialect.ErrUnknownOption},
		{"strict_unsupported", "directus", "/assets/a?transforms=x", true, "", dialect.ErrUnknownOption},
		{"control_char_source", "nextjs", "/_next/image?url=%2Fa%0Ab", false, "", dialect.ErrMalformedURL},
		{"empty_source", "keycdn", "/?width=1", false, "", dialect.ErrMalformedURL},
		{"ipx_no_source", "ipx", "/_ipx/w_100", false, "", dialect.ErrMalformedURL},
		{"hygraph_short", "hygraph", "/handle", false, "", dialect.ErrMalformedURL},
		{"storyblok_no_asset", "storyblok", "/200x0/x.jpg", false, "", dialect.ErrMalformedURL},
		{"storyblok_bad_filters", "storyblok", "/f/1/a.jpg/m/filters:format(webp", false, "", dialect.ErrMalformedURL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := parse(t, parser(t, tt.provider, &simple.Options{Strict: tt.strict}), tt.url)
			if err == nil {
				t.Fatal("no error")
			}
			if e, ok := errors.AsType[*errs.Error](err); !ok || e.Kind != errs.KindInvalidURL {
				t.Fatalf("want a 400 errs.Error, got %v", err)
			}
			if tt.field != "" {
				ae, ok := errors.AsType[*dialect.ArgumentError](err)
				if !ok || ae.Field != tt.field {
					t.Fatalf("want argument error on %q, got %v", tt.field, err)
				}
			}
			if tt.sentinel != nil && !errors.Is(err, tt.sentinel) {
				t.Fatalf("want %v, got %v", tt.sentinel, err)
			}
		})
	}
}

func TestXISignature(t *testing.T) {
	t.Parallel()
	keys := sign.NewKeyring(sign.NewKey([]byte("secret"), []byte("salt")))
	v, err := sign.NewXIVerifier(sign.XIOptions{Keys: keys, Size: 32})
	if err != nil {
		t.Fatal(err)
	}
	scheme, err := sign.NewXIScheme(32)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := sign.NewSigner(scheme, keys)
	if err != nil {
		t.Fatal(err)
	}
	p := parser(t, "nextjs", &simple.Options{Verifier: v})
	// The signature covers the provider-native path, whatever the mount.
	q, err := signer.SignXIQuery("/_next/image", "url=%2Fa.jpg&w=640")
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Parse(&dialect.Input{Path: "/", RawQuery: q, Mount: dialect.Mount{Prefix: "/_next/image"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Signature != sign.Verified {
		t.Errorf("signature %v", res.Signature)
	}
	for _, kv := range res.Request.Ignored {
		t.Errorf("xi_sig leaked into the options: %+v", kv)
	}
	if _, err := p.ParseURL("/_next/image", q+"0"); err == nil {
		t.Error("tampered signature accepted")
	}
	res, err = p.ParseURL("/_next/image", "url=%2Fa.jpg&w=640")
	if err != nil || res.Signature != sign.Unsigned {
		t.Errorf("unsigned: %v, %v", res, err)
	}
}

func TestNativePrefix(t *testing.T) {
	t.Parallel()
	p := parser(t, "ipx", nil)
	// Mounted at its native prefix, the router strips "/_ipx".
	res, err := p.Parse(&dialect.Input{Path: "/w_10/a.jpg", Mount: dialect.Mount{Prefix: "/_ipx", Source: "s"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.SourceString != "a.jpg" || res.Request.Pipelines[0].Resize.Width.Px != 10 || res.Request.Source.Mount != "s" {
		t.Errorf("got %q %+v", res.SourceString, res.Request.Pipelines[0].Resize)
	}
	// Under /simple/ipx/ the native path follows the mount.
	res, err = p.Parse(&dialect.Input{Path: "/_ipx/w_20/a.jpg", Mount: dialect.Mount{Prefix: "/simple/ipx"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.SourceString != "a.jpg" || res.Request.Pipelines[0].Resize.Width.Px != 20 {
		t.Errorf("got %q %+v", res.SourceString, res.Request.Pipelines[0].Resize)
	}
}

func TestSpecs(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	prefixes := map[string]string{}
	for _, s := range simple.Providers() {
		if seen[s.Name] {
			t.Errorf("duplicate provider %s", s.Name)
		}
		seen[s.Name] = true
		if s.Doc == "" {
			t.Errorf("%s: no Doc", s.Name)
		}
		if len(s.Prefixes) == 0 && len(s.Domains) == 0 {
			t.Errorf("%s: neither prefixes nor domains", s.Name)
		}
		for _, p := range s.Prefixes {
			if other, ok := prefixes[p]; ok {
				t.Errorf("prefix %s of %s already used by %s", p, s.Name, other)
			}
			prefixes[p] = s.Name
		}
		if _, err := simple.New(s, nil); err != nil {
			t.Errorf("%s: %v", s.Name, err)
		}
	}
	if simple.Lookup("nope") != nil {
		t.Error("Lookup of an unknown provider")
	}
	if _, err := simple.New(&simple.Spec{Name: "Bad.Name", Extract: simple.PathSource}, nil); err == nil {
		t.Error("bad name accepted")
	}
	if _, err := simple.New(&simple.Spec{Name: "x"}, nil); err == nil {
		t.Error("spec without extractor accepted")
	}
}

func TestMounts(t *testing.T) {
	t.Parallel()
	dialects, mounts, err := simple.Mounts(&simple.MountOptions{Enabled: []string{"nextjs", "contentful"}, Hosts: []string{"img.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(dialects))
	for k := range dialects {
		keys = append(keys, k)
	}
	if len(keys) != 3 || dialects["simple"] == nil || dialects["simple:nextjs"] == nil || dialects["simple:contentful"] == nil {
		t.Errorf("dialects %v", keys)
	}
	type m struct{ Dialect, Prefix string }
	gotMounts := make([]m, 0, len(mounts))
	for _, mo := range mounts {
		gotMounts = append(gotMounts, m{mo.Dialect, mo.Prefix})
	}
	want := []m{
		{"simple:nextjs", "/_next/image"},
		{"simple:nextjs", "/simple/nextjs/"},
		{"simple:contentful", "/simple/contentful/"},
		{"simple", ""},
	}
	if d := cmp.Diff(want, gotMounts); d != "" {
		t.Errorf("mounts (-want +got):\n%s", d)
	}
	d, ok := dialects["simple"].(*simple.Dispatcher)
	if !ok {
		t.Fatal("dispatcher type")
	}
	for _, tc := range []struct{ host, path, want string }{
		{"img.example.com", "/_next/image", "nextjs"},
		{"img.example.com", "/space/a.jpg", "contentful"},
		{"images.ctfassets.net:443", "/_nextx", "contentful"},
	} {
		if p := d.Select(tc.host, tc.path); p == nil || p.Spec().Name != tc.want {
			t.Errorf("Select(%s, %s) = %v, want %s", tc.host, tc.path, p, tc.want)
		}
	}
	for _, bad := range [][]string{{"nope"}, {"nextjs", "nextjs"}} {
		if _, _, err := simple.Mounts(&simple.MountOptions{Enabled: bad}); err == nil {
			t.Errorf("Mounts(%v) accepted", bad)
		}
	}
	only, _, err := simple.Mounts(&simple.MountOptions{Enabled: []string{"nextjs"}, Hosts: []string{"h"}})
	if err != nil {
		t.Fatal(err)
	}
	if p := only["simple"].(*simple.Dispatcher).Select("h", "/elsewhere"); p != nil { //nolint:forcetypeassert // built above
		t.Errorf("no CDN-style provider enabled, got %s", p.Spec().Name)
	}
}
