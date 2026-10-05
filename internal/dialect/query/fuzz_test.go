package query_test

import (
	"errors"
	"net/url"
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
	for _, s := range [][2]string{
		{"/", src + "&w=300&h=200&fit=cover&g=smart&f=webp&q=80&dpr=2"},
		{"/", src + "&o=rs:fill:300:200/g:sm/-/bl:2&crop=1,2,3,4&flip=hv&bg=1,2,3"},
		{"/", src + "&o=w:1/plain/x&o=-/-"},
		{"/", src + "&g=fp:0.1:0.2&trim=1,fff,1,1&pad=1,,3&preset=a,b&expires=1"},
		{"/", "url=%zz&sign=x&xi_sig=y"},
		{"/resize", src + "&width=300&height=200&extend=background&background=1,2,3&nocrop=0"},
		{"/zoom", src + "&factor=3&top=5&left=5&areawidth=10"},
		{"/watermark", src + "&text=hi&opacity=3&color=1,2,3"},
		{"/pipeline", src + "&operations=" + url.QueryEscape(`[{"operation":"crop","params":{"width":300,"height":"260"}},{"operation":"convert","params":{"type":"webp"}}]`)},
		{"/pipeline", src + "&operations=" + url.QueryEscape(`[{"operation":"zoom","params":{"factor":1e300}}]`)},
		{"/info", src},
		{"/video", src + "&start=1&duration=5&width=640&format=webm&mute=1"},
		{"/video", src + "&fit=cover&g=southeast:1:2&crop=1,2,3,4&flip=hv&vb=2M&ab=96k&vol=-6dB&ln=1,-16&fade=1,2&o=rs:fill:1:1/vd:1"},
		{"/video/", src + "&waveform=1,fff,000,1&f=webp&sr=44100&ach=1"},
		{"/%zz", src},
		{"", ""},
		{"//", "&&&"},
	} {
		f.Add(s[0], s[1])
	}
	fx := getFixture(f)
	f.Fuzz(func(t *testing.T, path, query string) {
		for name, p := range fx.parsers {
			res, err := p.Parse(&dialect.Input{Path: path, RawQuery: query})
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
			if n := len(r.Pipelines); n < 1 || n > transform.MaxPipelines {
				t.Fatalf("%s: %q?%q: %d pipelines", name, path, query, n)
			}
			r.ApplyDefaults(transform.StandardDefaults())
			_ = r.Validate()
			_ = r.CanonicalKey()
		}
	})
}
