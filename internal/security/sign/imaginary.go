package sign

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/url"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// ImaginaryParam is the query parameter that carries an imaginary signature.
const ImaginaryParam = "sign"

// imaginaryScheme is imaginary's URL signature (imaginary middleware.go
// validateURLSignature, MIT):
//
//	base64url-nopad(HMAC-SHA256(key, path ‖ url.Values.Encode()))
//
// The full 32-byte MAC is used. imaginary has no salt, so a key's salt is
// ignored: configure a key with an empty salt for imaginary clients.
type imaginaryScheme struct{}

// NewImaginaryScheme returns the imaginary signing scheme. The message is
// built by ImaginaryMessage.
func NewImaginaryScheme() Scheme { return imaginaryScheme{} }

func (imaginaryScheme) Name() string { return "imaginary" }

func (imaginaryScheme) Size() int { return sha256.Size }

func (imaginaryScheme) Sum(dst []byte, k *Key, msg string) []byte {
	mac := hmac.New(sha256.New, k.secret)
	_, _ = io.WriteString(mac, msg)
	var sum [sha256.Size]byte
	return append(dst, mac.Sum(sum[:0])...)
}

func (imaginaryScheme) Encode(raw []byte) string {
	return base64.RawURLEncoding.EncodeToString(raw)
}

// Decode matches imaginary: unpadded URL-safe base64 (Go's
// base64.RawURLEncoding, non-strict about trailing bits).
func (imaginaryScheme) Decode(dst []byte, sig string) ([]byte, error) {
	return base64.RawURLEncoding.AppendDecode(dst, []byte(sig))
}

// ImaginaryMessage splits a request into the presented sign value and the
// message imaginary signs: path (decoded, as http.Request.URL.Path)
// followed directly, without "?", by the query without sign re-encoded by
// url.Values.Encode (keys sorted, values in order, QueryEscape'd).
// Malformed query pairs are dropped as in http.Request.URL.Query.
//
// sig is "" when there is no sign parameter. More than one sign is an
// errs.KindUnauthorized error wrapping ErrMalformedSignature (imaginary
// uses the first).
func ImaginaryMessage(path, rawQuery string) (sig, msg string, err error) {
	// Like URL.Query() in imaginary, keep the valid pairs and ignore the error.
	q, _ := url.ParseQuery(rawQuery)
	switch s := q[ImaginaryParam]; len(s) {
	case 0:
	case 1:
		sig = s[0]
	default:
		return "", "", errs.Wrap(errs.KindUnauthorized, ErrMalformedSignature)
	}
	delete(q, ImaginaryParam)
	return sig, path + q.Encode(), nil
}

// ImaginaryOptions configures NewImaginaryVerifier.
type ImaginaryOptions struct {
	Keys     *Keyring // salts are ignored
	Trusted  []string
	Required bool
}

// NewImaginaryVerifier returns a verifier for imaginary's sign parameter.
// Call Verify(sig, msg) with the results of ImaginaryMessage. An absent
// sign is Unsigned unless Required.
func NewImaginaryVerifier(o ImaginaryOptions) (*KeyedVerifier, error) {
	return NewVerifier(NewImaginaryScheme(), Options{Keys: o.Keys, Trusted: o.Trusted, Required: o.Required})
}

// SignImaginaryQuery returns rawQuery re-encoded without sign plus a fresh
// sign for path appended, in imaginary's format. s must use the imaginary
// scheme.
func (s *Signer) SignImaginaryQuery(path, rawQuery string) (string, error) {
	_, msg, err := ImaginaryMessage(path, rawQuery)
	if err != nil {
		return "", err
	}
	sig, err := s.Sign(msg)
	if err != nil {
		return "", err
	}
	q := msg[len(path):]
	if q == "" {
		return ImaginaryParam + "=" + sig, nil
	}
	return q + "&" + ImaginaryParam + "=" + sig, nil
}
