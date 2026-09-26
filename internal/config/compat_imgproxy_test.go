package config

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
)

const compatOn = envCompatImgproxy + "=true"

type compatCase struct {
	env  []string          // IMGPROXY_* variables, including the one under test
	want map[string]string // translated XI_* values
}

// Test-only hex keys.
const (
	hexA = "943b421c9eb07c830af81030552c86009268de4e532ba2ee2eab8247c6da0881"
	hexB = "520f986b998545b4785e0defbc4f3c1203f22de2374a3d53cb7a7fe9fea309c5"
)

// imgproxyCases holds one case per entry of imgproxyVars, keyed by name.
// Example values differ from the xtandard-image defaults so the loaded
// config visibly changes.
func imgproxyCases() map[string]compatCase {
	s3 := []string{"IMGPROXY_USE_S3=true", "IMGPROXY_S3_ALLOWED_BUCKETS=assets"}
	s3Want := map[string]string{"XI_SOURCES": "s3", "XI_SOURCES_S3_TYPE": "s3", "XI_SOURCES_S3_BUCKETS": "assets"}
	gcs := []string{"IMGPROXY_USE_GCS=true", "IMGPROXY_GCS_ALLOWED_BUCKETS=assets"}
	gcsWant := map[string]string{"XI_SOURCES": "gcs", "XI_SOURCES_GCS_TYPE": "gcs", "XI_SOURCES_GCS_BUCKETS": "assets"}
	abs := []string{"IMGPROXY_USE_ABS=true", "IMGPROXY_ABS_ALLOWED_BUCKETS=assets"}
	absWant := map[string]string{"XI_SOURCES": "abs", "XI_SOURCES_ABS_TYPE": "azure", "XI_SOURCES_ABS_BUCKETS": "assets"}
	local := []string{"IMGPROXY_LOCAL_FILESYSTEM_ROOT=/data"}
	localWant := map[string]string{"XI_SOURCES": "local", "XI_SOURCES_LOCAL_TYPE": "fs", "XI_SOURCES_LOCAL_ROOT": "/data"}
	with := func(base []string, extra ...string) []string { return append(slices.Clone(base), extra...) }
	plus := func(base map[string]string, kv ...string) map[string]string {
		m := maps.Clone(base)
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	one := func(env, k, v string) compatCase {
		return compatCase{[]string{env}, map[string]string{k: v}}
	}
	return map[string]compatCase{
		"IMGPROXY_KEY":                        one("IMGPROXY_KEY="+hexA, "XI_SIGNING_KEYS", hexA),
		"IMGPROXY_SALT":                       one("IMGPROXY_SALT="+hexB, "XI_SIGNING_SALTS", hexB),
		"IMGPROXY_SIGNATURE_SIZE":             one("IMGPROXY_SIGNATURE_SIZE=8", "XI_SIGNING_SIZE", "8"),
		"IMGPROXY_TRUSTED_SIGNATURES":         one("IMGPROXY_TRUSTED_SIGNATURES=abc,def", "XI_SIGNING_TRUSTED", "abc,def"),
		"IMGPROXY_SOURCE_URL_ENCRYPTION_KEY":  one("IMGPROXY_SOURCE_URL_ENCRYPTION_KEY="+hexA, "XI_ENCRYPTION_KEYS", hexA),
		"IMGPROXY_SECRET":                     one("IMGPROXY_SECRET=s3cret", "XI_SECURITY_SECRET", "s3cret"),
		"IMGPROXY_ALLOWED_PROCESSING_OPTIONS": one("IMGPROXY_ALLOWED_PROCESSING_OPTIONS=rs,q", "XI_SECURITY_ALLOWED_PROCESSING_OPTIONS", "rs,q"),
		"IMGPROXY_ALLOW_SECURITY_OPTIONS":     one("IMGPROXY_ALLOW_SECURITY_OPTIONS=true", "XI_SECURITY_ALLOW_SECURITY_OPTIONS", "true"),
		"IMGPROXY_ALLOWED_SOURCES": {
			[]string{"IMGPROXY_ALLOWED_SOURCES=https://cdn.example.com/, http://*.example.org/img/,https://a.example.net:8443"},
			map[string]string{"XI_SOURCES": "http", "XI_SOURCES_HTTP_TYPE": "http", "XI_SOURCES_HTTP_ALLOW": "cdn.example.com,*.example.org/img,a.example.net:8443"},
		},
		"IMGPROXY_USE_S3": {
			[]string{"IMGPROXY_USE_S3=true", "IMGPROXY_ALLOWED_SOURCES=s3://assets/,s3://logos-*/"},
			map[string]string{"XI_SOURCES": "s3", "XI_SOURCES_S3_TYPE": "s3", "XI_SOURCES_S3_BUCKETS": "assets,logos-*"},
		},
		"IMGPROXY_S3_ALLOWED_BUCKETS": {s3, s3Want},
		"IMGPROXY_S3_DENIED_BUCKETS":  {with(s3, "IMGPROXY_S3_DENIED_BUCKETS=x"), nil},
		"IMGPROXY_S3_REGION": {
			with(s3, "IMGPROXY_S3_REGION=eu-west-1"), plus(s3Want, "XI_SOURCES_S3_REGION", "eu-west-1"),
		},
		"IMGPROXY_S3_ENDPOINT_USE_PATH_STYLE": {
			with(s3, "IMGPROXY_S3_ENDPOINT_USE_PATH_STYLE=true"), plus(s3Want, "XI_SOURCES_S3_FORCE_PATH_STYLE", "true"),
		},
		"IMGPROXY_S3_ENDPOINT": {
			with(s3, "IMGPROXY_S3_ENDPOINT=https://s3.example.com"),
			plus(s3Want, "XI_SOURCES_S3_ENDPOINT", "https://s3.example.com", "XI_SOURCES_S3_FORCE_PATH_STYLE", "true"),
		},
		"IMGPROXY_S3_ASSUME_ROLE_ARN": {
			with(s3, "IMGPROXY_S3_ASSUME_ROLE_ARN=arn:aws:iam::1:role/x"), plus(s3Want, "XI_SOURCES_S3_ROLE_ARN", "arn:aws:iam::1:role/x"),
		},
		"IMGPROXY_USE_GCS":             {gcs, gcsWant},
		"IMGPROXY_GCS_ALLOWED_BUCKETS": {gcs, gcsWant},
		"IMGPROXY_GCS_DENIED_BUCKETS":  {with(gcs, "IMGPROXY_GCS_DENIED_BUCKETS=x"), nil},
		"IMGPROXY_GCS_KEY": {
			with(gcs, `IMGPROXY_GCS_KEY={"type":"service_account"}`), plus(gcsWant, "XI_SOURCES_GCS_CREDENTIALS_JSON", `{"type":"service_account"}`),
		},
		"IMGPROXY_GCS_ENDPOINT": {
			with(gcs, "IMGPROXY_GCS_ENDPOINT=http://fake-gcs:4443/storage/v1/"), plus(gcsWant, "XI_SOURCES_GCS_ENDPOINT", "http://fake-gcs:4443/storage/v1/"),
		},
		"IMGPROXY_ABS_ENDPOINT": {
			with(abs, "IMGPROXY_ABS_ENDPOINT=http://azurite:10000/devstoreaccount1"), plus(absWant, "XI_SOURCES_ABS_ENDPOINT", "http://azurite:10000/devstoreaccount1"),
		},
		"IMGPROXY_USE_ABS":             {abs, absWant},
		"IMGPROXY_ABS_ALLOWED_BUCKETS": {abs, absWant},
		"IMGPROXY_ABS_DENIED_BUCKETS":  {with(abs, "IMGPROXY_ABS_DENIED_BUCKETS=x"), nil},
		"IMGPROXY_ABS_NAME":            {with(abs, "IMGPROXY_ABS_NAME=acct"), plus(absWant, "XI_SOURCES_ABS_ACCOUNT", "acct")},
		"IMGPROXY_ABS_KEY":             {with(abs, "IMGPROXY_ABS_KEY=a2V5"), plus(absWant, "XI_SOURCES_ABS_ACCOUNT_KEY", "a2V5")},
		"IMGPROXY_LOCAL_FILESYSTEM_ROOT": {
			[]string{"IMGPROXY_LOCAL_FILESYSTEM_ROOT=/data", "IMGPROXY_ALLOWED_SOURCES=local:///images/"},
			plus(localWant, "XI_SOURCES_LOCAL_PREFIX", "images/"),
		},
		"IMGPROXY_ALLOW_LOOPBACK_SOURCE_ADDRESSES":   one("IMGPROXY_ALLOW_LOOPBACK_SOURCE_ADDRESSES=true", "XI_FETCH_ALLOWED_NETWORKS", "127.0.0.0/8,::1/128"),
		"IMGPROXY_ALLOW_LINK_LOCAL_SOURCE_ADDRESSES": one("IMGPROXY_ALLOW_LINK_LOCAL_SOURCE_ADDRESSES=1", "XI_FETCH_ALLOWED_NETWORKS", "169.254.0.0/16,fe80::/10"),
		"IMGPROXY_ALLOW_PRIVATE_SOURCE_ADDRESSES": {
			[]string{"IMGPROXY_ALLOW_PRIVATE_SOURCE_ADDRESSES=true", "IMGPROXY_ALLOW_LOOPBACK_SOURCE_ADDRESSES=false"},
			map[string]string{"XI_FETCH_ALLOWED_NETWORKS": "10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,100.64.0.0/10,fc00::/7"},
		},
		"IMGPROXY_DOWNLOAD_TIMEOUT":               one("IMGPROXY_DOWNLOAD_TIMEOUT=3", "XI_FETCH_TIMEOUT", "3s"),
		"IMGPROXY_MAX_REDIRECTS":                  one("IMGPROXY_MAX_REDIRECTS=2", "XI_FETCH_MAX_REDIRECTS", "2"),
		"IMGPROXY_USER_AGENT":                     one("IMGPROXY_USER_AGENT=bot/1", "XI_FETCH_USER_AGENT", "bot/1"),
		"IMGPROXY_BIND":                           one("IMGPROXY_BIND=:9000", "XI_SERVER_ADDR", ":9000"),
		"IMGPROXY_PATH_PREFIX":                    one("IMGPROXY_PATH_PREFIX=/img", "XI_SERVER_PATH_PREFIX", "/img"),
		"IMGPROXY_TIMEOUT":                        one("IMGPROXY_TIMEOUT=30", "XI_SERVER_REQUEST_TIMEOUT", "30s"),
		"IMGPROXY_READ_REQUEST_TIMEOUT":           one("IMGPROXY_READ_REQUEST_TIMEOUT=7", "XI_SERVER_READ_TIMEOUT", "7s"),
		"IMGPROXY_WRITE_RESPONSE_TIMEOUT":         one("IMGPROXY_WRITE_RESPONSE_TIMEOUT=40", "XI_SERVER_WRITE_TIMEOUT", "40s"),
		"IMGPROXY_KEEP_ALIVE_TIMEOUT":             one("IMGPROXY_KEEP_ALIVE_TIMEOUT=11", "XI_SERVER_IDLE_TIMEOUT", "11s"),
		"IMGPROXY_GRACEFUL_STOP_TIMEOUT":          one("IMGPROXY_GRACEFUL_STOP_TIMEOUT=12", "XI_SERVER_SHUTDOWN_TIMEOUT", "12s"),
		"IMGPROXY_MAX_CLIENTS":                    one("IMGPROXY_MAX_CLIENTS=100", "XI_SERVER_MAX_CONNECTIONS", "100"),
		"IMGPROXY_ALLOW_ORIGIN":                   one("IMGPROXY_ALLOW_ORIGIN=https://app.example.com", "XI_SERVER_CORS_ORIGINS", "https://app.example.com"),
		"IMGPROXY_WORKERS":                        one("IMGPROXY_WORKERS=3", "XI_PROCESSING_WORKERS", "3"),
		"IMGPROXY_REQUESTS_QUEUE_SIZE":            one("IMGPROXY_REQUESTS_QUEUE_SIZE=10", "XI_PROCESSING_QUEUE_SIZE", "10"),
		"IMGPROXY_MAX_SRC_RESOLUTION":             one("IMGPROXY_MAX_SRC_RESOLUTION=16.8", "XI_LIMITS_MAX_SRC_PIXELS", "16800000"),
		"IMGPROXY_MAX_SRC_FILE_SIZE":              one("IMGPROXY_MAX_SRC_FILE_SIZE=1048576", "XI_LIMITS_MAX_SRC_BYTES", "1048576"),
		"IMGPROXY_MAX_ANIMATION_FRAMES":           one("IMGPROXY_MAX_ANIMATION_FRAMES=10", "XI_LIMITS_MAX_ANIMATION_FRAMES", "10"),
		"IMGPROXY_MAX_ANIMATION_FRAME_RESOLUTION": one("IMGPROXY_MAX_ANIMATION_FRAME_RESOLUTION=2", "XI_LIMITS_MAX_ANIMATION_FRAME_PIXELS", "2000000"),
		"IMGPROXY_MAX_RESULT_DIMENSION":           one("IMGPROXY_MAX_RESULT_DIMENSION=4096", "XI_LIMITS_MAX_RESULT_DIMENSION", "4096"),
		"IMGPROXY_TTL":                            one("IMGPROXY_TTL=3600", "XI_CACHE_HEADERS_TTL", "3600s"),
		"IMGPROXY_CACHE_CONTROL_PASSTHROUGH":      one("IMGPROXY_CACHE_CONTROL_PASSTHROUGH=true", "XI_CACHE_HEADERS_PASSTHROUGH", "true"),
		"IMGPROXY_ETAG_BUSTER":                    one("IMGPROXY_ETAG_BUSTER=v2", "XI_CACHE_ETAG_BUSTER", "v2"),
		"IMGPROXY_ARGUMENTS_SEPARATOR":            one("IMGPROXY_ARGUMENTS_SEPARATOR=,", "XI_DIALECTS_IMGPROXY_ARGUMENTS_SEPARATOR", ","),
		"IMGPROXY_BASE_URL":                       one("IMGPROXY_BASE_URL=https://cdn.example.com/", "XI_DIALECTS_IMGPROXY_BASE_URL", "https://cdn.example.com/"),
		"IMGPROXY_URL_REPLACEMENTS": one("IMGPROXY_URL_REPLACEMENTS=mys3://=s3://;https://*.example.com/=local:///${1}/",
			"XI_DIALECTS_IMGPROXY_URL_REPLACEMENTS", `^mys3://=s3://;^https://([^/]*)\.example\.com/=local:///${1}/`),
		"IMGPROXY_PRESETS_SEPARATOR": {[]string{"IMGPROXY_PRESETS_SEPARATOR=;"}, nil},
		"IMGPROXY_PRESETS": {
			[]string{"IMGPROXY_PRESETS=thumb=rs:fill:200:200; small=w:100", "IMGPROXY_PRESETS_SEPARATOR=;"},
			map[string]string{"XI_PRESETS": "thumb=rs:fill:200:200,small=w:100"},
		},
		"IMGPROXY_PRESETS_PATH":          one("IMGPROXY_PRESETS_PATH=/etc/presets", "XI_PRESETS_FILE", "/etc/presets"),
		"IMGPROXY_ONLY_PRESETS":          one("IMGPROXY_ONLY_PRESETS=true", "XI_PRESETS_ONLY", "true"),
		"IMGPROXY_AUTO_ROTATE":           one("IMGPROXY_AUTO_ROTATE=false", "XI_PROCESSING_AUTO_ROTATE", "false"),
		"IMGPROXY_STRIP_METADATA":        one("IMGPROXY_STRIP_METADATA=false", "XI_PROCESSING_STRIP_METADATA", "false"),
		"IMGPROXY_KEEP_COPYRIGHT":        one("IMGPROXY_KEEP_COPYRIGHT=false", "XI_PROCESSING_KEEP_COPYRIGHT", "false"),
		"IMGPROXY_STRIP_COLOR_PROFILE":   one("IMGPROXY_STRIP_COLOR_PROFILE=false", "XI_PROCESSING_STRIP_COLOR_PROFILE", "false"),
		"IMGPROXY_PRESERVE_HDR":          one("IMGPROXY_PRESERVE_HDR=true", "XI_PROCESSING_PRESERVE_HDR", "true"),
		"IMGPROXY_USE_LINEAR_COLORSPACE": one("IMGPROXY_USE_LINEAR_COLORSPACE=true", "XI_PROCESSING_LINEAR_RESIZE", "true"),
		"IMGPROXY_WATERMARK_OPACITY":     one("IMGPROXY_WATERMARK_OPACITY=0.5", "XI_PROCESSING_WATERMARK_OPACITY", "0.5"),
		"IMGPROXY_WATERMARK_PATH": {
			with(local, "IMGPROXY_WATERMARK_PATH=/data/wm/logo.png", "IMGPROXY_WATERMARK_URL=https://x.example.com/wm.png"),
			plus(localWant, "XI_PROCESSING_WATERMARK_SOURCE", "local:///wm/logo.png"),
		},
		"IMGPROXY_WATERMARK_URL": one("IMGPROXY_WATERMARK_URL=https://x.example.com/wm.png", "XI_PROCESSING_WATERMARK_SOURCE", "https://x.example.com/wm.png"),
		"IMGPROXY_FALLBACK_IMAGE_PATH": {
			with(local, "IMGPROXY_FALLBACK_IMAGE_PATH=/data/fallback.png"),
			plus(localWant, "XI_FALLBACK_SOURCE", "local:///fallback.png"),
		},
		"IMGPROXY_FALLBACK_IMAGE_URL":       one("IMGPROXY_FALLBACK_IMAGE_URL=https://x.example.com/f.png", "XI_FALLBACK_SOURCE", "https://x.example.com/f.png"),
		"IMGPROXY_FALLBACK_IMAGE_HTTP_CODE": one("IMGPROXY_FALLBACK_IMAGE_HTTP_CODE=404", "XI_FALLBACK_STATUS", "404"),
		"IMGPROXY_FORMAT_QUALITY": {
			[]string{"IMGPROXY_FORMAT_QUALITY=jpg=85, webp=70,avif=50,heic=55,jxl=65"},
			map[string]string{"XI_FORMATS_JPEG_QUALITY": "85", "XI_FORMATS_WEBP_QUALITY": "70", "XI_FORMATS_AVIF_QUALITY": "50", "XI_FORMATS_HEIC_QUALITY": "55", "XI_FORMATS_JXL_QUALITY": "65"},
		},
		"IMGPROXY_QUALITY": {
			[]string{"IMGPROXY_QUALITY=90", "IMGPROXY_FORMAT_QUALITY=jpeg=70"},
			map[string]string{"XI_FORMATS_QUALITY": "90", "XI_FORMATS_JPEG_QUALITY": "70", "XI_FORMATS_HEIC_QUALITY": "90"},
		},
		"IMGPROXY_JPEG_PROGRESSIVE":        one("IMGPROXY_JPEG_PROGRESSIVE=false", "XI_FORMATS_JPEG_PROGRESSIVE", "false"),
		"IMGPROXY_PNG_QUANTIZE":            one("IMGPROXY_PNG_QUANTIZE=true", "XI_FORMATS_PNG_QUANTIZE", "true"),
		"IMGPROXY_WEBP_EFFORT":             one("IMGPROXY_WEBP_EFFORT=6", "XI_FORMATS_WEBP_EFFORT", "6"),
		"IMGPROXY_AVIF_SPEED":              one("IMGPROXY_AVIF_SPEED=8", "XI_FORMATS_AVIF_EFFORT", "1"),
		"IMGPROXY_JXL_EFFORT":              one("IMGPROXY_JXL_EFFORT=9", "XI_FORMATS_JXL_EFFORT", "9"),
		"IMGPROXY_AUTO_WEBP":               one("IMGPROXY_AUTO_WEBP=false", "XI_FORMATS_AUTO_WEBP", "false"),
		"IMGPROXY_AUTO_AVIF":               one("IMGPROXY_AUTO_AVIF=false", "XI_FORMATS_AUTO_AVIF", "false"),
		"IMGPROXY_AUTO_JXL":                one("IMGPROXY_AUTO_JXL=true", "XI_FORMATS_AUTO_JXL", "true"),
		"IMGPROXY_ENFORCE_WEBP":            one("IMGPROXY_ENFORCE_WEBP=true", "XI_FORMATS_ENFORCE_WEBP", "true"),
		"IMGPROXY_ENFORCE_AVIF":            one("IMGPROXY_ENFORCE_AVIF=true", "XI_FORMATS_ENFORCE_AVIF", "true"),
		"IMGPROXY_ENFORCE_JXL":             one("IMGPROXY_ENFORCE_JXL=true", "XI_FORMATS_ENFORCE_JXL", "true"),
		"IMGPROXY_PREFERRED_FORMATS":       one("IMGPROXY_PREFERRED_FORMATS=png,jpg", "XI_FORMATS_PREFERRED", "png,jpg"),
		"IMGPROXY_SKIP_PROCESSING_FORMATS": one("IMGPROXY_SKIP_PROCESSING_FORMATS=svg,gif", "XI_FORMATS_SKIP_PROCESSING", "svg,gif"),
		"IMGPROXY_BEST_FORMAT_MAX_RESOLUTION": one("IMGPROXY_BEST_FORMAT_MAX_RESOLUTION=4.5",
			"XI_FORMATS_BEST_MAX_PIXELS", "4500000"),
		"IMGPROXY_BEST_FORMAT_COMPLEXITY_THRESHOLD": one("IMGPROXY_BEST_FORMAT_COMPLEXITY_THRESHOLD=4",
			"XI_FORMATS_BEST_COMPLEXITY_THRESHOLD", "4"),
		"IMGPROXY_ALWAYS_RASTERIZE_SVG":  one("IMGPROXY_ALWAYS_RASTERIZE_SVG=true", "XI_FORMATS_SVG_PASSTHROUGH", "false"),
		"IMGPROXY_ENABLE_CLIENT_HINTS":   one("IMGPROXY_ENABLE_CLIENT_HINTS=true", "XI_CLIENT_HINTS_ENABLED", "true"),
		"IMGPROXY_LOG_LEVEL":             one("IMGPROXY_LOG_LEVEL=debug", "XI_OBSERVABILITY_LOG_LEVEL", "debug"),
		"IMGPROXY_ENABLE_DEBUG_HEADERS":  one("IMGPROXY_ENABLE_DEBUG_HEADERS=true", "XI_OBSERVABILITY_DEBUG_HEADERS", "true"),
		"IMGPROXY_PROMETHEUS_BIND":       one("IMGPROXY_PROMETHEUS_BIND=:9091", "XI_ADMIN_ADDR", ":9091"),
		"IMGPROXY_OPEN_TELEMETRY_ENABLE": one("IMGPROXY_OPEN_TELEMETRY_ENABLE=true", "XI_OTEL_ENABLED", "true"),
	}
}

// TestImgproxyVars runs one case per mapped variable: the translation gives
// the expected XI_* values, only sets declared Targets, and loading the
// IMGPROXY_* environment equals loading the XI_* one.
func TestImgproxyVars(t *testing.T) {
	t.Parallel()
	cases := imgproxyCases()
	vars := imgproxyVars()
	names := map[string]bool{}
	for _, iv := range vars {
		if names[iv.Name] {
			t.Errorf("%s is mapped twice", iv.Name)
		}
		names[iv.Name] = true
		if _, ok := cases[iv.Name]; !ok {
			t.Errorf("%s has no test case", iv.Name)
		}
	}
	for name := range cases {
		if !names[name] {
			t.Errorf("test case %s has no mapping", name)
		}
	}
	targets := map[string][]string{}
	for _, iv := range vars {
		targets[iv.Name] = iv.Targets
	}
	defaults := mustParse(t, nil)

	for _, iv := range vars {
		tc, ok := cases[iv.Name]
		if !ok {
			continue
		}
		t.Run(iv.Name, func(t *testing.T) {
			t.Parallel()
			if !slices.ContainsFunc(tc.env, func(kv string) bool { return strings.HasPrefix(kv, iv.Name+"=") }) {
				t.Fatalf("case does not set %s", iv.Name)
			}
			c := translateImgproxyEnv(tc.env)
			if iv.Targets == nil { // rejected variable
				if !errors.Is(c.err, ErrEnv) || !strings.Contains(c.err.Error(), iv.Name) {
					t.Fatalf("want an error naming %s, got %v", iv.Name, c.err)
				}
				return
			}
			if c.err != nil {
				t.Fatalf("translate: %v", c.err)
			}
			if d := diff(tc.want, c.out); tc.want != nil && d != "" {
				t.Errorf("translation (-want +got):\n%s", d)
			}
			if tc.want == nil && len(c.out) != 0 {
				t.Errorf("want no translation, got %v", c.out)
			}
			var declared []string
			for _, kv := range tc.env {
				k, _, _ := strings.Cut(kv, "=")
				declared = append(declared, targets[k]...)
			}
			for k := range c.out {
				if !slices.Contains(declared, k) {
					t.Errorf("sets %s, which no variable of the case declares as a target", k)
				}
			}
			if tc.want != nil {
				produced := false
				for k := range c.out {
					if slices.Contains(iv.Targets, k) {
						produced = true
					}
				}
				if !produced {
					t.Errorf("%s produced none of its targets %v", iv.Name, iv.Targets)
				}
			}

			got, meta, err := load(opts(nil, append([]string{compatOn}, tc.env...)...))
			if err != nil {
				t.Fatalf("load IMGPROXY_* env: %v", err)
			}
			if len(meta.UnmappedImgproxyEnv) != 0 {
				t.Errorf("unmapped: %v", meta.UnmappedImgproxyEnv)
			}
			xi := []string{compatOn}
			for _, k := range slices.Sorted(maps.Keys(tc.want)) {
				xi = append(xi, k+"="+tc.want[k])
			}
			want := mustParse(t, nil, xi...)
			if d := diff(want, got); d != "" {
				t.Errorf("IMGPROXY_* env and its XI_* translation load differently (-xi +imgproxy):\n%s", d)
			}
			if tc.want != nil {
				if d := diff(defaults, got); d == "" {
					t.Errorf("example value does not change the config; pick a non-default value")
				}
			}
		})
	}
}

// TestImgproxyXIWins proves XI_* variables beat IMGPROXY_* ones.
func TestImgproxyXIWins(t *testing.T) {
	t.Parallel()
	f := files{"/run/secrets/addr": ":7000\n"}
	for _, tc := range []struct {
		name string
		env  []string
		want string
	}{
		{"xi wins", []string{"IMGPROXY_BIND=:9000", "XI_SERVER_ADDR=:8000"}, ":8000"},
		{"xi file wins", []string{"IMGPROXY_BIND=:9000", "XI_SERVER_ADDR_FILE=/run/secrets/addr"}, ":7000"},
		{"empty xi counts as unset", []string{"IMGPROXY_BIND=:9000", "XI_SERVER_ADDR="}, ":9000"},
		{"imgproxy beats yaml", []string{"IMGPROXY_BIND=:9000", "XI_CONFIG_FILE=/c.yaml"}, ":9000"},
		{"order-independent", []string{"XI_SERVER_ADDR=:8000", "IMGPROXY_BIND=:9000"}, ":8000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := maps.Clone(f)
			f["/c.yaml"] = "server: {addr: ':6000'}\n"
			cfg := mustParse(t, f, append([]string{compatOn}, tc.env...)...)
			if cfg.Server.Addr != tc.want {
				t.Errorf("addr = %q, want %q", cfg.Server.Addr, tc.want)
			}
		})
	}

	t.Run("sources merge", func(t *testing.T) {
		t.Parallel()
		cfg := mustParse(t, nil, compatOn, "XI_SOURCES=origin", "XI_SOURCES_ORIGIN_TYPE=http", "XI_SOURCES_ORIGIN_ALLOW=a.example.com",
			"IMGPROXY_ALLOWED_SOURCES=https://b.example.com/", "XI_SOURCES_HTTP_ALLOW=c.example.com")
		if got := slices.Sorted(maps.Keys(cfg.Sources)); !slices.Equal(got, []string{"http", "origin"}) {
			t.Fatalf("sources = %v", got)
		}
		if got := cfg.Sources["http"].Allow; !slices.Equal(got, []string{"c.example.com"}) {
			t.Errorf("XI_SOURCES_HTTP_ALLOW should win, got %v", got)
		}
	})
	t.Run("yaml switch", func(t *testing.T) {
		t.Parallel()
		f := files{"/c.yaml": "compat: {imgproxy_env: true}\n"}
		if cfg := mustParse(t, f, "XI_CONFIG_FILE=/c.yaml", "IMGPROXY_TTL=60"); cfg.Cache.Headers.TTL != time.Minute {
			t.Errorf("compat.imgproxy_env in YAML not honoured: ttl %s", cfg.Cache.Headers.TTL)
		}
		if cfg := mustParse(t, f, "XI_CONFIG_FILE=/c.yaml", envCompatImgproxy+"=false", "IMGPROXY_TTL=60"); cfg.Cache.Headers.TTL == time.Minute {
			t.Error("XI_COMPAT_IMGPROXY_ENV=false should override the file")
		}
	})
}

