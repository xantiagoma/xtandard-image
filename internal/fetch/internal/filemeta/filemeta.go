// Package filemeta holds the validator logic shared by the file-protocol
// fetch backends (ftpsrc, sftpsrc), whose servers expose only a size and a
// modification time: a weak ETag derived from both, and the RFC 9110 §13.2.2
// evaluation of If-None-Match / If-Modified-Since against them.
package filemeta

import (
	"path"
	"strconv"
	"strings"
	"time"
)

// ETag returns the weak validator W/"<size hex>-<unix seconds hex>" for a
// file, or "" when neither is known (size < 0 and a zero mtime).
func ETag(size int64, mtime time.Time) string {
	if size < 0 && mtime.IsZero() {
		return ""
	}
	var b strings.Builder
	b.WriteString(`W/"`)
	if size >= 0 {
		b.WriteString(strconv.FormatInt(size, 16))
	}
	b.WriteByte('-')
	if !mtime.IsZero() {
		b.WriteString(strconv.FormatInt(mtime.Unix(), 16))
	}
	b.WriteByte('"')
	return b.String()
}

// NotModified reports whether a conditional fetch may answer "not
// modified". If-None-Match, when set, is evaluated alone with the weak
// comparison; otherwise If-Modified-Since applies when mtime is known.
func NotModified(ifNoneMatch string, ifModifiedSince time.Time, etag string, mtime time.Time) bool {
	if strings.TrimSpace(ifNoneMatch) != "" {
		if etag == "" {
			return false
		}
		want := opaque(etag)
		for tag := range strings.SplitSeq(ifNoneMatch, ",") {
			tag = strings.TrimSpace(tag)
			if tag == "*" || opaque(tag) == want {
				return true
			}
		}
		return false
	}
	if ifModifiedSince.IsZero() || mtime.IsZero() {
		return false
	}
	return !mtime.Truncate(time.Second).After(ifModifiedSince)
}

// opaque strips the weakness indicator for the weak comparison.
func opaque(tag string) string {
	return strings.TrimPrefix(tag, "W/")
}

// Join places a Key.Path (relative, "/"-separated, already free of ".."
// segments) under root, returning an absolute server path. An empty root
// means "/".
func Join(root, key string) string {
	return path.Join("/", root, key)
}
