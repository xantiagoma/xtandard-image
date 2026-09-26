package svg

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// lines joins its arguments with newlines and adds a trailing one, so that
// expected documents (including whitespace-only lines left behind by removed
// elements) can be written one source line per output line.
func lines(l ...string) string {
	return strings.Join(l, "\n") + "\n"
}

func readTestdata(tb testing.TB, name string) []byte {
	tb.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

func sanitize(in []byte, opts Options) (string, error) {
	var out bytes.Buffer
	err := Sanitize(&out, bytes.NewReader(in), opts)
	return out.String(), err
}

func TestSanitizeMaliciousCorpus(t *testing.T) {
	t.Parallel()
	withData := Options{AllowDataImages: true}
	tests := []struct {
		name    string
		file    string
		opts    Options
		want    string
		wantErr error
	}{
		{
			name: "script",
			file: "script.svg",
			want: lines(
				`<svg xmlns="http://www.w3.org/2000/svg" xmlns:h="http://www.w3.org/1999/xhtml" width="10" height="10">`,
				`  `,
				`  <rect width="10" height="10"/>`,
				`  `,
				`  `,
				`  `,
				`  `,
				`  `,
				`</svg>`,
			),
		},
		{
			name: "event handlers",
			file: "event-handlers.svg",
			want: lines(
				`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10">`,
				`  <rect fill="red" width="5" height="5"/>`,
				`  <g xmlns:x="urn:x"><circle r="1"></circle></g>`,
				`</svg>`,
			),
		},
		{
			name: "foreignObject and embedding elements",
			file: "foreignobject.svg",
			want: lines(
				`<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100">`,
				`  `,
				`  `,
				`  `,
				`  `,
				`  <text x="0" y="10">kept</text>`,
				`</svg>`,
			),
		},
		{
			name: "external href",
			file: "external-href.svg",
			want: lines(
				`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink">`,
				`  <defs><linearGradient id="g"><stop offset="0" stop-color="red"/></linearGradient></defs>`,
				`  <a><text>click</text></a>`,
				`  <a><text>click</text></a>`,
				`  <image width="1" height="1"/>`,
				`  <image width="1" height="1"/>`,
				`  <image width="1" height="1"/>`,
				`  <image width="1" height="1"/>`,
				`  <image width="1" height="1"/>`,
				`  <feImage xlink:href="#g"/>`,
				`  <rect fill="url(#g)" width="1" height="1"/>`,
				`</svg>`,
			),
		},
		{
			name: "external href with data images allowed",
			file: "external-href.svg",
			opts: withData,
			want: lines(
				`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink">`,
				`  <defs><linearGradient id="g"><stop offset="0" stop-color="red"/></linearGradient></defs>`,
				`  <a><text>click</text></a>`,
				`  <a><text>click</text></a>`,
				`  <image width="1" height="1"/>`,
				`  <image width="1" height="1"/>`,
				`  <image width="1" height="1"/>`,
				`  <image href="data:image/png;base64,iVBORw0KGgo=" width="1" height="1"/>`,
				`  <image width="1" height="1"/>`,
				`  <feImage xlink:href="#g"/>`,
				`  <rect fill="url(#g)" width="1" height="1"/>`,
				`</svg>`,
			),
		},
		{
			name: "use with external references",
			file: "use-external.svg",
			opts: withData, // data: is refused on <use> even when allowed elsewhere
			want: lines(
				`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink">`,
				`  <symbol id="icon"><circle r="1"/></symbol>`,
				`  `,
				`  `,
				`  `,
				`  <use href="#icon" x="2"/>`,
				`</svg>`,
			),
		},
		{
			name: "css @import",
			file: "css-import.svg",
			want: lines(
				`<svg xmlns="http://www.w3.org/2000/svg">`,
				`  <style>  rect{fill:red}</style>`,
				`  <style> </style>`,
				`  <style> </style>`,
				`  <style></style>`,
				`  <rect width="1" height="1"/>`,
				`</svg>`,
			),
		},
		{
			name: "css url()",
			file: "css-url.svg",
			want: lines(
				`<svg xmlns="http://www.w3.org/2000/svg">`,
				`  <style>`,
				`    rect { fill: none; stroke: URL( "#ok" ) }`,
				`    circle { background: none }`,
				`    @font-face { font-family: f; src: none format("woff2") }`,
				`    path { mask: none }`,
				`  </style>`,
				`  <rect style="filter:none;fill:blue" width="1" height="1"/>`,
				`  <circle fill="none" stroke="url(#ok)" r="1"/>`,
				`  <path d="M0 0h1"/>`,
				`  <path clip-path="none" d="M0 0h1"/>`,
				`</svg>`,
			),
		},
		{
			name: "css url() with data images allowed",
			file: "css-url.svg",
			opts: withData,
			want: lines(
				`<svg xmlns="http://www.w3.org/2000/svg">`,
				`  <style>`,
				`    rect { fill: none; stroke: URL( "#ok" ) }`,
				`    circle { background: none }`,
				`    @font-face { font-family: f; src: none format("woff2") }`,
				`    path { mask: url(data:image/png;base64,AAAA) }`,
				`  </style>`,
				`  <rect style="filter:none;fill:blue" width="1" height="1"/>`,
				`  <circle fill="none" stroke="url(#ok)" r="1"/>`,
				`  <path d="M0 0h1"/>`,
				`  <path clip-path="none" d="M0 0h1"/>`,
				`</svg>`,
			),
		},
		{
			name: "animations targeting href and handlers",
			file: "animate-href.svg",
			want: lines(
				`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink">`,
				`  <a href="#x">`,
				`    `,
				`    `,
				`    `,
				`    <set attributeName="fill" to="none"/>`,
				`    <animate attributeName="opacity" from="0" to="1" dur="1s"/>`,
				`    <text>x</text>`,
				`  </a>`,
				`</svg>`,
			),
		},
		{
			name: "processing instructions",
			file: "processing-instructions.svg",
			want: lines(
				`<?xml version="1.0" encoding="UTF-8" standalone="no"?>`,
				``,
				`<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`,
			),
		},
		{name: "XXE", file: "xxe.svg", wantErr: ErrDTD},
		{name: "billion laughs", file: "billion-laughs.svg", wantErr: ErrDTD},
		{name: "undeclared entity", file: "undeclared-entity.svg", wantErr: ErrMalformed},
		{name: "deep nesting", file: "deep-nesting.svg", wantErr: ErrTooDeep},
		{name: "huge element count", file: "many-elements.svg", opts: Options{MaxElements: 1000}, wantErr: ErrTooManyElements},
		{name: "byte limit", file: "script.svg", opts: Options{MaxBytes: 100}, wantErr: ErrTooLarge},
		{name: "not svg", file: "not-svg.svg", wantErr: ErrNotSVG},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := sanitize(readTestdata(t, filepath.Join("malicious", tc.file)), tc.opts)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("output mismatch\n got: %q\nwant: %q", got, tc.want)
			}
			assertSafe(t, []byte(got), tc.opts.AllowDataImages)
		})
	}
}

