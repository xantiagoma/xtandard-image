// Package imgix parses the imgix Rendering API URL dialect
// (docs/URL-DIALECTS.md §5, https://docs.imgix.com/apis/rendering):
//
//	/<object key>?w=400&h=300&fit=crop&crop=faces,entropy&auto=format&s=<md5>
//	/<percent-encoded http(s) URL>?w=400&s=<md5>              (web proxy source)
//
// The path after the mount is the object key under the mount's bound source
// (the imgix "source" is our mount): the result's source is the escaped key
// without its leading "/", SourceRelative, with Source.Mount set to the
// mount's source. A path whose decoded form starts with http:// or https://
// is a web proxy source and becomes a plain absolute URL, subject to the
// source allowlist and the unsigned policy like any other.
//
// Parameters are applied by name, not in order (the last occurrence of a
// name wins); every parameter may also be written base64url-encoded under
// its name plus "64" (txt64, mark64, bg64, ...). Unknown parameters and the
// parameters imgix offers that xi deliberately does not (AI features,
// blending, halftone, ...) are recorded in Request.Ignored, or rejected
// with 400 in strict mode. Parameters that take effect in a later milestone
// are parsed into the request and recorded in Ignored with that milestone.
// ixlib and ixid (SDK and analytics tags) are accepted silently.
//
// Signing: s = hex(MD5(token ‖ path ‖ "?" ‖ query-without-s))
// (sign.ImgixMessage, sign.NewImgixVerifier). expires (unix seconds) makes
// the URL a 404 errs.KindExpired once passed; it is checked after the
// signature, so an attacker cannot learn anything from it.
//
// Semantics and approximations, per parameter, are documented on the
// functions in params.go; the summary lives in docs/URL-DIALECTS.md §5.
package imgix

