package ssrf

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// Defaults for zero Options fields.
const (
	DefaultMaxRedirects          = 5
	DefaultDialTimeout           = 5 * time.Second
	DefaultTLSHandshakeTimeout   = 5 * time.Second
	DefaultResponseHeaderTimeout = 10 * time.Second
	DefaultIdleConnTimeout       = 90 * time.Second
	DefaultMaxIdleConnsPerHost   = 16
	DefaultUserAgent             = "xtandard-image"
)

// Resolver looks up the addresses of a host. *net.Resolver satisfies it;
// tests inject fakes.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Options configures New and NewTransport. The zero value is a safe
// default: public addresses only, ports 80/443, 5 redirects, no proxy.
//
// The overall per-request deadline comes from the request context
// (XI_FETCH_TIMEOUT); the fields below bound the individual phases.
type Options struct {
	Policy Policy
	// Resolver defaults to a zero net.Resolver (the system resolver).
	Resolver Resolver

	// MaxRedirects caps followed redirects; 0 means DefaultMaxRedirects and
	// a negative value refuses every redirect.
	MaxRedirects int
	// AllowHTTPSDowngrade permits an https→http redirect hop.
	AllowHTTPSDowngrade bool
	// CheckRedirect, when set, is called for every redirect hop after the
	// SSRF checks, e.g. to re-check the source allowlist.
	CheckRedirect func(req *http.Request) error

	DialTimeout           time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	IdleConnTimeout       time.Duration
	MaxIdleConnsPerHost   int

	// TLSConfig is cloned; MinVersion is raised to TLS 1.2.
	TLSConfig *tls.Config

	// Proxy selects an HTTP proxy per request (http.ProxyFromEnvironment or
	// http.ProxyURL). Nil, the default, ignores proxy environment variables.
	// The proxy endpoint itself is trusted and dialed without the address
	// policy; target hosts are still resolved and checked before the request
	// is handed to the proxy, but the proxy performs its own resolution, so
	// it must enforce egress rules too.
	Proxy func(*http.Request) (*url.URL, error)

	// UserAgent is set on requests that have none; empty means
	// DefaultUserAgent.
	UserAgent string

	// testDialRewrite rewrites the validated address right before the
	// socket dial. Tests use it to simulate a connect-time mismatch.
	testDialRewrite func(netip.AddrPort) netip.AddrPort
	// testDialSocket replaces the ControlContext-guarded socket dial. Tests
	// use it to stand in for a public host.
	testDialSocket func(ctx context.Context, network, address string) (net.Conn, error)
}

// New returns an *http.Client whose transport and redirect policy enforce
// opts (nil means the zero Options). It has no Client.Timeout: bound each
// request with its context.
func New(opts *Options) *http.Client {
	if opts == nil {
		opts = &Options{}
	}
	return &http.Client{
		Transport:     NewTransport(opts),
		CheckRedirect: redirectChecker(opts),
	}
}

// Transport is an http.RoundTripper that enforces the SSRF policy on every
// request it sends, including each redirect hop issued by http.Client.
type Transport struct {
	base      *http.Transport
	dialer    *dialer
	policy    Policy
	proxy     func(*http.Request) (*url.URL, error)
	userAgent string
}

var _ http.RoundTripper = (*Transport)(nil)

// NewTransport builds the SSRF-safe transport (nil opts means the zero
// Options). HTTP/2 is attempted over TLS.
func NewTransport(opts *Options) *Transport {
	if opts == nil {
		opts = &Options{}
	}
	d := &dialer{
		policy:   opts.Policy,
		resolver: opts.Resolver,
		rewrite:  opts.testDialRewrite,
		socket:   opts.testDialSocket,
	}
	if d.resolver == nil {
		d.resolver = &net.Resolver{}
	}
	d.net = net.Dialer{
		Timeout:        orDefault(opts.DialTimeout, DefaultDialTimeout),
		KeepAlive:      30 * time.Second,
		ControlContext: d.control,
	}
	d.plain = net.Dialer{Timeout: d.net.Timeout, KeepAlive: d.net.KeepAlive}
	if d.socket == nil {
		d.socket = d.net.DialContext
	}

	var tlsCfg *tls.Config
	if opts.TLSConfig != nil {
		tlsCfg = opts.TLSConfig.Clone()
	} else {
		tlsCfg = &tls.Config{}
	}
	tlsCfg.MinVersion = max(tlsCfg.MinVersion, tls.VersionTLS12)

	t := &Transport{
		dialer:    d,
		policy:    opts.Policy,
		proxy:     opts.Proxy,
		userAgent: opts.UserAgent,
	}
	if t.userAgent == "" {
		t.userAgent = DefaultUserAgent
	}
	t.base = &http.Transport{
		DialContext:            d.DialContext,
		TLSClientConfig:        tlsCfg,
		ForceAttemptHTTP2:      true,
		TLSHandshakeTimeout:    orDefault(opts.TLSHandshakeTimeout, DefaultTLSHandshakeTimeout),
		ResponseHeaderTimeout:  orDefault(opts.ResponseHeaderTimeout, DefaultResponseHeaderTimeout),
		IdleConnTimeout:        orDefault(opts.IdleConnTimeout, DefaultIdleConnTimeout),
		ExpectContinueTimeout:  time.Second,
		MaxIdleConnsPerHost:    orDefault(opts.MaxIdleConnsPerHost, DefaultMaxIdleConnsPerHost),
		MaxResponseHeaderBytes: 1 << 20,
	}
	if opts.Proxy != nil {
		t.base.Proxy = t.proxyFunc
	}
	return t
}

