// Package bunny parses the Bunny Optimizer dialect (docs/URL-DIALECTS.md
// §9): Bunny's Dynamic Images API query parameters on a pull-zone path,
// mounted at /bunny/<mount>/ or bound to a host.
//
//	/a.jpg?width=400&aspect_ratio=16:9&quality=80&format=webp
//	/a.jpg?class=thumbnail&token=HS256-…&expires=1598024587
//	/bcdn_token=HS256-…&expires=1598024587&token_path=%2Fimg%2F/img/a.jpg
//
// The path after the mount is the source: a key relative to the mount's
// bound source (Input.Mount.Source), never an absolute URL. Parameters are
// unordered, as on Bunny: when one appears twice the last value wins, and
// options are applied in Bunny's documented order (class first, then crop,
// resize, orientation, filters, colour, format and quality). The mapping
// to transform.Request is in options.go.
//
// Token authentication follows sign.BunnyMessage: the token (query token,
// or the bcdn_token path segment) signs the decoded path, or token_path,
// the expires value and every other parameter, including the Optimizer
// options. A verified token with token_path only authorises paths under
// that prefix. expires is enforced for every URL that carries it (404
// KindExpired once passed). token_countries, token_countries_blocked and
// limit are signed but not enforced (no geo lookup, no rate limiting) and
// are recorded in Request.Ignored.
package bunny

