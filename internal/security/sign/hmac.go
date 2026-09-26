package sign

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
)

// Signature sizes for the salted HMAC-SHA256 schemes (XI_SIGNING_SIZE,
// imgproxy IMGPROXY_SIGNATURE_SIZE).
const (
	MinSignatureSize     = 1
	MaxSignatureSize     = sha256.Size
	DefaultSignatureSize = sha256.Size
)

// saltedHMACSHA256 is base64url-nopad(HMAC-SHA256(secret, salt ‖ msg)[:size]),
// the imgproxy construction (imgproxy security/signature.go signatureFor,
// Apache-2.0). It is shared by the imgproxy and xi_sig schemes.
type saltedHMACSHA256 struct {
	name string
	size int
}

// NewImgproxyScheme returns the imgproxy signing scheme with signatures
// truncated to size bytes (1–32; imgproxy IMGPROXY_SIGNATURE_SIZE).
func NewImgproxyScheme(size int) (Scheme, error) {
	return newSaltedHMACSHA256("imgproxy", size)
}

// NewXIScheme returns the xi_sig scheme: the imgproxy algorithm under its
// own name, applied to the message built by XIMessage.
func NewXIScheme(size int) (Scheme, error) {
	return newSaltedHMACSHA256("xi_sig", size)
}

func newSaltedHMACSHA256(name string, size int) (Scheme, error) {
	if size < MinSignatureSize || size > MaxSignatureSize {
		return nil, fmt.Errorf("sign: %s signature size %d out of range [%d, %d]",
			name, size, MinSignatureSize, MaxSignatureSize)
	}
	return saltedHMACSHA256{name: name, size: size}, nil
}

func (s saltedHMACSHA256) Name() string { return s.name }

func (s saltedHMACSHA256) Size() int { return s.size }

func (s saltedHMACSHA256) Sum(dst []byte, k *Key, msg string) []byte {
	mac := hmac.New(sha256.New, k.secret)
	mac.Write(k.salt)
	// imgproxy: the path is supposed to start with "/"; re-add it when the
	// caller split it off.
	if msg == "" || msg[0] != '/' {
		mac.Write([]byte{'/'})
	}
	_, _ = io.WriteString(mac, msg)
	var sum [sha256.Size]byte
	return append(dst, mac.Sum(sum[:0])[:s.size]...)
}

func (saltedHMACSHA256) Encode(raw []byte) string {
	return base64.RawURLEncoding.EncodeToString(raw)
}

// Decode matches imgproxy: unpadded URL-safe base64, non-strict about
// trailing bits.
func (saltedHMACSHA256) Decode(dst []byte, sig string) ([]byte, error) {
	return base64.RawURLEncoding.AppendDecode(dst, []byte(sig))
}
