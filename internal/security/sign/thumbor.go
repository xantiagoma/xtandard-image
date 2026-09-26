package sign

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // thumbor's URL signature is HMAC-SHA1 by protocol; HMAC-SHA1 is not affected by SHA-1 collisions
	"encoding/base64"
	"io"
)

// ThumborUnsafe is the first path segment of a deliberately unsigned
// thumbor URL.
const ThumborUnsafe = "unsafe"

// thumborScheme is thumbor's URL signature (libthumbor
// url_signers/base64_hmac_sha1.py, MIT):
//
//	base64url-padded(HMAC-SHA1(key, path))
//
// where path is the request path after "/<signature>/", still escaped (the
// Tornado request.path thumbor strips the prefix from). The full 20-byte MAC
// is used; the wire form is always 28 characters ending in "=". thumbor keys
// are plain strings (SECURITY_KEY), not hex, and have no salt: a key's salt
// is ignored.
type thumborScheme struct{}

// NewThumborScheme returns the thumbor signing scheme.
func NewThumborScheme() Scheme { return thumborScheme{} }

func (thumborScheme) Name() string { return "thumbor" }

func (thumborScheme) Size() int { return sha1.Size }

func (thumborScheme) Sum(dst []byte, k *Key, msg string) []byte {
	mac := hmac.New(sha1.New, k.secret)
	_, _ = io.WriteString(mac, msg)
	var sum [sha1.Size]byte
	return append(dst, mac.Sum(sum[:0])...)
}

func (thumborScheme) Encode(raw []byte) string {
	return base64.URLEncoding.EncodeToString(raw)
}

// Decode accepts only the canonical padded URL-safe encoding: thumbor
// compares the encoded strings, so any other spelling never matches there.
func (thumborScheme) Decode(dst []byte, sig string) ([]byte, error) {
	return base64.URLEncoding.Strict().AppendDecode(dst, []byte(sig))
}

// ThumborOptions configures NewThumborVerifier.
type ThumborOptions struct {
	Keys     *Keyring // raw SECURITY_KEY bytes; salts are ignored
	Trusted  []string
	Required bool // reject "unsafe" URLs (thumbor ALLOW_UNSAFE_URL=False)
}

// NewThumborVerifier returns a verifier for thumbor URLs. Call
// Verify(sig, path) with the percent-decoded signature segment and the
// escaped path after "/<signature>/"; pass ThumborUnsafe (or "") for an
// unsafe URL, which is Unsigned unless Required.
func NewThumborVerifier(o ThumborOptions) (*KeyedVerifier, error) {
	return NewVerifier(NewThumborScheme(), Options{
		Keys: o.Keys, Trusted: o.Trusted, Required: o.Required,
		Placeholders: []string{ThumborUnsafe},
	})
}

// SignThumborPath returns "/" + signature + "/" + path for a thumbor path
// (everything after the signature segment, without a leading "/", escaped
// exactly as it will be sent). s must use the thumbor scheme.
func (s *Signer) SignThumborPath(path string) (string, error) {
	sig, err := s.Sign(path)
	if err != nil {
		return "", err
	}
	return "/" + sig + "/" + path, nil
}