// TestImgproxyUnmapped proves unmapped IMGPROXY_* variables are reported,
// not fatal, and that nothing is read with the switch off.
func TestImgproxyUnmapped(t *testing.T) {
	t.Parallel()
	env := []string{"IMGPROXY_LOG_FORMAT=json", "IMGPROXY_SENTRY_DSN=x", "IMGPROXY_TTL=60", "IMGPROXY_EMPTY="}
	cfg, meta, err := load(opts(nil, append([]string{compatOn}, env...)...))
	if err != nil {
		t.Fatalf("unmapped variables must not fail: %v", err)
	}
	if d := diff([]string{"IMGPROXY_LOG_FORMAT", "IMGPROXY_SENTRY_DSN"}, meta.UnmappedImgproxyEnv); d != "" {
		t.Errorf("unmapped (-want +got):\n%s", d)
	}
	if cfg.Cache.Headers.TTL != time.Minute {
		t.Errorf("mapped variable not applied")
	}

	cfg, meta, err = load(opts(nil, env...))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Cache.Headers.TTL == time.Minute || meta.UnmappedImgproxyEnv != nil {
		t.Error("IMGPROXY_* read with the switch off")
	}
	if !slices.ContainsFunc(meta.Warnings, func(w string) bool { return strings.Contains(w, envCompatImgproxy) }) {
		t.Errorf("want a warning about ignored IMGPROXY_* variables, got %v", meta.Warnings)
	}
}

