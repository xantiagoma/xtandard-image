package ssrf

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// chainServer redirects /hop/N to /hop/N+1 until N == stop, then answers
// "done". stop < 0 redirects forever.
func chainServer(t *testing.T, stop int, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		n, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/hop/"))
		if err != nil {
			http.Error(w, "bad hop", http.StatusBadRequest)
			return
		}
		if n == stop {
			_, _ = io.WriteString(w, "done")
			return
		}
		http.Redirect(w, r, "/hop/"+strconv.Itoa(n+1), http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRedirectChain(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		max      int
		stop     int
		wantErr  bool
		wantHits int32
	}{
		{"three hops under default five", 0, 3, false, 4},
		{"exactly max", 2, 2, false, 3},
		{"too many", 3, -1, true, 4},
		{"default cap is five", 0, -1, true, 6},
		{"negative refuses all", -1, 1, true, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var hits atomic.Int32
			srv := chainServer(t, tc.stop, &hits)
			c := New(&Options{Policy: loopbackPolicy(t, srv), MaxRedirects: tc.max})
			status, body, err := fetch(t, c, srv.URL+"/hop/0")
			if tc.wantErr {
				wantKind(t, err, errs.KindUpstreamError, ErrTooManyRedirects)
			} else if err != nil || status != http.StatusOK || body != "done" {
				t.Fatalf("fetch = %d %q %v, want 200 done", status, body, err)
			}
			if got := hits.Load(); got != tc.wantHits {
				t.Errorf("server hits = %d, want %d", got, tc.wantHits)
			}
		})
	}
}

func redirectTo(t *testing.T, target string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.RedirectHandler(target, http.StatusFound))
	t.Cleanup(srv.Close)
	return srv
}

func TestRedirectDowngrade(t *testing.T) {
	t.Parallel()
	plain := okServer(t)
	secure := httptest.NewTLSServer(http.RedirectHandler(plain.URL+"/x", http.StatusMovedPermanently))
	t.Cleanup(secure.Close)
	policy := loopbackPolicy(t, plain, secure)

	t.Run("blocked by default", func(t *testing.T) {
		t.Parallel()
		c := New(&Options{Policy: policy, TLSConfig: trust(secure)})
		_, _, err := fetch(t, c, secure.URL)
		wantKind(t, err, errs.KindForbidden, ErrDowngrade)
	})
	t.Run("allowed when configured", func(t *testing.T) {
		t.Parallel()
		c := New(&Options{Policy: policy, TLSConfig: trust(secure), AllowHTTPSDowngrade: true})
		status, body, err := fetch(t, c, secure.URL)
		if err != nil || status != http.StatusOK || body != "ok" {
			t.Fatalf("fetch = %d %q %v, want 200 ok", status, body, err)
		}
	})
	t.Run("http to https upgrade allowed", func(t *testing.T) {
		t.Parallel()
		final := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "secure")
		}))
		t.Cleanup(final.Close)
		up := redirectTo(t, final.URL)
		c := New(&Options{Policy: loopbackPolicy(t, up, final), TLSConfig: trust(final)})
		status, body, err := fetch(t, c, up.URL)
		if err != nil || status != http.StatusOK || body != "secure" {
			t.Fatalf("fetch = %d %q %v, want 200 secure", status, body, err)
		}
	})
}

// TestRedirectRevalidated: every hop goes through the same address, port
// and scheme checks as the first request.
func TestRedirectRevalidated(t *testing.T) {
	t.Parallel()
	res := newResolver(map[string][]string{"internal.test": {"10.0.0.1"}})
	tests := []struct {
		name   string
		target string
		cause  error
	}{
		{"to metadata literal", "http://169.254.169.254/latest/meta-data/", ErrBlockedAddress},
		{"to mapped private literal", "http://[::ffff:10.0.0.1]/", ErrBlockedAddress},
		{"to name resolving private", "http://internal.test/", ErrBlockedAddress},
		{"to blocked port", "http://example.com:22/", ErrBlockedPort},
		{"to other scheme", "file:///etc/passwd", ErrBlockedScheme},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := redirectTo(t, tc.target)
			policy := loopbackPolicy(t, srv)
			policy.Ports = append(policy.Ports, 80, 443)
			c := New(&Options{Policy: policy, Resolver: res})
			_, _, err := fetch(t, c, srv.URL)
			wantKind(t, err, errs.KindForbidden, tc.cause)
		})
	}
}

