// Package simple implements the unpic long tail as data
// (docs/URL-DIALECTS.md §12): one parser engine and one table per provider
// (nextjs, vercel, netlify, ipx, astro, directus, contentful, shopify, ...).
//
// A provider is a [Spec]: where its URLs live (native path prefixes and
// upstream hosts), how the source image and the parameters are cut out of
// the URL (an [Extractor]), and what each parameter means (a [Param] per
// name, mapping the value onto the normalised [transform.Request] through a
// [State]). [New] turns a Spec into a [dialect.Dialect]; [Providers] lists
// every table; [Mounts] builds the router mounts for the enabled ones.
//
// Adding a provider is a table file (<provider>.go with a func returning its
// Spec, listed in [Providers]) plus its fixture file under tools/compat/unpic
// (generated from unpic by tools/compat/unpic/generate.ts); the generic
// fixture test picks both up.
//
// # Parsing
//
// The dialect sees the provider-native path: when the router stripped one
// of the spec's native prefixes, the prefix is put back, so a table always
// matches the URL exactly as unpic writes it ("/_next/image?url=…",
// "/_ipx/w_200/img.jpg", "/assets/<id>?width=…"). Host-bound and custom
// prefix mounts see the path after the mount unchanged.
//
// Parameters are applied in URL order (the last occurrence of a name wins).
// Unknown parameters are recorded in Request.Ignored ("unknown option"), or
// rejected with 400 in strict mode; parameters a provider documents but xi
// does not apply are recorded as "unsupported" (strict: 400). Values that do
// not parse are always a 400 naming the parameter.
//
// Sizing is collected first and applied last: width, height, fit mode,
// enlargement and device pixel ratio map onto Resize (see [Fit]), so a
// table only states what a value means, not how imgproxy-style options
// combine.
//
// # Source
//
// An embedded source (Next.js url=, Astro href=, IPX path, Cloudimage
// origin path) that is an absolute http(s) URL becomes a plain source; a
// relative one ("/_next/static/a.jpg") becomes a key relative to the
// mount's bound source (SourceRelative, Source.Mount = the mount source).
// A CDN-style path (Contentful, Shopify, Directus, ...) is the object key on
// the provider origin, relative to the bound source, keeping any query
// parameters the table marks as part of the source (Appwrite project=).
//
// # Signing
//
// None of these providers sign URLs. The optional xi scheme applies:
// xi_sig in the query, verified with sign.XIMessage over the native path
// and the query ([Options.Verifier]); the router applies the unsigned
// policy (restricted by default) to everything else.
package simple

