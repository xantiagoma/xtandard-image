package config

import (
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaultsOnly(t *testing.T) {
	t.Parallel()
	cfg, meta, err := Load(opts(nil, with()...))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if meta.File != "" {
		t.Errorf("File = %q, want none", meta.File)
	}
	want := Defaults()
	want.Sources = map[string]Source{"origin": {Type: SourceHTTP, Allow: []string{"cdn.example.com"}}}
	want.Server.WriteTimeout = 25 * time.Second
	want.Processing.Workers = 4
	want.Processing.QueueSize = 64
	want.Signing.Required = False
	want.Playground.Uploads = False
	if d := diff(want, cfg); d != "" {
		t.Errorf("config (-want +got):\n%s", d)
	}
	if len(meta.Warnings) == 0 || !strings.Contains(meta.Warnings[0], "restricted to widths [16 32") {
		t.Errorf("want the restricted-policy warning listing widths, got %q", meta.Warnings)
	}
}

// TestPrecedence proves default < YAML < env for scalars of every kind.
func TestPrecedence(t *testing.T) {
	t.Parallel()
	const yamlDoc = `
server:
  addr: ":7000"
  request_timeout: 30s
  max_header_bytes: 64KiB
limits:
  max_src_pixels: 1000
  max_svg_bytes: 1MiB
observability:
  log_level: debug
processing:
  workers: 3
formats:
  avif:
    quality: 50
`
	tests := []struct {
		name string
		env  []string
		want func(*Config)
	}{
		{
			name: "yaml over defaults",
			want: func(*Config) {},
		},
		{
			name: "env over yaml",
			env: []string{
				"XI_SERVER_ADDR=:9000", "XI_SERVER_REQUEST_TIMEOUT=1m", "XI_SERVER_MAX_HEADER_BYTES=1KiB",
				"XI_LIMITS_MAX_SRC_PIXELS=2000", "XI_OBSERVABILITY_LOG_LEVEL=ERROR", "XI_PROCESSING_WORKERS=5",
				"XI_FORMATS_AVIF_QUALITY=40",
			},
			want: func(c *Config) {
				c.Server.Addr = ":9000"
				c.Server.RequestTimeout = time.Minute
				c.Server.WriteTimeout = time.Minute + 5*time.Second
				c.Server.MaxHeaderBytes = 1 << 10
				c.Limits.MaxSrcPixels = 2000
				c.Observability.LogLevel = LogError
				c.Processing.Workers = 5
				c.Processing.QueueSize = 80
				c.Formats.AVIF.Quality = 40
			},
		},
		{
			name: "empty env value keeps yaml",
			env:  []string{"XI_SERVER_ADDR=", "XI_LIMITS_MAX_SRC_PIXELS="},
			want: func(*Config) {},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := mustParse(t, files{"/c.yaml": yamlDoc}, append(tc.env, "XI_CONFIG_FILE=/c.yaml")...)
			want := Defaults()
			want.Server.Addr = ":7000"
			want.Server.RequestTimeout = 30 * time.Second
			want.Server.WriteTimeout = 35 * time.Second
			want.Server.MaxHeaderBytes = 64 << 10
			want.Limits.MaxSrcPixels = 1000
			want.Limits.MaxSVGBytes = 1 << 20
			want.Observability.LogLevel = LogDebug
			want.Processing.Workers = 3
			want.Processing.QueueSize = 48
			want.Formats.AVIF.Quality = 50
			want.Signing.Required = False
			want.Playground.Uploads = False
			tc.want(&want)
			if d := diff(want, got); d != "" {
				t.Errorf("config (-want +got):\n%s", d)
			}
		})
	}
}

