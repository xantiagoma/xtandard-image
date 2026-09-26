package config

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadFile loads content as the config file path plus environ.
func loadFile(t *testing.T, path, content string, fs files, environ ...string) (Config, error) {
	t.Helper()
	if fs == nil {
		fs = files{}
	}
	fs[path] = content
	cfg, _, err := Load(opts(fs, append([]string{"XI_CONFIG_FILE=" + path}, environ...)...))
	return cfg, err
}

func readCorpus(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(validCorpusDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// docEnv returns the XI_* assignments of a CONFIG.md block, with <hex> and
// ${XI_…} placeholders replaced by testHexKey.
func docEnv(t *testing.T, sec string) []string {
	t.Helper()
	var out []string
	for line := range strings.SplitSeq(docCodeBlock(t, sec, sectionLang(sec)), "\n") {
		line = strings.TrimPrefix(strings.TrimSpace(line), "- ")
		if !strings.HasPrefix(line, "XI_") || !strings.Contains(line, "=") {
			continue
		}
		k, v, _ := strings.Cut(line, "=")
		if v == "<hex>" || strings.HasPrefix(v, "${") {
			v = testHexKey
		}
		out = append(out, k+"="+v)
	}
	if len(out) == 0 {
		t.Fatalf("no XI_ variables in CONFIG.md %s", sec)
	}
	return out
}

func sectionLang(sec string) string {
	if sec == "## 5." {
		return "yaml"
	}
	return ""
}

// TestFormatsLoadIdentically loads the same configuration written in each
// format (and as the deployment examples' environment) and compares the
// resulting Config.
func TestFormatsLoadIdentically(t *testing.T) {
	t.Parallel()
	yamlCfg, err := loadFile(t, "/c.yaml", docCodeBlock(t, "## 3.", "yaml"), nil)
	if err != nil {
		t.Fatalf("CONFIG.md §3 YAML: %v", err)
	}
	k8sSecrets := files{
		"/run/secrets/xi-signing/key":          testHexKey + "\n",
		"/run/secrets/xi-signing/salt":         testHexKey + "\n",
		"/run/secrets/xi-origin/authorization": "Bearer origin-token\n",
	}
	tests := []struct {
		name    string
		path    string
		content string
		environ []string // the equivalent environment-only configuration
		want    *Config
	}{
		{name: "§3 as JSON", path: "/c.json", content: readCorpus(t, "example.json"), want: &yamlCfg},
		{name: "§3 as TOML", path: "/c.toml", content: readCorpus(t, "example.toml"), want: &yamlCfg},
		{name: "Dokploy (§4) as JSON", path: "/c.json", content: readCorpus(t, "dokploy.json"), environ: docEnv(t, "## 4.")},
		{name: "Coolify (§5) as TOML", path: "/c.toml", content: readCorpus(t, "coolify.toml"), environ: docEnv(t, "## 5.")},
		{name: "Kubernetes (§6) with file references", path: "/c.yaml", content: readCorpus(t, "kubernetes.yaml"), environ: []string{
			"XI_ADMIN_ADDR=:9090",
			"XI_SIGNING_KEYS_FILE=/run/secrets/xi-signing/key",
			"XI_SIGNING_SALTS_FILE=/run/secrets/xi-signing/salt",
			"XI_SOURCES=origin",
			"XI_SOURCES_ORIGIN_TYPE=http",
			"XI_SOURCES_ORIGIN_ALLOW=cdn.example.com",
			"XI_SOURCES_ORIGIN_HEADERS=Authorization=Bearer origin-token",
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := loadFile(t, tc.path, tc.content, maps.Clone(k8sSecrets))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			want := tc.want
			if want == nil {
				cfg, _, err := Load(opts(maps.Clone(k8sSecrets), tc.environ...))
				if err != nil {
					t.Fatalf("load environment: %v", err)
				}
				want = &cfg
			}
			if d := diff(*want, got); d != "" {
				t.Errorf("config (-want +got):\n%s", d)
			}
		})
	}
}

// TestSchemaKeyIgnored accepts a top-level $schema in every format and
// rejects it anywhere else.
func TestSchemaKeyIgnored(t *testing.T) {
	t.Parallel()
	const url = "https://xantiagoma.github.io/xtandard-image/schema/v1/config.schema.json"
	for _, tc := range []struct{ path, content string }{
		{"/c.json", `{"$schema": "` + url + `", "server": {"addr": ":1"}}`},
		{"/c.toml", "\"$schema\" = \"" + url + "\"\n[server]\naddr = \":1\"\n"},
		{"/c.yaml", "$schema: " + url + "\nserver: {addr: ':1'}\n"},
		{"/c.yml", "# yaml-language-server: $schema=" + url + "\nserver: {addr: ':1'}\n"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			cfg, err := loadFile(t, tc.path, tc.content, nil, with()...)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if cfg.Server.Addr != ":1" {
				t.Errorf("server.addr = %q, want :1", cfg.Server.Addr)
			}
		})
	}
	t.Run("nested", func(t *testing.T) {
		t.Parallel()
		_, err := loadFile(t, "/c.json", `{"server": {"$schema": "x"}}`, nil, with()...)
		if !errors.Is(err, ErrFile) || !strings.Contains(err.Error(), "unknown field") {
			t.Errorf("err = %v, want unknown field", err)
		}
	})
}

// TestJSONAndTOMLSyntax covers JSON escapes, large integers and nested
// arrays, TOML inline tables, and TOML dates (rejected).
func TestJSONAndTOMLSyntax(t *testing.T) {
	t.Parallel()
	t.Run("json", func(t *testing.T) {
		t.Parallel()
		cfg, err := loadFile(t, "/c.json", `{
			"server": {"path_prefix": "/caf\u00e9\/x", "cors_origins": ["https://a.example"]},
			"limits": {"max_src_pixels": 9007199254740993},
			"dialects": {"imgix": {"mounts": [{"prefix": "/a/", "hosts": ["a.example", "b.example"]}]}},
			"presets": {"q\"x": "quality:80"}
		}`, nil, with()...)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Server.PathPrefix != "/café/x" || cfg.Limits.MaxSrcPixels != 9007199254740993 ||
			len(cfg.Dialects.Imgix.Mounts) != 1 || len(cfg.Dialects.Imgix.Mounts[0].Hosts) != 2 || cfg.Presets[`q"x`] == "" {
			t.Errorf("decoded %+v %d %+v %v", cfg.Server, cfg.Limits.MaxSrcPixels, cfg.Dialects.Imgix.Mounts, cfg.Presets)
		}
	})
	t.Run("toml", func(t *testing.T) {
		t.Parallel()
		cfg, err := loadFile(t, "/c.toml", `
fetch = { timeout = "3s", allowed_ports = [80, 443, 8443] }
[security]
allowed_widths = "100-300/100"
allowed_qualities = [50, "60-80/10"]
[[dialects.imgix.mounts]]
prefix = "/a/"
`, nil, with()...)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if d := diff([]int{100, 200, 300}, []int(cfg.Security.AllowedWidths)); d != "" {
			t.Errorf("allowed_widths: %s", d)
		}
		if d := diff([]int{50, 60, 70, 80}, []int(cfg.Security.AllowedQualities)); d != "" {
			t.Errorf("allowed_qualities: %s", d)
		}
		if cfg.Fetch.Timeout.String() != "3s" || len(cfg.Fetch.AllowedPorts) != 3 || cfg.Dialects.Imgix.Mounts[0].Prefix != "/a/" {
			t.Errorf("decoded %+v %+v", cfg.Fetch, cfg.Dialects.Imgix.Mounts)
		}
	})
	for _, tc := range []struct{ name, path, content, want string }{
		{"toml date", "/c.toml", "[server]\naddr = 1979-05-27\n", "server.addr: TOML dates and times"},
		{"toml syntax", "/c.toml", "[server\n", "toml: line 1"},
		{"json syntax", "/c.json", `{"server": `, "config file /c.json"},
		{"extension", "/c.ini", "a=1", `unsupported extension ".ini"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := loadFile(t, tc.path, tc.content, nil, with()...)
			if !errors.Is(err, ErrFile) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want ErrFile containing %q", err, tc.want)
			}
		})
	}
}

// TestDefaultConfigDiscovery reads whichever /etc/xtandard/config.* exists
// and refuses more than one.
func TestDefaultConfigDiscovery(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		fs       files
		wantFile string
		wantErr  string
	}{
		{name: "json", fs: files{"/etc/xtandard/config.json": `{"server": {"addr": ":1"}}`}, wantFile: "/etc/xtandard/config.json"},
		{name: "toml", fs: files{"/etc/xtandard/config.toml": "[server]\naddr = \":1\"\n"}, wantFile: "/etc/xtandard/config.toml"},
		{name: "yml", fs: files{"/etc/xtandard/config.yml": "server: {addr: ':1'}"}, wantFile: "/etc/xtandard/config.yml"},
		{name: "two", fs: files{
			"/etc/xtandard/config.yaml": "server: {addr: ':1'}",
			"/etc/xtandard/config.toml": "[server]\naddr = \":1\"\n",
		}, wantErr: "found /etc/xtandard/config.yaml and /etc/xtandard/config.toml; keep only one"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, meta, err := load(opts(tc.fs))
			if tc.wantErr != "" {
				if !errors.Is(err, ErrFile) || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if meta.File != tc.wantFile || cfg.Server.Addr != ":1" {
				t.Errorf("file %q addr %q", meta.File, cfg.Server.Addr)
			}
		})
	}
}

// TestDevNullConfig keeps --config /dev/null working as "no file": it skips
// default discovery although it has no known extension.
func TestDevNullConfig(t *testing.T) {
	t.Parallel()
	o := opts(files{os.DevNull: "", DefaultConfigFile: "server: {addr: ':1'}"})
	o.ConfigFile = os.DevNull
	cfg, _, err := load(o)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Server.Addr != ":8080" {
		t.Errorf("server.addr = %q: the default file was read", cfg.Server.Addr)
	}
}

// TestFileRefs proves a {file: …} reference loads like XI_…_FILE for a
// field, a list element and a map value.
func TestFileRefs(t *testing.T) {
	t.Parallel()
	secrets := files{
		"/s/key":    testHexKey + "\n",
		"/s/key2":   "ffeeddccbbaa99887766554433221100",
		"/s/salt":   testHexKey,
		"/s/salt2":  "ffeeddccbbaa99887766554433221100\n",
		"/s/secret": "bearer-secret\r\n",
		"/s/s3":     "s3-secret",
		"/s/auth":   "Bearer t",
		"/s/imgix":  "imgix-token",
		"/s/redis":  "redis://:pw@redis:6379/0\n",
	}
	file := `
signing:
  keys: [{file: /s/key}, {file: /s/key2}]
  salts:
    - file: /s/salt
    - {file: /s/salt2}
  trusted: [literal-sig]
security:
  secret: {file: /s/secret}
sources:
  assets: {type: s3, bucket: b, secret_access_key: {file: /s/s3}}
  origin:
    type: http
    allow: [a.example]
    headers: {Authorization: {file: /s/auth}, X-Plain: plain}
dialects:
  imgix:
    enabled: true
    keys: [{file: /s/imgix}]
    mounts: [{prefix: /i2/, keys: [{file: /s/imgix}]}]
cache:
  result: {redis: {url: {file: /s/redis}}}
`
	got, err := loadFile(t, "/c.yaml", file, maps.Clone(secrets))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want, err := loadFile(t, "/c.yaml", `
signing: {trusted: [literal-sig]}
sources:
  assets: {type: s3, bucket: b}
  origin: {type: http, allow: [a.example]}
dialects:
  imgix:
    enabled: true
    mounts: [{prefix: /i2/, keys: [imgix-token]}]
`, maps.Clone(secrets),
		"XI_SIGNING_KEYS_FILE=/s/key", "XI_SIGNING_SALTS_FILE=/s/salt",
		"XI_SECURITY_SECRET_FILE=/s/secret",
		"XI_SOURCES_ASSETS_SECRET_ACCESS_KEY_FILE=/s/s3",
		"XI_SOURCES_ORIGIN_HEADERS=Authorization=Bearer t,X-Plain=plain",
		"XI_DIALECTS_IMGIX_KEYS_FILE=/s/imgix",
		"XI_CACHE_RESULT_REDIS_URL_FILE=/s/redis",
	)
	if err != nil {
		t.Fatalf("load env: %v", err)
	}
	// The env form has one key; the file rotates two.
	want.Signing.Keys = append(want.Signing.Keys, HexKey{0xff, 0xee, 0xdd, 0xcc, 0xbb, 0xaa, 0x99, 0x88, 0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11, 0x00})
	want.Signing.Salts = append(want.Signing.Salts, want.Signing.Keys[1])
	if d := diff(want, got); d != "" {
		t.Errorf("config (-env +file):\n%s", d)
	}
	if strings.Contains(got.Redacted(), "bearer-secret") {
		t.Error("config print reveals a referenced secret")
	}
}

// TestFileRefErrors checks each failure names the key path and never the
// content, and that the environment still wins over a reference.
func TestFileRefErrors(t *testing.T) {
	t.Parallel()
	secrets := files{
		"/s/big":   strings.Repeat("a", maxSecretFileBytes+1),
		"/s/nohex": "zz-secret-zz",
	}
	tests := []struct {
		name, file string
		want       []string
	}{
		{"relative", "security: {secret: {file: s/x}}", []string{"security.secret (XI_SECURITY_SECRET)", "must be absolute"}},
		{"missing", "security: {secret: {file: /s/missing}}", []string{"security.secret", "/s/missing"}},
		{"oversize", "security: {secret: {file: /s/big}}", []string{"security.secret", "larger than 64 KiB"}},
		{"bad hex", "signing: {keys: [{file: /s/nohex}], salts: [{file: /s/nohex}]}", []string{"signing.keys[0] (XI_SIGNING_KEYS)", "signing.salts[0]", "not valid hex"}},
		{"map value", "sources: {o: {type: http, allow: [a], headers: {Authorization: {file: /s/missing}}}}", []string{"sources.o.headers[Authorization] (XI_SOURCES_O_HEADERS)"}},
		{"bad hex literal", "signing: {keys: [zz-secret-zz], salts: ['00']}", []string{"config file /c.yaml: signing.keys[0] (XI_SIGNING_KEYS): key is not valid hex"}},
		{"list for a secret", "security: {secret: [a, b]}", []string{"security.secret (XI_SECURITY_SECRET): want a string or {file: /absolute/path}"}},
		{"non-secret field", "server: {addr: {file: /s/x}}", []string{"config file /c.yaml"}},
		{"extra key", "security: {secret: {file: /s/x, mode: raw}}", []string{"{file: /absolute/path}"}},
		{"empty path", "security: {secret: {file: ''}}", []string{"needs a file path"}},
		{"NUL literal", "security: {secret: \"\\0x\"}", []string{"NUL"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := loadFile(t, "/c.yaml", tc.file, maps.Clone(secrets), with()...)
			if !errors.Is(err, ErrFile) {
				t.Fatalf("err = %v, want ErrFile", err)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("err = %v, want it to contain %q", err, w)
				}
			}
			if strings.Contains(err.Error(), "zz-secret-zz") || strings.Contains(err.Error(), "aaaa") {
				t.Errorf("error leaks file content: %v", err)
			}
		})
	}

	t.Run("env wins", func(t *testing.T) {
		t.Parallel()
		cfg, err := loadFile(t, "/c.yaml", "security: {secret: {file: /s/missing}}", nil, with("XI_SECURITY_SECRET=from-env")...)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if got := cfg.Security.Secret.Reveal(); got != "from-env" {
			t.Errorf("secret = %q, want the env value", got)
		}
	})
}
