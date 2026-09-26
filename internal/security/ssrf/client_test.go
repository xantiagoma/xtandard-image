package ssrf

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// fakeResolver answers each host with a sequence of responses; the last one
// repeats. Unknown hosts get a not-found DNS error.
type fakeResolver struct {
	mu      sync.Mutex
	answers map[string][][]netip.Addr
	calls   map[string]int
	err     error
}

func newResolver(answers map[string][]string) *fakeResolver {
	r := &fakeResolver{answers: map[string][][]netip.Addr{}, calls: map[string]int{}}
	for host, seq := range answers {
		for _, s := range seq {
			var addrs []netip.Addr
			if s != "" {
				addrs = append(addrs, netip.MustParseAddr(s))
			}
			r.answers[host] = append(r.answers[host], addrs)
		}
	}
	return r
}

// seq sets host to answer the given address sets in order.
func (r *fakeResolver) seq(host string, answers ...[]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.answers[host] = nil
	for _, set := range answers {
		addrs := make([]netip.Addr, 0, len(set))
		for _, s := range set {
			addrs = append(addrs, netip.MustParseAddr(s))
		}
		r.answers[host] = append(r.answers[host], addrs)
	}
}

func (r *fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	seq, ok := r.answers[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	i := min(r.calls[host], len(seq)-1)
	r.calls[host]++
	return slices.Clone(seq[i]), nil
}

func (r *fakeResolver) count(host string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[host]
}

func serverAddr(t *testing.T, srv *httptest.Server) netip.AddrPort {
	t.Helper()
	ap, err := netip.ParseAddrPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return ap
}

// loopbackPolicy permits 127.0.0.1 on the given servers' ports only, the
// way an operator would allowlist an in-cluster origin.
func loopbackPolicy(t *testing.T, srvs ...*httptest.Server) Policy {
	t.Helper()
	p := Policy{AllowCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}}
	for _, s := range srvs {
		p.Ports = append(p.Ports, serverAddr(t, s).Port())
	}
	return p
}

func get(ctx context.Context, t *testing.T, c *http.Client, rawURL string, hdr http.Header) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	maps.Copy(req.Header, hdr)
	return c.Do(req)
}

// fetch GETs rawURL and returns status and body, or the error.
func fetch(t *testing.T, c *http.Client, rawURL string) (int, string, error) {
	t.Helper()
	resp, err := get(t.Context(), t, c, rawURL, nil)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), err
}

func wantKind(t *testing.T, err error, k errs.Kind, cause error) {
	t.Helper()
	if err == nil {
		t.Fatalf("got nil error, want kind %s", k)
	}
	if got := errs.KindOf(err); got != k {
		t.Fatalf("KindOf(%v) = %s, want %s", err, got, k)
	}
	if cause != nil && !errors.Is(err, cause) {
		t.Fatalf("error %v does not wrap %v", err, cause)
	}
}

func okServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestClientBlocksDestinations(t *testing.T) {
	t.Parallel()
	srv := okServer(t)
	port := serverAddr(t, srv).Port()

	res := newResolver(map[string][]string{
		"loopback.test": {"127.0.0.1"},
		"metadata.test": {"169.254.169.254"},
		"private.test":  {"10.0.0.1"},
		"mapped.test":   {"::ffff:127.0.0.1"},
		"nat64.test":    {"64:ff9b::7f00:1"},
		"ula.test":      {"fd00::1"},
		"cgnat.test":    {"100.64.0.1"},
		"empty.test":    {""},
	})
	res.seq("mixed.test", []string{"198.51.100.7", "127.0.0.1"})

	var dials atomic.Int32
	c := New(&Options{
		Resolver: res,
		Policy:   Policy{Ports: []uint16{80, 443, port}},
		testDialSocket: func(context.Context, string, string) (net.Conn, error) {
			dials.Add(1)
			return nil, errors.New("unexpected dial")
		},
	})

	tests := []struct {
		name  string
		url   string
		kind  errs.Kind
		cause error
	}{
		{"loopback literal", srv.URL, errs.KindForbidden, ErrBlockedAddress},
		{"loopback name", "http://loopback.test:" + itoa(port), errs.KindForbidden, ErrBlockedAddress},
		{"metadata literal", "http://169.254.169.254/latest/meta-data/", errs.KindForbidden, ErrBlockedAddress},
		{"metadata name", "http://metadata.test/", errs.KindForbidden, ErrBlockedAddress},
		{"private name", "http://private.test/", errs.KindForbidden, ErrBlockedAddress},
		{"ipv4-mapped answer", "http://mapped.test/", errs.KindForbidden, ErrBlockedAddress},
		{"ipv4-mapped literal", "http://[::ffff:127.0.0.1]/", errs.KindForbidden, ErrBlockedAddress},
		{"nat64 answer", "http://nat64.test/", errs.KindForbidden, ErrBlockedAddress},
		{"ula answer", "http://ula.test/", errs.KindForbidden, ErrBlockedAddress},
		{"cgnat answer", "http://cgnat.test/", errs.KindForbidden, ErrBlockedAddress},
		{"unspecified literal", "http://0.0.0.0/", errs.KindForbidden, ErrBlockedAddress},
		{"mixed answer public+loopback", "http://mixed.test/", errs.KindForbidden, ErrBlockedAddress},
		{"port not allowlisted", "http://public.example:8080/", errs.KindForbidden, ErrBlockedPort},
		{"bad port", "http://public.example:99999/", errs.KindForbidden, ErrBlockedPort},
		{"scheme", "ftp://public.example/", errs.KindForbidden, ErrBlockedScheme},
		{"nxdomain", "http://nope.test/", errs.KindUpstreamError, nil},
		{"empty answer", "http://empty.test/", errs.KindUpstreamError, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := fetch(t, c, tc.url)
			wantKind(t, err, tc.kind, tc.cause)
		})
	}
	t.Cleanup(func() {
		if n := dials.Load(); n != 0 {
			t.Errorf("socket dialed %d times; every case must fail before dialing", n)
		}
	})
}

