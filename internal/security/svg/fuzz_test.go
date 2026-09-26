package svg

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// FuzzSanitize checks, for arbitrary input, that Sanitize never panics and
// that whatever it accepts comes out well-formed, free of anything the policy
// forbids (assertSafe re-checks every token independently) and stable: a
// second pass over the output changes nothing.
func FuzzSanitize(f *testing.F) {
	for _, dir := range []string{"benign", "malicious"} {
		files, err := filepath.Glob(filepath.Join("testdata", dir, "*.svg"))
		if err != nil {
			f.Fatal(err)
		}
		for _, name := range files {
			b, err := os.ReadFile(name)
			if err != nil {
				f.Fatal(err)
			}
			f.Add(b, false)
		}
	}
	for _, s := range []string{
		`<svg><style>@im<!---->port "x";a{fill:url(//x)}</style></svg>`,
		`<svg><a href="#a" xlink:href="data:image/png,x" style="fill:\75rl(x)"/></svg>`,
		`<svg><use href="#a"/><set attributeName="href" to="x"/></svg>`,
		utf8BOM + `<?xml version="1.0"?><svg><![CDATA[x]]></svg>`,
	} {
		f.Add([]byte(s), true)
	}

	opts := func(allowData bool) Options {
		return Options{AllowDataImages: allowData, MaxBytes: 1 << 20, MaxDepth: 64, MaxElements: 10_000}
	}
	f.Fuzz(func(t *testing.T, in []byte, allowData bool) {
		var out bytes.Buffer
		if err := Sanitize(&out, bytes.NewReader(in), opts(allowData)); err != nil {
			return
		}
		assertSafe(t, out.Bytes(), allowData)
		var again bytes.Buffer
		if err := Sanitize(&again, bytes.NewReader(out.Bytes()), opts(allowData)); err != nil {
			t.Fatalf("sanitised output rejected: %v\n in: %q\nout: %q", err, in, out.Bytes())
		}
		if !bytes.Equal(again.Bytes(), out.Bytes()) {
			t.Fatalf("not idempotent\n in: %q\nout: %q\nagain: %q", in, out.Bytes(), again.Bytes())
		}
	})
}
