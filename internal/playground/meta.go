package playground

import "strings"

// Signing schemes of a mount (Mount.Scheme): which signature the sign
// endpoint computes for URLs of that mount.
const (
	// SchemeImgproxy is the imgproxy path signature (XI_SIGNING_KEYS).
	SchemeImgproxy = "imgproxy"
	// SchemeXI is the xi_sig query parameter (XI_SIGNING_KEYS).
	SchemeXI = "xi_sig"
	// SchemeThumbor is the thumbor path signature (dialect keys).
	SchemeThumbor = "thumbor"
	// SchemeImgix is imgix's s query parameter (the mount's secure URL
	// token, XI_DIALECTS_IMGIX_KEYS).
	SchemeImgix = "imgix"
	// SchemeNone means the playground cannot sign URLs of the mount: its
	// dialect is unsigned or uses provider keys the playground does not
	// hold. URLs are returned as entered.
	SchemeNone = "none"
)

// URL grammars of a mount (Mount.Grammar): how the page builds the part of
// the URL after the mount prefix.
const (
	// GrammarImgproxy is /options.../plain/<source>, options from the
	// imgproxy catalogue.
	GrammarImgproxy = "imgproxy"
	// GrammarQuery is ?url=<source>&key=value, keys from the query
	// catalogue.
	GrammarQuery = "query"
	// GrammarImgix is /<source>?key=value, keys from the imgix
	// catalogue; the source is an object key on the mount's source or a
	// percent-encoded http(s) URL.
	GrammarImgix = "imgix"
	// GrammarCloudinary is /<cloud>/image/{fetch,upload}/<k_v,...>/<source>,
	// keys from the cloudinary catalogue.
	GrammarCloudinary = "cloudinary"
	// GrammarImagekit is /tr:<k-v,...>/<key>, keys from the imagekit
	// catalogue; the source is a key on the mount's source.
	GrammarImagekit = "imagekit"
	// GrammarCloudflare is /<k=v,...>/<source>, keys from the cloudflare
	// catalogue.
	GrammarCloudflare = "cloudflare"
	// GrammarWsrv is /?url=<source>&k=v, keys from the wsrv catalogue.
	GrammarWsrv = "wsrv"
	// GrammarThumbor is /<segments>/filters:<f(a)...>/<source>: segment
	// options and filters from the thumbor catalogue.
	GrammarThumbor = "thumbor"
	// GrammarBunny is /<key>?k=v, keys from the bunny catalogue; the
	// source is a key on the mount's source.
	GrammarBunny = "bunny"
	// GrammarAWSSIH is the base64 JSON request of AWS Serverless Image
	// Handler: {"key", "edits"} plus query parameters from the awssih
	// catalogue.
	GrammarAWSSIH = "awssih"
	// GrammarPath is free-form: the page takes the path and query after
	// the mount prefix as typed (the other dialects).
	GrammarPath = "path"
)

// Signing modes (Signing.Mode).
const (
	SigningOff      = "off"      // no XI_SIGNING_KEYS
	SigningOptional = "optional" // keys set, unsigned URLs fall under the unsigned policy
	SigningRequired = "required" // keys set and XI_SIGNING_REQUIRED
)

