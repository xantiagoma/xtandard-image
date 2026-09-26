package svg

import "testing"

func TestSanitizeCSS(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		in        string
		allowData bool
		want      string
		verdict   cssVerdict
	}{
		{name: "plain", in: "rect{fill:red}", want: "rect{fill:red}", verdict: cssUnchanged},
		{name: "fragment url", in: "fill:url(#a)", want: "fill:url(#a)", verdict: cssUnchanged},
		{name: "quoted fragment url", in: `fill:url( '#a' )`, want: `fill:url( '#a' )`, verdict: cssUnchanged},
		{name: "empty url", in: "fill:url()", want: "fill:url()", verdict: cssUnchanged},
		{name: "external url", in: "fill:url(https://e.example/a#b) red", want: "fill:none red", verdict: cssRewritten},
		{name: "relative url", in: "fill:url(a.svg#b)", want: "fill:none", verdict: cssRewritten},
		{name: "upper case", in: "fill:URL(//e.example/a)", want: "fill:none", verdict: cssRewritten},
		{name: "quoted with paren", in: `fill:url("a)b") x`, want: "fill:none x", verdict: cssRewritten},
		{name: "unterminated", in: "fill:url(http://e.example/", want: "fill:none", verdict: cssRewritten},
		{name: "src function", in: `src:src("f.woff")`, want: "src:none", verdict: cssRewritten},
		{name: "data image refused by default", in: "fill:url(data:image/png;base64,AA)", want: "fill:none", verdict: cssRewritten},
		{name: "data image allowed", in: "fill:url(data:image/png;base64,AA)", allowData: true, want: "fill:url(data:image/png;base64,AA)", verdict: cssUnchanged},
		{name: "data svg refused", in: "fill:url(data:image/svg+xml,x)", allowData: true, want: "fill:none", verdict: cssRewritten},
		{name: "image-set", in: `b:image-set("a.png" 1x, url(b.png) 2x);c:d`, want: "b:none;c:d", verdict: cssRewritten},
		{name: "vendor image-set", in: `b:-webkit-image-set("a.png" 1x)`, want: "b:none", verdict: cssRewritten},
		{name: "cross-fade", in: `b:cross-fade(url(#a), "b.png")`, want: "b:none", verdict: cssRewritten},
		{name: "not a url function", in: "b:myurl(x) x-url(y) 1url(z)", want: "b:myurl(x) x-url(y) 1url(z)", verdict: cssUnchanged},
		{name: "url identifier without paren", in: "url /* x */ (a)", want: "url /* x */ (a)", verdict: cssUnchanged},
		{name: "import string", in: `@import "a.css";p{}`, want: " p{}", verdict: cssRewritten},
		{name: "import url media", in: `@import url(a.css) screen and (x:y);p{}`, want: " p{}", verdict: cssRewritten},
		{name: "import quoted semicolon", in: `@import "a;b.css";p{}`, want: " p{}", verdict: cssRewritten},
		{name: "import unterminated", in: `@import "a.css"`, want: " ", verdict: cssRewritten},
		{name: "import before block", in: `@import x{p{}}`, want: " {p{}}", verdict: cssRewritten},
		{name: "import splice", in: `@im@import x;port`, want: "@im port", verdict: cssRewritten},
		{name: "other at-rule", in: `@media print{p{}}`, want: `@media print{p{}}`, verdict: cssUnchanged},
		{name: "escaped url", in: `fill:\75 rl(http://e.example/)`, verdict: cssRejected},
		{name: "escaped import", in: `@\69mport "x";`, verdict: cssRejected},
		{name: "escaped char", in: `fill:u\rl(x)`, verdict: cssRejected},
		{name: "escape elsewhere", in: `fill:url(http://e.example/) .\31 a{}`, verdict: cssRejected},
		{name: "harmless escape", in: `.\31 a{content:"\201C"}`, want: `.\31 a{content:"\201C"}`, verdict: cssUnchanged},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, verdict := sanitizeCSS(tc.in, tc.allowData)
			if verdict != tc.verdict {
				t.Fatalf("verdict = %d, want %d (got %q)", verdict, tc.verdict, got)
			}
			if verdict != cssRejected && got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCSSUnescape(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{`\75 rl`, "url"},
		{`\000075rl`, "url"},
		{`\75` + "\r\n" + `rl`, "url"},
		{`u\rl`, "url"},
		{`a\` + "\n" + `b`, "ab"},
		{`\0`, "�"},
		{`\D800`, "�"},
		{`\110000`, "�"},
		{`\`, ""},
		{`\é`, "é"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := cssUnescape(tc.in); got != tc.want {
				t.Errorf("cssUnescape(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
