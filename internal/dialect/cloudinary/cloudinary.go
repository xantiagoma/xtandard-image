// Package cloudinary parses Cloudinary delivery URLs (docs/URL-DIALECTS.md
// §6), mounted at /cloudinary/ by default or host-bound:
//
//	/<cloud>/image/<type>/[s--SIG--/][<transformation>/]*[v<digits>/]<public_id>[.<ext>]
//	/image/<type>/[s--SIG--/][<transformation>/]*[v<digits>/]<public_id>[.<ext>]
//
// The second shape is the private-CDN / custom-domain form (unpic
// privateRegex): the cloud is not in the path and the source comes from
// the mount. The first segment is taken as the asset type when it is
// "image" or "video" and the second is a delivery type; otherwise it is
// the cloud name.
//
// Delivery types: upload, private and authenticated take a public ID, a key
// relative to the cloud's source (Options.Clouds maps cloud → named
// source; without a mapping the mount's source applies); private and
// authenticated additionally require a verified signature. fetch takes an
// absolute http(s) URL (percent-encoded as the SDKs emit it), which the
// source allowlist then governs. Other delivery types and the video and raw
// asset types are 400.
//
// Each transformation segment is a component: comma-separated key_value
// parameters (w_400,c_fill,g_auto). A path segment is a component when
// every parameter has the key_value shape and at least one key is a
// Cloudinary parameter; the first segment that is not, or a version
// segment (v<digits>), starts the public ID. Components apply in URL
// order: a component with pipeline parameters (geometry, effects, named
// transformations) starts a chained pipeline once the current one holds
// anything, so "c_fill,w_400/e_blur" is two pipelines while
// "c_fill,w_400/q_auto/f_auto/b_white" is one; layers are watermarks on
// the current pipeline. See params.go and layer.go for the parameter
// mapping. The public ID's extension sets the output format unless f_ is
// given.
//
// Signatures (s--SIG--) are verified with sign.CloudinaryVerifier over the
// path after the signature segment with the version segment removed, as
// the SDKs sign. Unsigned requests are left to the router's unsigned
// policy. The query string (SDK analytics "_a=") is ignored.
package cloudinary

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/preset"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Name is the dialect identifier.
const Name = "cloudinary"

// Delivery types.
const (
	typeUpload        = "upload"
	typeFetch         = "fetch"
	typePrivate       = "private"
	typeAuthenticated = "authenticated"
)

// ErrUnknownCloud is the cause of the 403 for a cloud name that
// Options.Clouds does not map.
var ErrUnknownCloud = errors.New("cloudinary: unknown cloud")

// Options configures the parser. The zero value is a working unsigned,
// non-strict parser without cloud mappings.
type Options struct {
	// Verifier checks s--…-- signatures (sign.NewCloudinaryVerifier). nil
	// means signing is off: signature segments are skipped unchecked and
	// private/authenticated URLs are rejected.
	Verifier sign.Verifier
	// Clouds maps cloud names to named sources
	// (XI_DIALECTS_CLOUDINARY_CLOUDS). A cloud missing from a non-empty map
	// is 403; with an empty map the mount's source applies to every cloud.
	Clouds map[string]string
	// Strict rejects unknown and unsupported parameters with 400
	// (XI_DIALECTS_STRICT) instead of recording them in Request.Ignored.
	Strict bool
	// Presets expands t_<name> named transformations (imgproxy option
	// syntax). nil records the names in Delivery.Presets unexpanded. In
	// only-presets mode every parameter other than t_ is 403.
	Presets *preset.Store
	// ArgumentsSeparator is the separator Presets are written with (the
	// imgproxy dialect's); "" means ":".
	ArgumentsSeparator string
}

// Parser is the Cloudinary dialect. It is safe for concurrent use.
type Parser struct {
	o   Options
	opt *imgproxy.Parser // option semantics for presets and pipelines only
}

var _ dialect.Dialect = (*Parser)(nil)

// New returns a Parser; nil means the zero Options.
func New(opts *Options) (*Parser, error) {
	var o Options
	if opts != nil {
		o = *opts
	}
	for cloud, src := range o.Clouds {
		if cloud == "" || src == "" {
			return nil, fmt.Errorf("cloudinary: cloud %q maps to source %q; both must be non-empty", cloud, src)
		}
	}
	ip, err := imgproxy.New(&imgproxy.Options{
		ArgumentsSeparator: o.ArgumentsSeparator,
		Strict:             o.Strict,
		Presets:            o.Presets,
	})
	if err != nil {
		return nil, fmt.Errorf("cloudinary: %w", err)
	}
	return &Parser{o: o, opt: ip}, nil
}

