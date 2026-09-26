package router_test

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/router"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// precedenceOptions mirrors the default mount table plus host bindings.
func precedenceOptions() *router.Options {
	return &router.Options{
		Policy: router.PolicyAllow,
		Mounts: []router.Mount{
			{Name: "imgproxy", Dialect: "imgproxy", Prefix: "/"},
			{Name: "query", Dialect: "query", Prefix: "/q/"},
			{Name: "wsrv", Dialect: "wsrv", Prefix: "/wsrv/"},
			{Name: "imgix", Dialect: "imgix", Prefix: "/imgix/", Hosts: []string{"images.example.com"}},
			{Name: "imgix-shop", Dialect: "imgix", Prefix: "/imgix/shop/", Source: "shop"},
			{Name: "cloudinary", Dialect: "cloudinary", Hosts: []string{"*.cdn.example.com"}},
			{Name: "bunny", Dialect: "bunny", Hosts: []string{"*.example.com"}},
			{Name: "thumbor", Dialect: "thumbor", Prefix: "/thumbor"},
		},
		Default: router.Mount{Dialect: "imgproxy"},
		RootURL: &router.Mount{Name: "wsrv-root", Dialect: "wsrv"},
	}
}

func TestRoutePrecedence(t *testing.T) {
	t.Parallel()
	type want struct {
		Mount, Dialect, Path, Prefix string
	}
	tests := []struct {
		name, host, uri string
		want            want
	}{
		{"exact host beats prefix", "images.example.com", "/q/a.jpg?w=1", want{"imgix", "imgix", "/q/a.jpg", ""}},
		{"exact host beats root url rule", "images.example.com", "/?url=x.com/a.jpg", want{"imgix", "imgix", "/", ""}},
		{"exact host with port and case", "IMAGES.Example.com.:8080", "/a.jpg", want{"imgix", "imgix", "/a.jpg", ""}},
		{"exact host beats wildcard", "images.example.com", "/a", want{"imgix", "imgix", "/a", ""}},
		{"longest wildcard wins", "eu.cdn.example.com", "/a", want{"cloudinary", "cloudinary", "/a", ""}},
		{"wildcard any depth", "a.b.example.com", "/a", want{"bunny", "bunny", "/a", ""}},
		{"wildcard never matches apex", "example.com", "/a", want{"imgproxy", "imgproxy", "/a", ""}},
		{"host-bound mount prefix on other host", "xi.internal", "/imgix/a.jpg", want{"imgix", "imgix", "/a.jpg", "/imgix"}},
		{"root url rule beats imgproxy root", "xi.internal", "/?url=example.com/a.jpg&w=300", want{"wsrv-root", "wsrv", "/", ""}},
		{"root url rule with empty url value", "xi.internal", "/?url=", want{"wsrv-root", "wsrv", "/", ""}},
		{"no url key falls back to imgproxy", "xi.internal", "/?w=300", want{"imgproxy", "imgproxy", "/", ""}},
		{"url key off root is imgproxy", "xi.internal", "/unsafe/x?url=a", want{"imgproxy", "imgproxy", "/unsafe/x", ""}},
		{"wsrv prefix", "xi.internal", "/wsrv/?url=a", want{"wsrv", "wsrv", "/", "/wsrv"}},
		{"longest prefix wins", "xi.internal", "/imgix/shop/a.jpg", want{"imgix-shop", "imgix", "/a.jpg", "/imgix/shop"}},
		{"segment boundary", "xi.internal", "/imgix/shopx/a.jpg", want{"imgix", "imgix", "/shopx/a.jpg", "/imgix"}},
		{"prefix without trailing slash", "xi.internal", "/q", want{"query", "query", "/", "/q"}},
		{"prefix with trailing slash", "xi.internal", "/q/", want{"query", "query", "/", "/q"}},
		{"prefix double slash kept", "xi.internal", "/q//a", want{"query", "query", "//a", "/q"}},
		{"prefix is not a string prefix", "xi.internal", "/qx/a", want{"imgproxy", "imgproxy", "/qx/a", ""}},
		{"mount prefix configured without slash", "xi.internal", "/thumbor/unsafe/a", want{"thumbor", "thumbor", "/unsafe/a", "/thumbor"}},
		{"escaped path is kept escaped", "xi.internal", "/q/a%2Fb", want{"query", "query", "/a%2Fb", "/q"}},
		{"root prefix", "", "/unsafe/plain/a", want{"imgproxy", "imgproxy", "/unsafe/plain/a", ""}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fs, ds := fakes("imgproxy", "query", "wsrv", "imgix", "cloudinary", "bunny", "thumbor")
			rt := newRouter(t, precedenceOptions(), ds)
			res, m, err := rt.Route(request(t, tc.host, tc.uri))
			if err != nil {
				t.Fatalf("Route: %v", err)
			}
			in := fs[m.Dialect].last
			got := want{m.Name, res.Request.Dialect, in.Path, in.Mount.Prefix}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("route (-want +got):\n%s", diff)
			}
			if in.Mount.Name != m.Name || in.Host != tc.host {
				t.Errorf("input mount %q host %q, want %q %q", in.Mount.Name, in.Host, m.Name, tc.host)
			}
		})
	}
}

