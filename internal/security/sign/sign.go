package sign

import (
	"errors"
	"strconv"
)

// Sentinel causes. Verifiers return them wrapped in an errs.Error of kind
// errs.KindUnauthorized; match with errors.Is.
var (
	// ErrMissingSignature: signing is required but the URL carries no
	// signature (or only a placeholder such as "insecure").
	ErrMissingSignature = errors.New("sign: missing signature")
	// ErrMalformedSignature: the signature is not valid in the scheme's
	// encoding or has the wrong length.
	ErrMalformedSignature = errors.New("sign: malformed signature")
	// ErrSignatureMismatch: the signature does not match any key.
	ErrSignatureMismatch = errors.New("sign: signature mismatch")
	// ErrOptionAsSignature: imgproxy URL whose signature segment looks like a
	// processing option, i.e. the signature segment was left out.
	ErrOptionAsSignature = errors.New("sign: processing option in signature position")
	// ErrKeysDestroyed: the keyring was destroyed while still in use.
	ErrKeysDestroyed = errors.New("sign: keyring destroyed")
)

// Result is the outcome of a successful verification.
type Result uint8

const (
	// Unsigned means no signature was checked, either because signing is off or
	// because the request is deliberately unsigned and signing is not
	// required. The caller applies XI_SECURITY_UNSIGNED_POLICY.
	Unsigned Result = iota
	// Verified means the signature matched one of the keys.
	Verified
	// Trusted means the signature is on the trusted list; no MAC was computed.
	Trusted
)

// String returns the lower-case name used in logs and metric labels.
func (r Result) String() string {
	switch r {
	case Unsigned:
		return "unsigned"
	case Verified:
		return "verified"
	case Trusted:
		return "trusted"
	default:
		return "Result(" + strconv.Itoa(int(r)) + ")"
	}
}

// Signed reports whether r carries a verified or trusted signature.
func (r Result) Signed() bool { return r == Verified || r == Trusted }

// Verifier checks a presented signature against the signed message. The
// dialect extracts sig from the URL and builds msg in its canonical form.
//
// A nil error means the request may proceed; the Result says whether it is
// signed. A non-nil error is an errs error of kind errs.KindUnauthorized
// wrapping one of the sentinel errors above. Verifiers are safe for
// concurrent use and never do I/O.
type Verifier interface {
	Verify(sig, msg string) (Result, error)
}

// Scheme is one signing algorithm: MAC construction, truncation and wire
// encoding. Implementations live in this package and are stateless, so
// they are safe for concurrent use.
type Scheme interface {
	// Name identifies the scheme in logs ("imgproxy", "xi_sig", ...).
	Name() string
	// Size is the length in bytes of a raw (decoded, truncated) signature.
	Size() int
	// Sum appends the raw, truncated signature of msg under k to dst.
	Sum(dst []byte, k *Key, msg string) []byte
	// Encode renders a raw signature in the wire encoding.
	Encode(raw []byte) string
	// Decode appends the raw bytes of a wire signature to dst. It does not
	// check the length against Size.
	Decode(dst []byte, sig string) ([]byte, error)
}
