// Package httpsrc is the http(s) source backend (docs/ARCHITECTURE.md §6):
// a source.Source that GETs Key.Path through the SSRF-safe client of
// internal/security/ssrf (§10), with conditional requests, a body limit, a
// per-source header allowlist and per-hop redirect vetting.
package httpsrc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/ssrf"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

// Sentinel causes, wrapped in *errs.Error values that carry the kind.
var (
	// ErrInvalidConfig reports a Config that New rejects.
	ErrInvalidConfig = errors.New("httpsrc: invalid config")
	// ErrRedirectRefused is the cause when an origin redirects and the
	// fetch has no FetchOptions.CheckRedirect hook (KindForbidden).
	ErrRedirectRefused = errors.New("httpsrc: redirect refused")
	// ErrStatus is the cause for an origin status that is not 200 (or 304
	// on a conditional request).
	ErrStatus = errors.New("httpsrc: unexpected upstream status")
)

// Config configures one http source (docs/CONFIG.md XI_SOURCES_<NAME>_*,
// XI_FETCH_*). Transport settings (dial/TLS/header timeouts, max redirects,
// ports, private networks, proxy, User-Agent) live in the ssrf.Options the
// Client was built from.
type Config struct {
	// Name is the source name (source.DevSourceName for the dev-mode
	// source).
	Name string
	// Client must come from ssrf.New so every request and redirect hop is
	// SSRF-checked; app shares one client between http sources (the dev
	// source included). Nil builds ssrf.New(nil): public addresses only,
	// ports 80/443, 5 redirects, no proxy.
	Client *http.Client
	// Timeout bounds one fetch attempt, from sending the request to the
	// last body byte (XI_FETCH_TIMEOUT). Zero leaves it to ctx.
	Timeout time.Duration
	// Headers are static request headers sent on every fetch
	// (XI_SOURCES_<NAME>_HEADERS, e.g. Authorization for a private origin).
	// They win over passthrough headers.
	Headers http.Header
	// Passthrough lists the client request headers copied from
	// FetchOptions.Header (e.g. "Accept"). Nothing is forwarded by default;
	// credentials such as Authorization only when listed here.
	Passthrough []string
}

// Source fetches http(s) URLs. It is immutable after New and safe for
// concurrent use.
type Source struct {
	name        string
	client      *http.Client
	timeout     time.Duration
	headers     http.Header
	passthrough []string // canonical names
}

var _ source.Source = (*Source)(nil)

// New validates cfg and returns the source. Header names the transport or
// this package manages (hop-by-hop, Host, Content-Length, Accept-Encoding,
// Range and the conditional headers) are rejected in Headers and
// Passthrough.
func New(cfg Config) (*Source, error) {
	var problems []error
	if cfg.Name == "" {
		problems = append(problems, fmt.Errorf("%w: empty name", ErrInvalidConfig))
	}
	if cfg.Timeout < 0 {
		problems = append(problems, fmt.Errorf("%w: negative timeout", ErrInvalidConfig))
	}
	s := &Source{name: cfg.Name, client: cfg.Client, timeout: cfg.Timeout, headers: make(http.Header, len(cfg.Headers))}
	if s.client == nil {
		s.client = ssrf.New(nil)
	}
	for name, vals := range cfg.Headers {
		if err := checkHeaderName(name); err != nil {
			problems = append(problems, err)
			continue
		}
		for _, v := range vals {
			if strings.ContainsAny(v, "\r\n\x00") {
				problems = append(problems, fmt.Errorf("%w: header %q: control character in value", ErrInvalidConfig, name))
				continue
			}
			s.headers.Add(name, v)
		}
	}
	for _, name := range cfg.Passthrough {
		if err := checkHeaderName(name); err != nil {
			problems = append(problems, err)
			continue
		}
		s.passthrough = append(s.passthrough, http.CanonicalHeaderKey(name))
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}
	return s, nil
}

