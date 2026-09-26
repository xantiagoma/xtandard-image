package playground

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
	// Presets are the configured preset names.
	Presets []string `json:"presets" doc:"Configured preset names."`
	// Sources are the configured named sources (names and types only).
	Sources []Source `json:"sources" doc:"Named sources, name and type only (xi://<name>/<key>)."`
	// Formats are the output formats this server encodes.
	Formats []string `json:"formats" doc:"Output formats the server encodes."`
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
	Grammar string `json:"grammar" enum:"imgproxy,query,imgix,path" doc:"URL grammar: imgproxy options path, query keys, or free-form path."`
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
}

// Catalogue is the option catalogue of one URL grammar.
type Catalogue struct {
	Grammar string   `json:"grammar" enum:"imgproxy,query,imgix" doc:"The grammar the options belong to."`
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