// TestImgproxyErrors checks that bad values fail and name the IMGPROXY_*
// variable.
func TestImgproxyErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		env  []string
		want string
	}{
		{[]string{"IMGPROXY_TIMEOUT=10s"}, "IMGPROXY_TIMEOUT"},
		{[]string{"IMGPROXY_KEEP_ALIVE_TIMEOUT=0"}, "IMGPROXY_KEEP_ALIVE_TIMEOUT"},
		{[]string{"IMGPROXY_WORKERS=many"}, "XI_PROCESSING_WORKERS (from IMGPROXY_WORKERS)"},
		{[]string{"IMGPROXY_KEY=zz"}, "from IMGPROXY_KEY"},
		{[]string{"IMGPROXY_MAX_SRC_RESOLUTION=-1"}, "IMGPROXY_MAX_SRC_RESOLUTION"},
		{[]string{"IMGPROXY_AVIF_SPEED=10"}, "IMGPROXY_AVIF_SPEED"},
		{[]string{"IMGPROXY_ALWAYS_RASTERIZE_SVG=maybe"}, "IMGPROXY_ALWAYS_RASTERIZE_SVG"},
		{[]string{"IMGPROXY_ALLOW_PRIVATE_SOURCE_ADDRESSES=maybe"}, "IMGPROXY_ALLOW_PRIVATE_SOURCE_ADDRESSES"},
		{[]string{"IMGPROXY_FORMAT_QUALITY=webp"}, "IMGPROXY_FORMAT_QUALITY"},
		{[]string{"IMGPROXY_FORMAT_QUALITY=webp=0"}, "IMGPROXY_FORMAT_QUALITY"},
		{[]string{"IMGPROXY_PRESETS=thumb"}, "IMGPROXY_PRESETS"},
		{[]string{"IMGPROXY_URL_REPLACEMENTS=nope"}, "IMGPROXY_URL_REPLACEMENTS"},
		{[]string{"IMGPROXY_ALLOWED_SOURCES=example.com"}, "IMGPROXY_ALLOWED_SOURCES"},
		{[]string{"IMGPROXY_ALLOWED_SOURCES=https://example.com/*/img/"}, "IMGPROXY_ALLOWED_SOURCES"},
		{[]string{"IMGPROXY_ALLOWED_SOURCES=s3://bucket/prefix/"}, "IMGPROXY_ALLOWED_SOURCES"},
		{[]string{"IMGPROXY_ALLOWED_SOURCES=swift://c/"}, "IMGPROXY_ALLOWED_SOURCES"},
		{[]string{"IMGPROXY_ALLOWED_SOURCES=local:///a/,local:///b/", "IMGPROXY_LOCAL_FILESYSTEM_ROOT=/data"}, "IMGPROXY_LOCAL_FILESYSTEM_ROOT"},
		{[]string{"IMGPROXY_USE_S3=true", "IMGPROXY_S3_ALLOWED_BUCKETS=a", "IMGPROXY_ALLOWED_SOURCES=s3://b/"}, "IMGPROXY_S3_ALLOWED_BUCKETS"},
		{[]string{"IMGPROXY_USE_S3=yes please"}, "IMGPROXY_USE_S3"},
		{[]string{"IMGPROXY_MAX_CLIENTS=-1"}, "IMGPROXY_MAX_CLIENTS"},
	} {
		t.Run(strings.Join(tc.env, " "), func(t *testing.T) {
			t.Parallel()
			_, _, err := load(opts(nil, append([]string{compatOn}, tc.env...)...))
			if !errors.Is(err, ErrEnv) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("want an ErrEnv naming %q, got %v", tc.want, err)
			}
		})
	}
}

