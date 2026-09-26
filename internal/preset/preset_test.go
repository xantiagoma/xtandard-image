package preset_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/preset"
)

func TestParseDefinitions(t *testing.T) {
	t.Parallel()
	s, err := preset.New(preset.Options{Definitions: map[string]string{
		"sharp":   "sharpen:0.7",
		"avatar":  " resizing_type:fill/width:100/height:100/gravity:sm ",
		"chain":   "width:300/height:300/-/width:200/-/pr:sharp",
		"nested":  "pr:avatar:sharp/q:80",
		"eq":      "wm:1:ce/wmu:aGk=",
		"default": "sm:1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]*preset.Preset{
		"sharp": {Name: "sharp", Pipelines: [][]preset.Option{{{Name: "sharpen", Args: []string{"0.7"}}}}},
		"avatar": {Name: "avatar", Pipelines: [][]preset.Option{{
			{Name: "resizing_type", Args: []string{"fill"}},
			{Name: "width", Args: []string{"100"}},
			{Name: "height", Args: []string{"100"}},
			{Name: "gravity", Args: []string{"sm"}},
		}}},
		"chain": {Name: "chain", Pipelines: [][]preset.Option{
			{{Name: "width", Args: []string{"300"}}, {Name: "height", Args: []string{"300"}}},
			{{Name: "width", Args: []string{"200"}}},
			{{Name: "pr", Args: []string{"sharp"}}},
		}},
		"nested": {Name: "nested", Pipelines: [][]preset.Option{{
			{Name: "pr", Args: []string{"avatar", "sharp"}},
			{Name: "q", Args: []string{"80"}},
		}}},
		"eq":      {Name: "eq", Pipelines: [][]preset.Option{{{Name: "wm", Args: []string{"1", "ce"}}, {Name: "wmu", Args: []string{"aGk="}}}}},
		"default": {Name: "default", Pipelines: [][]preset.Option{{{Name: "sm", Args: []string{"1"}}}}},
	}
	for name, w := range want {
		got, ok := s.Get(name)
		if !ok {
			t.Errorf("%s: missing", name)
			continue
		}
		if d := cmp.Diff(w, got); d != "" {
			t.Errorf("%s (-want +got):\n%s", name, d)
		}
	}
	if d := cmp.Diff([]string{"avatar", "chain", "default", "eq", "nested", "sharp"}, s.Names()); d != "" {
		t.Errorf("names (-want +got):\n%s", d)
	}
	if _, ok := s.Default(); !ok {
		t.Error("default preset not found")
	}
	if s.Only() || s.Len() != 6 || s.Separator() != ":" {
		t.Errorf("only=%v len=%d sep=%q", s.Only(), s.Len(), s.Separator())
	}
}

func TestNilStore(t *testing.T) {
	t.Parallel()
	var s *preset.Store
	if _, ok := s.Get("x"); ok {
		t.Error("nil store has x")
	}
	if _, ok := s.Default(); ok || s.Only() || s.Len() != 0 || s.Names() != nil || s.Separator() != ":" {
		t.Error("nil store is not empty")
	}
}

func TestSeparator(t *testing.T) {
	t.Parallel()
	s, err := preset.New(preset.Options{Separator: ",", Definitions: map[string]string{"a": "rs,fill,10,10/pr,b", "b": "q,50"}})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := s.Get("a")
	want := [][]preset.Option{{{Name: "rs", Args: []string{"fill", "10", "10"}}, {Name: "pr", Args: []string{"b"}}}}
	if d := cmp.Diff(want, a.Pipelines); d != "" {
		t.Errorf("(-want +got):\n%s", d)
	}
	if _, err := preset.New(preset.Options{Separator: "/"}); err == nil {
		t.Error("separator '/' accepted")
	}
}

func TestNewErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		defs map[string]string
		only bool
		want string // substring of the error
	}{
		{"empty name", map[string]string{"": "q:1"}, false, "empty preset name"},
		{"name with separator", map[string]string{"a:b": "q:1"}, false, "name must not contain"},
		{"name with slash", map[string]string{"a/b": "q:1"}, false, "name must not contain"},
		{"empty value", map[string]string{"a": "  "}, false, `preset "a": empty value`},
		{"not an option", map[string]string{"a": "q:1/blur"}, false, `"blur" is not an option`},
		{"empty option name", map[string]string{"a": ":1"}, false, "is not an option"},
		{"empty segment", map[string]string{"a": "q:1//w:2"}, false, "is not an option"},
		{"leading dash", map[string]string{"a": "-/q:1"}, false, "empty pipeline"},
		{"trailing dash", map[string]string{"a": "q:1/-"}, false, "empty pipeline"},
		{"double dash", map[string]string{"a": "q:1/-/-/q:2"}, false, "empty pipeline"},
		{"empty reference", map[string]string{"a": "pr:b:"}, false, "empty preset reference"},
		{"undefined reference", map[string]string{"a": "preset:nope"}, false, `references undefined preset "nope"`},
		{"self cycle", map[string]string{"a": "q:1/pr:a"}, false, "reference cycle a -> a"},
		{"cycle", map[string]string{"a": "pr:b", "b": "w:1/-/pr:c", "c": "pr:a"}, false, "reference cycle"},
		{"default cycle", map[string]string{"default": "pr:x", "x": "pr:default"}, false, "reference cycle"},
		{"too many pipelines", map[string]string{"a": strings.Repeat("q:1/-/", 16) + "q:1"}, false, "more than 16"},
		{"too many nested pipelines", map[string]string{
			"a": strings.Repeat("q:1/-/", 10) + "pr:b",
			"b": strings.Repeat("q:1/-/", 6) + "q:1",
		}, false, `preset "a": expands to 17 pipelines`},
		{"only without presets", nil, true, "at least one preset"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := preset.New(preset.Options{Definitions: tt.defs, Only: tt.only})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestMaxPipelinesAllowed(t *testing.T) {
	t.Parallel()
	_, err := preset.New(preset.Options{Definitions: map[string]string{
		"a": strings.Repeat("q:1/-/", 10) + "pr:b",
		"b": strings.Repeat("q:1/-/", 5) + "q:1",
	}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestParseFile(t *testing.T) {
	t.Parallel()
	in := "# presets\n\n  thumb = rs:fill:200:200/g:sm  \n#x=q:1\nwm=wmu:aGk=\r\nsharp=sh:0.7\n"
	got, err := preset.ParseFile(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"thumb": "rs:fill:200:200/g:sm", "wm": "wmu:aGk=", "sharp": "sh:0.7"}
	if d := cmp.Diff(want, got); d != "" {
		t.Errorf("(-want +got):\n%s", d)
	}

	for _, tt := range []struct{ in, want string }{
		{"thumb", "line 1: expected name=options"},
		{"\n=q:1", "line 2: empty preset name"},
		{"a=", `preset "a": line 1: empty value`},
		{"a=q:1\na=q:2", `preset "a": line 2: defined twice`},
		{"a=" + strings.Repeat("x", 70<<10), "reading presets file"},
	} {
		if _, err := preset.ParseFile(strings.NewReader(tt.in)); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%.20q: err = %v, want %q", tt.in, err, tt.want)
		}
	}
}

func TestFileOption(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "presets.txt")
	if err := os.WriteFile(path, []byte("thumb=rs:fill:200:200\nsharp=sh:1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := preset.New(preset.Options{File: path, Definitions: map[string]string{"small": "pr:thumb/w:50"}, Only: true})
	if err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff([]string{"sharp", "small", "thumb"}, s.Names()); d != "" || !s.Only() {
		t.Errorf("only=%v names (-want +got):\n%s", s.Only(), d)
	}

	if _, err := preset.New(preset.Options{File: path, Definitions: map[string]string{"thumb": "q:1"}}); err == nil ||
		!strings.Contains(err.Error(), "defined in both") {
		t.Errorf("duplicate across sources: %v", err)
	}
	if _, err := preset.New(preset.Options{File: filepath.Join(dir, "missing.txt")}); err == nil ||
		!strings.Contains(err.Error(), "opening presets file") {
		t.Errorf("missing file: %v", err)
	}
}

// FuzzPresetDefinitions feeds arbitrary presets files through ParseFile and
// New. Neither may panic, and every accepted preset must be well-formed.
func FuzzPresetDefinitions(f *testing.F) {
	for _, s := range []string{
		"thumb=rs:fill:200:200/g:sm\nsharp=sh:0.7",
		"default=sm:1\na=pr:default",
		"a=w:1/-/w:2/-/pr:b\nb=h:1/-/h:2",
		"a=pr:a",
		"a=pr:b\nb=pr:a",
		"a=-/q:1",
		"a=q:1//q:2",
		"# comment\n\n=x",
		"a=pr:b:\nb=q:1",
		"a=wmu:aGk=",
	} {
		f.Add(s, ":")
	}
	f.Add("a=rs,fill/pr,b\nb=q,1", ",")
	f.Fuzz(func(t *testing.T, file, sep string) {
		defs, err := preset.ParseFile(strings.NewReader(file))
		if err != nil {
			return
		}
		s, err := preset.New(preset.Options{Definitions: defs, Separator: sep})
		if err != nil {
			return
		}
		for _, name := range s.Names() {
			p, ok := s.Get(name)
			if !ok || p.Name != name || len(p.Pipelines) == 0 {
				t.Fatalf("preset %q: %+v", name, p)
			}
			for _, pl := range p.Pipelines {
				if len(pl) == 0 {
					t.Fatalf("preset %q has an empty pipeline", name)
				}
				for _, o := range pl {
					if o.Name == "" || len(o.Args) == 0 {
						t.Fatalf("preset %q: bad option %+v", name, o)
					}
					if o.IsReference() {
						for _, ref := range o.Args {
							if _, ok := s.Get(ref); !ok {
								t.Fatalf("preset %q: dangling reference %q", name, ref)
							}
						}
					}
				}
			}
		}
	})
}
