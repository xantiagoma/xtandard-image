package imgproxy_test

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
)

// catalogNames maps every name and alias of opts to its canonical name.
func catalogNames(t *testing.T, opts []imgproxy.Option) map[string]string {
	t.Helper()
	names := map[string]string{}
	for i := range opts {
		for _, n := range opts[i].Names() {
			if prev, dup := names[n]; dup {
				t.Errorf("name %q is used by %s and %s", n, prev, opts[i].Name)
			}
			names[n] = opts[i].Name
		}
	}
	return names
}

// switchCases returns the string literals of the case clauses of the
// `switch name` statements in the named functions of the given files.
func switchCases(t *testing.T, funcs []string, files ...string) []string {
	t.Helper()
	fset := token.NewFileSet()
	var out []string
	for _, file := range files {
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || !slices.Contains(funcs, fd.Name.Name) {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				sw, ok := n.(*ast.SwitchStmt)
				if !ok {
					return true
				}
				if id, ok := sw.Tag.(*ast.Ident); !ok || id.Name != "name" {
					return true
				}
				for _, s := range sw.Body.List {
					for _, e := range s.(*ast.CaseClause).List { //nolint:forcetypeassert // a switch body holds only case clauses
						if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
							v, err := strconv.Unquote(lit.Value)
							if err != nil {
								t.Fatal(err)
							}
							out = append(out, v)
						}
					}
				}
				return false
			})
		}
	}
	slices.Sort(out)
	return out
}

// TestCatalogMatchesParser checks the catalogue against the parser's apply
// switches in both directions: every case is a catalogue name or alias,
// and every catalogue name and alias is a case.
func TestCatalogMatchesParser(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		opts  []imgproxy.Option
		funcs []string
		files []string
	}{
		{"processing", imgproxy.ProcessingOptions(), []string{"apply", "applyLater"}, []string{"options.go", "later.go"}},
		{"info", imgproxy.InfoOptions(), []string{"applyInfo"}, []string{"info.go"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cases := switchCases(t, tc.funcs, tc.files...)
			if len(cases) < 20 {
				t.Fatalf("found only %d cases in %v; did the switches move?", len(cases), tc.funcs)
			}
			names := slices.Sorted(maps.Keys(catalogNames(t, tc.opts)))
			if diff := cmp.Diff(cases, names); diff != "" {
				t.Errorf("apply cases (-) vs catalogue names and aliases (+):\n%s", diff)
			}
		})
	}
}

// TestCatalogAccepted parses every name and alias with its example in
// strict mode: each must be a known option with valid arguments.
func TestCatalogAccepted(t *testing.T) {
	t.Parallel()
	p, err := imgproxy.New(&imgproxy.Options{Strict: true, AllowSecurityOptions: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		opts  []imgproxy.Option
		parse func(string) error
	}{
		{"processing", imgproxy.ProcessingOptions(), func(s string) error { _, err := p.ParsePath(s); return err }},
		{"info", imgproxy.InfoOptions(), func(s string) error { _, err := p.ParseInfoPath(s); return err }},
	} {
		for _, o := range tc.opts {
			for _, n := range o.Names() {
				path := "/unsafe/" + n + ":" + o.Example + "/plain/https://example.com/a.jpg"
				if err := tc.parse(path); err != nil {
					t.Errorf("%s %s: %v", tc.name, path, err)
				}
			}
		}
	}
}

// docRow is one option row of a URL-DIALECTS option table.
type docRow struct {
	names     [][]string // per option: canonical name then aliases
	pro       bool
	syntax    string
	def       string
	milestone string
}

// docTable parses the table under heading in docs/URL-DIALECTS.md.
func docTable(t *testing.T, heading string) []docRow {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "URL-DIALECTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, sec, ok := strings.Cut(string(b), "\n"+heading+"\n")
	if !ok {
		t.Fatalf("no %q section", heading)
	}
	sec, _, _ = strings.Cut(sec, "\n### ")
	var rows []docRow
	for line := range strings.Lines(sec) {
		if !strings.HasPrefix(line, "| `") && !strings.HasPrefix(line, "| removed") {
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), " | ")
		if len(cells) != 5 {
			t.Fatalf("row with %d cells: %s", len(cells), line)
		}
		first := strings.TrimSpace(cells[0])
		r := docRow{
			pro:    strings.Contains(first, " Pro"),
			syntax: strings.TrimSpace(cells[1]), def: strings.TrimSpace(cells[3]), milestone: strings.TrimSpace(cells[4]),
		}
		// "`a` (`b`, `c`), `d` (`e`)": a name starts outside parentheses.
		depth := 0
		for i := 0; i < len(first); i++ {
			switch first[i] {
			case '(':
				depth++
			case ')':
				depth--
			case '`':
				end := strings.IndexByte(first[i+1:], '`')
				n := first[i+1 : i+1+end]
				if depth == 0 {
					r.names = append(r.names, []string{n})
				} else {
					r.names[len(r.names)-1] = append(r.names[len(r.names)-1], n)
				}
				i += end + 1
			}
		}
		rows = append(rows, r)
	}
	if len(rows) < 20 {
		t.Fatalf("parsed only %d rows under %q", len(rows), heading)
	}
	return rows
}

