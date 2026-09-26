package sign

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
)

const redacted = "[REDACTED]"

// Key is one secret with an optional salt. Its fields are unexported so key
// material cannot be printed by accident: fmt and log/slog render a Key as
// "[REDACTED]".
type Key struct {
	secret []byte
	salt   []byte
}

// NewKey returns a key holding copies of secret and salt; the caller may
// zero its own slices afterwards.
func NewKey(secret, salt []byte) Key {
	return Key{secret: clone(secret), salt: clone(salt)}
}

func clone(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return append(make([]byte, 0, len(b)), b...)
}

// String implements fmt.Stringer without revealing key material.
func (Key) String() string { return redacted }

// GoString implements fmt.GoStringer without revealing key material.
func (Key) GoString() string { return redacted }

// LogValue implements slog.LogValuer without revealing key material.
func (Key) LogValue() slog.Value { return slog.StringValue(redacted) }

func zeroKey(k *Key) {
	clear(k.secret)
	clear(k.salt)
}

// Keyring is an ordered list of keys. Verification accepts a signature made
// with any of them (rotation); signing uses the first. A Keyring is
// immutable after construction and safe for concurrent use until Destroy.
type Keyring struct {
	keys      []Key
	destroyed atomic.Bool
}

// NewKeyring returns a keyring over keys, copying their material.
func NewKeyring(keys ...Key) *Keyring {
	r := &Keyring{keys: make([]Key, len(keys))}
	for i := range keys {
		r.keys[i] = NewKey(keys[i].secret, keys[i].salt)
	}
	return r
}

// ParseHexKeyring decodes hex key/salt pairs (XI_SIGNING_KEYS,
// XI_SIGNING_SALTS; imgproxy IMGPROXY_KEY, IMGPROXY_SALT). Both lists must
// have the same length; keys must be non-empty, salts may be empty.
// Surrounding whitespace is ignored. Both lists empty yields an empty
// keyring (signing off).
//
// Errors name the offending index only, never the value.
func ParseHexKeyring(keys, salts []string) (*Keyring, error) {
	if len(keys) != len(salts) {
		return nil, fmt.Errorf("sign: %d keys but %d salts; counts must match", len(keys), len(salts))
	}
	r := &Keyring{keys: make([]Key, 0, len(keys))}
	var errList []error
	for i := range keys {
		secret, err := decodeHex(keys[i])
		if err == nil && len(secret) == 0 {
			err = errors.New("empty")
		}
		if err != nil {
			errList = append(errList, fmt.Errorf("sign: key %d: %w", i, err))
		}
		salt, serr := decodeHex(salts[i])
		if serr != nil {
			errList = append(errList, fmt.Errorf("sign: salt %d: %w", i, serr))
		}
		r.keys = append(r.keys, Key{secret: secret, salt: salt})
	}
	if len(errList) > 0 {
		r.Destroy()
		return nil, errors.Join(errList...)
	}
	return r, nil
}

// errInvalidHex deliberately carries no detail: encoding/hex errors quote
// the offending character, which is key material.
var errInvalidHex = errors.New("not valid hex")

func decodeHex(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if len(s)%2 != 0 {
		return nil, errInvalidHex
	}
	if s == "" {
		return nil, nil
	}
	src := []byte(s)
	defer clear(src)
	b := make([]byte, len(s)/2)
	if _, err := hex.Decode(b, src); err != nil {
		clear(b)
		return nil, errInvalidHex
	}
	return b, nil
}

// Len returns the number of keys. Zero means signing is off.
func (r *Keyring) Len() int {
	if r == nil {
		return 0
	}
	return len(r.keys)
}

// Destroy zeroes all key material. Verifiers and signers using the keyring
// fail with ErrKeysDestroyed afterwards (never fall back to unsigned). Call
// it on shutdown or after a failed configuration load.
func (r *Keyring) Destroy() {
	if r == nil {
		return
	}
	r.destroyed.Store(true)
	for i := range r.keys {
		zeroKey(&r.keys[i])
	}
}

func (r *Keyring) isDestroyed() bool { return r != nil && r.destroyed.Load() }

// String implements fmt.Stringer without revealing key material.
func (r *Keyring) String() string {
	return fmt.Sprintf("sign.Keyring{%d keys %s}", r.Len(), redacted)
}

// GoString implements fmt.GoStringer without revealing key material.
func (r *Keyring) GoString() string { return r.String() }

// LogValue implements slog.LogValuer without revealing key material.
func (r *Keyring) LogValue() slog.Value {
	return slog.GroupValue(slog.Int("keys", r.Len()))
}
