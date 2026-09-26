package cloudflare_test

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
)

func BenchmarkParse(b *testing.B) {
	fx := getFixture(b)
	sig, err := fx.cf.SignCloudflareQuery(delivery+"/w=400,sharpen=3", "exp="+expFuture)
	if err != nil {
		b.Fatal(err)
	}
	cases := []struct {
		name, cfg, path, query string
	}{
		{"image/unpic", "default", "/background=red,width=128,height=128,f=auto,fit=cover/uploads/generic/avatar-sample.jpeg", ""},
		{"image/absolute", "default", "/width=400,height=300,fit=cover,gravity=auto,q=80,f=webp/https://example.com/uploads/a.jpg", "v=1"},
		{"images/variant", "images", delivery + "/thumbnail", ""},
		{"images/flexible_signed", "images_signed", delivery + "/w=400,sharpen=3", sig},
	}
	for _, c := range cases {
		p := fx.parsers[c.cfg]
		in := &dialect.Input{Path: c.path, RawQuery: c.query}
		if _, err := p.Parse(in); err != nil {
			b.Fatalf("%s: %v", c.name, err)
		}
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, _ = p.Parse(in)
			}
		})
	}
}
