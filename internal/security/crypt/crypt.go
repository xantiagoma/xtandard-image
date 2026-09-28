// Package crypt encrypts and decrypts imgproxy-compatible source URLs
// (the `enc/` segment, docs/URL-DIALECTS.md §3, docs/ARCHITECTURE.md §10).
//
// Token format, identical to imgproxy's reference implementation
// (.refs/imgproxy/examples/encrypted_source_url.go):
//
//	token = base64url_unpadded( IV[16] || AES-CBC(key, IV, PKCS#7(plaintext)) )
//
// Keys are hex strings of 16, 24 or 32 bytes (AES-128/192/256). Several keys
// may be configured for rotation: Encrypt always uses the first, Decrypt tries
// them in order.
//
// # Padding oracle
//
// Unauthenticated CBC is malleable and, if padding failures are observable,
// decryptable by a padding-oracle attack. This package makes the padding check
// itself constant-time and returns one error kind and one public message for
// every failure, but the HTTP response for a well-padded token still differs
// (it goes on to fetch a source) from a badly padded one. That difference is
// inherent to the imgproxy format. The only real defence is URL signing:
// deployments that use `enc/` sources must require signatures, so a forged
// ciphertext is rejected before it reaches Decrypt.
//
// # Key rotation
//
// CBC has no authentication tag, so a token decrypted with the wrong key
// still has valid padding about once in 256 tries. To pick the right key,
// Decrypt only accepts a plaintext that also looks like a source URL: valid
// UTF-8 without ASCII control characters. A wrong key passes both checks with
// negligible probability. Encrypt refuses plaintexts that Decrypt would
// reject, so every token it produces round-trips.
//
// # Document passwords
//
// The same token format carries document passwords (WP-115, the imgproxy
// dialect's pdf_password option): DecryptPassword reverses Encrypt with its
// own public message, and PasswordDigest is the keyed hash the result-cache
// key holds in place of the password. Its MAC key is derived from the first
// AES key (HMAC-SHA256 over a fixed label), so a digest that reaches a client
// through an ETag cannot be brute-forced offline without the server's key.
//
// Key bytes never appear in errors, String, GoString or slog output.
package crypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// MaxTokenLen bounds the base64 token accepted by Decrypt. It matches the
// server's MaxHeaderBytes (32 KiB), which already bounds any request path.
const MaxTokenLen = 32 << 10

// publicMessage is the only text a client sees for any decryption failure.
const publicMessage = "invalid encrypted source url"

// passwordPublicMessage is the only text a client sees for any failure to
// decrypt a document password token.
const passwordPublicMessage = "invalid encrypted document password" //nolint:gosec // G101: an error message, not a credential

// digestLabel derives the document password MAC key from the first AES key.
const digestLabel = "xtandard-image/crypt.PasswordDigest/v1"

// ErrInvalidToken is wrapped by every Decrypt failure, which is also of kind
// errs.KindUnauthorized.
var ErrInvalidToken = errors.New("crypt: invalid token")

// ErrInvalidKey is wrapped by New when a key is not hex or not 16/24/32 bytes.
var ErrInvalidKey = errors.New("crypt: invalid key")

// ErrInvalidPlaintext is returned by Encrypt for input that Decrypt would
// refuse: invalid UTF-8 or ASCII control characters.
var ErrInvalidPlaintext = errors.New("crypt: plaintext is not a printable utf-8 string")

// ErrNoKeys is returned by Encrypt and New when no key is configured.
var ErrNoKeys = errors.New("crypt: no keys configured")

// Cipher encrypts and decrypts source URL tokens. It is safe for concurrent
// use. Create it with New; the zero value has no keys and rejects everything.
type Cipher struct {
	blocks []cipher.Block // blocks[0] encrypts; all are tried to decrypt
	// digestKey is HMAC-SHA256(first key, digestLabel): the key of
	// PasswordDigest.
	digestKey []byte
}

// New builds a Cipher from hex-encoded AES keys. The first key encrypts; all
// keys decrypt, in order. Errors name the key's position, never its bytes.
func New(hexKeys ...string) (*Cipher, error) {
	if len(hexKeys) == 0 {
		return nil, ErrNoKeys
	}
	blocks := make([]cipher.Block, 0, len(hexKeys))
	var digestKey []byte
	var errList []error
	for i, hk := range hexKeys {
		key, err := hex.DecodeString(strings.TrimSpace(hk))
		if err != nil {
			// hex errors quote the offending character; do not wrap them.
			errList = append(errList, fmt.Errorf("key %d: not hex: %w", i, ErrInvalidKey))
			continue
		}
		switch len(key) {
		case 16, 24, 32:
		default:
			errList = append(errList, fmt.Errorf("key %d: %d bytes, want 16, 24 or 32: %w", i, len(key), ErrInvalidKey))
			continue
		}
		b, err := aes.NewCipher(key)
		if digestKey == nil {
			m := hmac.New(sha256.New, key)
			m.Write([]byte(digestLabel))
			digestKey = m.Sum(nil)
		}
		clear(key)
		if err != nil {
			errList = append(errList, fmt.Errorf("key %d: %w", i, ErrInvalidKey))
			continue
		}
		blocks = append(blocks, b)
	}
	if err := errors.Join(errList...); err != nil {
		return nil, err
	}
	return &Cipher{blocks: blocks, digestKey: digestKey}, nil
}

