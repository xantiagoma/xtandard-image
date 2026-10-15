package xtandard

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"slices"
)

// EncryptionKey is the first of XI_ENCRYPTION_KEYS (imgproxy's
// IMGPROXY_SOURCE_URL_ENCRYPTION_KEY), with an optional fixed IV. Make one with
// [ParseEncryptionKey] or [NewEncryptionKey]; it is immutable and safe for
// concurrent use. Its String method never prints the key material.
//
// Without a fixed IV every token gets a fresh random IV, as the server's own
// encoder does. A fixed IV makes URLs deterministic and reveals when two URLs
// share a source: use it only for tests or cache keys.
type EncryptionKey struct {
	key, iv []byte
}

// ParseEncryptionKey returns an encryption key from a hex AES key (16, 24 or
// 32 bytes) and a hex IV of 16 bytes, or "" for a random IV per token.
func ParseEncryptionKey(key, iv string) (EncryptionKey, error) {
	k, err := hexKey(key, "encryption key")
	if err != nil {
		return EncryptionKey{}, err
	}
	var v []byte
	if iv != "" {
		if v, err = hexKey(iv, "iv"); err != nil {
			return EncryptionKey{}, err
		}
	}
	return newEncryptionKey(k, v, iv != "")
}

// NewEncryptionKey returns an encryption key from raw key bytes and a fixed
// IV, or nil for a random IV per token. Both are copied.
func NewEncryptionKey(key, iv []byte) (EncryptionKey, error) {
	return newEncryptionKey(slices.Clone(key), slices.Clone(iv), iv != nil)
}

func newEncryptionKey(key, iv []byte, fixed bool) (EncryptionKey, error) {
	if !slices.Contains(EncryptionKeySizes(), len(key)) {
		return EncryptionKey{}, Errorf(ErrInvalidKey, "encryption key is %d bytes, want 16, 24 or 32", len(key))
	}
	if fixed && len(iv) != IVSize {
		return EncryptionKey{}, Errorf(ErrInvalidKey, "iv is %d bytes, want %d", len(iv), IVSize)
	}
	return EncryptionKey{key: key, iv: iv}, nil
}

// String implements fmt.Stringer without the key material.
func (k EncryptionKey) String() string { return "xtandard.EncryptionKey{redacted}" }

// GoString implements fmt.GoStringer without the key material.
func (k EncryptionKey) GoString() string { return k.String() }

// seal returns base64url_nopad(IV ‖ AES-CBC(key, IV, PKCS#7(plaintext))).
func seal(plaintext string, k EncryptionKey) (string, error) {
	block, err := aes.NewCipher(k.key)
	if err != nil {
		return "", Errorf(ErrInvalidKey, "encryption key: %v", err)
	}
	pad := aes.BlockSize - len(plaintext)%aes.BlockSize
	out := make([]byte, aes.BlockSize+len(plaintext)+pad)
	iv := out[:aes.BlockSize]
	if k.iv != nil {
		copy(iv, k.iv)
	} else {
		rand.Read(iv) // never fails (crypto/rand, Go 1.24+)
	}
	data := out[aes.BlockSize:]
	copy(data, plaintext)
	for i := len(plaintext); i < len(data); i++ {
		data[i] = byte(pad)
	}
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(data, data) //nolint:gosec // G407: the IV is random unless the caller fixed one on purpose (EncryptionKey docs)
	return b64url(out), nil
}

// EncryptSource encrypts a source URL into an enc/ token (without the "enc/"
// prefix). An empty source or one with control characters is an
// [ErrInvalidSource] error.
func EncryptSource(source string, key EncryptionKey) (string, error) {
	if err := CheckSource(source); err != nil {
		return "", err
	}
	return seal(source, key)
}

// EncryptPDFPassword encrypts the password of a password-protected PDF into a
// pdf_password (pdp) token: the enc/ format over the UTF-8 password, with the
// same key. The server accepts the option on signed URLs only. An empty
// password, one with control characters or one longer than
// [MaxPDFPasswordBytes] is an [ErrInvalidPassword] error.
func EncryptPDFPassword(password string, key EncryptionKey) (string, error) {
	if err := checkText(password, ErrInvalidPassword, "password"); err != nil {
		return "", err
	}
	if len(password) > MaxPDFPasswordBytes {
		return "", Errorf(ErrInvalidPassword, "password is longer than %d bytes", MaxPDFPasswordBytes)
	}
	return seal(password, key)
}
