package source

import (
	"errors"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// Sentinels callers may branch on with errors.Is. Every error returned by
// ParseRef and the Registry also carries an errs.Kind.
var (
	// ErrInvalidSource: the source string cannot be parsed (KindInvalidURL).
	ErrInvalidSource = errors.New("invalid source")
	// ErrTraversal: a path contains a ".." segment (KindForbidden).
	ErrTraversal = errors.New("path traversal")
	// ErrNotAllowed: no configured source allows the reference (KindForbidden).
	ErrNotAllowed = errors.New("source not allowed")
)

// MaxRefLen bounds the source strings ParseRef accepts.
const MaxRefLen = 8 << 10

// Scheme is the syntactic form of a source string.
type Scheme uint8

// Schemes, in resolution-order groups (ARCHITECTURE §6).
const (
	SchemeRelative Scheme = iota // key relative to the dialect mount's bound source
	SchemeXI                     // xi://<source>/<key>
	SchemeHTTP                   // http://
	SchemeHTTPS                  // https://
	SchemeS3                     // s3://bucket/key[?versionId=]
	SchemeGS                     // gs://bucket/key[?generation=]
	SchemeAzure                  // abs://container/blob, alias az://
	SchemeLocal                  // local:///path, aliases local:/path, local:path, file:///path
	SchemeFTP                    // ftp://host[:port]/path
	SchemeSFTP                   // sftp://host[:port]/path
)

// String returns the canonical scheme name ("" for relative keys).
func (s Scheme) String() string {
	switch s {
	case SchemeRelative:
		return ""
	case SchemeXI:
		return "xi"
	case SchemeHTTP:
		return "http"
	case SchemeHTTPS:
		return "https"
	case SchemeS3:
		return "s3"
	case SchemeGS:
		return "gs"
	case SchemeAzure:
		return "abs"
	case SchemeLocal:
		return "local"
	case SchemeFTP:
		return "ftp"
	case SchemeSFTP:
		return "sftp"
	default:
		return "Scheme(" + strconv.Itoa(int(s)) + ")"
	}
}

// sourceType is the source type a scheme resolves to ("" for relative/xi).
func (s Scheme) sourceType() string {
	switch s {
	case SchemeHTTP, SchemeHTTPS:
		return TypeHTTP
	case SchemeS3:
		return TypeS3
	case SchemeGS:
		return TypeGCS
	case SchemeAzure:
		return TypeAzure
	case SchemeLocal:
		return TypeFS
	case SchemeFTP:
		return TypeFTP
	case SchemeSFTP:
		return TypeSFTP
	default:
		return ""
	}
}

// Ref is a parsed, normalised source string. It is syntax only: whether it
// is allowed is decided by Registry.Resolve.
type Ref struct {
	Scheme Scheme
	// Host is the lower-case host name (IPv6 without brackets) for
	// http/https/ftp/sftp, the bucket or container for s3/gs/abs, and the
	// lower-case source name for xi. Empty for local and relative refs.
	Host string
	// Port is an explicit, non-default port (http/https/ftp/sftp only).
	Port string
	// Path is the percent-decoded path. For http(s) it starts with "/"; for
	// every other scheme it is the key without its leading slash. It never
	// contains a ".." segment (split on "/" and "\").
	Path string
	// RawPath is Path in canonical percent-encoding (RFC 3986 pchar set,
	// upper-case hex, "/" inside a segment stays %2F), with the same slash
	// convention as Path.
	RawPath string
	// Query is the raw query for http(s), xi and relative refs.
	Query string
	// Version is the s3 versionId or gs generation query parameter.
	Version string
}

// String returns the canonical source string.
func (r *Ref) String() string {
	var b strings.Builder
	switch r.Scheme {
	case SchemeRelative:
		b.WriteString(r.RawPath)
	case SchemeLocal:
		b.WriteString("local:///")
		b.WriteString(r.RawPath)
	case SchemeHTTP, SchemeHTTPS:
		b.WriteString(r.Scheme.String())
		b.WriteString("://")
		b.WriteString(r.hostPort())
		b.WriteString(r.RawPath)
	default:
		b.WriteString(r.Scheme.String())
		b.WriteString("://")
		b.WriteString(r.hostPort())
		b.WriteByte('/')
		b.WriteString(r.RawPath)
	}
	switch {
	case r.Version != "" && r.Scheme == SchemeS3:
		b.WriteString("?versionId=" + url.QueryEscape(r.Version))
	case r.Version != "" && r.Scheme == SchemeGS:
		b.WriteString("?generation=" + url.QueryEscape(r.Version))
	case r.Query != "":
		b.WriteByte('?')
		b.WriteString(r.Query)
	}
	return b.String()
}

func (r *Ref) hostPort() string {
	h := r.Host
	if strings.IndexByte(h, ':') >= 0 {
		h = "[" + h + "]"
	}
	if r.Port != "" {
		h += ":" + r.Port
	}
	return h
}

func invalid(msg string) error {
	return errs.Wrapf(errs.KindInvalidURL, ErrInvalidSource, "%s", msg)
}

// ParseRef parses a dialect's source string (transform.SourceSpec.Raw).
// Accepted forms (scheme names are case-insensitive):
//
//	https://host[:port]/path[?query]  http://…    absolute origin URL
//	s3://bucket/key[?versionId=v]                 gs://bucket/key[?generation=g]
//	abs://container/blob   az://container/blob    local:///path  local:/path  local:path  file:///path
//	ftp://host[:port]/path  sftp://host[:port]/path
//	xi://<source>/<key>                           explicit named source
//	<key>                                         relative to the mount's source
//
// "scheme:/x" (a slash collapsed by a proxy) is repaired to "scheme://x" as
// imgproxy does. Userinfo, fragments-only keys, control characters, bad
// percent-encoding and unknown "scheme://" forms are KindInvalidURL; a ".."
// path segment (after percent-decoding, "/" or "\" separated) is
// KindForbidden with ErrTraversal.
func ParseRef(raw string) (Ref, error) {
	if raw == "" {
		return Ref{}, invalid("empty source")
	}
	if len(raw) > MaxRefLen {
		return Ref{}, invalid("source too long")
	}
	if hasCtl(raw) {
		return Ref{}, invalid("control character in source")
	}
	name, rest, ok := splitScheme(raw)
	if !ok {
		return parseRelative(raw)
	}
	switch name {
	case "http":
		return parseNetwork(SchemeHTTP, rest)
	case "https":
		return parseNetwork(SchemeHTTPS, rest)
	case "ftp":
		return parseNetwork(SchemeFTP, rest)
	case "sftp":
		return parseNetwork(SchemeSFTP, rest)
	case "s3":
		return parseBucket(SchemeS3, rest)
	case "gs":
		return parseBucket(SchemeGS, rest)
	case "abs", "az":
		return parseBucket(SchemeAzure, rest)
	case "xi":
		return parseBucket(SchemeXI, rest)
	case "local", "file":
		return parseLocal(rest)
	default:
		return Ref{}, invalid("unsupported source scheme")
	}
}

// knownSchemes lists the scheme names ParseRef understands.
func knownScheme(s string) bool {
	switch s {
	case "http", "https", "ftp", "sftp", "s3", "gs", "abs", "az", "xi", "local", "file":
		return true
	default:
		return false
	}
}

// splitScheme returns the lower-cased scheme and the text after ':'. A
// known scheme always counts; an unknown syntactically valid scheme counts
// only when followed by "//" (so it is rejected), otherwise the whole
// string is a relative key that happens to contain ':'.
func splitScheme(raw string) (name, rest string, ok bool) {
	i := strings.IndexByte(raw, ':')
	if i <= 0 || !isSchemeToken(raw[:i]) {
		return "", "", false
	}
	name, rest = strings.ToLower(raw[:i]), raw[i+1:]
	if knownScheme(name) || strings.HasPrefix(rest, "//") {
		return name, rest, true
	}
	return "", "", false
}

func isSchemeToken(s string) bool {
	for i := range len(s) {
		c := s[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z':
		case i > 0 && ('0' <= c && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return true
}

// authority strips "//" (or the proxy-collapsed single "/").
func authority(rest string) (string, bool) {
	if strings.HasPrefix(rest, "//") {
		return rest[2:], true
	}
	if strings.HasPrefix(rest, "/") {
		return rest[1:], true
	}
	return "", false
}

// cutQuery splits off "?query" and drops any "#fragment".
func cutQuery(s string) (path, query string) {
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	path, query, _ = strings.Cut(s, "?")
	return path, query
}

func parseNetwork(scheme Scheme, rest string) (Ref, error) {
	auth, ok := authority(rest)
	if !ok {
		return Ref{}, invalid("missing host")
	}
	end := strings.IndexAny(auth, "/?#")
	if end < 0 {
		end = len(auth)
	}
	hostport, tail := auth[:end], auth[end:]
	if strings.IndexByte(hostport, '@') >= 0 {
		return Ref{}, invalid("userinfo in source url")
	}
	host, port, err := splitHostPort(hostport, scheme)
	if err != nil {
		return Ref{}, err
	}
	p, query := cutQuery(tail)
	r := Ref{Scheme: scheme, Host: host, Port: port}
	if scheme == SchemeHTTP || scheme == SchemeHTTPS {
		if p == "" {
			p = "/"
		}
		r.Query = query
		r.Path, r.RawPath, err = canonPath(p)
		return r, err
	}
	r.Path, r.RawPath, err = canonKey(strings.TrimLeft(p, "/"))
	return r, err
}

func defaultPort(s Scheme) string {
	switch s {
	case SchemeHTTP:
		return "80"
	case SchemeHTTPS:
		return "443"
	case SchemeFTP:
		return "21"
	case SchemeSFTP:
		return "22"
	default:
		return ""
	}
}

// splitHostPort validates and normalises "host[:port]" / "[v6][:port]".
func splitHostPort(hp string, scheme Scheme) (host, port string, err error) {
	if strings.HasPrefix(hp, "[") {
		end := strings.IndexByte(hp, ']')
		if end < 0 {
			return "", "", invalid("bad ipv6 literal")
		}
		host, port = hp[1:end], hp[end+1:]
		a, perr := netip.ParseAddr(host)
		if perr != nil || !a.Is6() || a.Zone() != "" {
			return "", "", invalid("bad ipv6 literal")
		}
		host = a.String()
		if port != "" && !strings.HasPrefix(port, ":") {
			return "", "", invalid("bad port")
		}
		port = strings.TrimPrefix(port, ":")
	} else {
		host, port, _ = strings.Cut(hp, ":")
		host = strings.TrimSuffix(strings.ToLower(host), ".")
		if !validHostname(host) {
			return "", "", invalid("bad host")
		}
	}
	if port == "" {
		return host, "", nil
	}
	n, perr := strconv.ParseUint(port, 10, 16)
	if perr != nil || n == 0 {
		return "", "", invalid("bad port")
	}
	port = strconv.FormatUint(n, 10)
	if port == defaultPort(scheme) {
		port = ""
	}
	return host, port, nil
}

// validHostname accepts lower-case LDH labels (plus "_", seen in the wild)
// and IPv4 literals. Internationalised names must arrive as punycode.
func validHostname(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for label := range strings.SplitSeq(h, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for i := range len(label) {
			c := label[i]
			if ('a' > c || c > 'z') && ('0' > c || c > '9') && c != '-' && c != '_' {
				return false
			}
		}
	}
	return true
}

func parseBucket(scheme Scheme, rest string) (Ref, error) {
	auth, ok := authority(rest)
	if !ok {
		return Ref{}, invalid("missing bucket")
	}
	bucket, tail, _ := strings.Cut(auth, "/")
	if i := strings.IndexAny(bucket, "?#"); i >= 0 {
		bucket, tail = bucket[:i], ""
	}
	if scheme == SchemeXI {
		bucket = strings.ToLower(bucket)
		if !validName(bucket) {
			return Ref{}, invalid("bad source name")
		}
	} else if !validBucket(bucket) {
		return Ref{}, invalid("bad bucket")
	}
	p, query := cutQuery(tail)
	r := Ref{Scheme: scheme, Host: bucket}
	var err error
	if r.Path, r.RawPath, err = canonKey(p); err != nil {
		return Ref{}, err
	}
	switch scheme {
	case SchemeXI:
		r.Query = query
	case SchemeS3, SchemeGS:
		if r.Version, err = versionParam(scheme, query); err != nil {
			return Ref{}, err
		}
	default:
	}
	return r, nil
}

// versionParam extracts versionId (s3) or generation (gs) from a query.
func versionParam(scheme Scheme, query string) (string, error) {
	if query == "" {
		return "", nil
	}
	q, err := url.ParseQuery(query)
	if err != nil {
		return "", invalid("bad query")
	}
	switch scheme {
	case SchemeS3:
		return q.Get("versionId"), nil
	case SchemeGS:
		g := q.Get("generation")
		if g != "" {
			if _, err := strconv.ParseInt(g, 10, 64); err != nil {
				return "", invalid("bad generation")
			}
		}
		return g, nil
	default:
		return "", nil
	}
}

// validName matches configured source names: [a-z0-9-]{1,63}.
func validName(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if ('a' > c || c > 'z') && ('0' > c || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

// validBucket accepts s3/gcs bucket and azure container names. Case is
// preserved and significant.
func validBucket(s string) bool {
	if s == "" || len(s) > 255 {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if ('a' > c || c > 'z') && ('A' > c || c > 'Z') && ('0' > c || c > '9') && c != '-' && c != '_' && c != '.' {
			return false
		}
	}
	return true
}

func parseLocal(rest string) (Ref, error) {
	switch {
	case strings.HasPrefix(rest, "//"):
		host, p, _ := strings.Cut(rest[2:], "/")
		if host != "" && !strings.EqualFold(host, "localhost") {
			return Ref{}, invalid("local source with a host")
		}
		rest = p
	default:
		rest = strings.TrimPrefix(rest, "/")
	}
	p, _ := cutQuery(rest)
	r := Ref{Scheme: SchemeLocal}
	var err error
	r.Path, r.RawPath, err = canonKey(strings.TrimLeft(p, "/"))
	return r, err
}

func parseRelative(raw string) (Ref, error) {
	p, query := cutQuery(raw)
	r := Ref{Scheme: SchemeRelative, Query: query}
	var err error
	if r.Path, r.RawPath, err = canonKey(strings.TrimLeft(p, "/")); err != nil {
		return Ref{}, err
	}
	// RFC 3986 segment-nz-nc: a ':' in the first segment of a relative
	// reference would read as a scheme, so the canonical form escapes it.
	first, rest, found := strings.Cut(r.RawPath, "/")
	first = strings.ReplaceAll(first, ":", "%3A")
	if found {
		first += "/" + rest
	}
	r.RawPath = first
	return r, nil
}

// canonKey is canonPath for keys (no leading slash) and rejects empty keys.
func canonKey(escaped string) (decoded, canonical string, err error) {
	if escaped == "" {
		return "", "", invalid("empty key")
	}
	decoded, canonical, err = canonPath(escaped)
	if err == nil && decoded == "" {
		err = invalid("empty key")
	}
	return decoded, canonical, err
}

// canonPath percent-decodes an escaped path once, rejects control
// characters and ".." segments in the decoded form, and re-encodes it
// segment by segment in canonical form.
func canonPath(escaped string) (decoded, canonical string, err error) {
	decoded, uerr := url.PathUnescape(escaped)
	if uerr != nil {
		return "", "", invalid("bad percent-encoding")
	}
	if hasCtl(decoded) {
		return "", "", invalid("control character in path")
	}
	if hasDotDot(decoded) {
		return "", "", errs.Wrapf(errs.KindForbidden, ErrTraversal, "source path contains ..")
	}
	var b strings.Builder
	b.Grow(len(escaped))
	first := true
	for seg := range strings.SplitSeq(escaped, "/") {
		if !first {
			b.WriteByte('/')
		}
		first = false
		s, _ := url.PathUnescape(seg) // whole path already unescaped cleanly
		escapeSegment(&b, s)
	}
	return decoded, b.String(), nil
}

func hasDotDot(p string) bool {
	for seg := range strings.FieldsFuncSeq(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return true
		}
	}
	return false
}

func hasCtl(s string) bool {
	for i := range len(s) {
		if c := s[i]; c < 0x20 || c == 0x7f {
			return true
		}
	}
	return false
}

// escapeSegment writes s with every byte outside the RFC 3986 pchar set
// percent-encoded (upper-case hex). "/" is outside pchar, so a decoded "/"
// inside a segment stays %2F.
func escapeSegment(b *strings.Builder, s string) {
	const hex = "0123456789ABCDEF"
	for i := range len(s) {
		c := s[i]
		if isPchar(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&15])
	}
}

func isPchar(c byte) bool {
	switch {
	case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		return true
	}
	switch c {
	case '-', '.', '_', '~', '!', '$', '&', '\'', '(', ')', '*', '+', ',', ';', '=', ':', '@':
		return true
	default:
		return false
	}
}
