package svg

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func BenchmarkSanitize(b *testing.B) {
	large := `<svg xmlns="http://www.w3.org/2000/svg">` +
		strings.Repeat(`<path d="M10 10 L20 20 Q 30 40 50 50" fill="url(#g)" stroke="#000"/><text x="1">hi &amp; bye</text>`, 5000) +
		`</svg>`
	cases := []struct {
		name string
		in   []byte
		opts Options
	}{
		{name: "inkscape", in: readTestdata(b, "benign/inkscape.svg")},
		{name: "style", in: readTestdata(b, "benign/style-cdata-bom-crlf.svg")},
		{name: "malicious-css", in: readTestdata(b, "malicious/css-url.svg")},
		{name: "malicious-script", in: readTestdata(b, "malicious/script.svg")},
		{name: "large", in: []byte(large)},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(c.in)))
			for b.Loop() {
				if err := Sanitize(io.Discard, bytes.NewReader(c.in), c.opts); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkIsSVG(b *testing.B) {
	head := readTestdata(b, "benign/inkscape.svg")
	b.ReportAllocs()
	for b.Loop() {
		if !IsSVG(head) {
			b.Fatal("not detected")
		}
	}
}
