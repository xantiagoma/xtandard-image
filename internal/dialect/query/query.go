// Package query parses the query dialect (docs/URL-DIALECTS.md §4), mounted
// at /q/ by default. It has two shapes, chosen by the path after the mount:
//
//	/?url=<source>&w=300&h=200&fit=cover&g=smart&f=webp&q=80&o=<imgproxy options>
//	/<endpoint>?url=<source>&width=300&height=200&type=webp   (imaginary)
//
// Native mode (empty path) maps each key to the equivalent imgproxy option
// through imgproxy.Builder, so argument rules and semantics are imgproxy's;
// o= applies an imgproxy option path ("rs:fill:300:200/g:sm", "-" chains
// pipelines) and makes the dialect complete by construction. Keys are
// applied in query order and later keys override earlier ones, as later
// options do in an imgproxy path; keys after an o= that chains pipelines
// apply to the last pipeline.
//
// Endpoint mode implements imaginary's GET API (.refs/imaginary params.go,
// image.go, bimg resizer.go): the endpoint picks the operation and the
// imaginary params configure it; /pipeline chains one pipeline per JSON
// operation. See imaginary.go for the mapping and its approximations.
//
// Signing: the imaginary sign parameter (sign.ImaginaryMessage over the
// decoded path after the mount, e.g. "/resize") or xi_sig
// (sign.XIMessage over "/" + the escaped path after the mount), whichever is
// present; both at once is a 403. Without either, each configured verifier
// decides between Unsigned and ErrMissingSignature.
//
// Deliberate differences from imaginary, all stricter: malformed values are
// a 400 naming the parameter (imaginary silently falls back for gravity,
// extend, colours), negative numbers are rejected (imaginary takes the
// absolute value), url may appear once, and the file parameter (local
// paths) is rejected: use an fs source via xi://name/key instead.
package query

import (
	"net/url"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Name is the dialect identifier.
const Name = "query"

// Query parameters with a fixed meaning in both shapes.
const (
	paramURL  = "url"
	paramFile = "file"
	paramO    = "o"
)

// Reasons recorded in Request.Ignored.
const (
	reasonUnknown        = "unknown option"
	reasonNotImplemented = "not implemented"
	reasonM3             = "takes effect in M3"
	reasonApproximated   = "approximated"
)

// Options configures the parser. The zero value is a working unsigned,
// non-strict parser.
type Options struct {
	// ImaginaryVerifier checks the sign parameter (sign.NewImaginaryVerifier).
	// nil means imaginary signing is off.
	ImaginaryVerifier sign.Verifier
	// XIVerifier checks the xi_sig parameter (sign.NewXIVerifier). nil means
	// xi_sig signing is off.
	XIVerifier sign.Verifier
	// ArgumentsSeparator separates arguments inside o= (the imgproxy
	// dialect's XI_DIALECTS_IMGPROXY_ARGUMENTS_SEPARATOR); "" means ":".
	ArgumentsSeparator string
	// Strict rejects unknown keys with 400 (XI_DIALECTS_STRICT).
	Strict bool
	// AllowSecurityOptions permits imgproxy security options inside o=.
	AllowSecurityOptions bool
	// Now is the clock for expires; nil means time.Now.
	Now func() time.Time
}

// Parser is the query dialect. It is safe for concurrent use.
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
		ArgumentsSeparator:   o.ArgumentsSeparator,
		Strict:               o.Strict,
		AllowSecurityOptions: o.AllowSecurityOptions,
		Now:                  o.Now,
	})
	if err != nil {
		return nil, err
	}
	return &Parser{o: o, opt: ip}, nil
}

// Name implements dialect.Dialect.
func (*Parser) Name() string { return Name }

// Verifier implements dialect.Dialect. It returns the imaginary verifier,
// the dialect's native scheme; xi_sig is checked with Options.XIVerifier.
func (p *Parser) Verifier() sign.Verifier { return p.o.ImaginaryVerifier }

// Parse implements dialect.Dialect. It uses in.Path and in.RawQuery.
func (p *Parser) Parse(in *dialect.Input) (*dialect.Result, error) {
	return p.ParseURL(in.Path, in.RawQuery)
}