// Meta is the body of GET <path>/api/meta: everything the page needs to
// build URLs, and nothing secret. It never carries keys, salts, source
// credentials, source headers, base URLs or allowlists.
type Meta struct {
	// Version is the server build version.
	Version string `json:"version" doc:"Server build version."`
	// PathPrefix is XI_SERVER_PATH_PREFIX; generated URLs start with it.
	PathPrefix string `json:"path_prefix" doc:"XI_SERVER_PATH_PREFIX; every generated URL starts with it."`
	// PublicAddr is XI_SERVER_ADDR, from which the page guesses the public
	// base URL.
	PublicAddr string `json:"public_addr" doc:"XI_SERVER_ADDR (the page derives a default public base URL from its port)."`
	// Public reports XI_PLAYGROUND_PUBLIC.
	Public bool `json:"public" doc:"XI_PLAYGROUND_PUBLIC: the playground is also served on the public listener."`
	// Signing is the signing mode, never the keys.
	Signing Signing `json:"signing" doc:"Signing mode; keys and salts are never included."`
	// Mounts are the enabled dialect mounts.
	Mounts []Mount `json:"mounts" doc:"Enabled dialect mounts, in configuration order."`
	// Catalogues are the option catalogues, one per grammar.
	Catalogues []Catalogue `json:"catalogues" doc:"Option catalogues (internal/apidoc), one per URL grammar."`
	// InfoOptions is the imgproxy /info option catalogue for the Info
	// panel's builder.
	InfoOptions []Option `json:"info_options" doc:"imgproxy /info options (internal/apidoc, the ImgproxyInfoOptions schema) for the Info panel's builder."`
	// VideoOptions is the imgproxy-style /video option catalogue (WP-122,
	// WP-123).
	VideoOptions []Option `json:"video_options" doc:"imgproxy-style /video options (internal/apidoc, the ImgproxyVideoOptions schema): video transformations, audio and waveforms."`
	// Presets are the configured preset names.
	Presets []string `json:"presets" doc:"Configured preset names."`
	// Sources are the configured named sources (names and types only).
	Sources []Source `json:"sources" doc:"Named sources: name, type and the source strings each accepts (xi://<name>/<key>), never their settings."`
	// Formats are the output formats this server encodes.
	Formats []string `json:"formats" doc:"Output formats the server encodes."`
	// Samples are the built-in sample images (playground://samples/...),
	// the offline group of the gallery.
	Samples []Sample `json:"samples" doc:"Built-in sample images (repository fixtures), served as playground://samples/<name> to the playground only; they work offline."`
	// Gallery is the curated list of public sample files.
	Gallery []GallerySample `json:"gallery" doc:"Public sample files per input format and variant (docs/research/format-gap.md), fetched through the http sources."`
	// Uploads reports XI_PLAYGROUND_UPLOADS: api/upload accepts files.
	Uploads bool `json:"uploads" doc:"XI_PLAYGROUND_UPLOADS: api/upload accepts files (playground://uploads/<id>, 10 minutes)."`
	// MaxUploadBytes is the largest upload (XI_LIMITS_MAX_SRC_BYTES).
	MaxUploadBytes int64 `json:"max_upload_bytes" doc:"Largest upload in bytes (XI_LIMITS_MAX_SRC_BYTES)."`
	// Detection reports whether the detector is loaded (faces, objects).
	Detection bool `json:"detection" doc:"The detection models are loaded: face and object gravity, detect_objects."`
	// Video reports whether video sources can be decoded.
	Video bool `json:"video" doc:"Video sources are decoded (FFmpeg, full image)."`
	// Classes are the object classes gravity, crop and filters accept.
	Classes []string `json:"classes" doc:"Detection classes: face, the COCO class names and supercategories."`
	// QueryMethod reports XI_SERVER_QUERY_METHOD: query dialect mounts
	// accept HTTP QUERY (WP-147).
	QueryMethod bool `json:"query_method" doc:"XI_SERVER_QUERY_METHOD: the query dialect's endpoints accept HTTP QUERY (RFC 10008), its parameters in the request content."`
	// PDFPasswords reports whether api/sign can encrypt a pdf_password
	// (XI_ENCRYPTION_KEYS is set).
	PDFPasswords bool `json:"pdf_passwords" doc:"api/sign encrypts pdf_password for password-protected PDFs (XI_ENCRYPTION_KEYS is set); signed mounts only."`
	// Watermark reports that a default watermark image is configured
	// (XI_PROCESSING_WATERMARK_SOURCE), never which one.
	Watermark bool `json:"watermark" doc:"A default watermark image is configured (XI_PROCESSING_WATERMARK_SOURCE; the source itself is never included): imgproxy wm without wmu or wmt draws it."`
	// Recipes are the Examples tool's ready-made sessions.
	Recipes []Recipe `json:"recipes" doc:"Ready-made sessions of the Examples tool (internal/playground/recipes.json): source, dialect and options, loaded into the builder with one click."`
	// VideoTransform describes the video transformations this server runs
	// (WP-127); nil when they are off (XI_VIDEO_TRANSFORM_ENABLED, XI_ROLE).
	VideoTransform *VideoTransform `json:"video_transform,omitempty" doc:"Video transformations (/video, /q/video and the dialects' video URLs) this server runs: what this build can write and the output limits. Absent when XI_VIDEO_TRANSFORM_ENABLED is off or XI_ROLE=image."`
}