// TestCatalogMatchesURLDialects checks the catalogue against the §3.2 and
// §3.3 tables: the same options, aliases, Pro flags, argument syntax,
// defaults and milestones, in both directions.
func TestCatalogMatchesURLDialects(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		heading string
		opts    []imgproxy.Option
	}{
		{"### 3.2 Processing options → TR", imgproxy.ProcessingOptions()},
		{"### 3.3 Info options", imgproxy.InfoOptions()},
	} {
		t.Run(tc.heading, func(t *testing.T) {
			t.Parallel()
			byName := map[string]*imgproxy.Option{}
			for i := range tc.opts {
				byName[tc.opts[i].Name] = &tc.opts[i]
			}
			seen := map[string]bool{}
			for _, r := range docTable(t, tc.heading) {
				for _, names := range r.names {
					o, ok := byName[names[0]]
					if !ok {
						t.Errorf("URL-DIALECTS option %q is not in the catalogue", names[0])
						continue
					}
					seen[o.Name] = true
					if diff := cmp.Diff(names, o.Names()); diff != "" {
						t.Errorf("%s names (-doc +catalogue):\n%s", o.Name, diff)
					}
					if r.syntax == "as in §3.2" {
						if !o.Shared {
							t.Errorf("%s: the doc shares it with §3.2 but the catalogue entry is not Shared", o.Name)
						}
						continue
					}
					got := docRow{pro: o.Pro, syntax: o.Syntax, def: o.Default, milestone: o.Milestone}
					want := docRow{pro: r.pro, syntax: r.syntax, def: r.def, milestone: r.milestone}
					if diff := cmp.Diff(want, got, cmp.AllowUnexported(docRow{})); diff != "" {
						t.Errorf("%s row (-doc +catalogue):\n%s", o.Name, diff)
					}
				}
			}
			for n := range byName {
				if !seen[n] {
					t.Errorf("catalogue option %q has no URL-DIALECTS row", n)
				}
			}
		})
	}
}

