package simple_test

import (
	"errors"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/simple"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// FuzzParse feeds arbitrary paths and queries to every provider (strict
// and lenient) and to the host dispatcher. Parsing must never panic, must
// fail only with a 400 or 403 *errs.Error, and a parsed request must name
// its source and validate after ApplyDefaults.
func FuzzParse(f *testing.F) {
	seeds := [][2]string{
		{"/_next/image", "url=%2Fa.jpg&w=640&q=75"},
		{"/.netlify/images", "url=/a.jpg&w=1&h=2&fit=cover&position=top&fm=webp"},
		{"/_ipx/s_200x100,f_webp/https://example.com/a.jpg", ""},
		{"/_image", "href=https%3A%2F%2Fa.com%2Fb.png&w=1&fit=scale-down"},
		{"/assets/abc", "width=10&fit=contain&withoutEnlargement=true"},
		{"/f/1/2x2/h/a.jpg/m/fit-in/-300x0/filters:format(webp):fill(fff)", ""},
		{"/200x0/filters:rotate(90)/f/1/a.jpg", ""},
		{"/uuid/-/resize/300x/-/format/auto/a.jpg", ""},
		{"/env/resize=fit:crop,width:1/auto_image/handle", ""},
		{"/a.jpg", "imgeng=/w_1/h_2/m_cropbox&x=y"},
		{"/v7/example.com%2Fa.jpg", "ci_url_encoded=1&w=1&func=face"},
		{"/is/image/c/a", "wid=1&fit=crop,1&qlt=80,1&flip=lrud"},
		{"/s/files/a_100x200_crop_center.jpg.webp", "v=1&crop=top"},
		{"/storage/v1/render/image/public/b/a.jpg", "width=1&resize=fill"},
		{"/v1/storage/buckets/b/files/f/preview", "project=p&gravity=top-left"},
		{"/a:b/%zz", "w=%zz&xi_sig=abc"},
	}
	for _, s := range seeds {
		f.Add(s[0], s[1])
	}
	specs := simple.Providers()
	parsers := make([]dialect.Dialect, 0, 2*len(specs)+1)
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Name)
		for _, strict := range []bool{false, true} {
			p, err := simple.New(spec, &simple.Options{Strict: strict})
			if err != nil {
				f.Fatal(err)
			}
			parsers = append(parsers, p)
		}
	}
	dialects, _, err := simple.Mounts(&simple.MountOptions{Enabled: names, Hosts: []string{"h"}})
	if err != nil {
		f.Fatal(err)
	}
	parsers = append(parsers, dialects[simple.Name])
	f.Fuzz(func(t *testing.T, path, rawQuery string) {
		for _, p := range parsers {
			res, err := p.Parse(&dialect.Input{Path: path, RawQuery: rawQuery, Host: "h", Mount: dialect.Mount{Source: "s"}})
			if err != nil {
				e, ok := errors.AsType[*errs.Error](err)
				if !ok || (e.Kind != errs.KindInvalidURL && e.Kind != errs.KindForbidden && e.Kind != errs.KindUnauthorized) {
					t.Fatalf("%s: unexpected error %v", p.Name(), err)
				}
				continue
			}
			r := res.Request
			if res.SourceString == "" || res.SourceString != r.Source.Raw {
				t.Fatalf("%s: source %q / %q", p.Name(), res.SourceString, r.Source.Raw)
			}
			r.ApplyDefaults(transform.StandardDefaults())
			if err := r.Validate(); err != nil {
				t.Fatalf("%s: parsed request does not validate: %v", p.Name(), err)
			}
		}
	})
}
