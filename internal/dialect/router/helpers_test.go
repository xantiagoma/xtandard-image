package router_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/router"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// fakeDialect records the last Input and returns a request built by req.
// Each test builds its own fakes, so no locking is needed.
type fakeDialect struct {
	name string
	sig  sign.Result
	req  func() *transform.Request // nil = one empty pipeline
	last *dialect.Input
}

func (f *fakeDialect) Name() string          { return f.name }
func (*fakeDialect) Verifier() sign.Verifier { return nil }

func (f *fakeDialect) Parse(in *dialect.Input) (*dialect.Result, error) {
	f.last = in
	var r *transform.Request
	if f.req != nil {
		r = f.req()
	} else {
		r = &transform.Request{Pipelines: make([]transform.Pipeline, 1)}
	}
	r.Dialect = f.name
	return &dialect.Result{Request: r, SourceString: r.Source.Raw, Signature: f.sig}, nil
}

// fakes returns one fake per name, keyed by name, and the same map typed
// for router.New.
func fakes(names ...string) (map[string]*fakeDialect, map[string]dialect.Dialect) {
	fs := make(map[string]*fakeDialect, len(names))
	ds := make(map[string]dialect.Dialect, len(names))
	for _, n := range names {
		f := &fakeDialect{name: n}
		fs[n], ds[n] = f, f
	}
	return fs, ds
}

func newRouter(t *testing.T, opts *router.Options, ds map[string]dialect.Dialect) *router.Router {
	t.Helper()
	rt, err := router.New(opts, ds)
	if err != nil {
		t.Fatalf("router.New: %v", err)
	}
	return rt
}

// request builds a GET request for host and a raw request URI
// ("/path?query").
func request(t *testing.T, host, uri string) *http.Request {
	t.Helper()
	u, err := url.ParseRequestURI(uri)
	if err != nil {
		t.Fatalf("parse %q: %v", uri, err)
	}
	return &http.Request{Method: http.MethodGet, Host: host, URL: u, Header: http.Header{}}
}
