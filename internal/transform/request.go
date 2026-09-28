// Package transform is the normalised request model every URL dialect parses
// into and every later stage (planner, executor, encoder, cache, server)
// consumes (docs/ARCHITECTURE.md §4).
//
// The package is plain data: no I/O, no dependencies on other internal
// packages. Zero values mean "not set". The intended order of use is
//
//	r := dialect.Parse(...)       // fills only what the URL says
//	r.ApplyDefaults(defaults)     // materialises effective values
//	err := r.Validate()           // range checks; *ValidationError → 400
//	key := r.CanonicalKey()       // result-cache key and ETag input
//
// Normalisation rules every dialect relies on:
//
//   - Blur and sharpen are Gaussian sigma. Dialects convert their radius units.
//   - Colour adjustments are multipliers with 1 = unchanged, except Brightness
//     (additive −255..255, like imgproxy) and Hue (additive degrees).
//   - Dimensions are CSS pixels before DPR; the planner multiplies by DPR once.
//   - Rotate is always a multiple of 90; any other angle goes into RotateAny.
package transform

import "time"

// Request is one image request after dialect parsing.
type Request struct {
	Kind        Kind
	Dialect     string `key:"-"` // for metrics and logging only
	Source      SourceSpec
	Pipelines   []Pipeline // len ≥ 1 after ApplyDefaults; > 1 means chained pipelines (imgproxy "-", cloudinary "/")
	Output      Output
	Delivery    Delivery
	ClientHints ClientHints    // which client hints this URL opts into; the server resolves them before CanonicalKey
	Limits      LimitOverrides // honoured only with XI_SECURITY_ALLOW_SECURITY_OPTIONS
	Info        *InfoOptions   // Kind == KindInfo
	Ignored     []Ignored      `key:"-"` // options understood syntactically but not applied (debug header)
}

// SourceSpec identifies the source image.
type SourceSpec struct {
	Raw      string         // as written: "https://…", "s3://b/k", "local:///p", "xi://name/key", or a relative key
	Encoding SourceEncoding // how Raw was encoded in the URL
	Mount    string         // named source bound by a dialect mount (imgix source, cloudinary cloud, …)
	Hashsum  *Hashsum       // expected source digest
}

// Hashsum is an expected source digest (imgproxy hashsum).
type Hashsum struct {
	Algorithm HashAlgorithm
	Digest    string // lowercase hex
}

// ClientHints records which request hints a URL asks to honour (imgix ch=,
// cloudinary w_auto/dpr_auto, cloudflare width=auto). The server replaces them
// with concrete Resize values before computing CanonicalKey.
type ClientHints struct {
	Width     bool // Sec-CH-Width / Width → Resize.Width
	DPR       bool // Sec-CH-DPR / DPR → Resize.DPR
	SaveData  bool // Save-Data and effective-connection-type hints lower the quality (FORMATS §6.2)
	WidthStep int  // round hinted widths up to a multiple of this (cloudinary w_auto:100)
}

// LimitOverrides are per-request security limits (imgproxy max_* options).
// Zero means "use the config value".
type LimitOverrides struct {
	MaxSrcResolution            float64 // megapixels
	MaxSrcFileSize              int64   // bytes
	MaxAnimationFrames          int
	MaxAnimationFrameResolution float64 // megapixels
	MaxResultDimension          int     // px
}

// Ignored is an option the dialect understood syntactically but does not apply.
type Ignored struct {
	Option string // the option as written, for example "gif_options"
	Reason string // short, static, safe to put in a debug header
}

// InfoOptions mirrors imgproxy's info options (docs/URL-DIALECTS.md §3.3).
type InfoOptions struct {
	Schema         InfoSchema
	Size           bool
	Format         bool
	Dimensions     bool
	EXIF           bool
	IPTC           bool
	XMP            bool
	VideoMeta      bool
	Colorspace     bool
	Bands          bool
	SampleFormat   bool
	PagesNumber    bool
	Alpha          bool
	Crop           *Crop // report the crop the given options would produce
	ObjectsPos     bool
	Palette        int // number of palette colours; 0 = off
	Average        *InfoAverage
	DominantColors *InfoDominantColors
	Blurhash       *InfoBlurhash
	Thumbhash      bool
	PHash          bool
	CalcHashsums   []HashAlgorithm
	// DetectObjects reports the detected faces and objects (imgproxy Pro
	// detect_objects, imgix fm=json with faces=1), limited to
	// DetectClasses when set (see Gravity.Classes).
	DetectObjects bool
	DetectClasses []string
	// ClassifyTopK reports the image's ClassifyTopK best classes (imgproxy
	// Pro classify, WP-110); 0 is off. ClassifyClasses restricts them to
	// those class names of the classifier (none = every class).
	ClassifyTopK    int
	ClassifyClasses []string
}

// InfoAverage configures the info average colour.
type InfoAverage struct {
	IgnoreTransparent bool
}

// InfoDominantColors configures info dominant colours.
type InfoDominantColors struct {
	BuildMissed bool
}

// InfoBlurhash configures info blurhash components (1..9 each).
type InfoBlurhash struct {
	XComponents, YComponents int
}

// Delivery holds response-level options that do not change pixels.
type Delivery struct {
	Filename        string
	Attachment      *bool // Content-Disposition: attachment; nil = config default
	CacheBuster     string
	Expires         time.Time     // zero = never
	TTL             time.Duration // per-request max-age (thumbor max_age, wsrv maxage), clamped by config
	Immutable       bool          // add "immutable" to Cache-Control (cloudinary fl_immutable_cache)
	Fallback        *SourceSpec   // per-request fallback image
	OnErrorRedirect bool          // cloudflare onerror=redirect
	SkipProcessing  []Format      // source formats streamed unmodified
	BypassCache     bool          `key:"-"` // skip the result cache (needs XI_CACHE_ALLOW_BYPASS); never changes the output
	CacheTags       []string      // Cache-Tag / Surrogate-Key response header values
	Presets         []string      `key:"-"` // for logging; already expanded
	Encoding        ResponseEncoding
}