// ParseURL parses the escaped path after the mount prefix and the raw
// query, for example ("/", "url=https://e.com/a.jpg&w=300") or
// ("/resize", "url=...&width=300").
func (p *Parser) ParseURL(path, rawQuery string) (*dialect.Result, error) {
	endpoint := strings.Trim(path, "/")
	decoded, err := url.PathUnescape(endpoint)
	if err != nil {
		return nil, dialect.InvalidURL("invalid path escaping")
	}
	params, err := parseQuery(rawQuery)
	if err != nil {
		return nil, err
	}
	sigResult, err := p.verify("/"+endpoint, "/"+decoded, rawQuery, params)
	if err != nil {
		return nil, err
	}
	src, err := source(params)
	if err != nil {
		return nil, err
	}

	b := p.opt.NewBuilder(Name)
	if decoded == "" {
		err = p.native(b, params)
	} else {
		err = p.imaginary(b, decoded, params)
	}
	if err != nil {
		return nil, err
	}
	if err := b.Finish(); err != nil {
		return nil, err
	}
	r := b.Request()
	r.Source = transform.SourceSpec{Raw: src, Encoding: transform.SourcePlain}
	return &dialect.Result{Request: r, SourceString: src, Signature: sigResult}, nil
}

// param is one decoded query parameter.
type param struct {
	key, value string
}

// parseQuery decodes rawQuery into parameters in their original order.
// Empty segments are skipped; a malformed escape is a 400.
func parseQuery(rawQuery string) ([]param, error) {
	var params []param
	for part := range strings.SplitSeq(rawQuery, "&") {
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
		params = append(params, param{key, value})
	}
	return params, nil
}

// source returns the single url parameter.
func source(params []param) (string, error) {
	var (
		src   string
		found bool
	)
	for _, kv := range params {
		switch kv.key {
		case paramURL:
			if found {
				return "", dialect.InvalidURL("multiple source urls")
			}
			src, found = kv.value, true
		case paramFile:
			return "", dialect.InvalidArgument(paramFile, kv.value, "url (local files are served through an fs source)")
		}
	}
	if !found || src == "" {
		return "", dialect.InvalidURL("missing source url")
	}
	if !imgproxy.PlausibleSource(src) {
		return "", dialect.InvalidURL("invalid source url")
	}
	return src, nil
}

// verify checks sign (imaginary) or xi_sig, whichever is present.
// escapedPath and decodedPath start with "/".
func (p *Parser) verify(escapedPath, decodedPath, rawQuery string, params []param) (sign.Result, error) {
	var hasSign, hasXI bool
	for _, kv := range params {
		hasSign = hasSign || kv.key == sign.ImaginaryParam
		hasXI = hasXI || kv.key == sign.XIParam
	}
	switch {
	case hasSign && hasXI:
		return sign.Unsigned, errs.Wrap(errs.KindUnauthorized, sign.ErrMalformedSignature)
	case hasSign:
		sig, msg, err := sign.ImaginaryMessage(decodedPath, rawQuery)
		if err != nil {
			return sign.Unsigned, err
		}
		return verifyWith(p.o.ImaginaryVerifier, sig, msg)
	case hasXI:
		sig, msg, err := sign.XIMessage(escapedPath, rawQuery)
		if err != nil {
			return sign.Unsigned, err
		}
		return verifyWith(p.o.XIVerifier, sig, msg)
	}
	// No signature: every configured scheme must accept an unsigned request.
	if _, err := verifyWith(p.o.ImaginaryVerifier, "", ""); err != nil {
		return sign.Unsigned, err
	}
	return verifyWith(p.o.XIVerifier, "", "")
}

func verifyWith(v sign.Verifier, sig, msg string) (sign.Result, error) {
	if v == nil {
		return sign.Unsigned, nil
	}
	return v.Verify(sig, msg)
}

// isSignatureParam reports whether key carries a signature.
func isSignatureParam(key string) bool {
	return key == sign.ImaginaryParam || key == sign.XIParam
}

// unknown handles a key the current shape does not know.
func (p *Parser) unknown(b *imgproxy.Builder, key string) error {
	if p.o.Strict {
		return dialect.UnknownOption(key)
	}
	b.Ignore(key, reasonUnknown)
	return nil
}