func TestConfigFileSelection(t *testing.T) {
	t.Parallel()
	fs := files{
		DefaultConfigFile: "server: {addr: ':1'}",
		"/env.yaml":       "server: {addr: ':2'}",
		"/flag.yaml":      "server: {addr: ':3'}",
	}
	tests := []struct {
		name     string
		fs       files
		flag     string
		env      []string
		wantAddr string
		wantFile string
		wantErr  error
	}{
		{name: "default path", fs: fs, wantAddr: ":1", wantFile: DefaultConfigFile},
		{name: "default path missing is fine", fs: files{}, wantAddr: ":8080"},
		{name: "XI_CONFIG_FILE", fs: fs, env: []string{"XI_CONFIG_FILE=/env.yaml"}, wantAddr: ":2", wantFile: "/env.yaml"},
		{name: "flag beats env", fs: fs, flag: "/flag.yaml", env: []string{"XI_CONFIG_FILE=/env.yaml"}, wantAddr: ":3", wantFile: "/flag.yaml"},
		{name: "explicit missing file fails", fs: fs, env: []string{"XI_CONFIG_FILE=/nope.yaml"}, wantErr: ErrFile},
		{name: "empty file keeps defaults", fs: files{"/e.yaml": "# only a comment\n"}, env: []string{"XI_CONFIG_FILE=/e.yaml"}, wantAddr: ":8080", wantFile: "/e.yaml"},
		{name: "two documents fail", fs: files{"/m.yaml": "a: 1\n---\nb: 2\n"}, env: []string{"XI_CONFIG_FILE=/m.yaml"}, wantErr: ErrFile},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := opts(tc.fs, tc.env...)
			o.ConfigFile = tc.flag
			cfg, meta, err := load(o)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if cfg.Server.Addr != tc.wantAddr || meta.File != tc.wantFile {
				t.Errorf("addr %q file %q, want %q %q", cfg.Server.Addr, meta.File, tc.wantAddr, tc.wantFile)
			}
		})
	}
}

// TestListsReplace proves an env list replaces the YAML list (and maps
// replace maps) instead of appending or merging.
func TestListsReplace(t *testing.T) {
	t.Parallel()
	const yamlDoc = `
server:
  cors_origins: ["https://a.example", "https://b.example"]
fetch:
  allowed_ports: [80, 443, 8080]
  allowed_networks: [10.0.0.0/8]
security:
  allowed_widths: [100, 200]
presets:
  thumb: "rs:fill:100:100"
  big: "rs:fit:2000"
sources:
  origin:
    type: http
    allow: [a.example, b.example]
`
	tests := []struct {
		name string
		env  []string
		get  func(Config) any
		want any
	}{
		{"yaml list kept", nil, func(c Config) any { return c.Server.CORSOrigins }, []string{"https://a.example", "https://b.example"}},
		{"strings", []string{"XI_SERVER_CORS_ORIGINS=https://c.example"}, func(c Config) any { return c.Server.CORSOrigins }, []string{"https://c.example"}},
		{"ints with spaces", []string{"XI_FETCH_ALLOWED_PORTS=443, 8443"}, func(c Config) any { return c.Fetch.AllowedPorts }, []int{443, 8443}},
		{
			"cidrs",
			[]string{"XI_FETCH_ALLOWED_NETWORKS=192.168.1.0/24,fd00::1"},
			func(c Config) any { return c.Fetch.AllowedNetworks },
			[]CIDR{CIDR(netip.MustParsePrefix("192.168.1.0/24")), CIDR(netip.MustParsePrefix("fd00::1/128"))},
		},
		{"int ranges", []string{"XI_SECURITY_ALLOWED_WIDTHS=300,400-600/100"}, func(c Config) any { return []int(c.Security.AllowedWidths) }, []int{300, 400, 500, 600}},
		{"map", []string{"XI_PRESETS=small=rs:fit:50"}, func(c Config) any { return c.Presets }, map[string]string{"small": "rs:fit:50"}},
		{
			"autoquality per-format map",
			[]string{"XI_FORMATS_AUTOQUALITY_FORMAT_MIN=avif=50,jxl=45", "XI_FORMATS_AUTOQUALITY_FORMAT_TARGET=avif=0.015"},
			func(c Config) any { return []any{c.Formats.AutoQuality.FormatMin, c.Formats.AutoQuality.FormatTarget} },
			[]any{map[Format]int{FormatAVIF: 50, FormatJXL: 45}, map[Format]float64{FormatAVIF: 0.015}},
		},
		{"source list", []string{"XI_SOURCES_ORIGIN_ALLOW=c.example"}, func(c Config) any { return c.Sources["origin"].Allow }, []string{"c.example"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := mustParse(t, files{"/c.yaml": yamlDoc}, append(tc.env, "XI_CONFIG_FILE=/c.yaml")...)
			if d := diff(tc.want, tc.get(cfg)); d != "" {
				t.Errorf("(-want +got):\n%s", d)
			}
		})
	}
}

