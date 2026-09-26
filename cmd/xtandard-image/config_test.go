package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/config"
)

// writeConfig writes content to a file named name in a temp dir.
func writeConfig(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const validYAML = `# yaml-language-server: $schema=https://xantiagoma.github.io/xtandard-image/schema/config.schema.json
sources:
  origin: {type: http, allow: [cdn.example.com]}
`

func TestConfigValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		file     string // name of the config file
		content  string
		args     []string
		environ  []string
		wantCode int
		want     []string // substrings of the output
	}{
		{name: "valid yaml", file: "c.yaml", content: validYAML, args: []string{"--no-env"}, want: []string{"c.yaml: ok"}},
		{name: "valid json", file: "c.json", content: `{"$schema": "x", "sources": {"origin": {"type": "http", "allow": ["a.example"]}}}`, args: []string{"--no-env"}, want: []string{"c.json: ok"}},
		{name: "valid toml", file: "c.toml", content: "[sources.origin]\ntype = \"http\"\nallow = [\"a.example\"]\n", args: []string{"--no-env"}, want: []string{"c.toml: ok"}},
		{name: "typo", file: "c.yaml", content: validYAML + "server: {adr: ':1'}\n", args: []string{"--no-env"}, wantCode: 1, want: []string{"c.yaml: invalid", `unknown field "adr"`}},
		{
			name: "every violation", file: "c.yaml", wantCode: 1,
			content: validYAML + "signing: {size: 99}\nfetch: {max_redirects: 50}\nformats: {webp: {effort: 9}}\n",
			args:    []string{"--no-env"},
			want: []string{
				"signing.size (XI_SIGNING_SIZE): must be between 1 and 32",
				"fetch.max_redirects (XI_FETCH_MAX_REDIRECTS)",
				"formats.webp.effort (XI_FORMATS_WEBP_EFFORT)",
			},
		},
		{name: "env applies", file: "c.yaml", content: validYAML, environ: []string{"XI_SIGNING_SIZE=99"}, wantCode: 1, want: []string{"signing.size (XI_SIGNING_SIZE)"}},
		{name: "no-env ignores env", file: "c.yaml", content: validYAML, args: []string{"--no-env"}, environ: []string{"XI_SIGNING_SIZE=99", "XI_SERVER_ADRR=:1", "IMGPROXY_KEY=zz"}, want: []string{"c.yaml: ok"}},
		{name: "unknown env", file: "c.yaml", content: validYAML, environ: []string{"XI_SERVER_ADRR=:1"}, wantCode: 1, want: []string{"XI_SERVER_ADRR (did you mean XI_SERVER_ADDR?)"}},
		{name: "missing secret file", file: "c.yaml", content: validYAML + "security: {secret: {file: /nonexistent/xi-secret}}\n", args: []string{"--no-env"}, wantCode: 1, want: []string{"security.secret (XI_SECURITY_SECRET): {file: /nonexistent/xi-secret}"}},
		{name: "unsupported extension", file: "c.ini", content: "a=1", args: []string{"--no-env"}, wantCode: 1, want: []string{`unsupported extension ".ini"`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := writeConfig(t, tc.file, tc.content)
			args := append([]string{"config", "validate", "--config", path}, tc.args...)
			var out bytes.Buffer
			err := run(t.Context(), args, tc.environ, &out)
			if got := exitCode(err); got != tc.wantCode {
				t.Fatalf("exit code %d (err %v), want %d; output:\n%s", got, err, tc.wantCode, out.String())
			}
			if tc.wantCode != 0 && !errors.Is(err, errInvalidConfig) {
				t.Errorf("err = %v, want errInvalidConfig", err)
			}
			for _, w := range tc.want {
				if !strings.Contains(out.String(), w) {
					t.Errorf("output lacks %q:\n%s", w, out.String())
				}
			}
		})
	}
}

func TestConfigSchemaCommand(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := run(t.Context(), []string{"config", "schema"}, nil, &out); err != nil {
		t.Fatalf("config schema: %v", err)
	}
	if !bytes.Equal(out.Bytes(), config.Schema()) {
		t.Error("config schema output differs from the embedded schema")
	}
	if !bytes.Contains(out.Bytes(), []byte(`"$id": "`+config.SchemaID+`"`)) {
		t.Errorf("schema lacks $id %s", config.SchemaID)
	}
}

func TestConfigUnknownSubcommand(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"config"}, {"config", "nope"}} {
		err := run(t.Context(), args, nil, &bytes.Buffer{})
		if !errors.Is(err, errUnknownCommand) || exitCode(err) != 1 {
			t.Errorf("run(%q) = %v, want errUnknownCommand", args, err)
		}
	}
}
