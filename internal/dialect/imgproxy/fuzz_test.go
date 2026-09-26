package imgproxy_test

import (
	"errors"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// FuzzParse feeds arbitrary paths to every parser configuration. Parse must
// never panic, must return either a well-formed result or an errs error of
// a client kind, and argument errors must name a field.
func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"/unsafe/rs:fit:300:300/plain/https://example.com/a.jpg@webp",
		"/" + docsSig + docsPath,
		"/" + exampleSig + examplePath,
		"/unsafe/enc/" + encToken + ".png",
		"/unsafe/rs:fit:500:500/wm:0.5:nowe/-/wm:0.7:soea/plain/s3://bucket/key.jpg",
		"/unsafe/rs,fill,1,1/-/q,80/aHR0cDovL2UuY29tL2E.png",
		"/unsafe/g:objw:a:1:b:2/c:0.5:0.5:fp:0.1:0.2/t:1:fff:1:1/pd:1::3/fq:png:1/plain/x@",
		"/unsafe/rs%3Afill/plain/local:/x",
		"/unsafe/-/-/plain/x",
		"/x/fill/1/1/aGk",
		"/unsafe/wm:1:re:1:1:1/bg:1:2:3/bga:0.5/exp:1/fn:eA:1/plain/%zz",
		"",
		"/",
		"//////",
		"/unsafe/:/:::/plain/@@",
	} {
		f.Add(s)
	}
	for _, e := range loadJSCore(f) {
		f.Add("/unsafe" + e.Path)
	}
	fx := getFixture(f)
	f.Fuzz(func(t *testing.T, path string) {
		for name, p := range fx.parsers {
			res, err := p.ParsePath(path)
			if err != nil {
				if res != nil {
					t.Fatalf("%s: result with error", name)
				}
				if _, ok := errors.AsType[*errs.Error](err); !ok {
					t.Fatalf("%s: %q: error %T is not an errs.Error", name, path, err)
				}
				switch errs.KindOf(err) {
				case errs.KindInvalidURL, errs.KindUnauthorized, errs.KindForbidden, errs.KindExpired:
				default:
					t.Fatalf("%s: %q: unexpected kind %v: %v", name, path, errs.KindOf(err), err)
				}
				if ae, ok := errors.AsType[*dialect.ArgumentError](err); ok && ae.Field == "" {
					t.Fatalf("%s: argument error without field: %v", name, err)
				}
				continue
			}
			r := res.Request
			if res.SourceString == "" || res.SourceString != r.Source.Raw {
				t.Fatalf("%s: %q: bad source %q", name, path, res.SourceString)
			}
			if n := len(r.Pipelines); n < 1 || n > transform.MaxPipelines {
				t.Fatalf("%s: %q: %d pipelines", name, path, n)
			}
			// The request must survive defaults and canonical encoding.
			r.ApplyDefaults(transform.StandardDefaults())
			_ = r.Validate()
			_ = r.CanonicalKey()
		}
	})
}