func itoa(p uint16) string { return strconv.Itoa(int(p)) }

func TestClientAllowlistedFetch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		opts   func(srv *httptest.Server) Options
		url    func(srv *httptest.Server) string
		hdr    http.Header
		wantUA string
	}{
		{
			"literal default ua",
			func(srv *httptest.Server) Options { return Options{Policy: loopbackPolicy(t, srv)} },
			func(srv *httptest.Server) string { return srv.URL },
			nil, DefaultUserAgent,
		},
		{
			"name custom ua",
			func(srv *httptest.Server) Options {
				res := newResolver(map[string][]string{"origin.internal": {"127.0.0.1"}})
				return Options{Policy: loopbackPolicy(t, srv), Resolver: res, UserAgent: "xi/1"}
			},
			func(srv *httptest.Server) string { return "http://origin.internal:" + itoa(serverAddr(t, srv).Port()) },
			nil, "xi/1",
		},
		{
			"caller ua kept",
			func(srv *httptest.Server) Options { return Options{Policy: loopbackPolicy(t, srv)} },
			func(srv *httptest.Server) string { return srv.URL },
			http.Header{"User-Agent": {"mine"}},
			"mine",
		},
		{
			"allow loopback class",
			func(srv *httptest.Server) Options {
				return Options{Policy: Policy{Allow: ClassLoopback, Ports: loopbackPolicy(t, srv).Ports}}
			},
			func(srv *httptest.Server) string { return srv.URL },
			nil, DefaultUserAgent,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var ua atomic.Value
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ua.Store(r.UserAgent())
				_, _ = io.WriteString(w, "ok")
			}))
			t.Cleanup(srv.Close)
			opts := tc.opts(srv)
			resp, err := get(t.Context(), t, New(&opts), tc.url(srv), tc.hdr)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d", resp.StatusCode)
			}
			if got := ua.Load(); got != tc.wantUA {
				t.Errorf("User-Agent = %v, want %q", got, tc.wantUA)
			}
		})
	}
}

// TestDNSRebinding: the resolver answers a public IP first, then 127.0.0.1.
// The first fetch reaches the "public" host; the second is refused before
// any socket is opened, because every dial re-resolves and re-validates.
func TestDNSRebinding(t *testing.T) {
	t.Parallel()
	srv := okServer(t)
	res := newResolver(nil)
	res.seq("rebind.test", []string{"198.51.100.7"}, []string{"127.0.0.1"})

	var mu sync.Mutex
	var dialed []string
	var plain net.Dialer
	c := New(&Options{
		Resolver: res,
		testDialSocket: func(ctx context.Context, network, address string) (net.Conn, error) {
			mu.Lock()
			dialed = append(dialed, address)
			mu.Unlock()
			if address != "198.51.100.7:80" {
				return nil, errors.New("unexpected dial to " + address)
			}
			// Stand in for the public host with the test server.
			return plain.DialContext(ctx, network, srv.Listener.Addr().String())
		},
	})
	status, body, err := fetch(t, c, "http://rebind.test/")
	if err != nil || status != http.StatusOK || body != "ok" {
		t.Fatalf("first fetch = %d %q %v, want 200 ok", status, body, err)
	}
	c.CloseIdleConnections() // force a fresh dial, as a new request would
	_, _, err = fetch(t, c, "http://rebind.test/")
	wantKind(t, err, errs.KindForbidden, ErrBlockedAddress)

	if n := res.count("rebind.test"); n != 2 {
		t.Errorf("resolver called %d times, want 2", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(dialed, []string{"198.51.100.7:80"}) {
		t.Errorf("dialed %v, want only the public address", dialed)
	}
}

// TestConnectTimeCheck: the pre-dial check approves a public IP, but the
// socket ends up connecting to loopback (a resolver/dial mismatch). The
// ControlContext hook sees the real address and refuses before connect.
func TestConnectTimeCheck(t *testing.T) {
	t.Parallel()
	var conns atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "leaked")
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	target := serverAddr(t, srv)

	c := New(&Options{
		Resolver: newResolver(map[string][]string{"evil.test": {"198.51.100.7"}}),
		Policy:   Policy{Ports: []uint16{80, target.Port()}},
		testDialRewrite: func(netip.AddrPort) netip.AddrPort {
			return target
		},
	})
	_, _, err := fetch(t, c, "http://evil.test/")
	wantKind(t, err, errs.KindForbidden, ErrBlockedAddress)
	if n := conns.Load(); n != 0 {
		t.Errorf("server accepted %d connections, want 0", n)
	}
}

func TestControl(t *testing.T) {
	t.Parallel()
	d := &dialer{policy: Policy{Ports: []uint16{80}}}
	tests := []struct {
		addr  string
		cause error
	}{
		{"8.8.8.8:80", nil},
		{"127.0.0.1:80", ErrBlockedAddress},
		{"[::ffff:169.254.169.254]:80", ErrBlockedAddress},
		{"[fe80::1%en0]:80", ErrBlockedAddress},
		{"8.8.8.8:22", ErrBlockedPort},
		{"not-an-address", ErrBlockedAddress},
	}
	for _, tc := range tests {
		err := d.control(t.Context(), "tcp", tc.addr, nil)
		if tc.cause == nil {
			if err != nil {
				t.Errorf("control(%s) = %v, want nil", tc.addr, err)
			}
			continue
		}
		wantKind(t, err, errs.KindForbidden, tc.cause)
	}
}

func TestClientHTTP2(t *testing.T) {
	t.Parallel()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proto := "h1"
		if r.ProtoMajor == 2 {
			proto = "h2"
		}
		_, _ = io.WriteString(w, proto)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)

	c := New(&Options{Policy: loopbackPolicy(t, srv), TLSConfig: trust(srv)})
	status, body, err := fetch(t, c, srv.URL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("fetch = %d %v", status, err)
	}
	if body != "h2" {
		t.Errorf("negotiated %q, want h2", body)
	}
}

