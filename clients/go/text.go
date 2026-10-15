package xtandard

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// hasControl reports whether s contains an ASCII control character (C0 or DEL).
func hasControl(s string) bool {
	for i := range len(s) {
		if c := s[i]; c < 0x20 || c == 0x7f {
			return true
		}
	}
	return false
}

// checkText rejects an empty string or one with control characters.
func checkText(s string, code ErrorCode, what string) error {
	if s == "" || hasControl(s) {
		return Errorf(code, "%s must be a non-empty string without control characters", what)
	}
	return nil
}

// b64url is unpadded base64url (RFC 4648 §5).
func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// hexKey decodes hex key material (either case, surrounding whitespace ignored).
func hexKey(s, what string) ([]byte, error) {
	b, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, Errorf(ErrInvalidKey, "%s is not a hex string", what)
	}
	return b, nil
}

func isAlnum(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}

// escape percent-encodes every byte of s except ASCII letters, digits and safe.
func escape(s, safe string) string {
	n := 0
	for i := range len(s) {
		if c := s[i]; !isAlnum(c) && strings.IndexByte(safe, c) < 0 {
			n++
		}
	}
	if n == 0 {
		return s
	}
	const upperHex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s) + 2*n)
	for i := range len(s) {
		c := s[i]
		if isAlnum(c) || strings.IndexByte(safe, c) >= 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(upperHex[c>>4])
		b.WriteByte(upperHex[c&15])
	}
	return b.String()
}

// EscapePlainSource percent-escapes a source for a plain/ imgproxy URL; the
// server path-unescapes it. ASCII letters, digits and [PlainSafe] (the RFC 3986
// unreserved characters, the sub-delims, ":" and "/") are kept; every other
// byte of the UTF-8 string ("%", "@", "?", "#", spaces, non-ASCII) is written
// as %XX in upper case.
func EscapePlainSource(source string) string { return escape(source, PlainSafe) }

// EscapeQueryValue percent-escapes a query key or value, keeping ASCII
// letters, digits and [QuerySafe] (":", "/" and "," stay readable).
func EscapeQueryValue(value string) string { return escape(value, QuerySafe) }

// CheckExtension returns an [ErrInvalidExtension] error unless ext matches
// [ExtensionPattern] (ASCII letters and digits).
func CheckExtension(ext string) error {
	ok := ext != ""
	for i := range len(ext) {
		ok = ok && isAlnum(ext[i])
	}
	if !ok {
		return Errorf(ErrInvalidExtension, "invalid extension %q", ext)
	}
	return nil
}

// CheckSource returns an [ErrInvalidSource] error for an empty source or one
// with control characters.
func CheckSource(source string) error { return checkText(source, ErrInvalidSource, "source") }
