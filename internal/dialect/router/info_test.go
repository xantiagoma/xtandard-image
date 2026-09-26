package router_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/dialect/router"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// infoDialect is a fakeDialect with an info endpoint; it records which
// entry point ran.
type infoDialect struct {
	fakeDialect
	info bool
}

func (f *infoDialect) Parse(in *dialect.Input) (*dialect.Result, error) {
	f.info = false
	return f.fakeDialect.Parse(in)
}

func (f *infoDialect) ParseInfo(in *dialect.Input) (*dialect.Result, error) {
	res, err := f.fakeDialect.Parse(in)
	f.info = true
	if err == nil {
		res.Request.Kind = transform.KindInfo
	}
	return res, err
}

func TestRouteInfo(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, uri string
		dialect   string // mount that answers
		info      bool
		path      string
	}{
		{"root info", "/info/unsafe/plain/a.jpg", "ip", true, "/unsafe/plain/a.jpg"},
		{"bare info", "/info", "ip", true, "/"},
		{"info with trailing slash", "/info/", "ip", true, "/"},
		{"info under a prefix mount", "/p/info/sig/s:1/plain/a.jpg", "ip-prefixed", true, "/sig/s:1/plain/a.jpg"},
		{"segment boundary", "/infox/unsafe/plain/a.jpg", "ip", false, "/infox/unsafe/plain/a.jpg"},
		{"info deeper in the path is processing", "/unsafe/info/plain/a.jpg", "ip", false, "/unsafe/info/plain/a.jpg"},
		{"dialect without info endpoint", "/q/info/a.jpg", "q", false, "/info/a.jpg"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ip := new(infoDialect)
			ip.name = "ip"
			ipp := new(infoDialect)
			ipp.name = "ip-prefixed"
			q := &fakeDialect{name: "q"}
			rt := newRouter(t, &router.Options{
				Policy: router.PolicyAllow,
				Mounts: []router.Mount{
					{Name: "ip", Dialect: "ip", Prefix: "/"},
					{Name: "ip-prefixed", Dialect: "ip-prefixed", Prefix: "/p/"},
					{Name: "q", Dialect: "q", Prefix: "/q/"},
				},
			}, map[string]dialect.Dialect{"ip": ip, "ip-prefixed": ipp, "q": q})
			res, m, err := rt.Route(request(t, "h", tc.uri))
			if err != nil {
				t.Fatalf("Route: %v", err)
			}
			if m.Dialect != tc.dialect {
				t.Fatalf("mount %q, want %q", m.Dialect, tc.dialect)
			}
			var last *dialect.Input
			gotInfo := false
			switch tc.dialect {
			case "ip":
				last, gotInfo = ip.last, ip.info
			case "ip-prefixed":
				last, gotInfo = ipp.last, ipp.info
			default:
				last = q.last
			}
			if gotInfo != tc.info || last.Path != tc.path {
				t.Errorf("info %v path %q, want %v %q", gotInfo, last.Path, tc.info, tc.path)
			}
			if wantKind := map[bool]transform.Kind{true: transform.KindInfo, false: transform.KindProcess}[tc.info]; res.Request.Kind != wantKind {
				t.Errorf("kind %v, want %v", res.Request.Kind, wantKind)
			}
		})
	}
}

// TestRouteInfoImgproxy routes /info through the real imgproxy dialect:
// the signature covers the path after the signature segment exactly as for
// processing URLs, and the unsigned policy applies unchanged.
func TestRouteInfoImgproxy(t *testing.T) {
	t.Parallel()
	keys, err := sign.ParseHexKeyring([]string{"736563726574"}, []string{"68656C6C6F"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := sign.NewImgproxyVerifier(sign.ImgproxyOptions{Keys: keys, Size: 32})
	if err != nil {
		t.Fatal(err)
	}
	signer, err := sign.NewSigner(v.Scheme(), keys)
	if err != nil {
		t.Fatal(err)
	}
	p, err := imgproxy.New(&imgproxy.Options{Verifier: v})
	if err != nil {
		t.Fatal(err)
	}
	const opts = "/s:0/iptc:0/plain/https://example.com/a.jpg"
	signed, err := signer.SignImgproxyPath(opts)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		policy router.Policy
		uri    string
		kind   errs.Kind // zero: success
	}{
		{"signed", router.PolicyDeny, "/info" + signed, errs.KindUnknown},
		{"signed under a prefix", router.PolicyDeny, "/img/info" + signed, errs.KindUnknown},
		{"bad signature", router.PolicyAllow, "/info/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" + opts, errs.KindUnauthorized},
		{"processing signature is not an info signature", router.PolicyAllow, "/info" + signed + "x", errs.KindUnauthorized},
		{"unsigned allowed", router.PolicyAllow, "/info/unsafe" + opts, errs.KindUnknown},
		{"unsigned restricted", router.PolicyRestricted, "/info/unsafe" + opts, errs.KindForbidden},
		{"unsigned denied", router.PolicyDeny, "/info/unsafe" + opts, errs.KindForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rt := newRouter(t, &router.Options{
				Policy: tc.policy,
				Mounts: []router.Mount{
					{Name: "root", Dialect: "imgproxy", Prefix: "/"},
					{Name: "img", Dialect: "imgproxy", Prefix: "/img/"},
				},
			}, map[string]dialect.Dialect{"imgproxy": p})
			res, _, err := rt.Route(request(t, "h", tc.uri))
			if tc.kind != errs.KindUnknown {
				if errs.KindOf(err) != tc.kind {
					t.Fatalf("Route error %v (kind %v), want kind %v", err, errs.KindOf(err), tc.kind)
				}
				return
			}
			if err != nil {
				t.Fatalf("Route: %v", err)
			}
			want := &transform.InfoOptions{Format: true, Dimensions: true, EXIF: true, XMP: true, VideoMeta: true}
			if res.Request.Kind != transform.KindInfo {
				t.Errorf("kind %v, want info", res.Request.Kind)
			}
			if diff := cmp.Diff(want, res.Request.Info); diff != "" {
				t.Errorf("info options (-want +got):\n%s", diff)
			}
		})
	}
}
