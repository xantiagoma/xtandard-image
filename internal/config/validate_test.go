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
		{"docs without admin", func(c *Config) { c.Admin.Docs, c.Admin.Addr = true, "" }, []string{"admin.docs (XI_ADMIN_DOCS): needs admin.addr"}},
		{"admin same as server", func(c *Config) { c.Admin.Addr = c.Server.Addr }, []string{"admin.addr"}},
		{"playground enabled", func(c *Config) { c.Playground.Enabled = true }, nil},
		{"playground public without admin", func(c *Config) { c.Playground.Enabled, c.Playground.Public, c.Admin.Addr = true, true, "" }, nil},
		{"playground without admin", func(c *Config) { c.Playground.Enabled, c.Admin.Addr = true, "" }, []string{"playground.enabled (XI_PLAYGROUND_ENABLED): needs admin.addr"}},
		{"playground public without enabled", func(c *Config) { c.Playground.Public = true }, []string{"playground.public (XI_PLAYGROUND_PUBLIC): needs playground.enabled"}},
		{"playground uploads without enabled", func(c *Config) { c.Playground.Uploads = True }, []string{"playground.uploads (XI_PLAYGROUND_UPLOADS): needs playground.enabled"}},
		{"playground uploads", func(c *Config) { c.Playground.Enabled, c.Playground.Uploads = true, True }, nil},
		{"playground trailing slash", func(c *Config) { c.Playground.Path = "/play/" }, []string{"playground.path (XI_PLAYGROUND_PATH)"}},
		{"playground root", func(c *Config) { c.Playground.Path = "/" }, []string{"playground.path"}},
		{"playground bad char", func(c *Config) { c.Playground.Path = "/play?x" }, []string{"playground.path"}},
		{"playground shadows admin", func(c *Config) { c.Playground.Path = "/debug/play" }, []string{"shadows the admin route /debug"}},
		{"playground nested path", func(c *Config) { c.Playground.Path = "/tools/playground" }, nil},
		{"keys without salts", func(c *Config) { c.Signing.Keys = []HexKey{{1}} }, []string{"signing.salts (XI_SIGNING_SALTS): want one salt per key: 1 keys, 0 salts"}},
		{"signature size", func(c *Config) { c.Signing.Size = 33 }, []string{"signing.size"}},
		{"required without keys", func(c *Config) { c.Signing.Required = True }, []string{"signing.required (XI_SIGNING_REQUIRED): is true but no signing.keys"}},
		{"encryption key length", func(c *Config) { c.Encryption.Keys = []HexKey{make(HexKey, 32), make(HexKey, 10)} }, []string{"encryption.keys[1] (XI_ENCRYPTION_KEYS): AES key must be 16, 24 or 32 bytes, got 10"}},
		{"width above max dimension", func(c *Config) { c.Security.AllowedWidths = IntList{100, 9000} }, []string{"security.allowed_widths"}},
		{"empty widths when restricted", func(c *Config) { c.Security.AllowedWidths = nil }, []string{"must not be empty with the restricted policy"}},
		{"empty widths when allow", func(c *Config) { c.Security.AllowedWidths, c.Security.UnsignedPolicy = nil, UnsignedAllow }, nil},
		{"quality 0", func(c *Config) { c.Security.AllowedQualities = IntList{0} }, []string{"security.allowed_qualities"}},
		{"height above max dimension", func(c *Config) { c.Security.AllowedHeights = IntList{9000} }, []string{"security.allowed_heights (XI_SECURITY_ALLOWED_HEIGHTS)"}},
		{"dpr 0", func(c *Config) { c.Security.AllowedDPRs = []float64{1, 0} }, []string{"security.allowed_dprs (XI_SECURITY_ALLOWED_DPRS)"}},
		{"dpr 9", func(c *Config) { c.Security.AllowedDPRs = []float64{9} }, []string{"security.allowed_dprs"}},
		{"dprs ok", func(c *Config) { c.Security.AllowedDPRs = []float64{1, 1.5, 2, 3} }, nil},
		{"negative ttl", func(c *Config) { c.Cache.Origin.NegativeTTL = -time.Second }, []string{"cache.origin.negative_ttl (XI_CACHE_ORIGIN_NEGATIVE_TTL)"}},
		{"gcs and azure endpoints", func(c *Config) {
			c.Sources["g"] = Source{Type: SourceGCS, Bucket: "b", Endpoint: "http://fake-gcs:4443/storage/v1/"}
			c.Sources["a"] = Source{Type: SourceAzure, Bucket: "b", Endpoint: "http://127.0.0.1:10000/devstoreaccount1", SASToken: "sv=1"}
		}, nil},
		{"azure and fs fields elsewhere", func(c *Config) {
			c.Sources["s"] = Source{Type: SourceS3, Bucket: "b", SASToken: "x", FollowSymlinks: true}
		}, []string{"sources.s.sas_token (XI_SOURCES_S_SAS_TOKEN): does not apply to type s3", "sources.s.follow_symlinks"}},
		{"raising limits alone", func(c *Config) { c.Security.AllowRaisingLimits = true }, []string{"security.allow_raising_limits"}},
		{"source name", func(c *Config) { c.Sources["Bad_Name"] = Source{Type: SourceFS, Root: "/x"} }, []string{"sources.Bad_Name"}},
		{"source without type", func(c *Config) { c.Sources["x"] = Source{} }, []string{"sources.x.type (XI_SOURCES_X_TYPE): must be set"}},
		{"detect threads", func(c *Config) { c.Detect.Threads = 0 }, []string{"detect.threads (XI_DETECT_THREADS): must be between 1 and 64"}},
		{"detect max input", func(c *Config) { c.Detect.MaxInput = 8 }, []string{"detect.max_input (XI_DETECT_MAX_INPUT)"}},
		{"detect model path", func(c *Config) { c.Detect.FaceModel = "sub/face.onnx" }, []string{"detect.face_model (XI_DETECT_FACE_MODEL): must be a file name"}},
		{"detect relative dir", func(c *Config) { c.Detect.ModelsDir = "models" }, []string{"detect.models_dir (XI_DETECT_MODELS_DIR): must be an absolute path"}},
		{"detect enabled without models", func(c *Config) {
			c.Detect.Enabled, c.Detect.FaceModel, c.Detect.ObjectModel, c.Detect.Classify.Model = True, "", "", ""
		}, []string{"detect.enabled (XI_DETECT_ENABLED)"}},
		{"detect classify only", func(c *Config) { c.Detect.Enabled, c.Detect.FaceModel, c.Detect.ObjectModel = True, "", "" }, nil},
		{"detect classify settings", func(c *Config) {
			c.Detect.Classify.Labels, c.Detect.Classify.Normalization = "labels.txt", "bgr"
			c.Detect.Classify.TopKMax, c.Detect.Classify.Threshold = 65, 1.5
		}, []string{
			"detect.classify.labels (XI_DETECT_CLASSIFY_LABELS)", "detect.classify.normalization (XI_DETECT_CLASSIFY_NORMALIZATION)",
			"detect.classify.top_k_max (XI_DETECT_CLASSIFY_TOP_K_MAX)", "detect.classify.threshold (XI_DETECT_CLASSIFY_THRESHOLD)",
		}},
		{"detect off skips checks", func(c *Config) { c.Detect.Enabled, c.Detect.Threads, c.Detect.Library = False, 0, "" }, nil},
		{"detect absolute model", func(c *Config) {
			c.Detect.ObjectModel, c.Detect.FaceModel, c.Detect.ModelsDir = "/models/x.onnx", "", ""
			c.Detect.Classify.Model = "/models/c.onnx"
		}, nil},
		{"http without allow", func(c *Config) { c.Sources["web"] = Source{Type: SourceHTTP} }, []string{"sources.web.allow"}},
		{"http allow with http(s) scheme", func(c *Config) {
			c.Sources["web"] = Source{Type: SourceHTTP, Allow: []string{"https://a.example", "HTTP://b.example:8080/img/", "https://*"}}
		}, nil},
		{"http allow with another scheme", func(c *Config) {
			c.Sources["web"] = Source{Type: SourceHTTP, Allow: []string{"a.example", "ftp://a.example", "https://", "https:///x", "https://https://a"}}
		}, []string{"sources.web.allow[1]", "sources.web.allow[2]", "sources.web.allow[3]", "sources.web.allow[4]"}},
		{
			"field of another type", func(c *Config) {
				c.Sources["web"] = Source{Type: SourceHTTP, Allow: []string{"a"}, Root: "/x", Bucket: "b"}
			},
			[]string{"sources.web.bucket (XI_SOURCES_WEB_BUCKET): does not apply to type http", "sources.web.root"},
		},
		{"s3 without buckets", func(c *Config) { c.Sources["s"] = Source{Type: SourceS3} }, []string{"sources.s.buckets"}},
		{"s3 bucket not in list", func(c *Config) { c.Sources["s"] = Source{Type: SourceS3, Buckets: []string{"a"}, Bucket: "b"} }, []string{`sources.s.bucket (XI_SOURCES_S_BUCKET): "b" is not in buckets`}},
		{"s3 endpoint", func(c *Config) { c.Sources["s"] = Source{Type: SourceS3, Bucket: "b", Endpoint: "rustfs:9000"} }, []string{"sources.s.endpoint"}},
		{"fs relative root", func(c *Config) { c.Sources["f"] = Source{Type: SourceFS, Root: "data"} }, []string{"sources.f.root"}},
		{"sftp host key", func(c *Config) { c.Sources["f"] = Source{Type: SourceSFTP, Host: "h"} }, []string{"sources.f.host_key (XI_SOURCES_F_HOST_KEY): is mandatory"}},
		{"ftp pool and timeouts", func(c *Config) {
			c.Sources["f"] = Source{Type: SourceFTP, Host: "h", Root: "/pub", PoolSize: -1, DialTimeout: -time.Second, IdleTimeout: -time.Second}
		}, []string{"sources.f.pool_size (XI_SOURCES_F_POOL_SIZE)", "sources.f.dial_timeout", "sources.f.idle_timeout"}},
		{"passphrase without key", func(c *Config) {
			c.Sources["f"] = Source{Type: SourceSFTP, Host: "h", HostKey: "k", Password: "p", PrivateKeyPassphrase: "x"}
		}, []string{"sources.f.private_key_passphrase (XI_SOURCES_F_PRIVATE_KEY_PASSPHRASE): needs private_key"}},
		{"passphrase on ftp", func(c *Config) {
			c.Sources["f"] = Source{Type: SourceFTP, Host: "h", PrivateKey: "k", PrivateKeyPassphrase: "x"}
		}, []string{"sources.f.private_key_passphrase (XI_SOURCES_F_PRIVATE_KEY_PASSPHRASE): does not apply to type ftp"}},
		{"pdf password with a control character", func(c *Config) {
			c.Sources["f"] = Source{Type: SourceFS, Root: "/data", PDFPassword: "a\nb"}
		}, []string{"sources.f.pdf_password (XI_SOURCES_F_PDF_PASSWORD): must be at most 1024 bytes without control characters"}},
		{"pdf password too long", func(c *Config) {
			c.Sources["f"] = Source{Type: SourceFS, Root: "/data", PDFPassword: Secret(strings.Repeat("x", 1025))}
		}, []string{"sources.f.pdf_password"}},
		{"ports", func(c *Config) { c.Fetch.AllowedPorts = []int{0} }, []string{"fetch.allowed_ports"}},
		{"default dialect disabled", func(c *Config) { c.Dialects.Default = DialectImgix }, []string{"dialects.default (XI_DIALECTS_DEFAULT): dialect imgix is not enabled"}},
		{"mount source missing", func(c *Config) { c.Dialects.Imgix.Source = "nope" }, []string{`dialects.imgix.source (XI_DIALECTS_IMGIX_SOURCE): source "nope" is not configured`}},
		{"mount prefix", func(c *Config) { c.Dialects.Query.Prefix = "q/" }, []string{"dialects.query.prefix"}},
		{"extra mount", func(c *Config) { c.Dialects.Imgix.Mounts = []MountOptions{{Source: "origin"}} }, []string{"dialects.imgix.mounts[0]"}},
		{"host with scheme", func(c *Config) { c.Dialects.Imgix.Hosts = []string{"https://x"} }, []string{"dialects.imgix.hosts[0]"}},
		{"bad replacement regex", func(c *Config) { c.Dialects.Imgproxy.URLReplacements = []string{"(=x"} }, []string{"dialects.imgproxy.url_replacements[0]"}},
		{"imagekit endpoint path", func(c *Config) { c.Dialects.Imagekit.EndpointPath = "/demo?x" }, []string{"dialects.imagekit.endpoint_path (XI_DIALECTS_IMAGEKIT_ENDPOINT_PATH): must be a plain path"}},
		{"cloud source", func(c *Config) { c.Dialects.Cloudinary.Clouds = map[string]string{"demo": "nope"} }, []string{"dialects.cloudinary.clouds"}},
		{"presets only without presets", func(c *Config) { c.PresetsOnly = true }, []string{"presets_only (XI_PRESETS_ONLY)"}},
		{"workers", func(c *Config) { c.Processing.Workers = 0 }, []string{"processing.workers"}},
		{"go memory fraction", func(c *Config) { c.Processing.GoMemoryFraction = 1.5 }, []string{"processing.go_memory_fraction"}},
		{"fallback status", func(c *Config) { c.Fallback.Status = 99 }, []string{"fallback.status"}},
		{"quality", func(c *Config) { c.Formats.AVIF.Quality = 0 }, []string{"formats.avif.quality (XI_FORMATS_AVIF_QUALITY)"}},
		{"effort", func(c *Config) { c.Formats.WebP.Effort = 7 }, []string{"formats.webp.effort"}},
		{"hdr tonemap", func(c *Config) { c.Formats.HDR.ToneMap = "filmic" }, []string{`formats.hdr.tonemap (XI_FORMATS_HDR_TONEMAP): must be reinhard, aces or clip, got "filmic"`}},
		{"autoquality order", func(c *Config) { c.Formats.AutoQuality.Min = 90 }, []string{"formats.autoquality.min (XI_FORMATS_AUTOQUALITY_MIN): must be <= formats.autoquality.max"}},
		{"autoquality size target", func(c *Config) { c.Formats.AutoQuality.Method = AutoQualitySize }, []string{"formats.autoquality.target (XI_FORMATS_AUTOQUALITY_TARGET): must be a byte count"}},
		{"autoquality allowed error", func(c *Config) { c.Formats.AutoQuality.AllowedError = 2 }, []string{"formats.autoquality.allowed_error"}},
		{"autoquality per-format order", func(c *Config) { c.Formats.AutoQuality.FormatMin[FormatAVIF] = 85 }, []string{"formats.autoquality.format_min (XI_FORMATS_AUTOQUALITY_FORMAT_MIN): avif minimum 85 is above its maximum 80"}},
		{"autoquality per-format lossless", func(c *Config) { c.Formats.AutoQuality.FormatMax = map[Format]int{FormatPNG: 80} }, []string{`formats.autoquality.format_max (XI_FORMATS_AUTOQUALITY_FORMAT_MAX): "png" is not a lossy`}},
		{"autoquality per-format target", func(c *Config) { c.Formats.AutoQuality.FormatTarget = map[Format]float64{FormatJXL: 0} }, []string{"formats.autoquality.format_target"}},
		{"limits", func(c *Config) { c.Limits.MaxSVGBytes = 0 }, []string{"limits.max_svg_bytes (XI_LIMITS_MAX_SVG_BYTES): must be > 0"}},
		{"sandbox address space off", func(c *Config) { c.Sandbox.MaxAS = 0 }, nil},
		{"sandbox address space small", func(c *Config) { c.Sandbox.MaxAS = 1 << 20 }, []string{"sandbox.max_as (XI_SANDBOX_MAX_AS): must be 0 (no limit) or in [64MiB, 1TiB], got 1MiB"}},
		{"video limits", func(c *Config) { c.Video.MaxDuration, c.Video.Thumbnail.Samples = 0, 33 }, []string{"video.max_duration (XI_VIDEO_MAX_DURATION): must be > 0", "video.thumbnail.samples"}},
		{"video second", func(c *Config) { c.Video.Thumbnail.Second = "soon" }, []string{`video.thumbnail.second (XI_VIDEO_THUMBNAIL_SECOND): "soon" is not a number`}},
		{"video encode", func(c *Config) {
			c.Video.MaxAnimationFrames, c.Video.Encode.VP9CRF, c.Video.Encode.H264CRF = 0, 64, 52
			c.Video.Encode.H264Preset, c.Video.Encode.MaxDuration = "turbo", 0
		}, []string{
			"video.max_animation_frames", "video.encode.vp9_crf", "video.encode.h264_crf",
			`video.encode.h264_preset (XI_VIDEO_ENCODE_H264_PRESET): "turbo" is not an x264/x265 preset`,
			"video.encode.max_duration (XI_VIDEO_ENCODE_MAX_DURATION): must be > 0",
		}},
		{"duplicate store", func(c *Config) { c.Cache.Result.Stores = []StoreKind{StoreMemory, StoreMemory} }, []string{"memory listed twice"}},
		{"disk store without path", func(c *Config) { c.Cache.Result.Stores = []StoreKind{StoreDisk} }, []string{"cache.result.disk.path (XI_CACHE_RESULT_DISK_PATH)"}},
		{"redis store without url", func(c *Config) { c.Cache.Result.Stores = []StoreKind{StoreRedis} }, []string{"cache.result.redis.url"}},
		{"origin store without backend config", func(c *Config) { c.Cache.Origin.Stores = []StoreKind{StoreDisk, StoreRedis} }, []string{"cache.result.disk.path", "cache.result.redis.url"}},
		{"origin memory store", func(c *Config) { c.Cache.Origin.Stores = []StoreKind{StoreMemory} }, []string{"cache.origin.stores (XI_CACHE_ORIGIN_STORES): memory is the built-in origin LRU"}},
		{"origin store ttl", func(c *Config) {
			c.Cache.Origin.Stores = []StoreKind{StoreRedis}
			c.Cache.Result.Redis.URL = "redis://r:6379"
			c.Cache.Origin.StoreTTL = 0
		}, []string{"cache.origin.store_ttl (XI_CACHE_ORIGIN_STORE_TTL): must be > 0"}},
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