// TestImgproxySources covers the source translations beyond one variable.
func TestImgproxySources(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		env      []string
		want     map[string]string
		warnings []string // substrings
	}{
		{
			name:     "no allowed sources",
			env:      []string{"IMGPROXY_TTL=5"},
			want:     map[string]string{"XI_CACHE_HEADERS_TTL": "5s"},
			warnings: []string{"IMGPROXY_ALLOWED_SOURCES is unset"},
		},
		{
			name:     "allowed sources without s3 keeps s3 closed",
			env:      []string{"IMGPROXY_ALLOWED_SOURCES=https://a.example.com/", "IMGPROXY_USE_S3=true", "IMGPROXY_S3_REGION=x"},
			want:     map[string]string{"XI_SOURCES": "http", "XI_SOURCES_HTTP_TYPE": "http", "XI_SOURCES_HTTP_ALLOW": "a.example.com"},
			warnings: []string{`source "s3" not created`},
		},
		{
			name: "bucket intersection",
			env:  []string{"IMGPROXY_ALLOWED_SOURCES=s3://a/,s3://b/", "IMGPROXY_USE_S3=true", "IMGPROXY_S3_ALLOWED_BUCKETS=b,c"},
			want: map[string]string{"XI_SOURCES": "s3", "XI_SOURCES_S3_TYPE": "s3", "XI_SOURCES_S3_BUCKETS": "b"},
		},
		{
			name: "use_s3 false",
			env:  []string{"IMGPROXY_USE_S3=false", "IMGPROXY_S3_REGION=x", "IMGPROXY_S3_ENDPOINT=https://e"},
			want: map[string]string{},
		},
		{
			name: "explicit path style wins over endpoint default",
			env:  []string{"IMGPROXY_USE_S3=true", "IMGPROXY_S3_ALLOWED_BUCKETS=a", "IMGPROXY_S3_ENDPOINT=https://e.example.com", "IMGPROXY_S3_ENDPOINT_USE_PATH_STYLE=false"},
			want: map[string]string{"XI_SOURCES": "s3", "XI_SOURCES_S3_TYPE": "s3", "XI_SOURCES_S3_BUCKETS": "a", "XI_SOURCES_S3_ENDPOINT": "https://e.example.com", "XI_SOURCES_S3_FORCE_PATH_STYLE": "false"},
		},
		{
			name: "local root allowed whole",
			env:  []string{"IMGPROXY_LOCAL_FILESYSTEM_ROOT=/data", "IMGPROXY_ALLOWED_SOURCES=local://"},
			want: map[string]string{"XI_SOURCES": "local", "XI_SOURCES_LOCAL_TYPE": "fs", "XI_SOURCES_LOCAL_ROOT": "/data"},
		},
		{
			name:     "watermark outside root",
			env:      []string{"IMGPROXY_ALLOWED_SOURCES=https://a.example.com/", "IMGPROXY_WATERMARK_PATH=/etc/wm.png"},
			want:     map[string]string{"XI_SOURCES": "http", "XI_SOURCES_HTTP_TYPE": "http", "XI_SOURCES_HTTP_ALLOW": "a.example.com"},
			warnings: []string{"IMGPROXY_WATERMARK_PATH"},
		},
		{
			name:     "format quality without per-format setting",
			env:      []string{"IMGPROXY_ALLOWED_SOURCES=https://a.example.com/", "IMGPROXY_FORMAT_QUALITY=png=50"},
			want:     map[string]string{"XI_SOURCES": "http", "XI_SOURCES_HTTP_TYPE": "http", "XI_SOURCES_HTTP_ALLOW": "a.example.com"},
			warnings: []string{"png has no per-format quality"},
		},
		{
			name: "networks merge",
			env:  []string{"IMGPROXY_ALLOW_LOOPBACK_SOURCE_ADDRESSES=true", "IMGPROXY_ALLOW_LINK_LOCAL_SOURCE_ADDRESSES=true"},
			want: map[string]string{"XI_FETCH_ALLOWED_NETWORKS": "127.0.0.0/8,::1/128,169.254.0.0/16,fe80::/10"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := translateImgproxyEnv(tc.env)
			if c.err != nil {
				t.Fatal(c.err)
			}
			if d := diff(tc.want, c.out); d != "" {
				t.Errorf("translation (-want +got):\n%s", d)
			}
			for _, w := range tc.warnings {
				if !slices.ContainsFunc(c.warnings, func(s string) bool { return strings.Contains(s, w) }) {
					t.Errorf("no warning containing %q in %v", w, c.warnings)
				}
			}
			if _, _, err := load(opts(nil, append([]string{compatOn}, tc.env...)...)); err != nil {
				t.Errorf("load: %v", err)
			}
		})
	}
}

