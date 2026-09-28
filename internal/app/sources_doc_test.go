//go:build cgo

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// docSource is a source.Source that is never fetched from.
type docSource struct{ name, typ string }

func (s docSource) Name() string { return s.name }
func (s docSource) Type() string { return s.typ }
func (docSource) Fetch(context.Context, source.Key, source.FetchOptions) (*source.Object, error) {
	return nil, errors.New("not fetched")
}

// resolutionRow is one row of a docs/SOURCES.md resolution table.
type resolutionRow struct {
	line                   int
	raw, mount, want, body string
}

// resolutionTables returns, per config file, the rows of every table that
// follows a "<!-- resolution-table: docs/examples/<file> -->" marker.
func resolutionTables(t *testing.T, doc string) map[string][]resolutionRow {
	t.Helper()
	b, err := os.ReadFile(doc)
	if err != nil {
		t.Fatal(err)
	}
	const marker = "<!-- resolution-table: "
	out := map[string][]resolutionRow{}
	file := ""
	for i, line := range strings.Split(string(b), "\n") {
		switch {
		case strings.HasPrefix(line, marker):
			file = strings.TrimSuffix(strings.TrimPrefix(line, marker), " -->")
		case file != "" && strings.HasPrefix(line, "|"):
			cells := strings.Split(line, "|")
			if len(cells) != 5 {
				t.Fatalf("%s:%d: want 3 cells", doc, i+1)
			}
			raw := strings.Trim(strings.TrimSpace(cells[1]), "`")
			if raw == "Source string" || strings.HasPrefix(raw, "---") {
				continue
			}
			row := resolutionRow{line: i + 1, raw: raw, mount: strings.Trim(strings.TrimSpace(cells[2]), "`")}
			if row.mount == "—" {
				row.mount = ""
			}
			want := strings.TrimSpace(cells[3])
			if status, msg, ok := strings.Cut(want, " `"); ok && (status == "400" || status == "403") {
				row.want, row.body = status, strings.TrimSuffix(msg, "`")
			} else {
				row.want = strings.Trim(want, "`")
			}
			out[file] = append(out[file], row)
		case file != "" && strings.TrimSpace(line) != "" && len(out[file]) > 0:
			file = "" // the table ended
		}
	}
	return out
}

// TestSourcesDocResolution checks the resolution tables of docs/SOURCES.md:
// each config example is loaded, its sources registered in the order the
// app uses (sourceOrder), and every row's source string resolved with the
// row's mount source. A resolved key must print as the row says
// (source.RedactKey), a refusal must have the row's status and public
// message.
func TestSourcesDocResolution(t *testing.T) {
	t.Parallel()
	root := golden.ModuleRoot(t)
	tables := resolutionTables(t, filepath.Join(root, "docs", "SOURCES.md"))
	if len(tables) == 0 {
		t.Fatal("docs/SOURCES.md has no resolution table")
	}
	for file, rows := range tables {
		cfgPath := filepath.Join(root, filepath.FromSlash(file))
		cfg, _, err := config.Load(config.LoadOptions{
			ConfigFile: cfgPath,
			ReadFile: func(name string) ([]byte, error) {
				if name == cfgPath {
					return os.ReadFile(name)
				}
				return []byte("00112233445566778899aabbccddeeff"), nil // secrets
			},
		})
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		var bindings []source.Binding
		for _, name := range sourceOrder(&cfg) {
			sc := cfg.Sources[name]
			bindings = append(bindings, source.Binding{Spec: sourceSpec(name, &sc), Source: docSource{name, string(sc.Type)}})
		}
		reg, err := source.NewRegistry(source.Options{Sources: bindings})
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if len(rows) < 10 {
			t.Fatalf("%s: only %d rows", file, len(rows))
		}
		for _, r := range rows {
			where := "SOURCES.md:" + strconv.Itoa(r.line) + " " + r.raw
			k, err := reg.Resolve(transform.SourceSpec{Raw: r.raw, Mount: r.mount})
			if r.body == "" {
				if err != nil {
					t.Errorf("%s: want %s, got %v", where, r.want, err)
				} else if got := source.RedactKey(k); got != r.want {
					t.Errorf("%s: resolves to %s, the table says %s", where, got, r.want)
				}
				continue
			}
			e, ok := errors.AsType[*errs.Error](err)
			if !ok {
				t.Errorf("%s: want %s %q, got %v (key %s)", where, r.want, r.body, err, source.RedactKey(k))
				continue
			}
			if got := strconv.Itoa(e.HTTPStatus()) + " " + e.PublicMessage(); got != r.want+" "+r.body {
				t.Errorf("%s: answers %q, the table says %q", where, got, r.want+" "+r.body)
			}
		}
	}
}