// TestSanitizeMaliciousCorpusNoLeaks is a coarse net over the whole corpus:
// whatever the per-file expectations say, no output may contain an external
// URL or active content marker.
func TestSanitizeMaliciousCorpusNoLeaks(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob(filepath.Join("testdata", "malicious", "*.svg"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			t.Parallel()
			in, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, allow := range []bool{false, true} {
				got, err := sanitize(in, Options{AllowDataImages: allow, MaxElements: 1000})
				if err != nil {
					continue
				}
				lower := strings.ToLower(got)
				for _, bad := range []string{"evil.example", "javascript:", "alert(", "<script", "foreignobject", "@import", "data:image/svg"} {
					if strings.Contains(lower, bad) {
						t.Errorf("allow=%v: output contains %q:\n%s", allow, bad, got)
					}
				}
			}
		})
	}
}

func TestSanitizeBenignCorpusRoundTrips(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob(filepath.Join("testdata", "benign", "*.svg"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("empty benign corpus")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			t.Parallel()
			in, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			got, err := sanitize(in, Options{AllowDataImages: true})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if want, gotC := canonical(t, in), canonical(t, []byte(got)); gotC != want {
				t.Errorf("canonical XML differs\n got: %s\nwant: %s", gotC, want)
			}
			// Nothing needed rewriting, so every token is copied verbatim.
			if got != string(in) {
				t.Errorf("output is not byte-identical\n got: %q\nwant: %q", got, in)
			}
		})
	}
}

