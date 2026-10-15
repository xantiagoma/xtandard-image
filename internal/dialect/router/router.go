// Package router picks the dialect mount that handles a request, runs the
// dialect's parser on the path after the mount prefix, and enforces the
// unsigned-request policy of that mount (docs/URL-DIALECTS.md §1–2,
// docs/ARCHITECTURE.md §2 stage 2 and §5).
//
// # Precedence
//
// Selection is deterministic; the first rule that matches wins:
//
//	| # | Rule                     | Matches when                                              | Input.Path            | Input.Mount.Prefix |
//	|---|--------------------------|-----------------------------------------------------------|-----------------------|--------------------|
//	| 1 | exact host binding       | request host (lower-cased, port and trailing dot removed) | whole path            | ""                 |
//	|   |                          | equals a mount host                                       |                       |                    |
//	| 2 | wildcard host binding    | "*.example.com" and the host ends in ".example.com"       | whole path            | ""                 |
//	|   |                          | (any depth; the longest suffix wins; never the apex)      |                       |                    |
//	| 3 | root ?url= rule          | Options.RootURL is set, path is "/" or "" and the query   | "/"                   | ""                 |
//	|   |                          | has a "url" key (wsrv / query shape at the root)          |                       |                    |
//	| 4 | longest path prefix      | path == prefix or path starts with prefix + "/"; prefixes | path minus the prefix | prefix, without   |
//	|   |                          | are compared without their trailing slash, so "/q" and    | (always starts "/")   | trailing slash     |
//	|   |                          | "/q/" both match mount "/q/"; "/qx" does not              |                       |                    |
//	| 5 | root prefix "/"          | any path (a prefix mount at "/", normally imgproxy)       | whole path            | ""                 |
//	| 6 | default mount            | nothing above matched                                     | whole path            | ""                 |
//
// Rule 3 is why "/?url=example.com/a.jpg&w=300" reaches wsrv (or query) even
// though imgproxy is mounted at "/". A mount with both hosts and a prefix is
// reachable both ways: on its hosts for every path, and on any other host
// under its prefix. A mount with hosts and no prefix is host-only.
//
// Health, readiness and metrics paths never reach the router: the server
// registers them on its ServeMux ahead of the catch-all (ARCHITECTURE §3).
//
// # Info endpoint
//
// When the selected dialect implements [InfoParser] and the path after the
// mount prefix is "/info" or starts with "/info/", the router strips that
// segment and calls ParseInfo instead of Parse ("/info/sig/opts/plain/src"
// on a root mount, "/p/info/sig/..." under mount "/p/"). The signature and
// unsigned policy apply unchanged (restricted rejects info requests).
// "/video" selects [VideoParser] the same way (video transformations,
// which follow [VideoPolicy] as well).
//
// # Source binding
//
// The mount's Source is passed to the dialect in Input.Mount.Source and, if
// the dialect left Request.Source.Mount empty, copied there, so the source
// resolver jails relative keys to that named source.
//
// # Unsigned policy
//
// After a successful parse, a result whose Signature is not Verified or
// Trusted is checked against the mount's policy (Mount.Policy, else
// Options.Policy, else restricted):
//
//   - allow: anything.
//   - deny: 403.
//   - restricted: only the cheap option set described in [Restrictions]
//     (allowlisted width, height, dpr and quality; no watermarks, fallback
//     URLs, security options, raw, info, video or effects). Everything
//     else is 403.
//
// Video transformations (Kind video, from any dialect) are checked against
// Options.Video as well ([VideoPolicy]): deny by default, allow leaves
// them to the mount's policy, restricted admits what the URL's video
// presets make of it.
//
// Denials are errs.KindForbidden with the public message "unsigned request
// not allowed" and wrap [ErrUnsignedNotAllowed]; the internal message names
// the offending option.
package router

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// Policy governs requests without a verified signature
// (XI_SECURITY_UNSIGNED_POLICY and the per-dialect overrides).
type Policy string

// Unsigned policies. The empty value on a Mount inherits Options.Policy;
// the empty Options.Policy means PolicyRestricted.
const (
	PolicyAllow      Policy = "allow"
	PolicyRestricted Policy = "restricted"
	PolicyDeny       Policy = "deny"
)

