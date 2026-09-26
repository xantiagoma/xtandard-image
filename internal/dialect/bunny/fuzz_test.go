package bunny_test

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
	for _, s := range [][2]string{
		{img, "width=400&height=300&aspect_ratio=16:9&crop_gravity=north&quality=80&format=webp"},
		{img, "crop=400,300,200,150&focus_crop=400,300,0.5,0.5&face_crop=600,600&upscaling=resampling"},
		{img, "flip=true&flop=1&rotate=-90&blur=25&sharpen=3&brightness=-30&gamma=40&contrast=5&saturation=-100&hue=33&tint=aaffff&sepia=75"},
		{img, "class=thumbnail&auto_optimize=high&output=avif&optimizer=image&token_countries=US&limit=5"},
		{"/300kb.jpg", "token=HS256-o10JRWlsAItyAsdKS6jJKjabHN4FrFsplDHPV1idcX4&expires=1598024587"},
		{"/abc/300kb.jpg", "token=xwPaUzEMSgOZ7yl86K55G7len9n1UMiuP36IAyw8Mjs&token_path=%2Fabc&expires=1598024587"},
		{"/bcdn_token=e0fYj-NC_YeROS_0gTGvscP7HR_Du78I7WBVSDV8P4E&expires=1598024587/abc/", ""},
		{"/bcdn_token=HS256-1-x&token_ignore_params=true&expires=1/a", "token=y"},
		{"/https:/x/a.jpg", "expires=-1&aspect_ratio=1e308:1e-308"},
		{"/%zz", "width=%zz"},
		{"", ""},
		{"//", "&&&"},
	} {
		f.Add(s[0], s[1])
	}
	fx := getFixture(f)
	f.Fuzz(func(t *testing.T, path, query string) {
		for name, p := range fx.parsers {
			res, err := p.Parse(&dialect.Input{Path: path, RawQuery: query, Mount: dialect.Mount{Source: mountSource}})
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
			if res.SourceString == "" || res.SourceString != r.Source.Raw || res.SourceString[0] == '/' {
				t.Fatalf("%s: %q?%q: bad source %q", name, path, query, res.SourceString)
			}
			if len(r.Pipelines) != 1 {
				t.Fatalf("%s: %q?%q: %d pipelines", name, path, query, len(r.Pipelines))
			}
			r.ApplyDefaults(transform.StandardDefaults())
			if err := r.Validate(); err != nil {
				t.Fatalf("%s: %q?%q: Validate: %v", name, path, query, err)
			}
			_ = r.CanonicalKey()
		}
	})
}

func BenchmarkParse(b *testing.B) {
	f := getFixture(b)
	cases := []struct{ name, cfg, path, query string }{
		{"width_only", "default", "/img/a.jpg", "width=400"},
		{"typical_6_options", "default", "/img/a.jpg", "width=800&height=600&aspect_ratio=4:3&crop_gravity=north&quality=80&format=webp"},
		{"class", "presets", "/img/a.jpg", "class=thumbnail"},
		{"hs256_signed", "signed", "/300kb.jpg", "token=HS256-o10JRWlsAItyAsdKS6jJKjabHN4FrFsplDHPV1idcX4&expires=1598024587"},
		{"sha256_path_token", "signed", "/bcdn_token=xwPaUzEMSgOZ7yl86K55G7len9n1UMiuP36IAyw8Mjs&token_path=%2Fabc&expires=1598024587/abc/", ""},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			p := f.parsers[c.cfg]
			if _, err := p.ParseURL(c.path, c.query); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				_, _ = p.ParseURL(c.path, c.query)
			}
		})
	}
}
