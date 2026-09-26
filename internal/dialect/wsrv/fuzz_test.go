package wsrv_test

import (
	"errors"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// FuzzParse feeds arbitrary paths and queries to every parser
// configuration. Parse must never panic, must return either a well-formed
// result or an errs error of a client kind, and argument errors must name
// a field.
func FuzzParse(f *testing.F) {
	for _, s := range [][3]string{
		{"/", src + "&w=300&h=300&fit=cover&a=attention&output=webp&q=80&dpr=2", ""},
		{"/", "url=ssl:e.com/a.jpg&w=50%25&filt=duotone&start=f00&stop=80ffffff&default=e.com/404.png", ""},
		{"/", src + "&cx=10%&cy=5&cw=1&ch=2&fit=contain&cbg=red&ro=45&rbg=0f0&mask=heart&mtrim&mbg=fff", ""},
		{"/", src + "&mod=1,2,3&sharp=1,2,3&crop=1,2,3,4&a=focal-10-90&trim&tbg=abc&maxage=1w 2d", ""},
		{"/", src + "&n=-1&page=-2&l=9&af&il&ll&encoding=base64&filename=x&loop&delay=1,2", ""},
		{"/", "url=photos/a.jpg&default=b.png", "assets"},
		{"/", "url=%zz&w=%&xi_sig=AA", ""},
		{"", "", ""},
		{"/x", "&&&==", ""},
	} {
		f.Add(s[0], s[1], s[2] != "")
	}
	fx := getFixture(f)
	f.Fuzz(func(t *testing.T, path, query string, mounted bool) {
		mount := ""
		if mounted {
			mount = "assets"
		}
		for name, p := range fx.parsers {
			res, err := p.Parse(&dialect.Input{Path: path, RawQuery: query, Mount: dialect.Mount{Source: mount}})
			if err != nil {
				if res != nil {
					t.Fatalf("%s: result with error", name)
				}
				if _, ok := errors.AsType[*errs.Error](err); !ok {
					t.Fatalf("%s: %q?%q: error %T is not an errs.Error", name, path, query, err)
				}
				switch errs.KindOf(err) {
				case errs.KindInvalidURL, errs.KindUnauthorized, errs.KindForbidden, errs.KindExpired:
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
			if n := len(r.Pipelines); n < 1 || n > 2 {
				t.Fatalf("%s: %q?%q: %d pipelines", name, path, query, n)
			}
			r.ApplyDefaults(transform.StandardDefaults())
			if err := r.Validate(); err != nil {
				t.Fatalf("%s: %q?%q: parsed request does not validate: %v", name, path, query, err)
			}
			_ = r.CanonicalKey()
		}
	})
}
