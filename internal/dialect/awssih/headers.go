package awssih

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// deniedHeader reports whether SIH's HEADER_DENY_LIST (lib/constants.ts)
// drops the response header name. The comparison is case-insensitive.
func deniedHeader(name string) bool {
	n := strings.ToLower(name)
	switch n {
	case "authorization", "connection", "server", "transfer-encoding", "referrer-policy",
		"permissions-policy", "www-authenticate", "proxy-authenticate", "x-api-key", "set-cookie":
		return true
	}
	for _, p := range []string{
		"x-frame-", "x-content-", "x-xss-", "strict-transport-", "permissions-",
		"x-amz-", "x-amzn-", "access-control-", "cross-origin-", "content-",
	} {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

// cacheTagHeaders carry cache tags the server can emit (Delivery.CacheTags).
var cacheTagHeaders = map[string]bool{"cache-tag": true, "surrogate-key": true, "edge-cache-tag": true}

// applyHeaders maps the request's "headers" object onto Delivery. SIH sends
// the (non-denied) headers verbatim; the model can express only
// Cache-Control max-age and immutable, and cache tags. Everything else is
// recorded in Ignored and never fails the request, strict or not.
func (b *builder) applyHeaders(raw json.RawMessage) error {
	if kind(raw) == kindNull {
		return nil
	}
	fields, err := objectFields(raw)
	if err != nil {
		return invalidJSON("headers", "an object")
	}
	for _, f := range fields {
		name := "headers." + truncate(f.key)
		lower := strings.ToLower(f.key)
		switch {
		case deniedHeader(f.key):
			b.ignore(name, reasonHeaderDenied)
		case lower == "cache-control":
			v, ok := asString(f.raw)
			if !ok || !b.cacheControl(v) {
				b.ignore(name, reasonPartial)
			}
		case cacheTagHeaders[lower]:
			v, ok := asString(f.raw)
			if !ok {
				b.ignore(name, reasonHeader)
				continue
			}
			for tag := range strings.FieldsFuncSeq(v, func(r rune) bool { return r == ',' || r == ' ' }) {
				b.r.Delivery.CacheTags = append(b.r.Delivery.CacheTags, tag)
			}
		default:
			b.ignore(name, reasonHeader)
		}
	}
	return nil
}

// cacheControl applies max-age and immutable; it reports whether every
// directive was understood (public is the server's default).
func (b *builder) cacheControl(v string) bool {
	all := true
	for d := range strings.SplitSeq(v, ",") {
		d = strings.ToLower(strings.TrimSpace(d))
		switch {
		case d == "" || d == "public":
		case d == "immutable":
			b.r.Delivery.Immutable = true
		case strings.HasPrefix(d, "max-age="):
			n, err := strconv.ParseInt(strings.Trim(d[len("max-age="):], `"`), 10, 64)
			if err != nil || n < 0 || n > int64(maxTTL/time.Second) {
				all = false
				continue
			}
			b.r.Delivery.TTL = time.Duration(n) * time.Second
		default:
			all = false
		}
	}
	return all
}

// maxTTL bounds a max-age taken from the request (the server clamps it
// further by configuration).
const maxTTL = 10 * 365 * 24 * time.Hour

func truncate(s string) string {
	if len(s) > maxQuoted {
		return s[:maxQuoted]
	}
	return s
}