import (
	"encoding/base64"
	"net/url"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Name is the dialect identifier.
const Name = "imgix"

// Options configures the parser. The zero value is a working unsigned,
// non-strict parser.
type Options struct {
	// Verifier checks the s parameter (sign.NewImgixVerifier). nil means
	// signing is off.
	Verifier sign.Verifier
	// Strict rejects unknown and unsupported parameters with 400
	// (XI_DIALECTS_STRICT).
	Strict bool
	// Now is the clock for expires; nil means time.Now.
	Now func() time.Time
}

// Parser is the imgix dialect. It is safe for concurrent use.
type Parser struct {
	o  Options
	ip *imgproxy.Parser // request builder and shared finishing rules only
}

var _ dialect.Dialect = (*Parser)(nil)

// New returns a Parser; nil means the zero Options.
func New(opts *Options) (*Parser, error) {
	var o Options
	if opts != nil {
		o = *opts
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	ip, err := imgproxy.New(&imgproxy.Options{Strict: o.Strict, Now: o.Now})
	if err != nil {
		return nil, err
	}
	return &Parser{o: o, ip: ip}, nil
}

// Name implements dialect.Dialect.
func (*Parser) Name() string { return Name }

// Verifier implements dialect.Dialect.
func (p *Parser) Verifier() sign.Verifier { return p.o.Verifier }

// Parse implements dialect.Dialect. It uses in.Path, in.RawQuery and
// in.Mount.Source.
func (p *Parser) Parse(in *dialect.Input) (*dialect.Result, error) {
	res, err := p.ParseURL(in.Path, in.RawQuery)
	if err != nil {
		return nil, err
	}
	r := res.Request
	if r.Source.Encoding == transform.SourceRelative {
		r.Source.Mount = in.Mount.Source
	}
	for i := range r.Pipelines[0].Watermarks {
		if s := r.Pipelines[0].Watermarks[i].Source; s != nil && s.Encoding == transform.SourceRelative {
			s.Mount = in.Mount.Source
		}
	}
	return res, nil
}

// ParseURL parses the escaped path after the mount prefix (starting with
// "/") and the raw query, for example ("/photos/a.jpg", "w=400&fit=crop").
func (p *Parser) ParseURL(path, rawQuery string) (*dialect.Result, error) {
	if path == "" {
		path = "/"
	}
	sig, msg, err := sign.ImgixMessage(path, rawQuery)
	if err != nil {
		return nil, err
	}
	sigResult := sign.Unsigned
	if p.o.Verifier != nil {
		if sigResult, err = p.o.Verifier.Verify(sig, msg); err != nil {
			return nil, err
		}
	}
	src, enc, err := source(path)
	if err != nil {
		return nil, err
	}
	params, err := parseQuery(rawQuery)
	if err != nil {
		return nil, err
	}
	b := p.ip.NewBuilder(Name)
	st := &state{p: p, b: b, r: b.Request(), pl: b.Pipeline(), vals: make(map[string]string, len(params))}
	if err := st.collect(params); err != nil {
		return nil, err
	}
	if err := st.apply(); err != nil {
		return nil, err
	}
	if err := b.Finish(); err != nil {
		return nil, err
	}
	r := b.Request()
	r.Source = transform.SourceSpec{Raw: src, Encoding: enc}
	return &dialect.Result{Request: r, SourceString: src, Signature: sigResult}, nil
}

// source turns the escaped path into the source reference: a web proxy
// URL (decoded) or an object key (still escaped, without the leading "/").
func source(path string) (string, transform.SourceEncoding, error) {
	key := strings.TrimPrefix(path, "/")
	if key == "" {
		return "", 0, dialect.InvalidURL("missing source path")
	}
	decoded, err := url.PathUnescape(key)
	if err != nil {
		return "", 0, dialect.InvalidURL("invalid path escaping")
	}
	if !imgproxy.PlausibleSource(decoded) {
		return "", 0, dialect.InvalidURL("invalid source path")
	}
	if l := strings.ToLower(decoded); strings.HasPrefix(l, "http:") || strings.HasPrefix(l, "https:") {
		return decoded, transform.SourcePlain, nil
	}
	// A ":" in the first segment would read as a scheme to the source
	// resolver; escape it (RFC 3986 segment-nz-nc).
	first, rest, found := strings.Cut(key, "/")
	if strings.Contains(first, ":") {
		first = strings.ReplaceAll(first, ":", "%3A")
		if found {
			key = first + "/" + rest
		} else {
			key = first
		}
	}
	return key, transform.SourceRelative, nil
}

// param is one decoded query parameter.
type param struct {
	key, value string
}

// b64Suffix marks a base64url-encoded parameter value.
const b64Suffix = "64"

// parseQuery decodes rawQuery into parameters in their original order,
// decoding "<name>64" values from base64url into "<name>". Empty segments
// are skipped; a malformed escape or base64 value is a 400. s is dropped
// (ParseURL verified it).
func parseQuery(rawQuery string) ([]param, error) {
	params := make([]param, 0, strings.Count(rawQuery, "&")+1)
	for part := range strings.SplitSeq(rawQuery, "&") {
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		if k == sign.ImgixParam {
			continue
		}
		key, err := url.QueryUnescape(k)
		if err != nil {
			return nil, dialect.InvalidURL("invalid query escaping")
		}
		value, err := url.QueryUnescape(v)
		if err != nil {
			return nil, dialect.InvalidURL("invalid query escaping")
		}
		if base, ok := strings.CutSuffix(key, b64Suffix); ok && base != "" && base != sign.ImgixParam {
			if value, err = decodeBase64(key, value); err != nil {
				return nil, err
			}
			key = base
		}
		params = append(params, param{key, value})
	}
	return params, nil
}

// decodeBase64 decodes a base64url value (RFC 4648 §5, padding optional).
// The standard alphabet is accepted too, since "+" and "/" cannot be
// mistaken for anything else.
func decodeBase64(field, v string) (string, error) {
	s := strings.TrimRight(v, "=")
	s = strings.NewReplacer("+", "-", "/", "_").Replace(s)
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return "", dialect.InvalidArgument(field, v, "base64url string")
	}
	return string(b), nil
}