func trust(srv *httptest.Server) *tls.Config {
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
}

// blockingServer never writes a response until the client goes away.
func blockingServer(t *testing.T) *httptest.Server {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) }) // runs first: unblocks handlers
	return srv
}

func TestClientTimeouts(t *testing.T) {
	t.Parallel()
	srv := blockingServer(t)

	t.Run("response header timeout", func(t *testing.T) {
		t.Parallel()
		c := New(&Options{Policy: loopbackPolicy(t, srv), ResponseHeaderTimeout: 50 * time.Millisecond})
		_, _, err := fetch(t, c, srv.URL)
		wantKind(t, err, errs.KindUpstreamTimeout, nil)
	})
	t.Run("context deadline", func(t *testing.T) {
		t.Parallel()
		c := New(&Options{Policy: loopbackPolicy(t, srv)})
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		resp, err := get(ctx, t, c, srv.URL, nil)
		if err == nil {
			_ = resp.Body.Close()
		}
		wantKind(t, err, errs.KindUpstreamTimeout, context.DeadlineExceeded)
	})
	t.Run("context canceled", func(t *testing.T) {
		t.Parallel()
		c := New(&Options{Policy: loopbackPolicy(t, srv)})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		resp, err := get(ctx, t, c, srv.URL, nil)
		if err == nil {
			_ = resp.Body.Close()
		}
		wantKind(t, err, errs.KindCanceled, context.Canceled)
	})
	t.Run("dns timeout", func(t *testing.T) {
		t.Parallel()
		res := newResolver(nil)
		res.err = &net.DNSError{Err: "i/o timeout", Name: "slow.test", IsTimeout: true}
		c := New(&Options{Resolver: res})
		_, _, err := fetch(t, c, "http://slow.test/")
		wantKind(t, err, errs.KindUpstreamTimeout, nil)
	})
}

func TestClientConnectionRefused(t *testing.T) {
	t.Parallel()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	ap := netip.MustParseAddrPort(addr)
	c := New(&Options{Policy: Policy{
		AllowCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		Ports:      []uint16{ap.Port()},
	}})
	_, _, err = fetch(t, c, "http://"+addr+"/")
	wantKind(t, err, errs.KindUpstreamError, nil)
}

// TestClientProxy: the proxy endpoint (on loopback, not allowlisted) is
// trusted, but targets are still resolved and checked before the request
// is handed to the proxy.
func TestClientProxy(t *testing.T) {
	t.Parallel()
	var seen atomic.Value
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Store(r.URL.String())
		_, _ = io.WriteString(w, "via-proxy")
	}))
	t.Cleanup(proxy.Close)
	pu, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	c := New(&Options{
		Resolver: newResolver(map[string][]string{
			"public.test":   {"198.51.100.7"},
			"internal.test": {"10.0.0.1"},
		}),
		Proxy: http.ProxyURL(pu),
	})

	status, body, err := fetch(t, c, "http://public.test/img.png")
	if err != nil || status != http.StatusOK || body != "via-proxy" {
		t.Fatalf("proxied fetch = %d %q %v", status, body, err)
	}
	if got := seen.Load(); got != "http://public.test/img.png" {
		t.Errorf("proxy saw %v", got)
	}
	_, _, err = fetch(t, c, "http://internal.test/")
	wantKind(t, err, errs.KindForbidden, ErrBlockedAddress)
}