func TestFileSecrets(t *testing.T) {
	t.Parallel()
	//nolint:gosec // G101: test fixtures, not real credentials
	fs := files{
		"/run/secrets/keys":  "943b421c9eb07c830af81030552c86009268de4e532ba2ee2eab8247c6da0881\n",
		"/run/secrets/salts": "520f986b998545b4785e0defbc4f3c1203f22de2374a3d53cb7a7fe9fea309c5\r\n",
		"/run/secrets/s3":    "s3cr3t\n",
		"/run/secrets/redis": "redis://:pw@redis:6379/0",
	}
	cfg := mustParse(t, fs,
		"XI_SIGNING_KEYS_FILE=/run/secrets/keys",
		"XI_SIGNING_SALTS_FILE=/run/secrets/salts",
		"XI_SOURCES=assets",
		"XI_SOURCES_ASSETS_TYPE=s3",
		"XI_SOURCES_ASSETS_SECRET_ACCESS_KEY_FILE=/run/secrets/s3",
		"XI_CACHE_RESULT_REDIS_URL_FILE=/run/secrets/redis",
	)
	if got := cfg.Signing.Keys[0].Hex(); got != "943b421c9eb07c830af81030552c86009268de4e532ba2ee2eab8247c6da0881" {
		t.Errorf("signing key = %s", got)
	}
	if len(cfg.Signing.Salts) != 1 || len(cfg.Signing.Salts[0]) != 32 {
		t.Errorf("salts = %d entries", len(cfg.Signing.Salts))
	}
	if got := cfg.Sources["assets"].SecretAccessKey.Reveal(); got != "s3cr3t" {
		t.Errorf("secret access key = %q, want trailing newline trimmed", got)
	}
	if got := cfg.Cache.Result.Redis.URL.Reveal(); got != "redis://:pw@redis:6379/0" {
		t.Errorf("redis url = %q", got)
	}
	if !cfg.Signing.Required.Bool() {
		t.Error("signing.required should default to true once keys are set")
	}
}

func TestFileSecretErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		env     []string
		wantMsg string
	}{
		{"both set", []string{"XI_SECURITY_SECRET=a", "XI_SECURITY_SECRET_FILE=/s"}, "set XI_SECURITY_SECRET or XI_SECURITY_SECRET_FILE, not both"},
		{"missing file", []string{"XI_SECURITY_SECRET_FILE=/missing"}, "XI_SECURITY_SECRET_FILE"},
		{"unknown base", []string{"XI_SECURTY_SECRET_FILE=/s"}, "XI_SECURTY_SECRET_FILE (did you mean XI_SECURITY_SECRET?)"},
		{"bad hex from file", []string{"XI_SIGNING_KEYS_FILE=/bad"}, "XI_SIGNING_KEYS: key is not valid hex"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := load(opts(files{"/s": "x", "/bad": "zz-secret-zz"}, tc.env...))
			if err == nil || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantMsg)
			}
			if strings.Contains(err.Error(), "zz-secret-zz") {
				t.Errorf("error leaks the secret: %v", err)
			}
		})
	}
}

func TestPresetsFileIsAVariableNotAnIndirection(t *testing.T) {
	t.Parallel()
	cfg := mustParse(t, nil, "XI_PRESETS_FILE=/etc/xtandard/presets.txt")
	if cfg.PresetsFile != "/etc/xtandard/presets.txt" || cfg.Presets != nil {
		t.Errorf("presets_file = %q presets = %v", cfg.PresetsFile, cfg.Presets)
	}
}

