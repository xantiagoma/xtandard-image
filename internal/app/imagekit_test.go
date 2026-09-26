package app

import (
	"net/http/httptest"
	"os"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

// TestImageKitMount: XI_DIALECTS_IMAGEKIT_* mounts the ImageKit dialect,
// verifying ik-s with the mount's raw private key.
func TestImageKitMount(t *testing.T) {
	t.Parallel()
	cfg, _, err := config.Load(config.LoadOptions{Environ: []string{
		"XI_DEV_MODE=true",
		"XI_DIALECTS_IMAGEKIT_ENABLED=true",
		"XI_DIALECTS_IMAGEKIT_KEYS=private_test",
		"XI_DIALECTS_IMAGEKIT_UNSIGNED_POLICY=allow",
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

	res, m, err := rt.Route(httptest.NewRequestWithContext(t.Context(), "GET", "/imagekit/tr:w-300/a.jpg", nil))
	if err != nil || m.Dialect != "imagekit" || res.Request.Pipelines[0].Resize.Width.Px != 300 || res.Signature != sign.Unsigned {
		t.Fatalf("Route = %+v, %+v, %v", res, m, err)
	}

	s, err := sign.NewSigner(sign.NewImageKitScheme(), sign.NewKeyring(sign.NewKey([]byte("private_test"), nil)))
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.SignImageKitQuery("/a.jpg", "tr=w-100", 0)
	if err != nil {
		t.Fatal(err)
	}
	res, _, err = rt.Route(httptest.NewRequestWithContext(t.Context(), "GET", "/imagekit/a.jpg?"+q, nil))
	if err != nil || res.Signature != sign.Verified {
		t.Fatalf("signed Route = %+v, %v", res, err)
	}
	if _, _, err := rt.Route(httptest.NewRequestWithContext(t.Context(), "GET", "/imagekit/b.jpg?"+q, nil)); err == nil {
		t.Fatal("signature for another path accepted")
	}
}

// TestImageKitEndpointPath: XI_DIALECTS_IMAGEKIT_ENDPOINT_PATH is stripped
// from host-bound ImageKit URLs before parsing and signing; paths outside it
// are rejected.
func TestImageKitEndpointPath(t *testing.T) {
	t.Parallel()
	cfg, _, err := config.Load(config.LoadOptions{Environ: []string{
		"XI_DEV_MODE=true",
		"XI_DIALECTS_IMAGEKIT_ENABLED=true",
		"XI_DIALECTS_IMAGEKIT_ENDPOINT_PATH=/demo",
		"XI_DIALECTS_IMAGEKIT_KEYS=private_test",
		"XI_DIALECTS_IMAGEKIT_UNSIGNED_POLICY=allow",
	}, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Dialects.Imagekit.EndpointPath != "/demo" {
		t.Fatalf("EndpointPath = %q", cfg.Dialects.Imagekit.EndpointPath)
	}
	rt, _, err := buildRouter(&cfg)
	if err != nil {
		t.Fatal(err)
	}

	res, _, err := rt.Route(httptest.NewRequestWithContext(t.Context(), "GET", "/imagekit/demo/tr:w-300/a.jpg", nil))
	if err != nil || res.Request.Pipelines[0].Resize.Width.Px != 300 || res.SourceString != "a.jpg" {
		t.Fatalf("Route = %+v, %v", res, err)
	}
	if _, _, err := rt.Route(httptest.NewRequestWithContext(t.Context(), "GET", "/imagekit/other/a.jpg", nil)); err == nil {
		t.Error("path outside the endpoint accepted")
	}

	// The signed message excludes the endpoint path.
	s, err := sign.NewSigner(sign.NewImageKitScheme(), sign.NewKeyring(sign.NewKey([]byte("private_test"), nil)))
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.SignImageKitQuery("/a.jpg", "tr=w-100", 0)
	if err != nil {
		t.Fatal(err)
	}
	res, _, err = rt.Route(httptest.NewRequestWithContext(t.Context(), "GET", "/imagekit/demo/a.jpg?"+q, nil))
	if err != nil || res.Signature != sign.Verified {
		t.Fatalf("signed Route = %+v, %v", res, err)
	}
}
