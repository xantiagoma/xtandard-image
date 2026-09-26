package app

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

// imgixToken is the imgix-blueprint secure URL token.
const imgixToken = "FOO123bar"

func imgixSignQuery(t *testing.T, token, path, query string) string {
	t.Helper()
	s, err := sign.NewSigner(sign.NewImgixScheme(), sign.NewKeyring(sign.NewKey([]byte(token), nil)))
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.SignImgixQuery(path, query)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

// TestImgixMount: XI_DIALECTS_IMGIX_* mounts the imgix dialect under its
// prefix and hosts, bound to its source, verifying s with the mount's
// token; unsigned URLs follow the restricted default policy, and an extra
// mount (dialects.imgix.mounts[]) verifies with its own token.
func TestImgixMount(t *testing.T) {
	t.Parallel()
	yml := `dev_mode: true
sources:
  assets:
    type: fs
    root: ` + strconv.Quote(t.TempDir()) + `
dialects:
  imgix:
    enabled: true
    prefix: /imgix/shop/
    hosts: [images.example.com]
    source: assets
    keys: [` + imgixToken + `, rotated-in]
    mounts:
      - prefix: /imgix/other/
        keys: [other-token]
        unsigned_policy: deny
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(config.LoadOptions{ConfigFile: path})
	if err != nil {
		t.Fatal(err)
	}
	rt, warnings, err := buildRouter(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range warnings {
		t.Errorf("warning: %s", w)
	}

	past := strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)
	signed := imgixSignQuery(t, imgixToken, "/a.jpg", "w=333&blur=40")
	for _, tc := range []struct {
		name, host, target string
		mount, source      string
		kind               errs.Kind // 0 = success
		sig                sign.Result
	}{
		{"prefix unsigned allowlisted", "xi.internal", "/imgix/shop/a.jpg?w=640&fit=min&auto=format", "imgix", "assets", 0, sign.Unsigned},
		{"host-bound unsigned", "images.example.com", "/a.jpg?w=1080&auto=format,compress", "imgix", "assets", 0, sign.Unsigned},
		{"host-bound with port", "images.example.com:8080", "/a.jpg?w=640", "imgix", "assets", 0, sign.Unsigned},
		{"restricted width", "images.example.com", "/a.jpg?w=333", "", "", errs.KindForbidden, 0},
		{"restricted effect", "xi.internal", "/imgix/shop/a.jpg?w=640&blur=40", "", "", errs.KindForbidden, 0},
		{"signed bypasses policy", "images.example.com", "/a.jpg?" + signed, "imgix", "assets", 0, sign.Verified},
		{"signed under prefix", "xi.internal", "/imgix/shop/a.jpg?" + signed, "imgix", "assets", 0, sign.Verified},
		{"second token", "xi.internal", "/imgix/shop/a.jpg?" + imgixSignQuery(t, "rotated-in", "/a.jpg", "w=333"), "imgix", "assets", 0, sign.Verified},
		{"signature for another path", "xi.internal", "/imgix/shop/b.jpg?" + signed, "", "", errs.KindUnauthorized, 0},
		{"expired", "xi.internal", "/imgix/shop/a.jpg?" + imgixSignQuery(t, imgixToken, "/a.jpg", "w=640&expires="+past), "", "", errs.KindExpired, 0},
		{"extra mount own token", "xi.internal", "/imgix/other/a.jpg?" + imgixSignQuery(t, "other-token", "/a.jpg", "w=333"), "imgix[1]", "", 0, sign.Verified},
		{"extra mount rejects primary token", "xi.internal", "/imgix/other/a.jpg?" + signed, "", "", errs.KindUnauthorized, 0},
		{"extra mount deny unsigned", "xi.internal", "/imgix/other/a.jpg?w=640", "", "", errs.KindForbidden, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequestWithContext(t.Context(), "GET", tc.target, nil)
			r.Host = tc.host
			res, m, err := rt.Route(r)
			if tc.kind != 0 {
				if errs.KindOf(err) != tc.kind {
					t.Fatalf("err = %v, want kind %v", err, tc.kind)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if m.Dialect != tc.mount || res.Signature != tc.sig {
				t.Fatalf("mount %s signature %v, want %s %v", m.Dialect, res.Signature, tc.mount, tc.sig)
			}
			if src := res.Request.Source; src.Raw != "a.jpg" || src.Mount != tc.source {
				t.Fatalf("source %+v, want a.jpg on %q", src, tc.source)
			}
		})
	}
}

// TestImgixDefault: XI_DIALECTS_DEFAULT=imgix serves unmatched paths with
// the imgix dialect, on its primary mount's source and unsigned policy.
func TestImgixDefault(t *testing.T) {
	t.Parallel()
	cfg, _, err := config.Load(config.LoadOptions{Environ: []string{
		"XI_DEV_MODE=true",
		"XI_DIALECTS_IMGPROXY_ENABLED=false",
		"XI_DIALECTS_DEFAULT=imgix",
		"XI_DIALECTS_IMGIX_ENABLED=true",
		"XI_DIALECTS_IMGIX_UNSIGNED_POLICY=allow",
		"XI_DIALECTS_IMGIX_KEYS=" + imgixToken,
	}, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	rt, warnings, err := buildRouter(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range warnings {
		t.Errorf("warning: %s", w)
	}
	res, m, err := rt.Route(httptest.NewRequestWithContext(t.Context(), "GET", "/photos/a.jpg?w=333&blur=40", nil))
	if err != nil || m.Name != "default" || m.Dialect != "imgix" || res.Request.Pipelines[0].Resize.Width.Px != 333 {
		t.Fatalf("Route = %+v, %+v, %v", res, m, err)
	}
	q := imgixSignQuery(t, imgixToken, "/photos/a.jpg", "w=333")
	if res, _, err := rt.Route(httptest.NewRequestWithContext(t.Context(), "GET", "/photos/a.jpg?"+q, nil)); err != nil || res.Signature != sign.Verified {
		t.Fatalf("signed default Route = %+v, %v", res, err)
	}
	// The mount prefix still routes to the primary mount.
	if _, m, err := rt.Route(httptest.NewRequestWithContext(t.Context(), "GET", "/imgix/a.jpg?w=10", nil)); err != nil || m.Name != "imgix" {
		t.Fatalf("prefix Route = %+v, %v", m, err)
	}
}