// VideoTransform is what the video subsystem of this server can do, for
// the playground's video mode: the codecs the build encodes (the gpl image
// has H.264 and HEVC, the full image AV1 for MP4), the containers, the
// optional filters and the output limits. It names no key or source.
type VideoTransform struct {
	// UnsignedPolicy is XI_VIDEO_TRANSFORM_UNSIGNED_POLICY.
	UnsignedPolicy string `json:"unsigned_policy" enum:"allow,restricted,deny" doc:"XI_VIDEO_TRANSFORM_UNSIGNED_POLICY: deny refuses unsigned video URLs (403) whatever the mount's policy; restricted admits only what their video presets make of them."`
	// Presets are the video preset names (XI_VIDEO_TRANSFORM_PRESETS).
	Presets []string `json:"presets,omitempty" doc:"Video preset names (XI_VIDEO_TRANSFORM_PRESETS; their options are not shown): pr:<name> in /video, preset=<name> in /q/video, t_<name> on Cloudinary videos, n-<name> on ImageKit videos."`
	// Codecs are the encoders of this build (video.TranscodeCaps.Names).
	Codecs []string `json:"codecs" doc:"Encoders of this build: vp9, av1, h264, hevc (video), aac, opus, mp3, flac, pcm (audio), and waveform when waveform pictures can be drawn."`
	// Containers are the output containers this build can write.
	Containers []string `json:"containers" doc:"Output containers this build writes (mp4, webm, m4a, mp3, opus, ogg, flac, wav; png and webp are waveform pictures; m3u8 and mpd are HLS and DASH streams)."`
	// MP4Video is the video codec of mp4 with video_codec auto.
	MP4Video string `json:"mp4_video,omitempty" doc:"Video codec of an mp4 output with video_codec auto: h264 (gpl image) or av1 (full image)."`
	// Overlays, Timecode and AudioEffects report the optional filters.
	Overlays     bool `json:"overlays,omitempty" doc:"Image and text overlays (wm, wmu, wmt with overlay_window and overlay_fade)."`
	Timecode     bool `json:"timecode,omitempty" doc:"Timecode overlays (timecode, FFmpeg drawtext)."`
	AudioEffects bool `json:"audio_effects,omitempty" doc:"Audio volume, loudnorm and fades."`
	// MaxOutputDuration, MaxOutputPixels and MaxFPS are the output limits
	// (XI_VIDEO_TRANSFORM_MAX_*); 0 is unlimited.
	MaxOutputDuration float64 `json:"max_output_duration,omitempty" doc:"XI_VIDEO_TRANSFORM_MAX_OUTPUT_DURATION in seconds."`
	MaxOutputPixels   int     `json:"max_output_pixels,omitempty" doc:"XI_VIDEO_TRANSFORM_MAX_OUTPUT_PIXELS."`
	MaxFPS            float64 `json:"max_fps,omitempty" doc:"XI_VIDEO_TRANSFORM_MAX_FPS."`
	// Probe reports that api/probe and api/probe/result describe media
	// files (FFmpeg).
	Probe bool `json:"probe,omitempty" doc:"api/probe (a source) and api/probe/result (a result body) describe media files: container, duration, codecs, frame rate, sample rate and channels."`
}

// Signing is the signing configuration without key material.
type Signing struct {
	Mode           string `json:"mode" enum:"off,optional,required" doc:"off: no XI_SIGNING_KEYS; optional: keys set, unsigned URLs fall under the unsigned policy; required: XI_SIGNING_REQUIRED."`
	UnsignedPolicy string `json:"unsigned_policy" enum:"allow,restricted,deny" doc:"XI_SECURITY_UNSIGNED_POLICY."`
}

