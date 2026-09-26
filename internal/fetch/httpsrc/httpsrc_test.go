package httpsrc

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/ssrf"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

// newServer starts an httptest server and a Source whose SSRF client allows
// exactly that loopback address and port.
func newServer(t *testing.T, h http.HandlerFunc, mod func(*Config)) (*httptest.Server, *Source) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	ap, err := netip.ParseAddrPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		Name: "origin",
		Client: ssrf.New(&ssrf.Options{Policy: ssrf.Policy{
			AllowCIDRs: []netip.Prefix{netip.PrefixFrom(ap.Addr(), 32)},
			Ports:      []uint16{ap.Port()},
		}}),
	}
	if mod != nil {
		mod(&cfg)
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return srv, s
}

func key(u string) source.Key { return source.Key{Source: "origin", Path: u} }

func wantKind(t *testing.T, err error, k errs.Kind) {
	t.Helper()
	if err == nil {
		t.Fatalf("err = nil, want kind %v", k)
	}
	if got := errs.KindOf(err); got != k {
		t.Fatalf("kind = %v, want %v (err: %v)", got, k, err)
	}
}

func TestFetchOK(t *testing.T) {
	t.Parallel()
	lm := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	exp := lm.Add(time.Hour)
	srv, s := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.RawQuery != "tok=secret" {
			t.Errorf("request %s %s", r.Method, r.URL)
		}
		h := w.Header()
		h.Set("Content-Type", "text/plain") // not an image: passed through, not rejected
		h.Set("ETag", `"v1"`)
		h.Set("Last-Modified", lm.Format(http.TimeFormat))
		h.Set("Cache-Control", "public, max-age=60")
		h.Set("Expires", exp.Format(http.TimeFormat))
		_, _ = io.WriteString(w, "hello")
	}, nil)

	for _, dev := range []bool{false, true} {
		k := key(srv.URL + "/a.jpg?tok=secret")
		if dev {
			k.Source, k.Dev = source.DevSourceName, true
		}
		obj, err := s.Fetch(t.Context(), k, source.FetchOptions{MaxBytes: 5})
		if err != nil {
			t.Fatalf("dev=%v: %v", dev, err)
		}
		b, err := io.ReadAll(obj.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := obj.Body.Close(); err != nil {
			t.Fatal(err)
		}
		obj.Body = nil
		want := &source.Object{Size: 5, ContentType: "text/plain", ETag: `"v1"`, LastModified: lm, CacheControl: "public, max-age=60", Expires: exp}
		if diff := cmp.Diff(want, obj); diff != "" || string(b) != "hello" {
			t.Errorf("dev=%v: body %q, object (-want +got):\n%s", dev, b, diff)
		}
	}
}

func TestFetchConditional(t *testing.T) {
	t.Parallel()
	ims := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
	srv, s := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		inm, since := r.Header.Get("If-None-Match"), r.Header.Get("If-Modified-Since")
		switch {
		case inm == `"v1"` || since == ims.UTC().Format(http.TimeFormat):
			w.WriteHeader(http.StatusNotModified)
		case inm == "" && since == "" && r.URL.Path == "/unasked":
			w.WriteHeader(http.StatusNotModified)
		default:
			_, _ = io.WriteString(w, "fresh")
		}
	}, nil)

	tests := []struct {
		name string
		path string
		opt  source.FetchOptions
		want bool // NotModified
	}{
		{"etag match", "/a", source.FetchOptions{IfNoneMatch: `"v1"`}, true},
		{"etag mismatch", "/a", source.FetchOptions{IfNoneMatch: `"v0"`}, false},
		{"if-modified-since", "/a", source.FetchOptions{IfModifiedSince: ims}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			obj, err := s.Fetch(t.Context(), key(srv.URL+tc.path), tc.opt)
			if err != nil {
				t.Fatal(err)
			}
			if obj.NotModified != tc.want {
				t.Fatalf("NotModified = %v, want %v", obj.NotModified, tc.want)
			}
			if tc.want {
				if obj.Body != nil || obj.ETag != `"v1"` || obj.Size != -1 {
					t.Errorf("304 object = %+v", obj)
				}
				return
			}
			_ = obj.Body.Close()
		})
	}

	t.Run("304 without condition", func(t *testing.T) {
		t.Parallel()
		_, err := s.Fetch(t.Context(), key(srv.URL+"/unasked"), source.FetchOptions{})
		wantKind(t, err, errs.KindUpstreamError)
	})
}

