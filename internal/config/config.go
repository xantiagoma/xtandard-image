// Package config loads the one Config struct of xtandard-image: built-in
// defaults, then an optional YAML file (goccy/go-yaml, strict), then XI_*
// environment variables (caarlos0/env/v11; env wins, lists replace), with a
// _FILE variant for every variable. See docs/CONFIG.md.
//
// Mapping rule: a variable is its YAML path upper-cased with dots replaced
// by underscores and an XI_ prefix (limits.max_src_bytes ↔
// XI_LIMITS_MAX_SRC_BYTES). Named sources are the only dynamic part:
// sources.<name>.<field> ↔ XI_SOURCES_<NAME>_<FIELD>.
//
// config imports nothing internal except errs (docs/ARCHITECTURE.md §3);
// internal/app maps Config into each component's own Options.
package config

import "time"

// Config is the whole service configuration. Build it with Load.
type Config struct {
	DevMode bool `yaml:"dev_mode" env:"DEV_MODE"`

	Server     Server     `yaml:"server"     envPrefix:"SERVER_"`
	Admin      Admin      `yaml:"admin"      envPrefix:"ADMIN_"`
	Signing    Signing    `yaml:"signing"    envPrefix:"SIGNING_"`
	Encryption Encryption `yaml:"encryption" envPrefix:"ENCRYPTION_"`
	Security   Security   `yaml:"security"   envPrefix:"SECURITY_"`

	// Sources are the allowlist, keyed by name ([a-z0-9-]). In env, XI_SOURCES
	// lists the names and XI_SOURCES_<NAME>_<FIELD> sets their fields.
	Sources map[string]Source `yaml:"sources" env:"-"`
	Fetch   Fetch             `yaml:"fetch"   envPrefix:"FETCH_"`

	Dialects Dialects `yaml:"dialects" envPrefix:"DIALECTS_"`
	// Presets maps a preset name to imgproxy processing options
	// (XI_PRESETS="name=opts,name2=opts").
	Presets     map[string]string `yaml:"presets"      env:"PRESETS" envKeyValSeparator:"="`
	PresetsFile string            `yaml:"presets_file" env:"PRESETS_FILE"`
	PresetsOnly bool              `yaml:"presets_only" env:"PRESETS_ONLY"`

	Processing    Processing    `yaml:"processing"    envPrefix:"PROCESSING_"`
	Fallback      Fallback      `yaml:"fallback"      envPrefix:"FALLBACK_"`
	Formats       Formats       `yaml:"formats"       envPrefix:"FORMATS_"`
	ClientHints   ClientHints   `yaml:"client_hints"  envPrefix:"CLIENT_HINTS_"`
	Limits        Limits        `yaml:"limits"        envPrefix:"LIMITS_"`
	Vips          Vips          `yaml:"vips"          envPrefix:"VIPS_"`
	Cache         Cache         `yaml:"cache"         envPrefix:"CACHE_"`
	Observability Observability `yaml:"observability" envPrefix:"OBSERVABILITY_"`
	OTel          OTel          `yaml:"otel"          envPrefix:"OTEL_"`
	Compat        Compat        `yaml:"compat"        envPrefix:"COMPAT_"`
}

// Server configures the public listener (docs/go-standards.md §2).
type Server struct {
	Addr              string        `yaml:"addr"                env:"ADDR"`
	PathPrefix        string        `yaml:"path_prefix"         env:"PATH_PREFIX"`
	RequestTimeout    time.Duration `yaml:"request_timeout"     env:"REQUEST_TIMEOUT"`
	ReadHeaderTimeout time.Duration `yaml:"read_header_timeout" env:"READ_HEADER_TIMEOUT"`
	ReadTimeout       time.Duration `yaml:"read_timeout"        env:"READ_TIMEOUT"`
	// WriteTimeout defaults to RequestTimeout + 5s when zero.
	WriteTimeout        time.Duration `yaml:"write_timeout"         env:"WRITE_TIMEOUT"`
	IdleTimeout         time.Duration `yaml:"idle_timeout"          env:"IDLE_TIMEOUT"`
	MaxHeaderBytes      ByteSize      `yaml:"max_header_bytes"      env:"MAX_HEADER_BYTES"`
	MaxConnections      int           `yaml:"max_connections"       env:"MAX_CONNECTIONS"`
	ShutdownDelay       time.Duration `yaml:"shutdown_delay"        env:"SHUTDOWN_DELAY"`
	ShutdownTimeout     time.Duration `yaml:"shutdown_timeout"      env:"SHUTDOWN_TIMEOUT"`
	ReadyQueueThreshold int           `yaml:"ready_queue_threshold" env:"READY_QUEUE_THRESHOLD"`
	CORSOrigins         []string      `yaml:"cors_origins"          env:"CORS_ORIGINS"`
}