// Mount is one dialect mount the page can build URLs for.
type Mount struct {
	// ID names the mount in sign requests: the router mount name
	// ("imgproxy", "query", "thumbor[1]").
	ID string `json:"id" doc:"Mount name; the mount field of a sign request."`
	// Dialect is the dialect key.
	Dialect string `json:"dialect" doc:"Dialect name."`
	// Prefix is the path prefix ("/", "/q/"); empty for host-only mounts.
	Prefix string `json:"prefix,omitempty" doc:"Path prefix; empty for host-only mounts."`
	// Prefixes are every path prefix the mount answers on, Prefix first,
	// when it has more than one (a simple provider's native prefixes and
	// /simple/<name>/): the router lists the mount once per prefix, the
	// page once.
	Prefixes []string `json:"prefixes,omitempty" doc:"Every path prefix of the mount, prefix first, when it has more than one (simple providers: native prefixes and /simple/<name>/)."`
	// Hosts are the host bindings.
	Hosts []string `json:"hosts,omitempty" doc:"Host bindings."`
	// Source is the named source relative keys resolve against.
	Source string `json:"source,omitempty" doc:"Named source relative keys resolve against."`
	// UnsignedPolicy is the effective unsigned policy of the mount.
	UnsignedPolicy string `json:"unsigned_policy" enum:"allow,restricted,deny" doc:"Effective unsigned policy."`
	// Scheme is the signature the sign endpoint computes (Scheme*).
	Scheme string `json:"scheme" enum:"imgproxy,xi_sig,thumbor,imgix,none" doc:"Signature the sign endpoint computes for this mount."`
	// Keyed reports whether the server holds a key for Scheme: when false,
	// URLs are returned unsigned (with the scheme's placeholder).
	Keyed bool `json:"keyed" doc:"The server holds a key for the scheme; false returns unsigned URLs."`
	// Grammar is how the page builds URLs (Grammar*).
	Grammar string `json:"grammar" enum:"imgproxy,query,imgix,cloudinary,imagekit,cloudflare,wsrv,thumbor,bunny,awssih,path" doc:"URL grammar the page builds with the catalogue of the same name; path is free-form."`
	// ArgumentsSeparator separates imgproxy option arguments (":").
	ArgumentsSeparator string `json:"arguments_separator,omitempty" doc:"imgproxy argument separator (XI_DIALECTS_IMGPROXY_ARGUMENTS_SEPARATOR)."`
	// Example is a path (and query) after the prefix, for the free-form
	// grammar.
	Example string `json:"example,omitempty" doc:"Example path and query after the prefix (free-form grammar)."`
}

// Source is a named source without its settings.
type Source struct {
	Name string `json:"name" doc:"Source name."`
	Type string `json:"type" doc:"Source type (http, s3, gcs, azure, fs, ftp, sftp)."`
	// Listable reports that api/sources/<name>/list browses it (fs).
	Listable bool `json:"listable,omitempty" doc:"api/sources/<name>/list browses this source (fs sources)."`
	// Syntax lists the source strings the source accepts, the named form
	// first ("…" is the part to fill in). It names no host, bucket, base
	// url or path of the configuration (SourceSyntax).
	Syntax []string `json:"syntax" doc:"Source strings this source accepts, the named form first; … is the part to fill in (xi://web/https://…, https://…, local:///…). Configured hosts, buckets and base URLs are never shown."`
	// Prefix is what the source picker pre-fills when the source is
	// picked: Syntax[0] up to the "…".
	Prefix string `json:"prefix" doc:"What the source picker pre-fills when this source is picked (the first syntax up to …)."`
}

// SourceSyntax returns Source.Syntax and Source.Prefix for a named source
// of type typ. hasBase reports an http base_url, hasBucket an s3/gcs/azure
// default bucket (docs/URL-DIALECTS.md §3.1, WP-95):
//
//   - http without base_url: xi://<name>/https://… (the absolute URL is
//     checked against the source's allowlist), https://…
//   - http with base_url: xi://<name>/… (joined to the base url), https://…
//   - fs: xi://<name>/…, local:///…
//   - s3, gcs, azure: xi://<name>/… (default bucket only), then the scheme
//     form with the bucket
//   - ftp, sftp: xi://<name>/…, ftp://<host>/…
func SourceSyntax(name, typ string, hasBase, hasBucket bool) (syntax []string, prefix string) {
	xi := "xi://" + name + "/"
	switch typ {
	case "http":
		if hasBase {
			syntax = []string{xi + "…", "https://…"}
		} else {
			syntax = []string{xi + "https://…", "https://…"}
		}
	case "fs":
		syntax = []string{xi + "…", "local:///…"}
	case "s3", "gcs", "azure":
		scheme := map[string]string{"s3": "s3://<bucket>/…", "gcs": "gs://<bucket>/…", "azure": "abs://<container>/…"}[typ]
		if hasBucket {
			syntax = []string{xi + "…", scheme}
		} else {
			syntax = []string{scheme}
		}
	case "ftp", "sftp":
		syntax = []string{xi + "…", typ + "://<host>/…"}
	default:
		syntax = []string{xi + "…"}
	}
	prefix, _, _ = strings.Cut(syntax[0], "<")
	prefix, _, _ = strings.Cut(prefix, "…")
	return syntax, prefix
}

// Catalogue is the option catalogue of one URL grammar.
type Catalogue struct {
	Grammar string   `json:"grammar" enum:"imgproxy,query,imgix,cloudinary,imagekit,cloudflare,wsrv,thumbor,bunny,awssih" doc:"The grammar the options belong to."`
	Options []Option `json:"options" doc:"Options, in documentation order."`
}

