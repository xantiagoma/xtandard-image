// Package source defines the storage abstraction every fetch backend
// implements (docs/ARCHITECTURE.md §6) and the resolver that maps a dialect's
// source string onto a configured, allowlisted source (§10: the allowlist IS
// the set of configured sources).
//
// The package is pure Go and performs no I/O: backends live under
// internal/fetch/<type>src and are constructed by internal/app, which binds
// them to their Spec in a Registry.
package source

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

// Source types, as written in XI_SOURCES_<NAME>_TYPE (docs/CONFIG.md).
const (
	TypeHTTP  = "http"
	TypeS3    = "s3"
	TypeGCS   = "gcs"
	TypeAzure = "azure"
	TypeFS    = "fs"
	TypeFTP   = "ftp"
	TypeSFTP  = "sftp"
)

// DevSourceName is the name of the implicit http source used in dev mode
// (XI_DEV_MODE=true) for absolute http(s) URLs no configured source allows.
// Configured names match [a-z0-9-]+, so it can never collide with one.
const DevSourceName = "_dev"

// PlaygroundSourceName is the name of the playground's in-memory source
// (playground://<area>/<key>, WP-94): uploads and the built-in samples.
// Like DevSourceName it can never collide with a configured name.
const PlaygroundSourceName = "_playground"

// Source is one configured origin (a named source). Implementations live in
// internal/fetch/<type>src.
type Source interface {
	Name() string
	Type() string // TypeHTTP, TypeS3, TypeGCS, TypeAzure, TypeFS, TypeFTP, TypeSFTP
	// Fetch returns a streaming body; the fetcher enforces limits, the source
	// only honours ctx. Errors carry an errs.Kind (NotFound, UpstreamError,
	// UpstreamTimeout, Forbidden, ...).
	Fetch(ctx context.Context, key Key, opt FetchOptions) (*Object, error)
}

// Key addresses one object within a named source. The Resolver produces it;
// it is already allowlisted, jailed and normalised, so backends use it as is.
type Key struct {
	// Source is the registered source name, or DevSourceName.
	Source string
	// Bucket is the s3/gcs bucket or azure container; empty for other types.
	Bucket string
	// Path is, for http sources, the canonical absolute URL (lower-case
	// scheme and host, default port dropped, canonical percent-encoding, no
	// userinfo or fragment). For every other type it is the decoded object
	// key or file path relative to the source root (bucket, fs root, ftp/sftp
	// "/"), without a leading slash, including any configured prefix jail and
	// never containing a ".." segment.
	Path string
	// Version is an s3 versionId or gcs generation; empty means latest.
	Version string
	// Dev reports that no configured source matched and dev mode supplied the
	// implicit http source: the only protection left is the SSRF policy,
	// which httpsrc applies to every fetch (and every redirect hop) anyway.
	Dev bool
}

// FetchOptions are per-fetch parameters set by the fetcher.
type FetchOptions struct {
	MaxBytes        int64
	IfNoneMatch     string
	IfModifiedSince time.Time
	Header          http.Header // passthrough headers (allowlisted)
	// CheckRedirect vets each redirect hop (an absolute URL) of an http
	// fetch; the fetcher binds it to Registry.ResolveRedirect for the key
	// being fetched. Nil means redirects are refused.
	CheckRedirect func(location string) error
}

// Object is a fetched original. The caller must close Body (nil when
// NotModified).
type Object struct {
	Body         io.ReadCloser
	Size         int64 // -1 unknown
	ContentType  string
	ETag         string
	LastModified time.Time
	CacheControl string
	Expires      time.Time
	NotModified  bool
}

// maxRedactedLen caps RedactKey output so a hostile URL cannot flood logs.
const maxRedactedLen = 512

// RedactKey renders k for logs: "<source>:<locator>", where http query
// strings (which often carry origin tokens) are replaced by "?REDACTED" and
// the result is capped at 512 bytes.
func RedactKey(k Key) string {
	var b strings.Builder
	b.WriteString(k.Source)
	b.WriteByte(':')
	if k.Bucket != "" {
		b.WriteString(k.Bucket)
		b.WriteByte('/')
	}
	p := k.Path
	if i := strings.IndexByte(p, '?'); i >= 0 && isHTTPURL(p) {
		p = p[:i] + "?REDACTED"
	}
	b.WriteString(p)
	if k.Version != "" {
		b.WriteString("#v=")
		b.WriteString(k.Version)
	}
	s := b.String()
	if len(s) > maxRedactedLen {
		s = s[:maxRedactedLen] + "..."
	}
	return s
}

func isHTTPURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}
