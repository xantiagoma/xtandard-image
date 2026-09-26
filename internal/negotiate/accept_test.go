package negotiate

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseAccept(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want AcceptList
	}{
		{"empty", "", nil},
		{"only_commas", " , ,, ", nil},
		{
			"chrome", acceptChrome,
			AcceptList{
				{Type: "image", Subtype: "avif", Q: 1000},
				{Type: "image", Subtype: "webp", Q: 1000},
				{Type: "image", Subtype: "apng", Q: 1000},
				{Type: "image", Subtype: "svg+xml", Q: 1000},
				{Type: "image", Subtype: "*", Q: 1000},
				{Type: "*", Subtype: "*", Q: 800},
			},
		},
		{"case_folded", "Image/WebP;Q=0.5", AcceptList{{Type: "image", Subtype: "webp", Q: 500}}},
		{"ows", " image/png \t; q=0.25 ", AcceptList{{Type: "image", Subtype: "png", Q: 250}}},
		{"q_forms", "a/b;q=1.000,c/d;q=0,e/f;q=0.,g/h;q=.5", AcceptList{
			{Type: "a", Subtype: "b", Q: 1000}, {Type: "c", Subtype: "d", Q: 0}, {Type: "e", Subtype: "f", Q: 0},
		}},
		{"q_invalid_dropped", "a/b;q=1.001,c/d;q=2,e/f;q=0.1234,g/h;q=x,i/j", AcceptList{{Type: "i", Subtype: "j", Q: 1000}}},
		{
			"params_before_q_kept_after_ignored", `text/plain;format=flowed;q=0.4;ext=1, image/x;a="b,c";q=0.3`,
			AcceptList{
				{Type: "text", Subtype: "plain", Params: []Param{{"format", "flowed"}}, Q: 400},
				{Type: "image", Subtype: "x", Params: []Param{{"a", "b,c"}}, Q: 300},
			},
		},
		{"quoted_escape", `a/b;x="q\"w\\e"`, AcceptList{{Type: "a", Subtype: "b", Params: []Param{{"x", `q"w\e`}}, Q: 1000}}},
		{"trailing_semicolon", "image/webp;", AcceptList{{Type: "image", Subtype: "webp", Q: 1000}}},
		{
			"malformed_elements_skipped", "image, /png, image/, */png, image/png junk, a/b;=c, a/b;c, image/gif, a/b;c=\"open, x/y",
			AcceptList{{Type: "image", Subtype: "gif", Q: 1000}},
		},
		{"wildcard_type", "image/*;q=0.3", AcceptList{{Type: "image", Subtype: "*", Q: 300}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, ParseAccept(tt.in)); diff != "" {
				t.Errorf("ParseAccept(%q) (-want +got):\n%s", tt.in, diff)
			}
		})
	}
}

func TestParseAcceptCap(t *testing.T) {
	t.Parallel()
	in := strings.Repeat("a/b,", MaxAcceptRanges*4)
	if got := len(ParseAccept(in)); got != MaxAcceptRanges {
		t.Errorf("len = %d, want %d", got, MaxAcceptRanges)
	}
}

func TestParseAcceptHeader(t *testing.T) {
	t.Parallel()
	h := http.Header{}
	if got := ParseAcceptHeader(h); got != nil {
		t.Errorf("absent header = %v, want nil", got)
	}
	h.Add("Accept", "image/avif")
	h.Add("Accept", "image/webp;q=0.5")
	want := AcceptList{{Type: "image", Subtype: "avif", Q: 1000}, {Type: "image", Subtype: "webp", Q: 500}}
	if diff := cmp.Diff(want, ParseAcceptHeader(h)); diff != "" {
		t.Errorf("multi-line header (-want +got):\n%s", diff)
	}
}