import (
	"net/url"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/preset"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Name is the dialect identifier.
const Name = "bunny"

// Options configures the parser. The zero value is a working unsigned,
// non-strict parser.
type Options struct {
	// Verifier checks Bunny tokens (sign.NewBunnyVerifier with the mount's
	// token authentication keys). nil means token authentication is off.
	Verifier *sign.BunnyVerifier
	// Presets resolves class=<name> (Bunny image classes) to xi presets.
	// In only-presets mode ("forced transformation classes") any other
	// Optimizer option is a 403. nil records class names unexpanded.
	Presets *preset.Store
	// ArgumentsSeparator is the imgproxy separator the presets use; "" = ":".
	ArgumentsSeparator string
	// Strict rejects unknown parameters with 400 (XI_DIALECTS_STRICT).
	Strict bool
	// Now is the clock for expires; nil means time.Now.
	Now func() time.Time
}

// Parser is the Bunny dialect. It is safe for concurrent use.
type Parser struct {
	o   Options
	opt *imgproxy.Parser // option semantics only; never parses a path
}

var _ dialect.Dialect = (*Parser)(nil)

// New returns a Parser; nil means the zero Options.
func New(opts *Options) (*Parser, error) {
	var o Options
	if opts != nil {
		o = *opts
	}
	ip, err := imgproxy.New(&imgproxy.Options{
		ArgumentsSeparator: o.ArgumentsSeparator,
		Strict:             o.Strict,
		Now:                o.Now,
		Presets:            o.Presets,
	})
	if err != nil {
		return nil, err
	}
	return &Parser{o: o, opt: ip}, nil
}

// Name implements dialect.Dialect.
func (*Parser) Name() string { return Name }

// Verifier implements dialect.Dialect.
func (p *Parser) Verifier() sign.Verifier {
	if p.o.Verifier == nil {
		return nil
	}
	return p.o.Verifier
}

// Parse implements dialect.Dialect. It uses in.Path, in.RawQuery and
// in.Mount.Source.
func (p *Parser) Parse(in *dialect.Input) (*dialect.Result, error) {
	res, err := p.ParseURL(in.Path, in.RawQuery)
	if err != nil {
		return nil, err
	}
	res.Request.Source.Mount = in.Mount.Source
	return res, nil
}

// ParseURL parses the escaped path after the mount prefix and the raw
// query, for example ("/img/a.jpg", "width=300&token=…&expires=…").
func (p *Parser) ParseURL(path, rawQuery string) (*dialect.Result, error) {
	path, pathParams, err := pathToken(path)
	if err != nil {
		return nil, err
	}
	key, decoded, err := sourceKey(path)
	if err != nil {
		return nil, err
	}
	params, err := decodePairs(pathParams, rawQuery)
	if err != nil {
		return nil, err
	}
	sigResult, expires, err := p.verify(decoded, params)
	if err != nil {
		return nil, err
	}

	b := p.opt.NewBuilder(Name)
	if expires != "" {
		if err := b.Apply("expires", expires); err != nil {
			return nil, err
		}
	}
	if err := p.apply(b, params); err != nil {
		return nil, err
	}
	if err := b.Finish(); err != nil {
		return nil, err
	}
	r := b.Request()
	r.Source = transform.SourceSpec{Raw: key, Encoding: transform.SourcePlain}
	return &dialect.Result{Request: r, SourceString: key, Signature: sigResult}, nil
}

// pathToken splits a leading "/bcdn_token=…&expires=…" segment (Bunny
// path-based tokens) off the escaped path and decodes its parameters.
func pathToken(path string) (string, []sign.BunnyParam, error) {
	rest := strings.TrimPrefix(path, "/")
	if !strings.HasPrefix(rest, sign.BunnyPathTokenParam+"=") {
		return path, nil, nil
	}
	seg, tail, _ := strings.Cut(rest, "/")
	params, err := decodePairs(nil, seg)
	if err != nil {
		return "", nil, err
	}
	return "/" + tail, params, nil
}

// sourceKey returns the relative source key for the escaped path (no
// leading slash, a ':' in the first segment escaped so the key never reads
// as a URL scheme) and the decoded path Bunny signs.
func sourceKey(path string) (key, decoded string, err error) {
	if path == "" || path[0] != '/' {
		path = "/" + path
	}
	decoded, uerr := url.PathUnescape(path)
	if uerr != nil {
		return "", "", dialect.InvalidURL("invalid path escaping")
	}
	key = strings.TrimLeft(path, "/")
	if key == "" {
		return "", "", dialect.InvalidURL("missing source")
	}
	if !imgproxy.PlausibleSource(decoded) {
		return "", "", dialect.InvalidURL("invalid source")
	}
	first, rest, found := strings.Cut(key, "/")
	if strings.Contains(first, ":") {
		key = strings.ReplaceAll(first, ":", "%3A")
		if found {
			key += "/" + rest
		}
	}
	return key, decoded, nil
}

// decodePairs appends the decoded key=value pairs of s to dst in order.
// Empty segments are skipped; a malformed escape is a 400.
func decodePairs(dst []sign.BunnyParam, s string) ([]sign.BunnyParam, error) {
	for part := range strings.SplitSeq(s, "&") {
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		key, err := url.QueryUnescape(k)
		if err != nil {
			return nil, dialect.InvalidURL("invalid query escaping")
		}
		value, err := url.QueryUnescape(v)
		if err != nil {
			return nil, dialect.InvalidURL("invalid query escaping")
		}
		dst = append(dst, sign.BunnyParam{Key: key, Value: value})
	}
	return dst, nil
}

// verify checks the token and token_path scope and returns the expires
// value. decodedPath is the path Bunny signs.
func (p *Parser) verify(decodedPath string, params []sign.BunnyParam) (sign.Result, string, error) {
	var token, expires, tokenPath string
	var nToken, nExpires int
	for _, kv := range params {
		switch kv.Key {
		case sign.BunnyTokenParam, sign.BunnyPathTokenParam:
			token = kv.Value
			nToken++
		case sign.BunnyExpiresParam:
			expires = kv.Value
			nExpires++
		case sign.BunnyTokenPathParam:
			tokenPath = kv.Value
		}
	}
	if nToken > 1 || nExpires > 1 {
		return sign.Unsigned, "", errs.Wrap(errs.KindUnauthorized, sign.ErrMalformedSignature)
	}
	if p.o.Verifier == nil || !p.o.Verifier.Enabled() {
		return sign.Unsigned, expires, nil
	}
	// A token without an expiry would never expire; Bunny requires both.
	if token != "" && expires == "" {
		return sign.Unsigned, "", errs.Wrap(errs.KindUnauthorized, sign.ErrMalformedSignature)
	}
	var msg string
	if token != "" {
		var err error
		if msg, err = sign.BunnyMessage(token, decodedPath, expires, params); err != nil {
			return sign.Unsigned, "", err
		}
	}
	res, err := p.o.Verifier.Verify(token, msg)
	if err != nil {
		return sign.Unsigned, "", err
	}
	if res.Signed() && tokenPath != "" && !strings.HasPrefix(decodedPath, tokenPath) {
		return sign.Unsigned, "", errs.Wrap(errs.KindUnauthorized, sign.ErrSignatureMismatch)
	}
	return res, expires, nil
}
