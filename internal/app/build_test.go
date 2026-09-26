package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/codec/encode"
	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/server"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// TestServerConfigSpecialPaths: XI_FALLBACK_* and the debug-header switch
// reach the server (WP-34c).
func TestServerConfigSpecialPaths(t *testing.T) {
	t.Parallel()
	cfg, _, err := config.Load(config.LoadOptions{Environ: []string{
		"XI_DEV_MODE=true",
		"XI_FALLBACK_SOURCE=local:///fallback.png",
		"XI_FALLBACK_STATUS=404",
		"XI_FALLBACK_TTL=30s",
		"XI_FALLBACK_ALLOW_REQUEST_URL=true",
		"XI_OBSERVABILITY_DEBUG_HEADERS=true",
	}, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	sc := serverConfig(&cfg)
	want := server.Fallback{Source: "local:///fallback.png", Status: 404, TTL: 30 * time.Second, AllowRequestURL: true}
	if diff := cmp.Diff(want, sc.Fallback); diff != "" {
		t.Errorf("fallback (-want +got):\n%s", diff)
	}
	if !sc.DebugHeaders {
		t.Error("debug headers not mapped")
	}

	def, _, err := config.Load(config.LoadOptions{Environ: []string{"XI_DEV_MODE=true"}, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	if got := serverConfig(&def).Fallback; got != (server.Fallback{Status: 200, TTL: time.Minute}) {
		t.Errorf("default fallback = %+v", got)
	}
}

// TestEncoderConfigAutoQuality: XI_FORMATS_AUTOQUALITY_* reach the encoder,
// and the documented defaults equal the encoder's own.
func TestEncoderConfigAutoQuality(t *testing.T) {
	t.Parallel()
	def, _, err := config.Load(config.LoadOptions{Environ: []string{"XI_DEV_MODE=true"}, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(encode.DefaultAutoQuality(), encoderConfig(&def).AutoQuality); diff != "" {
		t.Errorf("defaults (-encode +config):\n%s", diff)
	}

	cfg, _, err := config.Load(config.LoadOptions{Environ: []string{
		"XI_DEV_MODE=true",
		"XI_FORMATS_AUTOQUALITY_METHOD=dssim",
		"XI_FORMATS_AUTOQUALITY_TARGET=0.015",
		"XI_FORMATS_AUTOQUALITY_MIN=50",
		"XI_FORMATS_AUTOQUALITY_MAX=88",
		"XI_FORMATS_AUTOQUALITY_ALLOWED_ERROR=0.002",
		"XI_FORMATS_AUTOQUALITY_FORMAT_MIN=jpeg=45,avif=35",
		"XI_FORMATS_AUTOQUALITY_FORMAT_MAX=avif=70",
		"XI_FORMATS_AUTOQUALITY_FORMAT_TARGET=webp=0.01",
	}, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	want := encode.AutoQualityConfig{
		Method: transform.AutoQualityDSSIM, Target: 0.015, Min: 50, Max: 88, AllowedError: 0.002,
		FormatTarget: map[transform.Format]float64{transform.FormatWebP: 0.01},
		FormatMin:    map[transform.Format]int{transform.FormatJPEG: 45, transform.FormatAVIF: 35},
		FormatMax:    map[transform.Format]int{transform.FormatAVIF: 70},
	}
	ec := encoderConfig(&cfg)
	if diff := cmp.Diff(want, ec.AutoQuality); diff != "" {
		t.Errorf("mapped (-want +got):\n%s", diff)
	}
	if err := ec.Validate(); err != nil {
		t.Errorf("mapped config invalid: %v", err)
	}
}

// TestBuildRouterRootURL: "/?url=" goes to wsrv when it is enabled, else to
// query (URL-DIALECTS §1); /wsrv/ is wsrv's own mount (WP-46).
func TestBuildRouterRootURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		env    []string
		target string
		want   string
	}{
		{"wsrv root", []string{"XI_DIALECTS_WSRV_ENABLED=true", "XI_DIALECTS_QUERY_ENABLED=true"}, "/?url=example.com/a.jpg&w=16", "wsrv"},
		{"wsrv prefix", []string{"XI_DIALECTS_WSRV_ENABLED=true"}, "/wsrv/?url=example.com/a.jpg&w=16", "wsrv"},
		{"query root", []string{"XI_DIALECTS_QUERY_ENABLED=true"}, "/?url=https://example.com/a.jpg&w=16", "query"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, _, err := config.Load(config.LoadOptions{
				Environ:    append([]string{"XI_DEV_MODE=true", "XI_SECURITY_UNSIGNED_POLICY=allow"}, tc.env...),
				ConfigFile: os.DevNull,
			})
			if err != nil {
				t.Fatal(err)
			}
			rt, warnings, err := buildRouter(&cfg)
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range warnings {
				if strings.Contains(w, "wsrv") {
					t.Errorf("warning %q", w)
				}
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.target, http.NoBody)
			res, m, err := rt.Route(req)
			if err != nil {
				t.Fatal(err)
			}
			if m.Dialect != tc.want || res.Request.Dialect != tc.want {
				t.Errorf("dialect = %q / %q, want %q", m.Dialect, res.Request.Dialect, tc.want)
			}
		})
	}
}

// TestThumborMount: XI_DIALECTS_THUMBOR_* mounts the thumbor dialect with
// its SECURITY_KEY-style keys (WP-45).
func TestThumborMount(t *testing.T) {
	t.Parallel()
	cfg, _, err := config.Load(config.LoadOptions{Environ: []string{
		"XI_DEV_MODE=true",
		"XI_SECURITY_UNSIGNED_POLICY=allow",
		"XI_DIALECTS_THUMBOR_ENABLED=true",
		"XI_DIALECTS_THUMBOR_KEYS=ACME-SEC",
	}, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	rt, warnings, err := buildRouter(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range warnings {
		if strings.Contains(w, "thumbor") {
			t.Errorf("warning %q", w)
		}
	}
	tests := []struct {
		path string
		want sign.Result
		ok   bool
	}{
		// thumbor tests/handlers/test_base_handler.py (key ACME-SEC).
		{"/thumbor/_wIUeSaeHw8dricKG2MGhqu5thk=/smart/image.jpg", sign.Verified, true},
		{"/thumbor/unsafe/300x200/smart/image.jpg", sign.Unsigned, true},
		{"/thumbor/_wIUeSaeHw8dricKG2MGhqu5thk=/smart/other.jpg", sign.Unsigned, false},
	}
	for _, tc := range tests {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.path, nil)
		res, m, err := rt.Route(r)
		if (err == nil) != tc.ok {
			t.Fatalf("%s: err = %v", tc.path, err)
		}
		if err != nil {
			continue
		}
		if m.Dialect != "thumbor" || res.Signature != tc.want || res.SourceString == "" {
			t.Errorf("%s: dialect %q, signature %v, source %q", tc.path, m.Dialect, res.Signature, res.SourceString)
		}
	}
}

func TestThumborDisabledNotMounted(t *testing.T) {
	t.Parallel()
	def, _, err := config.Load(config.LoadOptions{Environ: []string{"XI_DEV_MODE=true"}, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	rt, _, err := buildRouter(&def)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/thumbor/unsafe/a.jpg", nil)
	if _, m, err := rt.Route(r); err == nil && m.Dialect == "thumbor" {
		t.Fatal("thumbor mounted while disabled")
	}
}

// TestBuildRouterBunny: an enabled bunny mount is routed with its own
// token key (WP-44) and no longer reported as unimplemented.
func TestBuildRouterBunny(t *testing.T) {
	t.Parallel()
	cfg, _, err := config.Load(config.LoadOptions{Environ: []string{
		"XI_DEV_MODE=true",
		"XI_DIALECTS_BUNNY_ENABLED=true",
		"XI_DIALECTS_BUNNY_KEYS=SecurityKey",
	}, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	rt, warnings, err := buildRouter(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range warnings {
		if strings.Contains(w, "bunny") {
			t.Errorf("warning %q", w)
		}
	}
	// BunnyWay/BunnyCDN.TokenAuthentication e2e vector "plain": the token
	// verifies, then its 2020 expiry answers 404; a changed path is a 403.
	const q = "?token=HS256-o10JRWlsAItyAsdKS6jJKjabHN4FrFsplDHPV1idcX4&expires=1598024587"
	for path, want := range map[string]errs.Kind{
		"/bunny/300kb.jpg" + q: errs.KindExpired,
		"/bunny/301kb.jpg" + q: errs.KindUnauthorized,
	} {
		_, m, err := rt.Route(httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody))
		if errs.KindOf(err) != want || m == nil || m.Dialect != "bunny" {
			t.Errorf("%s: mount %v, err %v; want %v", path, m, err, want)
		}
	}
	res, _, err := rt.Route(httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/bunny/a.jpg?width=640", http.NoBody))
	if err != nil || res.Request.Dialect != "bunny" || res.SourceString != "a.jpg" {
		t.Errorf("unsigned: %v, %v", res, err)
	}
}

// TestAWSSIHRegistration: an enabled awssih mount is routed with its
// bucket map and verbatim keys (WP-85).
func TestAWSSIHRegistration(t *testing.T) {
	t.Parallel()
	yml := `sources:
  fixtures:
    type: fs
    root: ` + strconv.Quote(t.TempDir()) + `
dialects:
  awssih:
    enabled: true
    source: fixtures
    keys: ["secret"]
    thumbor: true
    buckets:
      my-bucket: fixtures
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
	// The thumbor dialect is always registered (WP-45), so forwarding
	// resolves and there is no forwarding warning.
	if slices.ContainsFunc(warnings, func(w string) bool { return strings.Contains(w, "awssih") }) {
		t.Errorf("warnings = %q, want no awssih warning", warnings)
	}
	// setup.spec.ts signature fixture shape, with the bucket renamed.
	body := `{"bucket":"my-bucket","key":"validKey","edits":{"toFormat":"png"}}`
	p := "/" + base64.StdEncoding.EncodeToString([]byte(body))
	h := hmac.New(sha256.New, []byte("secret"))
	h.Write([]byte(p))
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/sih"+p+"?signature="+hex.EncodeToString(h.Sum(nil)), nil)
	res, m, err := rt.Route(r)
	if err != nil {
		t.Fatal(err)
	}
	if m.Dialect != "awssih" || res.Request.Dialect != "awssih" || res.Signature != sign.Verified ||
		res.Request.Source.Mount != "fixtures" || res.SourceString != "validKey" {
		t.Errorf("route = mount %q dialect %q sig %v source %q/%q",
			m.Dialect, res.Request.Dialect, res.Signature, res.Request.Source.Mount, res.SourceString)
	}
	wrong := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/sih"+p+"?signature="+strings.Repeat("0", 64), nil)
	if _, _, err := rt.Route(wrong); err == nil {
		t.Error("wrong signature accepted")
	}
}