func TestRouteDefaultMount(t *testing.T) {
	t.Parallel()
	fs, ds := fakes("imgproxy", "query")
	rt := newRouter(t, &router.Options{
		Policy:  router.PolicyAllow,
		Mounts:  []router.Mount{{Name: "query", Dialect: "query", Prefix: "/q/"}},
		Default: router.Mount{Dialect: "imgproxy", Source: "origin"},
	}, ds)
	for _, uri := range []string{"/a/b", "/", "/?url=x"} {
		_, m, err := rt.Route(request(t, "h", uri))
		if err != nil {
			t.Fatalf("Route(%q): %v", uri, err)
		}
		if m.Dialect != "imgproxy" || m.Name != "" || m.Policy != router.PolicyAllow {
			t.Errorf("Route(%q) mount = %+v, want default imgproxy", uri, m)
		}
		if got := fs["imgproxy"].last.Mount; got != (dialect.Mount{Source: "origin"}) {
			t.Errorf("Route(%q) input mount = %+v", uri, got)
		}
	}
	// The empty path of a request like "GET http://h" is "/".
	r := &http.Request{Host: "h", URL: &url.URL{}}
	if _, _, err := rt.Route(r); err != nil || fs["imgproxy"].last.Path != "/" {
		t.Errorf("empty path: err %v, path %q", err, fs["imgproxy"].last.Path)
	}
}

func TestRouteNoRoute(t *testing.T) {
	t.Parallel()
	_, ds := fakes("query")
	rt := newRouter(t, &router.Options{Mounts: []router.Mount{{Dialect: "query", Prefix: "/q/"}}}, ds)
	_, m, err := rt.Route(request(t, "h", "/a"))
	if m != nil || !errors.Is(err, router.ErrNoRoute) || errs.KindOf(err) != errs.KindNotFound {
		t.Fatalf("Route = %v, %v; want 404 no route", m, err)
	}
	if _, _, err := rt.Route(nil); errs.KindOf(err) != errs.KindInvalidURL {
		t.Errorf("Route(nil) = %v, want invalid url", err)
	}
}

