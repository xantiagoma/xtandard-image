package awssih_test

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
)

func BenchmarkParse(b *testing.B) {
	f := getFixture(b)
	typical := b64raw(`{"bucket":"validBucket","key":"photos/2026/cat.jpg","edits":{"resize":{"width":800,"height":600,"fit":"cover","position":"attention"},"jpeg":{"quality":82,"progressive":true},"sharpen":{"sigma":0.5}},"outputFormat":"webp","headers":{"Cache-Control":"max-age=86400"}}`)
	const fixturePath = "/eyJidWNrZXQiOiJ2YWxpZEJ1Y2tldCIsImtleSI6InZhbGlkS2V5IiwiZWRpdHMiOnsidG9Gb3JtYXQiOiJwbmcifX0="
	cases := []struct{ name, cfg, path, query string }{
		{"minimal", "default", b64raw(`{"key":"a.jpg"}`), ""},
		{"typical_edits", "default", typical, ""},
		{"signed_fixture", "signed", fixturePath, "signature=4d41311006641a56de7bca8abdbda91af254506107a2c7b338a13ca2fa95eac3"},
		{"query_mapper", "query", typical, "width=400&format=avif&grayscale=true"},
		{"thumbor_forward", "thumbor", "/fit-in/300x300/filters:grayscale()/s3:sample-bucket/a.jpg", ""},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			p := f.parsers[c.cfg]
			in := &dialect.Input{Path: c.path, RawQuery: c.query, Mount: dialect.Mount{Source: mountSource}}
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