// Admin configures the private listener (/metrics, pprof).
type Admin struct {
	Addr  string `yaml:"addr"  env:"ADDR"` // empty disables the listener
	Pprof bool   `yaml:"pprof" env:"PPROF"`
}

// Signing configures URL signature verification (imgproxy scheme and xi_sig).
type Signing struct {
	Keys  []HexKey `yaml:"keys"  env:"KEYS"`
	Salts []HexKey `yaml:"salts" env:"SALTS"`
	Size  int      `yaml:"size"  env:"SIZE"`
	// Trusted signatures are accepted verbatim.
	Trusted []Secret `yaml:"trusted" env:"TRUSTED"`
	// Required defaults to True when keys are set, False otherwise.
	Required Tristate `yaml:"required" env:"REQUIRED"`
}

// Encryption configures enc/ source URLs. The first key encrypts; every key
// decrypts (rotation).
type Encryption struct {
	Keys []HexKey `yaml:"keys" env:"KEYS"`
	// Key is the deprecated single-key alias of Keys; Load folds it into Keys.
	Key HexKey `yaml:"key,omitempty" env:"KEY"`
}

// Security holds the unsigned-request policy and per-request overrides.
type Security struct {
	UnsignedPolicy UnsignedPolicy `yaml:"unsigned_policy"            env:"UNSIGNED_POLICY"`
	AllowedWidths  IntList        `yaml:"allowed_widths"             env:"ALLOWED_WIDTHS"`
	// AllowedHeights are the restricted-policy heights; empty means
	// AllowedWidths.
	AllowedHeights IntList `yaml:"allowed_heights" env:"ALLOWED_HEIGHTS"`
	// AllowedDPRs are the restricted-policy device pixel ratios.
	AllowedDPRs              []float64 `yaml:"allowed_dprs"      env:"ALLOWED_DPRS"`
	AllowedQualities         IntList   `yaml:"allowed_qualities" env:"ALLOWED_QUALITIES"`
	AllowSecurityOptions     bool      `yaml:"allow_security_options"     env:"ALLOW_SECURITY_OPTIONS"`
	AllowRaisingLimits       bool      `yaml:"allow_raising_limits"       env:"ALLOW_RAISING_LIMITS"`
	Secret                   Secret    `yaml:"secret"                     env:"SECRET"`
	AllowedProcessingOptions []string  `yaml:"allowed_processing_options" env:"ALLOWED_PROCESSING_OPTIONS"`
}