func TestUnknownEnv(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		env     []string
		wantMsg []string
	}{
		{"typo", []string{"XI_SERVER_ADRR=:1"}, []string{"XI_SERVER_ADRR (did you mean XI_SERVER_ADDR?)"}},
		{
			"several",
			[]string{"XI_LIMITS_MAX_SRC_BYTE=1", "XI_NOPE_NOTHING_CLOSE_AT_ALL=1"},
			[]string{"XI_LIMITS_MAX_SRC_BYTE (did you mean XI_LIMITS_MAX_SRC_BYTES?)", "XI_NOPE_NOTHING_CLOSE_AT_ALL (set XI_ALLOW_UNKNOWN_ENV=true"},
		},
		{"undeclared source", []string{"XI_SOURCES_ASSETS_TYPE=s3"}, []string{`source "assets" is not listed in XI_SOURCES`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := load(opts(nil, tc.env...))
			if !errors.Is(err, ErrUnknownEnv) {
				t.Fatalf("err = %v, want ErrUnknownEnv", err)
			}
			for _, m := range tc.wantMsg {
				if !strings.Contains(err.Error(), m) {
					t.Errorf("err = %v, want it to contain %q", err, m)
				}
			}
		})
	}

	t.Run("escape hatch", func(t *testing.T) {
		t.Parallel()
		_, meta, err := load(opts(nil, "XI_ALLOW_UNKNOWN_ENV=true", "XI_SERVER_ADRR=:1", "XI_FOO_FILE=/x", "IMGPROXY_KEY=ignored", "HOME=/root"))
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if d := diff([]string{"XI_FOO_FILE", "XI_SERVER_ADRR"}, meta.UnknownEnv); d != "" {
			t.Errorf("UnknownEnv (-want +got):\n%s", d)
		}
	})
}

func TestUnknownYAMLKey(t *testing.T) {
	t.Parallel()
	for _, doc := range []string{
		"servr: {addr: ':1'}",
		"server: {adr: ':1'}",
		"sources: {origin: {type: http, allowed: [a.example]}}",
		"dialects: {imgix: {enabled: true, mount: x}}",
	} {
		_, _, err := load(opts(files{"/c.yaml": doc}, "XI_CONFIG_FILE=/c.yaml"))
		if !errors.Is(err, ErrFile) || !strings.Contains(err.Error(), "unknown field") {
			t.Errorf("%q: err = %v, want unknown field", doc, err)
		}
	}
}

func TestEnvParseErrorsNameTheVariable(t *testing.T) {
	t.Parallel()
	_, _, err := load(opts(nil,
		"XI_SERVER_READ_TIMEOUT=5x",
		"XI_LIMITS_MAX_SRC_BYTES=10MB",
		"XI_SECURITY_UNSIGNED_POLICY=maybe",
		"XI_FETCH_ALLOWED_NETWORKS=10.0.0.1/8",
		"XI_ADMIN_PPROF=yes please",
		"XI_SOURCES=s",
		"XI_SOURCES_S_TYPE=webdav",
	))
	for _, want := range []string{
		"XI_SERVER_READ_TIMEOUT: duration",
		"XI_LIMITS_MAX_SRC_BYTES: size",
		"XI_SECURITY_UNSIGNED_POLICY: \"maybe\": want one of allow, restricted, deny",
		"XI_FETCH_ALLOWED_NETWORKS: cidr \"10.0.0.1/8\": host bits set, did you mean 10.0.0.0/8",
		"XI_ADMIN_PPROF:",
		"XI_SOURCES_S_TYPE: \"webdav\"",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v\nwant it to contain %q", err, want)
		}
	}
	if !errors.Is(err, ErrEnv) {
		t.Errorf("err does not wrap ErrEnv")
	}
}

