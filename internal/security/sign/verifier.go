package sign

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"slices"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// maxRawSize bounds the raw signature bytes any scheme produces or accepts
// (SHA-512 would be 64). Longer presented signatures are rejected before
// decoding.
const maxRawSize = 64

// Options configures a KeyedVerifier.
type Options struct {
	// Keys to verify against, in order. Empty means signing is off and every
	// request is Unsigned.
	Keys *Keyring
	// Trusted signatures are accepted verbatim without computing a MAC
	// (XI_SIGNING_TRUSTED, imgproxy IMGPROXY_TRUSTED_SIGNATURES).
	Trusted []string
	// Required rejects requests without a signature (XI_SIGNING_REQUIRED).
	// When false, an absent signature or a Placeholder is Unsigned and the
	// caller applies the unsigned policy; a present, wrong signature is
	// still rejected. Requires at least one key.
	Required bool
	// Placeholders are signature values that mean "deliberately unsigned",
	// e.g. imgproxy's "insecure", "unsafe" and "_". The empty signature is
	// always one.
	Placeholders []string
}

// KeyedVerifier implements Verifier for any Scheme over a Keyring.
type KeyedVerifier struct {
	scheme       Scheme
	keys         *Keyring
	trusted      []string
	placeholders []string
	required     bool
}

var _ Verifier = (*KeyedVerifier)(nil)

// NewVerifier returns a verifier for scheme with the given options.
func NewVerifier(scheme Scheme, o Options) (*KeyedVerifier, error) {
	if scheme == nil {
		return nil, errors.New("sign: nil scheme")
	}
	if scheme.Size() < 1 || scheme.Size() > maxRawSize {
		return nil, fmt.Errorf("sign: %s: signature size %d out of range", scheme.Name(), scheme.Size())
	}
	if o.Required && o.Keys.Len() == 0 {
		return nil, fmt.Errorf("sign: %s: signing required but no keys configured", scheme.Name())
	}
	if o.Keys.isDestroyed() {
		return nil, fmt.Errorf("sign: %s: %w", scheme.Name(), ErrKeysDestroyed)
	}
	for i, t := range o.Trusted {
		if t == "" {
			return nil, fmt.Errorf("sign: %s: trusted signature %d is empty", scheme.Name(), i)
		}
	}
	return &KeyedVerifier{
		scheme:       scheme,
		keys:         o.Keys,
		trusted:      slices.Clone(o.Trusted),
		placeholders: slices.Clone(o.Placeholders),
		required:     o.Required,
	}, nil
}

// Scheme returns the verifier's scheme.
func (v *KeyedVerifier) Scheme() Scheme { return v.scheme }

// Enabled reports whether any key is configured.
func (v *KeyedVerifier) Enabled() bool { return v.keys.Len() > 0 }

// Verify implements Verifier. Order: signing off → Unsigned; trusted list →
// Trusted; absent/placeholder → Unsigned or ErrMissingSignature when
// required; otherwise the decoded signature must equal the MAC under one of
// the keys (constant-time).
func (v *KeyedVerifier) Verify(sig, msg string) (Result, error) {
	if v.keys.Len() == 0 {
		return Unsigned, nil
	}
	if v.keys.isDestroyed() {
		return Unsigned, errs.Wrap(errs.KindInternal, ErrKeysDestroyed)
	}
	if containsConstantTime(v.trusted, sig) {
		return Trusted, nil
	}
	if sig == "" || slices.Contains(v.placeholders, sig) {
		if v.required {
			return Unsigned, errs.Wrap(errs.KindUnauthorized, ErrMissingSignature)
		}
		return Unsigned, nil
	}
	// Any encoding needs at least one character per raw byte; reject absurd
	// lengths before decoding (fuzzing, abuse).
	if len(sig) > 4*maxRawSize {
		return Unsigned, errs.Wrap(errs.KindUnauthorized, ErrMalformedSignature)
	}
	var gotBuf, wantBuf [maxRawSize]byte
	got, err := v.scheme.Decode(gotBuf[:0], sig)
	if err != nil || len(got) != v.scheme.Size() {
		return Unsigned, errs.Wrap(errs.KindUnauthorized, ErrMalformedSignature)
	}
	for i := range v.keys.keys {
		want := v.scheme.Sum(wantBuf[:0], &v.keys.keys[i], msg)
		if subtle.ConstantTimeCompare(got, want) == 1 {
			return Verified, nil
		}
	}
	return Unsigned, errs.Wrap(errs.KindUnauthorized, ErrSignatureMismatch)
}

// containsConstantTime reports whether sig equals an entry of list. Each
// comparison is constant-time for equal lengths; lengths are not secret.
func containsConstantTime(list []string, sig string) bool {
	found := 0
	for _, t := range list {
		found |= subtle.ConstantTimeCompare([]byte(t), []byte(sig))
	}
	return found == 1
}