// Source is one named source. Which fields apply depends on Type; Validate
// rejects fields set for another type.
type Source struct {
	Type SourceType `yaml:"type" env:"TYPE"`

	// http
	Allow   []string          `yaml:"allow,omitempty"    env:"ALLOW"`
	BaseURL string            `yaml:"base_url,omitempty" env:"BASE_URL"`
	Headers map[string]Secret `yaml:"headers,omitempty"  env:"HEADERS" envKeyValSeparator:"="`

	// s3, gcs, azure
	Buckets []string `yaml:"buckets,omitempty" env:"BUCKETS"`
	Bucket  string   `yaml:"bucket,omitempty"  env:"BUCKET"`
	// every type except http
	Prefix string `yaml:"prefix,omitempty" env:"PREFIX"`

	// s3, gcs, azure: service endpoint override (MinIO, fake-gcs, Azurite)
	Endpoint string `yaml:"endpoint,omitempty" env:"ENDPOINT"`

	// s3
	Region          string `yaml:"region,omitempty"            env:"REGION"`
	ForcePathStyle  bool   `yaml:"force_path_style,omitempty"  env:"FORCE_PATH_STYLE"`
	AccessKeyID     string `yaml:"access_key_id,omitempty"     env:"ACCESS_KEY_ID"`
	SecretAccessKey Secret `yaml:"secret_access_key,omitempty" env:"SECRET_ACCESS_KEY"`
	RoleARN         string `yaml:"role_arn,omitempty"          env:"ROLE_ARN"`

	// gcs
	CredentialsJSON Secret `yaml:"credentials_json,omitempty" env:"CREDENTIALS_JSON"`

	// azure
	Account          string `yaml:"account,omitempty"           env:"ACCOUNT"`
	AccountKey       Secret `yaml:"account_key,omitempty"       env:"ACCOUNT_KEY"`
	ConnectionString Secret `yaml:"connection_string,omitempty" env:"CONNECTION_STRING"`
	SASToken         Secret `yaml:"sas_token,omitempty"         env:"SAS_TOKEN"`

	// fs: the jail root; ftp, sftp: the server directory keys are relative
	// to ("" means "/").
	Root string `yaml:"root,omitempty" env:"ROOT"`
	// FollowSymlinks allows symlinks whose target stays inside Root.
	FollowSymlinks bool `yaml:"follow_symlinks,omitempty" env:"FOLLOW_SYMLINKS"`

	// ftp, sftp
	Host       string `yaml:"host,omitempty"        env:"HOST"`
	Port       int    `yaml:"port,omitempty"        env:"PORT"`
	User       string `yaml:"user,omitempty"        env:"USER"`
	Password   Secret `yaml:"password,omitempty"    env:"PASSWORD"`
	PrivateKey Secret `yaml:"private_key,omitempty" env:"PRIVATE_KEY"`
	// PrivateKeyPassphrase decrypts an encrypted PrivateKey.
	PrivateKeyPassphrase Secret `yaml:"private_key_passphrase,omitempty" env:"PRIVATE_KEY_PASSPHRASE"`
	HostKey              string `yaml:"host_key,omitempty"               env:"HOST_KEY"`
	TLS                  bool   `yaml:"tls,omitempty"                    env:"TLS"`
	// PoolSize bounds open connections to the server (0 = backend default, 4).
	PoolSize int `yaml:"pool_size,omitempty" env:"POOL_SIZE"`
	// DialTimeout bounds connect, TLS/SSH handshake and login (0 = 10s).
	DialTimeout time.Duration `yaml:"dial_timeout,omitempty" env:"DIAL_TIMEOUT"`
	// IdleTimeout closes pooled connections idle for longer (0 = 1m).
	IdleTimeout time.Duration `yaml:"idle_timeout,omitempty" env:"IDLE_TIMEOUT"`
}

// Fetch configures origin downloads and the SSRF policy.
type Fetch struct {
	Timeout time.Duration `yaml:"timeout" env:"TIMEOUT"`
	// Retry retries a transient failure once (5xx, connection error,
	// attempt timeout); never 4xx.
	Retry                bool  `yaml:"retry"                  env:"RETRY"`
	MaxRedirects         int   `yaml:"max_redirects"          env:"MAX_REDIRECTS"`
	MaxConcurrent        int   `yaml:"max_concurrent"         env:"MAX_CONCURRENT"`
	AllowedPorts         []int `yaml:"allowed_ports"          env:"ALLOWED_PORTS"`
	AllowPrivateNetworks bool  `yaml:"allow_private_networks" env:"ALLOW_PRIVATE_NETWORKS"`
	// AllowedNetworks are exempt from the private-address block (for example
	// an in-cluster MinIO) without opening every private range.
	AllowedNetworks []CIDR `yaml:"allowed_networks" env:"ALLOWED_NETWORKS"`
	// UserAgent defaults to xtandard-image/<version> when empty.
	UserAgent string `yaml:"user_agent" env:"USER_AGENT"`
}