// Mount binds a dialect to a place in the URL space.
type Mount struct {
	// Name is the mount name for logs and metrics; "" for the default mount.
	Name string
	// Dialect is the key of the dialect in the map given to New.
	Dialect string
	// Prefix is the path prefix ("/q/", "/imgix/shop/"); "/" is the root
	// prefix and "" means no prefix mount (host-only). It must start with
	// "/" when set.
	Prefix string
	// Hosts are host bindings: "images.example.com" or "*.example.com".
	Hosts []string
	// Source is the named source relative keys resolve against; "" = none.
	Source string
	// Policy overrides Options.Policy when set. Route returns mounts with
	// the effective policy filled in.
	Policy Policy
}

// Options configures a Router. The app maps config.Config into it
// (ARCHITECTURE §3: components declare their own options).
type Options struct {
	// Mounts are the enabled dialect mounts, primary and extra.
	Mounts []Mount
	// Default handles requests no mount matches (XI_DIALECTS_DEFAULT). Only
	// Name, Dialect, Source and Policy are used. A zero Default means
	// unmatched requests are 404.
	Default Mount
	// RootURL, when non-nil, handles "/" and "" paths whose query has a
	// "url" key (wsrv, else query). Only Name, Dialect, Source and Policy
	// are used.
	RootURL *Mount
	// Policy is the global unsigned policy; "" means restricted.
	Policy Policy
	// Restrictions are the allowlists of the restricted policy.
	Restrictions Restrictions
	// Video is the unsigned policy of video transformations
	// (videopolicy.go); its zero value denies them.
	Video VideoPolicy
}

// ErrNoRoute is wrapped by the 404 returned when no mount matches.
var ErrNoRoute = errors.New("router: no route")

// Router selects mounts. It is immutable after New and safe for concurrent
// use.
type Router struct {
	exact    map[string]*Mount
	wildcard []hostSuffix // longest suffix first
	prefixes []prefixMount
	root     *Mount // prefix "/" mount
	rootURL  *Mount
	def      *Mount
	all      []*Mount // opts.Mounts in order, effective policy filled in
	dialects map[string]dialect.Dialect
	restrict *restrictions
	video    *videoPolicy
}

type hostSuffix struct {
	suffix string // ".example.com"
	m      *Mount
}

type prefixMount struct {
	prefix string // without trailing slash, non-empty
	m      *Mount
}

// New validates opts and builds a Router. Every mount must name a dialect
// present in dialects; prefixes and hosts must be unique.
func New(opts *Options, dialects map[string]dialect.Dialect) (*Router, error) {
	if opts == nil {
		opts = &Options{}
	}
	global := opts.Policy
	if global == "" {
		global = PolicyRestricted
	}
	if !validPolicy(global) {
		return nil, fmt.Errorf("router: unknown unsigned policy %q", global)
	}
	vp, err := newVideoPolicy(&opts.Video)
	if err != nil {
		return nil, err
	}
	rt := &Router{
		exact:    make(map[string]*Mount),
		dialects: make(map[string]dialect.Dialect, len(dialects)),
		restrict: newRestrictions(&opts.Restrictions),
		video:    vp,
	}
	for name, d := range dialects {
		if d != nil {
			rt.dialects[name] = d
		}
	}
	finish := func(m Mount, what string) (*Mount, error) {
		if _, ok := rt.dialects[m.Dialect]; !ok {
			return nil, fmt.Errorf("router: %s: dialect %q is not available", what, m.Dialect)
		}
		if m.Policy == "" {
			m.Policy = global
		}
		if !validPolicy(m.Policy) {
			return nil, fmt.Errorf("router: %s: unknown unsigned policy %q", what, m.Policy)
		}
		m.Hosts = slices.Clone(m.Hosts)
		return &m, nil
	}

	seenPrefix := make(map[string]string)
	for i := range opts.Mounts {
		what := fmt.Sprintf("mount %d (%s)", i, opts.Mounts[i].Name)
		m, err := finish(opts.Mounts[i], what)
		if err != nil {
			return nil, err
		}
		if m.Prefix == "" && len(m.Hosts) == 0 {
			return nil, fmt.Errorf("router: %s: needs a prefix or hosts", what)
		}
		rt.all = append(rt.all, m)
		if err := rt.addHosts(m, what); err != nil {
			return nil, err
		}
		if m.Prefix == "" {
			continue
		}
		if !strings.HasPrefix(m.Prefix, "/") {
			return nil, fmt.Errorf("router: %s: prefix %q must start with /", what, m.Prefix)
		}
		key := strings.TrimRight(m.Prefix, "/")
		if prev, dup := seenPrefix[key]; dup {
			return nil, fmt.Errorf("router: %s: prefix %q already used by %s", what, m.Prefix, prev)
		}
		seenPrefix[key] = what
		if key == "" {
			rt.root = m
			continue
		}
		rt.prefixes = append(rt.prefixes, prefixMount{prefix: key, m: m})
	}
	slices.SortStableFunc(rt.prefixes, func(a, b prefixMount) int { return len(b.prefix) - len(a.prefix) })
	slices.SortStableFunc(rt.wildcard, func(a, b hostSuffix) int { return len(b.suffix) - len(a.suffix) })

	if opts.Default.Dialect != "" {
		d := opts.Default
		d.Prefix, d.Hosts = "", nil
		m, err := finish(d, "default mount")
		if err != nil {
			return nil, err
		}
		rt.def = m
	}
	if opts.RootURL != nil {
		d := *opts.RootURL
		d.Prefix, d.Hosts = "", nil
		m, err := finish(d, "root url mount")
		if err != nil {
			return nil, err
		}
		rt.rootURL = m
	}
	return rt, nil
}

