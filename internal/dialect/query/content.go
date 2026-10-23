package query

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"io"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// HTTP QUERY content (RFC 10008, WP-147). A QUERY request to the dialect's
// mount carries in its content the parameters a GET request carries in
// its query string. ContentQuery turns that content into the raw query of
// the equivalent GET request, which ParseURL then parses, signs and
// limits exactly like a GET: the two methods share one grammar, one
// signature message and one result cache key.
//
// application/x-www-form-urlencoded content is the query string itself
// and is kept as written (order and encoding), as xi_sig signs a GET
// query as written. application/json content is an object whose members
// are the parameters in document order; its canonical query writes each
// member as key=value, escaped like the official clients escape query
// keys and values (letters, digits and QuerySafe kept, every other UTF-8
// byte %XX in upper case), so a client that signs the form encoding of
// its parameters signs the JSON content too. Values: strings as they are,
// numbers as written in the document, true and false, arrays of those
// joined by ","; null skips the member. Objects, nested arrays, duplicate
// names and invalid UTF-8 are errors.

// Media types of QUERY content.
const (
	MediaTypeForm = "application/x-www-form-urlencoded"
	MediaTypeJSON = "application/json"
)

// AcceptQuery is the Accept-Query field value (RFC 10008 §3): a
// Structured Field list of the media types ContentQuery accepts.
const AcceptQuery = MediaTypeForm + ", " + MediaTypeJSON

// QuerySafe are the characters besides ASCII letters and digits the
// canonical query keeps unescaped (clients/spec/spec.json url.query_safe).
const QuerySafe = "-._~:/,@!$'()*"

// ErrUnsupportedMediaType is returned by ContentQuery for a media type it
// does not read (the server answers 415 with Accept-Query).
var ErrUnsupportedMediaType = errors.New("query: unsupported media type")

// ErrInvalidContent is wrapped by the 400 errors of ContentQuery.
var ErrInvalidContent = errors.New("query: invalid content")

// ContentQuery returns the raw query that content of the given media
// type (lower case, without parameters) stands for. Errors are
// ErrUnsupportedMediaType or a KindInvalidURL *errs.Error wrapping
// ErrInvalidContent.
func ContentQuery(mediaType string, content []byte) (string, error) {
	switch mediaType {
	case MediaTypeForm:
		return formQuery(content)
	case MediaTypeJSON:
		return jsonQuery(content)
	default:
		return "", ErrUnsupportedMediaType
	}
}

// invalidContent is the 400 of malformed content; reason is static and
// client-safe.
func invalidContent(reason string) error {
	return &errs.Error{
		Kind:   errs.KindInvalidURL,
		Msg:    reason,
		Public: "invalid query content: " + reason,
		Err:    ErrInvalidContent,
	}
}

// formQuery checks form content: only characters a URL query may hold, so the equivalent GET URL is well-formed; escapes are
// checked by ParseURL. A trailing CR LF or LF (curl --data-binary @file)
// is dropped.
func formQuery(content []byte) (string, error) {
	content = bytes.TrimSuffix(content, []byte("\n"))
	content = bytes.TrimSuffix(content, []byte("\r"))
	for _, c := range content {
		if !isQueryByte(c) {
			return "", invalidContent("form content holds a character a url query may not")
		}
	}
	return string(content), nil
}

// isQueryByte reports a byte that may appear unescaped in a URL query
// (RFC 3986 query = *( pchar / "/" / "?" ), plus "%" of escapes). "+"
// is a space in form encoding.
func isQueryByte(c byte) bool {
	return isAlnum(c) || strings.IndexByte("-._~!$&'()*+,;=:@/?%", c) >= 0
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// jsonQuery writes the canonical query of a JSON object.
func jsonQuery(content []byte) (string, error) {
	d := jsontext.NewDecoder(bytes.NewReader(content))
	tok, err := d.ReadToken()
	if err != nil {
		return "", invalidContent("content is not json")
	}
	if tok.Kind() != jsontext.KindBeginObject {
		return "", invalidContent("json content must be an object")
	}
	var b strings.Builder
	for d.PeekKind() != jsontext.KindEndObject {
		tok, err := d.ReadToken()
		if err != nil {
			return "", jsonError(err)
		}
		name := tok.String() // the token is only valid until the next read
		v, err := d.ReadValue()
		if err != nil {
			return "", jsonError(err)
		}
		val, skip, err := jsonParam(v)
		if err != nil {
			return "", err
		}
		if skip {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('&')
		}
		escapeTo(&b, name)
		b.WriteByte('=')
		escapeTo(&b, val)
	}
	if _, err := d.ReadToken(); err != nil { // '}'
		return "", jsonError(err)
	}
	if _, err := d.ReadToken(); !errors.Is(err, io.EOF) {
		return "", invalidContent("content after the json object")
	}
	return b.String(), nil
}

// jsonError maps a decoder error to a 400; a repeated name has its own
// reason.
func jsonError(err error) error {
	if errors.Is(err, jsontext.ErrDuplicateName) {
		return invalidContent("duplicate json member")
	}
	return invalidContent("content is not json")
}

// jsonParam returns the parameter value of one member's JSON value; skip
// is set for null.
func jsonParam(v jsontext.Value) (val string, skip bool, err error) {
	switch v.Kind() {
	case jsontext.KindNull:
		return "", true, nil
	case jsontext.KindBeginArray:
		d := jsontext.NewDecoder(bytes.NewReader(v))
		if _, err := d.ReadToken(); err != nil {
			return "", false, jsonError(err)
		}
		var parts []string
		for d.PeekKind() != jsontext.KindEndArray {
			e, err := d.ReadValue()
			if err != nil {
				return "", false, jsonError(err)
			}
			s, ok := jsonScalar(e)
			if !ok {
				return "", false, invalidContent("json array elements must be strings, numbers or booleans")
			}
			parts = append(parts, s)
		}
		return strings.Join(parts, ","), false, nil
	default:
	}
	s, ok := jsonScalar(v)
	if !ok {
		return "", false, invalidContent("json values must be strings, numbers, booleans, arrays or null")
	}
	return s, false, nil
}

// jsonScalar returns a string, number or boolean as a parameter value.
func jsonScalar(v jsontext.Value) (string, bool) {
	switch v.Kind() {
	case jsontext.KindString:
		s, err := jsontext.AppendUnquote(nil, v)
		return string(s), err == nil
	case jsontext.KindNumber, jsontext.KindTrue, jsontext.KindFalse:
		return string(v), true // numbers as written; true, false
	default:
		return "", false
	}
}

const upperHex = "0123456789ABCDEF"

// escapeTo writes s escaped for a query key or value: letters, digits
// and QuerySafe as they are, every other byte as %XX (upper case).
func escapeTo(b *strings.Builder, s string) {
	for i := range len(s) {
		c := s[i]
		if isAlnum(c) || strings.IndexByte(QuerySafe, c) >= 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(upperHex[c>>4])
		b.WriteByte(upperHex[c&15])
	}
}

// EscapeQuery escapes s like the canonical query of JSON content does
// (and the official clients' query encoding).
func EscapeQuery(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	escapeTo(&b, s)
	return b.String()
}