func TestRouteSourceBinding(t *testing.T) {
	t.Parallel()
	fs, ds := fakes("imgix", "cloudinary")
	fs["cloudinary"].req = func() *transform.Request {
		return &transform.Request{Source: transform.SourceSpec{Raw: "a.jpg", Mount: "cloud-src"}}
	}
	rt := newRouter(t, &router.Options{
		Policy: router.PolicyAllow,
		Mounts: []router.Mount{
			{Name: "shop", Dialect: "imgix", Hosts: []string{"shop.example.com"}, Source: "assets"},
			{Name: "blog", Dialect: "imgix", Prefix: "/blog/"},
			{Name: "cld", Dialect: "cloudinary", Prefix: "/cld/", Source: "assets"},
		},
	}, ds)
	tests := []struct {
		name, host, uri          string
		wantInput, wantReqSource string
	}{
		{"host mount binds source", "shop.example.com", "/a.jpg", "assets", "assets"},
		{"mount without source", "other", "/blog/a.jpg", "", ""},
		{"dialect choice is kept", "other", "/cld/a.jpg", "assets", "cloud-src"},
	}
	for _, tc := range tests {
		// Not parallel: the subtests share the fakes.
		res, m, err := rt.Route(request(t, tc.host, tc.uri))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := fs[m.Dialect].last.Mount.Source; got != tc.wantInput {
			t.Errorf("%s: Input.Mount.Source = %q, want %q", tc.name, got, tc.wantInput)
		}
		if got := res.Request.Source.Mount; got != tc.wantReqSource {
			t.Errorf("%s: Request.Source.Mount = %q, want %q", tc.name, got, tc.wantReqSource)
		}
	}
}

func TestRouteParseError(t *testing.T) {
	t.Parallel()
	ds := map[string]dialect.Dialect{"x": errDialect{}}
	rt := newRouter(t, &router.Options{Default: router.Mount{Name: "d", Dialect: "x"}}, ds)
	_, m, err := rt.Route(request(t, "h", "/a"))
	if errs.KindOf(err) != errs.KindInvalidURL || m == nil || m.Name != "d" {
		t.Fatalf("Route = %v, %v; want the dialect's 400 and the mount", m, err)
	}
}

type errDialect struct{}

func (errDialect) Name() string            { return "x" }
func (errDialect) Verifier() sign.Verifier { return nil }

func (errDialect) Parse(*dialect.Input) (*dialect.Result, error) {
	return nil, dialect.InvalidURL("bad")
}

func TestNewErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		opts router.Options
		want string
	}{
		{"unknown dialect", router.Options{Mounts: []router.Mount{{Dialect: "nope", Prefix: "/"}}}, "not available"},
		{"unknown default dialect", router.Options{Default: router.Mount{Dialect: "nope"}}, "not available"},
		{"unknown root url dialect", router.Options{RootURL: &router.Mount{Dialect: "nope"}}, "not available"},
		{"no prefix or hosts", router.Options{Mounts: []router.Mount{{Dialect: "a"}}}, "needs a prefix"},
		{"relative prefix", router.Options{Mounts: []router.Mount{{Dialect: "a", Prefix: "q/"}}}, "must start with /"},
		{"duplicate prefix", router.Options{Mounts: []router.Mount{{Dialect: "a", Prefix: "/q/"}, {Dialect: "b", Prefix: "/q"}}}, "already used"},
		{"duplicate root", router.Options{Mounts: []router.Mount{{Dialect: "a", Prefix: "/"}, {Dialect: "b", Prefix: "//"}}}, "already used"},
		{"duplicate host", router.Options{Mounts: []router.Mount{{Dialect: "a", Hosts: []string{"x.com"}}, {Dialect: "b", Hosts: []string{"X.com:80"}}}}, "bound twice"},
		{"duplicate wildcard", router.Options{Mounts: []router.Mount{{Dialect: "a", Hosts: []string{"*.x.com", "*.X.com"}}}}, "bound twice"},
		{"bad wildcard", router.Options{Mounts: []router.Mount{{Dialect: "a", Hosts: []string{"a.*.com"}}}}, "bad host"},
		{"empty wildcard", router.Options{Mounts: []router.Mount{{Dialect: "a", Hosts: []string{"*."}}}}, "bad host"},
		{"bad global policy", router.Options{Policy: "maybe"}, "unknown unsigned policy"},
		{"bad mount policy", router.Options{Mounts: []router.Mount{{Dialect: "a", Prefix: "/", Policy: "maybe"}}}, "unknown unsigned policy"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, ds := fakes("a", "b")
			_, err := router.New(&tc.opts, ds)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("New error = %v, want %q", err, tc.want)
			}
		})
	}
	if _, err := router.New(nil, nil); err != nil {
		t.Errorf("New(nil, nil) = %v", err)
	}
}
