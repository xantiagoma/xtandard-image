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

import (
	"os"
	"path/filepath"
	"slices"
	"time"
)

// Config is the whole service configuration. Build it with Load.
type Config struct {
	DevMode bool `yaml:"dev_mode" env:"DEV_MODE"`
	// Role is what this instance serves: all (default), image (no video
	// transformations) or video (only video transformations, plus health
	// and metrics), so video can run as a dedicated deployment (ADR 0012).
	Role Role `yaml:"role" env:"ROLE"`

	Server     Server     `yaml:"server"     envPrefix:"SERVER_"`
	Admin      Admin      `yaml:"admin"      envPrefix:"ADMIN_"`
	Playground Playground `yaml:"playground" envPrefix:"PLAYGROUND_"`
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
	Sandbox       Sandbox       `yaml:"sandbox"       envPrefix:"SANDBOX_"`
	Video         Video         `yaml:"video"         envPrefix:"VIDEO_"`
	Vips          Vips          `yaml:"vips"          envPrefix:"VIPS_"`
	Detect        Detect        `yaml:"detect"        envPrefix:"DETECT_"`
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

// Admin configures the private listener (/metrics, /openapi.json, pprof).
type Admin struct {
	Addr  string `yaml:"addr"  env:"ADDR"` // empty disables the listener
	Pprof bool   `yaml:"pprof" env:"PPROF"`
	// Docs serves the Scalar API reference at /docs, loaded from a pinned
	// jsDelivr URL (ADR 0008); /openapi.json is always served.
	Docs bool `yaml:"docs" env:"DOCS"`
}

// Playground configures the embedded playground UI (WP-80): off by default,
// served on the admin listener, and on the public listener only when Public
// is set.
type Playground struct {
	Enabled bool `yaml:"enabled" env:"ENABLED"`
	// Public also serves the playground on the public listener (under
	// server.path_prefix); its sign endpoint then signs URLs for anyone.
	Public bool `yaml:"public" env:"PUBLIC"`
	// Path is where the playground is mounted: starts with "/", no
	// trailing "/".
	Path string `yaml:"path" env:"PATH"`
	// Uploads enables POST <path>/api/upload: files dropped on the page
	// are kept in memory for 10 minutes as playground:// sources, visible
	// only through the playground. Auto (the default) follows Enabled.
	Uploads Tristate `yaml:"uploads" env:"UPLOADS"`
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

	// s3, gcs, azure: service endpoint override (RustFS, fake-gcs, Azurite)
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

	// every type: the password password-protected PDFs of this source open
	// with when a request carries no pdf_password of its own (WP-115).
	PDFPassword Secret `yaml:"pdf_password,omitempty" env:"PDF_PASSWORD"`
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
	// an in-cluster object store) without opening every private range.
	AllowedNetworks []CIDR `yaml:"allowed_networks" env:"ALLOWED_NETWORKS"`
	// UserAgent defaults to xtandard-image/<version> when empty.
	UserAgent string `yaml:"user_agent" env:"USER_AGENT"`
}

// Dialects configures the URL router (docs/URL-DIALECTS.md §1).
type Dialects struct {
	Default DialectName `yaml:"default" env:"DEFAULT"`
	Strict  bool        `yaml:"strict"  env:"STRICT"`

	Imgproxy         ImgproxyDialect         `yaml:"imgproxy"          envPrefix:"IMGPROXY_"`
	Query            Mount                   `yaml:"query"             envPrefix:"QUERY_"`
	Wsrv             Mount                   `yaml:"wsrv"              envPrefix:"WSRV_"`
	Cloudflare       Mount                   `yaml:"cloudflare"        envPrefix:"CLOUDFLARE_"`
	CloudflareImages CloudflareImagesDialect `yaml:"cloudflare_images" envPrefix:"CLOUDFLARE_IMAGES_"`
	Imgix            Mount                   `yaml:"imgix"             envPrefix:"IMGIX_"`
	Cloudinary       CloudinaryDialect       `yaml:"cloudinary"        envPrefix:"CLOUDINARY_"`
	Imagekit         ImagekitDialect         `yaml:"imagekit"          envPrefix:"IMAGEKIT_"`
	Bunny            Mount                   `yaml:"bunny"             envPrefix:"BUNNY_"`
	Thumbor          Mount                   `yaml:"thumbor"           envPrefix:"THUMBOR_"`
	AWSSIH           AWSSIHDialect           `yaml:"awssih"            envPrefix:"AWSSIH_"`
	Simple           SimpleDialect           `yaml:"simple"            envPrefix:"SIMPLE_"`
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

// ImagekitDialect adds the URL-endpoint path of host-bound mounts.
type ImagekitDialect struct {
	Mount `yaml:",inline"`
	// EndpointPath is the ImageKit URL-endpoint id kept in the path of every
	// mount of the dialect (https://ik.imagekit.io/<id>/…): "/<id>". It is
	// stripped before parsing, excluded from the source key and from the
	// signed message; requests outside it are 400. Empty: none.
	EndpointPath string `yaml:"endpoint_path" env:"ENDPOINT_PATH"`
}

// CloudflareImagesDialect adds the account hash → source map.
type CloudflareImagesDialect struct {
	Mount    `yaml:",inline"`
	Accounts map[string]string `yaml:"accounts" env:"ACCOUNTS" envKeyValSeparator:"="`
}

// AWSSIHDialect is AWS "Dynamic Image Transformation for Amazon
// CloudFront" (Serverless Image Handler, docs/URL-DIALECTS.md §13). Keys
// are the SIH secrets, used verbatim (SIH's SECRETS_MANAGER/SECRET_KEY
// value).
type AWSSIHDialect struct {
	Mount `yaml:",inline"`
	// Buckets maps SIH bucket names to sources (SIH SOURCE_BUCKETS); a
	// request without a bucket uses the mount's source.
	Buckets map[string]string `yaml:"buckets" env:"BUCKETS" envKeyValSeparator:"="`
	// Thumbor forwards non-JSON paths to the thumbor dialect.
	Thumbor bool `yaml:"thumbor" env:"THUMBOR"`
	// QueryParams enables SIH's query-param mapper (format, fit, width, ...).
	QueryParams bool `yaml:"query_params" env:"QUERY_PARAMS"`
	// RewriteMatchPattern and RewriteSubstitution are SIH's custom request
	// type: a "/pattern/flags" regex applied to non-JSON paths before they
	// are forwarded to thumbor.
	RewriteMatchPattern string `yaml:"rewrite_match_pattern" env:"REWRITE_MATCH_PATTERN"`
	RewriteSubstitution string `yaml:"rewrite_substitution"  env:"REWRITE_SUBSTITUTION"`
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
	// WatermarkFont is the Pango font description of text watermarks; a
	// request's font takes its missing family or size from it.
	WatermarkFont string `yaml:"watermark_font" env:"WATERMARK_FONT"`
	// WatermarkFontFile is a font file registered for text watermarks
	// besides the image's fontconfig fonts.
	WatermarkFontFile string `yaml:"watermark_font_file" env:"WATERMARK_FONT_FILE"`
}

// Fallback is the image served when the source fails.
type Fallback struct {
	Source string        `yaml:"source" env:"SOURCE"`
	Status int           `yaml:"status" env:"STATUS"`
	TTL    time.Duration `yaml:"ttl"    env:"TTL"`
	// AllowRequestURL honours a request's fallback_image_url.
	AllowRequestURL bool `yaml:"allow_request_url" env:"ALLOW_REQUEST_URL"`
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
	PDF  PDFFormat  `yaml:"pdf"  envPrefix:"PDF_"`
	EPS  EPSFormat  `yaml:"eps"  envPrefix:"EPS_"`
	// Metafile configures WMF and EMF input (WP-108).
	Metafile MetafileFormat `yaml:"metafile" envPrefix:"METAFILE_"`
	PSD      PSDFormat      `yaml:"psd"  envPrefix:"PSD_"`
	RAW      RAWFormat      `yaml:"raw"  envPrefix:"RAW_"`
	HDR      HDRFormat      `yaml:"hdr"  envPrefix:"HDR_"`

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
	// CriticalCH adds Critical-CH (Sec-CH-DPR, Sec-CH-Width,
	// Sec-CH-Viewport-Width) next to Accept-CH on image responses.
	CriticalCH bool `yaml:"critical_ch" env:"CRITICAL_CH"`
}

// JPEGFormat holds JPEG encoder defaults.
type JPEGFormat struct {
	Quality     int  `yaml:"quality"     env:"QUALITY"`
	Progressive bool `yaml:"progressive" env:"PROGRESSIVE"`
}

// RAWFormat configures camera RAW decoding (FORMATS.md §2).
type RAWFormat struct {
	// PreviewMinRatio: the embedded JPEG preview is decoded instead of
	// demosaicing when it is at least this many times the size the image
	// is scaled to, on both axes.
	PreviewMinRatio float64 `yaml:"preview_min_ratio" env:"PREVIEW_MIN_RATIO"`
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

// PDFFormat configures PDF input (PDFium).
type PDFFormat struct {
	// DPI is the resolution of a page's natural size, used when no size is
	// requested; resized pages render directly at the target size.
	DPI float64 `yaml:"dpi" env:"DPI"`
}

// EPSFormat configures EPS, PostScript and PostScript Illustrator input,
// rendered by Ghostscript (AGPL-3.0) in a sandboxed subprocess. Only the
// gpl image ships gs; enabling it without one fails startup (ADR 0011).
type EPSFormat struct {
	// Enabled renders EPS/PS/AI sources with Ghostscript. Off, a DOS EPS
	// file's embedded TIFF preview is used when it has one, and anything
	// else is refused (422).
	Enabled bool `yaml:"enabled" env:"ENABLED"`
	// MaxProcs bounds concurrent Ghostscript processes; more renders fail
	// fast with 429.
	MaxProcs int `yaml:"max_procs" env:"MAX_PROCS"`
	// Timeout bounds one render (wall clock); the process gets the same
	// CPU time plus one second.
	Timeout time.Duration `yaml:"timeout" env:"TIMEOUT"`
	// MaxDPI caps the render resolution.
	MaxDPI float64 `yaml:"max_dpi" env:"MAX_DPI"`
	// GSPath is the gs executable: an absolute path or a name looked up
	// in PATH.
	GSPath string `yaml:"gs_path" env:"GS_PATH"`
}

// MetafileFormat configures Windows metafile input (WMF, EMF), converted
// to SVG by xi-metafile2svg (libwmf, libemf2svg: GPL) in a sandboxed
// subprocess. Only the gpl image ships the converter (WP-108).
type MetafileFormat struct {
	// Enabled converts WMF and EMF sources. auto (the default) converts them
	// when the converter is found (the gpl image) and refuses them (422)
	// otherwise; true fails startup without it; false refuses them.
	Enabled Tristate `yaml:"enabled" env:"ENABLED"`
	// MaxProcs bounds concurrent conversions; more fail fast with 429.
	MaxProcs int `yaml:"max_procs" env:"MAX_PROCS"`
	// Timeout bounds one conversion (wall clock); the process gets the
	// same CPU time plus one second.
	Timeout time.Duration `yaml:"timeout" env:"TIMEOUT"`
	// Path is the converter: an absolute path or a name looked up in PATH.
	Path string `yaml:"path" env:"PATH"`
}

// Sandbox configures the resource limits of the sandboxed subprocesses
// (Ghostscript for EPS, the metafile converter; ADR 0011) that depend on the
// host rather than the input.
type Sandbox struct {
	// MaxAS is the base address-space limit (RLIMIT_AS) of a subprocess;
	// twice the expected output comes on top. 0 turns the limit off (the
	// CPU time, process, file-size and open-file limits stay).
	MaxAS ByteSize `yaml:"max_as" env:"MAX_AS"`
	// DetectEmulation drops the address-space limit when the server runs
	// under CPU emulation (Rosetta, QEMU user mode), whose translator
	// cannot map its own memory under it.
	DetectEmulation bool `yaml:"detect_emulation" env:"DETECT_EMULATION"`
}

// HDRFormat configures scene-linear inputs (OpenEXR, Radiance, PFM).
type HDRFormat struct {
	// ToneMap is the operator that maps them to 8-bit sRGB unless
	// preserve_hdr keeps them (docs/FORMATS.md §1.4).
	ToneMap ToneMap `yaml:"tonemap" env:"TONEMAP"`
}

// PSDFormat configures Photoshop PSD/PSB input (ImageMagick, PSD only).
type PSDFormat struct {
	// RequireComposite refuses files saved without a merged composite
	// ("Maximize compatibility" off) instead of letting ImageMagick flatten
	// the layers.
	RequireComposite bool `yaml:"require_composite" env:"REQUIRE_COMPOSITE"`
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

// AutoQuality configures quality search (docs/FORMATS.md "Auto-quality").
// The per-format maps override Target, Min and Max for their format; a
// variable replaces the whole default map.
type AutoQuality struct {
	Method       AutoQualityMethod  `yaml:"method"        env:"METHOD"`
	Target       float64            `yaml:"target"        env:"TARGET"`
	Min          int                `yaml:"min"           env:"MIN"`
	Max          int                `yaml:"max"           env:"MAX"`
	AllowedError float64            `yaml:"allowed_error" env:"ALLOWED_ERROR"`
	FormatTarget map[Format]float64 `yaml:"format_target" env:"FORMAT_TARGET" envKeyValSeparator:"="`
	FormatMin    map[Format]int     `yaml:"format_min"    env:"FORMAT_MIN"    envKeyValSeparator:"="`
	FormatMax    map[Format]int     `yaml:"format_max"    env:"FORMAT_MAX"    envKeyValSeparator:"="`
}

// Limits bound the work a single request may cause.
type Limits struct {
	MaxSrcBytes             ByteSize `yaml:"max_src_bytes"              env:"MAX_SRC_BYTES"`
	MaxSrcPixels            int64    `yaml:"max_src_pixels"             env:"MAX_SRC_PIXELS"`
	MaxResultDimension      int      `yaml:"max_result_dimension"       env:"MAX_RESULT_DIMENSION"`
	MaxAnimationFrames      int      `yaml:"max_animation_frames"       env:"MAX_ANIMATION_FRAMES"`
	MaxAnimationFramePixels int64    `yaml:"max_animation_frame_pixels" env:"MAX_ANIMATION_FRAME_PIXELS"`
	MaxAnimationPixels      int64    `yaml:"max_animation_pixels"       env:"MAX_ANIMATION_PIXELS"`
	MaxRawDemosaicPixels    int64    `yaml:"max_raw_demosaic_pixels"    env:"MAX_RAW_DEMOSAIC_PIXELS"`
	MaxSVGBytes             ByteSize `yaml:"max_svg_bytes"              env:"MAX_SVG_BYTES"`
	MaxSVGDepth             int      `yaml:"max_svg_depth"              env:"MAX_SVG_DEPTH"`
	MaxSVGElements          int      `yaml:"max_svg_elements"           env:"MAX_SVG_ELEMENTS"`
	MaxPDFPages             int      `yaml:"max_pdf_pages"              env:"MAX_PDF_PAGES"`
	// MaxWatermarkTextPixels bounds the area a text watermark is rendered
	// on: larger text is shrunk to fit.
	MaxWatermarkTextPixels int64 `yaml:"max_watermark_text_pixels" env:"MAX_WATERMARK_TEXT_PIXELS"`
}

// Video configures video sources: a frame of an MP4, MOV, WebM, MKV or AVI
// file becomes the image (docs/FORMATS.md §1.5). Only the full image
// decodes video (FFmpeg, build tag ffmpeg; ADR 0010).
type Video struct {
	// Enabled: auto decodes video when the build has FFmpeg (the full
	// image) and refuses it otherwise; true fails startup without FFmpeg;
	// false refuses video sources (422) in every image. Unlike the other
	// Tristates it stays auto after Load: the application resolves it
	// against the build.
	Enabled Tristate `yaml:"enabled" env:"ENABLED"`
	// MaxBytes caps a video source; limits.max_src_bytes applies first, at
	// fetch.
	MaxBytes ByteSize `yaml:"max_bytes" env:"MAX_BYTES"`
	// MaxDuration caps the container duration.
	MaxDuration time.Duration `yaml:"max_duration" env:"MAX_DURATION"`
	// MaxPixels caps the width × height of the video stream (the decoder
	// refuses larger frames too).
	MaxPixels int64 `yaml:"max_pixels" env:"MAX_PIXELS"`
	// MaxAnimationFrames caps the frames a video_thumbnail_tile sprite or a
	// video_thumbnail_animation decodes; larger grids and animations are
	// cut to it (limits.max_animation_frames still applies to the result).
	MaxAnimationFrames int `yaml:"max_animation_frames" env:"MAX_ANIMATION_FRAMES"`

	Thumbnail VideoThumbnail `yaml:"thumbnail" envPrefix:"THUMBNAIL_"`
	Encode    VideoEncode    `yaml:"encode"    envPrefix:"ENCODE_"`
	Transform VideoTransform `yaml:"transform" envPrefix:"TRANSFORM_"`
}

// VideoTransform configures video transformations (WP-121, ADR 0012): a
// video source transcoded into MP4, WebM or an audio file. They run in a
// worker pool of their own, separate from processing.workers, and only in
// images with FFmpeg (full, gpl).
type VideoTransform struct {
	// Enabled serves video transformations; true fails startup on an
	// image without FFmpeg (slim).
	Enabled bool `yaml:"enabled" env:"ENABLED"`
	// Workers sizes the pool: 0 is a quarter of GOMAXPROCS (at least 1).
	// It bounds the probes run at once and gives the defaults of Capacity
	// and MaxJobs.
	Workers int `yaml:"workers" env:"WORKERS"`
	// Capacity is the cost units (estimated milliseconds of one core's
	// work, WP-121c) the running transcodes may hold together; 0 is
	// Workers × 10000. A transcode costing more runs alone.
	Capacity int64 `yaml:"capacity" env:"CAPACITY"`
	// MaxJobs bounds the transcodes running at once whatever their cost;
	// 0 is 2 × Workers.
	MaxJobs int `yaml:"max_jobs" env:"MAX_JOBS"`
	// Queue is how many transcodes may wait beyond MaxJobs (429 beyond).
	Queue int `yaml:"queue" env:"QUEUE"`
	// QueueCost bounds the cost units the waiting transcodes add up to
	// (429 beyond; one always waits behind nothing); 0 is
	// max(Queue, 1) × 10000.
	QueueCost int64 `yaml:"queue_cost" env:"QUEUE_COST"`
	// QueueTimeout is how long one waits, probe included (503 after).
	QueueTimeout time.Duration `yaml:"queue_timeout" env:"QUEUE_TIMEOUT"`
	// Timeout bounds one transcode, from admission (504 after); a video
	// request's whole budget is fetch.timeout + queue_timeout + timeout.
	Timeout time.Duration `yaml:"timeout" env:"TIMEOUT"`
	// Threads is the decoder and encoder threads of one transcode.
	Threads int `yaml:"threads" env:"THREADS"`
	// MaxInputBytes, MaxInputDuration and MaxInputPixels bound the source
	// (422); the fetcher accepts sources up to the larger of this and
	// limits.max_src_bytes for video requests.
	MaxInputBytes    ByteSize      `yaml:"max_input_bytes"    env:"MAX_INPUT_BYTES"`
	MaxInputDuration time.Duration `yaml:"max_input_duration" env:"MAX_INPUT_DURATION"`
	MaxInputPixels   int64         `yaml:"max_input_pixels"   env:"MAX_INPUT_PIXELS"`
	// MaxOutputBytes, MaxOutputDuration and MaxOutputPixels bound the
	// output (422); MaxFPS caps the frame rate (a faster source is reduced
	// to it, a faster request refused).
	MaxOutputBytes    ByteSize      `yaml:"max_output_bytes"    env:"MAX_OUTPUT_BYTES"`
	MaxOutputDuration time.Duration `yaml:"max_output_duration" env:"MAX_OUTPUT_DURATION"`
	MaxOutputPixels   int64         `yaml:"max_output_pixels"   env:"MAX_OUTPUT_PIXELS"`
	MaxFPS            float64       `yaml:"max_fps"             env:"MAX_FPS"`
	// UnsignedPolicy governs video transformations without a verified
	// signature (ADR 0012 §8): deny (the default) answers them 403 whatever
	// the mount's unsigned policy; allow leaves them to the mount's policy
	// (security.unsigned_policy or the dialect's own; restricted refuses
	// them, its allowlists describe images); restricted admits only what
	// the URL's video presets make of it (WP-132), plus the
	// UnsignedAllowedOptions and a container of UnsignedFormats, on a
	// mount whose policy is not deny.
	UnsignedPolicy UnsignedPolicy `yaml:"unsigned_policy" env:"UNSIGNED_POLICY"`

	// Video presets (WP-132, ADR 0012 §8).
	//
	// Presets maps a video preset name to /video options in imgproxy's
	// preset syntax, "/"-separated name:args with the imgproxy dialect's
	// arguments separator
	// (XI_VIDEO_TRANSFORM_PRESETS="web=rs:fit:1280:0/q:70,clip=vd:10/f:webm").
	// URLs name them with pr: (imgproxy /video, query preset=), t_
	// (Cloudinary) or n- (ImageKit); "default" applies to every video
	// transformation first. Separate from presets (image options).
	Presets map[string]string `yaml:"presets" env:"PRESETS" envKeyValSeparator:"="`
	// UnsignedAllowedOptions are the options an unsigned request may set
	// besides its presets under the restricted policy: start, end,
	// duration, mute. Empty allows none.
	UnsignedAllowedOptions []string `yaml:"unsigned_allowed_options" env:"UNSIGNED_ALLOWED_OPTIONS"`
	// UnsignedFormats are the containers an unsigned restricted request
	// may pick (extension or format) instead of its presets' one, of the
	// same kind (video, audio only, waveform picture).
	UnsignedFormats []string `yaml:"unsigned_formats" env:"UNSIGNED_FORMATS"`
	// UnsignedMaxDuration bounds the cut an unsigned restricted request
	// sets with start, end or duration (it must then have a duration or an
	// end); 0 leaves only max_output_duration.
	UnsignedMaxDuration time.Duration `yaml:"unsigned_max_duration" env:"UNSIGNED_MAX_DURATION"`

	// HLS and DASH (WP-126, ADR 0012 §6).
	//
	// ABRLadder lists the renditions a stream offers, as <height>[:<bit
	// rate>] items ("1080:5000k", "720"): the short side of the frame in
	// pixels and the peak video bit rate in bits per second (k and m
	// suffixes; without one, the standard rate of that height). Rungs
	// larger than the source are dropped.
	ABRLadder []string `yaml:"abr_ladder" env:"ABR_LADDER"`
	// SegmentDuration is the length of a stream's segments, whole seconds
	// from 1 to 30.
	SegmentDuration time.Duration `yaml:"segment_duration" env:"SEGMENT_DURATION"`
	// PrefetchSegments is how many segments after a requested one are
	// transcoded ahead, only when the video pool has room for them at
	// once (0 is off).
	PrefetchSegments int `yaml:"prefetch_segments" env:"PREFETCH_SEGMENTS"`
	// StreamTTL is how long the child URLs of a manifest (its playlists
	// and segments) work.
	StreamTTL time.Duration `yaml:"stream_ttl" env:"STREAM_TTL"`
	// StreamKeys seal the tickets of child URLs (hex, at least 16 bytes;
	// the first seals, every one opens). Empty derives a key from the first
	// signing key, else a random one per process (then child URLs work only
	// on the instance that wrote the manifest, until it restarts).
	StreamKeys []HexKey `yaml:"stream_keys" env:"STREAM_KEYS"`
	// StreamCacheBytes bounds the in-memory cache of stream probes and
	// segments in front of the result cache (0 is off).
	StreamCacheBytes ByteSize `yaml:"stream_cache_bytes" env:"STREAM_CACHE_BYTES"`

	// Spool files (WP-121b, ADR 0012 §5).
	//
	// SpoolDir holds the temporary files of large sources and outputs
	// (created 0700 if missing, cleared of the files a previous process
	// left at start); it must be writable (a tmpfs or emptyDir with a
	// read-only root file system). Empty is <os.TempDir()>/xtandard-video
	// (SpoolPath).
	SpoolDir string `yaml:"spool_dir" env:"SPOOL_DIR"`
	// SpoolThreshold: a source or an output larger than this goes to a
	// spool file instead of memory; 0 keeps everything in memory.
	SpoolThreshold ByteSize `yaml:"spool_threshold" env:"SPOOL_THRESHOLD"`
	// SpoolMaxBytes bounds the spool files' bytes at once (a request that
	// would exceed it answers 503 with Retry-After); 0 is no bound.
	SpoolMaxBytes ByteSize `yaml:"spool_max_bytes" env:"SPOOL_MAX_BYTES"`
	// SpoolTTL is how long a spooled source is shared (the segments of a
	// stream, repeated transformations) before the origin is asked again,
	// and how long it is kept without use; 0 shares nothing.
	SpoolTTL time.Duration `yaml:"spool_ttl" env:"SPOOL_TTL"`
}

// SpoolPath is the spool directory: SpoolDir, or xtandard-video in the
// system's temporary directory.
func (t *VideoTransform) SpoolPath() string {
	if t.SpoolDir != "" {
		return t.SpoolDir
	}
	return filepath.Join(os.TempDir(), "xtandard-video")
}

// VideoEncode configures MP4, WebM and HEVC output (docs/FORMATS.md §5.1):
// the codec is the build's (VP9 for WebM; H.264 for MP4 in the gpl image,
// AV1 in the full image; HEVC only in gpl), these are its settings.
type VideoEncode struct {
	// CRF of each codec when the request sets no quality; a request
	// quality maps onto the codec's scale around it (80 is the CRF, 100 a
	// quarter of it, 1 the worst).
	VP9CRF  int `yaml:"vp9_crf"  env:"VP9_CRF"`
	AV1CRF  int `yaml:"av1_crf"  env:"AV1_CRF"`
	H264CRF int `yaml:"h264_crf" env:"H264_CRF"`
	HEVCCRF int `yaml:"hevc_crf" env:"HEVC_CRF"`
	// VP9Speed and AV1Speed are libvpx's and libaom's cpu-used in their
	// realtime modes (higher is faster and larger).
	VP9Speed int `yaml:"vp9_speed" env:"VP9_SPEED"`
	AV1Speed int `yaml:"av1_speed" env:"AV1_SPEED"`
	// H264Preset and HEVCPreset are the x264 and x265 presets.
	H264Preset string `yaml:"h264_preset" env:"H264_PRESET"`
	HEVCPreset string `yaml:"hevc_preset" env:"HEVC_PRESET"`
	// MaxFrames, MaxDuration and MaxPixels (per frame) bound what is
	// encoded; more is 422 limit_exceeded.
	MaxFrames   int           `yaml:"max_frames"   env:"MAX_FRAMES"`
	MaxDuration time.Duration `yaml:"max_duration" env:"MAX_DURATION"`
	MaxPixels   int64         `yaml:"max_pixels"   env:"MAX_PIXELS"`
}

// VideoThumbnail holds the frame-selection defaults of video thumbnails.
type VideoThumbnail struct {
	// Second is the frame taken when a request sets no
	// video_thumbnail_second: a timestamp in seconds (negative counts
	// from the end), or auto for best-frame selection.
	Second VideoSecond `yaml:"second" env:"SECOND"`
	// Keyframes takes the keyframe at or before the timestamp instead of
	// the exact frame (imgproxy IMGPROXY_VIDEO_THUMBNAIL_KEYFRAMES).
	Keyframes bool `yaml:"keyframes" env:"KEYFRAMES"`
	// Samples is the number of keyframes best-frame selection compares.
	Samples int `yaml:"samples" env:"SAMPLES"`
	// Span is how much of the start of a video best-frame selection
	// samples.
	Span time.Duration `yaml:"span" env:"SPAN"`
}

// Vips configures libvips.
type Vips struct {
	Concurrency int    `yaml:"concurrency" env:"CONCURRENCY"`
	FailOn      FailOn `yaml:"fail_on"     env:"FAIL_ON"`
}

// Detect configures the face and object detection runtime (internal/detect,
// WP-71): ONNX Runtime loaded at startup and the YuNet/YOLOX models.
type Detect struct {
	// Enabled: auto runs detection when the library and the models are
	// found (the full image) and without it otherwise (slim); true fails
	// startup when they are missing; false never loads them.
	Enabled Tristate `yaml:"enabled" env:"ENABLED"`
	// Library is the ONNX Runtime shared library, a path or a file name the
	// dynamic linker resolves.
	Library   string `yaml:"library"    env:"LIBRARY"`
	ModelsDir string `yaml:"models_dir" env:"MODELS_DIR"`
	// FaceModel and ObjectModel are file names in ModelsDir (or absolute
	// paths); empty disables that model.
	FaceModel   string `yaml:"face_model"   env:"FACE_MODEL"`
	ObjectModel string `yaml:"object_model" env:"OBJECT_MODEL"`
	// Classify configures the image classifier of the /info classify
	// option (WP-110).
	Classify DetectClassify `yaml:"classify" envPrefix:"CLASSIFY_"`
	// Threads is ONNX Runtime's intra-op threads per inference.
	Threads int `yaml:"threads" env:"THREADS"`
	// Workers bounds concurrent inferences; 0 is GOMAXPROCS / Threads.
	Workers int `yaml:"workers" env:"WORKERS"`
	// MaxInput is the longest side fed to a model with a dynamic input
	// size; the shipped models are fixed at 640x640.
	MaxInput        int  `yaml:"max_input"        env:"MAX_INPUT"`
	VerifyChecksums bool `yaml:"verify_checksums" env:"VERIFY_CHECKSUMS"`
	// CacheSize bounds the per-source detection results kept in memory
	// (entries of one model on one source); 0 disables the cache.
	CacheSize int `yaml:"cache_size" env:"CACHE_SIZE"`
	// SmartFaces makes smart gravity crop on detected faces when there are
	// some (and the face model is loaded); false keeps libvips attention.
	SmartFaces bool `yaml:"smart_faces" env:"SMART_FACES"`
}

// DetectClassify configures image classification (internal/detect
// Classify): an ImageNet-style ONNX classifier, run on CPU only when a
// request asks for it (its session is created on first use).
type DetectClassify struct {
	// Model is a file name in Detect.ModelsDir (or an absolute path); empty
	// disables classification. A missing file disables classification
	// only, while detection keeps running.
	Model string `yaml:"model" env:"MODEL"`
	// Labels is the class names file of Model, one per line in output
	// order; empty uses the embedded ImageNet-1k names (1000 outputs).
	Labels string `yaml:"labels" env:"LABELS"`
	// Normalization scales the input: imagenet (mean/std, timm and
	// torchvision models), full ([-1, 1]), half ([0, 1]) or none (0-255).
	Normalization Normalization `yaml:"normalization" env:"NORMALIZATION"`
	// TopKMax caps the classes one request gets (classify:top_k).
	TopKMax int `yaml:"top_k_max" env:"TOP_K_MAX"`
	// Threshold drops classes whose probability is below it.
	Threshold float64 `yaml:"threshold" env:"THRESHOLD"`
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

// ResultCache configures the tiered result cache. It is on when Stores is
// not empty (Enabled).
type ResultCache struct {
	Stores      []StoreKind `yaml:"stores"       env:"STORES"`
	MemoryBytes ByteSize    `yaml:"memory_bytes" env:"MEMORY_BYTES"`
	// TTL is how long a result is served from the cache without
	// contacting the source.
	TTL time.Duration `yaml:"ttl" env:"TTL"`
	// WriteQueue is the most asynchronous cache writes in flight; more
	// are dropped (xi_cache_writes_dropped_total).
	WriteQueue int `yaml:"write_queue" env:"WRITE_QUEUE"`

	Disk  DiskCache     `yaml:"disk"  envPrefix:"DISK_"`
	Redis RedisCache    `yaml:"redis" envPrefix:"REDIS_"`
	S3    S3ResultCache `yaml:"s3"    envPrefix:"S3_"`
}

// Enabled reports whether any result cache tier is configured.
func (r *ResultCache) Enabled() bool { return len(r.Stores) > 0 }

// DiskCache configures the disk tier.
type DiskCache struct {
	Path  string   `yaml:"path"  env:"PATH"`
	Bytes ByteSize `yaml:"bytes" env:"BYTES"`
}

// RedisCache configures the redis tier.
type RedisCache struct {
	URL Secret `yaml:"url" env:"URL"` // may carry a password
	// Prefix namespaces every key on a shared server.
	Prefix string `yaml:"prefix" env:"PREFIX"`
	// MaxValueBytes is the largest value stored; larger entries are
	// skipped and counted.
	MaxValueBytes ByteSize `yaml:"max_value_bytes" env:"MAX_VALUE_BYTES"`
}

// S3ResultCache configures the s3 tier; Source names an s3 source whose
// credentials are reused.
type S3ResultCache struct {
	Source string `yaml:"source" env:"SOURCE"`
	Bucket string `yaml:"bucket" env:"BUCKET"`
	Prefix string `yaml:"prefix" env:"PREFIX"`
}

// OriginCache configures the in-memory original-bytes LRU and the
// optional shared stores behind it.
type OriginCache struct {
	MemoryBytes ByteSize      `yaml:"memory_bytes" env:"MEMORY_BYTES"`
	TTL         time.Duration `yaml:"ttl"          env:"TTL"`
	// NegativeTTL is how long a source not-found is remembered; 0 disables.
	NegativeTTL time.Duration `yaml:"negative_ttl" env:"NEGATIVE_TTL"`
	// Stores are shared tiers (disk, redis, s3; configured under
	// cache.result) backing the LRU under the orig/ namespace, so
	// replicas and restarts share originals. Empty disables them.
	Stores []StoreKind `yaml:"stores" env:"STORES"`
	// StoreTTL is how long an original is served from the shared stores
	// without contacting the source.
	StoreTTL time.Duration `yaml:"store_ttl" env:"STORE_TTL"`
}

// SharedStores reports every store kind the result or origin cache uses,
// each once, result order first.
func (c *Cache) SharedStores() []StoreKind {
	var out []StoreKind
	for _, k := range append(append([]StoreKind(nil), c.Result.Stores...), c.Origin.Stores...) {
		if !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out
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
		Role: RoleAll,
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
		Admin:      Admin{Addr: "127.0.0.1:9090"},
		Playground: Playground{Path: "/_playground"},
		Signing:    Signing{Size: 32},
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
			CloudflareImages: CloudflareImagesDialect{Mount: mount("/cdn-cgi/imagedelivery/")},
			Imgix:            mount("/imgix/"),
			Cloudinary:       CloudinaryDialect{Mount: mount("/cloudinary/")},
			Imagekit:         ImagekitDialect{Mount: mount("/imagekit/")},
			Bunny:            mount("/bunny/"),
			Thumbor:          mount("/thumbor/"),
			AWSSIH:           AWSSIHDialect{Mount: mount("/sih/"), QueryParams: true},
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
			WatermarkFont:     "sans 16",
		},
		Fallback: Fallback{Status: 200, TTL: time.Minute},
		Formats: Formats{
			Quality:              80,
			JPEG:                 JPEGFormat{Quality: 80, Progressive: true},
			WebP:                 WebPFormat{Quality: 80, Effort: 4},
			AVIF:                 AVIFFormat{Quality: 60, Effort: 4, MaxPixels: 16_000_000},
			HEIC:                 HEICFormat{Quality: 60},
			JXL:                  JXLFormat{Quality: 75, Effort: 5},
			GIF:                  GIFFormat{Effort: 7},
			PDF:                  PDFFormat{DPI: 72},
			EPS:                  EPSFormat{MaxProcs: 2, Timeout: 10 * time.Second, MaxDPI: 600, GSPath: "gs"},
			Metafile:             MetafileFormat{MaxProcs: 2, Timeout: 10 * time.Second, Path: "xi-metafile2svg"},
			RAW:                  RAWFormat{PreviewMinRatio: 1},
			HDR:                  HDRFormat{ToneMap: ToneMapReinhard},
			Auto:                 FormatSwitches{AVIF: true, WebP: true},
			Preferred:            []Format{FormatJPEG, FormatPNG, FormatGIF},
			SVGPassthrough:       true,
			SaveDataQualityDelta: -15,
			Best:                 BestFormat{MaxPixels: 8_000_000, ComplexityThreshold: 5.5},
			AutoQuality: AutoQuality{
				Method: AutoQualityNone, Target: 0.02, Min: 60, Max: 85, AllowedError: 0.001,
				FormatMin: map[Format]int{FormatJPEG: 40, FormatWebP: 40, FormatAVIF: 30},
				FormatMax: map[Format]int{FormatJPEG: 90, FormatWebP: 90, FormatAVIF: 80},
			},
		},
		Limits: Limits{
			MaxSrcBytes:             50 << 20,
			MaxSrcPixels:            50_000_000,
			MaxResultDimension:      8192,
			MaxAnimationFrames:      256,
			MaxAnimationFramePixels: 16_000_000,
			MaxAnimationPixels:      200_000_000,
			MaxRawDemosaicPixels:    120_000_000,
			MaxSVGBytes:             5 << 20,
			MaxSVGDepth:             256,
			MaxSVGElements:          200_000,
			MaxPDFPages:             500,
			MaxWatermarkTextPixels:  16_777_216,
		},
		Sandbox: Sandbox{MaxAS: 1 << 30, DetectEmulation: true},
		Video: Video{
			MaxBytes:           100 << 20,
			MaxDuration:        time.Hour,
			MaxPixels:          7680 * 4320, // 8K UHD
			MaxAnimationFrames: 100,
			Thumbnail:          VideoThumbnail{Second: VideoSecondAuto, Samples: 5, Span: 10 * time.Second},
			Encode: VideoEncode{
				VP9CRF: 36, AV1CRF: 36, H264CRF: 25, HEVCCRF: 28,
				VP9Speed: 8, AV1Speed: 8, H264Preset: "veryfast", HEVCPreset: "veryfast",
				MaxFrames: 1000, MaxDuration: time.Minute, MaxPixels: 3840 * 2160,
			},
			Transform: VideoTransform{
				Queue: 8, QueueTimeout: 30 * time.Second, Timeout: 2 * time.Minute, Threads: 1,
				MaxInputBytes: 512 << 20, MaxInputDuration: time.Hour, MaxInputPixels: 7680 * 4320,
				MaxOutputBytes: 256 << 20, MaxOutputDuration: 5 * time.Minute, MaxOutputPixels: 3840 * 2160,
				MaxFPS: 60, UnsignedPolicy: UnsignedDeny, UnsignedFormats: []string{"mp4", "webm"},
				ABRLadder:       []string{"1080:5000k", "720:2800k", "480:1400k", "360:800k", "240:400k"},
				SegmentDuration: 6 * time.Second, PrefetchSegments: 2, StreamTTL: 12 * time.Hour,
				StreamCacheBytes: 256 << 20,
				SpoolThreshold:   32 << 20, SpoolTTL: time.Minute,
			},
		},
		Vips: Vips{Concurrency: 1, FailOn: FailOnWarning},
		Detect: Detect{
			Library:     "libonnxruntime.so.1",
			ModelsDir:   "/usr/share/xtandard/models",
			FaceModel:   "face_detection_yunet_2023mar.onnx",
			ObjectModel: "yolox_s.onnx",
			Classify: DetectClassify{
				Model: "mobilenetv3_large_100.onnx", Normalization: NormalizeImageNet, TopKMax: 20,
			},
			Threads:    1,
			MaxInput:   640,
			CacheSize:  512,
			SmartFaces: true,
		},
		Cache: Cache{
			Headers:    CacheHeaders{TTL: 31_536_000 * time.Second, ErrorTTL: 10 * time.Second},
			TagsHeader: "Cache-Tag",
			Result: ResultCache{
				MemoryBytes: 256 << 20, TTL: 24 * time.Hour, WriteQueue: 64,
				Disk:  DiskCache{Bytes: 10 << 30},
				Redis: RedisCache{Prefix: "xi:", MaxValueBytes: 1 << 20},
			},
			Origin: OriginCache{MemoryBytes: 256 << 20, TTL: 60 * time.Second, NegativeTTL: 5 * time.Second, StoreTTL: 10 * time.Minute},
		},
		Observability: Observability{LogLevel: LogInfo},
	}
}

func mount(prefix string) Mount {
	return Mount{Prefix: prefix}
}