// checkHeaderName rejects non-token names and headers we must control.
func checkHeaderName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty header name", ErrInvalidConfig)
	}
	for i := range len(name) {
		if !isTokenChar(name[i]) {
			return fmt.Errorf("%w: header name %q is not a token", ErrInvalidConfig, name)
		}
	}
	switch http.CanonicalHeaderKey(name) {
	case "Connection", "Keep-Alive", "Proxy-Connection", "Proxy-Authorization", "Proxy-Authenticate",
		"Te", "Trailer", "Transfer-Encoding", "Upgrade", "Host", "Content-Length", "Expect",
		"Accept-Encoding", "Range", "If-Range", "If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since":
		return fmt.Errorf("%w: header %q is managed by the fetcher", ErrInvalidConfig, name)
	default:
		return nil
	}
}

// isTokenChar reports whether c is an RFC 9110 tchar.
func isTokenChar(c byte) bool {
	switch {
	case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		return true
	default:
		return strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
	}
}

// Name returns the configured source name.
func (s *Source) Name() string { return s.name }

// Type returns source.TypeHTTP.
func (s *Source) Type() string { return source.TypeHTTP }

// Fetch GETs key.Path (an absolute http(s) URL). Dev keys (key.Dev) take
// the same path: the SSRF client is their only guard, as for any key.
//
// Results: 200 → Object with a streaming Body the caller must close (it is
// capped at opt.MaxBytes and classifies read errors); 304 on a conditional
// request → Object{NotModified: true} without Body. Errors carry an
// errs.Kind: 404/410 NotFound, 401/403 Forbidden, other statuses
// UpstreamError, Content-Length over opt.MaxBytes SourceTooLarge, deadline
// UpstreamTimeout, redirect refused by the hook or SSRF policy Forbidden.
// The Content-Type is passed through unvalidated: sniffing is the codec's
// job.
func (s *Source) Fetch(ctx context.Context, key source.Key, opt source.FetchOptions) (*source.Object, error) {
	where := source.RedactKey(key)
	u, err := url.Parse(key.Path)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errs.Newf(errs.KindInvalidURL, "httpsrc: %s: not an absolute http(s) url", where)
	}

	cancel := context.CancelFunc(func() {})
	if s.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		cancel()
		return nil, errs.Wrapf(errs.KindInvalidURL, err, "httpsrc: %s: build request", where)
	}
	s.setHeaders(req.Header, &opt)

	resp, err := s.clientFor(opt.CheckRedirect).Do(req)
	if err != nil {
		if resp != nil { // CheckRedirect failures return the (closed) last response
			_ = resp.Body.Close()
		}
		cancel()
		return nil, classify(ctx.Err(), unwrapURLError(err), where)
	}
	obj, err := s.object(ctx, resp, &opt, where, cancel)
	if err != nil {
		drainClose(resp.Body)
		cancel()
		return nil, err
	}
	return obj, nil
}

// setHeaders copies the allowlisted passthrough headers, then the static
// ones, then the conditional headers.
func (s *Source) setHeaders(h http.Header, opt *source.FetchOptions) {
	for _, name := range s.passthrough {
		if vals := opt.Header.Values(name); len(vals) > 0 {
			h[name] = append([]string(nil), vals...)
		}
	}
	for name, vals := range s.headers {
		h[name] = append([]string(nil), vals...)
	}
	if opt.IfNoneMatch != "" {
		h.Set("If-None-Match", opt.IfNoneMatch)
	}
	if !opt.IfModifiedSince.IsZero() {
		h.Set("If-Modified-Since", opt.IfModifiedSince.UTC().Format(http.TimeFormat))
	}
}

// clientFor returns a shallow copy of the SSRF client whose redirect check
// runs the SSRF checks first and then hook with the hop's absolute URL
// (http.Client has already resolved a relative Location against the
// previous hop). A nil hook refuses redirects.
func (s *Source) clientFor(hook func(location string) error) *http.Client {
	c := *s.client
	base := s.client.CheckRedirect
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if base != nil {
			if err := base(req, via); err != nil {
				return err
			}
		}
		if hook == nil {
			return errs.Wrapf(errs.KindForbidden, ErrRedirectRefused, "redirect to %s", req.URL.Hostname())
		}
		if err := hook(req.URL.String()); err != nil {
			if _, ok := errors.AsType[*errs.Error](err); ok {
				return err
			}
			return errs.Wrapf(errs.KindForbidden, err, "redirect to %s", req.URL.Hostname())
		}
		return nil
	}
	return &c
}