func TestFetchStatus(t *testing.T) {
	t.Parallel()
	srv, s := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		code, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		w.WriteHeader(code)
		_, _ = io.WriteString(w, "error page")
	}, nil)
	tests := []struct {
		code int
		want errs.Kind
	}{
		{http.StatusNotFound, errs.KindNotFound},
		{http.StatusGone, errs.KindNotFound},
		{http.StatusUnauthorized, errs.KindForbidden},
		{http.StatusForbidden, errs.KindForbidden},
		{http.StatusTeapot, errs.KindUpstreamError},
		{http.StatusTooManyRequests, errs.KindUpstreamError},
		{http.StatusPartialContent, errs.KindUpstreamError},
		{http.StatusInternalServerError, errs.KindUpstreamError},
		{http.StatusBadGateway, errs.KindUpstreamError},
		{http.StatusServiceUnavailable, errs.KindUpstreamError},
		{http.StatusGatewayTimeout, errs.KindUpstreamError},
	}
	for _, tc := range tests {
		t.Run(strconv.Itoa(tc.code), func(t *testing.T) {
			t.Parallel()
			obj, err := s.Fetch(t.Context(), key(srv.URL+"/"+strconv.Itoa(tc.code)+"?tok=secret"), source.FetchOptions{})
			if obj != nil {
				t.Fatalf("object %+v on status %d", obj, tc.code)
			}
			wantKind(t, err, tc.want)
			if !errors.Is(err, ErrStatus) || strings.Contains(err.Error(), "secret") {
				t.Errorf("err = %v", err)
			}
		})
	}
}

// hookRecorder records redirect hops and fails those containing "deny".
type hookRecorder struct {
	mu   sync.Mutex
	hops []string
}

var errDenied = errors.New("not allowlisted")

func (h *hookRecorder) check(loc string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hops = append(h.hops, loc)
	if strings.Contains(loc, "deny") {
		return errDenied
	}
	return nil
}

// redirect answers 302 with a raw Location header.
func redirect(w http.ResponseWriter, location string) {
	w.Header().Set("Location", location)
	w.WriteHeader(http.StatusFound)
}

