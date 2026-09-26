package config

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// valid returns a loaded, valid config for Validate table tests.
func valid(t *testing.T) Config {
	t.Helper()
	cfg, _, err := Load(opts(nil, with()...))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func TestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*Config)
		want   []string // substrings; empty means valid
	}{
		{"valid", func(*Config) {}, nil},
		{"dev mode without sources", func(c *Config) { c.Sources, c.DevMode = nil, true }, nil},
		{"no sources", func(c *Config) { c.Sources = nil }, []string{"sources (XI_SOURCES): no sources configured"}},
		{"zero timeout", func(c *Config) { c.Server.ReadTimeout = 0 }, []string{"server.read_timeout (XI_SERVER_READ_TIMEOUT): must be > 0"}},
		{"write below request", func(c *Config) { c.Server.WriteTimeout = time.Second }, []string{"server.write_timeout (XI_SERVER_WRITE_TIMEOUT): must be >= server.request_timeout"}},
		{"path prefix", func(c *Config) { c.Server.PathPrefix = "img" }, []string{"server.path_prefix (XI_SERVER_PATH_PREFIX): must start with /"}},
		{"ready threshold", func(c *Config) { c.Server.ReadyQueueThreshold = 101 }, []string{"server.ready_queue_threshold"}},
		{"cors origin", func(c *Config) { c.Server.CORSOrigins = []string{"*", "example.com"} }, []string{`server.cors_origins[1]`}},
		{"pprof without admin", func(c *Config) { c.Admin.Pprof, c.Admin.Addr = true, "" }, []string{"admin.pprof (XI_ADMIN_PPROF): needs admin.addr"}},
		{"admin same as server", func(c *Config) { c.Admin.Addr = c.Server.Addr }, []string{"admin.addr"}},
		{"keys without salts", func(c *Config) { c.Signing.Keys = []HexKey{{1}} }, []string{"signing.salts (XI_SIGNING_SALTS): want one salt per key: 1 keys, 0 salts"}},
		{"signature size", func(c *Config) { c.Signing.Size = 33 }, []string{"signing.size"}},
		{"required without keys", func(c *Config) { c.Signing.Required = True }, []string{"signing.required (XI_SIGNING_REQUIRED): is true but no signing.keys"}},
		{"encryption key length", func(c *Config) { c.Encryption.Keys = []HexKey{make(HexKey, 32), make(HexKey, 10)} }, []string{"encryption.keys[1] (XI_ENCRYPTION_KEYS): AES key must be 16, 24 or 32 bytes, got 10"}},
		{"width above max dimension", func(c *Config) { c.Security.AllowedWidths = IntList{100, 9000} }, []string{"security.allowed_widths"}},
		{"empty widths when restricted", func(c *Config) { c.Security.AllowedWidths = nil }, []string{"must not be empty with the restricted policy"}},
		{"empty widths when allow", func(c *Config) { c.Security.AllowedWidths, c.Security.UnsignedPolicy = nil, UnsignedAllow }, nil},
		{"quality 0", func(c *Config) { c.Security.AllowedQualities = IntList{0} }, []string{"security.allowed_qualities"}},
		{"raising limits alone", func(c *Config) { c.Security.AllowRaisingLimits = true }, []string{"security.allow_raising_limits"}},
		{"source name", func(c *Config) { c.Sources["Bad_Name"] = Source{Type: SourceFS, Root: "/x"} }, []string{"sources.Bad_Name"}},
		{"source without type", func(c *Config) { c.Sources["x"] = Source{} }, []string{"sources.x.type (XI_SOURCES_X_TYPE): must be set"}},
		{"http without allow", func(c *Config) { c.Sources["web"] = Source{Type: SourceHTTP} }, []string{"sources.web.allow"}},
		{"http allow with scheme", func(c *Config) { c.Sources["web"] = Source{Type: SourceHTTP, Allow: []string{"https://a.example"}} }, []string{"sources.web.allow[0]"}},
		{
			"field of another type", func(c *Config) {
				c.Sources["web"] = Source{Type: SourceHTTP, Allow: []string{"a"}, Root: "/x", Bucket: "b"}
			},
			[]string{"sources.web.bucket (XI_SOURCES_WEB_BUCKET): does not apply to type http", "sources.web.root"},
		},
		{"s3 without buckets", func(c *Config) { c.Sources["s"] = Source{Type: SourceS3} }, []string{"sources.s.buckets"}},
		{"s3 bucket not in list", func(c *Config) { c.Sources["s"] = Source{Type: SourceS3, Buckets: []string{"a"}, Bucket: "b"} }, []string{`sources.s.bucket (XI_SOURCES_S_BUCKET): "b" is not in buckets`}},
		{"s3 endpoint", func(c *Config) { c.Sources["s"] = Source{Type: SourceS3, Bucket: "b", Endpoint: "minio:9000"} }, []string{"sources.s.endpoint"}},
		{"fs relative root", func(c *Config) { c.Sources["f"] = Source{Type: SourceFS, Root: "data"} }, []string{"sources.f.root"}},
		{"sftp host key", func(c *Config) { c.Sources["f"] = Source{Type: SourceSFTP, Host: "h"} }, []string{"sources.f.host_key (XI_SOURCES_F_HOST_KEY): is mandatory"}},
		{"ports", func(c *Config) { c.Fetch.AllowedPorts = []int{0} }, []string{"fetch.allowed_ports"}},
		{"default dialect disabled", func(c *Config) { c.Dialects.Default = DialectImgix }, []string{"dialects.default (XI_DIALECTS_DEFAULT): dialect imgix is not enabled"}},
		{"mount source missing", func(c *Config) { c.Dialects.Imgix.Source = "nope" }, []string{`dialects.imgix.source (XI_DIALECTS_IMGIX_SOURCE): source "nope" is not configured`}},
		{"mount prefix", func(c *Config) { c.Dialects.Query.Prefix = "q/" }, []string{"dialects.query.prefix"}},
		{"extra mount", func(c *Config) { c.Dialects.Imgix.Mounts = []MountOptions{{Source: "origin"}} }, []string{"dialects.imgix.mounts[0]"}},
		{"host with scheme", func(c *Config) { c.Dialects.Imgix.Hosts = []string{"https://x"} }, []string{"dialects.imgix.hosts[0]"}},
		{"bad replacement regex", func(c *Config) { c.Dialects.Imgproxy.URLReplacements = []string{"(=x"} }, []string{"dialects.imgproxy.url_replacements[0]"}},
		{"cloud source", func(c *Config) { c.Dialects.Cloudinary.Clouds = map[string]string{"demo": "nope"} }, []string{"dialects.cloudinary.clouds"}},
		{"presets only without presets", func(c *Config) { c.PresetsOnly = true }, []string{"presets_only (XI_PRESETS_ONLY)"}},
		{"workers", func(c *Config) { c.Processing.Workers = 0 }, []string{"processing.workers"}},
		{"go memory fraction", func(c *Config) { c.Processing.GoMemoryFraction = 1.5 }, []string{"processing.go_memory_fraction"}},
		{"fallback status", func(c *Config) { c.Fallback.Status = 99 }, []string{"fallback.status"}},
		{"quality", func(c *Config) { c.Formats.AVIF.Quality = 0 }, []string{"formats.avif.quality (XI_FORMATS_AVIF_QUALITY)"}},
		{"effort", func(c *Config) { c.Formats.WebP.Effort = 7 }, []string{"formats.webp.effort"}},
		{"autoquality order", func(c *Config) { c.Formats.AutoQuality.Min = 90 }, []string{"formats.autoquality.min (XI_FORMATS_AUTOQUALITY_MIN): must be <= formats.autoquality.max"}},
		{"limits", func(c *Config) { c.Limits.MaxSVGBytes = 0 }, []string{"limits.max_svg_bytes (XI_LIMITS_MAX_SVG_BYTES): must be > 0"}},
		{"duplicate store", func(c *Config) { c.Cache.Result.Stores = []StoreKind{StoreMemory, StoreMemory} }, []string{"memory listed twice"}},
		{"disk store without path", func(c *Config) { c.Cache.Result.Stores = []StoreKind{StoreDisk} }, []string{"cache.result.disk.path (XI_CACHE_RESULT_DISK_PATH)"}},
		{"redis store without url", func(c *Config) { c.Cache.Result.Stores = []StoreKind{StoreRedis} }, []string{"cache.result.redis.url"}},
		{"s3 store on http source", func(c *Config) {
			c.Cache.Result.Stores = []StoreKind{StoreS3}
			c.Cache.Result.S3 = S3ResultCache{Source: "origin", Bucket: "b"}
		}, []string{`cache.result.s3.source (XI_CACHE_RESULT_S3_SOURCE): must name an s3 source, got "origin"`}},
		{"every violation reported", func(c *Config) {
			c.Server.Addr, c.Limits.MaxPDFPages, c.Vips.Concurrency = "", 0, 0
		}, []string{"server.addr", "limits.max_pdf_pages", "vips.concurrency"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := valid(t)
			tc.mutate(&cfg)
			err := cfg.Validate()
			if len(tc.want) == 0 {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("err = %v\nwant it to contain %q", err, w)
				}
			}
		})
	}
}