// object maps a response to an Object or a classified error. On success the
// returned Body owns resp.Body and cancel.
func (s *Source) object(ctx context.Context, resp *http.Response, opt *source.FetchOptions, where string, cancel context.CancelFunc) (*source.Object, error) {
	conditional := opt.IfNoneMatch != "" || !opt.IfModifiedSince.IsZero()
	switch code := resp.StatusCode; {
	case code == http.StatusOK:
	case code == http.StatusNotModified && conditional:
		obj := meta(resp)
		obj.NotModified, obj.Size = true, -1
		drainClose(resp.Body)
		cancel()
		return obj, nil
	case code == http.StatusNotFound || code == http.StatusGone:
		return nil, errs.Wrapf(errs.KindNotFound, ErrStatus, "httpsrc: %s: status %d", where, code)
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return nil, errs.Wrapf(errs.KindForbidden, ErrStatus, "httpsrc: %s: status %d", where, code)
	default:
		return nil, errs.Wrapf(errs.KindUpstreamError, ErrStatus, "httpsrc: %s: status %d", where, code)
	}
	if err := ssrf.LimitBody(resp, opt.MaxBytes); err != nil {
		return nil, errs.Wrapf(errs.KindOf(err), err, "httpsrc: %s", where)
	}
	obj := meta(resp)
	obj.Body = &body{rc: resp.Body, ctxErr: ctx.Err, cancel: cancel, where: where}
	return obj, nil
}

// meta copies the cache metadata of resp.
func meta(resp *http.Response) *source.Object {
	h := resp.Header
	obj := &source.Object{
		Size:         resp.ContentLength,
		ContentType:  h.Get("Content-Type"),
		ETag:         h.Get("ETag"),
		CacheControl: h.Get("Cache-Control"),
	}
	if obj.Size < 0 {
		obj.Size = -1
	}
	if t, err := http.ParseTime(h.Get("Last-Modified")); err == nil {
		obj.LastModified = t
	}
	if t, err := http.ParseTime(h.Get("Expires")); err == nil {
		obj.Expires = t
	}
	return obj
}

// unwrapURLError drops the *url.Error layer, whose text carries the full
// URL (query tokens included); callers log RedactKey instead.
func unwrapURLError(err error) error {
	if ue, ok := errors.AsType[*url.Error](err); ok && ue.Err != nil {
		return ue.Err
	}
	return err
}

// classify gives a transport or body error an errs.Kind. Errors that
// already carry one (SSRF blocks, body limit, redirect hook) keep it.
// ctxErr is the fetch context's error at the time of failure.
func classify(ctxErr, err error, where string) error {
	if _, ok := errors.AsType[*errs.Error](err); ok {
		return errs.Wrapf(errs.KindOf(err), err, "httpsrc: %s", where)
	}
	kind := errs.KindUpstreamError
	switch {
	case errors.Is(ctxErr, context.Canceled):
		kind = errs.KindCanceled
	case errors.Is(ctxErr, context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		kind = errs.KindUpstreamTimeout
	default:
		if ne, ok := errors.AsType[net.Error](err); ok && ne.Timeout() {
			kind = errs.KindUpstreamTimeout
		}
	}
	return errs.Wrapf(kind, err, "httpsrc: %s", where)
}

// maxDrain bounds how much of an unwanted body is read so the connection
// can be reused.
const maxDrain = 64 << 10

func drainClose(rc io.ReadCloser) {
	_, _ = io.CopyN(io.Discard, rc, maxDrain)
	_ = rc.Close()
}

// body is Object.Body: it classifies read errors and releases the
// per-attempt timeout on Close.
type body struct {
	rc     io.ReadCloser
	ctxErr func() error // the fetch context's Err
	cancel context.CancelFunc
	where  string
}

func (b *body) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if err != nil && err != io.EOF { //nolint:errorlint // io.Reader contract: EOF is returned unwrapped
		err = classify(b.ctxErr(), err, b.where)
	}
	return n, err
}

func (b *body) Close() error {
	err := b.rc.Close()
	b.cancel()
	return err
}