// Mounts returns copies of the prefix and host mounts in configuration
// order, with the effective unsigned policy filled in. The default and
// root ?url= mounts are not included. The playground lists them.
func (rt *Router) Mounts() []Mount {
	out := make([]Mount, 0, len(rt.all))
	for _, m := range rt.all {
		c := *m
		c.Hosts = slices.Clone(m.Hosts)
		out = append(out, c)
	}
	return out
}

func validPolicy(p Policy) bool {
	return p == PolicyAllow || p == PolicyRestricted || p == PolicyDeny
}

func (rt *Router) addHosts(m *Mount, what string) error {
	for _, h := range m.Hosts {
		n := normalizeHost(h)
		if suffix, ok := strings.CutPrefix(n, "*."); ok {
			if suffix == "" || strings.Contains(suffix, "*") {
				return fmt.Errorf("router: %s: bad wildcard host %q", what, h)
			}
			for _, w := range rt.wildcard {
				if w.suffix == "."+suffix {
					return fmt.Errorf("router: %s: host %q is bound twice", what, h)
				}
			}
			rt.wildcard = append(rt.wildcard, hostSuffix{suffix: "." + suffix, m: m})
			continue
		}
		if n == "" || strings.Contains(n, "*") {
			return fmt.Errorf("router: %s: bad host %q", what, h)
		}
		if _, dup := rt.exact[n]; dup {
			return fmt.Errorf("router: %s: host %q is bound twice", what, h)
		}
		rt.exact[n] = m
	}
	return nil
}

// normalizeHost lower-cases h and removes a port and a trailing dot.
func normalizeHost(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.TrimSuffix(strings.Trim(h, "[]"), ".")
	return strings.ToLower(h)
}

// selectMount returns the mount for host, escaped path and query, the path
// the dialect sees and the prefix that was stripped from it.
func (rt *Router) selectMount(host, path string, query url.Values) (m *Mount, rest, prefix string) {
	if path == "" {
		path = "/"
	}
	if host != "" {
		h := normalizeHost(host)
		if m := rt.exact[h]; m != nil {
			return m, path, ""
		}
		for _, w := range rt.wildcard {
			if len(h) > len(w.suffix) && strings.HasSuffix(h, w.suffix) {
				return w.m, path, ""
			}
		}
	}
	if rt.rootURL != nil && path == "/" && query.Has("url") {
		return rt.rootURL, path, ""
	}
	for _, p := range rt.prefixes {
		after, ok := strings.CutPrefix(path, p.prefix)
		if !ok {
			continue
		}
		switch {
		case after == "":
			return p.m, "/", p.prefix
		case after[0] == '/':
			return p.m, after, p.prefix
		}
	}
	if rt.root != nil {
		return rt.root, path, ""
	}
	return rt.def, path, ""
}