// Dialects configures the URL router (docs/URL-DIALECTS.md §1).
type Dialects struct {
	Default DialectName `yaml:"default" env:"DEFAULT"`
	Strict  bool        `yaml:"strict"  env:"STRICT"`

	Imgproxy         ImgproxyDialect   `yaml:"imgproxy"          envPrefix:"IMGPROXY_"`
	Query            Mount             `yaml:"query"             envPrefix:"QUERY_"`
	Wsrv             Mount             `yaml:"wsrv"              envPrefix:"WSRV_"`
	Cloudflare       Mount             `yaml:"cloudflare"        envPrefix:"CLOUDFLARE_"`
	CloudflareImages Mount             `yaml:"cloudflare_images" envPrefix:"CLOUDFLARE_IMAGES_"`
	Imgix            Mount             `yaml:"imgix"             envPrefix:"IMGIX_"`
	Cloudinary       CloudinaryDialect `yaml:"cloudinary"        envPrefix:"CLOUDINARY_"`
	Imagekit         Mount             `yaml:"imagekit"          envPrefix:"IMAGEKIT_"`
	Bunny            Mount             `yaml:"bunny"             envPrefix:"BUNNY_"`
	Thumbor          Mount             `yaml:"thumbor"           envPrefix:"THUMBOR_"`
	Simple           SimpleDialect     `yaml:"simple"            envPrefix:"SIMPLE_"`
}

// MountOptions are the per-mount settings of a dialect.
type MountOptions struct {
	Prefix string   `yaml:"prefix" env:"PREFIX"`
	Hosts  []string `yaml:"hosts"  env:"HOSTS"`
	Source string   `yaml:"source" env:"SOURCE"`
	// Keys are dialect secrets (imgix token, thumbor key, …).
	Keys []Secret `yaml:"keys" env:"KEYS"`
	// UnsignedPolicy overrides security.unsigned_policy when set.
	UnsignedPolicy UnsignedPolicy `yaml:"unsigned_policy" env:"UNSIGNED_POLICY"`
}

// Mount is a dialect with its primary mount and optional extra mounts
// (YAML only: dialects.<name>.mounts[]).
type Mount struct {
	Enabled      bool `yaml:"enabled" env:"ENABLED"`
	MountOptions `yaml:",inline"`
	Mounts       []MountOptions `yaml:"mounts" env:"-"`
}

// ImgproxyDialect is the canonical dialect.
type ImgproxyDialect struct {
	Mount              `yaml:",inline"`
	ArgumentsSeparator string `yaml:"arguments_separator" env:"ARGUMENTS_SEPARATOR"`
	BaseURL            string `yaml:"base_url"            env:"BASE_URL"`
	// URLReplacements are "regex=replacement" pairs (env: ;-separated).
	URLReplacements []string `yaml:"url_replacements" env:"URL_REPLACEMENTS" envSeparator:";"`
}

// CloudinaryDialect adds the cloud → source map.
type CloudinaryDialect struct {
	Mount  `yaml:",inline"`
	Clouds map[string]string `yaml:"clouds" env:"CLOUDS" envKeyValSeparator:"="`
}

// SimpleDialect is the table-driven family (nextjs, netlify, ipx, …); each
// provider mounts at its native prefix, so Enabled lists providers.
type SimpleDialect struct {
	Enabled        []string       `yaml:"enabled"         env:"ENABLED"`
	Hosts          []string       `yaml:"hosts"           env:"HOSTS"`
	Source         string         `yaml:"source"          env:"SOURCE"`
	UnsignedPolicy UnsignedPolicy `yaml:"unsigned_policy" env:"UNSIGNED_POLICY"`
}

