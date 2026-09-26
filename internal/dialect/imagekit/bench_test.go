package imagekit_test

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
)

func BenchmarkParse(b *testing.B) {
	f := getFixture(b)
	signed, err := f.signer.SignImageKitQuery("/folder/a.jpg", "tr=w-400,h-300,fo-auto", 1893456000)
	if err != nil {
		b.Fatal(err)
	}
	cases := []struct{ name, cfg, path, query string }{
		{"path_simple", "default", "/tr:w-300,h-200/folder/a.jpg", ""},
		{"query_unpic", "default", "/folder/a.jpg", "tr=w-300,h-300,c-maintain_ratio,fo-auto"},
		{"typical_8_params", "default", "/tr:w-800,h-600,c-maintain_ratio,fo-auto,q-82,f-webp,dpr-2,e-sharpen/folder/photo.jpg", ""},
		{"chained_3", "default", "/folder/a.jpg", "tr=w-500,h-500,cm-pad_resize,bg-white:l-image,i-logo.png,lx-10,ly-10,l-end:bl-4"},
		{"signed_expiring", "signed", "/folder/a.jpg", signed},
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
