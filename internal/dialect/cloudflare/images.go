package cloudflare

import (
	"errors"
	"maps"
	"strconv"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/preset"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// ImagesName is the identifier of the Cloudflare Images delivery dialect.
const ImagesName = "cloudflare_images"

// deliveryPrefix is the custom-domain path prefix, stripped when a
// host-bound mount passes the whole path.
const deliveryPrefix = "/cdn-cgi/imagedelivery"

// PublicVariant is Cloudflare's default variant. Without a preset of that
// name it serves the image unchanged.
const PublicVariant = "public"

// ErrUnknownAccount is wrapped by the 403 for an account hash that is not
// in ImagesOptions.Accounts.
var ErrUnknownAccount = errors.New("cloudflare: unknown account hash")

// ErrFlexibleVariants is wrapped by the 403 for inline options while only
// presets are allowed (Cloudflare "flexible variants" off).
var ErrFlexibleVariants = errors.New("cloudflare: flexible variants are disabled")

// ImagesOptions configures ImagesParser. The zero value is a working
// unsigned, non-strict parser whose images resolve against the mount's
// bound source.
type ImagesOptions struct {
	// Verifier checks sig (sign.NewCloudflareVerifier). nil means signing
	// is off; exp is still enforced.
	Verifier sign.Verifier
	// Accounts maps an account hash to the source its image ids are keys
	// of (XI_DIALECTS_CLOUDFLARE_IMAGES_ACCOUNTS). Empty means every hash
	// uses the mount's source; otherwise an unknown hash is a 403.
	Accounts map[string]string
	// Presets holds the named variants (imgproxy syntax, XI_PRESETS). In
	// only-presets mode inline options (flexible variants) are a 403.
	Presets *preset.Store
	// Strict rejects unknown and unsupported options with 400.
	Strict bool
	// Now is the clock for exp; nil means time.Now.
	Now func() time.Time
}

// ImagesParser is the Cloudflare Images delivery dialect:
//
//	/<account_hash>/<image_id>/<variant|options>[?exp=<unix>&sig=<hex>]
//
// after the mount prefix (/cdn-cgi/imagedelivery/ by default), or the whole
// path on a host-bound mount (imagedelivery.net). A leading
// "/cdn-cgi/imagedelivery" is stripped too, so one host binding serves both
// shapes. The image id may contain "/" (custom ids): it is everything
// between the account hash and the last segment. With only two segments
// the variant is "public".
//
// The account hash selects the source (ImagesOptions.Accounts) and the
// image id is the key in it. A last segment containing "=" is an inline
// option list (Cloudflare "flexible variants", the same parser as
// /cdn-cgi/image/); otherwise it names a variant, looked up as a preset.
//
// Signed URLs follow Cloudflare: sig = hex(HMAC-SHA256(key, path ‖ "?" ‖
// query without sig)) over the path after the prefix, then exp (unix
// seconds) must be in the future; an expired URL is errs.KindExpired
// (404). onerror is not supported for hosted images and is ignored.
type ImagesParser struct {
	o   ImagesOptions
	opt *imgproxy.Parser // request construction and preset expansion
}

var _ dialect.Dialect = (*ImagesParser)(nil)

// NewImages returns an ImagesParser; nil means the zero ImagesOptions. It
// validates every preset.
func NewImages(opts *ImagesOptions) (*ImagesParser, error) {
	var o ImagesOptions
	if opts != nil {
		o = *opts
	}
	o.Accounts = maps.Clone(o.Accounts)
	if o.Now == nil {
		o.Now = time.Now
	}
	for hash, src := range o.Accounts {
		if hash == "" || strings.Contains(hash, "/") || src == "" {
			return nil, errors.New("cloudflare: account hashes and sources must be non-empty and hashes must not contain /")
		}
	}
	ip, err := imgproxy.New(&imgproxy.Options{
		ArgumentsSeparator: o.Presets.Separator(),
		Strict:             o.Strict,
		Now:                o.Now,
		Presets:            o.Presets,
	})
	if err != nil {
		return nil, err
	}
	return &ImagesParser{o: o, opt: ip}, nil
}

// Name implements dialect.Dialect.
func (*ImagesParser) Name() string { return ImagesName }

// Verifier implements dialect.Dialect: the sig verifier, or nil.
func (p *ImagesParser) Verifier() sign.Verifier { return p.o.Verifier }

// Parse implements dialect.Dialect. It uses in.Path and in.RawQuery.
func (p *ImagesParser) Parse(in *dialect.Input) (*dialect.Result, error) {
	return p.ParseURL(in.Path, in.RawQuery)
}

// DeliveryURL is a split Cloudflare Images delivery path.
type DeliveryURL struct {
	AccountHash string
	ImageID     string // escaped
	Variant     string // variant name or escaped option list; "" = none
}

// SplitDeliveryPath splits an escaped delivery path
// ("/<hash>/<id...>/<variant>") into its parts.
func SplitDeliveryPath(path string) (DeliveryURL, error) {
	hash, rest, ok := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if !ok || hash == "" || rest == "" {
		return DeliveryURL{}, dialect.InvalidURL("missing account hash or image id")
	}
	u := DeliveryURL{AccountHash: hash, ImageID: rest}
	if id, variant, ok := strings.CutLast(rest, "/"); ok {
		u.ImageID, u.Variant = id, variant
	}
	if u.ImageID == "" || u.ImageID[0] == '/' || strings.Contains(u.ImageID, "//") ||
		(u.Variant == "" && u.ImageID != rest) {
		return DeliveryURL{}, dialect.InvalidURL("empty path segment")
	}
	return u, nil
}

// ParseURL parses the escaped path after the mount prefix and the raw
// query.
func (p *ImagesParser) ParseURL(path, rawQuery string) (*dialect.Result, error) {
	if rest, ok := strings.CutPrefix(path, deliveryPrefix); ok && (rest == "" || rest[0] == '/') {
		path = rest
	}
	sigResult, err := p.verify(path, rawQuery)
	if err != nil {
		return nil, err
	}
	u, err := SplitDeliveryPath(path)
	if err != nil {
		return nil, err
	}
	var mount string
	if len(p.o.Accounts) > 0 {
		src, ok := p.o.Accounts[u.AccountHash]
		if !ok {
			return nil, &errs.Error{Kind: errs.KindForbidden, Public: "unknown account", Err: ErrUnknownAccount}
		}
		mount = src
	}

	b := p.opt.NewBuilder(ImagesName)
	st := newState(b, p.o.Strict, true)
	if err := p.variant(st, u.Variant); err != nil {
		return nil, err
	}
	exp, err := expiry(rawQuery)
	if err != nil {
		return nil, err
	}
	if !exp.IsZero() {
		b.Request().Delivery.Expires = exp
	}
	if err := st.finish(); err != nil {
		return nil, err
	}
	source := "/" + u.ImageID
	if !imgproxy.PlausibleSource(source) {
		return nil, dialect.InvalidURL("invalid image id")
	}
	r := b.Request()
	r.Source = transform.SourceSpec{Raw: source, Encoding: transform.SourcePlain, Mount: mount}
	return &dialect.Result{Request: r, SourceString: source, Signature: sigResult}, nil
}

// variant applies the last path segment: inline options when it contains
// "=", else a named variant.
func (p *ImagesParser) variant(st *state, v string) error {
	switch {
	case strings.Contains(v, "="):
		if p.o.Presets.Only() {
			return &errs.Error{Kind: errs.KindForbidden, Public: "flexible variants are disabled", Err: ErrFlexibleVariants}
		}
		return st.applyList(v)
	case v == "":
		v = PublicVariant
	}
	if _, ok := p.o.Presets.Get(v); ok {
		return st.b.Apply("preset", v)
	}
	if v == PublicVariant {
		st.req().Delivery.Presets = append(st.req().Delivery.Presets, v)
		return nil
	}
	return dialect.InvalidArgument("variant", v, "defined variant (preset)")
}

// verify checks sig over path ‖ "?" ‖ query without sig.
func (p *ImagesParser) verify(path, rawQuery string) (sign.Result, error) {
	sig, msg, err := sign.CloudflareMessage(path, rawQuery)
	if err != nil {
		return sign.Unsigned, err
	}
	if p.o.Verifier == nil {
		return sign.Unsigned, nil
	}
	return p.o.Verifier.Verify(sig, msg)
}

// expiry returns the exp parameter as a time; zero when absent. A repeated
// or malformed exp is a 400.
func expiry(rawQuery string) (time.Time, error) {
	var (
		exp   time.Time
		found bool
	)
	for part := range strings.SplitSeq(rawQuery, "&") {
		k, v, _ := strings.Cut(part, "=")
		if k != sign.CloudflareExpParam {
			continue
		}
		if found {
			return time.Time{}, dialect.InvalidArgument("exp", v, "a single unix timestamp")
		}
		found = true
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 || n > maxUnix {
			return time.Time{}, dialect.InvalidArgument("exp", v, "unix timestamp in seconds")
		}
		exp = time.Unix(n, 0).UTC()
	}
	return exp, nil
}

// maxUnix bounds exp (year 9999) so time arithmetic cannot overflow.
const maxUnix = 253402300799
