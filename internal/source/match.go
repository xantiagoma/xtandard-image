package source

import (
	"cmp"
	"fmt"
	"net/netip"
	"path"
	"strconv"
	"strings"
)

// allowRule is one compiled http allow pattern:
// "[http://|https://]host-glob[:port][/path/prefix/]".
type allowRule struct {
	scheme Scheme // SchemeRelative = any of http/https
	host   string // lower-case glob, "*" = any host
	port   string // "" = any port (the SSRF port allowlist still applies); else the effective port
	prefix string // decoded path prefix, "/" or "/dir/.../"
}

// compileAllow parses an http allow pattern (docs/CONFIG.md
// XI_SOURCES_<NAME>_ALLOW).
func compileAllow(p string) (allowRule, error) {
	var r allowRule
	rest := p
	switch lower := strings.ToLower(p); {
	case strings.HasPrefix(lower, "https://"):
		r.scheme, rest = SchemeHTTPS, p[len("https://"):]
	case strings.HasPrefix(lower, "http://"):
		r.scheme, rest = SchemeHTTP, p[len("http://"):]
	case strings.Contains(p, "://"):
		return r, fmt.Errorf("allow pattern %q: only http and https schemes", p)
	default:
	}
	hostport, pfx, hasPath := strings.Cut(rest, "/")
	if strings.ContainsAny(hostport, "@?#") {
		return r, fmt.Errorf("allow pattern %q: bad host", p)
	}
	switch {
	case strings.HasPrefix(hostport, "["):
		end := strings.IndexByte(hostport, ']')
		if end < 0 {
			return r, fmt.Errorf("allow pattern %q: bad ipv6 literal", p)
		}
		a, err := netip.ParseAddr(hostport[1:end])
		if err != nil || !a.Is6() || a.Zone() != "" {
			return r, fmt.Errorf("allow pattern %q: bad ipv6 literal", p)
		}
		r.host, r.port = a.String(), strings.TrimPrefix(hostport[end+1:], ":")
	default:
		r.host, r.port, _ = strings.Cut(hostport, ":")
		r.host = strings.TrimSuffix(strings.ToLower(r.host), ".")
		if !validHostGlob(r.host) {
			return r, fmt.Errorf("allow pattern %q: bad host glob", p)
		}
	}
	if r.port != "" {
		n, err := strconv.ParseUint(r.port, 10, 16)
		if err != nil || n == 0 {
			return r, fmt.Errorf("allow pattern %q: bad port", p)
		}
		r.port = strconv.FormatUint(n, 10)
	}
	r.prefix = "/"
	if hasPath && pfx != "" {
		dec, _, err := canonPath("/" + pfx)
		if err != nil {
			return r, fmt.Errorf("allow pattern %q: bad path prefix", p)
		}
		r.prefix = dirPrefix(dec)
	}
	return r, nil
}

// dirPrefix makes a jail prefix end in "/", so "/img" cannot match
// "/img-private/x".
func dirPrefix(p string) string {
	if p == "" || strings.HasSuffix(p, "/") {
		return p
	}
	return p + "/"
}

// validHostGlob accepts "*", or dot-separated labels made of [a-z0-9_-*].
func validHostGlob(g string) bool {
	if g == "*" {
		return true
	}
	if g == "" || len(g) > 253 {
		return false
	}
	for label := range strings.SplitSeq(g, ".") {
		if label == "" {
			return false
		}
		for i := range len(label) {
			c := label[i]
			if ('a' > c || c > 'z') && ('0' > c || c > '9') && c != '-' && c != '_' && c != '*' {
				return false
			}
		}
	}
	return true
}

// matchURL reports whether the http(s) ref r is allowed by rule a.
func (a allowRule) matchURL(r *Ref) bool {
	if a.scheme != SchemeRelative && a.scheme != r.Scheme {
		return false
	}
	if a.port != "" && a.port != cmp.Or(r.Port, defaultPort(r.Scheme)) {
		return false
	}
	if !matchHost(a.host, r.Host) {
		return false
	}
	return strings.HasPrefix(r.Path, a.prefix)
}

// matchHost matches a lower-case host against a glob. Hosts are compared
// case-insensitively (both sides are lower-cased by the parsers). A leading
// "*" label matches one or more labels ("*.example.com" matches
// "a.example.com" and "a.b.example.com", not "example.com"); any other "*"
// matches within one label ("img-*.example.com"). "*" alone matches any host.
func matchHost(glob, host string) bool {
	if glob == "*" {
		return true
	}
	if !strings.Contains(glob, "*") {
		return glob == host
	}
	gl := strings.Split(glob, ".")
	hl := strings.Split(host, ".")
	if gl[0] == "*" {
		tail := gl[1:]
		if len(hl) <= len(tail) {
			return false
		}
		return matchLabels(tail, hl[len(hl)-len(tail):])
	}
	return len(gl) == len(hl) && matchLabels(gl, hl)
}

func matchLabels(glob, host []string) bool {
	for i, g := range glob {
		if ok, err := path.Match(g, host[i]); err != nil || !ok {
			return false
		}
	}
	return true
}

// validBucketGlob accepts bucket names with "*" wildcards.
func validBucketGlob(g string) bool {
	return validBucket(strings.ReplaceAll(g, "*", "x"))
}

// matchBucket matches a bucket against a glob; bucket names are
// case-sensitive.
func matchBucket(glob, bucket string) bool {
	ok, err := path.Match(glob, bucket)
	return err == nil && ok
}

// inJail reports whether key lies under the (dirPrefix-normalised) prefix.
func inJail(prefix, key string) bool {
	return strings.HasPrefix(key, prefix)
}
