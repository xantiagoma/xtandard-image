// Copyright 2017 Sergey Alexandrovich (imgproxy), Apache License 2.0.
// Reimplemented from imgproxy options/parser/url.go. Modifications copyright
// xtandard-image contributors, MIT. See NOTICE.

package imgproxy

import (
	"encoding/base64"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

const (
	tokenPlain = "plain"
	tokenEnc   = "enc"
)

// decodeSource decodes the source part of the path (everything after the
// options) into st.r.Source and returns the extension, if any.
func (st *state) decodeSource(rest string) (string, error) {
	if rest == "" {
		return "", dialect.InvalidURL("missing source url")
	}
	first, tail, hasTail := strings.Cut(rest, "/")
	var (
		raw, ext string
		enc      transform.SourceEncoding
		err      error
	)
	switch {
	case first == tokenPlain && hasTail:
		enc = transform.SourcePlain
		raw, ext, err = decodePlain(tail)
	case first == tokenEnc && hasTail:
		enc = transform.SourceEncrypted
		var token string
		token, ext, err = st.joinEncoded(tail)
		if err == nil {
			raw, err = st.decrypt(token)
		}
	default:
		enc = transform.SourceBase64
		var token string
		token, ext, err = st.joinEncoded(rest)
		if err == nil {
			raw, err = decodeBase64(token)
		}
	}
	if err != nil {
		return "", err
	}
	raw = st.preprocessURL(raw)
	if !plausibleSource(raw) {
		return "", dialect.InvalidURL("invalid source url")
	}
	// Keep a hashsum option parsed before the source.
	st.r.Source.Raw, st.r.Source.Encoding = raw, enc
	return ext, nil
}

// decodePlain handles plain/%source_url[@%extension]; the source is
// percent-decoded.
func decodePlain(s string) (raw, ext string, err error) {
	u, ext, hasExt := strings.Cut(s, "@")
	if u == "" {
		return "", "", dialect.InvalidURL("missing source url")
	}
	if hasExt && strings.Contains(ext, "@") {
		return "", "", dialect.InvalidURL("multiple extensions")
	}
	raw, err = url.PathUnescape(u)
	if err != nil {
		return "", "", dialect.InvalidURL("invalid source url escaping")
	}
	return raw, ext, nil
}

// joinEncoded joins a base64 or encrypted source that may be split with
// "/" and splits off ".%extension".
func (st *state) joinEncoded(s string) (token, ext string, err error) {
	if st.p.o.Base64URLIncludesFilename {
		if i := strings.LastIndexByte(s, '/'); i >= 0 {
			s = s[:i]
		}
	}
	if strings.IndexByte(s, '/') >= 0 {
		s = strings.ReplaceAll(s, "/", "")
	}
	token, ext, hasExt := strings.Cut(s, ".")
	if token == "" {
		return "", "", dialect.InvalidURL("missing source url")
	}
	if hasExt && strings.Contains(ext, ".") {
		return "", "", dialect.InvalidURL("multiple extensions")
	}
	return token, ext, nil
}

func decodeBase64(token string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(token, "="))
	if err != nil {
		return "", dialect.InvalidURL("invalid base64 source url")
	}
	return string(b), nil
}

func (st *state) decrypt(token string) (string, error) {
	b, err := st.p.o.Cipher.Decrypt(token)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// preprocessURL applies URL replacements, then prefixes relative sources
// with the base URL. imgproxy prefixes every source that does not already
// start with the base URL; docs/URL-DIALECTS.md §3.1 limits this to
// relative sources so absolute URLs are never corrupted.
func (st *state) preprocessURL(u string) string {
	for _, r := range st.p.o.URLReplacements {
		u = r.Pattern.ReplaceAllString(u, r.Replacement)
	}
	if b := st.p.o.BaseURL; b != "" && !hasScheme(u) && !strings.HasPrefix(u, b) {
		u = b + u
	}
	return u
}

// hasScheme reports whether u starts with an RFC 3986 scheme and ":".
func hasScheme(u string) bool {
	for i := range len(u) {
		c := u[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'):
		case i > 0 && c == ':':
			return true
		default:
			return false
		}
	}
	return false
}

// plausibleSource reports whether s is a non-empty UTF-8 string without
// ASCII control characters.
func plausibleSource(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if c := s[i]; c < 0x20 || c == 0x7f {
			return false
		}
	}
	return utf8.ValidString(s)
}