func orDefault[T time.Duration | int](v, def T) T {
	if v <= 0 {
		return def
	}
	return v
}

// RoundTrip checks the URL, sets the User-Agent and sends req. Transport
// failures come back as *errs.Error.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := checkURL(&t.policy, req.URL); err != nil {
		closeReqBody(req)
		return nil, err
	}
	if req.Header.Get("User-Agent") == "" {
		req = req.Clone(req.Context())
		req.Header.Set("User-Agent", t.userAgent)
	}
	if t.proxy != nil {
		if err := t.precheckProxied(req); err != nil {
			closeReqBody(req)
			return nil, err
		}
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, classify(req.Context(), req.URL.Hostname(), err)
	}
	return resp, nil
}

// CloseIdleConnections closes idle keep-alive connections.
func (t *Transport) CloseIdleConnections() { t.base.CloseIdleConnections() }

// proxyFunc wraps Options.Proxy and records the proxy endpoints it returns
// so the dialer lets them through.
func (t *Transport) proxyFunc(req *http.Request) (*url.URL, error) {
	u, err := t.proxy(req)
	if err != nil || u == nil {
		return u, err
	}
	t.dialer.trusted.Store(hostKey(u), struct{}{})
	return u, nil
}

// precheckProxied resolves and validates the target of a request that will
// go through a proxy, since our dialer never sees the target then.
func (t *Transport) precheckProxied(req *http.Request) error {
	u, err := t.proxy(req)
	if err != nil {
		return errs.Wrapf(errs.KindUpstreamError, err, "proxy for %s", req.URL.Hostname())
	}
	if u == nil {
		return nil
	}
	_, err = t.dialer.resolve(req.Context(), "tcp", req.URL.Hostname())
	return err
}

// checkURL validates scheme, port and (for IP literals) the address.
func checkURL(p *Policy, u *url.URL) error {
	if u == nil {
		return errs.New(errs.KindInvalidURL, "ssrf: nil url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errs.Wrapf(errs.KindForbidden, ErrBlockedScheme, "scheme %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return errs.New(errs.KindInvalidURL, "ssrf: url has no host")
	}
	port, err := urlPort(u)
	if err != nil {
		return errs.Wrapf(errs.KindForbidden, ErrBlockedPort, "port %q", u.Port())
	}
	if err := p.CheckPort(port); err != nil {
		return err
	}
	if a, err := netip.ParseAddr(host); err == nil {
		return p.CheckAddr(a)
	}
	return nil
}

func urlPort(u *url.URL) (uint16, error) {
	s := u.Port()
	if s == "" {
		if u.Scheme == "https" {
			return 443, nil
		}
		return 80, nil
	}
	n, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		return 0, err
	}
	return uint16(n), nil
}

// hostKey is the lowercase host:port of u with the scheme's default port.
func hostKey(u *url.URL) string {
	p, err := urlPort(u)
	if err != nil {
		return strings.ToLower(u.Host)
	}
	return net.JoinHostPort(strings.ToLower(u.Hostname()), strconv.Itoa(int(p)))
}

func closeReqBody(req *http.Request) {
	if req.Body != nil {
		_ = req.Body.Close()
	}
}

// classify maps a transport error to an errs kind, keeping errs errors (our
// own policy blocks) untouched and preserving the cause chain.
func classify(ctx context.Context, host string, err error) error {
	if _, ok := errors.AsType[*errs.Error](err); ok {
		return err
	}
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return errs.Wrapf(errs.KindCanceled, err, "fetch %s", host)
	}
	if isTimeout(err) {
		return errs.Wrapf(errs.KindUpstreamTimeout, err, "fetch %s", host)
	}
	return errs.Wrapf(errs.KindUpstreamError, err, "fetch %s", host)
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.ErrNoProgress) {
		return true
	}
	ne, ok := errors.AsType[net.Error](err)
	return ok && ne.Timeout()
}

