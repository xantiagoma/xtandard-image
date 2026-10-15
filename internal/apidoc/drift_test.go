package apidoc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"regexp"
	"slices"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
)

// tsOptionsFile is the TypeScript option catalogue generated from
// openapi.json (packages/image-url/scripts/generate.ts).
const tsOptionsFile = "../../packages/image-url/src/generated/options.ts"

// tsTable returns the string literals of `export const <name> = [ … ]` or
// `{ "alias": "name", … }` in the generated TypeScript (the keys of an
// object, the elements of an array).
func tsTable(t *testing.T, src, name string) []string {
	t.Helper()
	block := regexp.MustCompile(`(?s)export const ` + regexp.QuoteMeta(name) + ` = [\[{](.*?)[\]}] as const;`).FindStringSubmatch(src)
	if block == nil {
		t.Fatalf("%s: no %s table", tsOptionsFile, name)
	}
	lits := regexp.MustCompile(`(?m)^\s*("(?:[^"\\]|\\.)*")`).FindAllStringSubmatch(block[1], -1)
	out := make([]string, 0, len(lits))
	for _, m := range lits {
		s, err := strconv.Unquote(m[1])
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// TestTypeScriptCatalogue fails when the option catalogues and the
// committed TypeScript types of @xtandard/image-url drift: every option
// name and alias of the processing, /info and /video catalogues must be
// in the generated tables, and nothing else. Fix: go test ./internal/apidoc
// -run TestGolden -update, then bun run generate in packages/image-url.
func TestTypeScriptCatalogue(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(tsOptionsFile)
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, tc := range []struct {
		kind string
		opts []imgproxy.Option
	}{
		{"Processing", imgproxy.ProcessingOptions()},
		{"Info", imgproxy.InfoOptions()},
		{"Video", imgproxy.VideoOptions()},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			t.Parallel()
			names := make([]string, 0, len(tc.opts))
			var aliases []string
			for i := range tc.opts {
				names = append(names, tc.opts[i].Name)
				aliases = append(aliases, tc.opts[i].Aliases...)
			}
			slices.Sort(names)
			slices.Sort(aliases)
			if d := cmp.Diff(names, tsTable(t, src, "imgproxy"+tc.kind+"OptionNames")); d != "" {
				t.Errorf("option names (-catalogue +TypeScript); run bun run generate:\n%s", d)
			}
			if d := cmp.Diff(aliases, tsTable(t, src, "imgproxy"+tc.kind+"Aliases")); d != "" {
				t.Errorf("aliases (-catalogue +TypeScript); run bun run generate:\n%s", d)
			}
		})
	}
}

// TestQueryKeysMatchParser checks the documented native query keys (the
// getQueryImage parameters, which type the TypeScript query builder)
// against the cases of the query dialect's nativeKey switch.
func TestQueryKeysMatchParser(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "../dialect/query/native.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Constants of the switch: the url, o and pdf_password keys.
	consts := map[string]string{
		"paramURL": "url", "paramO": "o", "paramPDFPassword": "pdf_password", "paramPDFPasswordShort": "pdp",
	}
	parsed := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "nativeKey" {
			return true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			for _, s := range sw.Body.List {
				cc, ok := s.(*ast.CaseClause)
				if !ok {
					continue
				}
				for _, e := range cc.List {
					switch v := e.(type) {
					case *ast.BasicLit:
						k, err := strconv.Unquote(v.Value)
						if err != nil {
							t.Fatal(err)
						}
						parsed[k] = true
					case *ast.Ident:
						k, ok := consts[v.Name]
						if !ok {
							t.Fatalf("nativeKey case %s: add it to the constant table", v.Name)
						}
						parsed[k] = true
					}
				}
			}
			return false // the outer switch only
		})
		return false
	})
	if len(parsed) < 20 {
		t.Fatalf("found only %d nativeKey cases; did the switch move?", len(parsed))
	}
	parsed["sign"], parsed["xi_sig"] = true, true // signature parameters (isSignatureParam)
	keys := queryKeys()
	documented := make([]string, 0, len(keys))
	for _, kv := range keys {
		documented = append(documented, kv[0])
	}
	slices.Sort(documented)
	if d := cmp.Diff(slices.Sorted(maps.Keys(parsed)), documented); d != "" {
		t.Errorf("nativeKey cases (-) vs queryKeys (+):\n%s", d)
	}
}
