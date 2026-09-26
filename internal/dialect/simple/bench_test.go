package simple_test

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
)

// BenchmarkParse measures one parse per URL shape: a query-source proxy
// (nextjs), a path grammar (ipx), a CDN-style query (contentful) and the
// most involved path grammar (storyblok).
func BenchmarkParse(b *testing.B) {
	cases := []struct{ provider, path, query string }{
		{"nextjs", "/_next/image", "url=%2F_next%2Fstatic%2Fmedia%2Fbunny.0e498116.jpg&w=828&q=75"},
		{"ipx", "/_ipx/s_800x600,q_80,f_webp,fit_cover/images/test.jpg", ""},
		{"contentful", "/aaaa/xxxx/yyyy/how-to-wow-a-customer.jpg", "w=400&h=300&fit=fill&fm=webp&q=80&f=faces"},
		{"storyblok", "/f/39898/3310x2192/e4ec08624e/demo-image.jpeg/m/400x300/filters:format(webp):quality(80)", ""},
	}
	for _, c := range cases {
		b.Run(c.provider, func(b *testing.B) {
			p := parser(b, c.provider, nil)
			in := &dialect.Input{Path: c.path, RawQuery: c.query, Mount: dialect.Mount{Source: "origin"}}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := p.Parse(in); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
