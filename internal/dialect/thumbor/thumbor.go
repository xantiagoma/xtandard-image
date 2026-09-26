// Package thumbor parses thumbor URLs (docs/URL-DIALECTS.md §10), mounted at
// /thumbor/ by default:
//
//	/<signature|unsafe>/[debug/][meta/][trim[:top-left|:bottom-right][:tolerance]/]
//	  [AxB:CxD/][adaptive-][full-]fit-in/][-][W|orig]x[-][H|orig]/
//	  [left|center|right/][top|middle|bottom/][smart/][filters:f(a):g(b)/]<image>
//
// The grammar is libthumbor's Url.regex (url.py, MIT), matched segment by
// segment: each optional segment is taken only when something follows it,
// as the regular expression's backtracking does, and whatever remains is
// the image. Segments are matched on the escaped path and unescaped
// afterwards, as Tornado does for thumbor's path arguments, so an image
// written as https%3A%2F%2Fexample.com%2Fa.jpg is one segment.
//
// Signing: the first segment is "unsafe" or a signature of at least 28
// characters (thumbor's [^/]{28,}); anything else is a 400, like thumbor's
// "URL does not have hash or unsafe". The signature is
// base64url-padded(HMAC-SHA1(key, path after "/<signature>/")) checked by
// sign.NewThumborVerifier; "unsafe" is Unsigned and the router applies the
// mount's unsigned policy (thumbor ALLOW_UNSAFE_URL).
//
// The image is a key relative to the mount's source or an absolute
// http(s) URL; the source resolver enforces the allowlist.
//
// Filters are parsed by the filters sub-package; see filter.go for the
// mapping onto the request model. Deliberate differences from thumbor, all
// stricter or more explicit: a known filter with malformed arguments is a
// 400 (thumbor silently skips it), unknown filters are recorded in
// Request.Ignored (a 400 in strict mode), and trim:N means tolerance N
// (thumbor reads a lone number as the corner name and falls back to
// top-left with tolerance 0).
package thumbor

import (
	"net/url"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Name is the dialect identifier.
const Name = "thumbor"

// minSignatureLen is the shortest first segment libthumbor's regex treats
// as a signature (base64 of a SHA-1 MAC is 28 characters).
const minSignatureLen = 28

// Options configures the parser. The zero value is a working unsigned,
// non-strict parser.
type Options struct {
	// Verifier checks signatures (sign.NewThumborVerifier). nil means
	// signing is off: every URL is Unsigned.
	Verifier sign.Verifier
	// Strict rejects unknown filters with 400 (XI_DIALECTS_STRICT).
	Strict bool
}

// Parser is the thumbor dialect. It is safe for concurrent use.
type Parser struct {
	o Options
}

var _ dialect.Dialect = (*Parser)(nil)

// New returns a Parser; nil means the zero Options.
func New(opts *Options) *Parser {
	var o Options
	if opts != nil {
		o = *opts
	}
	return &Parser{o: o}
}

// Name implements dialect.Dialect.
func (*Parser) Name() string { return Name }

// Verifier implements dialect.Dialect.
func (p *Parser) Verifier() sign.Verifier { return p.o.Verifier }

// Parse implements dialect.Dialect. Only in.Path is used: thumbor ignores
// the query string.
func (p *Parser) Parse(in *dialect.Input) (*dialect.Result, error) {
	return p.ParsePath(in.Path)
}

// ParsePath parses the escaped path after the mount prefix, for example
// "/unsafe/300x200/smart/https%3A%2F%2Fexample.com%2Fa.jpg".
func (p *Parser) ParsePath(path string) (*dialect.Result, error) {
	path = strings.TrimPrefix(path, "/")
	first, rest, ok := strings.Cut(path, "/")
	if !ok || (first != sign.ThumborUnsafe && len(first) < minSignatureLen) {
		return nil, dialect.InvalidURL("missing signature or unsafe segment")
	}
	sigResult, err := p.verify(first, rest)
	if err != nil {
		return nil, err
	}
	u, err := parseParts(rest)
	if err != nil {
		return nil, err
	}
	b := newBuilder(p.o.Strict)
	if err := b.build(u); err != nil {
		return nil, err
	}
	r := b.r
	return &dialect.Result{Request: r, SourceString: r.Source.Raw, Signature: sigResult}, nil
}

// verify checks the signature segment against the rest of the path.
func (p *Parser) verify(first, rest string) (sign.Result, error) {
	if p.o.Verifier == nil {
		return sign.Unsigned, nil
	}
	sig := first
	if first != sign.ThumborUnsafe {
		// thumbor accepts the signature percent-encoded ("%3D" for "=").
		s, err := url.PathUnescape(first)
		if err != nil {
			return sign.Unsigned, errs.Wrap(errs.KindUnauthorized, sign.ErrMalformedSignature)
		}
		sig = s
	}
	return p.o.Verifier.Verify(sig, rest)
}

// unescape percent-decodes one matched part of the path (Tornado's
// url_unescape with plus=False).
func unescape(field, s string) (string, error) {
	v, err := url.PathUnescape(s)
	if err != nil {
		return "", dialect.InvalidArgument(field, s, "valid percent-encoding")
	}
	return v, nil
}

// newRequest returns an empty request with one pipeline.
func newRequest() *transform.Request {
	return &transform.Request{Dialect: Name, Pipelines: make([]transform.Pipeline, 1)}
}
