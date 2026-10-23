//go:build cgo

package app

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/dialect/query"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
)

// send makes one request with an optional body and content type.
func send(tb testing.TB, method, u, ctype, body string) response {
	tb.Helper()
	req, err := http.NewRequestWithContext(tb.Context(), method, u, strings.NewReader(body))
	if err != nil {
		tb.Fatal(err)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		tb.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		tb.Fatal(err)
	}
	return response{status: resp.StatusCode, ctype: resp.Header.Get("Content-Type"), body: b, header: resp.Header}
}

const (
	qmKey  = "943b421c9eb07c830af81030552c86009268de4e532ba2ee2eab8247c6da0881"
	qmSalt = "520f986b998545b4785e0defbc4f3c1203f22de2374a3d53cb7a7fe9fea309c5"
)

func queryMethodConfig(t *testing.T, extra ...string) config.Config {
	t.Helper()
	fixtures := filepath.Join(golden.ModuleRoot(t), "testdata", "fixtures")
	cfg, _, err := config.Load(config.LoadOptions{Environ: append([]string{
		"XI_SERVER_ADDR=127.0.0.1:0",
		"XI_ADMIN_ADDR=127.0.0.2:0",
		"XI_SERVER_SHUTDOWN_DELAY=0s",
		"XI_SECURITY_UNSIGNED_POLICY=deny",
		"XI_OBSERVABILITY_DEBUG_HEADERS=true",
		"XI_SIGNING_KEYS=" + qmKey,
		"XI_SIGNING_SALTS=" + qmSalt,
		"XI_SIGNING_REQUIRED=false",
		"XI_DIALECTS_QUERY_ENABLED=true",
		"XI_CACHE_RESULT_STORES=memory",
		"XI_CACHE_RESULT_MEMORY_BYTES=8MiB",
		"XI_SOURCES=docs",
		"XI_SOURCES_DOCS_TYPE=fs",
		"XI_SOURCES_DOCS_ROOT=" + fixtures,
	}, extra...), ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestQueryMethodEndToEnd runs WP-147 through the real service: a signed
// QUERY (form and JSON content) renders the same image as the signed GET
// URL from the same result cache entry, its Location is that GET URL, a
// query too long for a URL works, and unsigned, unsupported and disabled
// requests fail as documented.
func TestQueryMethodEndToEnd(t *testing.T) {
	t.Parallel()
	cfg := queryMethodConfig(t)
	var store *cache.AsyncStore
	a := start(t, &cfg, hooks{resultCache: func(s *cache.AsyncStore) { store = s }})
	base := "http://" + a.public.String()

	ring, err := sign.ParseHexKeyring([]string{qmKey}, []string{qmSalt})
	if err != nil {
		t.Fatal(err)
	}
	scheme, err := sign.NewXIScheme(32)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := sign.NewSigner(scheme, ring)
	if err != nil {
		t.Fatal(err)
	}
	signQuery := func(path, q string) string {
		s, err := signer.SignXIQuery(path, q)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	params := "url=xi://docs/jpeg/baseline.jpg&w=120&h=90&fit=cover&f=jpeg"
	signed := signQuery("/", params)
	getRes := get(t, base+"/q/?"+signed)
	checkJPEG(t, getRes, 120, 90)
	if got := getRes.header.Get("Accept-Query"); got != query.AcceptQuery {
		t.Errorf("GET Accept-Query = %q", got)
	}
	store.Wait()

	form := send(t, "QUERY", base+"/q/", query.MediaTypeForm, signed)
	checkJPEG(t, form, 120, 90)
	if form.header.Get("X-Xi-Cache") != "HIT" || form.header.Get("ETag") != getRes.header.Get("ETag") || !bytes.Equal(form.body, getRes.body) {
		t.Errorf("QUERY after GET: cache %q, ETag %q vs %q, same body %t",
			form.header.Get("X-Xi-Cache"), form.header.Get("ETag"), getRes.header.Get("ETag"), bytes.Equal(form.body, getRes.body))
	}
	if got := form.header.Get("Location"); got != "/q/?"+signed {
		t.Errorf("Location = %q", got)
	}

	sig := signed[strings.LastIndex(signed, "=")+1:]
	jsonBody := `{"url":"xi://docs/jpeg/baseline.jpg","w":120,"h":90,"fit":"cover","f":"jpeg","xi_sig":"` + sig + `"}`
	js := send(t, "QUERY", base+"/q/", query.MediaTypeJSON, jsonBody)
	checkJPEG(t, js, 120, 90)
	if js.header.Get("X-Xi-Cache") != "HIT" {
		t.Errorf("JSON QUERY cache %q, want HIT", js.header.Get("X-Xi-Cache"))
	}

	// Options that do not fit in a URL: the GET is refused (414), QUERY
	// renders, without a Location.
	long := signQuery("/", "url=xi://docs/jpeg/baseline.jpg&w=64&f=jpeg&cb="+strings.Repeat("x", 9000))
	if r := get(t, base+"/q/?"+long); r.status != http.StatusRequestURITooLong {
		t.Errorf("long GET: %d, want 414", r.status)
	}
	lr := send(t, "QUERY", base+"/q/", query.MediaTypeForm, long)
	checkJPEG(t, lr, 64, 64)
	if lr.header.Get("Location") != "" {
		t.Error("Location on a query longer than the limit")
	}

	for _, tc := range []struct {
		name, ctype, body string
		status            int
	}{
		{"unsigned", query.MediaTypeForm, params, http.StatusForbidden},
		{"tampered", query.MediaTypeForm, signed + "&w=121", http.StatusForbidden},
		{"text", "text/plain", signed, http.StatusUnsupportedMediaType},
		{"bad json", query.MediaTypeJSON, "{", http.StatusBadRequest},
	} {
		r := send(t, "QUERY", base+"/q/", tc.ctype, tc.body)
		if r.status != tc.status || r.header.Get("Accept-Query") != query.AcceptQuery {
			t.Errorf("%s: %d (Accept-Query %q), want %d", tc.name, r.status, r.header.Get("Accept-Query"), tc.status)
		}
	}
	if r := send(t, "QUERY", base+"/insecure/rs:fit:64:64/plain/xi://docs/jpeg/baseline.jpg", query.MediaTypeForm, "x=1"); r.status != http.StatusMethodNotAllowed || r.header.Get("Allow") != "GET, HEAD" {
		t.Errorf("QUERY on imgproxy: %d Allow %q", r.status, r.header.Get("Allow"))
	}
}

// TestQueryMethodDisabled: with XI_SERVER_QUERY_METHOD=false QUERY is the
// 405 it was before WP-147 and GET responses carry no Accept-Query.
func TestQueryMethodDisabled(t *testing.T) {
	t.Parallel()
	cfg := queryMethodConfig(t, "XI_SERVER_QUERY_METHOD=false", "XI_SECURITY_UNSIGNED_POLICY=allow")
	a := start(t, &cfg, hooks{})
	base := "http://" + a.public.String()
	r := send(t, "QUERY", base+"/q/", query.MediaTypeForm, "url=xi://docs/jpeg/baseline.jpg&w=64")
	if r.status != http.StatusMethodNotAllowed || r.header.Get("Allow") != "GET, HEAD" || r.header.Get("Accept-Query") != "" {
		t.Errorf("QUERY: %d Allow %q Accept-Query %q", r.status, r.header.Get("Allow"), r.header.Get("Accept-Query"))
	}
	g := get(t, base+"/q/?url=xi://docs/jpeg/baseline.jpg&w=64&f=jpeg")
	checkJPEG(t, g, 64, 64)
	if g.header.Get("Accept-Query") != "" {
		t.Error("Accept-Query on GET with QUERY off")
	}
}
