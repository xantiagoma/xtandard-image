package playground

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

const (
	testKeyHex  = "943b421c9eb07c830af81030552c86009268de4e532ba2ee2eab8247c6da0881"
	testSaltHex = "520f986b998545b4785e0defbc4f3c1203f22de2374a3d53cb7a7fe9fea309c5"
)

func testRing(t *testing.T) *sign.Keyring {
	t.Helper()
	ring, err := sign.ParseHexKeyring([]string{testKeyHex}, []string{testSaltHex})
	if err != nil {
		t.Fatal(err)
	}
	return ring
}

func testSigner(t *testing.T, scheme sign.Scheme, ring *sign.Keyring) *sign.Signer {
	t.Helper()
	s, err := sign.NewSigner(scheme, ring)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func testMeta() *Meta {
	return &Meta{
		Version: "test", PathPrefix: "/img", PublicAddr: ":8080",
		Signing: Signing{Mode: SigningOptional, UnsignedPolicy: "restricted"},
		Mounts: []Mount{
			{ID: "imgproxy", Dialect: "imgproxy", Prefix: "/", Scheme: SchemeImgproxy, Keyed: true, Grammar: GrammarImgproxy, UnsignedPolicy: "allow"},
			{ID: "query", Dialect: "query", Prefix: "/q/", Scheme: SchemeXI, Keyed: true, Grammar: GrammarQuery, UnsignedPolicy: "restricted"},
			{ID: "wsrv", Dialect: "wsrv", Prefix: "/wsrv/", Scheme: SchemeXI, Keyed: true, Grammar: GrammarPath, UnsignedPolicy: "restricted"},
			{ID: "thumbor", Dialect: "thumbor", Prefix: "/thumbor/", Scheme: SchemeThumbor, Keyed: true, Grammar: GrammarPath, UnsignedPolicy: "restricted"},
			{ID: "imgproxy-open", Dialect: "imgproxy", Prefix: "/open/", Scheme: SchemeImgproxy, Grammar: GrammarImgproxy, UnsignedPolicy: "allow"},
			{ID: "imagekit", Dialect: "imagekit", Prefix: "/imagekit/", Scheme: SchemeNone, Grammar: GrammarPath, UnsignedPolicy: "allow"},
			{ID: "imgix", Dialect: "imgix", Prefix: "/imgix/", Scheme: SchemeImgix, Keyed: true, Grammar: GrammarImgix, UnsignedPolicy: "restricted"},
			{ID: "imgix[1]", Dialect: "imgix[1]", Prefix: "/imgix-open/", Scheme: SchemeImgix, Grammar: GrammarImgix, UnsignedPolicy: "allow"},
		},
		Catalogues: []Catalogue{{Grammar: GrammarImgproxy, Options: []Option{{Name: "resize", Aliases: []string{"rs"}, Forms: []Form{{Args: []Arg{{Name: "type", Type: "enum", Enum: []string{"fit"}}}}}}}}},
		Presets:    []string{"thumb"},
		Sources:    []Source{{Name: "local", Type: "fs"}},
		Formats:    []string{"jpeg", "webp"},
	}
}

// fakeImage records the request api/render passes on.
type fakeImage struct{ got *http.Request }

func (f *fakeImage) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.got = r
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Xi-Result-Width", "10")
	_, _ = w.Write([]byte("\x89PNG"))
}

