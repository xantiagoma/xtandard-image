package filters_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect/thumbor/filters"
)

func TestCut(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, in   string
		list, rest string
		ok         bool
	}{
		{"simple", "filters:quality(80)/a.jpg", "quality(80)", "a.jpg", true},
		{"chain", "filters:format(webp):quality(80)/s3/a.jpg", "format(webp):quality(80)", "s3/a.jpg", true},
		{"trailing (storyblok)", "filters:format(webp):focal(150x100:250x200)", "format(webp):focal(150x100:250x200)", "", true},
		{"slash inside args", "filters:watermark(http://a.com/w.png,0,0,0)/img.jpg", "watermark(http://a.com/w.png,0,0,0)", "img.jpg", true},
		{"nested thumbor path", "filters:watermark(/unsafe/filters:quality(5)/w.png,0,0,0)/img.jpg", "watermark(/unsafe/filters:quality(5)/w.png,0,0,0)", "img.jpg", true},
		{"quoted paren", "filters:watermark('w).png',0,0,0)/img.jpg", "watermark('w).png',0,0,0)", "img.jpg", true},
		{"empty args", "filters:grayscale()/a.jpg", "grayscale()", "a.jpg", true},
		{"unbalanced falls back to lazy", "filters:a(b))/x", "a(b))", "x", true},
		{"unterminated", "filters:quality(80/a.jpg", "", "", false},
		{"no parens", "filters:grayscale/a.jpg", "", "", false},
		{"no prefix", "quality(80)/a.jpg", "", "", false},
		{"text after close", "filters:a(b)c/d", "", "", false},
		{"empty", "filters:", "", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			list, rest, ok := filters.Cut(tc.in)
			if list != tc.list || rest != tc.rest || ok != tc.ok {
				t.Fatalf("Cut(%q) = %q, %q, %v; want %q, %q, %v", tc.in, list, rest, ok, tc.list, tc.rest, tc.ok)
			}
		})
	}
}

func TestParse(t *testing.T) {
	t.Parallel()
	type F = filters.Filter
	tests := []struct {
		name, in string
		want     []F
		err      error
	}{
		{"one", "quality(80)", []F{{"quality", "80"}}, nil},
		{"empty args", "grayscale()", []F{{"grayscale", ""}}, nil},
		{"chain", "brightness(10):contrast(30)", []F{{"brightness", "10"}, {"contrast", "30"}}, nil},
		{"colon in args", "focal(146x206:279x360):quality(5)", []F{{"focal", "146x206:279x360"}, {"quality", "5"}}, nil},
		{"nested", "watermark(/unsafe/filters:q(5)/w.png,0,0,0)", []F{{"watermark", "/unsafe/filters:q(5)/w.png,0,0,0"}}, nil},
		{"quoted", "watermark('a),b.png',1,2,3)", []F{{"watermark", "'a),b.png',1,2,3"}}, nil},
		{"unterminated quote", "a('b)", []F{{"a", "'b"}}, nil},
		{"semicolons", "convolution(1;2;1;2;4;2;1;2;1,3,true)", []F{{"convolution", "1;2;1;2;4;2;1;2;1,3,true"}}, nil},
		{"empty", "", nil, filters.ErrSyntax},
		{"no parens", "grayscale", nil, filters.ErrSyntax},
		{"no name", "(1)", nil, filters.ErrSyntax},
		{"bad name", "a-b(1)", nil, filters.ErrSyntax},
		{"trailing colon", "a(1):", nil, filters.ErrSyntax},
		{"double colon", "a(1)::b(2)", nil, filters.ErrSyntax},
		{"junk after", "a(1)b", nil, filters.ErrSyntax},
		{"unbalanced", "a(b(c)", nil, filters.ErrSyntax},
		{"too many", strings.Repeat("a():", filters.MaxFilters) + "a()", nil, filters.ErrTooMany},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := filters.Parse(tc.in)
			if !errors.Is(err, tc.err) {
				t.Fatalf("Parse(%q) err = %v, want %v", tc.in, err, tc.err)
			}
			if d := cmp.Diff(tc.want, got); d != "" {
				t.Fatalf("Parse(%q) (-want +got):\n%s", tc.in, d)
			}
		})
	}
}

func TestArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"  ", nil},
		{"80", []string{"80"}},
		{"4, 2", []string{"4", "2"}},
		{"a,", []string{"a", ""}},
		{" 'x,y' , 1", []string{"x,y", "1"}},
		{"'it''s'", []string{"it''s"}},
		{"url(a,b),1", []string{"url(a,b)", "1"}},
		{"http://a.com/w.png,-10p,center,50,20,none", []string{"http://a.com/w.png", "-10p", "center", "50", "20", "none"}},
		{"146x206:279x360", []string{"146x206:279x360"}},
		{"a,'b", []string{"a", "'b"}},
		{"x'y,z", []string{"x'y", "z"}},
	}
	for _, tc := range tests {
		if d := cmp.Diff(tc.want, filters.Args(tc.in)); d != "" {
			t.Errorf("Args(%q) (-want +got):\n%s", tc.in, d)
		}
	}
}

