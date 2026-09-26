package sign

import (
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// ImgproxyPlaceholders are the signature segments conventionally used for
// unsigned imgproxy URLs. With keys configured and signing not required
// they mean Unsigned; with signing off every segment does.
func ImgproxyPlaceholders() []string { return []string{"insecure", "unsafe", "_"} }

// optionAsSignaturePublic is imgproxy's client-facing message for a URL
// whose signature segment was left out.
const optionAsSignaturePublic = "The signature appears to be a processing option. " +
	"The signature section should always be present in the URL."

// ImgproxyOptions configures NewImgproxyVerifier.
type ImgproxyOptions struct {
	Keys     *Keyring
	Size     int // XI_SIGNING_SIZE, 1–32
	Trusted  []string
	Required bool
}

// ImgproxyVerifier verifies imgproxy signatures. msg is the path after the
// signature segment, starting with "/".
type ImgproxyVerifier struct {
	v *KeyedVerifier
}

var _ Verifier = (*ImgproxyVerifier)(nil)

// NewImgproxyVerifier returns the imgproxy verifier.
func NewImgproxyVerifier(o ImgproxyOptions) (*ImgproxyVerifier, error) {
	scheme, err := NewImgproxyScheme(o.Size)
	if err != nil {
		return nil, err
	}
	v, err := NewVerifier(scheme, Options{
		Keys:         o.Keys,
		Trusted:      o.Trusted,
		Required:     o.Required,
		Placeholders: ImgproxyPlaceholders(),
	})
	if err != nil {
		return nil, err
	}
	return &ImgproxyVerifier{v: v}, nil
}

// Verify implements Verifier with imgproxy semantics (security/signature.go
// VerifySignature): with signing off any signature segment is accepted as
// Unsigned unless it contains ":" (a processing option where the signature
// should be); otherwise see KeyedVerifier.Verify.
func (p *ImgproxyVerifier) Verify(sig, path string) (Result, error) {
	if !p.v.Enabled() && strings.Contains(sig, ":") {
		return Unsigned, errs.WithPublic(
			errs.Wrap(errs.KindUnauthorized, ErrOptionAsSignature), optionAsSignaturePublic)
	}
	return p.v.Verify(sig, path)
}

// Scheme returns the imgproxy scheme (for building a Signer).
func (p *ImgproxyVerifier) Scheme() Scheme { return p.v.Scheme() }
