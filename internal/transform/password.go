package transform

import (
	"fmt"
	"log/slog"
)

// redactedPassword is what every formatting of a set Password prints.
const redactedPassword = "[REDACTED]"

// MaxPasswordLen bounds a document password in bytes (URL option and
// per-source default alike).
const MaxPasswordLen = 1024

// Password is a decrypted document password (WP-115: the imgproxy
// pdf_password option). It never formats, logs or marshals as its bytes:
// String, GoString, LogValue, MarshalJSON and MarshalText print
// "[REDACTED]" (or "" when empty). It is not part of CanonicalKey;
// DocumentPassword.Digest, a keyed hash, stands for it there. The request
// that carries it owns the bytes: the server clears them once the request
// is served.
type Password []byte

// String implements fmt.Stringer without revealing the password.
func (p Password) String() string {
	if len(p) == 0 {
		return ""
	}
	return redactedPassword
}

// GoString keeps %#v from printing the bytes.
func (p Password) GoString() string { return "transform.Password(" + p.String() + ")" }

// Format keeps every fmt verb (%x, %q, %d, ...) from printing the bytes.
func (p Password) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(p.String())) }

// LogValue keeps log/slog from printing the bytes.
func (p Password) LogValue() slog.Value { return slog.StringValue(p.String()) }

// MarshalJSON keeps encoding/json from printing the bytes.
func (p Password) MarshalJSON() ([]byte, error) { return []byte(`"` + p.String() + `"`), nil }

// MarshalText keeps text encoders from printing the bytes.
func (p Password) MarshalText() ([]byte, error) { return []byte(p.String()), nil }

// Clear overwrites the bytes with zeros.
func (p Password) Clear() { clear(p) }
