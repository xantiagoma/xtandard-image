package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Published locations (WP-81b, ADR 0007 decision 6): scripts/pages-build.sh
// publishes the schema at pagesSchemaBase + "v<major>/" and "latest/".
const (
	pagesSchemaBase   = "https://xantiagoma.github.io/xtandard-image/schema/"
	schemaStoreDir    = "../../docs/schemastore"
	schemaStoreEntry  = schemaStoreDir + "/catalog-entry.json"
	schemaStoreTests  = schemaStoreDir + "/tests"
	wantSchemaIDMajor = "v1"
)

// TestSchemaID pins the published $id: it is a forever URL (ADR 0007
// Consequences), so changing it must be a deliberate edit here too.
func TestSchemaID(t *testing.T) {
	t.Parallel()
	const want = pagesSchemaBase + wantSchemaIDMajor + "/config.schema.json"
	if SchemaID != want {
		t.Errorf("SchemaID = %q, want %q", SchemaID, want)
	}
	var doc struct {
		ID     string `json:"$id"`
		Schema string `json:"$schema"`
	}
	if err := json.Unmarshal(Schema(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.ID != want {
		t.Errorf("config.schema.json $id = %q, want %q", doc.ID, want)
	}
	if doc.Schema != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("$schema = %q, want draft 2020-12", doc.Schema)
	}
}

// TestSchemaURLsInDocs keeps every Pages URL of the config schema in the
// docs, the examples and the SchemaStore entry on the $id (or the latest
// alias), so no modeline points at a path that is never published.
func TestSchemaURLsInDocs(t *testing.T) {
	t.Parallel()
	re := regexp.MustCompile(regexp.QuoteMeta(pagesSchemaBase) + `[^\s"'` + "`" + `)>]*config\.schema\.json`)
	allowed := map[string]bool{SchemaID: true, pagesSchemaBase + "latest/config.schema.json": true}
	paths := []string{"../../README.md", "../../docs/CONFIG.md", schemaStoreEntry}
	for _, dir := range []string{validCorpusDir, schemaStoreTests + "/positive", schemaStoreTests + "/negative"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, u := range re.FindAllString(string(b), -1) {
			if !allowed[u] {
				t.Errorf("%s: %s is not the published $id %s (or the latest alias)", p, u, SchemaID)
			}
		}
	}
}

// TestSchemaStoreEntry checks docs/schemastore against SchemaStore's catalog
// lint (cli.js assertCatalogJsonHasNoBadFields) and the schema: the entry
// points at the $id, and the positive and negative samples are accepted and
// rejected by both the schema and the loader.
func TestSchemaStoreEntry(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(schemaStoreEntry)
	if err != nil {
		t.Fatal(err)
	}
	var entry struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		FileMatch   []string `json:"fileMatch"`
		URL         string   `json:"url"`
	}
	if err := json.Unmarshal(b, &entry); err != nil {
		t.Fatal(err)
	}
	if entry.URL != SchemaID {
		t.Errorf("url = %q, want the $id %q", entry.URL, SchemaID)
	}
	for _, s := range []string{entry.Name, entry.Description} {
		switch {
		case s == "":
			t.Error("name and description are required")
		case strings.Contains(strings.ToLower(s), "schema"):
			t.Errorf("%q: SchemaStore rejects the word schema in name or description", s)
		case strings.ContainsAny(s[len(s)-1:], ",. \t-"), strings.Contains(s, "\n"):
			t.Errorf("%q: SchemaStore rejects a trailing ,.<space><tab>- or a newline", s)
		}
	}
	for _, m := range entry.FileMatch {
		if !strings.Contains(m, "xtandard") {
			t.Errorf("fileMatch %q is not project-specific", m)
		}
		if strings.Contains(m, "/") && !strings.HasPrefix(m, "**/") {
			t.Errorf("fileMatch %q: a directory pattern must start with **/", m)
		}
		if strings.ContainsAny(m, "{}*") && !strings.HasPrefix(m, "**/") {
			t.Errorf("fileMatch %q: SchemaStore prefers simple patterns", m)
		}
	}

	sch := compileSchema(t, Schema())
	for _, tc := range []struct {
		dir   string
		valid bool
	}{{"positive", true}, {"negative", false}} {
		dir := filepath.Join(schemaStoreTests, tc.dir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) == 0 {
			t.Fatalf("%s is empty", dir)
		}
		for _, e := range entries {
			t.Run(tc.dir+"/"+e.Name(), func(t *testing.T) {
				t.Parallel()
				b, err := os.ReadFile(filepath.Join(dir, e.Name()))
				if err != nil {
					t.Fatal(err)
				}
				if err := sch.Validate(fileAsJSON(t, e.Name(), b)); (err == nil) != tc.valid {
					t.Errorf("schema validation error = %v, want valid=%v", err, tc.valid)
				}
				path := "/etc/xtandard/config" + filepath.Ext(e.Name())
				if _, _, err := Load(opts(files{path: string(b)}, "XI_CONFIG_FILE="+path)); (err == nil) != tc.valid {
					t.Errorf("Load error = %v, want valid=%v", err, tc.valid)
				}
			})
		}
	}
}
