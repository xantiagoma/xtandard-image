package thumbor_test

import "testing"

func BenchmarkParse(b *testing.B) {
	f := getFixture(b)
	cases := []struct{ name, cfg, path string }{
		{"unsafe_simple", "default", "/unsafe/300x200/https://example.com/a.jpg"},
		{"signed_vector", "signed", "/_wIUeSaeHw8dricKG2MGhqu5thk=/smart/image.jpg"},
		{"typical", "default", "/unsafe/fit-in/800x600/smart/filters:format(webp):quality(82):strip_exif()/" + gh},
		{"all_segments", "default", "/unsafe/meta/trim:bottom-right:5/10x20:310x220/adaptive-full-fit-in/-300x-0/right/bottom/smart/filters:quality(50):watermark(https://example.com/w.png,-10,-10,50,20,none)/s3-key/a.jpg"},
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