// compileSchema compiles an options schema with jsonschema/v6.
func compileSchema(t *testing.T, name string, schema map[string]any) *jsonschema.Schema {
	t.Helper()
	b, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	url := "mem://" + name + ".json"
	if err := c.AddResource(url, doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile(url)
	if err != nil {
		t.Fatalf("%s does not compile: %v", name, err)
	}
	return s
}

func validate(t *testing.T, s *jsonschema.Schema, v any) error {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return s.Validate(inst)
}

// occurrence is one option segment of a URL.
type occurrence struct {
	name string
	args []string
}

// urlOptions returns the option segments of an imgproxy path (after the
// signature, up to the source), as the parser splits them.
func urlOptions(path, sep string) []occurrence {
	path, _, _ = strings.Cut(path, "?")
	_, rest, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	var out []occurrence
	for seg := range strings.SplitSeq(rest, "/") {
		if seg == "-" {
			continue
		}
		seg = strings.ReplaceAll(seg, "%3A", ":")
		name, args, ok := strings.Cut(seg, sep)
		if !ok {
			break
		}
		out = append(out, occurrence{name, strings.Split(args, sep)})
	}
	return out
}

type goldenCase struct {
	Path     string          `json:"path"`
	Endpoint string          `json:"endpoint"`
	Config   string          `json:"config"`
	Error    json.RawMessage `json:"error"`
}

// TestSchemaAcceptsTableURLs decodes every successful WP-21a/21b/35
// table-case URL into options objects (one per option occurrence) and
// validates them against the generated schema; the catalogue examples too.
func TestSchemaAcceptsTableURLs(t *testing.T) {
	t.Parallel()
	proc, info := imgproxy.ProcessingOptions(), imgproxy.InfoOptions()
	schemas := map[bool]*jsonschema.Schema{
		false: compileSchema(t, "processing", imgproxy.ProcessingOptionsSchema()),
		true:  compileSchema(t, "info", imgproxy.InfoOptionsSchema()),
	}
	catalogues := map[bool][]imgproxy.Option{false: proc, true: info}
	index := map[bool]map[string]*imgproxy.Option{false: {}, true: {}}
	for isInfo, opts := range catalogues {
		for i := range opts {
			for _, n := range opts[i].Names() {
				index[isInfo][n] = &opts[i]
			}
		}
	}

	checked := 0
	files, err := filepath.Glob(filepath.Join("testdata", "golden", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if filepath.Base(file) == "signing.json" {
			continue
		}
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var cases map[string]goldenCase
		if err := json.Unmarshal(b, &cases); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		for name, c := range cases {
			if c.Error != nil || c.Path == "" {
				continue
			}
			sep := ":"
			if c.Config == "sep" {
				sep = ","
			}
			isInfo := c.Endpoint == "info"
			for _, occ := range urlOptions(c.Path, sep) {
				o, ok := index[isInfo][occ.name]
				if !ok {
					continue // unknown option, recorded in Ignored
				}
				obj := map[string]any{o.Name: o.Decode(occ.args)}
				if err := validate(t, schemas[isInfo], obj); err != nil {
					t.Errorf("%s/%s: %s:%s decoded to %v: %v", filepath.Base(file), name, occ.name, strings.Join(occ.args, sep), obj, err)
				}
				checked++
			}
		}
	}
	for isInfo, opts := range catalogues {
		for i := range opts {
			obj := map[string]any{opts[i].Name: opts[i].Decode(strings.Split(opts[i].Example, ":"))}
			if err := validate(t, schemas[isInfo], obj); err != nil {
				t.Errorf("example %v: %v", obj, err)
			}
			checked++
		}
	}
	if checked < 500 {
		t.Errorf("validated only %d option occurrences", checked)
	}
}

// TestSchemaRejects checks that the schema is strict where the parser is.
func TestSchemaRejects(t *testing.T) {
	t.Parallel()
	s := compileSchema(t, "processing", imgproxy.ProcessingOptionsSchema())
	for _, v := range []map[string]any{
		{"rs": []any{"fill"}},                   // aliases are not property names
		{"resize": []any{"cover"}},              // not a resizing type
		{"quality": []any{101}},                 // out of range
		{"quality": []any{80, 1}},               // too many arguments
		{"rotate": []any{45}},                   // not a multiple of 90
		{"width": []any{nil}},                   // empty width is a 400
		{"gravity": []any{"fp", 0.5}},           // fp needs x and y
		{"background": []any{"zzzzzz"}},         // not hex
		{"enlarge": []any{"yes"}},               // not a boolean
		{"format_quality": []any{"webp"}},       // pairs
		{"progressive_blur": []any{1, "north"}}, // bad direction
		{"nope": []any{1}},                      // unknown option
	} {
		if err := validate(t, s, v); err == nil {
			t.Errorf("%v validates, want a schema error", v)
		}
	}
}

func TestSchemaAnnotations(t *testing.T) {
	t.Parallel()
	props, _ := imgproxy.ProcessingOptionsSchema()["properties"].(map[string]any)
	rs, _ := props["resize"].(map[string]any)
	want := map[string]any{
		"x-xi-aliases": []string{"rs"}, "x-xi-milestone": "M1", "x-xi-pro": false,
		"x-xi-scope": "pipeline", "x-xi-syntax": "type:w:h:enlarge:extend(:gravity…)",
	}
	for k, v := range want {
		if diff := cmp.Diff(v, rs[k]); diff != "" {
			t.Errorf("resize %s (-want +got):\n%s", k, diff)
		}
	}
	msr, _ := props["max_src_resolution"].(map[string]any)
	if msr["x-xi-security"] != true {
		t.Errorf("max_src_resolution lacks x-xi-security")
	}
}
