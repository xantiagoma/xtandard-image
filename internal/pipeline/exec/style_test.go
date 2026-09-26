package exec

import (
	"bytes"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/security/svg"
)

func TestInjectStyle(t *testing.T) {
	t.Parallel()
	const style = `<style xmlns="http://www.w3.org/2000/svg">`
	for _, tc := range []struct {
		name, doc, css, want string
	}{
		{
			name: "first child of the root",
			doc:  `<?xml version="1.0"?>` + "\n" + `<svg xmlns="http://www.w3.org/2000/svg" width="2"><rect/></svg>`,
			css:  "rect{fill:red}",
			want: `<?xml version="1.0"?>` + "\n" + `<svg xmlns="http://www.w3.org/2000/svg" width="2">` + style + `rect{fill:red}</style><rect/></svg>`,
		},
		{
			name: "self-closing root",
			doc:  `<svg xmlns="http://www.w3.org/2000/svg" />`,
			css:  "a{}",
			want: `<svg xmlns="http://www.w3.org/2000/svg" >` + style + `a{}</style></svg>`,
		},
		{
			name: "prefixed root",
			doc:  `<s:svg xmlns:s="http://www.w3.org/2000/svg"/>`,
			css:  "a{}",
			want: `<s:svg xmlns:s="http://www.w3.org/2000/svg">` + style + `a{}</style></s:svg>`,
		},
		{
			name: "markup in the css stays text",
			doc:  `<svg xmlns="http://www.w3.org/2000/svg"></svg>`,
			css:  `a{}</style><script>alert(1)</script><style>`,
			want: `<svg xmlns="http://www.w3.org/2000/svg">` + style + `a{}&lt;/style&gt;&lt;script&gt;alert(1)&lt;/script&gt;&lt;style&gt;</style></svg>`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := injectStyle([]byte(tc.doc), tc.css)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("injectStyle:\n got %s\nwant %s", got, tc.want)
			}
		})
	}
	if _, err := injectStyle([]byte("<!-- nothing -->"), "a{}"); err == nil {
		t.Error("a document without a root element was accepted")
	}
}

// TestInjectedStyleSanitised: the injected CSS goes through the SVG
// sanitiser like a document's own <style>: external references never reach
// librsvg.
func TestInjectedStyleSanitised(t *testing.T) {
	t.Parallel()
	doc := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="4" height="4"><rect width="4" height="4"/></svg>`)
	for _, css := range []string{
		`@import "https://evil.example/a.css"; rect{fill:red}`,
		`rect{fill:url(https://evil.example/p.svg#x)}`,
		`rect{fill:\75 rl(https://evil.example/p.svg#x)}`,
		`rect{background:image-set("https://evil.example/a.png" 1x)}`,
	} {
		injected, err := injectStyle(doc, css)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := svg.Sanitize(&out, bytes.NewReader(injected), svg.Options{}); err != nil {
			t.Fatalf("sanitise %q: %v", css, err)
		}
		if strings.Contains(out.String(), "evil.example") {
			t.Errorf("css %q survived sanitising: %s", css, out.String())
		}
	}
}
