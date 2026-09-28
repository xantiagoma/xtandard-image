//go:build cgo

package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/playground"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
)

const (
	pgKeyHex  = "943b421c9eb07c830af81030552c86009268de4e532ba2ee2eab8247c6da0881"
	pgSaltHex = "520f986b998545b4785e0defbc4f3c1203f22de2374a3d53cb7a7fe9fea309c5"
)

func hexKey(t *testing.T, s string) config.HexKey {
	t.Helper()
	var k config.HexKey
	if err := k.UnmarshalText([]byte(s)); err != nil {
		t.Fatal(err)
	}
	return k
}

func postJSON(t *testing.T, u, body string) response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, u, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response{status: resp.StatusCode, ctype: resp.Header.Get("Content-Type"), body: b, header: resp.Header}
}

// TestPlaygroundListeners: the playground is a 404 on both listeners when
// disabled, served on the admin listener only when enabled, and on the
// public one too with XI_PLAYGROUND_PUBLIC; on the admin listener it signs
// a URL that the public listener serves, and api/meta never carries the
// key or the salt.
func TestPlaygroundListeners(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name            string
		enabled, public bool
		wantAdmin       int
		wantPublic      int
	}{
		{"disabled", false, false, http.StatusNotFound, http.StatusNotFound},
		{"enabled", true, false, http.StatusOK, http.StatusNotFound},
		{"public", true, true, http.StatusOK, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := fixturesConfig(t)
			cfg.Playground.Enabled, cfg.Playground.Public = tc.enabled, tc.public
			if tc.enabled {
				cfg.Playground.Uploads = config.True // Load resolved auto before Enabled was set
			}
			cfg.Signing.Keys = []config.HexKey{hexKey(t, pgKeyHex)}
			cfg.Signing.Salts = []config.HexKey{hexKey(t, pgSaltHex)}
			cfg.Signing.Required = config.True
			a := start(t, &cfg, hooks{})
			admin, public := "http://"+a.admin.String(), "http://"+a.public.String()

			for _, c := range []struct {
				base string
				want int
			}{{admin, tc.wantAdmin}, {public, tc.wantPublic}} {
				for _, path := range []string{"/_playground/", "/_playground/api/meta"} {
					got := get(t, c.base+path)
					if got.status != c.want {
						t.Errorf("GET %s%s = %d, want %d", c.base, path, got.status, c.want)
					}
					if got.status == http.StatusOK && !strings.Contains(got.header.Get("Content-Security-Policy"), "default-src 'self'") {
						t.Errorf("GET %s%s: CSP %q", c.base, path, got.header.Get("Content-Security-Policy"))
					}
				}
			}
			if !tc.enabled {
				return
			}

			meta := get(t, admin+"/_playground/api/meta")
			for _, secret := range []string{pgKeyHex, pgSaltHex, strings.ToUpper(pgKeyHex), strings.ToUpper(pgSaltHex)} {
				if bytes.Contains(meta.body, []byte(secret)) {
					t.Fatalf("api/meta contains key material")
				}
			}
			var m playground.Meta
			if err := json.Unmarshal(meta.body, &m); err != nil {
				t.Fatal(err)
			}
			if m.Signing.Mode != playground.SigningRequired || len(m.Mounts) == 0 || m.Mounts[0].ID != "imgproxy" || !m.Mounts[0].Keyed ||
				len(m.Catalogues) != 10 || len(m.Sources) != 1 || m.Sources[0].Name != "fixtures" ||
				m.Sources[0].Prefix != "xi://fixtures/" || len(m.Sources[0].Syntax) != 2 {
				t.Errorf("meta %+v", m)
			}

			signed := postJSON(t, admin+"/_playground/api/sign", `{"mount":"imgproxy","path":"/rs:fit:64:64/plain/jpeg/baseline.jpg"}`)
			var sr playground.SignResponse
			if err := json.Unmarshal(signed.body, &sr); err != nil || signed.status != http.StatusOK || !sr.Signed {
				t.Fatalf("sign: %d %s (%v)", signed.status, signed.body, err)
			}
			checkJPEG(t, get(t, public+sr.URL), 64, 64)
			rendered := get(t, admin+"/_playground/api/render?u="+url.QueryEscape(sr.URL))
			checkJPEG(t, rendered, 64, 64)
			if rendered.header.Get("Server-Timing") == "" {
				t.Error("api/render has no Server-Timing")
			}
			checkPlaygroundV2(t, admin, public, &m)
			// Signing is required: the unsigned form is rejected.
			if got := get(t, public+"/insecure/rs:fit:64:64/plain/jpeg/baseline.jpg"); got.status != http.StatusForbidden {
				t.Errorf("unsigned URL: %d, want 403", got.status)
			}
		})
	}
}