func TestSanitizeInline(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		opts    Options
		want    string
		wantErr error
	}{
		{name: "minimal", in: `<svg/>`, want: `<svg/>`},
		{name: "prefixed root", in: `<s:svg xmlns:s="http://www.w3.org/2000/svg"/>`, want: `<s:svg xmlns:s="http://www.w3.org/2000/svg"/>`},
		{name: "BOM kept", in: "\uFEFF<svg/>", want: "\uFEFF<svg/>"},
		{name: "BOM before declaration", in: "\uFEFF<?xml version=\"1.0\"?>\n<svg/>", want: "\uFEFF<?xml version=\"1.0\"?>\n<svg/>"},
		{name: "declaration not first is dropped", in: "<!-- c --><?xml version=\"1.0\"?><svg/>", want: "<!-- c --><svg/>"},
		{name: "comments and whitespace after root", in: "<svg/>\n<!-- end -->\n", want: "<svg/>\n<!-- end -->\n"},
		{name: "cdata preserved", in: `<svg><text><![CDATA[a<b]]> &amp; c</text></svg>`, want: `<svg><text><![CDATA[a<b]]> &amp; c</text></svg>`},
		{name: "entity references preserved", in: `<svg><text a="&quot;x&#10;">&#x3C;&lt;</text></svg>`, want: `<svg><text a="&quot;x&#10;">&#x3C;&lt;</text></svg>`},
		{
			name: "rewritten tag re-escapes kept attributes",
			in:   `<svg onload="x" title='a "b" &amp; &lt;c&gt;&#9;&#10;&#13;'/>`,
			want: `<svg title="a &quot;b&quot; &amp; &lt;c&gt;&#x9;&#xA;&#xD;"/>`,
		},
		{name: "rewritten tag keeps open form", in: `<svg onload="x"><g/></svg>`, want: `<svg><g/></svg>`},
		{name: "namespace declaration starting with on kept", in: `<svg xmlns:onx="urn:x" onx:a="1"/>`, want: `<svg xmlns:onx="urn:x" onx:a="1"/>`},
		{name: "xml:base removed", in: `<svg xml:base="https://evil.example/"><use href="#a"/></svg>`, want: `<svg><use href="#a"/></svg>`},
		{name: "empty href removed", in: `<svg><a href=""/></svg>`, want: `<svg><a/></svg>`},
		{name: "fragment href with spaces kept", in: `<svg><a href=" #a "/></svg>`, want: `<svg><a href=" #a "/></svg>`},
		{name: "fragment after space and tab kept", in: `<svg><a href=" &#9;#a"/></svg>`, want: `<svg><a href=" &#9;#a"/></svg>`},
		{name: "fragment after unicode space is relative", in: `<svg><use href="&#xA0;#a"/><a href="&#xA0;#a"/></svg>`, want: `<svg><a/></svg>`},
		{
			name: "data svg split by a tab refused",
			in:   `<svg><image href="data:image/sv&#9;g+xml,x"/><image href="data:image/pn&#9;g,x"/></svg>`,
			opts: Options{AllowDataImages: true},
			want: `<svg><image/><image href="data:image/pn&#9;g,x"/></svg>`,
		},
		{name: "use without href kept", in: `<svg><use x="1"/></svg>`, want: `<svg><use x="1"/></svg>`},
		{name: "self-closing style", in: `<svg><style/></svg>`, want: `<svg><style/></svg>`},
		{name: "elements inside style dropped", in: `<svg><style>a{}<b>x</b>c{}</style></svg>`, want: `<svg><style>a{}c{}</style></svg>`},
		{name: "style with comment kept verbatim", in: `<svg><style>a{<!-- c -->}</style></svg>`, want: `<svg><style>a{<!-- c -->}</style></svg>`},
		{name: "rewritten style is escaped", in: `<svg><style>a>b{fill:url(//x)}</style></svg>`, want: `<svg><style>a&gt;b{fill:none}</style></svg>`},
		{name: "escaped css without references kept", in: `<svg><style>.\31 a{fill:red}</style></svg>`, want: `<svg><style>.\31 a{fill:red}</style></svg>`},
		{name: "nested dropped subtrees", in: `<svg><script><script/></script><g/></svg>`, want: `<svg><g/></svg>`},
		{name: "dropped root children keep depth", in: `<svg><foreignObject><g><g/></g></foreignObject><g/></svg>`, want: `<svg><g/></svg>`},
		{name: "depth at limit", in: `<svg><g><g/></g></svg>`, opts: Options{MaxDepth: 3}, want: `<svg><g><g/></g></svg>`},
		{name: "depth over limit", in: `<svg><g><g><g/></g></g></svg>`, opts: Options{MaxDepth: 3}, wantErr: ErrTooDeep},
		{name: "elements at limit", in: `<svg><g/><g/></svg>`, opts: Options{MaxElements: 3}, want: `<svg><g/><g/></svg>`},
		{name: "removed elements count", in: `<svg><script/><script/><script/></svg>`, opts: Options{MaxElements: 3}, wantErr: ErrTooManyElements},
		{name: "bytes at limit", in: `<svg/>`, opts: Options{MaxBytes: 6}, want: `<svg/>`},
		{name: "bytes over limit", in: `<svg/> `, opts: Options{MaxBytes: 6}, wantErr: ErrTooLarge},
		{name: "empty input", in: ``, wantErr: ErrNotSVG},
		{name: "whitespace only", in: " \n", wantErr: ErrNotSVG},
		{name: "text before root", in: `x<svg/>`, wantErr: ErrMalformed},
		{name: "text after root", in: `<svg/>x`, wantErr: ErrMalformed},
		{name: "two roots", in: `<svg/><svg/>`, wantErr: ErrMalformed},
		{name: "mismatched end", in: `<svg><g></a></svg>`, wantErr: ErrMalformed},
		{name: "mismatched prefix", in: `<a:svg></b:svg>`, wantErr: ErrMalformed},
		{name: "unclosed", in: `<svg><g>`, wantErr: ErrMalformed},
		{name: "unclosed inside dropped subtree", in: `<svg><script>`, wantErr: ErrMalformed},
		{name: "bare directive", in: `<svg><!ENTITY a "b"></svg>`, wantErr: ErrDTD},
		{name: "doctype without subset", in: `<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd"><svg/>`, wantErr: ErrDTD},
		{name: "non utf-8 encoding", in: `<?xml version="1.0" encoding="ISO-8859-1"?><svg/>`, wantErr: ErrMalformed},
		{name: "invalid utf-8", in: "<svg>\xff</svg>", wantErr: ErrMalformed},
		{name: "html root", in: `<html/>`, wantErr: ErrNotSVG},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := sanitize([]byte(tc.in), tc.opts)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("output mismatch\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// TestSanitizeDefaultLimits streams documents past the default limits without
// materialising them.
func TestSanitizeDefaultLimits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		src     io.Reader
		wantErr error
	}{
		{
			name:    "elements",
			src:     io.MultiReader(strings.NewReader("<svg>"), repeatReader("<g/>", DefaultMaxElements)),
			wantErr: ErrTooManyElements,
		},
		{
			name:    "depth",
			src:     repeatReader("<svg>", DefaultMaxDepth+1),
			wantErr: ErrTooDeep,
		},
		{
			name:    "bytes",
			src:     io.MultiReader(strings.NewReader("<svg><text>"), repeatReader("a", DefaultMaxBytes)),
			wantErr: ErrTooLarge,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := Sanitize(io.Discard, tc.src, Options{})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func repeatReader(s string, n int) io.Reader {
	return io.LimitReader(&cycleReader{s: s}, int64(len(s)*n))
}

type cycleReader struct {
	s   string
	off int
}

func (r *cycleReader) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		c := copy(p[n:], r.s[r.off:])
		n += c
		r.off = (r.off + c) % len(r.s)
	}
	return n, nil
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

type errWriter struct{ err error }

func (w errWriter) Write([]byte) (int, error) { return 0, w.err }

func TestSanitizeIOErrors(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	t.Run("read", func(t *testing.T) {
		t.Parallel()
		err := Sanitize(io.Discard, io.MultiReader(strings.NewReader("<svg>"), errReader{boom}), Options{})
		if !errors.Is(err, boom) || errors.Is(err, ErrMalformed) {
			t.Fatalf("err = %v, want wrapped %v", err, boom)
		}
	})
	t.Run("write", func(t *testing.T) {
		t.Parallel()
		err := Sanitize(errWriter{boom}, strings.NewReader("<svg/>"), Options{})
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want wrapped %v", err, boom)
		}
	})
}

// TestSanitizeLargeTokens exercises window growth: a single token larger
// than the reader's initial buffer, fed in small reads.
func TestSanitizeLargeTokens(t *testing.T) {
	t.Parallel()
	text := strings.Repeat("0123456789", 10_000)
	in := `<svg><text>` + text + `</text><path d="` + text + `" onclick="x"/></svg>`
	want := `<svg><text>` + text + `</text><path d="` + text + `"/></svg>`
	var out bytes.Buffer
	if err := Sanitize(&out, iotestHalfReader(strings.NewReader(in)), Options{}); err != nil {
		t.Fatal(err)
	}
	if out.String() != want {
		t.Errorf("output mismatch (len %d, want %d)", out.Len(), len(want))
	}
}

// iotestHalfReader returns at most 7 bytes per Read, like a slow network.
func iotestHalfReader(r io.Reader) io.Reader { return &smallReader{r: r} }

type smallReader struct{ r io.Reader }

func (s *smallReader) Read(p []byte) (int, error) {
	return s.r.Read(p[:min(len(p), 7)])
}

// canonical renders a document as a namespace-resolved token list: elements
// with sorted attributes and merged character data; comments, processing
// instructions and whitespace outside the root are ignored. Two documents
// with equal canonical forms render identically.
func canonical(t *testing.T, doc []byte) string {
	t.Helper()
	d := xml.NewDecoder(bytes.NewReader(doc))
	d.Strict = true
	var b strings.Builder
	var text strings.Builder
	depth := 0
	flush := func() {
		if text.Len() > 0 {
			b.WriteString("T" + strings.ReplaceAll(text.String(), "\r\n", "\n") + "\n")
			text.Reset()
		}
	}
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("canonical: %v", err)
		}
		switch tt := tok.(type) {
		case xml.StartElement:
			flush()
			depth++
			attrs := make([]string, 0, len(tt.Attr))
			for _, a := range tt.Attr {
				attrs = append(attrs, a.Name.Space+" "+a.Name.Local+"="+a.Value)
			}
			slices.Sort(attrs)
			b.WriteString("S{" + tt.Name.Space + "}" + tt.Name.Local + " [" + strings.Join(attrs, "|") + "]\n")
		case xml.EndElement:
			flush()
			depth--
			b.WriteString("E\n")
		case xml.CharData:
			if depth > 0 {
				text.Write(tt)
			}
		}
	}
	flush()
	return b.String()
}

// assertSafe re-parses a sanitised document and checks the invariants the
// sanitiser promises, independently of how it got there.
func assertSafe(t *testing.T, doc []byte, allowData bool) {
	t.Helper()
	d := xml.NewDecoder(bytes.NewReader(doc))
	d.Strict = true
	first := true
	for {
		tok, err := d.RawToken()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatalf("sanitised output does not parse: %v\n%s", err, doc)
		}
		switch tt := tok.(type) {
		case xml.StartElement:
			if dropElement(tt) {
				t.Errorf("forbidden element %q survived", tt.Name.Local)
			}
			for _, a := range tt.Attr {
				v, keep := sanitizeAttr(a, allowData)
				if !keep || v != a.Value {
					t.Errorf("unsafe attribute %s:%s=%q survived", a.Name.Space, a.Name.Local, a.Value)
				}
			}
		case xml.ProcInst:
			if tt.Target != "xml" || !first {
				t.Errorf("processing instruction %q survived", tt.Target)
			}
		case xml.Directive:
			t.Errorf("directive survived: %q", tt)
		case xml.CharData:
			if !first || !bytes.Equal(tt, []byte(utf8BOM)) {
				first = false
			}
			continue
		}
		first = false
	}
}