func TestFetchRedirect(t *testing.T) {
	t.Parallel()
	srv, s := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/final":
			_, _ = io.WriteString(w, "final")
		case "/abs":
			redirect(w, "http://"+r.Host+"/final")
		case "/rel":
			w.Header().Set("Location", "sub/../final") // relative to /rel → /final
			w.WriteHeader(http.StatusMovedPermanently)
		case "/deny":
			http.Redirect(w, r, "/deny-target", http.StatusFound)
		case "/meta":
			_, port, _ := strings.Cut(r.Host, ":") // same port: the address, not the port, is what is blocked
			redirect(w, "http://169.254.169.254:"+port+"/latest/meta-data")
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}, nil)

	tests := []struct {
		name     string
		path     string
		nilHook  bool
		wantKind errs.Kind // zero kind means the fetch succeeds
		wantErr  error
		wantHops []string // relative to srv.URL
	}{
		{name: "absolute allowed", path: "/abs", wantHops: []string{"/final"}},
		{name: "relative location", path: "/rel", wantHops: []string{"/final"}},
		{name: "blocked by hook", path: "/deny", wantKind: errs.KindForbidden, wantErr: errDenied, wantHops: []string{"/deny-target"}},
		{name: "blocked by ssrf", path: "/meta", wantKind: errs.KindForbidden, wantErr: ssrf.ErrBlockedAddress},
		{name: "nil hook refuses", path: "/abs", nilHook: true, wantKind: errs.KindForbidden, wantErr: ErrRedirectRefused},
		{
			name: "too many", path: "/loop", wantKind: errs.KindUpstreamError, wantErr: ssrf.ErrTooManyRedirects,
			wantHops: []string{"/loop", "/loop", "/loop", "/loop", "/loop"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var h hookRecorder
			opt := source.FetchOptions{CheckRedirect: h.check}
			if tc.nilHook {
				opt.CheckRedirect = nil
			}
			obj, err := s.Fetch(t.Context(), key(srv.URL+tc.path), opt)
			if tc.wantKind == errs.KindUnknown {
				if err != nil {
					t.Fatal(err)
				}
				b, err := io.ReadAll(obj.Body)
				_ = obj.Body.Close()
				if err != nil || string(b) != "final" {
					t.Fatalf("body %q, err %v", b, err)
				}
			} else {
				wantKind(t, err, tc.wantKind)
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("err = %v, want cause %v", err, tc.wantErr)
				}
			}
			var want []string
			for _, p := range tc.wantHops {
				want = append(want, srv.URL+p)
			}
			if diff := cmp.Diff(want, h.hops); diff != "" {
				t.Errorf("hook hops (-want +got):\n%s", diff)
			}
		})
	}
}

func TestFetchOversize(t *testing.T) {
	t.Parallel()
	payload := strings.Repeat("x", 100)
	srv, s := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/streamed" {
			_ = http.NewResponseController(w).Flush() // headers first: chunked, no Content-Length
		}
		_, _ = io.WriteString(w, payload)
	}, nil)

	t.Run("content-length", func(t *testing.T) {
		t.Parallel()
		obj, err := s.Fetch(t.Context(), key(srv.URL+"/declared"), source.FetchOptions{MaxBytes: 99})
		if obj != nil {
			t.Fatalf("object %+v", obj)
		}
		wantKind(t, err, errs.KindSourceTooLarge)
		if !errors.Is(err, ssrf.ErrBodyTooLarge) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("streamed", func(t *testing.T) {
		t.Parallel()
		obj, err := s.Fetch(t.Context(), key(srv.URL+"/streamed"), source.FetchOptions{MaxBytes: 99})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = obj.Body.Close() }()
		if obj.Size != -1 {
			t.Errorf("Size = %d, want -1", obj.Size)
		}
		_, err = io.ReadAll(obj.Body)
		wantKind(t, err, errs.KindSourceTooLarge)
	})
	t.Run("exactly at limit", func(t *testing.T) {
		t.Parallel()
		for _, p := range []string{"/declared", "/streamed"} {
			obj, err := s.Fetch(t.Context(), key(srv.URL+p), source.FetchOptions{MaxBytes: 100})
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(obj.Body)
			_ = obj.Body.Close()
			if err != nil || len(b) != 100 {
				t.Errorf("%s: read %d bytes, err %v", p, len(b), err)
			}
		}
	})
}

func TestFetchTimeout(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv, s := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stall-body" {
			w.Header().Set("Content-Length", "10")
			_, _ = io.WriteString(w, "12345")
			_ = http.NewResponseController(w).Flush()
		}
		select { // hold the response until the client gives up
		case <-r.Context().Done():
		case <-release:
		}
	}, func(c *Config) { c.Timeout = 50 * time.Millisecond })
	t.Cleanup(func() { close(release) })

	t.Run("headers", func(t *testing.T) {
		t.Parallel()
		_, err := s.Fetch(t.Context(), key(srv.URL+"/stall"), source.FetchOptions{})
		wantKind(t, err, errs.KindUpstreamTimeout)
	})
	t.Run("body", func(t *testing.T) {
		t.Parallel()
		obj, err := s.Fetch(t.Context(), key(srv.URL+"/stall-body"), source.FetchOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = obj.Body.Close() }()
		_, err = io.ReadAll(obj.Body)
		wantKind(t, err, errs.KindUpstreamTimeout)
	})
	t.Run("caller deadline", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()
		_, err := s.Fetch(ctx, key(srv.URL+"/stall"), source.FetchOptions{})
		wantKind(t, err, errs.KindUpstreamTimeout)
	})
	t.Run("caller canceled", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := s.Fetch(ctx, key(srv.URL+"/stall"), source.FetchOptions{})
		wantKind(t, err, errs.KindCanceled)
	})
}