// Processing configures admission control and pipeline defaults.
type Processing struct {
	// Workers defaults to GOMAXPROCS when zero.
	Workers int `yaml:"workers" env:"WORKERS"`
	// QueueSize defaults to Workers×16 when negative.
	QueueSize         int           `yaml:"queue_size"          env:"QUEUE_SIZE"`
	QueueTimeout      time.Duration `yaml:"queue_timeout"       env:"QUEUE_TIMEOUT"`
	MemoryBudget      ByteSize      `yaml:"memory_budget"       env:"MEMORY_BUDGET"` // 0 = off
	GoMemoryFraction  float64       `yaml:"go_memory_fraction"  env:"GO_MEMORY_FRACTION"`
	AutoRotate        bool          `yaml:"auto_rotate"         env:"AUTO_ROTATE"`
	StripMetadata     bool          `yaml:"strip_metadata"      env:"STRIP_METADATA"`
	KeepCopyright     bool          `yaml:"keep_copyright"      env:"KEEP_COPYRIGHT"`
	StripColorProfile bool          `yaml:"strip_color_profile" env:"STRIP_COLOR_PROFILE"`
	PreserveHDR       bool          `yaml:"preserve_hdr"        env:"PRESERVE_HDR"`
	LinearResize      bool          `yaml:"linear_resize"       env:"LINEAR_RESIZE"`
	ColorProfilesDir  string        `yaml:"color_profiles_dir"  env:"COLOR_PROFILES_DIR"`
	WatermarkSource   string        `yaml:"watermark_source"    env:"WATERMARK_SOURCE"`
	WatermarkOpacity  float64       `yaml:"watermark_opacity"   env:"WATERMARK_OPACITY"`
}

// Fallback is the image served when the source fails.
type Fallback struct {
	Source string `yaml:"source" env:"SOURCE"`
	Status int    `yaml:"status" env:"STATUS"`
}

// Formats holds encoder defaults and negotiation switches (docs/FORMATS.md).
type Formats struct {
	Quality int `yaml:"quality" env:"QUALITY"`

	JPEG JPEGFormat `yaml:"jpeg" envPrefix:"JPEG_"`
	PNG  PNGFormat  `yaml:"png"  envPrefix:"PNG_"`
	WebP WebPFormat `yaml:"webp" envPrefix:"WEBP_"`
	AVIF AVIFFormat `yaml:"avif" envPrefix:"AVIF_"`
	HEIC HEICFormat `yaml:"heic" envPrefix:"HEIC_"`
	JXL  JXLFormat  `yaml:"jxl"  envPrefix:"JXL_"`
	GIF  GIFFormat  `yaml:"gif"  envPrefix:"GIF_"`

	Auto    FormatSwitches `yaml:"auto"    envPrefix:"AUTO_"`
	Enforce FormatSwitches `yaml:"enforce" envPrefix:"ENFORCE_"`

	StrictAccept   bool     `yaml:"strict_accept"           env:"STRICT_ACCEPT"`
	Preferred      []Format `yaml:"preferred"               env:"PREFERRED"`
	SVGPassthrough bool     `yaml:"svg_passthrough"         env:"SVG_PASSTHROUGH"`
	// SkipProcessing lists source formats streamed unmodified.
	SkipProcessing       []Format `yaml:"skip_processing" env:"SKIP_PROCESSING"`
	SaveDataQualityDelta int      `yaml:"save_data_quality_delta" env:"SAVE_DATA_QUALITY_DELTA"`

	Best        BestFormat  `yaml:"best"        envPrefix:"BEST_"`
	AutoQuality AutoQuality `yaml:"autoquality" envPrefix:"AUTOQUALITY_"`
}

// ClientHints configures server-wide client hints (docs/FORMATS.md).
type ClientHints struct {
	// Enabled honours DPR/Width/Viewport-Width/Save-Data on every request
	// that leaves the value unset (imgproxy IMGPROXY_ENABLE_CLIENT_HINTS).
	Enabled bool `yaml:"enabled" env:"ENABLED"`
}

// JPEGFormat holds JPEG encoder defaults.
type JPEGFormat struct {
	Quality     int  `yaml:"quality"     env:"QUALITY"`
	Progressive bool `yaml:"progressive" env:"PROGRESSIVE"`
}

// PNGFormat holds PNG encoder defaults.
type PNGFormat struct {
	Quantize bool `yaml:"quantize" env:"QUANTIZE"`
}

