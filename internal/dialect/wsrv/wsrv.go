// Package wsrv parses the wsrv.nl / weserv dialect (docs/URL-DIALECTS.md
// §11). It is mounted at /wsrv/ and, when enabled, answers "/?url=..." at
// the root (router rule 3):
//
//	/?url=example.com/a.jpg&w=300&h=300&fit=cover&a=attention&output=webp&q=80
//	/wsrv/?url=ssl:example.com/a.jpg&w=50%25&filt=greyscale&default=example.com/404.png
//
// The parameter semantics follow the weserv source
// (.refs/weserv-images src/api/parsers/query.cpp for the grammar,
// src/api/processors/*.cpp for the meaning, src/nginx for url, default,
// maxage, filename and encoding) rather than the prose docs:
//
//   - The first occurrence of a parameter wins (upstream uses emplace), and
//     synonyms (width, height, quality, level, lossless, pages, align and
//     the deprecated t, bri, or, shape, strim, errorredirect) fold into the
//     canonical key. Parameter order does not matter otherwise: the
//     processing order is fixed.
//   - A parameter without "=" has the empty value. Booleans (we, flip,
//     flop, il, ll, af, precrop, mtrim) are true unless "0" or "false".
//     Empty numeric values are unset, except where wsrv gives them a
//     meaning: trim (threshold 10), blur and sharp (the mild 3×3 kernels)
//     and gam (2.2).
//   - wsrv silently ignores malformed values; xi rejects them with 400
//     naming the parameter, like the other dialects. Values wsrv maps to a
//     documented fallback keep that meaning (blur outside 0.3–1000, sharp
//     outside 0–10, gam outside 1–3).
//   - The source url may omit the scheme ("example.com/a.jpg" and
//     "//example.com/a.jpg" mean https) or use the legacy "ssl:" prefix.
//     On a mount bound to a named source, a scheme-less url is a key
//     relative to it.
//
// The mapping onto transform.Request is in apply.go. Signing: wsrv has no
// scheme; the optional xi scheme (xi_sig, sign.XIMessage) is supported.
package wsrv

import (
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Name is the dialect identifier.
const Name = "wsrv"

// Reasons recorded in Request.Ignored.
const (
	reasonUnknown        = "unknown option"
	reasonNotImplemented = "not implemented"
	reasonApproximated   = "approximated"
	reasonNotApplicable  = "not applicable"
	reasonInfoSchema     = "imgproxy info schema"
)

// Options configures the parser. The zero value is a working unsigned,
// non-strict parser.
type Options struct {
	// XIVerifier checks the xi_sig parameter (sign.NewXIVerifier). nil
	// means xi_sig signing is off.
	XIVerifier sign.Verifier
	// Strict rejects unknown parameters with 400 (XI_DIALECTS_STRICT).
	Strict bool
	// Now is the clock for the option builder; nil means time.Now.
	Now func() time.Time
}

// Parser is the wsrv dialect. It is safe for concurrent use.
type Parser struct {
	o   Options
	opt *imgproxy.Parser // request builder only; never parses a path
}

var _ dialect.Dialect = (*Parser)(nil)

// New returns a Parser; nil means the zero Options.
func New(opts *Options) (*Parser, error) {
	var o Options
	if opts != nil {
		o = *opts
	}
	ip, err := imgproxy.New(&imgproxy.Options{Strict: o.Strict, Now: o.Now})
	if err != nil {
		return nil, err
	}
	return &Parser{o: o, opt: ip}, nil
}

// Name implements dialect.Dialect.
func (*Parser) Name() string { return Name }

// Verifier implements dialect.Dialect: the xi scheme, wsrv has none.
func (p *Parser) Verifier() sign.Verifier { return p.o.XIVerifier }

// Parse implements dialect.Dialect. It uses in.Path, in.RawQuery and
// in.Mount.Source.
func (p *Parser) Parse(in *dialect.Input) (*dialect.Result, error) {
	return p.ParseURL(in.Path, in.RawQuery, in.Mount.Source != "")
}

// ParseURL parses the escaped path after the mount prefix ("/" or "") and
// the raw query. mountBound makes scheme-less urls relative keys of the
// mount's named source instead of https URLs.
func (p *Parser) ParseURL(path, rawQuery string, mountBound bool) (*dialect.Result, error) {
	if path != "" && path != "/" {
		return nil, dialect.InvalidURL("unexpected path")
	}
	ps := parseParams(rawQuery)
	sig, err := p.verify(ps.sig, rawQuery)
	if err != nil {
		return nil, err
	}
	raw, ok := ps.get("url")
	if !ok || raw == "" {
		return nil, dialect.InvalidURL("missing source url")
	}
	src, err := sourceSpec("url", raw, mountBound)
	if err != nil {
		return nil, err
	}

	b := p.opt.NewBuilder(Name)
	for _, k := range ps.unknown {
		if p.o.Strict {
			return nil, dialect.UnknownOption(k)
		}
		b.Ignore(k, reasonUnknown)
	}
	if err := apply(b, ps, mountBound); err != nil {
		return nil, err
	}
	if err := b.Finish(); err != nil {
		return nil, err
	}
	r := b.Request()
	r.Source = src
	return &dialect.Result{Request: r, SourceString: src.Raw, Signature: sig}, nil
}

// verify checks xi_sig when present; otherwise the verifier decides
// between Unsigned and a missing-signature error.
func (p *Parser) verify(hasSig bool, rawQuery string) (sign.Result, error) {
	v := p.o.XIVerifier
	if v == nil {
		return sign.Unsigned, nil
	}
	if !hasSig {
		return v.Verify("", "")
	}
	s, msg, err := sign.XIMessage("/", rawQuery)
	if err != nil {
		return sign.Unsigned, err
	}
	return v.Verify(s, msg)
}

// sourceSpec normalises a wsrv url (or default) value: "ssl:" and "//"
// prefixes and scheme-less hosts become https; any "scheme://" is kept
// for the source resolver's allowlist. A value starting with "/", or any
// scheme-less value on a mount bound to a named source, is a relative key.
func sourceSpec(field, v string, mountBound bool) (transform.SourceSpec, error) {
	if !imgproxy.PlausibleSource(v) {
		return transform.SourceSpec{}, dialect.InvalidArgument(field, v, "an image url")
	}
	plain := func(s string) (transform.SourceSpec, error) {
		return transform.SourceSpec{Raw: s, Encoding: transform.SourcePlain}, nil
	}
	switch {
	case hasPrefixFold(v, "ssl:"):
		return plain("https://" + strings.TrimPrefix(v[4:], "//"))
	case strings.HasPrefix(v, "//"):
		return plain("https:" + v)
	case hasScheme(v):
		return plain(v)
	case mountBound || strings.HasPrefix(v, "/"):
		key := strings.TrimPrefix(v, "/")
		if key == "" {
			return transform.SourceSpec{}, dialect.InvalidArgument(field, v, "an image url")
		}
		return transform.SourceSpec{Raw: key, Encoding: transform.SourceRelative}, nil
	default:
		return plain("https://" + v)
	}
}

// hasScheme reports whether v starts with "<scheme>://".
func hasScheme(v string) bool {
	i := strings.Index(v, "://")
	if i <= 0 {
		return false
	}
	for j := range i {
		c := v[j]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case j > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return true
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}
