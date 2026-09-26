package router_test

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/dialect/router"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// FuzzRoute routes arbitrary hosts and request URIs through fakes and the
// real imgproxy dialect. Route must never panic; it returns either a
// result with a mount whose dialect parsed a path starting with "/", or an
// errs error of a client kind.
func FuzzRoute(f *testing.F) {
	for _, s := range [][2]string{
		{"images.example.com", "/a.jpg?w=640"},
		{"IMAGES.example.com.:443", "/"},
		{"a.b.cdn.example.com", "//x"},
		{"[::1]:8080", "/?url=example.com/a.jpg&w=300"},
		{"xi", "/q"},
		{"xi", "/q/"},
		{"xi", "/imgix/shop/a%2Fb.jpg"},
		{"xi", "/unsafe/rs:fit:640:0/plain/https://example.com/a.jpg"},
		{"xi", "/unsafe/rs:fit:641:0/wm:1/plain/https://example.com/a.jpg"},
		{"", ""},
		{"*.example.com", "*"},
		{"xi", "/wsrv?url"},
		{"\x00", "/%zz?%zz"},
	} {
		f.Add(s[0], s[1])
	}
	p, err := imgproxy.New(nil)
	if err != nil {
		f.Fatal(err)
	}
	_, ds := fakes("query", "wsrv", "imgix", "cloudinary", "bunny", "thumbor")
	ds["imgproxy"] = p
	opts := precedenceOptions()
	opts.Policy = router.PolicyRestricted
	opts.Restrictions = restrictions
	rt, err := router.New(opts, ds)
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, host, uri string) {
		u, err := url.ParseRequestURI(uri)
		if err != nil {
			u = &url.URL{Path: uri}
		}
		r := &http.Request{Method: http.MethodGet, Host: host, URL: u, Header: http.Header{}}
		res, m, err := rt.Route(r)
		if err != nil {
			if res != nil {
				t.Fatalf("result with error %v", err)
			}
			if _, ok := errors.AsType[*errs.Error](err); !ok {
				t.Fatalf("error %v is not an *errs.Error", err)
			}
			if k := errs.KindOf(err); k == errs.KindInternal || k == errs.KindUnknown {
				t.Fatalf("error kind %v for a client input: %v", k, err)
			}
			return
		}
		if m == nil || res == nil || res.Request == nil {
			t.Fatalf("Route = %v, %v with nil error", res, m)
		}
		if _, ok := ds[m.Dialect]; !ok {
			t.Fatalf("mount %+v names an unknown dialect", m)
		}
		if f, ok := ds[m.Dialect].(*fakeDialect); ok {
			checkInput(t, f.last)
		}
	})
}

func checkInput(t *testing.T, in *dialect.Input) {
	t.Helper()
	if !strings.HasPrefix(in.Path, "/") {
		t.Fatalf("Input.Path %q does not start with /", in.Path)
	}
	if in.Mount.Prefix != "" && (strings.HasSuffix(in.Mount.Prefix, "/") || !strings.HasPrefix(in.Mount.Prefix, "/")) {
		t.Fatalf("Input.Mount.Prefix %q is not normalised", in.Mount.Prefix)
	}
}