func TestParseColor(t *testing.T) {
	t.Parallel()
	type C = filters.Color
	tests := []struct {
		in   string
		want C
		ok   bool
	}{
		{"blue", C{0, 0, 255, 255}, true},
		{"Magenta", C{255, 0, 255, 255}, true},
		{"lightblue", C{173, 216, 230, 255}, true},
		{"rebeccapurple", C{102, 51, 153, 255}, true},
		{"transparent", C{}, true},
		{"f00", C{255, 0, 0, 255}, true},
		{"f0f", C{255, 0, 255, 255}, true},
		{"#add8e6", C{173, 216, 230, 255}, true},
		{"ADD8E6", C{173, 216, 230, 255}, true},
		{"ff00ff80", C{255, 0, 255, 128}, true},
		{"f008", C{255, 0, 0, 136}, true},
		{"ff00f", C{}, false},
		{"xyz", C{}, false},
		{"", C{}, false},
		{"#", C{}, false},
		{"ff00ff00ff", C{}, false},
		{strings.Repeat("a", 40), C{}, false},
	}
	for _, tc := range tests {
		got, ok := filters.ParseColor(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ParseColor(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// TestStoryblok parses the filter strings of unpic's Storyblok provider
// fixtures (src/providers/storyblok.test.ts, MIT), which reuse thumbor's
// filter grammar as a trailing path segment.
func TestStoryblok(t *testing.T) {
	t.Parallel()
	type F = filters.Filter
	tests := []struct {
		segment string
		want    []F
	}{
		{"filters:format(webp):focal(150x100:250x200)", []F{{"format", "webp"}, {"focal", "150x100:250x200"}}},
		{"filters:rotate(90):format(png)", []F{{"rotate", "90"}, {"format", "png"}}},
		{"filters:format(webp)", []F{{"format", "webp"}}},
		{"filters:focal(150x150:250x250)", []F{{"focal", "150x150:250x250"}}},
		{"filters:focal(150x150:250x250):format(webp)", []F{{"focal", "150x150:250x250"}, {"format", "webp"}}},
		{"filters:format(png)", []F{{"format", "png"}}},
		{"filters:format(jpg)", []F{{"format", "jpg"}}},
	}
	for _, tc := range tests {
		list, rest, ok := filters.Cut(tc.segment)
		if !ok || rest != "" {
			t.Errorf("Cut(%q) = %q, %q, %v", tc.segment, list, rest, ok)
			continue
		}
		got, err := filters.Parse(list)
		if err != nil {
			t.Errorf("Parse(%q): %v", list, err)
			continue
		}
		if d := cmp.Diff(tc.want, got); d != "" {
			t.Errorf("%q (-want +got):\n%s", tc.segment, d)
		}
	}
}

// FuzzFilters checks Cut, Parse and Args on arbitrary input: no panics,
// Cut's list and rest are substrings in order, Parse's output round-trips
// and every filter name is a valid identifier.
func FuzzFilters(f *testing.F) {
	for _, s := range []string{
		"filters:quality(80)/a.jpg",
		"filters:format(webp):quality(80):focal(1x2:3x4)/s3/a.jpg",
		"filters:watermark(/unsafe/filters:quality(5)/w.png,0,0,0)/img.jpg",
		"filters:watermark('w).png',-10p,center,50,20,none)/x",
		"filters:round_corner(20|40,0,0,0,1)",
		"filters:a(b))/x",
		"filters:((((",
		"filters:'''",
		"filters:",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		list, rest, ok := filters.Cut(s)
		if ok {
			if !strings.HasPrefix(s, filters.Prefix+list) || !strings.HasSuffix(s, rest) {
				t.Fatalf("Cut(%q) = %q, %q", s, list, rest)
			}
			if list == "" || list[len(list)-1] != ')' {
				t.Fatalf("Cut(%q) list %q does not end in )", s, list)
			}
		}
		body := strings.TrimPrefix(s, filters.Prefix)
		fs, err := filters.Parse(body)
		if err != nil {
			if !errors.Is(err, filters.ErrSyntax) && !errors.Is(err, filters.ErrTooMany) {
				t.Fatalf("Parse(%q): unexpected error %v", body, err)
			}
		} else {
			parts := make([]string, len(fs))
			for i, fl := range fs {
				if fl.Name == "" {
					t.Fatalf("Parse(%q): empty name", body)
				}
				parts[i] = fl.Name + "(" + fl.Args + ")"
				_ = filters.Args(fl.Args)
			}
			if got := strings.Join(parts, ":"); got != body {
				t.Fatalf("Parse(%q) round-trip = %q", body, got)
			}
		}
		_ = filters.Args(s)
		_, _ = filters.ParseColor(s)
	})
}

func BenchmarkParse(b *testing.B) {
	const s = "filters:format(webp):quality(80):focal(146x206:279x360):watermark(https://example.com/w.png,-10,-10,50,20,none)/s3/a.jpg"
	b.ReportAllocs()
	for b.Loop() {
		list, _, _ := filters.Cut(s)
		fs, _ := filters.Parse(list)
		for _, f := range fs {
			_ = filters.Args(f.Args)
		}
	}
}
