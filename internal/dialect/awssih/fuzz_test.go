package awssih_test

import (
	"encoding/base64"
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
		{b64raw(`{"bucket":"validBucket","key":"a.jpg","edits":{"resize":{"width":300,"height":200,"fit":"contain","background":"#fff"},"toFormat":"webp"}}`), ""},
		{b64raw(`{"key":"a.jpg","edits":{"extract":{"left":1,"top":2,"width":3,"height":4},"resize":{"width":"9.5"},"crop":{"left":0,"top":0,"width":1,"height":1},"extend":{"top":1}}}`), "width=5&format=png"},
		{b64raw(`{"key":"a.jpg","edits":{"overlayWith":{"bucket":"bucket","key":"k","options":{"left":"-10p","top":"5"}},"smartCrop":{"faceIndex":1},"roundCrop":true}}`), ""},
		{b64raw(`{"key":"a.jpg","edits":{"jpeg":{"quality":80,"mozjpeg":true},"webp":{"lossless":true,"effort":3}},"outputFormat":"avif","effort":2,"headers":{"Cache-Control":"max-age=1"}}`), "expires=20991231T235959Z"},
		{b64raw(`{"key":"a.jpg","edits":{"a":{"b":{"c":[1,2,{"d":null}]}},"blur":{"sigma":1e308}}}`), "signature=00"},
		{"/eyJrZXkiOiJhPz8_Pj4-LmpwZyJ9", "rotate=&flip=1&grayscale=0"},
		{"/fit-in/300x300/filters:grayscale()/s3:sample-bucket/a.jpg", "width=0"},
		{"/filters-rotate(90)/a.jpg", ""},
		{"/" + base64.StdEncoding.EncodeToString([]byte(`{"key":"x","edits":{"resize":{"ratio":1e-300,"width":1}}}`)), ""},
		{"", ""},
		{"/%zz", "a=%zz"},
		{"/====", "expires=bad"},
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

// FuzzParseJSON fuzzes the JSON document behind the base64 path directly,
// so the fuzzer explores edits rather than base64.
func FuzzParseJSON(f *testing.F) {
	for _, s := range []string{
		`{"key":"a.jpg"}`,
		`{"bucket":"test","key":"a.jpg","edits":{"resize":{"width":10,"height":10,"fit":"cover","position":"attention"},"grayscale":true},"outputFormat":"webp","effort":4}`,
		`{"key":"a.jpg","edits":{"trim":{"threshold":5,"background":{"r":1,"g":2,"b":3,"alpha":0.5}},"rotate":45.5,"tint":"rgba(1,2,3,0.4)","modulate":{"hue":-30}}}`,
		`{"key":"a.jpg","edits":{"png":{"palette":true,"colours":16},"gif":{"effort":1},"flatten":{"background":"navy"}}}`,
		`{"key":"a.jpg","headers":{"Cache-Tag":"a,b","X-Amz-Meta":"x","cache-control":"max-age=abc"}}`,
		`{"key":"a.jpg","edits":{"resize":{"width":1},"extract":{"left":0,"top":0,"width":1,"height":1},"extend":3}}`,
	} {
		f.Add(s, "")
		f.Add(s, "width=10&height=0&fit=inside&format=jpeg&rotate=90&flop=true")
	}
	fx := getFixture(f)
	f.Fuzz(func(t *testing.T, doc, query string) {
		path := "/" + base64.StdEncoding.EncodeToString([]byte(doc))
		for name, p := range fx.parsers {
			res, err := p.Parse(&dialect.Input{Path: path, RawQuery: query, Mount: dialect.Mount{Source: mountSource}})
			if err != nil {
				switch errs.KindOf(err) {
				case errs.KindInvalidURL, errs.KindUnauthorized, errs.KindForbidden, errs.KindExpired:
				default:
					t.Fatalf("%s: %q: unexpected kind %v: %v", name, doc, errs.KindOf(err), err)
				}
				continue
			}
			r := res.Request
			r.ApplyDefaults(transform.StandardDefaults())
			_ = r.Validate()
			_ = r.CanonicalKey()
		}
	})
}