// Name implements dialect.Dialect.
func (*Parser) Name() string { return Name }

// Verifier implements dialect.Dialect.
func (p *Parser) Verifier() sign.Verifier { return p.o.Verifier }

// Parse implements dialect.Dialect. It uses in.Path and in.Mount.Source.
func (p *Parser) Parse(in *dialect.Input) (*dialect.Result, error) {
	return p.ParsePath(in.Path, in.Mount.Source)
}

// parsedURL is a Cloudinary delivery path split into its parts. Every string is
// still percent-encoded.
type parsedURL struct {
	cloud     string // "" in the private-CDN form
	delivery  string
	signature string   // between "s--" and "--"; "" = none
	comps     []string // transformation components in URL order
	source    string   // public ID (with extension) or fetch URL
	message   string   // what the signature covers
}

// ParsePath parses the escaped path after the mount prefix; mountSource is
// the mount's named source ("" = none).
func (p *Parser) ParsePath(path, mountSource string) (*dialect.Result, error) {
	u, err := splitPath(path)
	if err != nil {
		return nil, err
	}
	sigResult, err := p.verify(u)
	if err != nil {
		return nil, err
	}
	mount, err := p.cloudSource(u.cloud)
	if err != nil {
		return nil, err
	}
	if mount == "" {
		mount = mountSource
	}

	b := p.opt.NewBuilder(Name)
	a := newApplier(p, b, mount)
	if err := a.presetPrologue(); err != nil {
		return nil, err
	}
	src, err := a.source(u)
	if err != nil {
		return nil, err
	}
	if err := a.components(u.comps); err != nil {
		return nil, err
	}
	a.finishFormat()
	if err := b.Finish(); err != nil {
		return nil, err
	}
	r := b.Request()
	r.Source = src
	return &dialect.Result{Request: r, SourceString: src.Raw, Signature: sigResult}, nil
}

// splitPath splits the escaped path into its grammar parts.
func splitPath(path string) (*parsedURL, error) {
	rest := strings.TrimPrefix(path, "/")
	first, rest, _ := strings.Cut(rest, "/")
	second, afterSecond, _ := strings.Cut(rest, "/")
	u := &parsedURL{}
	var asset string
	if isAssetType(first) && isDeliveryType(second) {
		asset, u.delivery, rest = first, second, afterSecond
	} else {
		if first == "" {
			return nil, dialect.InvalidURL("missing cloud name")
		}
		u.cloud = first
		asset = second
		u.delivery, rest, _ = strings.Cut(afterSecond, "/")
	}
	switch asset {
	case "image":
	case "":
		return nil, dialect.InvalidURL("missing asset type")
	default:
		return nil, dialect.InvalidURL("unsupported asset type")
	}
	switch u.delivery {
	case typeUpload, typeFetch, typePrivate, typeAuthenticated:
	case "":
		return nil, dialect.InvalidURL("missing delivery type")
	default:
		return nil, dialect.InvalidURL("unsupported delivery type")
	}

	if seg, tail, _ := strings.Cut(rest, "/"); strings.HasPrefix(seg, "s--") {
		sig, ok := sign.CloudinarySignature(seg)
		if !ok {
			return nil, errs.Wrap(errs.KindUnauthorized, sign.ErrMalformedSignature)
		}
		u.signature, rest = sig, tail
	}

	// Components, then an optional version, then the source. The message
	// is the rest with the version segment removed.
	txEnd := 0 // length of rest covered by components (including their "/")
	for rest[txEnd:] != "" {
		seg, _, found := strings.Cut(rest[txEnd:], "/")
		if u.delivery == typeFetch && isURLStart(seg) {
			break
		}
		if isVersion(seg) && found {
			u.source = rest[txEnd+len(seg)+1:]
			if u.source == "" {
				return nil, dialect.InvalidURL("missing public id")
			}
			break
		}
		if !found || !isComponent(seg) {
			break
		}
		u.comps = append(u.comps, seg)
		txEnd += len(seg) + 1
	}
	if u.source == "" {
		u.source = rest[txEnd:]
	}
	if u.source == "" {
		return nil, dialect.InvalidURL("missing public id")
	}
	u.message = rest[:txEnd] + u.source
	return u, nil
}

func isAssetType(s string) bool { return s == "image" || s == "video" || s == "raw" }