// InfoParser is implemented by dialects with a metadata endpoint under
// "<mount>/info" (imgproxy's /info, docs/URL-DIALECTS.md §3.3). ParseInfo
// receives the path after the "/info" segment and returns a request of
// Kind transform.KindInfo.
type InfoParser interface {
	ParseInfo(in *dialect.Input) (*dialect.Result, error)
}

// VideoParser is implemented by dialects with a video transformation
// endpoint under "<mount>/video" (the imgproxy dialect's /video URLs,
// docs/URL-DIALECTS.md §3.4, WP-122). ParseVideo receives the path after
// the "/video" segment and returns a request of Kind transform.KindVideo.
type VideoParser interface {
	ParseVideo(in *dialect.Input) (*dialect.Result, error)
}

// infoSegment and videoSegment are the first path segments (after the
// mount prefix) that select InfoParser.ParseInfo and
// VideoParser.ParseVideo.
const (
	infoSegment  = "/info"
	videoSegment = "/video"
)

// parse runs d on in: a path under "/info" goes to ParseInfo when d
// implements InfoParser, one under "/video" to ParseVideo when d
// implements VideoParser, with the segment removed; everything else goes
// to Parse. Dialects without such an endpoint see "/info/..." and
// "/video/..." as ordinary paths.
func parse(d dialect.Dialect, in *dialect.Input) (*dialect.Result, error) {
	if ip, ok := d.(InfoParser); ok {
		if sub, ok := under(in, infoSegment); ok {
			return ip.ParseInfo(sub)
		}
	}
	if vp, ok := d.(VideoParser); ok {
		if sub, ok := under(in, videoSegment); ok {
			return vp.ParseVideo(sub)
		}
	}
	return d.Parse(in)
}

// under returns in with its path made relative to segment, when the path
// is segment or starts with segment + "/".
func under(in *dialect.Input, segment string) (*dialect.Input, bool) {
	rest, ok := strings.CutPrefix(in.Path, segment)
	switch {
	case !ok || (rest != "" && rest[0] != '/'):
		return nil, false
	case rest == "":
		rest = "/"
	}
	sub := *in
	sub.Path = rest
	return &sub, true
}

// Route selects the mount for r, parses the URL with the mount's dialect
// and applies the unsigned policy. The returned mount must not be
// modified. Errors are *errs.Error values: the dialect's parse errors,
// KindNotFound (404) when no mount matches, and KindForbidden (403) when
// the unsigned policy rejects the request.
func (rt *Router) Route(r *http.Request) (*dialect.Result, *Mount, error) {
	if r == nil || r.URL == nil {
		return nil, nil, dialect.InvalidURL("missing url")
	}
	path := r.URL.EscapedPath()
	if path != "" && path[0] != '/' {
		return nil, nil, dialect.InvalidURL("path must start with /")
	}
	query := r.URL.Query()
	m, path, prefix := rt.selectMount(r.Host, path, query)
	if m == nil {
		return nil, nil, &errs.Error{Kind: errs.KindNotFound, Public: "not found", Err: ErrNoRoute}
	}
	in := &dialect.Input{
		Path:     path,
		Query:    query,
		RawQuery: r.URL.RawQuery,
		Host:     r.Host,
		Header:   r.Header,
		Mount:    dialect.Mount{Name: m.Name, Prefix: prefix, Source: m.Source},
	}
	res, err := parse(rt.dialects[m.Dialect], in)
	if err != nil {
		return nil, m, err
	}
	if res == nil || res.Request == nil {
		return nil, m, errs.Newf(errs.KindInternal, "router: dialect %s returned no request", m.Dialect)
	}
	if m.Source != "" && res.Request.Source.Mount == "" {
		res.Request.Source.Mount = m.Source
	}
	if err := rt.enforce(m, res); err != nil {
		return nil, m, err
	}
	return res, m, nil
}