import (
	"errors"
	"net/url"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Name is the dialect family name used in config (dialects.simple) and as
// the prefix of every provider's dialect name ("simple:nextjs").
const Name = "simple"

// Reasons recorded in Request.Ignored (static, client-safe).
const (
	reasonUnknown     = "unknown option"
	reasonUnsupported = "unsupported"
)

// Spec is one provider's table.
type Spec struct {
	// Name is the provider name in XI_DIALECTS_SIMPLE_ENABLED: lowercase
	// letters, digits and "-" (unpic's id without dots: "builderio").
	Name string
	// Doc is the upstream reference (unpic provider and API docs).
	Doc string
	// Prefixes are the provider's native path prefixes ("/_next/image",
	// "/_ipx/"); each is mounted when the provider is enabled and put back
	// in front of the path before extraction.
	Prefixes []string
	// Domains are the upstream CDN hosts ("images.ctfassets.net") or host
	// suffixes (".kc-usercontent.com"). A host-bound request on one of
	// them selects this provider (see [Mounts]).
	Domains []string
	// Extract cuts the source and the parameters out of the native path
	// and query.
	Extract Extractor
	// Params maps parameter names to their meaning.
	Params map[string]Param
	// Fit is the fit mode when the URL gives none; zero is FitCover.
	Fit Fit
	// Enlarge is whether the provider upscales by default.
	Enlarge bool
	// Finish, when set, runs after every parameter and before sizing is
	// applied (cross-parameter rules such as "fit only with both sides").
	Finish func(st *State) error
}

// Extractor splits the provider-native escaped path (starting with "/")
// and the raw query (xi_sig removed) into the source and parameters.
type Extractor func(path, rawQuery string) (*Extracted, error)

// Extracted is an Extractor's result.
type Extracted struct {
	// Source is the source reference. Escaped says whether it is still
	// percent-encoded (a path) or already decoded (a query value).
	Source  string
	Escaped bool
	// Params are the parameters in URL order, decoded.
	Params []KV
}

// KV is one decoded parameter.
type KV struct {
	Key, Value string
}

// Param is what a parameter name means. The zero value is a parameter the
// provider documents but xi does not apply (Ignored "unsupported", strict:
// 400).
type Param struct {
	// Apply maps the value onto the request.
	Apply func(st *State, value string) error
	// Silent parameters are accepted without effect (SDK tags, values the
	// extractor already consumed, cache versions).
	Silent bool
	// Source parameters belong to the source reference: they are kept in
	// the query of a relative key (Appwrite project=).
	Source bool
}

// Options configures a parser. The zero value is unsigned and non-strict.
type Options struct {
	// Verifier checks xi_sig (sign.NewXIVerifier); nil means unsigned.
	Verifier sign.Verifier
	// Strict rejects unknown and unsupported parameters with 400
	// (XI_DIALECTS_STRICT).
	Strict bool
}

// Parser is one provider's dialect. It is safe for concurrent use.
type Parser struct {
	spec *Spec
	o    Options
	ip   *imgproxy.Parser // request builder only
}

var _ dialect.Dialect = (*Parser)(nil)

// New validates spec and returns its parser; nil opts means the zero
// Options. The spec must not be modified afterwards.
func New(spec *Spec, opts *Options) (*Parser, error) {
	if err := spec.validate(); err != nil {
		return nil, err
	}
	var o Options
	if opts != nil {
		o = *opts
	}
	ip, err := imgproxy.New(&imgproxy.Options{Strict: o.Strict})
	if err != nil {
		return nil, err
	}
	return &Parser{spec: spec, o: o, ip: ip}, nil
}

func (s *Spec) validate() error {
	switch {
	case s == nil:
		return errors.New("simple: nil spec")
	case !validName(s.Name):
		return errors.New("simple: invalid provider name " + s.Name)
	case s.Extract == nil:
		return errors.New("simple: " + s.Name + ": no extractor")
	}
	for _, p := range s.Prefixes {
		if !strings.HasPrefix(p, "/") || len(strings.TrimSuffix(p, "/")) < 2 {
			return errors.New("simple: " + s.Name + ": bad prefix " + p)
		}
	}
	return nil
}

func validName(s string) bool {
	if s == "" || s[0] == '-' {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

// Name implements dialect.Dialect: "simple:<provider>".
func (p *Parser) Name() string { return Name + ":" + p.spec.Name }

// Spec returns the provider table.
func (p *Parser) Spec() *Spec { return p.spec }

// Verifier implements dialect.Dialect.
func (p *Parser) Verifier() sign.Verifier { return p.o.Verifier }

// Parse implements dialect.Dialect. It uses in.Path, in.RawQuery and
// in.Mount (Prefix to rebuild the native path, Source for relative keys).
func (p *Parser) Parse(in *dialect.Input) (*dialect.Result, error) {
	res, err := p.ParseURL(p.nativePath(in.Mount.Prefix, in.Path), in.RawQuery)
	if err != nil {
		return nil, err
	}
	if r := res.Request; r.Source.Encoding == transform.SourceRelative {
		r.Source.Mount = in.Mount.Source
	}
	return res, nil
}

// nativePath puts a stripped native prefix back in front of path.
func (p *Parser) nativePath(prefix, path string) string {
	if prefix == "" {
		return path
	}
	for _, np := range p.spec.Prefixes {
		if strings.TrimSuffix(np, "/") == prefix {
			if path == "/" {
				return prefix
			}
			return prefix + path
		}
	}
	return path
}

// ParseURL parses a provider-native escaped path (starting with "/") and
// raw query, for example ("/_next/image", "url=%2Fa.jpg&w=640&q=75").
func (p *Parser) ParseURL(path, rawQuery string) (*dialect.Result, error) {
	if path == "" {
		path = "/"
	}
	sigResult := sign.Unsigned
	if p.o.Verifier != nil {
		sig, msg, err := sign.XIMessage(path, rawQuery)
		if err != nil {
			return nil, err
		}
		if sigResult, err = p.o.Verifier.Verify(sig, msg); err != nil {
			return nil, err
		}
	}
	ex, err := p.spec.Extract(path, withoutParam(rawQuery, sign.XIParam))
	if err != nil {
		return nil, err
	}
	b := p.ip.NewBuilder(Name + ":" + p.spec.Name)
	st := &State{Request: b.Request(), Pipeline: b.Pipeline(), b: b}
	var keep []KV
	for _, kv := range ex.Params {
		prm, ok := p.spec.Params[kv.Key]
		switch {
		case !ok:
			if p.o.Strict {
				return nil, dialect.UnknownOption(kv.Key)
			}
			b.Ignore(kv.Key, reasonUnknown)
		case prm.Source:
			keep = append(keep, kv)
		case prm.Silent:
		case prm.Apply == nil:
			if p.o.Strict {
				return nil, dialect.UnknownOption(kv.Key)
			}
			b.Ignore(kv.Key, reasonUnsupported)
		default:
			if err := prm.Apply(st, kv.Value); err != nil {
				return nil, err
			}
		}
	}
	if p.spec.Finish != nil {
		if err := p.spec.Finish(st); err != nil {
			return nil, err
		}
	}
	st.applySize(p.spec)
	if err := b.Finish(); err != nil {
		return nil, err
	}
	src, enc, err := sourceRef(ex, keep)
	if err != nil {
		return nil, err
	}
	r := b.Request()
	r.Source = transform.SourceSpec{Raw: src, Encoding: enc}
	return &dialect.Result{Request: r, SourceString: src, Signature: sigResult}, nil
}

// withoutParam drops every raw query parameter named name.
func withoutParam(rawQuery, name string) string {
	if !strings.Contains(rawQuery, name) {
		return rawQuery
	}
	var b strings.Builder
	for part := range strings.SplitSeq(rawQuery, "&") {
		if k, _, _ := strings.Cut(part, "="); k == name || part == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('&')
		}
		b.WriteString(part)
	}
	return b.String()
}

// sourceRef turns the extracted source into the request's source: a plain
// http(s) URL, or a key relative to the mount's source (escaped, without a
// leading "/", with the table's source parameters as its query).
func sourceRef(ex *Extracted, keep []KV) (string, transform.SourceEncoding, error) {
	raw := ex.Source
	decoded := raw
	if ex.Escaped {
		var err error
		if decoded, err = url.PathUnescape(raw); err != nil {
			return "", 0, dialect.InvalidURL("invalid path escaping")
		}
	}
	if strings.HasPrefix(decoded, "//") {
		decoded = "https:" + decoded
	}
	if l := strings.ToLower(decoded); strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") {
		if !imgproxy.PlausibleSource(decoded) {
			return "", 0, dialect.InvalidURL("invalid source url")
		}
		return decoded, transform.SourcePlain, nil
	}
	key := raw
	if !ex.Escaped {
		key = escapeKey(raw)
	}
	key = strings.TrimLeft(key, "/")
	path, query, _ := strings.Cut(key, "?")
	if path == "" || !imgproxy.PlausibleSource(decoded) {
		return "", 0, dialect.InvalidURL("missing source path")
	}
	if len(keep) > 0 {
		q := make([]string, 0, len(keep)+1)
		if query != "" {
			q = append(q, query)
		}
		for _, kv := range keep {
			q = append(q, url.QueryEscape(kv.Key)+"="+url.QueryEscape(kv.Value))
		}
		query = strings.Join(q, "&")
	}
	// A ":" in the first segment would read as a scheme to the source
	// resolver; escape it (RFC 3986 segment-nz-nc).
	first, rest, found := strings.Cut(path, "/")
	if strings.Contains(first, ":") {
		first = strings.ReplaceAll(first, ":", "%3A")
		if found {
			path = first + "/" + rest
		} else {
			path = first
		}
	}
	if query != "" {
		return path + "?" + query, transform.SourceRelative, nil
	}
	return path, transform.SourceRelative, nil
}

// escapeKey percent-encodes a decoded relative reference segment by
// segment, keeping "/" and a query after "?".
func escapeKey(s string) string {
	path, query, hasQuery := strings.Cut(s, "?")
	segs := strings.Split(path, "/")
	for i, seg := range segs {
		segs[i] = url.PathEscape(seg)
	}
	out := strings.Join(segs, "/")
	if hasQuery {
		out += "?" + query
	}
	return out
}
