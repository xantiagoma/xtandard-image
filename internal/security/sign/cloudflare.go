package sign

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// Query parameters of Cloudflare Images signed URLs.
const (
	CloudflareParam    = "sig"
	CloudflareExpParam = "exp"
)

// cloudflareScheme is the Cloudflare Images signed-URL token (developers.
// cloudflare.com/images/optimization/hosted-images/serve-private-images/):
//
//	sig = hex(HMAC-SHA256(key, path ‖ "?" ‖ query without sig))
//
// The key is the raw signing key string from the dashboard (not hex), there
// is no salt and no truncation. The message is built by CloudflareMessage.
type cloudflareScheme struct{}

// NewCloudflareScheme returns the Cloudflare Images signing scheme. A key's
// salt is ignored.
func NewCloudflareScheme() Scheme { return cloudflareScheme{} }

func (cloudflareScheme) Name() string { return "cloudflare" }

func (cloudflareScheme) Size() int { return sha256.Size }

func (cloudflareScheme) Sum(dst []byte, k *Key, msg string) []byte {
	mac := hmac.New(sha256.New, k.secret)
	_, _ = io.WriteString(mac, msg)
	var sum [sha256.Size]byte
	return append(dst, mac.Sum(sum[:0])...)
}

// Encode renders lower-case hex, as the documented Worker does.
func (cloudflareScheme) Encode(raw []byte) string { return hex.EncodeToString(raw) }

// Decode accepts hex in either case.
func (cloudflareScheme) Decode(dst []byte, sig string) ([]byte, error) {
	return hex.AppendDecode(dst, []byte(sig))
}

// CloudflareMessage splits a request into the presented sig value and the
// message Cloudflare signs: path (the escaped URL path, e.g.
// "/<account_hash>/<image_id>/<variant>"), "?", and the raw query without
// any sig parameter, parameters in their original order and encoding. The
// "?" is always present, as in the documented Worker
// (url.pathname + "?" + url.searchParams.toString()); empty segments are
// dropped.
//
// sig is "" when there is no sig parameter. More than one sig is an
// errs.KindUnauthorized error wrapping ErrMalformedSignature.
func CloudflareMessage(path, rawQuery string) (sig, msg string, err error) {
	var (
		b     strings.Builder
		found bool
	)
	b.Grow(len(path) + 1 + len(rawQuery))
	b.WriteString(path)
	b.WriteByte('?')
	first := true
	for part := range strings.SplitSeq(rawQuery, "&") {
		if part == "" {
			continue
		}
		key, val, _ := strings.Cut(part, "=")
		if key == CloudflareParam {
			if found {
				return "", "", errs.Wrap(errs.KindUnauthorized, ErrMalformedSignature)
			}
			found, sig = true, val
			continue
		}
		if !first {
			b.WriteByte('&')
		}
		first = false
		b.WriteString(part)
	}
	return sig, b.String(), nil
}

// CloudflareOptions configures NewCloudflareVerifier.
type CloudflareOptions struct {
	Keys     *Keyring // raw signing keys; salts are ignored
	Trusted  []string
	Required bool
}

// NewCloudflareVerifier returns a verifier for Cloudflare Images sig
// tokens. Call Verify(sig, msg) with the results of CloudflareMessage; the
// caller checks exp. An absent sig is Unsigned unless Required.
func NewCloudflareVerifier(o CloudflareOptions) (*KeyedVerifier, error) {
	return NewVerifier(NewCloudflareScheme(), Options{Keys: o.Keys, Trusted: o.Trusted, Required: o.Required})
}

// SignCloudflareQuery returns rawQuery without sig plus a fresh sig for
// path appended, in Cloudflare's format. s must use the Cloudflare scheme.
func (s *Signer) SignCloudflareQuery(path, rawQuery string) (string, error) {
	_, msg, err := CloudflareMessage(path, rawQuery)
	if err != nil {
		return "", err
	}
	sig, err := s.Sign(msg)
	if err != nil {
		return "", err
	}
	q := msg[len(path)+1:]
	if q == "" {
		return CloudflareParam + "=" + sig, nil
	}
	return q + "&" + CloudflareParam + "=" + sig, nil
}