func TestRedirectHook(t *testing.T) {
	t.Parallel()
	final := okServer(t)
	srv := redirectTo(t, final.URL+"/elsewhere")
	errNotAllowed := errors.New("not in source allowlist")
	var seen atomic.Value
	c := New(&Options{
		Policy: loopbackPolicy(t, srv, final),
		CheckRedirect: func(req *http.Request) error {
			seen.Store(req.URL.Path)
			return errs.Wrap(errs.KindForbidden, errNotAllowed)
		},
	})
	_, _, err := fetch(t, c, srv.URL)
	wantKind(t, err, errs.KindForbidden, errNotAllowed)
	if got := seen.Load(); got != "/elsewhere" {
		t.Errorf("hook saw %v, want /elsewhere", got)
	}
}

// TestRedirectCredentials: credentials survive a same-host redirect but are
// stripped as soon as a hop changes host, including to a subdomain or to a
// different port, and stay stripped if the chain comes back.
func TestRedirectCredentials(t *testing.T) {
	t.Parallel()
	type seenHeaders struct{ auth, cookie, proxyAuth string }
	via := func(base, next string) string { return base + "/?next=" + url.QueryEscape(next) }
	// Each case gets its own servers; start builds the URL from their ports.
	tests := []struct {
		name  string
		start func(a, sub, b, aOther string) string
		keep  bool
	}{
		{"same host keeps", func(a, _, _, _ string) string { return via(a, "/final") }, true},
		{"cross host strips", func(a, _, b, _ string) string { return via(a, b+"/final") }, false},
		{"subdomain strips", func(a, sub, _, _ string) string { return via(a, sub+"/final") }, false},
		{"other port and back strips", func(a, _, _, o string) string { return via(a, via(o, a+"/final")) }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var last atomic.Pointer[seenHeaders]
			bounce := func(w http.ResponseWriter, r *http.Request) bool {
				next := r.URL.Query().Get("next")
				if next == "" {
					return false
				}
				http.Redirect(w, r, next, http.StatusFound) //nolint:gosec // G710: test origin redirects where the test says
				return true
			}
			final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if bounce(w, r) {
					return
				}
				last.Store(&seenHeaders{r.Header.Get("Authorization"), r.Header.Get("Cookie"), r.Header.Get("Proxy-Authorization")})
				_, _ = io.WriteString(w, "ok")
			}))
			t.Cleanup(final.Close)
			other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { bounce(w, r) }))
			t.Cleanup(other.Close)
			fport, oport := itoa(serverAddr(t, final).Port()), itoa(serverAddr(t, other).Port())

			res := newResolver(map[string][]string{
				"a.test":     {"127.0.0.1"},
				"sub.a.test": {"127.0.0.1"},
				"b.test":     {"127.0.0.1"},
			})
			c := New(&Options{Policy: loopbackPolicy(t, final, other), Resolver: res})
			hdr := http.Header{
				"Authorization":       {"Bearer secret"},
				"Cookie":              {"session=secret"},
				"Proxy-Authorization": {"Basic c2VjcmV0"},
			}
			start := tc.start("http://a.test:"+fport, "http://sub.a.test:"+fport, "http://b.test:"+fport, "http://a.test:"+oport)
			resp, err := get(t.Context(), t, c, start, hdr)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			got := last.Load()
			if got == nil {
				t.Fatal("final handler not reached")
			}
			want := seenHeaders{}
			if tc.keep {
				want = seenHeaders{"Bearer secret", "session=secret", "Basic c2VjcmV0"}
			}
			if *got != want {
				t.Errorf("final hop saw %+v, want %+v", *got, want)
			}
		})
	}
}
