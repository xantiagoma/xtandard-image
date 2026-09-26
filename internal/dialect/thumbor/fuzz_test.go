package thumbor_test

import (
	"errors"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// FuzzParse feeds arbitrary paths to every parser configuration. Parse must
// never panic, must return either a well-formed, valid result or an errs
// error of a client kind, and argument errors must name a field.
func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"/unsafe/300x200/smart/filters:format(webp):quality(80)/https://example.com/a.jpg",
		"/unsafe/debug/meta/trim:bottom-right:5/10x20:310x220/adaptive-full-fit-in/-300x-0/right/bottom/smart/filters:quality(50)/s3-key/a.jpg",
		"/unsafe/filters:watermark(http://example.com/w.png,-10p,repeat,50,20,none):round_corner(20|40,0,0,0,1)/a.jpg",
		"/unsafe/filters:focal(0.1x0.2:0.3x0.6):fill(blur,true):rgb(1,2,3):sharpen(2,1.0,true)/a.jpg",
		"/unsafe/300x100/filters:extract_focal()/localhost:8888/unsafe/240x220:480x260/" + gh,
		"/_wIUeSaeHw8dricKG2MGhqu5thk=/smart/image.jpg",
		"/unsafe/filters:a(b))/x",
		"/unsafe/origx-orig/%zz",
		"",
		"//",
	} {
		f.Add(s)
	}
	fx := getFixture(f)
	f.Fuzz(func(t *testing.T, path string) {
		for name, p := range fx.parsers {
			res, err := p.Parse(&dialect.Input{Path: path})
			if err != nil {
				if res != nil {
					t.Fatalf("%s: result with error", name)
				}
				if _, ok := errors.AsType[*errs.Error](err); !ok {
					t.Fatalf("%s: %q: error %T is not an errs.Error", name, path, err)
				}
				switch errs.KindOf(err) {
				case errs.KindInvalidURL, errs.KindUnauthorized:
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
			if len(r.Pipelines) != 1 {
				t.Fatalf("%s: %q: %d pipelines", name, path, len(r.Pipelines))
			}
			r.ApplyDefaults(transform.StandardDefaults())
			if err := r.Validate(); err != nil {
				t.Fatalf("%s: %q: Validate: %v", name, path, err)
			}
			_ = r.CanonicalKey()
		}
	})
}