// Encrypt returns the unpadded base64url token for plaintext, encrypted with
// the first key under a fresh random IV.
func (c *Cipher) Encrypt(plaintext []byte) (string, error) {
	if c == nil || len(c.blocks) == 0 {
		return "", ErrNoKeys
	}
	if !plausible(plaintext) {
		return "", ErrInvalidPlaintext
	}
	var iv [aes.BlockSize]byte
	if _, err := rand.Read(iv[:]); err != nil {
		return "", fmt.Errorf("crypt: read iv: %w", err)
	}
	return seal(c.blocks[0], iv[:], plaintext), nil
}

// seal is Encrypt with a caller-chosen IV. Tests use it to reproduce fixed
// vectors; production code must use Encrypt.
func seal(b cipher.Block, iv, plaintext []byte) string {
	n := len(plaintext)/aes.BlockSize*aes.BlockSize + aes.BlockSize
	buf := make([]byte, aes.BlockSize+n)
	copy(buf, iv)
	body := buf[aes.BlockSize:]
	copy(body, plaintext)
	pad := byte(n - len(plaintext)) //nolint:gosec // always 1..aes.BlockSize
	for i := len(plaintext); i < n; i++ {
		body[i] = pad
	}
	cipher.NewCBCEncrypter(b, iv).CryptBlocks(body, body)
	return base64.RawURLEncoding.EncodeToString(buf)
}

// Decrypt reverses Encrypt (or imgproxy's reference encoder). Trailing "="
// padding is tolerated. Every failure is an errs.KindUnauthorized error that
// wraps ErrInvalidToken and shows clients the same generic message; the
// internal message only says whether the token was malformed or did not
// decrypt, never which check failed after decryption.
func (c *Cipher) Decrypt(token string) ([]byte, error) {
	return c.decrypt(token, "decrypt source url: ", publicMessage)
}

// DecryptPassword is Decrypt for a document password token (the same
// format; pdf_password). The caller owns the plaintext and should clear it
// once used. Failures are errs.KindUnauthorized errors wrapping
// ErrInvalidToken with one public message that never echoes the token.
func (c *Cipher) DecryptPassword(token string) ([]byte, error) {
	return c.decrypt(token, "decrypt document password: ", passwordPublicMessage)
}

// PasswordDigest returns the keyed hash of a document password: 64
// lowercase hex digits of HMAC-SHA256 under a key derived from the first
// AES key. It stands for the password in cache keys, which may reach
// clients (ETag). A nil Cipher returns "".
func (c *Cipher) PasswordDigest(password []byte) string {
	if c == nil || c.digestKey == nil {
		return ""
	}
	m := hmac.New(sha256.New, c.digestKey)
	m.Write(password)
	return hex.EncodeToString(m.Sum(nil))
}

func (c *Cipher) decrypt(token, prefix, public string) ([]byte, error) {
	if c == nil || len(c.blocks) == 0 {
		return nil, invalid(prefix, public, "no decryption keys")
	}
	if len(token) > MaxTokenLen {
		return nil, invalid(prefix, public, "token too long")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(token, "="))
	if err != nil {
		return nil, invalid(prefix, public, "token is not base64url")
	}
	if len(raw) < 2*aes.BlockSize || len(raw)%aes.BlockSize != 0 {
		return nil, invalid(prefix, public, "token length is not iv plus whole blocks")
	}
	iv, ct := raw[:aes.BlockSize], raw[aes.BlockSize:]
	out := make([]byte, len(ct))
	for _, b := range c.blocks {
		cipher.NewCBCDecrypter(b, iv).CryptBlocks(out, ct)
		n, ok := unpad(out)
		if ok && plausible(out[:n]) {
			return out[:n:n], nil
		}
	}
	clear(out)
	return nil, invalid(prefix, public, "token does not decrypt with any key")
}

// unpad validates PKCS#7 padding in constant time with respect to the padding
// bytes: it always inspects the whole last block. len(b) must be a positive
// multiple of aes.BlockSize. It returns the unpadded length.
func unpad(b []byte) (int, bool) {
	last := b[len(b)-aes.BlockSize:]
	padByte := last[aes.BlockSize-1]
	padLen := int(padByte)
	good := subtle.ConstantTimeLessOrEq(1, padLen) & subtle.ConstantTimeLessOrEq(padLen, aes.BlockSize)
	for i := range aes.BlockSize {
		// inPad is 1 for the last padLen bytes of the block. Outside the
		// padding the check is a no-op (select 1); inside, the byte must
		// equal padByte.
		inPad := subtle.ConstantTimeLessOrEq(aes.BlockSize-i, padLen)
		eq := subtle.ConstantTimeByteEq(last[i], padByte)
		good &= subtle.ConstantTimeSelect(inPad, eq, 1)
	}
	n := subtle.ConstantTimeSelect(good, len(b)-padLen, 0)
	return n, good == 1
}

// plausible reports whether p looks like a source URL: valid UTF-8 without
// ASCII control characters (C0 and DEL).
func plausible(p []byte) bool {
	for _, c := range p {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return utf8.Valid(p)
}

func invalid(prefix, public, msg string) error {
	return &errs.Error{
		Kind:   errs.KindUnauthorized,
		Msg:    prefix + msg,
		Public: public,
		Err:    ErrInvalidToken,
	}
}

// String reports only the number of keys.
func (c *Cipher) String() string {
	if c == nil {
		return "crypt.Cipher(nil)"
	}
	return "crypt.Cipher{keys:" + strconv.Itoa(len(c.blocks)) + "}"
}

// GoString keeps %#v from printing the expanded key schedules.
func (c *Cipher) GoString() string { return c.String() }

// LogValue keeps slog from printing the expanded key schedules.
func (c *Cipher) LogValue() slog.Value {
	if c == nil {
		return slog.GroupValue()
	}
	return slog.GroupValue(slog.Int("keys", len(c.blocks)))
}
