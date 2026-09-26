package simple

import (
	"errors"
	"net"
	"slices"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/router"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

// MountOptions are the shared settings of the simple family
// (XI_DIALECTS_SIMPLE_*).
type MountOptions struct {
	// Enabled lists provider names; the order decides the host fallback.
	Enabled []string
	// Hosts bind the whole family to hosts (see [Mounts]).
	Hosts []string
	// Source is the named source relative keys resolve against.
	Source string
	// Policy overrides the global unsigned policy when set.
	Policy router.Policy
	// Parser options shared by every provider.
	Options
}

// Mounts builds one parser per enabled provider and its router mounts:
//
//   - every native prefix of the provider ("/_next/image", "/_ipx/",
//     "/assets/", ...), with the prefix stripped by the router and put
//     back by the parser;
//   - "/simple/<name>/", under which the provider-native path follows
//     ("/simple/contentful/<space>/<asset>/<token>/a.jpg?w=400");
//   - when Hosts is set, one host-bound dispatcher (dialect "simple")
//     that picks the provider per request: the longest native prefix
//     matching the path, else the provider whose upstream domain equals
//     the request host, else the first enabled provider without native
//     prefixes (the CDN-style ones: contentful, shopify, ...).
//
// Dialects are keyed "simple:<name>" (and "simple" for the dispatcher).
func Mounts(o *MountOptions) (map[string]dialect.Dialect, []router.Mount, error) {
	dialects := make(map[string]dialect.Dialect, len(o.Enabled)+1)
	var mounts []router.Mount
	var parsers []*Parser
	for _, name := range o.Enabled {
		spec := Lookup(name)
		if spec == nil {
			return nil, nil, errors.New("simple: unknown provider " + name)
		}
		key := Name + ":" + name
		if _, dup := dialects[key]; dup {
			return nil, nil, errors.New("simple: provider " + name + " enabled twice")
		}
		p, err := New(spec, &o.Options)
		if err != nil {
			return nil, nil, err
		}
		dialects[key] = p
		parsers = append(parsers, p)
		for _, prefix := range append(slices.Clone(spec.Prefixes), "/"+Name+"/"+name+"/") {
			mounts = append(mounts, router.Mount{
				Name: key, Dialect: key, Prefix: prefix, Source: o.Source, Policy: o.Policy,
			})
		}
	}
	if len(o.Hosts) > 0 && len(parsers) > 0 {
		dialects[Name] = &Dispatcher{parsers: parsers, verifier: o.Verifier}
		mounts = append(mounts, router.Mount{
			Name: Name, Dialect: Name, Hosts: o.Hosts, Source: o.Source, Policy: o.Policy,
		})
	}
	return dialects, mounts, nil
}

// Dispatcher is the host-bound entry of the simple family: it picks the
// provider for each request (see [Mounts]). It is safe for concurrent use.
type Dispatcher struct {
	parsers  []*Parser
	verifier sign.Verifier
}

var _ dialect.Dialect = (*Dispatcher)(nil)

// Name implements dialect.Dialect.
func (*Dispatcher) Name() string { return Name }

// Verifier implements dialect.Dialect: the xi_sig verifier every provider
// shares.
func (d *Dispatcher) Verifier() sign.Verifier { return d.verifier }

// Parse implements dialect.Dialect.
func (d *Dispatcher) Parse(in *dialect.Input) (*dialect.Result, error) {
	p := d.Select(in.Host, in.Path)
	if p == nil {
		return nil, dialect.InvalidURL("no image provider matches the path")
	}
	return p.Parse(in)
}

// Select returns the provider for a host-bound request, or nil.
func (d *Dispatcher) Select(host, path string) *Parser {
	var best *Parser
	bestLen := 0
	for _, p := range d.parsers {
		for _, prefix := range p.spec.Prefixes {
			if n := matchPrefix(path, prefix); n > bestLen {
				best, bestLen = p, n
			}
		}
	}
	if best != nil {
		return best
	}
	host = hostOnly(host)
	for _, p := range d.parsers {
		for _, dom := range p.spec.Domains {
			if host == dom || (strings.HasPrefix(dom, ".") && strings.HasSuffix(host, dom)) {
				return p
			}
		}
	}
	for _, p := range d.parsers {
		if len(p.spec.Prefixes) == 0 {
			return p
		}
	}
	return nil
}

// matchPrefix returns the length of prefix if path is under it, else 0.
func matchPrefix(path, prefix string) int {
	pre := strings.TrimSuffix(prefix, "/")
	if path == pre || strings.HasPrefix(path, pre+"/") {
		return len(pre)
	}
	return 0
}

// hostOnly lower-cases host and drops a port and a trailing dot.
func hostOnly(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}
