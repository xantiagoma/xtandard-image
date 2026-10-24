package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// unconsumed are the settings no code outside this package reads by
// their field name, each with the reason. TestEveryFieldHasAConsumer fails
// when a new one appears, so a setting cannot be validated and documented
// but silently ignored again (WP-150). Keep the reasons honest: a gap
// belongs in docs/ROADMAP.md and is marked in docs/CONFIG.md.
var unconsumed = map[string]string{
	"encryption.key":            "deprecated alias: Load folds it into encryption.keys",
	"compat.imgproxy_env":       "read by Load: translates IMGPROXY_* variables",
	"video.transform.spool_dir": "read through VideoTransform.SpoolPath",
	"processing.linear_resize":  "not implemented yet (ROADMAP backlog, CONFIG.md)",
}

// TestEveryFieldHasAConsumer checks that every env-settable field of
// Config (named sources included) is selected by name (x.Field) somewhere
// in the non-test code of cmd/ and internal/ outside this package.
//
// The check is by field name, not by type: it cannot see that a common
// name such as Enabled or Secret is read for another struct, so it misses
// some unused fields, but it never flags a field that is read. It is a
// tripwire for new settings, not a proof; the full audit (WP-150) used
// type information. For the same reason it cannot tell when a listed
// field starts being read (Key is selected all over the code base), so
// remove entries from unconsumed by hand when wiring them up.
func TestEveryFieldHasAConsumer(t *testing.T) {
	t.Parallel()
	selected := selectorNames(t, filepath.Join("..", ".."), []string{"cmd", "internal"}, filepath.Join("internal", "config"))
	fields := append(staticFields(), sourceFields("x")...)
	root := map[bool]reflect.Type{false: reflect.TypeFor[Config](), true: reflect.TypeFor[Source]()}
	for _, f := range fields {
		isSource := strings.HasPrefix(f.YAML, "sources.x.")
		name := root[isSource].FieldByIndex(f.Index).Name
		key := f.YAML
		if isSource {
			key = "sources.<name>." + strings.TrimPrefix(f.YAML, "sources.x.")
		}
		if _, allowed := unconsumed[key]; !selected[name] && !allowed {
			t.Errorf("%s (%s, field %s) is read nowhere outside internal/config: wire it up, or list it in unconsumed with the reason", key, f.Env, name)
		}
	}
	for key := range unconsumed {
		found := false
		for _, f := range fields {
			found = found || f.YAML == key
		}
		if !found {
			t.Errorf("unconsumed lists %s, which is not a setting", key)
		}
	}
}

// selectorNames returns every selected name (the Sel of x.Sel) in the
// non-test Go files under base/dirs, skipping the skip directory.
func selectorNames(t *testing.T, base string, dirs []string, skip string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	fset := token.NewFileSet()
	for _, dir := range dirs {
		root := filepath.Join(base, dir)
		err := fs.WalkDir(os.DirFS(base), dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if filepath.Clean(p) == skip || d.Name() == "testdata" {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, filepath.Join(base, p), nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			ast.Inspect(f, func(n ast.Node) bool {
				if se, ok := n.(*ast.SelectorExpr); ok {
					names[se.Sel.Name] = true
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	return names
}