type fixture struct {
	h       http.Handler
	image   *fakeImage
	ring    *sign.Keyring
	signers map[string]*sign.Signer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ring := testRing(t)
	ip, err := sign.NewImgproxyScheme(32)
	if err != nil {
		t.Fatal(err)
	}
	xi, err := sign.NewXIScheme(32)
	if err != nil {
		t.Fatal(err)
	}
	thumborRing := sign.NewKeyring(sign.NewKey([]byte("thumbor-secret"), nil))
	img := &fakeImage{}
	signers := map[string]*sign.Signer{
		"imgproxy": testSigner(t, ip, ring),
		"query":    testSigner(t, xi, ring),
		"wsrv":     testSigner(t, xi, ring),
		"thumbor":  testSigner(t, sign.NewThumborScheme(), thumborRing),
		"imgix":    testSigner(t, sign.NewImgixScheme(), sign.NewKeyring(sign.NewKey([]byte("FOO123bar"), nil))),
	}
	h, err := New(Config{Path: DefaultPath}, &Deps{
		Meta:    testMeta(),
		Signers: signers,
		Image:   img,
		Logger:  slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{h: h, image: img, ring: ring, signers: signers}
}

// result is a recorded response.
type result struct {
	StatusCode int
	Header     http.Header
}

func (f *fixture) do(t *testing.T, req *http.Request) (result, []byte) {
	t.Helper()
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	b, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	return result{StatusCode: rec.Code, Header: rec.Header()}, b
}

func get(t *testing.T, path string) *http.Request {
	t.Helper()
	return httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
}

func post(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/_playground/api/sign", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestNewValidates(t *testing.T) {
	t.Parallel()
	deps := func() *Deps {
		return &Deps{Meta: testMeta(), Image: http.NotFoundHandler(), Logger: slog.New(slog.DiscardHandler)}
	}
	for _, tc := range []struct {
		name string
		path string
		deps *Deps
	}{
		{"empty path", "", deps()},
		{"root", "/", deps()},
		{"trailing slash", "/play/", deps()},
		{"relative", "play", deps()},
		{"wildcard", "/{x}", deps()},
		{"nil deps", DefaultPath, nil},
		{"no image", DefaultPath, &Deps{Meta: testMeta(), Logger: slog.New(slog.DiscardHandler)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := New(Config{Path: tc.path}, tc.deps); err == nil {
				t.Error("New succeeded")
			}
		})
	}
}

func TestPageAndAssets(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	res, body := f.do(t, get(t, "/_playground/"))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("page status %d", res.StatusCode)
	}
	for k, want := range map[string]string{
		"Content-Type":           "text/html; charset=utf-8",
		"Cache-Control":          "no-store",
		"X-Frame-Options":        "DENY",
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := res.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	csp := res.Header.Get("Content-Security-Policy")
	for _, d := range []string{"default-src 'self'", "frame-ancestors 'none'", "object-src 'none'"} {
		if !strings.Contains(csp, d) {
			t.Errorf("CSP %q lacks %q", csp, d)
		}
	}
	if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "http") {
		t.Errorf("CSP %q allows inline code or another origin", csp)
	}
	m := regexp.MustCompile(`assets/([0-9a-f]{12})/app\.js`).FindSubmatch(body)
	if m == nil {
		t.Fatalf("page does not reference a versioned app.js:\n%s", body)
	}
	if strings.Contains(string(body), "__ASSETS__") || regexp.MustCompile(`(src|href)="(https?:)?//`).Match(body) {
		t.Error("page has an unfilled placeholder or loads from another origin")
	}
	version := string(m[1])
	for name, ct := range map[string]string{"app.js": "text/javascript; charset=utf-8", "style.css": "text/css; charset=utf-8"} {
		res, body := f.do(t, get(t, "/_playground/assets/"+version+"/"+name))
		if res.StatusCode != http.StatusOK || len(body) == 0 {
			t.Errorf("%s: status %d, %d bytes", name, res.StatusCode, len(body))
		}
		if got := res.Header.Get("Content-Type"); got != ct {
			t.Errorf("%s Content-Type %q", name, got)
		}
		if got := res.Header.Get("Cache-Control"); !strings.Contains(got, "immutable") {
			t.Errorf("%s Cache-Control %q", name, got)
		}
	}
	for _, path := range []string{
		"/_playground/assets/000000000000/app.js", "/_playground/assets/" + version + "/index.html",
		"/_playground/nope", "/_playground/api/unknown",
	} {
		if res, _ := f.do(t, get(t, path)); res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, res.StatusCode)
		}
	}
	res, _ = f.do(t, get(t, "/_playground"))
	if res.StatusCode != http.StatusTemporaryRedirect || res.Header.Get("Location") != "/_playground/" {
		t.Errorf("/_playground: %d → %q", res.StatusCode, res.Header.Get("Location"))
	}
}

