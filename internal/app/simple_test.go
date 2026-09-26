package app

import (
	"net/http/httptest"
	"os"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// TestSimpleMount: XI_DIALECTS_SIMPLE_* mounts the enabled unpic providers
// at their native prefixes and /simple/<name>/, binds the dispatcher to the
// hosts, relative sources to the source, and verifies xi_sig with the
// shared signing keys.
func TestSimpleMount(t *testing.T) {
	t.Parallel()
	cfg, _, err := config.Load(config.LoadOptions{Environ: []string{
		"XI_DEV_MODE=true",
		"XI_SOURCES=origin",
		"XI_SOURCES_ORIGIN_TYPE=http",
		"XI_SOURCES_ORIGIN_BASE_URL=https://www.example.com/",
		"XI_DIALECTS_SIMPLE_ENABLED=nextjs,ipx,contentful",
		"XI_DIALECTS_SIMPLE_HOSTS=img.example.com",
		"XI_DIALECTS_SIMPLE_SOURCE=origin",
		"XI_DIALECTS_SIMPLE_UNSIGNED_POLICY=allow",
		"XI_SIGNING_KEYS=736563726574",
		"XI_SIGNING_SALTS=73616c74",
		"XI_SIGNING_REQUIRED=false",
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
	tests := []struct {
		host, target, dialect, source string
		width                         int
	}{
		{"xi.test", "/_next/image?url=%2Fa.jpg&w=640&q=75", "simple:nextjs", "a.jpg", 640},
		{"xi.test", "/_ipx/w_200/b.jpg", "simple:ipx", "b.jpg", 200},
		{"xi.test", "/simple/contentful/s/a/t/c.jpg?w=300", "simple:contentful", "s/a/t/c.jpg", 300},
		{"img.example.com", "/s/a/t/d.jpg?w=100", "simple:contentful", "s/a/t/d.jpg", 100},
		{"img.example.com", "/_next/image?url=%2Fe.jpg&w=16", "simple:nextjs", "e.jpg", 16},
	}
	for _, tt := range tests {
		req := httptest.NewRequestWithContext(t.Context(), "GET", tt.target, nil)
		req.Host = tt.host
		res, _, err := rt.Route(req)
		if err != nil {
			t.Errorf("%s%s: %v", tt.host, tt.target, err)
			continue
		}
		r := res.Request
		if r.Dialect != tt.dialect || res.SourceString != tt.source || r.Source.Mount != "origin" ||
			r.Source.Encoding != transform.SourceRelative || r.Pipelines[0].Resize.Width.Px != tt.width {
			t.Errorf("%s%s: dialect %s, source %q (%s), width %d", tt.host, tt.target, r.Dialect, res.SourceString, r.Source.Mount, r.Pipelines[0].Resize.Width.Px)
		}
		if res.Signature != sign.Unsigned {
			t.Errorf("%s: signature %v", tt.target, res.Signature)
		}
	}

	keys, err := sign.ParseHexKeyring([]string{"736563726574"}, []string{"73616c74"})
	if err != nil {
		t.Fatal(err)
	}
	scheme, err := sign.NewXIScheme(cfg.Signing.Size)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := sign.NewSigner(scheme, keys)
	if err != nil {
		t.Fatal(err)
	}
	q, err := signer.SignXIQuery("/_next/image", "url=%2Fa.jpg&w=640")
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := rt.Route(httptest.NewRequestWithContext(t.Context(), "GET", "/_next/image?"+q, nil))
	if err != nil || res.Signature != sign.Verified {
		t.Fatalf("signed Route = %+v, %v", res, err)
	}
}

// TestSimpleUnknownProvider: an unknown provider name fails startup.
func TestSimpleUnknownProvider(t *testing.T) {
	t.Parallel()
	cfg, _, err := config.Load(config.LoadOptions{Environ: []string{
		"XI_DEV_MODE=true",
		"XI_DIALECTS_SIMPLE_ENABLED=nextjs,nope",
	}, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := buildRouter(&cfg); err == nil {
		t.Fatal("unknown provider accepted")
	}
}
