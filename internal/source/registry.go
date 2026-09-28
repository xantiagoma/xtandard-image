package source

import (
	"errors"
	"fmt"
	"iter"
	"net/url"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Resolver maps a SourceSpec to a Key using the configured sources; it IS
// the allowlist (ARCHITECTURE §6, §10).
type Resolver interface {
	Resolve(spec transform.SourceSpec) (Key, error) // errs.KindForbidden when nothing matches
}

// ErrInvalidSpec reports a source configuration NewRegistry rejects.
var ErrInvalidSpec = errors.New("invalid source spec")

// Spec is the allowlist-relevant part of one named source's configuration
// (docs/CONFIG.md XI_SOURCES_<NAME>_*). app maps config.Config into it;
// credentials and transport settings stay with the backend.
type Spec struct {
	Name string // [a-z0-9-]{1,63}
	Type string // TypeHTTP, TypeS3, ...

	// http
	Allow   []string // "[http(s)://]host-glob[:port][/path/prefix/]"
	BaseURL string   // base for relative keys; also implicitly allowed

	// s3, gcs, azure
	Buckets []string // allowed buckets/containers (globs, case-sensitive)
	Bucket  string   // default bucket for relative keys; allowed implicitly

	// s3, gcs, azure, fs, ftp, sftp: object key / path prefix jail. It is
	// prepended to relative keys and required on absolute references.
	Prefix string

	// ftp, sftp
	Host string // host name or IP literal the scheme URL must name
	Port int    // 0 = scheme default
}

// Binding pairs a Spec with the backend that serves it.
type Binding struct {
	Spec   Spec
	Source Source
}

// Options configures NewRegistry.
type Options struct {
	// Sources in configuration order (internal/app passes them sorted by
	// name); resolution tries them in this order (after the dialect mount's
	// own source). docs/SOURCES.md documents the result.
	Sources []Binding
	// DevMode (XI_DEV_MODE) resolves otherwise unmatched absolute http(s)
	// URLs to Dev instead of returning KindForbidden.
	DevMode bool
	// Dev is the implicit http backend used in dev mode (Type() == "http").
	// app builds it like any http source, SSRF-safe client included.
	Dev Source
	// Playground serves playground://<area>/<key> under
	// PlaygroundSourceName (WP-94); nil rejects the scheme. It never
	// counts as a configured source for the empty-allowlist check.
	Playground Source
}

type entry struct {
	spec    Spec
	src     Source
	allow   []allowRule
	base    *Ref // http BaseURL
	buckets []string
	prefix  string // jail, "" or "dir/.../"
	port    string // ftp/sftp explicit non-default port
}

// Registry holds the named sources and resolves source strings against
// them. It is immutable after NewRegistry and safe for concurrent use.
type Registry struct {
	byName     map[string]*entry
	order      []*entry
	devMode    bool
	dev        Source
	playground Source
}

var _ Resolver = (*Registry)(nil)

// NewRegistry validates and compiles the sources. With no sources and dev
// mode off it fails: the allowlist would be empty (ARCHITECTURE §10).
func NewRegistry(opts Options) (*Registry, error) {
	r := &Registry{
		byName: make(map[string]*entry, len(opts.Sources)), devMode: opts.DevMode, dev: opts.Dev, playground: opts.Playground,
	}
	var problems []error
	if len(opts.Sources) == 0 && !opts.DevMode {
		problems = append(problems, fmt.Errorf("%w: no sources configured and dev mode off", ErrInvalidSpec))
	}
	if opts.DevMode && (opts.Dev == nil || opts.Dev.Type() != TypeHTTP) {
		problems = append(problems, fmt.Errorf("%w: dev mode needs an http Dev source", ErrInvalidSpec))
	}
	for i := range opts.Sources {
		e, err := compileEntry(&opts.Sources[i])
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if _, dup := r.byName[e.spec.Name]; dup {
			problems = append(problems, fmt.Errorf("%w: source %q defined twice", ErrInvalidSpec, e.spec.Name))
			continue
		}
		r.byName[e.spec.Name] = e
		r.order = append(r.order, e)
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}
	return r, nil
}

func compileEntry(b *Binding) (*entry, error) {
	s := b.Spec
	e := &entry{spec: s, src: b.Source}
	var problems []error
	bad := func(format string, args ...any) {
		problems = append(problems, fmt.Errorf("%w: source %q: %s", ErrInvalidSpec, s.Name, fmt.Sprintf(format, args...)))
	}
	if !validName(s.Name) {
		bad("name must match [a-z0-9-]{1,63}")
	}
	switch {
	case b.Source == nil:
		bad("no backend bound")
	case b.Source.Name() != s.Name || b.Source.Type() != s.Type:
		bad("backend is %s/%s, spec is %s/%s", b.Source.Name(), b.Source.Type(), s.Name, s.Type)
	default:
	}
	if s.Prefix != "" {
		p := strings.TrimLeft(s.Prefix, "/")
		if hasCtl(p) || hasDotDot(p) {
			bad("bad prefix %q", s.Prefix)
		}
		e.prefix = dirPrefix(p)
	}
	switch s.Type {
	case TypeHTTP:
		for _, a := range s.Allow {
			rule, err := compileAllow(a)
			if err != nil {
				bad("%v", err)
				continue
			}
			e.allow = append(e.allow, rule)
		}
		if s.BaseURL != "" {
			base, err := ParseRef(s.BaseURL)
			if err != nil || (base.Scheme != SchemeHTTP && base.Scheme != SchemeHTTPS) || base.Query != "" {
				bad("base url must be an absolute http(s) url without a query")
			} else {
				base.Path, base.RawPath = dirPrefix(base.Path), dirPrefix(base.RawPath)
				e.base = &base
				e.allow = append(e.allow, e.baseRule())
			}
		}
		if len(e.allow) == 0 {
			bad("http source needs allow patterns or a base url")
		}
	case TypeS3, TypeGCS, TypeAzure:
		for _, g := range s.Buckets {
			if !validBucketGlob(g) {
				bad("bad bucket glob %q", g)
				continue
			}
			e.buckets = append(e.buckets, g)
		}
		if s.Bucket != "" {
			if !validBucket(s.Bucket) {
				bad("bad bucket %q", s.Bucket)
			}
			e.buckets = append(e.buckets, s.Bucket)
		}
	case TypeFTP, TypeSFTP:
		host, port, err := splitHostPort(s.Host, SchemeFTP)
		if err != nil || port != "" {
			bad("bad host %q (put the port in Port)", s.Host)
		}
		e.spec.Host = host
		if s.Port < 0 || s.Port > 65535 {
			bad("bad port %d", s.Port)
		} else if s.Port != 0 {
			e.port = strconv.Itoa(s.Port)
			if (s.Type == TypeFTP && s.Port == 21) || (s.Type == TypeSFTP && s.Port == 22) {
				e.port = ""
			}
		}
	case TypeFS:
	default:
		bad("unknown type %q", s.Type)
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}
	return e, nil
}

// Source returns the backend registered under name (DevSourceName in dev
// mode included).
func (r *Registry) Source(name string) (Source, bool) {
	switch name {
	case DevSourceName:
		return r.dev, r.devMode && r.dev != nil
	case PlaygroundSourceName:
		return r.playground, r.playground != nil
	}
	e, ok := r.byName[name]
	if !ok {
		return nil, false
	}
	return e.src, true
}

func notAllowed(ref *Ref, why string) error {
	return errs.Wrapf(errs.KindForbidden, ErrNotAllowed, "%s: %s", why, redactRef(ref))
}

// refused is notAllowed with a client-safe message naming the check that
// failed (WP-95). public never echoes configuration (allow patterns, base
// urls, buckets, prefixes); names and schemes the client wrote are fine.
func refused(ref *Ref, why, public string) error {
	return errs.WithPublic(notAllowed(ref, why), public)
}

func redactRef(ref *Ref) string {
	c := *ref
	c.Query = ""
	s := c.String()
	if len(s) > maxRedactedLen {
		s = s[:maxRedactedLen] + "..."
	}
	return s
}

// Resolve maps spec to a Key. Order (ARCHITECTURE §6):
//
//  1. xi://<name>/<key>: the named source, key relative to it; for an http
//     source an absolute http(s) key is that URL (entry.httpKey).
//  2. s3://, gs://, abs:// (az://), local:/// (file:///), ftp://, sftp://:
//     the first source of that type (the mount's source first, then
//     configuration order) whose buckets / host allow the target and whose
//     prefix jail contains the key.
//  3. http(s)://: the first http source whose allow patterns (or base url)
//     match; otherwise the implicit dev source in dev mode.
//  4. a relative key: resolved against spec.Mount's source.
//  5. playground://<area>/<key>: the playground store, when configured.
//
// Nothing matching is KindForbidden (ErrNotAllowed); a malformed string is
// KindInvalidURL (ErrInvalidSource); ".." is KindForbidden (ErrTraversal).
func (r *Registry) Resolve(spec transform.SourceSpec) (Key, error) {
	ref, err := ParseRef(spec.Raw)
	if err != nil {
		return Key{}, err
	}
	var mount *entry
	if spec.Mount != "" {
		if mount = r.byName[spec.Mount]; mount == nil {
			return Key{}, notAllowed(&ref, "unknown mount source "+strconv.Quote(spec.Mount))
		}
	}
	switch ref.Scheme {
	case SchemeRelative:
		if mount == nil {
			return Key{}, notAllowed(&ref, "relative source without a mount source")
		}
		return mount.relative(&ref)
	case SchemeXI:
		e := r.byName[ref.Host]
		if e == nil {
			return Key{}, refused(&ref, "unknown source", "unknown source "+strconv.Quote(ref.Host))
		}
		return e.relative(&ref)
	case SchemeHTTP, SchemeHTTPS:
		for e := range r.candidates(mount, TypeHTTP) {
			if e.allowsURL(&ref) {
				return Key{Source: e.spec.Name, Path: ref.String()}, nil
			}
		}
		if r.devMode {
			return Key{Source: DevSourceName, Path: ref.String(), Dev: true}, nil
		}
		return Key{}, refused(&ref, "no http source allows url", "no source allows this url")
	case SchemePlayground:
		if r.playground == nil {
			return Key{}, notAllowed(&ref, "playground store disabled")
		}
		return Key{Source: PlaygroundSourceName, Path: ref.Host + "/" + ref.Path}, nil
	default:
		for e := range r.candidates(mount, ref.Scheme.sourceType()) {
			if k, ok := e.absolute(&ref); ok {
				return k, nil
			}
		}
		return Key{}, refused(&ref, "no "+ref.Scheme.sourceType()+" source allows reference",
			"no source allows this "+ref.Scheme.String()+":// reference")
	}
}

// candidates yields the sources of type typ: the mount's first, then the
// rest in configuration order.
func (r *Registry) candidates(mount *entry, typ string) iter.Seq[*entry] {
	return func(yield func(*entry) bool) {
		if mount != nil && mount.spec.Type == typ && !yield(mount) {
			return
		}
		for _, e := range r.order {
			if e != mount && e.spec.Type == typ && !yield(e) {
				return
			}
		}
	}
}

func (e *entry) allowsURL(ref *Ref) bool {
	for _, a := range e.allow {
		if a.matchURL(ref) {
			return true
		}
	}
	return false
}

// absolute checks a non-http scheme reference against e.
func (e *entry) absolute(ref *Ref) (Key, bool) {
	k := Key{Source: e.spec.Name, Path: ref.Path, Version: ref.Version}
	switch e.spec.Type {
	case TypeS3, TypeGCS, TypeAzure:
		if !e.allowsBucket(ref.Host) {
			return Key{}, false
		}
		k.Bucket = ref.Host
	case TypeFTP, TypeSFTP:
		if ref.Host != e.spec.Host || ref.Port != e.port {
			return Key{}, false
		}
	default:
	}
	return k, inJail(e.prefix, ref.Path)
}

func (e *entry) allowsBucket(b string) bool {
	for _, g := range e.buckets {
		if matchBucket(g, b) {
			return true
		}
	}
	return false
}

// relative resolves a relative or xi:// key against e: http sources join it
// to the base url or take an absolute key as is (entry.httpKey); the others
// prepend the prefix jail.
func (e *entry) relative(ref *Ref) (Key, error) {
	k := Key{Source: e.spec.Name}
	switch e.spec.Type {
	case TypeHTTP:
		return e.httpKey(ref)
	case TypeS3, TypeGCS, TypeAzure:
		if e.spec.Bucket == "" {
			return Key{}, refused(ref, "source has no default bucket",
				"source "+strconv.Quote(e.spec.Name)+" has no default bucket: name the bucket in the url")
		}
		k.Bucket = e.spec.Bucket
		if ref.Query != "" {
			q, err := url.ParseQuery(ref.Query)
			if err != nil {
				return Key{}, invalid("bad query")
			}
			switch e.spec.Type {
			case TypeS3:
				k.Version = q.Get("versionId")
			case TypeGCS:
				k.Version = q.Get("generation")
			default:
			}
		}
	default:
	}
	k.Path = e.prefix + ref.Path
	return k, nil
}

// httpKey resolves the key of xi://<name>/<key> (or a relative key on the
// mount) against the http source e (WP-95, docs/URL-DIALECTS.md §3.1):
//
//   - an xi:// key that is itself an absolute http(s) URL (literal, or
//     percent-encoded as a whole) is fetched as that URL, through e's allow
//     patterns, headers and limits, when e has no base url or when the URL
//     lies under e's base url;
//   - any other key is joined to the base url (an absolute URL outside the
//     base included, as before WP-95); without a base url it is refused.
func (e *entry) httpKey(ref *Ref) (Key, error) {
	abs, isAbs, err := absoluteKey(ref)
	if err != nil {
		return Key{}, err
	}
	name := strconv.Quote(e.spec.Name)
	switch {
	case isAbs && e.base == nil:
		if !e.allowsURL(&abs) {
			return Key{}, refused(&abs, "url not in the allowlist of source "+name, e.allowPublic(&abs))
		}
		return Key{Source: e.spec.Name, Path: abs.String()}, nil
	case isAbs && e.baseRule().matchURL(&abs):
		return Key{Source: e.spec.Name, Path: abs.String()}, nil
	case e.base == nil:
		return Key{}, refused(ref, "http source "+name+" has no base url and the key is not an absolute http(s) url",
			"source "+name+" has no base_url: the key must be an absolute http(s) url")
	default:
	}
	u := e.base.Scheme.String() + "://" + e.base.hostPort() + e.base.RawPath + ref.RawPath
	if ref.Query != "" {
		u += "?" + ref.Query
	}
	joined, err := ParseRef(u)
	if err != nil {
		return Key{}, err
	}
	return Key{Source: e.spec.Name, Path: joined.String()}, nil
}

// baseRule is the allow rule the base url implies.
func (e *entry) baseRule() allowRule {
	return allowRule{scheme: e.base.Scheme, host: e.base.Host, port: e.base.Port, prefix: e.base.Path}
}

// allowPublic is the client-safe reason e's allowlist refuses the http(s)
// ref: its host, or (host allowed) its scheme, port or path.
func (e *entry) allowPublic(ref *Ref) string {
	name := strconv.Quote(e.spec.Name)
	for _, a := range e.allow {
		if matchHost(a.host, ref.Host) {
			return "source " + name + ": url scheme, port or path not in the source allowlist"
		}
	}
	return "source " + name + ": host not in the source allowlist"
}

// absoluteKey reports whether the key of the xi:// ref is an absolute
// http(s) URL and parses it, with the xi query as its query. The literal
// form (xi://web/https://host/p, the inner URL's percent-encoding kept as
// written) is preferred; a key percent-encoded as a whole
// (xi://web/https%3A%2F%2Fhost%2Fp) is decoded once. ParseRef repairs a
// proxy-collapsed "https:/host".
func absoluteKey(ref *Ref) (Ref, bool, error) {
	if ref.Scheme != SchemeXI {
		return Ref{}, false, nil
	}
	s := ref.RawPath
	if !isHTTPScheme(s) {
		if !isHTTPScheme(ref.Path) {
			return Ref{}, false, nil
		}
		s = ref.Path
	}
	if ref.Query != "" {
		s += "?" + ref.Query
	}
	abs, err := ParseRef(s)
	if err != nil {
		return Ref{}, true, err
	}
	return abs, true, nil
}

// isHTTPScheme reports whether s starts with "http:/" or "https:/" (any
// case).
func isHTTPScheme(s string) bool {
	name, rest, ok := splitScheme(s)
	return ok && (name == "http" || name == "https") && strings.HasPrefix(rest, "/")
}

// ResolveRedirect vets one redirect hop of an http fetch of from: location
// must be an absolute http(s) URL, must not downgrade https to http, and
// must be allowed by the same source that served from (dev keys accept any
// host; SSRF rules still apply in httpsrc). It returns the Key to fetch
// next.
func (r *Registry) ResolveRedirect(from Key, location string) (Key, error) {
	ref, err := ParseRef(location)
	if err != nil {
		return Key{}, err
	}
	if ref.Scheme != SchemeHTTP && ref.Scheme != SchemeHTTPS {
		return Key{}, notAllowed(&ref, "redirect to a non-http url")
	}
	if ref.Scheme == SchemeHTTP && strings.HasPrefix(from.Path, "https://") {
		return Key{}, notAllowed(&ref, "redirect downgrades https to http")
	}
	if from.Dev {
		if !r.devMode || from.Source != DevSourceName {
			return Key{}, notAllowed(&ref, "dev key outside dev mode")
		}
		return Key{Source: DevSourceName, Path: ref.String(), Dev: true}, nil
	}
	e := r.byName[from.Source]
	if e == nil || e.spec.Type != TypeHTTP || !e.allowsURL(&ref) {
		return Key{}, notAllowed(&ref, "redirect target not allowed")
	}
	return Key{Source: e.spec.Name, Path: ref.String()}, nil
}
