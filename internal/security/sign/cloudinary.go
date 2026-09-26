package sign

import (
	"crypto/sha1" //nolint:gosec // Cloudinary's delivery URL signature is defined over SHA-1
	"crypto/sha256"
	"encoding/base64"
	"hash"
	"io"
	"strings"
)

// Cloudinary delivery URL signatures (docs/URL-DIALECTS.md §6,
// https://cloudinary.com/documentation/delivery_url_signatures):
//
//	s--<sig>--   sig = first N chars of base64url(H(message ‖ api_secret))
//
// where message is the path after the signature segment with the version
// segment removed ("<transformations>/<public_id>.<ext>", or the escaped
// remote URL for fetch), exactly as the SDKs emit it. Short signatures are
// N = 8 with H = SHA-1 (or SHA-256 when the account's signature algorithm
// is sha256); long signatures (long_url_signature) are N = 32 with
// H = SHA-256. The construction is a plain hash of message and secret, not
// an HMAC, and there is no salt. It is reproduced from the Node SDK
// (cloudinary-npm lib/utils/index.js url and compute_hash, MIT).
const (
	// CloudinaryShortLen is the length of a short signature in characters.
	CloudinaryShortLen = 8
	// CloudinaryLongLen is the length of a long signature in characters.
	CloudinaryLongLen = 32

	cloudinarySigPrefix = "s--"
	cloudinarySigSuffix = "--"
)

// cloudinaryScheme is one Cloudinary signature flavour. 8 and 32 base64
// characters are exactly 6 and 24 bytes, so truncating the base64 text is
// truncating the digest.
type cloudinaryScheme struct {
	name    string
	newHash func() hash.Hash
	size    int // raw bytes
}

// NewCloudinaryScheme returns the Cloudinary scheme: long selects the
// 32-character SHA-256 signature, otherwise the 8-character signature
// uses SHA-256 when shortSHA256 is set and SHA-1 when it is not.
func NewCloudinaryScheme(long, shortSHA256 bool) Scheme {
	switch {
	case long:
		return cloudinaryScheme{name: "cloudinary-long", newHash: sha256.New, size: CloudinaryLongLen * 3 / 4}
	case shortSHA256:
		return cloudinaryScheme{name: "cloudinary-sha256", newHash: sha256.New, size: CloudinaryShortLen * 3 / 4}
	default:
		return cloudinaryScheme{name: "cloudinary", newHash: sha1.New, size: CloudinaryShortLen * 3 / 4}
	}
}

func (s cloudinaryScheme) Name() string { return s.name }

func (s cloudinaryScheme) Size() int { return s.size }

func (s cloudinaryScheme) Sum(dst []byte, k *Key, msg string) []byte {
	h := s.newHash()
	_, _ = io.WriteString(h, msg)
	_, _ = h.Write(k.secret)
	var sum [sha256.Size]byte
	return append(dst, h.Sum(sum[:0])[:s.size]...)
}

func (cloudinaryScheme) Encode(raw []byte) string {
	return base64.RawURLEncoding.EncodeToString(raw)
}

// Decode accepts the URL-safe alphabet the SDKs emit ("+" → "-",
// "/" → "_"); 8 and 32 characters decode without padding or spare bits.
func (cloudinaryScheme) Decode(dst []byte, sig string) ([]byte, error) {
	return base64.RawURLEncoding.Strict().AppendDecode(dst, []byte(sig))
}

// CloudinarySignature extracts the signature from a path segment of the
// form "s--<sig>--". ok is false when segment has another shape; sig may
// then still be malformed, which Verify reports.
func CloudinarySignature(segment string) (sig string, ok bool) {
	rest, ok := strings.CutPrefix(segment, cloudinarySigPrefix)
	if !ok {
		return "", false
	}
	sig, ok = strings.CutSuffix(rest, cloudinarySigSuffix)
	if !ok || sig == "" {
		return "", false
	}
	return sig, true
}

// CloudinaryOptions configures NewCloudinaryVerifier.
type CloudinaryOptions struct {
	// Keys are the api secrets (raw bytes, not hex); salts are ignored.
	Keys *Keyring
	// Trusted signatures (the text between "s--" and "--") are accepted
	// verbatim.
	Trusted []string
	// Required rejects URLs without a signature segment.
	Required bool
	// ShortSHA256 verifies 8-character signatures with SHA-256 instead of
	// SHA-1 (accounts whose signature algorithm is sha256).
	ShortSHA256 bool
	// LongOnly rejects 8-character signatures (48 bits) and accepts only
	// the 32-character long form.
	LongOnly bool
}

// CloudinaryVerifier checks Cloudinary signatures. It dispatches on the
// length of the presented signature: 8 characters use the short scheme,
// anything else the long one (so a wrong length is malformed).
type CloudinaryVerifier struct {
	short, long *KeyedVerifier
	longOnly    bool
}

var _ Verifier = (*CloudinaryVerifier)(nil)

// NewCloudinaryVerifier returns a verifier for s--…-- signatures. Call
// Verify with the text between "s--" and "--" ("" when the URL has no
// signature segment) and the message the dialect builds.
func NewCloudinaryVerifier(o CloudinaryOptions) (*CloudinaryVerifier, error) {
	opts := Options{Keys: o.Keys, Trusted: o.Trusted, Required: o.Required}
	short, err := NewVerifier(NewCloudinaryScheme(false, o.ShortSHA256), opts)
	if err != nil {
		return nil, err
	}
	long, err := NewVerifier(NewCloudinaryScheme(true, false), opts)
	if err != nil {
		return nil, err
	}
	return &CloudinaryVerifier{short: short, long: long, longOnly: o.LongOnly}, nil
}

// Enabled reports whether any key is configured.
func (v *CloudinaryVerifier) Enabled() bool { return v.long.Enabled() }

// Verify implements Verifier.
func (v *CloudinaryVerifier) Verify(sig, msg string) (Result, error) {
	if len(sig) == CloudinaryShortLen && !v.longOnly {
		return v.short.Verify(sig, msg)
	}
	return v.long.Verify(sig, msg)
}

// SignCloudinary returns the signature segment "s--<sig>--" for msg. s
// must use a Cloudinary scheme.
func (s *Signer) SignCloudinary(msg string) (string, error) {
	sig, err := s.Sign(msg)
	if err != nil {
		return "", err
	}
	return cloudinarySigPrefix + sig + cloudinarySigSuffix, nil
}