func TestSources(t *testing.T) {
	t.Parallel()
	const yamlDoc = `
sources:
  assets:
    type: s3
    endpoint: https://account.r2.cloudflarestorage.com
    region: auto
    buckets: [assets]
  my-files:
    type: fs
    root: /data
`
	cfg := mustParse(t, files{"/c.yaml": yamlDoc},
		"XI_CONFIG_FILE=/c.yaml",
		"XI_SOURCES=origin, Other",
		"XI_SOURCES_ORIGIN_TYPE=http",
		"XI_SOURCES_ORIGIN_ALLOW=cdn.example.com,*.example.org/images/",
		"XI_SOURCES_ORIGIN_HEADERS=Authorization=Bearer t,X-Tenant=a",
		"XI_SOURCES_OTHER_TYPE=gcs",
		"XI_SOURCES_OTHER_BUCKET=b",
		"XI_SOURCES_ASSETS_ACCESS_KEY_ID=AKIA",
		"XI_SOURCES_MY_FILES_ROOT=/srv",
		"XI_SOURCES_MY_FILES_FOLLOW_SYMLINKS=true",
		"XI_SOURCES_OTHER_ENDPOINT=http://fake-gcs:4443/storage/v1/",
	)
	remote := mustParse(t, nil,
		"XI_SOURCES=box",
		"XI_SOURCES_BOX_TYPE=sftp",
		"XI_SOURCES_BOX_HOST=sftp.example.com",
		"XI_SOURCES_BOX_HOST_KEY=ssh-ed25519 AAAA",
		"XI_SOURCES_BOX_USER=xi",
		"XI_SOURCES_BOX_PRIVATE_KEY=pem",
		"XI_SOURCES_BOX_PRIVATE_KEY_PASSPHRASE=pass",
		"XI_SOURCES_BOX_ROOT=/home/xi",
		"XI_SOURCES_BOX_POOL_SIZE=8",
		"XI_SOURCES_BOX_DIAL_TIMEOUT=5s",
		"XI_SOURCES_BOX_IDLE_TIMEOUT=2m",
	)
	wantBox := Source{
		Type: SourceSFTP, Host: "sftp.example.com", HostKey: "ssh-ed25519 AAAA", User: "xi",
		PrivateKey: "pem", PrivateKeyPassphrase: "pass", Root: "/home/xi",
		PoolSize: 8, DialTimeout: 5 * time.Second, IdleTimeout: 2 * time.Minute,
	}
	if d := diff(map[string]Source{"box": wantBox}, remote.Sources); d != "" {
		t.Errorf("remote sources (-want +got):\n%s", d)
	}
	want := map[string]Source{
		"assets": {
			Type: SourceS3, Endpoint: "https://account.r2.cloudflarestorage.com", Region: "auto",
			Buckets: []string{"assets"}, AccessKeyID: "AKIA",
		},
		"my-files": {Type: SourceFS, Root: "/srv", FollowSymlinks: true},
		"origin": {
			Type: SourceHTTP, Allow: []string{"cdn.example.com", "*.example.org/images/"},
			Headers: map[string]Secret{"Authorization": "Bearer t", "X-Tenant": "a"},
		},
		"other": {Type: SourceGCS, Bucket: "b", Endpoint: "http://fake-gcs:4443/storage/v1/"},
	}
	if d := diff(want, cfg.Sources); d != "" {
		t.Errorf("sources (-want +got):\n%s", d)
	}
}

func TestEncryptionKeyAlias(t *testing.T) {
	t.Parallel()
	const k1 = "1eb5b0e971ad7f45324c1bb15c947cb207c43152fa5c6c7f35c4f36e0c18e0f1"
	const k2 = "00112233445566778899aabbccddeeff"
	tests := []struct {
		name     string
		yaml     string
		env      []string
		wantKeys []string
		wantErr  string
		wantWarn bool
	}{
		{name: "env list", env: []string{"XI_ENCRYPTION_KEYS=" + k1 + "," + k2}, wantKeys: []string{k1, k2}},
		{name: "env alias", env: []string{"XI_ENCRYPTION_KEY=" + k2}, wantKeys: []string{k2}, wantWarn: true},
		{name: "yaml alias", yaml: "encryption: {key: " + k1 + "}", wantKeys: []string{k1}},
		{name: "env alias replaces yaml list", yaml: "encryption: {keys: [" + k1 + "]}", env: []string{"XI_ENCRYPTION_KEY=" + k2}, wantKeys: []string{k2}, wantWarn: true},
		{name: "both in env", env: []string{"XI_ENCRYPTION_KEY=" + k2, "XI_ENCRYPTION_KEYS=" + k1}, wantErr: "not both"},
		{name: "both in yaml", yaml: "encryption: {key: " + k2 + ", keys: [" + k1 + "]}", wantErr: "not both"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, meta, err := load(opts(files{"/c.yaml": tc.yaml}, append(tc.env, "XI_CONFIG_FILE=/c.yaml")...))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			var got []string
			for _, k := range cfg.Encryption.Keys {
				got = append(got, k.Hex())
			}
			if d := diff(tc.wantKeys, got); d != "" {
				t.Errorf("keys (-want +got):\n%s", d)
			}
			if cfg.Encryption.Key != nil {
				t.Error("alias not folded into keys")
			}
			if hasWarn := strings.Contains(strings.Join(meta.Warnings, "\n"), "XI_ENCRYPTION_KEY is deprecated"); hasWarn != tc.wantWarn {
				t.Errorf("deprecation warning = %v, want %v", hasWarn, tc.wantWarn)
			}
		})
	}
}

