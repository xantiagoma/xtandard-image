package config

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var update = flag.Bool("update", false, "rewrite config.schema.json")

const (
	schemaFile     = "config.schema.json"
	validCorpusDir = "testdata/schema/valid"
	testHexKey     = "00112233445566778899aabbccddeeff"
)

// docCodeBlock returns the first ```lang block of CONFIG.md section sec.
func docCodeBlock(t *testing.T, sec, lang string) string {
	t.Helper()
	_, rest, ok := strings.Cut(section(t, readDoc(t), sec), "```"+lang+"\n")
	if !ok {
		t.Fatalf("no %s block in CONFIG.md %s", lang, sec)
	}
	block, _, _ := strings.Cut(rest, "```")
	return block
}

// compileSchema compiles schema with the test-only validator.
func compileSchema(t *testing.T, schema []byte) *jsonschema.Schema {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		t.Fatalf("schema is not JSON: %v", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(SchemaID, doc); err != nil {
		t.Fatalf("add schema: %v", err)
	}
	sch, err := c.Compile(SchemaID)
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	return sch
}

// fileAsJSON converts a config file in any format to the JSON instance the
// schema sees.
func fileAsJSON(t *testing.T, name string, b []byte) any {
	t.Helper()
	f, err := fileFormatOf(name)
	if err != nil {
		t.Fatal(err)
	}
	if f == formatTOML {
		if b, err = tomlToYAML(b); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	j, err := yaml.YAMLToJSON(b)
	if err != nil {
		t.Fatalf("%s: to JSON: %v", name, err)
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(j))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return v
}

// secretFiles are the files the corpus' {file: …} references point at.
func secretFiles() files {
	return files{
		"/run/secrets/xi-signing/key":           testHexKey + "\n",
		"/run/secrets/xi-signing/salt":          testHexKey + "\n",
		"/run/secrets/xi-origin/authorization":  "Bearer origin-token\n",
		"/run/secrets/xi_key_current":           testHexKey,
		"/run/secrets/xi_key_previous":          testHexKey,
		"/run/secrets/xi_salt_current":          testHexKey,
		"/run/secrets/xi_salt_previous":         testHexKey,
		"/run/secrets/s3_secret":                "s3-secret\n",
		"/run/secrets/xi-signing/key-too-large": strings.Repeat("a", maxSecretFileBytes+2),
	}
}

// validCorpus is every config the schema must accept: the CONFIG.md
// examples and testdata/schema/valid.
func validCorpus(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{
		"CONFIG.md §3.yaml":                     []byte(docCodeBlock(t, "## 3.", "yaml")),
		"CONFIG.md §1 secrets in the file.yaml": []byte(docCodeBlock(t, "## 1.", "yaml")),
	}
	entries, err := os.ReadDir(validCorpusDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(validCorpusDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = b
	}
	if len(out) < 7 {
		t.Fatalf("valid corpus has %d files", len(out))
	}
	return out
}

// TestJSONSchema regenerates the schema, fails when the committed
// config.schema.json is stale (fix: go test ./internal/config -run
// TestJSONSchema -update), and checks it against the corpora: the loader
// and the schema must agree on every file.
func TestJSONSchema(t *testing.T) {
	t.Parallel()
	got := generateSchema(t)
	if *update {
		if err := os.WriteFile(schemaFile, got, 0o644); err != nil { //nolint:gosec // G306: a committed, public file
			t.Fatal(err)
		}
	}
	committed, err := os.ReadFile(schemaFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, committed) {
		t.Fatalf("%s is stale (config struct or CONFIG.md changed): run go test ./internal/config -run TestJSONSchema -update", schemaFile)
	}
	if !*update && !bytes.Equal(Schema(), committed) {
		t.Fatal("Schema() differs from the committed file; rebuild")
	}
	sch := compileSchema(t, got)

	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		for name, b := range validCorpus(t) {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				if err := sch.Validate(fileAsJSON(t, name, b)); err != nil {
					t.Errorf("schema rejects a valid file: %v", err)
				}
				path := "/etc/xtandard/c" + filepath.Ext(name)
				fs := secretFiles()
				fs[path] = string(b)
				if _, _, err := Load(opts(fs, "XI_CONFIG_FILE="+path)); err != nil {
					t.Errorf("loader rejects it: %v", err)
				}
			})
		}
	})

	t.Run("invalid", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ name, file string }{
			{"typo.yaml", "server: {adr: ':1'}"},
			{"typo nested.yaml", "cache: {result: {store: [memory]}}"},
			{"typo source field.yaml", "sources: {origin: {type: http, allowed: [a.example]}}"},
			{"wrong type.yaml", "server: {max_connections: many}"},
			{"wrong list type.yaml", "fetch: {allowed_ports: 443}"},
			{"wrong enum.yaml", "security: {unsigned_policy: maybe}"},
			{"wrong enum in list.yaml", "cache: {result: {stores: [memory, floppy]}}"},
			{"s3 field on http source.yaml", "sources: {origin: {type: http, allow: [a.example], bucket: b}}"},
			{"source without type.yaml", "sources: {origin: {allow: [a.example]}}"},
			{"bad source name.yaml", "sources: {Origin: {type: http, allow: [a.example]}}"},
			{"duration without unit.yaml", "fetch: {timeout: 5}"},
			{"decimal size.yaml", "limits: {max_src_bytes: 10MB}"},
			{"negative duration.yaml", "fetch: {timeout: -5s}"},
			{"bad int list.yaml", "security: {allowed_widths: 100-}"},
			{"bad hex.yaml", "signing: {keys: [zz], salts: [zz]}"},
			{"file ref on a non-secret.yaml", "server: {addr: {file: /run/secrets/addr}}"},
			{"relative file ref.yaml", "security: {secret: {file: run/secrets/s}}"},
			{"file ref with extra key.yaml", "security: {secret: {file: /run/secrets/s, mode: raw}}"},
			{"nested $schema.json", `{"server": {"$schema": "x"}}`},
			{"typo.json", `{"server": {"adr": ":1"}}`},
			{"wrong type.toml", "[server]\nmax_connections = \"many\"\n"},
			{"typo.toml", "[fetch]\ntimout = \"5s\"\n"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				if err := sch.Validate(fileAsJSON(t, tc.name, []byte(tc.file))); err == nil {
					t.Error("schema accepts an invalid file")
				}
				path := "/c" + filepath.Ext(tc.name)
				fs := secretFiles()
				fs["/run/secrets/s"] = "s"
				fs[path] = tc.file
				if _, _, err := Load(opts(fs, "XI_CONFIG_FILE="+path, "XI_DEV_MODE=true")); err == nil {
					t.Error("loader accepts an invalid file")
				}
			})
		}
	})
}

// TestSchemaCoversEveryField walks the schema along every leaf the loader
// knows (fieldsOf), so no field is missing from editors.
func TestSchemaCoversEveryField(t *testing.T) {
	t.Parallel()
	var root map[string]any
	if err := yaml.Unmarshal(Schema(), &root); err != nil {
		t.Fatal(err)
	}
	defs, _ := root["$defs"].(map[string]any)
	lookup := func(schema map[string]any, path string) bool {
		for part := range strings.SplitSeq(path, ".") {
			props, _ := schema["properties"].(map[string]any)
			next, ok := props[part].(map[string]any)
			if !ok {
				return false
			}
			schema = next
		}
		return true
	}
	for _, f := range staticFields() {
		if !lookup(root, f.YAML) {
			t.Errorf("schema has no %s (%s)", f.YAML, f.Env)
		}
	}
	for _, f := range sourceFields("x") {
		key := strings.TrimPrefix(f.YAML, "sources.x.")
		found := false
		for _, st := range sourceTypes() {
			d, _ := defs["source_"+string(st)].(map[string]any)
			found = found || lookup(d, key)
		}
		if !found {
			t.Errorf("no source schema has %s", key)
		}
	}
}