// TestImgproxyMigration loads a typical imgproxy deployment end to end,
// including Validate.
func TestImgproxyMigration(t *testing.T) {
	t.Parallel()
	cfg, meta, err := Load(opts(nil, compatOn,
		"IMGPROXY_KEY="+hexA, "IMGPROXY_SALT="+hexB,
		"IMGPROXY_ALLOWED_SOURCES=https://cdn.example.com/,s3://assets/",
		"IMGPROXY_USE_S3=true", "IMGPROXY_S3_REGION=auto", "IMGPROXY_S3_ENDPOINT=https://acct.r2.cloudflarestorage.com",
		"IMGPROXY_TTL=86400", "IMGPROXY_MAX_SRC_RESOLUTION=40", "IMGPROXY_QUALITY=85",
		"IMGPROXY_PRESETS=default=q:80,thumb=rs:fill:200:200", "IMGPROXY_ENABLE_WEBP_DETECTION=true",
	))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Signing.Keys) != 1 || cfg.Sources["s3"].Region != "auto" || !cfg.Sources["s3"].ForcePathStyle ||
		cfg.Limits.MaxSrcPixels != 40_000_000 || cfg.Formats.JPEG.Quality != 85 || cfg.Presets["thumb"] == "" ||
		!slices.Equal(cfg.Sources["http"].Allow, []string{"cdn.example.com"}) {
		t.Errorf("migration not applied: %s", cfg.Redacted())
	}
	if d := diff([]string{"IMGPROXY_ENABLE_WEBP_DETECTION"}, meta.UnmappedImgproxyEnv); d != "" {
		t.Errorf("unmapped (-want +got):\n%s", d)
	}
}