func TestDerivedDefaults(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		env  []string
		want func(Config) any
		exp  any
	}{
		{"write timeout follows request timeout", []string{"XI_SERVER_REQUEST_TIMEOUT=10s"}, func(c Config) any { return c.Server.WriteTimeout }, 15 * time.Second},
		{"explicit write timeout kept", []string{"XI_SERVER_WRITE_TIMEOUT=1m"}, func(c Config) any { return c.Server.WriteTimeout }, time.Minute},
		{"workers default to GOMAXPROCS", nil, func(c Config) any { return c.Processing.Workers }, 4},
		{"queue follows workers", []string{"XI_PROCESSING_WORKERS=2"}, func(c Config) any { return c.Processing.QueueSize }, 32},
		{"explicit zero queue kept", []string{"XI_PROCESSING_QUEUE_SIZE=0"}, func(c Config) any { return c.Processing.QueueSize }, 0},
		{"signing not required without keys", nil, func(c Config) any { return c.Signing.Required }, False},
		{"signing required explicitly off", []string{"XI_SIGNING_KEYS=aa", "XI_SIGNING_SALTS=bb", "XI_SIGNING_REQUIRED=false"}, func(c Config) any { return c.Signing.Required }, False},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if d := diff(tc.exp, tc.want(mustParse(t, nil, tc.env...))); d != "" {
				t.Errorf("(-want +got):\n%s", d)
			}
		})
	}
}

// TestNoSourcesFailsWithoutDevMode is the ROADMAP done criterion.
func TestNoSourcesFailsWithoutDevMode(t *testing.T) {
	t.Parallel()
	_, _, err := Load(opts(nil))
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "no sources configured") {
		t.Fatalf("err = %v, want no sources error", err)
	}
	if _, _, err := Load(opts(nil, "XI_DEV_MODE=true")); err != nil {
		t.Fatalf("dev mode without sources: %v", err)
	}
}

// TestRouterAndFetchEnv covers the restricted-policy DPR/height lists, the
// fetch retry switch and the negative origin TTL.
func TestRouterAndFetchEnv(t *testing.T) {
	t.Parallel()
	cfg := mustParse(t, nil)
	if !slices.Equal(cfg.Security.AllowedDPRs, []float64{1}) || cfg.Security.AllowedHeights != nil ||
		!cfg.Fetch.Retry || cfg.Cache.Origin.NegativeTTL != 5*time.Second {
		t.Errorf("defaults: dprs %v heights %v retry %v negative ttl %s",
			cfg.Security.AllowedDPRs, cfg.Security.AllowedHeights, cfg.Fetch.Retry, cfg.Cache.Origin.NegativeTTL)
	}
	cfg = mustParse(t, nil, "XI_SECURITY_ALLOWED_DPRS=1, 1.5,2", "XI_SECURITY_ALLOWED_HEIGHTS=100-300/100",
		"XI_FETCH_RETRY=false", "XI_CACHE_ORIGIN_NEGATIVE_TTL=0s")
	if !slices.Equal(cfg.Security.AllowedDPRs, []float64{1, 1.5, 2}) || !slices.Equal(cfg.Security.AllowedHeights, IntList{100, 200, 300}) ||
		cfg.Fetch.Retry || cfg.Cache.Origin.NegativeTTL != 0 {
		t.Errorf("env: dprs %v heights %v retry %v negative ttl %s",
			cfg.Security.AllowedDPRs, cfg.Security.AllowedHeights, cfg.Fetch.Retry, cfg.Cache.Origin.NegativeTTL)
	}
}
