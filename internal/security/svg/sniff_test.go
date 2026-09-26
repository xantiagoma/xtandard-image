package svg

import (
	"strings"
	"testing"
)

func TestIsSVG(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		head string
		want bool
	}{
		{name: "bare", head: `<svg/>`, want: true},
		{name: "attributes", head: `<svg xmlns="http://www.w3.org/2000/svg" width="1">`, want: true},
		{name: "newline after name", head: "<svg\n  width=\"1\">", want: true},
		{name: "prefixed", head: `<svg:svg xmlns:svg="http://www.w3.org/2000/svg">`, want: true},
		{name: "declaration", head: `<?xml version="1.0" encoding="UTF-8"?>` + "\n<svg>", want: true},
		{name: "BOM", head: utf8BOM + `<?xml version="1.0"?><svg>`, want: true},
		{name: "comments", head: "<!-- a -->\n<!-- <html> -->\n<svg>", want: true},
		{name: "doctype", head: `<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd">` + "\n<svg>", want: true},
		{name: "doctype subset", head: `<!DOCTYPE svg [<!ENTITY a "<b>">]><svg>`, want: true},
		{name: "stylesheet pi", head: `<?xml-stylesheet href="a.css"?><svg>`, want: true},
		{name: "leading whitespace", head: "\r\n\t <svg>", want: true},
		{name: "html", head: `<html><svg>`, want: false},
		{name: "svg prefix of name", head: `<svgfoo>`, want: false},
		{name: "upper case", head: `<SVG>`, want: false},
		{name: "text first", head: `hello <svg>`, want: false},
		{name: "empty", head: ``, want: false},
		{name: "png", head: "\x89PNG\r\n\x1a\n", want: false},
		{name: "unterminated comment", head: `<!-- <svg>`, want: false},
		{name: "unterminated declaration", head: `<?xml version="1.0"`, want: false},
		{name: "unterminated doctype", head: `<!DOCTYPE svg [ <svg>`, want: false},
		{name: "name cut by window", head: `<svg`, want: false},
		{name: "svg past 4 KiB", head: "<!--" + strings.Repeat("x", SniffLen) + "--><svg>", want: false},
		{name: "svg just inside 4 KiB", head: "<!--" + strings.Repeat("x", SniffLen-13) + "--><svg>", want: true},
		{name: "utf-16", head: "\xff\xfe<\x00s\x00v\x00g\x00", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsSVG([]byte(tc.head)); got != tc.want {
				t.Errorf("IsSVG(%.40q) = %v, want %v", tc.head, got, tc.want)
			}
		})
	}
}