// Option is one catalogue entry.
type Option struct {
	Name      string   `json:"name" doc:"Canonical name."`
	Aliases   []string `json:"aliases,omitempty" doc:"Accepted short names."`
	Doc       string   `json:"doc,omitempty" doc:"One-line description."`
	Forms     []Form   `json:"forms" doc:"Accepted argument shapes; most options have one."`
	Example   string   `json:"example,omitempty" doc:"A valid argument string."`
	Milestone string   `json:"milestone,omitempty" doc:"Milestone that implements the option."`
	Security  bool     `json:"security,omitempty" doc:"Needs XI_SECURITY_ALLOW_SECURITY_OPTIONS."`
	// Group is the builder category.
	Group string `json:"group,omitempty" enum:"size,gravity,format,color,effects,watermark,metadata,frames,detection,delivery,presets,other,report,hashes,security,time,audio" doc:"Builder category; report, hashes and security are Info panel groups, time and audio video builder groups."`
	// Default is the value the server uses when the option is absent
	// (info options only).
	Default string `json:"default,omitempty" doc:"Value used when the option is absent, as imgproxy documents it (info options)."`
}

// Form is one argument shape.
type Form struct {
	Args     []Arg `json:"args" doc:"Leading positional arguments; trailing ones may be omitted."`
	Rest     []Arg `json:"rest,omitempty" doc:"A group of arguments repeated after args."`
	MinItems int   `json:"min_items,omitempty" doc:"Least number of arguments (0 means 1)."`
}

// Arg is one positional argument.
type Arg struct {
	Name     string   `json:"name" doc:"Argument name."`
	Type     string   `json:"type" enum:"string,integer,number,boolean,enum,color,base64,direction" doc:"Argument type."`
	Enum     []string `json:"enum,omitempty" doc:"Accepted values of an enum argument."`
	Min      *float64 `json:"min,omitempty" doc:"Lower bound of a numeric argument."`
	Max      *float64 `json:"max,omitempty" doc:"Upper bound of a numeric argument."`
	Optional bool     `json:"optional,omitempty" doc:"May be left empty."`
	Pattern  string   `json:"pattern,omitempty" doc:"Regular expression for string arguments."`
	Doc      string   `json:"doc,omitempty" doc:"Description."`
}

// SignRequest is the body of POST <path>/api/sign.
type SignRequest struct {
	// Mount is a Mount.ID.
	Mount string `json:"mount" doc:"Mount ID from api/meta (the dialect name for its primary mount)."`
	// Path is the escaped path after the mount prefix, starting with "/"
	// ("/rs:fill:300:200/plain/https://example.com/a.jpg"); for the
	// imgproxy and thumbor schemes, without the signature segment.
	Path string `json:"path" minLength:"1" maxLength:"8192" doc:"Escaped path after the mount prefix, starting with /; without the signature segment for imgproxy and thumbor."`
	// Query is the raw query without "?"; any xi_sig is replaced.
	Query string `json:"query,omitempty" maxLength:"8192" doc:"Raw query without ?; an existing xi_sig is replaced."`
	// Prefix picks one of the mount's Prefixes; empty means Prefix.
	Prefix string `json:"prefix,omitempty" maxLength:"512" doc:"One of the mount's prefixes; empty means its first prefix."`
	// PDFPassword is the plain-text password of a password-protected PDF
	// source (WP-115): the server encrypts it into the URL's pdf_password
	// option. Never logged or echoed.
	PDFPassword string `json:"pdf_password,omitempty" maxLength:"1024" doc:"Plain-text password of a password-protected PDF source: the server encrypts it (XI_ENCRYPTION_KEYS) into a pdf_password option (imgproxy mounts: a pdp segment first in the path; the query mount: a pdf_password parameter) and signs the result. Needs a signed mount; never logged or echoed."`
}

// SignResponse is the body of a successful sign request.
type SignResponse struct {
	// URL is the path and query to request on the public listener,
	// starting with the path prefix.
	URL string `json:"url" doc:"Path and query on the public listener (prepend the public base URL)."`
	// Signed reports whether a signature was computed.
	Signed bool `json:"signed" doc:"A signature was computed; false when the server has no key for the mount."`
	// Scheme is the mount's scheme.
	Scheme string `json:"scheme" enum:"imgproxy,xi_sig,thumbor,imgix,none" doc:"The mount's signing scheme."`
}

// ErrorResponse is the body of a failed playground API request.
type ErrorResponse struct {
	Error string `json:"error" doc:"Client-safe message."`
}
