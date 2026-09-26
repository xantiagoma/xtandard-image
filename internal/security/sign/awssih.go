package sign

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"slices"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// AWSSIHParam is the query parameter that carries an AWS Serverless Image
// Handler ("Dynamic Image Transformation for Amazon CloudFront") signature.
const AWSSIHParam = "signature"

// awssihScheme is the Serverless Image Handler URL signature
// (source/image-handler/image-request.ts validateRequestSignature,
// Apache-2.0):
//
//	hex(HMAC-SHA256(secret, path [‖ "?" ‖ query]))
//
// The secret is used verbatim (SIH reads it from Secrets Manager as a
// string), so a key's salt is ignored. SIH compares the presented value with
// the lowercase hex digest as strings, so Decode accepts lowercase hex only.
type awssihScheme struct{}

// NewAWSSIHScheme returns the Serverless Image Handler signing scheme. The
// message is built by AWSSIHMessage.
func NewAWSSIHScheme() Scheme { return awssihScheme{} }

func (awssihScheme) Name() string { return "awssih" }

func (awssihScheme) Size() int { return sha256.Size }

func (awssihScheme) Sum(dst []byte, k *Key, msg string) []byte {
	mac := hmac.New(sha256.New, k.secret)
	_, _ = io.WriteString(mac, msg)
	var sum [sha256.Size]byte
	return append(dst, mac.Sum(sum[:0])...)
}

func (awssihScheme) Encode(raw []byte) string { return hex.EncodeToString(raw) }

var errUppercaseHex = errors.New("sign: awssih signature must be lowercase hex")

func (awssihScheme) Decode(dst []byte, sig string) ([]byte, error) {
	for i := range len(sig) {
		if c := sig[i]; 'A' <= c && c <= 'F' {
			return dst, errUppercaseHex
		}
	}
	return hex.AppendDecode(dst, []byte(sig))
}

// AWSSIHMessage splits a request into the presented signature and the
// message SIH signs. path is the decoded request path starting with "/"
// (API Gateway's event.path; for a mount, the path after the mount
// prefix). The query is rebuilt the way SIH's recreateQueryString does from
// API Gateway's queryStringParameters:
//
//   - parameters are form-decoded (url.QueryUnescape); for a repeated key
//     the last value wins, as in queryStringParameters;
//   - signature is removed;
//   - the remaining [key, value] pairs are sorted with JavaScript's default
//     Array.prototype.sort, i.e. by the string "key,value" in UTF-16 code
//     unit order;
//   - each pair is joined as key=value (not re-encoded) and pairs with "&".
//
// The message is path, followed by "?" and that query when it is
// non-empty. sig is "" when there is no signature parameter. A malformed
// escape is an errs.KindInvalidURL error.
func AWSSIHMessage(path, rawQuery string) (sig, msg string, err error) {
	type pair struct{ k, v string }
	var pairs []pair
	index := map[string]int{}
	for part := range strings.SplitSeq(rawQuery, "&") {
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		key, kerr := url.QueryUnescape(k)
		val, verr := url.QueryUnescape(v)
		if kerr != nil || verr != nil {
			return "", "", errs.Newf(errs.KindInvalidURL, "sign: awssih: malformed query escape")
		}
		if i, ok := index[key]; ok {
			pairs[i].v = val
			continue
		}
		index[key] = len(pairs)
		pairs = append(pairs, pair{key, val})
	}
	type entry struct{ sortKey, k, v string }
	entries := make([]entry, 0, len(pairs))
	for _, p := range pairs {
		if p.k == AWSSIHParam {
			sig = p.v
			continue
		}
		entries = append(entries, entry{p.k + "," + p.v, p.k, p.v})
	}
	slices.SortStableFunc(entries, func(a, b entry) int { return compareUTF16(a.sortKey, b.sortKey) })
	var b strings.Builder
	b.Grow(len(path) + 1 + len(rawQuery))
	b.WriteString(path)
	for i, e := range entries {
		if i == 0 {
			b.WriteByte('?')
		} else {
			b.WriteByte('&')
		}
		b.WriteString(e.k)
		b.WriteByte('=')
		b.WriteString(e.v)
	}
	return sig, b.String(), nil
}

// compareUTF16 orders strings by UTF-16 code units, as JavaScript's default
// sort comparator does. It differs from Go's byte order only for code
// points above U+FFFF against U+E000..U+FFFF.
func compareUTF16(a, b string) int {
	for a != "" && b != "" {
		ra, na := utf8.DecodeRuneInString(a)
		rb, nb := utf8.DecodeRuneInString(b)
		if ra != rb {
			var bufA, bufB [2]uint16
			return slices.Compare(utf16.AppendRune(bufA[:0], ra), utf16.AppendRune(bufB[:0], rb))
		}
		a, b = a[na:], b[nb:]
	}
	return len(a) - len(b)
}

// AWSSIHOptions configures NewAWSSIHVerifier.
type AWSSIHOptions struct {
	Keys     *Keyring // secrets used verbatim; salts are ignored
	Trusted  []string
	Required bool // SIH ENABLE_SIGNATURE=Yes
}

// NewAWSSIHVerifier returns a verifier for SIH's signature parameter. Call
// Verify(sig, msg) with the results of AWSSIHMessage. An absent signature
// is Unsigned unless Required.
func NewAWSSIHVerifier(o AWSSIHOptions) (*KeyedVerifier, error) {
	return NewVerifier(NewAWSSIHScheme(), Options{Keys: o.Keys, Trusted: o.Trusted, Required: o.Required})
}

// SignAWSSIHQuery returns rawQuery with any signature removed and a fresh
// signature for path appended. s must use the awssih scheme.
func (s *Signer) SignAWSSIHQuery(path, rawQuery string) (string, error) {
	_, msg, err := AWSSIHMessage(path, rawQuery)
	if err != nil {
		return "", err
	}
	sig, err := s.Sign(msg)
	if err != nil {
		return "", err
	}
	var kept []string
	for part := range strings.SplitSeq(rawQuery, "&") {
		k, _, _ := strings.Cut(part, "=")
		if key, _ := url.QueryUnescape(k); part != "" && key != AWSSIHParam {
			kept = append(kept, part)
		}
	}
	return strings.Join(append(kept, AWSSIHParam+"="+sig), "&"), nil
}