func isDeliveryType(s string) bool {
	switch s {
	case typeUpload, typeFetch, typePrivate, typeAuthenticated,
		"sprite", "facebook", "twitter", "twitter_name", "youtube", "vimeo",
		"gravatar", "instagram", "list", "multi", "text", "asset":
		return true
	}
	return false
}

// isVersion reports whether seg is v<digits>.
func isVersion(seg string) bool {
	if len(seg) < 2 || seg[0] != 'v' {
		return false
	}
	for i := 1; i < len(seg); i++ {
		if seg[i] < '0' || seg[i] > '9' {
			return false
		}
	}
	return true
}

// isURLStart reports whether a fetch path segment starts the remote URL
// ("https:", "http:", or their percent-encoded forms).
func isURLStart(seg string) bool {
	s := strings.ToLower(seg)
	return s == "http:" || s == "https:" || strings.HasPrefix(s, "http%3a") || strings.HasPrefix(s, "https%3a")
}

// verify checks the signature segment and enforces signatures on private
// and authenticated delivery types.
func (p *Parser) verify(u *parsedURL) (sign.Result, error) {
	res := sign.Unsigned
	if p.o.Verifier != nil {
		var err error
		if res, err = p.o.Verifier.Verify(u.signature, u.message); err != nil {
			return sign.Unsigned, err
		}
	}
	if (u.delivery == typePrivate || u.delivery == typeAuthenticated) && !res.Signed() {
		return sign.Unsigned, &errs.Error{
			Kind:   errs.KindUnauthorized,
			Msg:    "delivery type " + u.delivery,
			Public: "signature required",
			Err:    sign.ErrMissingSignature,
		}
	}
	return res, nil
}

// cloudSource maps a cloud name to its source.
func (p *Parser) cloudSource(cloud string) (string, error) {
	if cloud == "" || len(p.o.Clouds) == 0 {
		return "", nil
	}
	if src, ok := p.o.Clouds[cloud]; ok {
		return src, nil
	}
	return "", &errs.Error{
		Kind:   errs.KindForbidden,
		Msg:    "cloud " + truncate(cloud),
		Public: "unknown cloud",
		Err:    ErrUnknownCloud,
	}
}

// source decodes the public ID or fetch URL.
func (a *applier) source(u *parsedURL) (transform.SourceSpec, error) {
	raw, err := url.PathUnescape(u.source)
	if err != nil {
		return transform.SourceSpec{}, dialect.InvalidURL("invalid path escaping")
	}
	if !imgproxy.PlausibleSource(raw) {
		return transform.SourceSpec{}, dialect.InvalidURL("invalid source")
	}
	if u.delivery == typeFetch {
		fixed, ok := fetchURL(raw)
		if !ok {
			return transform.SourceSpec{}, dialect.InvalidURL("fetch source must be an http(s) url")
		}
		return transform.SourceSpec{Raw: fixed, Encoding: transform.SourcePlain}, nil
	}
	if err := checkKey(raw); err != nil {
		return transform.SourceSpec{}, err
	}
	if strings.HasSuffix(raw, "/") {
		return transform.SourceSpec{}, dialect.InvalidURL("missing public id")
	}
	a.extension(raw)
	return transform.SourceSpec{Raw: raw, Encoding: transform.SourceRelative, Mount: a.mount}, nil
}

// fetchURL accepts http(s) URLs, repairing a slash collapsed by a proxy
// ("https:/host" → "https://host").
func fetchURL(s string) (string, bool) {
	scheme, rest, ok := strings.Cut(s, ":")
	if !ok {
		return "", false
	}
	switch strings.ToLower(scheme) {
	case "http", "https":
	default:
		return "", false
	}
	if !strings.HasPrefix(rest, "//") {
		rest = "/" + rest
	}
	if len(rest) <= 2 || rest[2] == '/' {
		return "", false
	}
	return s[:len(scheme)] + ":" + rest, true
}

// checkKey rejects public IDs (and layer or default-image IDs) that are not
// plain relative keys: a scheme-like first segment ("https:", "s3:") would
// otherwise be resolved as an absolute reference, escaping the cloud's
// source.
func checkKey(key string) error {
	if key == "" {
		return dialect.InvalidURL("missing public id")
	}
	if key[0] == '/' {
		return dialect.InvalidURL("invalid public id")
	}
	first, _, _ := strings.Cut(key, "/")
	if strings.Contains(first, ":") {
		return dialect.InvalidURL("invalid public id")
	}
	return nil
}

// maxQuoted bounds attacker-controlled text copied into internal messages.
const maxQuoted = 64

func truncate(s string) string {
	if len(s) > maxQuoted {
		return s[:maxQuoted]
	}
	return s
}
