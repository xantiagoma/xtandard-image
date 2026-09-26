package sign

import (
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// XIParam is the query parameter that carries an xi_sig signature.
const XIParam = "xi_sig"

// XIMessage splits a request into the presented xi_sig value and the
// message it signs: path, followed by "?" and the raw query without any
// xi_sig parameter when that remainder is non-empty. Parameters keep their
// original order and encoding; empty segments ("a=1&&b=2") are dropped.
// The parameter name is matched on the raw (still-escaped) key.
//
// sig is "" when the query has no xi_sig. More than one xi_sig is an
// errs.KindUnauthorized error wrapping ErrMalformedSignature.
func XIMessage(path, rawQuery string) (sig, msg string, err error) {
	var (
		b     strings.Builder
		found bool
	)
	b.Grow(len(path) + 1 + len(rawQuery))
	b.WriteString(path)
	first := true
	for part := range strings.SplitSeq(rawQuery, "&") {
		if part == "" {
			continue
		}
		key, val, _ := strings.Cut(part, "=")
		if key == XIParam {
			if found {
				return "", "", errs.Wrap(errs.KindUnauthorized, ErrMalformedSignature)
			}
			found, sig = true, val
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
	return sig, b.String(), nil
}

// XIOptions configures NewXIVerifier. Keys, Size and Trusted are shared
// with the imgproxy scheme (XI_SIGNING_*).
type XIOptions struct {
	Keys     *Keyring
	Size     int
	Trusted  []string
	Required bool
}

// NewXIVerifier returns a verifier for xi_sig. Call Verify(sig, msg) with
// the results of XIMessage. An absent xi_sig is Unsigned unless Required.
func NewXIVerifier(o XIOptions) (*KeyedVerifier, error) {
	scheme, err := NewXIScheme(o.Size)
	if err != nil {
		return nil, err
	}
	return NewVerifier(scheme, Options{Keys: o.Keys, Trusted: o.Trusted, Required: o.Required})
}
