package sign

import (
	"crypto/md5" //nolint:gosec // G501: imgix's URL signature is defined as MD5; not our choice of primitive
	"encoding/hex"
	"io"
)

// ImgixParam is the query parameter that carries an imgix signature.
const ImgixParam = "s"

// imgixScheme is imgix's secure URL signature (imgix-blueprint "Securing
// URLs", @imgix/js-core _signParams, BSD-2-Clause):
//
//	lowercase-hex(MD5(token ‖ path [‖ "?" ‖ query-without-s]))
//
// The token is the key's secret used verbatim (not hex-decoded by imgix: build
// the keyring with NewKey([]byte(token), nil)); salts are ignored. MD5 is
// what imgix defines; it is a keyed prefix hash, so a new token should be
// long and random.
type imgixScheme struct{}

// NewImgixScheme returns the imgix signing scheme. The message is built by
// ImgixMessage.
func NewImgixScheme() Scheme { return imgixScheme{} }

func (imgixScheme) Name() string { return "imgix" }

func (imgixScheme) Size() int { return md5.Size }

func (imgixScheme) Sum(dst []byte, k *Key, msg string) []byte {
	h := md5.New() //nolint:gosec // G401: imgix signature algorithm
	h.Write(k.secret)
	_, _ = io.WriteString(h, msg)
	var sum [md5.Size]byte
	return append(dst, h.Sum(sum[:0])...)
}

func (imgixScheme) Encode(raw []byte) string { return hex.EncodeToString(raw) }

// Decode accepts hex in either case (imgix emits lowercase).
func (imgixScheme) Decode(dst []byte, sig string) ([]byte, error) {
	return hex.AppendDecode(dst, []byte(sig))
}

// ImgixMessage splits a request into the presented s value and the message
// imgix signs, without the token: the escaped path (with its leading "/")
// followed by "?" and the raw query without the s parameter when that
// remainder is non-empty. Parameters keep their order and encoding; empty
// segments are dropped. imgix requires s to be the last parameter; it is
// accepted anywhere, since the signed bytes are the same.
//
// sig is "" when the query has no s. More than one s is an
// errs.KindUnauthorized error wrapping ErrMalformedSignature.
func ImgixMessage(path, rawQuery string) (sig, msg string, err error) {
	return cutSignature(path, rawQuery, ImgixParam)
}

// ImgixOptions configures NewImgixVerifier.
type ImgixOptions struct {
	Keys     *Keyring // secrets are the raw imgix tokens; salts are ignored
	Trusted  []string
	Required bool
}

// NewImgixVerifier returns a verifier for imgix's s parameter. Call
// Verify(sig, msg) with the results of ImgixMessage. An absent s is
// Unsigned unless Required.
func NewImgixVerifier(o ImgixOptions) (*KeyedVerifier, error) {
	return NewVerifier(NewImgixScheme(), Options{Keys: o.Keys, Trusted: o.Trusted, Required: o.Required})
}

// SignImgixQuery returns rawQuery without any s parameter plus a fresh s
// for path appended as the last parameter, as @imgix/js-core builds it. s
// must use the imgix scheme.
func (s *Signer) SignImgixQuery(path, rawQuery string) (string, error) {
	_, msg, err := ImgixMessage(path, rawQuery)
	if err != nil {
		return "", err
	}
	sig, err := s.Sign(msg)
	if err != nil {
		return "", err
	}
	q := msg[len(path):]
	if q == "" {
		return ImgixParam + "=" + sig, nil
	}
	return q[1:] + "&" + ImgixParam + "=" + sig, nil
}