// dialer resolves, validates and dials. It is the only path to a socket.
type dialer struct {
	policy   Policy
	resolver Resolver
	net      net.Dialer // with ControlContext = control
	plain    net.Dialer // for trusted proxy endpoints only
	trusted  sync.Map   // hostKey of proxy endpoints → struct{}
	rewrite  func(netip.AddrPort) netip.AddrPort
	socket   func(ctx context.Context, network, address string) (net.Conn, error) // d.net.DialContext
}

// DialContext validates every resolved address of address's host before
// dialing any of them, then dials the validated IP literals in order.
func (d *dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if _, ok := d.trusted.Load(strings.ToLower(address)); ok {
		conn, err := d.plain.DialContext(ctx, network, address)
		if err != nil {
			return nil, classify(ctx, address, err)
		}
		return conn, nil
	}
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errs.Wrapf(errs.KindInvalidURL, err, "ssrf: dial address %q", address)
	}
	port64, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return nil, errs.Wrapf(errs.KindForbidden, ErrBlockedPort, "port %q", portStr)
	}
	port := uint16(port64)
	if err := d.policy.CheckPort(port); err != nil {
		return nil, err
	}
	addrs, err := d.resolve(ctx, network, host)
	if err != nil {
		return nil, err
	}
	var dialErrs []error
	for _, a := range addrs {
		ap := netip.AddrPortFrom(a, port)
		if d.rewrite != nil {
			ap = d.rewrite(ap)
		}
		conn, err := d.socket(ctx, network, ap.String())
		if err == nil {
			return conn, nil
		}
		if _, ok := errors.AsType[*errs.Error](err); ok {
			return nil, err // blocked by control: do not try the rest
		}
		if ctx.Err() != nil {
			return nil, classify(ctx, host, err)
		}
		dialErrs = append(dialErrs, err)
	}
	return nil, classify(ctx, host, errors.Join(dialErrs...))
}

// resolve returns host's addresses, all of which passed the policy. An IP
// literal is returned as is after the check. If any resolved address is
// blocked the whole name is refused, so a mixed answer cannot be used to
// race a private address in.
func (d *dialer) resolve(ctx context.Context, network, host string) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		if err := d.policy.CheckAddr(a); err != nil {
			return nil, err
		}
		return []netip.Addr{a.WithZone("").Unmap()}, nil
	}
	addrs, err := d.resolver.LookupNetIP(ctx, lookupNetwork(network), host)
	if err != nil {
		return nil, classify(ctx, host, err)
	}
	if len(addrs) == 0 {
		return nil, errs.Newf(errs.KindUpstreamError, "resolve %s: no addresses", host)
	}
	out := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		if err := d.policy.CheckAddr(a); err != nil {
			return nil, errs.Wrapf(errs.KindForbidden, err, "resolve %s", host)
		}
		out = append(out, a.WithZone("").Unmap())
	}
	return out, nil
}

func lookupNetwork(network string) string {
	switch network {
	case "tcp4":
		return "ip4"
	case "tcp6":
		return "ip6"
	default:
		return "ip"
	}
}

// control runs right before connect(2) on the actual socket address. It is
// the backstop against anything that changed between resolve and dial.
func (d *dialer) control(_ context.Context, _, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return errs.Wrapf(errs.KindForbidden, ErrBlockedAddress, "unparsable connect address %q", address)
	}
	return d.policy.CheckAddrPort(ap)
}

// redirectChecker returns the http.Client.CheckRedirect for opts.
func redirectChecker(opts *Options) func(req *http.Request, via []*http.Request) error {
	policy := opts.Policy
	allowDowngrade, hook := opts.AllowHTTPSDowngrade, opts.CheckRedirect
	maxHops := opts.MaxRedirects
	switch {
	case maxHops == 0:
		maxHops = DefaultMaxRedirects
	case maxHops < 0:
		maxHops = 0
	}
	return func(req *http.Request, via []*http.Request) error {
		if len(via) > maxHops {
			return errs.Wrapf(errs.KindUpstreamError, ErrTooManyRedirects, "stopped after %d redirects", maxHops)
		}
		prev := via[len(via)-1]
		if prev.URL.Scheme == "https" && req.URL.Scheme == "http" && !allowDowngrade {
			return errs.Wrapf(errs.KindForbidden, ErrDowngrade, "redirect to %s", req.URL.Hostname())
		}
		if err := checkURL(&policy, req.URL); err != nil {
			return err
		}
		origin := hostKey(via[0].URL)
		cross := hostKey(req.URL) != origin
		for _, r := range via[1:] {
			cross = cross || hostKey(r.URL) != origin
		}
		if cross {
			for _, h := range []string{"Authorization", "Proxy-Authorization", "Cookie", "Www-Authenticate"} {
				req.Header.Del(h)
			}
		}
		if hook != nil {
			return hook(req)
		}
		return nil
	}
}
