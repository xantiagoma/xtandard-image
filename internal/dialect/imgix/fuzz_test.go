package imgix_test

import (
	"errors"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// FuzzParse feeds arbitrary paths and queries to every parser
// configuration. Parse must never panic and must return either a
// well-formed result that validates after defaults, or an errs error of a
// client kind; argument errors must name a field.
func FuzzParse(f *testing.F) {
	for _, s := range [][2]string{
		{img, "w=400&h=300&fit=crop&crop=faces,entropy&auto=format,compress&q=60&dpr=2"},
		{img, "w=0.5&fit=crop&crop=focalpoint&fp-x=0.2&fp-y=0.9&fp-z=4&ar=16:9&rect=1,2,3,4"},
		{img, "fit=fill&fill=blur&fill-color=80ff0000&bg=red&pad=3&border=2,000&border-radius=1,2,3,4"},
		{img, "orient=5&rot=33&flip=hv&bri=10&con=-10&gam=-100&usm=20&usmrad=500&sepia=1"},
		{img, "trim=color&trim-color=fff&trim-tol=3&mask=ellipse&mask-bg=0000&page=2&frame=1"},
		{img, "mark64=aHR0cHM6Ly9lLmNvbS9sLnBuZw&mark-align=top,left&txt=hi&txt-size=9&txt-color=f00"},
		{img, "fm=json&ch=Width,DPR&dl=a.jpg&expires=1&cs=strip&chromasub=420&s=00"},
		{"/https%3A%2F%2Fe.com%2Fa.jpg", "w=1&s=6797c24146142d5b40bde3141fd3600c"},
		{"/a%zz", "w=%zz"},
		{"", ""},
		{"/", "&&&"},
		{img, "txt64=***&foo=bar&bg-remove=1"},
	} {
		f.Add(s[0], s[1])
	}
	fx := getFixture(f)
	f.Fuzz(func(t *testing.T, path, query string) {
		for name, p := range fx.parsers {
			res, err := p.Parse(&dialect.Input{Path: path, RawQuery: query, Mount: dialect.Mount{Source: "assets"}})
			if err != nil {
				if res != nil {
					t.Fatalf("%s: result with error", name)
				}
				if _, ok := errors.AsType[*errs.Error](err); !ok {
					t.Fatalf("%s: %q?%q: error %T is not an errs.Error", name, path, query, err)
				}
				switch errs.KindOf(err) {
				case errs.KindInvalidURL, errs.KindUnauthorized, errs.KindExpired, errs.KindUnsupported:
				default:
					t.Fatalf("%s: %q?%q: unexpected kind %v: %v", name, path, query, errs.KindOf(err), err)
				}
				if ae, ok := errors.AsType[*dialect.ArgumentError](err); ok && ae.Field == "" {
					t.Fatalf("%s: argument error without field: %v", name, err)
				}
				continue
			}
			r := res.Request
			if res.SourceString == "" || res.SourceString != r.Source.Raw {
				t.Fatalf("%s: %q?%q: bad source %q", name, path, query, res.SourceString)
			}
			if len(r.Pipelines) != 1 {
				t.Fatalf("%s: %q?%q: %d pipelines", name, path, query, len(r.Pipelines))
			}
			r.ApplyDefaults(transform.StandardDefaults())
			if err := r.Validate(); err != nil {
				t.Fatalf("%s: %q?%q: parsed request does not validate: %v", name, path, query, err)
			}
			_ = r.CanonicalKey()
		}
	})
}

func BenchmarkParse(b *testing.B) {
	f := getFixture(b)
	cases := []struct{ name, cfg, path, query string }{
		{"unpic_default", "default", "/photo-1674255909399-9bcb2cab6489", "w=640&h=480&fit=min&auto=format"},
		{"typical_signed", "required", "/users/1.png", "w=400&h=300&s=c7b86f666a832434dd38577e38cf86d1"},
		{"many_params", "default", img, "w=800&h=600&fit=crop&crop=faces,entropy&auto=format,compress&q=70&dpr=2&bg=fff&sharp=10&ixlib=js-3.8.0"},
		{"web_proxy_b64", "default", "/https%3A%2F%2Fexample.com%2Fa.jpg", "w=300&txt64=SGVsbG8sIFdvcmxkIQ&mark64=aHR0cHM6Ly9hc3NldHMuaW1naXgubmV0L2xvZ28ucG5n"},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			p := f.parsers[c.cfg]
			in := &dialect.Input{Path: c.path, RawQuery: c.query}
			if _, err := p.Parse(in); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				_, _ = p.Parse(in)
			}
		})
	}
}