func TestAcceptQuality(t *testing.T) {
	t.Parallel()
	l := ParseAccept("image/webp;q=0.9, image/*;q=0.5, */*;q=0.1, image/png;q=0, image/jxl;v=1")
	tests := []struct {
		typ, sub      string
		quality, expl int
	}{
		{"image", "webp", 900, 900},
		{"image", "gif", 500, 0},
		{"image", "png", 0, 0},
		{"text", "html", 100, 0},
		{"image", "jxl", 500, 0}, // the parameterised range does not match
	}
	for _, tt := range tests {
		if got := l.Quality(tt.typ, tt.sub); got != tt.quality {
			t.Errorf("Quality(%s/%s) = %d, want %d", tt.typ, tt.sub, got, tt.quality)
		}
		if got := l.Explicit(tt.typ, tt.sub); got != tt.expl {
			t.Errorf("Explicit(%s/%s) = %d, want %d", tt.typ, tt.sub, got, tt.expl)
		}
	}
	if got := AcceptList(nil).Quality("image", "png"); got != 1000 {
		t.Errorf("empty list Quality = %d, want 1000", got)
	}
	if got := ParseAccept("text/html").Quality("image", "png"); got != 0 {
		t.Errorf("no match Quality = %d, want 0", got)
	}
	if got := ParseAccept("image/png;q=0.2,image/png").Quality("image", "png"); got != 200 {
		t.Errorf("first equally specific range must win, got %d", got)
	}
}

func TestMediaRangeString(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"image/webp":                  "image/webp",
		"IMAGE/PNG;Q=0.50":            "image/png;q=0.5",
		"a/b;q=0.001":                 "a/b;q=0.001",
		"a/b;q=0.000":                 "a/b;q=0",
		`a/b;x="y z";q=0.12`:          `a/b;x="y z";q=0.12`,
		`a/b;x="";y=tok`:              `a/b;x="";y=tok`,
		" a/b , c/d;q=0.9":            "a/b, c/d;q=0.9",
		`a/b;x="\"quoted\\"`:          `a/b;x="\"quoted\\"`,
		"a/b;q=1.0,*/*;q=0.8,image/*": "a/b, */*;q=0.8, image/*",
	}
	for in, want := range tests {
		if got := ParseAccept(in).String(); got != want {
			t.Errorf("ParseAccept(%q).String() = %q, want %q", in, got, want)
		}
	}
}

// FuzzParseAccept checks that parsing never panics, that every range is
// well-formed, that the canonical form round-trips, and that Quality stays in
// range.
func FuzzParseAccept(f *testing.F) {
	for _, s := range []string{
		"", acceptChrome, acceptWebP, acceptLegacy, "*/*", "image/*;q=0",
		`text/plain;format=flowed;q=0.4;ext=1`, `a/b;x="q\"w\\e",c/d`, "a/b;q=0.123", "\"", "a/b;x=\"\xff\"",
		"image/webp;", ",,,", "a/b;q=1.001",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		l := ParseAccept(s)
		if len(l) > MaxAcceptRanges {
			t.Fatalf("len %d > cap", len(l))
		}
		for _, r := range l {
			if r.Q < 0 || r.Q > 1000 {
				t.Fatalf("q %d out of range in %q", r.Q, s)
			}
			if !isToken(r.Type) || !isToken(r.Subtype) || r.Type != strings.ToLower(r.Type) ||
				r.Subtype != strings.ToLower(r.Subtype) || (r.Type == "*" && r.Subtype != "*") {
				t.Fatalf("bad range %+v from %q", r, s)
			}
			for _, p := range r.Params {
				if !isToken(p.Name) || p.Name == "q" {
					t.Fatalf("bad param %+v from %q", p, s)
				}
			}
		}
		canon := l.String()
		again := ParseAccept(canon)
		if len(l) == 0 {
			if len(again) != 0 {
				t.Fatalf("empty list re-parsed to %v", again)
			}
		} else if diff := cmp.Diff(l, again); diff != "" {
			t.Fatalf("round trip of %q via %q (-first +second):\n%s", s, canon, diff)
		}
		if again.String() != canon {
			t.Fatalf("canonical form not stable: %q vs %q", canon, again.String())
		}
		for _, mt := range [...]mediaType{{"image", "avif"}, {"image", "webp"}, {"image", "png"}, {"text", "html"}} {
			if q := l.Quality(mt.typ, mt.sub); q < 0 || q > 1000 {
				t.Fatalf("Quality %d out of range", q)
			}
			if e := l.Explicit(mt.typ, mt.sub); e < 0 || e > 1000 {
				t.Fatalf("Explicit %d out of range", e)
			}
		}
	})
}

func BenchmarkParseAccept(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if l := ParseAccept(acceptChrome); len(l) != 6 {
			b.Fatal(len(l))
		}
	}
}