func TestMetaHasNoKeyMaterial(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	res, body := f.do(t, get(t, "/_playground/api/meta"))
	if res.StatusCode != http.StatusOK || res.Header.Get("Cache-Control") != "no-store" || res.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, headers %v", res.StatusCode, res.Header)
	}
	var got Meta
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(testMeta(), &got); diff != "" {
		t.Errorf("meta (-want +got):\n%s", diff)
	}
	key, _ := hex.DecodeString(testKeyHex)
	for _, secret := range []string{testKeyHex, testSaltHex, strings.ToUpper(testKeyHex), string(key), "thumbor-secret"} {
		if strings.Contains(string(body), secret) {
			t.Errorf("api/meta contains key material %q", secret)
		}
	}
}

func TestSign(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ipv, err := sign.NewImgproxyVerifier(sign.ImgproxyOptions{Keys: f.ring, Size: 32, Required: true})
	if err != nil {
		t.Fatal(err)
	}
	xiv, err := sign.NewXIVerifier(sign.XIOptions{Keys: f.ring, Size: 32, Required: true})
	if err != nil {
		t.Fatal(err)
	}
	thv, err := sign.NewThumborVerifier(sign.ThumborOptions{Keys: sign.NewKeyring(sign.NewKey([]byte("thumbor-secret"), nil)), Required: true})
	if err != nil {
		t.Fatal(err)
	}
	const src = "/rs:fit:300:0/plain/https://example.com/a.jpg@webp"

	signed := func(t *testing.T, body string) SignResponse {
		t.Helper()
		res, b := f.do(t, post(t, body))
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status %d: %s", res.StatusCode, b)
		}
		var out SignResponse
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	t.Run("imgproxy", func(t *testing.T) {
		t.Parallel()
		out := signed(t, `{"mount":"imgproxy","path":"`+src+`"}`)
		rest, ok := strings.CutPrefix(out.URL, "/img/")
		if !ok || !out.Signed || out.Scheme != SchemeImgproxy {
			t.Fatalf("response %+v", out)
		}
		sig, path, _ := strings.Cut(rest, "/")
		if path = "/" + path; path != src {
			t.Fatalf("signed path %q, want %q", path, src)
		}
		if r, err := ipv.Verify(sig, path); err != nil || r != sign.Verified {
			t.Errorf("Verify = %v, %v", r, err)
		}
	})
	t.Run("xi_sig query", func(t *testing.T) {
		t.Parallel()
		out := signed(t, `{"mount":"query","path":"/","query":"url=https%3A%2F%2Fexample.com%2Fa.jpg&w=300&xi_sig=stale"}`)
		path, query, _ := strings.Cut(strings.TrimPrefix(out.URL, "/img/q"), "?")
		sig, msg, err := sign.XIMessage(path, query)
		if err != nil || !strings.HasPrefix(out.URL, "/img/q/?url=") {
			t.Fatalf("url %q: %v", out.URL, err)
		}
		if r, err := xiv.Verify(sig, msg); err != nil || r != sign.Verified {
			t.Errorf("Verify = %v, %v", r, err)
		}
	})
	t.Run("xi_sig wsrv signs /", func(t *testing.T) {
		t.Parallel()
		out := signed(t, `{"mount":"wsrv","path":"/","query":"url=example.com/a.jpg&w=300"}`)
		_, query, _ := strings.Cut(out.URL, "?")
		sig, msg, err := sign.XIMessage("/", query)
		if err != nil {
			t.Fatal(err)
		}
		if r, err := xiv.Verify(sig, msg); err != nil || r != sign.Verified {
			t.Errorf("Verify = %v, %v (%s)", r, err, out.URL)
		}
	})
	t.Run("thumbor", func(t *testing.T) {
		t.Parallel()
		out := signed(t, `{"mount":"thumbor","path":"/300x200/smart/example.com/a.jpg"}`)
		sig, rest, _ := strings.Cut(strings.TrimPrefix(out.URL, "/img/thumbor/"), "/")
		if rest != "300x200/smart/example.com/a.jpg" {
			t.Fatalf("url %q", out.URL)
		}
		if r, err := thv.Verify(sig, rest); err != nil || r != sign.Verified {
			t.Errorf("Verify = %v, %v", r, err)
		}
	})
	t.Run("imgix", func(t *testing.T) {
		t.Parallel()
		v, err := sign.NewImgixVerifier(sign.ImgixOptions{Keys: sign.NewKeyring(sign.NewKey([]byte("FOO123bar"), nil)), Required: true})
		if err != nil {
			t.Fatal(err)
		}
		out := signed(t, `{"mount":"imgix","path":"/photos/a.jpg","query":"w=300&s=stale&fit=crop"}`)
		path, query, _ := strings.Cut(strings.TrimPrefix(out.URL, "/img/imgix"), "?")
		if path != "/photos/a.jpg" || !out.Signed || out.Scheme != SchemeImgix || !strings.HasPrefix(query, "w=300&fit=crop&s=") {
			t.Fatalf("response %+v", out)
		}
		sig, msg, err := sign.ImgixMessage(path, query)
		if err != nil {
			t.Fatal(err)
		}
		if r, err := v.Verify(sig, msg); err != nil || r != sign.Verified {
			t.Errorf("Verify = %v, %v", r, err)
		}
		out = signed(t, `{"mount":"imgix[1]","path":"/a.jpg","query":"w=1"}`)
		if out.URL != "/img/imgix-open/a.jpg?w=1" || out.Signed || out.Scheme != SchemeImgix {
			t.Errorf("unkeyed imgix response %+v", out)
		}
	})
	t.Run("no key", func(t *testing.T) {
		t.Parallel()
		out := signed(t, `{"mount":"imgproxy-open","path":"`+src+`"}`)
		if want := "/img/open/insecure" + src; out.URL != want || out.Signed {
			t.Errorf("response %+v, want unsigned %s", out, want)
		}
	})
	t.Run("unsupported scheme", func(t *testing.T) {
		t.Parallel()
		out := signed(t, `{"mount":"imagekit","path":"/tr:w-300/a.jpg","query":"x=1"}`)
		if out.URL != "/img/imagekit/tr:w-300/a.jpg?x=1" || out.Signed || out.Scheme != SchemeNone {
			t.Errorf("response %+v", out)
		}
	})
}