// WebPFormat holds WebP encoder defaults.
type WebPFormat struct {
	Quality int `yaml:"quality" env:"QUALITY"`
	Effort  int `yaml:"effort"  env:"EFFORT"`
}

// AVIFFormat holds AVIF encoder defaults.
type AVIFFormat struct {
	Quality   int   `yaml:"quality"    env:"QUALITY"`
	Effort    int   `yaml:"effort"     env:"EFFORT"`
	MaxPixels int64 `yaml:"max_pixels" env:"MAX_PIXELS"`
}

// HEICFormat holds HEIC encoder defaults. Encoding is off by default (HEVC
// patents, docs/AGENT-WORKFLOW.md decisions).
type HEICFormat struct {
	Quality int  `yaml:"quality" env:"QUALITY"`
	Encode  bool `yaml:"encode"  env:"ENCODE"`
}

// JXLFormat holds JPEG XL encoder defaults.
type JXLFormat struct {
	Quality int `yaml:"quality" env:"QUALITY"`
	Effort  int `yaml:"effort"  env:"EFFORT"`
}

// GIFFormat holds GIF encoder defaults.
type GIFFormat struct {
	Effort int `yaml:"effort" env:"EFFORT"`
}

// FormatSwitches toggles a behaviour per modern format.
type FormatSwitches struct {
	AVIF bool `yaml:"avif" env:"AVIF"`
	WebP bool `yaml:"webp" env:"WEBP"`
	JXL  bool `yaml:"jxl"  env:"JXL"`
}

// BestFormat configures format=best.
type BestFormat struct {
	MaxPixels           int64   `yaml:"max_pixels"           env:"MAX_PIXELS"`
	ComplexityThreshold float64 `yaml:"complexity_threshold" env:"COMPLEXITY_THRESHOLD"`
}

// AutoQuality configures quality search.
type AutoQuality struct {
	Method       AutoQualityMethod `yaml:"method"        env:"METHOD"`
	Target       float64           `yaml:"target"        env:"TARGET"`
	Min          int               `yaml:"min"           env:"MIN"`
	Max          int               `yaml:"max"           env:"MAX"`
	AllowedError float64           `yaml:"allowed_error" env:"ALLOWED_ERROR"`
}

// Limits bound the work a single request may cause.
type Limits struct {
	MaxSrcBytes             ByteSize `yaml:"max_src_bytes"              env:"MAX_SRC_BYTES"`
	MaxSrcPixels            int64    `yaml:"max_src_pixels"             env:"MAX_SRC_PIXELS"`
	MaxResultDimension      int      `yaml:"max_result_dimension"       env:"MAX_RESULT_DIMENSION"`
	MaxAnimationFrames      int      `yaml:"max_animation_frames"       env:"MAX_ANIMATION_FRAMES"`
	MaxAnimationFramePixels int64    `yaml:"max_animation_frame_pixels" env:"MAX_ANIMATION_FRAME_PIXELS"`
	MaxRawDemosaicPixels    int64    `yaml:"max_raw_demosaic_pixels"    env:"MAX_RAW_DEMOSAIC_PIXELS"`
	MaxSVGBytes             ByteSize `yaml:"max_svg_bytes"              env:"MAX_SVG_BYTES"`
	MaxSVGDepth             int      `yaml:"max_svg_depth"              env:"MAX_SVG_DEPTH"`
	MaxSVGElements          int      `yaml:"max_svg_elements"           env:"MAX_SVG_ELEMENTS"`
	MaxPDFPages             int      `yaml:"max_pdf_pages"              env:"MAX_PDF_PAGES"`
}

// Vips configures libvips.
type Vips struct {
	Concurrency int    `yaml:"concurrency" env:"CONCURRENCY"`
	FailOn      FailOn `yaml:"fail_on"     env:"FAIL_ON"`
}

