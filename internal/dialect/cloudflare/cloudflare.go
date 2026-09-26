// Package cloudflare parses the two Cloudflare Images URL grammars
// (docs/URL-DIALECTS.md §8):
//
//	/cdn-cgi/image/<options>/<source>                                   (dialect "cloudflare")
//	/cdn-cgi/imagedelivery/<account_hash>/<image_id>/<variant|options>  (dialect "cloudflare_images")
//	https://imagedelivery.net/<account_hash>/<image_id>/<variant|options>
//
// Both share one option parser: a comma-separated list of key=value pairs
// with Cloudflare's names and aliases (width/w, height/h, fit, gravity/g,
// quality/q, format/f, dpr, anim, background, blur, sharpen, brightness,
// contrast, gamma, saturation, rotate, flip, trim, border, metadata,
// onerror, compression, slow-connection-quality/scq, zoom, segment,
// upscale). Options map directly onto transform.Request fields; the table
// and its approximations are in docs/URL-DIALECTS.md §8. Later options
// override earlier ones. Unknown keys and unsupported values (segment,
// upscale=generate) are recorded in Request.Ignored, or rejected with 400
// in strict mode.
//
// # Transform via URL
//
// The source after the options is a path on the mount's bound source
// ("/uploads/a.jpg", kept with its leading "/" so it always resolves as a
// relative key) or an absolute http(s) URL checked against the source
// allowlist. An absolute URL may be percent-encoded as a whole
// ("https%3A%2F%2F…", as unpic emits for URLs with a query), and a
// "https:/host" collapsed by path cleaning is repaired. The request query
// belongs to the source and is appended to it, minus xi_sig. There is no
// upstream signature: the optional xi_sig (sign.XIMessage over the escaped
// path after the mount) authenticates URLs when configured.
//
// onerror=redirect sets Delivery.OnErrorRedirect: when the source cannot be
// fetched or decoded the server answers 302 to the source URL instead of an
// error or the fallback image.
//
// # Images delivery
//
// See ImagesParser.
package cloudflare

import (
	"net/url"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Name is the identifier of the /cdn-cgi/image/ dialect.
const Name = "cloudflare"

// imagePrefix is the native path prefix, stripped when a host-bound mount
// passes the whole path.
const imagePrefix = "/cdn-cgi/image"

// Options configures Parser. The zero value is a working unsigned,
// non-strict parser.
type Options struct {
	// XIVerifier checks the xi_sig parameter (sign.NewXIVerifier). nil
	// means xi_sig signing is off.
	XIVerifier sign.Verifier
	// Strict rejects unknown and unsupported options with 400
	// (XI_DIALECTS_STRICT).
	Strict bool
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Parser is the /cdn-cgi/image/ dialect. It is safe for concurrent use.
type Parser struct {
	o   Options
	opt *imgproxy.Parser // request construction only; never parses a path
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

// Verifier implements dialect.Dialect: the xi_sig verifier, or nil.
func (p *Parser) Verifier() sign.Verifier { return p.o.XIVerifier }

// Parse implements dialect.Dialect. It uses in.Path and in.RawQuery.
func (p *Parser) Parse(in *dialect.Input) (*dialect.Result, error) {
	return p.ParseURL(in.Path, in.RawQuery)
}

// ParseURL parses the escaped path after the mount prefix, for example
// "/width=400,fit=cover/uploads/a.jpg", and the raw query.
func (p *Parser) ParseURL(path, rawQuery string) (*dialect.Result, error) {
	if rest, ok := strings.CutPrefix(path, imagePrefix); ok && (rest == "" || rest[0] == '/') {
		path = rest
	}
	sig, msg, err := sign.XIMessage(path, rawQuery)
	if err != nil {
		return nil, err
	}
	sigResult := sign.Unsigned
	if p.o.XIVerifier != nil {
		if sigResult, err = p.o.XIVerifier.Verify(sig, msg); err != nil {
			return nil, err
		}
	}

	opts, src, ok := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	switch {
	case opts == "":
		return nil, dialect.InvalidURL("missing options")
	case !ok:
		return nil, dialect.InvalidURL("missing source")
	}
	source, err := sourceOf(src, msg[len(path):])
	if err != nil {
		return nil, err
	}

	b := p.opt.NewBuilder(Name)
	st := newState(b, p.o.Strict, false)
	if err := st.applyList(opts); err != nil {
		return nil, err
	}
	if err := st.finish(); err != nil {
		return nil, err
	}
	r := b.Request()
	r.Source = transform.SourceSpec{Raw: source, Encoding: transform.SourcePlain}
	return &dialect.Result{Request: r, SourceString: source, Signature: sigResult}, nil
}

// sourceOf turns the escaped path after the options into a source string.
// query is "" or "?" followed by the request query without xi_sig.
func sourceOf(escaped, query string) (string, error) {
	if escaped == "" {
		return "", dialect.InvalidURL("missing source")
	}
	s := escaped
	if hasPrefixFold(s, "http") && !isAbsolute(s) {
		// A percent-encoded URL (encodeURIComponent): decode it once.
		dec, err := url.PathUnescape(s)
		if err != nil {
			return "", dialect.InvalidURL("invalid source escaping")
		}
		if isAbsolute(dec) {
			s = dec
		}
	}
	switch {
	case isAbsolute(s):
	case hasPrefixFold(s, "http:/"):
		s = s[:len("http:/")] + "/" + s[len("http:/"):]
	case hasPrefixFold(s, "https:/"):
		s = s[:len("https:/")] + "/" + s[len("https:/"):]
	default:
		s = "/" + s
	}
	if query != "" && strings.Contains(s, "?") {
		query = "&" + query[1:]
	}
	s += query
	dec, err := url.PathUnescape(s)
	if err != nil || !imgproxy.PlausibleSource(s) || !imgproxy.PlausibleSource(dec) {
		return "", dialect.InvalidURL("invalid source url")
	}
	return s, nil
}

func isAbsolute(s string) bool {
	return hasPrefixFold(s, "http://") || hasPrefixFold(s, "https://")
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// finish applies the options that need every other option.
func (st *state) finish() error {
	if err := st.finishTrim(); err != nil {
		return err
	}
	if st.border != nil {
		st.pl().Border = st.border
	}
	st.finishZoom()
	return st.b.Finish()
}
