package imgproxy_test

import "testing"

func BenchmarkParse(b *testing.B) {
	f := getFixture(b)
	cases := []struct{ name, cfg, path string }{
		{"plain_simple", "default", "/unsafe/rs:fit:300:300/plain/https://example.com/a.jpg@webp"},
		{"base64_signed_docs", "signed", "/" + docsSig + docsPath},
		{"plain_signed", "signed", "/" + exampleSig + examplePath},
		{"typical_8_options", "default", "/unsafe/rs:fill:800:600:1/g:sm/q:82/f:webp/sm:1/dpr:2/sh:0.3/cb:v1/plain/https://example.com/images/photo.jpg"},
		{"chained_3", "default", "/unsafe/rs:fit:500:500/wm:0.5:nowe/-/wm:0.7:soea/-/bl:2/plain/s3://bucket/key.jpg"},
		{"enc", "enc", "/unsafe/w:100/enc/" + encToken + ".png"},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			p := f.parsers[c.cfg]
			if _, err := p.ParsePath(c.path); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				_, _ = p.ParsePath(c.path)
			}
		})
	}
}