func TestFetchHeaders(t *testing.T) {
	t.Parallel()
	got := make(chan http.Header, 1)
	srv, s := newServer(t, func(_ http.ResponseWriter, r *http.Request) {
		got <- r.Header.Clone()
	}, func(c *Config) {
		c.Passthrough = []string{"accept", "X-Tenant"}
		c.Headers = http.Header{"X-Tenant": {"static"}, "Authorization": {"Bearer origin-token"}}
	})

	in := http.Header{
		"Accept":        {"image/avif,image/webp"},
		"X-Tenant":      {"client"},
		"Authorization": {"Bearer client-token"},
		"Cookie":        {"session=1"},
		"X-Other":       {"1"},
	}
	obj, err := s.Fetch(t.Context(), key(srv.URL+"/a"), source.FetchOptions{Header: in})
	if err != nil {
		t.Fatal(err)
	}
	_ = obj.Body.Close()
	h := <-got
	for name, want := range map[string]string{
		"Accept":        "image/avif,image/webp",
		"X-Tenant":      "static",
		"Authorization": "Bearer origin-token",
		"Cookie":        "",
		"X-Other":       "",
		"User-Agent":    ssrf.DefaultUserAgent,
	} {
		if v := strings.Join(h.Values(name), ","); v != want {
			t.Errorf("%s = %q, want %q", name, v, want)
		}
	}
	if in.Get("X-Tenant") != "client" {
		t.Error("Fetch mutated FetchOptions.Header")
	}
}

func TestFetchInvalidKey(t *testing.T) {
	t.Parallel()
	s, err := New(Config{Name: "origin"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"", "photos/a.jpg", "ftp://h/a", "http://", "http://h/%zz"} {
		_, err := s.Fetch(t.Context(), key(p), source.FetchOptions{})
		wantKind(t, err, errs.KindInvalidURL)
	}
}

func TestFetchDefaultPolicyBlocksLoopback(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("request reached a loopback origin")
	}))
	t.Cleanup(srv.Close)
	s, err := New(Config{Name: source.DevSourceName})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Fetch(t.Context(), source.Key{Source: source.DevSourceName, Path: srv.URL + "/a", Dev: true}, source.FetchOptions{})
	wantKind(t, err, errs.KindForbidden)
}

func TestNew(t *testing.T) {
	t.Parallel()
	s, err := New(Config{Name: "origin"})
	if err != nil || s.Name() != "origin" || s.Type() != source.TypeHTTP {
		t.Fatalf("New = %v, %v", s, err)
	}
	bad := []Config{
		{},
		{Name: "a", Timeout: -time.Second},
		{Name: "a", Headers: http.Header{"Host": {"x"}}},
		{Name: "a", Headers: http.Header{"X-A": {"v\r\nX-B: 1"}}},
		{Name: "a", Headers: http.Header{"Bad Name": {"v"}}},
		{Name: "a", Passthrough: []string{"accept-encoding"}},
		{Name: "a", Passthrough: []string{"If-None-Match"}},
		{Name: "a", Passthrough: []string{""}},
	}
	for _, cfg := range bad {
		if _, err := New(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("New(%+v) err = %v, want ErrInvalidConfig", cfg, err)
		}
	}
}
