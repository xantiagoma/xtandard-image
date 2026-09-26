package imgix_test

import (
	"net/http/httptest"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgix"
	"github.com/xantiagoma/xtandard-image/internal/dialect/router"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// TestRouterMount mounts the dialect the way the app does (host-bound and
// under /imgix/<mount>/) with the restricted unsigned policy: unpic's
// default URLs pass, signed URLs bypass the policy, and costly unsigned
// options are refused.
func TestRouterMount(t *testing.T) {
	t.Parallel()
	f := getFixture(t)
	rt, err := router.New(&router.Options{
		Mounts: []router.Mount{
			{Name: "shop", Dialect: imgix.Name, Prefix: "/imgix/shop/", Hosts: []string{"images.example.com"}, Source: "assets"},
		},
		Policy:       router.PolicyRestricted,
		Restrictions: router.Restrictions{Widths: []int{640, 1080}, Qualities: []int{45, 75}},
	}, map[string]dialect.Dialect{imgix.Name: f.parsers["signed"]})
	if err != nil {
		t.Fatal(err)
	}
	signed, err := f.signer.SignImgixQuery("/a.jpg", "w=333&blur=40")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, host, target string
		kind               errs.Kind // 0 = success
		sig                sign.Result
	}{
		{"unpic default host-bound", "images.example.com", "/a.jpg?w=640&fit=min&auto=format", 0, sign.Unsigned},
		{"unpic default prefix", "xi.internal", "/imgix/shop/a.jpg?w=1080&fit=min&auto=format", 0, sign.Unsigned},
		{"auto compress", "images.example.com", "/a.jpg?w=640&auto=compress", 0, sign.Unsigned},
		{"width not allowlisted", "images.example.com", "/a.jpg?w=333", errs.KindForbidden, 0},
		{"effect unsigned", "images.example.com", "/a.jpg?w=640&blur=40", errs.KindForbidden, 0},
		{"watermark unsigned", "images.example.com", "/a.jpg?w=640&mark=https://evil.example/x.png", errs.KindForbidden, 0},
		{"signed bypasses policy", "images.example.com", "/a.jpg?" + signed, 0, sign.Verified},
		{"bad signature", "images.example.com", "/a.jpg?w=640&s=00000000000000000000000000000000", errs.KindUnauthorized, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequestWithContext(t.Context(), "GET", tc.target, nil)
			r.Host = tc.host
			res, _, err := rt.Route(r)
			if tc.kind != 0 {
				if errs.KindOf(err) != tc.kind {
					t.Fatalf("err = %v, want kind %v", err, tc.kind)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if res.Signature != tc.sig {
				t.Fatalf("signature %v, want %v", res.Signature, tc.sig)
			}
			src := res.Request.Source
			if src.Raw != "a.jpg" || src.Encoding != transform.SourceRelative || src.Mount != "assets" {
				t.Fatalf("source %+v", src)
			}
		})
	}
}
