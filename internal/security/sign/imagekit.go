package sign

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // ImageKit's URL signature is HMAC-SHA1; HMAC does not rely on SHA-1 collision resistance
	"encoding/hex"
	"io"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// ImageKit query parameters (imagekit-nodejs src/resources/helper.ts).
const (
	// ImageKitSignatureParam carries the hex HMAC-SHA1 signature.
	ImageKitSignatureParam = "ik-s"
	// ImageKitExpiresParam carries the expiry as unix seconds.
	ImageKitExpiresParam = "ik-t"
	// ImageKitNoExpiry is the timestamp the SDK signs when a URL does not
	// expire; it is signed but not written to the URL.
	ImageKitNoExpiry = "9999999999"
)

// imageKitScheme is ImageKit's URL signature (imagekit-nodejs helper.ts
// getSignature, Apache-2.0):
//
//	hex(HMAC-SHA1(private_key, url-relative-to-endpoint ‖ expiry))
//
// The private key is used as written (no hex decoding); salts are ignored.
type imageKitScheme struct{}

// NewImageKitScheme returns the ImageKit signing scheme. The message is
// built by ImageKitMessage.
func NewImageKitScheme() Scheme { return imageKitScheme{} }

func (imageKitScheme) Name() string { return "imagekit" }

func (imageKitScheme) Size() int { return sha1.Size }

func (imageKitScheme) Sum(dst []byte, k *Key, msg string) []byte {
	mac := hmac.New(sha1.New, k.secret)
	_, _ = io.WriteString(mac, msg)
	var sum [sha1.Size]byte
	return append(dst, mac.Sum(sum[:0])...)
}

func (imageKitScheme) Encode(raw []byte) string { return hex.EncodeToString(raw) }

// Decode accepts upper- and lower-case hex.
func (imageKitScheme) Decode(dst []byte, sig string) ([]byte, error) {
	return hex.AppendDecode(dst, []byte(sig))
}

// ImageKitMessage splits a request into the presented ik-s signature, the
// ik-t expiry as written, and the message ImageKit signs:
//
//	path ["?" query] expiry
//
// path is the escaped URL path relative to the URL endpoint (a leading "/"
// is removed, as the SDK strips the endpoint including its trailing
// slash). query is rawQuery without ik-s and ik-t, the remaining
// parameters kept in their original order and encoding (empty segments
// dropped); the "?" is present only when that remainder is non-empty.
// expiry is the ik-t value, or ImageKitNoExpiry when absent.
//
// sig and expires are "" when absent. A repeated ik-s or ik-t is an
// errs.KindUnauthorized error wrapping ErrMalformedSignature.
func ImageKitMessage(path, rawQuery string) (sig, expires, msg string, err error) {
	var (
		b                  strings.Builder
		hasSig, hasExpires bool
	)
	path = strings.TrimPrefix(path, "/")
	b.Grow(len(path) + 1 + len(rawQuery) + len(ImageKitNoExpiry))
	b.WriteString(path)
	first := true
	for part := range strings.SplitSeq(rawQuery, "&") {
		if part == "" {
			continue
		}
		key, val, _ := strings.Cut(part, "=")
		switch key {
		case ImageKitSignatureParam:
			if hasSig {
				return "", "", "", errs.Wrap(errs.KindUnauthorized, ErrMalformedSignature)
			}
			hasSig, sig = true, val
			continue
		case ImageKitExpiresParam:
			if hasExpires {
				return "", "", "", errs.Wrap(errs.KindUnauthorized, ErrMalformedSignature)
			}
			hasExpires, expires = true, val
			continue
		}
		if first {
			b.WriteByte('?')
			first = false
		} else {
			b.WriteByte('&')
		}
		b.WriteString(part)
	}
	if hasExpires {
		b.WriteString(expires)
	} else {
		b.WriteString(ImageKitNoExpiry)
	}
	return sig, expires, b.String(), nil
}

// ImageKitOptions configures NewImageKitVerifier.
type ImageKitOptions struct {
	Keys     *Keyring // ImageKit private keys, raw (not hex); salts are ignored
	Trusted  []string
	Required bool
}

// NewImageKitVerifier returns a verifier for ik-s. Call Verify(sig, msg)
// with the results of ImageKitMessage. An absent ik-s is Unsigned unless
// Required.
func NewImageKitVerifier(o ImageKitOptions) (*KeyedVerifier, error) {
	return NewVerifier(NewImageKitScheme(), Options{Keys: o.Keys, Trusted: o.Trusted, Required: o.Required})
}

// SignImageKitQuery returns rawQuery without ik-s and ik-t, followed by
// ik-t (only when expires > 0) and a fresh ik-s for path, in the order the
// SDK writes them. path is relative to the URL endpoint. s must use the
// ImageKit scheme.
func (s *Signer) SignImageKitQuery(path, rawQuery string, expires int64) (string, error) {
	q := stripImageKitParams(rawQuery)
	if expires > 0 {
		q = joinQuery(q, ImageKitExpiresParam+"="+strconv.FormatInt(expires, 10))
	}
	_, _, msg, err := ImageKitMessage(path, q)
	if err != nil {
		return "", err
	}
	sig, err := s.Sign(msg)
	if err != nil {
		return "", err
	}
	return joinQuery(q, ImageKitSignatureParam+"="+sig), nil
}

// stripImageKitParams drops ik-s and ik-t (and empty segments) from
// rawQuery.
func stripImageKitParams(rawQuery string) string {
	var b strings.Builder
	for part := range strings.SplitSeq(rawQuery, "&") {
		key, _, _ := strings.Cut(part, "=")
		if part == "" || key == ImageKitSignatureParam || key == ImageKitExpiresParam {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('&')
		}
		b.WriteString(part)
	}
	return b.String()
}

func joinQuery(q, param string) string {
	if q == "" {
		return param
	}
	return q + "&" + param
}