func TestSignRejects(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for _, tc := range []struct {
		name   string
		req    func(t *testing.T) *http.Request
		status int
	}{
		{"unknown mount", func(t *testing.T) *http.Request { t.Helper(); return post(t, `{"mount":"nope","path":"/a"}`) }, http.StatusBadRequest},
		{"relative path", func(t *testing.T) *http.Request { t.Helper(); return post(t, `{"mount":"imgproxy","path":"a.jpg"}`) }, http.StatusBadRequest},
		{"space", func(t *testing.T) *http.Request {
			t.Helper()
			return post(t, `{"mount":"imgproxy","path":"/plain/a b.jpg"}`)
		}, http.StatusBadRequest},
		{"unknown field", func(t *testing.T) *http.Request {
			t.Helper()
			return post(t, `{"mount":"imgproxy","path":"/a","key":"x"}`)
		}, http.StatusBadRequest},
		{"not json", func(t *testing.T) *http.Request { t.Helper(); return post(t, `mount=imgproxy`) }, http.StatusBadRequest},
		{"too long", func(t *testing.T) *http.Request {
			t.Helper()
			return post(t, `{"mount":"imgproxy","path":"/`+strings.Repeat("a", maxURLPart)+`"}`)
		}, http.StatusBadRequest},
		{"too large", func(t *testing.T) *http.Request {
			t.Helper()
			return post(t, `{"mount":"imgproxy","path":"/a","query":"`+strings.Repeat("a", maxSignBody)+`"}`)
		}, http.StatusRequestEntityTooLarge},
		{"form content type", func(t *testing.T) *http.Request {
			t.Helper()
			r := post(t, `{"mount":"imgproxy","path":"/a"}`)
			r.Header.Set("Content-Type", "text/plain")
			return r
		}, http.StatusUnsupportedMediaType},
		{"cross site", func(t *testing.T) *http.Request {
			t.Helper()
			r := post(t, `{"mount":"imgproxy","path":"/a"}`)
			r.Header.Set("Sec-Fetch-Site", "cross-site")
			return r
		}, http.StatusForbidden},
		{"cross origin", func(t *testing.T) *http.Request {
			t.Helper()
			r := post(t, `{"mount":"imgproxy","path":"/a"}`)
			r.Header.Set("Origin", "https://evil.example")
			return r
		}, http.StatusForbidden},
		{"get", func(t *testing.T) *http.Request { t.Helper(); return get(t, "/_playground/api/sign") }, http.StatusMethodNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, body := f.do(t, tc.req(t))
			if res.StatusCode != tc.status {
				t.Errorf("status %d, want %d: %s", res.StatusCode, tc.status, body)
			}
		})
	}
}

