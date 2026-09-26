package sign

import (
	"errors"
	"fmt"
)

// Signer produces signatures with the first (primary) key of a keyring. It
// backs tests, the `sign` subcommand and the playground; the request path
// only verifies.
type Signer struct {
	scheme Scheme
	keys   *Keyring
}

// NewSigner returns a signer for scheme using keys' first key.
func NewSigner(scheme Scheme, keys *Keyring) (*Signer, error) {
	if scheme == nil {
		return nil, errors.New("sign: nil scheme")
	}
	if keys.Len() == 0 {
		return nil, fmt.Errorf("sign: %s: signer needs at least one key", scheme.Name())
	}
	return &Signer{scheme: scheme, keys: keys}, nil
}

// Sign returns the encoded signature of msg.
func (s *Signer) Sign(msg string) (string, error) {
	if s.keys.isDestroyed() {
		return "", ErrKeysDestroyed
	}
	var buf [maxRawSize]byte
	return s.scheme.Encode(s.scheme.Sum(buf[:0], &s.keys.keys[0], msg)), nil
}

// SignImgproxyPath returns "/" + signature + path for an imgproxy path
// (everything after the signature segment, starting with "/").
func (s *Signer) SignImgproxyPath(path string) (string, error) {
	sig, err := s.Sign(path)
	if err != nil {
		return "", err
	}
	if path == "" || path[0] != '/' {
		path = "/" + path
	}
	return "/" + sig + path, nil
}

// SignXIQuery returns rawQuery with any existing xi_sig removed and a fresh
// xi_sig for path appended as the last parameter.
func (s *Signer) SignXIQuery(path, rawQuery string) (string, error) {
	_, msg, err := XIMessage(path, rawQuery)
	if err != nil {
		return "", err
	}
	sig, err := s.Sign(msg)
	if err != nil {
		return "", err
	}
	q := msg[len(path):]
	if q == "" {
		return XIParam + "=" + sig, nil
	}
	return q[1:] + "&" + XIParam + "=" + sig, nil
}