// TestPlaygroundSignsForEveryScheme signs one URL per signable mount with
// the playground's signers and routes it through the real router: the
// dialect must report it Verified. This pins each dialect's signed message
// (imgproxy path, xi_sig over the path after the mount or "/" for wsrv,
// thumbor path, imgix s over the path and query) to what the dialects
// verify.
func TestPlaygroundSignsForEveryScheme(t *testing.T) {
	t.Parallel()
	cfg, _, err := config.Load(config.LoadOptions{Environ: []string{
		"XI_DEV_MODE=true",
		"XI_SIGNING_KEYS=" + pgKeyHex,
		"XI_SIGNING_SALTS=" + pgSaltHex,
		"XI_SIGNING_REQUIRED=true",
		"XI_DIALECTS_QUERY_ENABLED=true",
		"XI_DIALECTS_WSRV_ENABLED=true",
		"XI_DIALECTS_CLOUDFLARE_ENABLED=true",
		"XI_DIALECTS_THUMBOR_ENABLED=true",
		"XI_DIALECTS_THUMBOR_KEYS=ACME-SEC",
		"XI_DIALECTS_IMAGEKIT_ENABLED=true",
		"XI_DIALECTS_IMGIX_ENABLED=true",
		"XI_DIALECTS_IMGIX_KEYS=FOO123bar",
		"XI_PRESETS=thumb=rs:fill:100:100",
		"XI_PLAYGROUND_ENABLED=true",
	}, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	rt, _, err := buildRouter(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	meta, signers, err := playgroundMeta(&cfg, rt, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Presets) != 1 || meta.Presets[0] != "thumb" {
		t.Errorf("presets %v", meta.Presets)
	}
	req := map[string][2]string{
		"imgproxy":   {"/rs:fit:300:0/plain/https://example.com/a.jpg", ""},
		"query":      {"/", "url=https%3A%2F%2Fexample.com%2Fa.jpg&w=300"},
		"wsrv":       {"/", "url=example.com/a.jpg&w=300"},
		"cloudflare": {"/width=300,fit=cover/https://example.com/a.jpg", ""},
		"thumbor":    {"/300x200/smart/example.com/a.jpg", ""},
		"imgix":      {"/photos/a.jpg", "w=300&h=200&fit=crop"},
	}
	seen := map[string]bool{}
	for i := range meta.Mounts {
		m := &meta.Mounts[i]
		in, ok := req[m.ID]
		if !ok {
			if m.Scheme != playground.SchemeNone && m.ID != "imagekit" {
				t.Errorf("mount %s (%s) has no case", m.ID, m.Scheme)
			}
			continue
		}
		seen[m.ID] = true
		if signers[m.ID] == nil || !m.Keyed {
			t.Errorf("%s: no signer", m.ID)
			continue
		}
		out, err := playground.SignURL(m, signers[m.ID], in[0], in[1])
		if err != nil || !out.Signed {
			t.Fatalf("%s: %+v, %v", m.ID, out, err)
		}
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, out.URL, http.NoBody)
		res, rm, err := rt.Route(r)
		if err != nil {
			t.Errorf("%s: route %s: %v", m.ID, out.URL, err)
			continue
		}
		if rm.Name != m.ID || res.Signature != sign.Verified {
			t.Errorf("%s: %s routed to %s with %v, want Verified", m.ID, out.URL, rm.Name, res.Signature)
		}
	}
	for id := range req {
		if !seen[id] {
			t.Errorf("mount %s missing from meta", id)
		}
	}
	for _, m := range meta.Mounts {
		if m.ID == "imagekit" && (m.Scheme != playground.SchemeNone || m.Keyed) {
			t.Errorf("imagekit mount %+v: the playground holds no ImageKit keys", m)
		}
	}
}

// checkPlaygroundV2 exercises the WP-94 endpoints against the real server:
// a built-in sample and an upload render through api/render but not on
// the public listener, translate signs the mounted dialect's URL, and the
// fs source lists its files.
func checkPlaygroundV2(t *testing.T, admin, public string, m *playground.Meta) {
	t.Helper()
	if len(m.Samples) == 0 || !m.Uploads || m.MaxUploadBytes <= 0 || len(m.Gallery) == 0 || len(m.Classes) < 2 || !m.Sources[0].Listable {
		t.Errorf("meta v2 fields: samples %d uploads %v max %d gallery %d classes %d sources %+v",
			len(m.Samples), m.Uploads, m.MaxUploadBytes, len(m.Gallery), len(m.Classes), m.Sources)
	}
	sign := func(path string) string {
		t.Helper()
		res := postJSON(t, admin+"/_playground/api/sign", `{"mount":"imgproxy","path":"`+path+`"}`)
		var sr playground.SignResponse
		if err := json.Unmarshal(res.body, &sr); err != nil || res.status != http.StatusOK {
			t.Fatalf("sign %s: %d %s", path, res.status, res.body)
		}
		return sr.URL
	}
	sample := sign("/rs:fit:64:0/f:jpeg/plain/playground://samples/photo.jpg")
	rendered := get(t, admin+"/_playground/api/render?u="+url.QueryEscape(sample))
	checkJPEG(t, rendered, 64, 48)
	if st := strings.Join(rendered.header.Values("Server-Timing"), ", "); !strings.Contains(st, "fetch;dur=") ||
		!strings.Contains(st, "process;dur=") || !strings.Contains(st, "xi;") {
		t.Errorf("render Server-Timing %q lacks the stages", st)
	}
	if got := get(t, public+sample); got.status != http.StatusNotFound {
		t.Errorf("playground sample on the public listener: %d, want 404", got.status)
	}

	photo, err := os.ReadFile(filepath.Join(golden.ModuleRoot(t), "testdata", "fixtures", "jpeg", "baseline.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, admin+"/_playground/api/upload", bytes.NewReader(photo))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "image/jpeg")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var up playground.Upload
	err = json.NewDecoder(resp.Body).Decode(&up)
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: %d %v", resp.StatusCode, err)
	}
	uploaded := sign("/rs:fit:32:0/f:jpeg/plain/" + up.Source)
	checkJPEG(t, get(t, admin+"/_playground/api/render?u="+url.QueryEscape(uploaded)), 32, 24)

	tr := postJSON(t, admin+"/_playground/api/translate", `{"mount":"imgproxy","path":"/rs:fill:64:64/f:jpeg/plain/jpeg/baseline.jpg"}`)
	var tres playground.TranslateResponse
	if err := json.Unmarshal(tr.body, &tres); err != nil || tr.status != http.StatusOK || len(tres.Targets) < 10 {
		t.Fatalf("translate: %d %s", tr.status, tr.body)
	}
	if ip := tres.Targets[0]; ip.Dialect != "imgproxy" || !ip.Signed {
		t.Errorf("translate imgproxy target %+v", ip)
	} else {
		pub := get(t, public+ip.URL)
		checkJPEG(t, pub, 64, 64)
		if st := pub.header.Get("Server-Timing"); st != "" {
			t.Errorf("public response carries Server-Timing %q", st)
		}
	}

	list := get(t, admin+"/_playground/api/sources/"+m.Sources[0].Name+"/list?prefix=jpeg")
	if list.status != http.StatusOK || !bytes.Contains(list.body, []byte(`"key":"jpeg/baseline.jpg"`)) {
		t.Errorf("list: %d %s", list.status, list.body)
	}
}

// TestPlaygroundMetaMergesPrefixes: the router lists a simple provider
// once per prefix; api/meta lists it once with every prefix.
func TestPlaygroundMetaMergesPrefixes(t *testing.T) {
	t.Parallel()
	cfg, _, err := config.Load(config.LoadOptions{Environ: []string{
		"XI_DEV_MODE=true",
		"XI_DIALECTS_SIMPLE_ENABLED=supabase,ipx,shopify",
		"XI_PLAYGROUND_ENABLED=true",
	}, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	rt, _, err := buildRouter(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	meta, _, err := playgroundMeta(&cfg, rt, "test")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, m := range meta.Mounts {
		if _, dup := got[m.ID]; dup {
			t.Errorf("mount %s listed twice", m.ID)
		}
		got[m.ID] = append([]string{m.Prefix}, m.Prefixes...)
	}
	want := map[string][]string{
		"simple:supabase": {"/storage/v1/render/image/public/", "/storage/v1/render/image/public/", "/storage/v1/object/public/", "/simple/supabase/"},
		"simple:ipx":      {"/_ipx/", "/_ipx/", "/simple/ipx/"},
		"simple:shopify":  {"/simple/shopify/"},
	}
	for id, w := range want {
		if !slices.Equal(got[id], w) {
			t.Errorf("%s: prefix and prefixes %v, want %v", id, got[id], w)
		}
	}
}

// TestPlaygroundMetaWatermark: api/meta says whether a default watermark
// is configured (the Examples tool disables the recipes that need one)
// without naming its source, and carries the recipes.
func TestPlaygroundMetaWatermark(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, source string
		want         bool
	}{
		{"none", "", false},
		{"configured", "local:///private/brand/secret-logo.png", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := []string{"XI_DEV_MODE=true", "XI_PLAYGROUND_ENABLED=true"}
			if tc.source != "" {
				env = append(env, "XI_PROCESSING_WATERMARK_SOURCE="+tc.source)
			}
			cfg, _, err := config.Load(config.LoadOptions{Environ: env, ConfigFile: os.DevNull})
			if err != nil {
				t.Fatal(err)
			}
			rt, _, err := buildRouter(&cfg)
			if err != nil {
				t.Fatal(err)
			}
			meta, _, err := playgroundMeta(&cfg, rt, "test")
			if err != nil {
				t.Fatal(err)
			}
			if err := (&playgroundEnv{}).extend(meta, &cfg); err != nil {
				t.Fatal(err)
			}
			if meta.Watermark != tc.want {
				t.Errorf("watermark = %v, want %v", meta.Watermark, tc.want)
			}
			if len(meta.Recipes) == 0 {
				t.Error("no recipes")
			}
			b, err := json.Marshal(meta)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(b, []byte("secret-logo")) || bytes.Contains(b, []byte("/private/brand")) {
				t.Error("api/meta names the watermark source")
			}
		})
	}
}