func TestRender(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := "/img/insecure/rs:fit:10:0/plain/https:%2F%2Fexample.com%2Fa.jpg@png?cb=1"
	req := get(t, "/_playground/api/render?u="+strings.ReplaceAll(strings.ReplaceAll(u, "%", "%25"), "?", "%3F"))
	req.Header.Set("Accept", "image/webp")
	req.Header.Set("Sec-Ch-Dpr", "2")
	req.Header.Set("Cookie", "session=secret")
	req.Header.Set("Authorization", "Basic eDp5")
	res, body := f.do(t, req)
	if res.StatusCode != http.StatusOK || string(body) != "\x89PNG" {
		t.Fatalf("status %d body %q", res.StatusCode, body)
	}
	got := f.image.got
	if got.RequestURI != u || got.URL.RawQuery != "cb=1" || got.URL.EscapedPath() != strings.Split(u, "?")[0] {
		t.Errorf("image got %q (path %q query %q)", got.RequestURI, got.URL.EscapedPath(), got.URL.RawQuery)
	}
	if got.Header.Get("Accept") != "image/webp" || got.Header.Get("Sec-Ch-Dpr") != "2" {
		t.Errorf("negotiation headers not forwarded: %v", got.Header)
	}
	if got.Header.Get("Cookie") != "" || got.Header.Get("Authorization") != "" {
		t.Errorf("credentials forwarded: %v", got.Header)
	}
	if !strings.HasPrefix(res.Header.Get("Server-Timing"), "xi;") || res.Header.Get("X-Xi-Result-Width") != "10" {
		t.Errorf("headers %v", res.Header)
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Errorf("render CSP %q", csp)
	}
	for _, bad := range []string{"", "insecure/a", "//evil.example/a", "/a%20b%0A", "/a%23frag"} {
		if res, _ := f.do(t, get(t, "/_playground/api/render?u="+bad)); res.StatusCode != http.StatusBadRequest {
			t.Errorf("u=%q: status %d, want 400", bad, res.StatusCode)
		}
	}
}

func TestPanicIs500(t *testing.T) {
	t.Parallel()
	h, err := New(Config{Path: "/p"}, &Deps{
		Meta:   testMeta(),
		Image:  http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }),
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, get(t, "/p/api/render?u=/a"))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status %d, want 500", rec.Code)
	}
}