// Cache configures HTTP cache headers and the result/origin caches.
type Cache struct {
	Headers     CacheHeaders `yaml:"headers"      envPrefix:"HEADERS_"`
	ETagBuster  Secret       `yaml:"etag_buster"  env:"ETAG_BUSTER"`
	TagsHeader  string       `yaml:"tags_header"  env:"TAGS_HEADER"`
	AllowBypass bool         `yaml:"allow_bypass" env:"ALLOW_BYPASS"`
	Result      ResultCache  `yaml:"result"       envPrefix:"RESULT_"`
	Origin      OriginCache  `yaml:"origin"       envPrefix:"ORIGIN_"`
}

// CacheHeaders configures Cache-Control.
type CacheHeaders struct {
	TTL         time.Duration `yaml:"ttl"         env:"TTL"`
	ErrorTTL    time.Duration `yaml:"error_ttl"   env:"ERROR_TTL"`
	Passthrough bool          `yaml:"passthrough" env:"PASSTHROUGH"`
}

// ResultCache configures the tiered result cache.
type ResultCache struct {
	Stores      []StoreKind   `yaml:"stores"       env:"STORES"`
	MemoryBytes ByteSize      `yaml:"memory_bytes" env:"MEMORY_BYTES"`
	Disk        DiskCache     `yaml:"disk"         envPrefix:"DISK_"`
	Redis       RedisCache    `yaml:"redis"        envPrefix:"REDIS_"`
	S3          S3ResultCache `yaml:"s3"           envPrefix:"S3_"`
}

// DiskCache configures the disk tier.
type DiskCache struct {
	Path  string   `yaml:"path"  env:"PATH"`
	Bytes ByteSize `yaml:"bytes" env:"BYTES"`
}

// RedisCache configures the redis tier.
type RedisCache struct {
	URL Secret `yaml:"url" env:"URL"` // may carry a password
}

// S3ResultCache configures the s3 tier; Source names an s3 source whose
// credentials are reused.
type S3ResultCache struct {
	Source string `yaml:"source" env:"SOURCE"`
	Bucket string `yaml:"bucket" env:"BUCKET"`
	Prefix string `yaml:"prefix" env:"PREFIX"`
}

// OriginCache configures the in-memory original-bytes LRU.
type OriginCache struct {
	MemoryBytes ByteSize      `yaml:"memory_bytes" env:"MEMORY_BYTES"`
	TTL         time.Duration `yaml:"ttl"          env:"TTL"`
	// NegativeTTL is how long a source not-found is remembered; 0 disables.
	NegativeTTL time.Duration `yaml:"negative_ttl" env:"NEGATIVE_TTL"`
}

// Observability configures logs and debug headers. Metrics live on the
// admin listener; tracing uses the standard OTEL_* variables.
type Observability struct {
	LogLevel     LogLevel `yaml:"log_level"     env:"LOG_LEVEL"`
	DebugHeaders bool     `yaml:"debug_headers" env:"DEBUG_HEADERS"`
}

// OTel forces tracing on without an OTEL_EXPORTER_OTLP_ENDPOINT; the
// exporter is still chosen by the standard OTEL_* variables.
type OTel struct {
	Enabled bool `yaml:"enabled" env:"ENABLED"`
}

// Compat holds imgproxy compatibility switches.
type Compat struct {
	// ImgproxyStatus answers invalid URLs and disallowed sources with 404
	// like imgproxy instead of 400/403.
	ImgproxyStatus bool `yaml:"imgproxy_status" env:"IMGPROXY_STATUS"`
	// ImgproxyEnv maps IMGPROXY_* variables onto XI_* (WP-04b).
	ImgproxyEnv bool `yaml:"imgproxy_env" env:"IMGPROXY_ENV"`
}

