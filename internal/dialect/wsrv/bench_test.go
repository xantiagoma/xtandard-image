package wsrv_test

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
)

func BenchmarkParse(b *testing.B) {
	f := getFixture(b)
	signed, err := f.xi.SignXIQuery("/", "url=example.com/a.jpg&w=300&h=300&fit=cover&output=webp&q=80")
	if err != nil {
		b.Fatal(err)
	}
	cases := []struct{ name, cfg, query string }{
		{"unpic_simple", "default", "url=example.com%2Fimage.jpg&w=300&fit=cover"},
		{"typical_8_params", "default", "url=example.com/photos/a.jpg&w=800&h=600&fit=cover&a=attention&output=webp&q=82&dpr=2&maxage=31d"},
		{"effects_postcrop", "default", "url=ssl:example.com/a.jpg&w=500&cx=10&cy=20&cw=300&ch=200&filt=duotone&mod=1.2,0.8,30&sharp=2&bg=%23ff8800&mask=circle"},
		{"signed", "signed", signed},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			p := f.parsers[c.cfg]
			in := &dialect.Input{Path: "/", RawQuery: c.query}
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
