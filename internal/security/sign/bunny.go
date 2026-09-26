package sign

import (
	"crypto/hmac"
	"crypto/md5" //nolint:gosec // Bunny basic token authentication is MD5 by definition; verification only
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"slices"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// Bunny CDN token authentication (docs/URL-DIALECTS.md §2 and §9).
//
// Three token layouts exist. The byte layouts below were verified against
// Bunny's documentation (bunny.net/docs/cdn/security/token-authentication/
// basic and /advanced, fetched 2026-09-26) and the official SDK repository
// github.com/BunnyWay/BunnyCDN.TokenAuthentication (go/token.go and
// e2e/vectors.json at 48ff1af, 2026-07-24, for HS256; nodejs/token.js,
// php/url_signing.php and the C# test vectors at 1019b23, the last commit
// before the HMAC rewrite, for the legacy SHA-256 layout):
//
//	basic   token = b64url(MD5(key ‖ path ‖ expires))                 16 bytes
//	sha256  token = b64url(SHA-256(key ‖ path ‖ expires ‖ data))      32 bytes (legacy "advanced")
//	hs256   token = "HS256-" b64url(HMAC-SHA256(key, path ‖ expires ‖ data))
//
// b64url is URL-safe base64 without padding. key is the pull zone's URL
// token authentication key as raw bytes (not hex). path is the decoded URL
// path, or token_path when present. expires is the expires parameter
// exactly as written. data is every other query parameter except token and
// expires, sorted by key and joined as key=value with "&" (raw, decoded
// values; empty values kept as "key="); with token_ignore_params=true it is
// only "token_ignore_params=true" plus token_path. IP-locked tokens
// (basic with the IP appended, "HS256-1-…") cannot be verified because
// dialects do not see the client address: they fail as malformed or
// mismatched signatures.

// Bunny query parameters with a signing meaning.
const (
	BunnyTokenParam        = "token"
	BunnyExpiresParam      = "expires"
	BunnyTokenPathParam    = "token_path"
	BunnyIgnoreParamsParam = "token_ignore_params"
	// BunnyPathTokenParam opens a path-based token segment:
	// "/bcdn_token=<token>&expires=…&token_path=…/<path>".
	BunnyPathTokenParam = "bcdn_token"
	// BunnyHS256Prefix starts an HMAC-SHA256 token; "HS256-1-" marks an
	// IP-locked one.
	BunnyHS256Prefix = "HS256-"
)

// BunnyLayout is a set of accepted Bunny token layouts.
type BunnyLayout uint8

// Bunny token layouts. The zero BunnyLayout accepts all of them.
const (
	BunnyBasic      BunnyLayout = 1 << iota // MD5, deprecated by Bunny
	BunnySHA256                             // legacy advanced, SHA-256 without HMAC
	BunnyHS256                              // current advanced, HMAC-SHA256
	BunnyAllLayouts = BunnyBasic | BunnySHA256 | BunnyHS256
)

// bunnyHash is the basic and legacy SHA-256 layouts: H(secret ‖ msg).
type bunnyHash struct {
	name string
	size int
	sum  func(secret []byte, msg string) []byte
}

// NewBunnyBasicScheme returns Bunny basic token authentication:
// b64url(MD5(key ‖ msg)); the message is path ‖ expires (BunnyMessage).
func NewBunnyBasicScheme() Scheme {
	return bunnyHash{name: "bunny_basic", size: md5.Size, sum: func(secret []byte, msg string) []byte {
		h := md5.New() //nolint:gosec // see import
		h.Write(secret)
		_, _ = io.WriteString(h, msg)
		return h.Sum(nil)
	}}
}

// NewBunnySHA256Scheme returns Bunny's legacy advanced token
// authentication: b64url(SHA-256(key ‖ msg)).
func NewBunnySHA256Scheme() Scheme {
	return bunnyHash{name: "bunny_sha256", size: sha256.Size, sum: func(secret []byte, msg string) []byte {
		h := sha256.New()
		h.Write(secret)
		_, _ = io.WriteString(h, msg)
		return h.Sum(nil)
	}}
}

func (s bunnyHash) Name() string { return s.name }

func (s bunnyHash) Size() int { return s.size }

func (s bunnyHash) Sum(dst []byte, k *Key, msg string) []byte {
	return append(dst, s.sum(k.secret, msg)...)
}

func (bunnyHash) Encode(raw []byte) string { return base64.RawURLEncoding.EncodeToString(raw) }

// Decode accepts unpadded or padded URL-safe base64 (the SDKs strip "=",
// hand-written tokens may not).
func (bunnyHash) Decode(dst []byte, sig string) ([]byte, error) {
	return base64.RawURLEncoding.AppendDecode(dst, []byte(strings.TrimRight(sig, "=")))
}

// bunnyHS256 is "HS256-" b64url(HMAC-SHA256(key, msg)).
type bunnyHS256 struct{}

// NewBunnyHS256Scheme returns Bunny's current advanced token
// authentication (HMAC-SHA256, "HS256-" prefix).
func NewBunnyHS256Scheme() Scheme { return bunnyHS256{} }

func (bunnyHS256) Name() string { return "bunny_hs256" }

func (bunnyHS256) Size() int { return sha256.Size }

func (bunnyHS256) Sum(dst []byte, k *Key, msg string) []byte {
	mac := hmac.New(sha256.New, k.secret)
	_, _ = io.WriteString(mac, msg)
	var sum [sha256.Size]byte
	return append(dst, mac.Sum(sum[:0])...)
}

func (bunnyHS256) Encode(raw []byte) string {
	return BunnyHS256Prefix + base64.RawURLEncoding.EncodeToString(raw)
}

// errBunnyIPLocked: "HS256-1-…" tokens bind the client IP, which dialects
// never see.
var errBunnyIPLocked = errors.New("sign: bunny ip-locked token")

func (bunnyHS256) Decode(dst []byte, sig string) ([]byte, error) {
	body, ok := strings.CutPrefix(sig, BunnyHS256Prefix)
	if !ok {
		return dst, ErrMalformedSignature
	}
	if strings.HasPrefix(body, "1-") {
		return dst, errBunnyIPLocked
	}
	return base64.RawURLEncoding.AppendDecode(dst, []byte(strings.TrimRight(body, "=")))
}

// BunnyParam is one decoded query parameter of a Bunny URL.
type BunnyParam struct {
	Key, Value string
}

// BunnySignedParam reports whether key is covered by the token's signing
// data, i.e. is neither the token nor the expiry.
func BunnySignedParam(key string) bool {
	return key != BunnyTokenParam && key != BunnyExpiresParam && key != BunnyPathTokenParam
}

// BunnyMessage returns the message a token signs, for the layout the token
// uses (see the package comment above): path ‖ expires for basic tokens,
// path ‖ expires ‖ data otherwise. path is the decoded request path; it is
// replaced by token_path when params carry one. params are the URL's
// decoded parameters in any order; token, expires and bcdn_token are
// skipped. A parameter present twice is an errs.KindUnauthorized error
// wrapping ErrMalformedSignature (the SDKs refuse to sign such URLs).
func BunnyMessage(token, path, expires string, params []BunnyParam) (string, error) {
	var signed []BunnyParam
	ignore := false
	for _, p := range params {
		if !BunnySignedParam(p.Key) {
			continue
		}
		for _, q := range signed {
			if q.Key == p.Key {
				return "", errs.Wrap(errs.KindUnauthorized, ErrMalformedSignature)
			}
		}
		signed = append(signed, p)
		switch p.Key {
		case BunnyTokenPathParam:
			path = p.Value
		case BunnyIgnoreParamsParam:
			ignore = p.Value == "true"
		}
	}
	if bunnyLayoutOf(token) == BunnyBasic {
		return path + expires, nil
	}
	if ignore {
		signed = slices.DeleteFunc(signed, func(p BunnyParam) bool {
			return p.Key != BunnyIgnoreParamsParam && p.Key != BunnyTokenPathParam
		})
	}
	slices.SortFunc(signed, func(a, b BunnyParam) int { return strings.Compare(a.Key, b.Key) })
	var b strings.Builder
	n := len(path) + len(expires)
	for _, p := range signed {
		n += len(p.Key) + len(p.Value) + 2
	}
	b.Grow(n)
	b.WriteString(path)
	b.WriteString(expires)
	for i, p := range signed {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(p.Key)
		b.WriteByte('=')
		b.WriteString(p.Value)
	}
	return b.String(), nil
}

// bunnyLayoutOf classifies a presented token: the HS256 prefix, else by
// length (22 base64 characters for MD5, 43 for SHA-256).
func bunnyLayoutOf(token string) BunnyLayout {
	switch {
	case strings.HasPrefix(token, BunnyHS256Prefix):
		return BunnyHS256
	case len(strings.TrimRight(token, "=")) <= base64.RawURLEncoding.EncodedLen(md5.Size):
		return BunnyBasic
	default:
		return BunnySHA256
	}
}

// BunnyOptions configures NewBunnyVerifier.
type BunnyOptions struct {
	// Keys are the pull zone token authentication keys, raw (not hex);
	// salts are ignored. Empty means token authentication is off.
	Keys *Keyring
	// Required rejects URLs without a token.
	Required bool
	// Layouts are the accepted token layouts; 0 means BunnyAllLayouts.
	Layouts BunnyLayout
}

// BunnyVerifier verifies Bunny tokens of any enabled layout. Verify takes
// the token and the message built by BunnyMessage for that token.
type BunnyVerifier struct {
	basic, sha, hs *KeyedVerifier
	layouts        BunnyLayout
}

var _ Verifier = (*BunnyVerifier)(nil)

// NewBunnyVerifier returns a verifier for Bunny token authentication.
func NewBunnyVerifier(o BunnyOptions) (*BunnyVerifier, error) {
	if o.Layouts == 0 {
		o.Layouts = BunnyAllLayouts
	}
	if o.Layouts&^BunnyAllLayouts != 0 {
		return nil, errors.New("sign: bunny: unknown token layout")
	}
	opts := Options{Keys: o.Keys, Required: o.Required}
	v := &BunnyVerifier{layouts: o.Layouts}
	var err error
	if v.basic, err = NewVerifier(NewBunnyBasicScheme(), opts); err != nil {
		return nil, err
	}
	if v.sha, err = NewVerifier(NewBunnySHA256Scheme(), opts); err != nil {
		return nil, err
	}
	if v.hs, err = NewVerifier(NewBunnyHS256Scheme(), opts); err != nil {
		return nil, err
	}
	return v, nil
}

// Enabled reports whether any key is configured.
func (v *BunnyVerifier) Enabled() bool { return v.hs.Enabled() }

// Verify implements Verifier: token is the presented token ("" when
// absent) and msg is BunnyMessage(token, …).
func (v *BunnyVerifier) Verify(token, msg string) (Result, error) {
	if token == "" {
		return v.hs.Verify("", msg)
	}
	layout := bunnyLayoutOf(token)
	if v.layouts&layout == 0 && v.hs.Enabled() {
		return Unsigned, errs.Wrap(errs.KindUnauthorized, ErrMalformedSignature)
	}
	switch layout {
	case BunnyBasic:
		return v.basic.Verify(token, msg)
	case BunnySHA256:
		return v.sha.Verify(token, msg)
	default:
		return v.hs.Verify(token, msg)
	}
}