// Defaults returns the built-in defaults (docs/CONFIG.md §2). Values derived
// from other settings (worker count, queue size, write timeout, signing
// requirement) are resolved by Load.
func Defaults() Config {
	qualities, _ := ParseIntList("5-100/5")
	return Config{
		Server: Server{
			Addr:              ":8080",
			RequestTimeout:    20 * time.Second,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			IdleTimeout:       90 * time.Second,
			MaxHeaderBytes:    32 << 10,
			MaxConnections:    4096,
			ShutdownDelay:     5 * time.Second,
			ShutdownTimeout:   30 * time.Second,
		},
		Admin:   Admin{Addr: "127.0.0.1:9090"},
		Signing: Signing{Size: 32},
		Security: Security{
			UnsignedPolicy: UnsignedRestricted,
			// Next.js deviceSizes + imageSizes.
			AllowedWidths:    IntList{16, 32, 48, 64, 96, 128, 256, 384, 640, 750, 828, 1080, 1200, 1920, 2048, 3840},
			AllowedDPRs:      []float64{1},
			AllowedQualities: qualities,
		},
		Fetch: Fetch{
			Timeout:       10 * time.Second,
			Retry:         true,
			MaxRedirects:  5,
			MaxConcurrent: 256,
			AllowedPorts:  []int{80, 443},
		},
		Dialects: Dialects{
			Default:          DialectImgproxy,
			Imgproxy:         ImgproxyDialect{Mount: Mount{Enabled: true, Prefix: "/"}, ArgumentsSeparator: ":"},
			Query:            mount("/q/"),
			Wsrv:             mount("/wsrv/"),
			Cloudflare:       mount("/cdn-cgi/image/"),
			CloudflareImages: mount("/cdn-cgi/imagedelivery/"),
			Imgix:            mount("/imgix/"),
			Cloudinary:       CloudinaryDialect{Mount: mount("/cloudinary/")},
			Imagekit:         mount("/imagekit/"),
			Bunny:            mount("/bunny/"),
			Thumbor:          mount("/thumbor/"),
		},
		Processing: Processing{
			QueueSize:         -1,
			QueueTimeout:      5 * time.Second,
			GoMemoryFraction:  0.3,
			AutoRotate:        true,
			StripMetadata:     true,
			KeepCopyright:     true,
			StripColorProfile: true,
			WatermarkOpacity:  1,
		},
		Fallback: Fallback{Status: 200},
		Formats: Formats{
			Quality:              80,
			JPEG:                 JPEGFormat{Quality: 80, Progressive: true},
			WebP:                 WebPFormat{Quality: 80, Effort: 4},
			AVIF:                 AVIFFormat{Quality: 60, Effort: 4, MaxPixels: 16_000_000},
			HEIC:                 HEICFormat{Quality: 60},
			JXL:                  JXLFormat{Quality: 75, Effort: 5},
			GIF:                  GIFFormat{Effort: 7},
			Auto:                 FormatSwitches{AVIF: true, WebP: true},
			Preferred:            []Format{FormatJPEG, FormatPNG, FormatGIF},
			SVGPassthrough:       true,
			SaveDataQualityDelta: -15,
			Best:                 BestFormat{MaxPixels: 8_000_000, ComplexityThreshold: 5.5},
			AutoQuality:          AutoQuality{Method: AutoQualityNone, Target: 0.02, Min: 60, Max: 85, AllowedError: 0.001},
		},
		Limits: Limits{
			MaxSrcBytes:             50 << 20,
			MaxSrcPixels:            50_000_000,
			MaxResultDimension:      8192,
			MaxAnimationFrames:      256,
			MaxAnimationFramePixels: 16_000_000,
			MaxRawDemosaicPixels:    60_000_000,
			MaxSVGBytes:             5 << 20,
			MaxSVGDepth:             256,
			MaxSVGElements:          200_000,
			MaxPDFPages:             500,
		},
		Vips: Vips{Concurrency: 1, FailOn: FailOnWarning},
		Cache: Cache{
			Headers:    CacheHeaders{TTL: 31_536_000 * time.Second, ErrorTTL: 10 * time.Second},
			TagsHeader: "Cache-Tag",
			Result:     ResultCache{MemoryBytes: 256 << 20, Disk: DiskCache{Bytes: 10 << 30}},
			Origin:     OriginCache{MemoryBytes: 256 << 20, TTL: 60 * time.Second, NegativeTTL: 5 * time.Second},
		},
		Observability: Observability{LogLevel: LogInfo},
	}
}

func mount(prefix string) Mount {
	return Mount{Prefix: prefix}
}
