package server

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect/query"
	"github.com/xantiagoma/xtandard-image/internal/dialect/router"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

const (
	formType = "application/x-www-form-urlencoded"
	jsonType = "application/json"
	// queryParams is a query dialect request the restricted policy admits
	// (width 64 is allowlisted in newQueryHarness).
	queryParams = "url=http://origin/a.jpg&w=64&fit=cover"
	queryJSON   = `{"url":"http://origin/a.jpg","w":64,"fit":"cover"}`
	// pinnedNativeKey is TestResultKeysPinned's "query native" key.
	pinnedNativeKey = "v1/e03fa37150a5108049ad382ad460081fa8365b6574879d2bd7bc595583e7a87b"
)

// doQuery sends a QUERY request with content of type ct ("" for none).
func (h *harness) doQuery(tb testing.TB, target, ct, content string, hdr http.Header) *httptest.ResponseRecorder {
	tb.Helper()
	req := httptest.NewRequestWithContext(tb.Context(), MethodQuery, target, strings.NewReader(content))
	maps.Copy(req.Header, hdr)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	rec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, req)
	return rec
}

// TestQueryMethod covers the QUERY ingress: media types, failures,
// headers and the method toggle.
func TestQueryMethod(t *testing.T) {
	t.Parallel()
	long := "url=http://origin/" + strings.Repeat("a", 3000) + ".jpg&w=64"
	tests := []struct {
		name     string
		mutate   func(*harness)
		target   string
		ct       string
		content  string
		hdr      http.Header
		status   int
		header   map[string]string // "" means absent
		body     string            // substring of the body
		noAccept bool              // no Accept-Query
	}{
		{
			name: "form", target: "/q/", ct: formType, content: queryParams, status: http.StatusOK,
			header: map[string]string{"Location": "/q/?" + queryParams, "Vary": "Accept, Content-Type", "Content-Type": "image/jpeg"},
		},
		{
			name: "form charset", target: "/q", ct: formType + "; charset=UTF-8", content: queryParams, status: http.StatusOK,
			header: map[string]string{"Location": "/q?" + queryParams},
		},
		{
			name: "json", target: "/q/", ct: jsonType, content: queryJSON, status: http.StatusOK,
			header: map[string]string{"Location": "/q/?" + queryParams},
		},
		{
			name: "json escapes", target: "/q/", ct: jsonType, content: `{"url":"http://origin/a.jpg?x=1&y=2","w":64}`, status: http.StatusOK,
			header: map[string]string{"Location": "/q/?url=http://origin/a.jpg%3Fx%3D1%26y%3D2&w=64"},
		},
		{
			name: "imaginary endpoint", target: "/q/resize", ct: formType, content: "url=http://origin/a.jpg&width=64&type=webp", status: http.StatusOK,
			header: map[string]string{"Location": "/q/resize?url=http://origin/a.jpg&width=64&type=webp"},
		},
		{
			name: "location too long", target: "/q/", ct: formType, content: long, status: http.StatusOK,
			header: map[string]string{"Location": ""},
		},
		{
			name: "location off", target: "/q/", ct: formType, content: queryParams, status: http.StatusOK,
			mutate: func(h *harness) { h.cfg.Query.LocationMaxLength = 0 },
			header: map[string]string{"Location": ""},
		},
		{
			name: "no location on errors", target: "/q/", ct: formType, content: "url=http://origin/a.jpg&w=64&f=nope", status: http.StatusBadRequest,
			header: map[string]string{"Location": ""},
		},
		{name: "missing content type", target: "/q/", content: queryParams, status: http.StatusUnsupportedMediaType},
		{name: "text content", target: "/q/", ct: "text/plain", content: queryParams, status: http.StatusUnsupportedMediaType},
		{name: "bad content type", target: "/q/", ct: "application/", content: queryParams, status: http.StatusUnsupportedMediaType},
		{name: "latin1", target: "/q/", ct: formType + "; charset=iso-8859-1", content: queryParams, status: http.StatusUnsupportedMediaType},
		{
			name: "content encoding", target: "/q/", ct: formType, content: queryParams, status: http.StatusUnsupportedMediaType,
			hdr: http.Header{"Content-Encoding": {"gzip"}},
		},
		{name: "malformed json", target: "/q/", ct: jsonType, content: `{"url":`, status: http.StatusBadRequest, body: "invalid query content"},
		{name: "duplicate json", target: "/q/", ct: jsonType, content: `{"w":1,"w":2}`, status: http.StatusBadRequest},
		{name: "bad form", target: "/q/", ct: formType, content: "url=a b", status: http.StatusBadRequest},
		{name: "bad escape", target: "/q/", ct: formType, content: "url=%zz", status: http.StatusBadRequest},
		{name: "empty content", target: "/q/", ct: formType, content: "", status: http.StatusBadRequest},
		{name: "url query too", target: "/q/?w=1", ct: formType, content: queryParams, status: http.StatusBadRequest, body: "not the url"},
		{
			name: "too large", target: "/q/", ct: formType, content: queryParams, status: http.StatusRequestEntityTooLarge,
			mutate: func(h *harness) { h.cfg.Query.MaxBodyBytes = 16 },
		},
		{
			name: "too large without length", target: "/q/", ct: jsonType, content: queryJSON, status: http.StatusRequestEntityTooLarge,
			mutate: func(h *harness) { h.cfg.Query.MaxBodyBytes = 16 },
			hdr:    http.Header{"Transfer-Encoding": {"chunked"}},
		},
		{
			name: "not a query endpoint", target: "/insecure/rs:fit:64:64/plain/http://origin/a.jpg", ct: formType, content: "x=1",
			status: http.StatusMethodNotAllowed, header: map[string]string{"Allow": "GET, HEAD"}, noAccept: true,
		},
		{
			name: "disabled", target: "/q/", ct: formType, content: queryParams, status: http.StatusMethodNotAllowed,
			mutate: func(h *harness) { h.cfg.Query.Enabled = false },
			header: map[string]string{"Allow": "GET, HEAD", "Vary": "Accept"}, noAccept: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newQueryHarness(t, nil, router.PolicyRestricted, tc.mutate)
			rec := h.doQuery(t, tc.target, tc.ct, tc.content, tc.hdr)
			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			for k, v := range tc.header {
				if got := strings.Join(rec.Header().Values(k), ", "); got != v {
					t.Errorf("%s = %q, want %q", k, got, v)
				}
			}
			wantAQ := query.AcceptQuery
			if tc.noAccept {
				wantAQ = ""
			}
			if got := rec.Header().Get("Accept-Query"); got != wantAQ {
				t.Errorf("Accept-Query = %q, want %q", got, wantAQ)
			}
			if !strings.Contains(rec.Body.String(), tc.body) {
				t.Errorf("body %q lacks %q", rec.Body.String(), tc.body)
			}
		})
	}
}

// TestQueryMethodSameAsGet: a QUERY response is the GET response for the
// same parameters, plus Accept-Query, Vary: Content-Type and Location.
func TestQueryMethodSameAsGet(t *testing.T) {
	t.Parallel()
	h := newQueryHarness(t, nil, router.PolicyAllow, func(h *harness) { h.cache = nil })
	get := h.do(t, http.MethodGet, "/q/?"+queryParams, http.Header{"Accept": {"image/webp,*/*"}})
	q := h.doQuery(t, "/q/", formType, queryParams, http.Header{"Accept": {"image/webp,*/*"}})
	if get.Code != http.StatusOK || q.Code != http.StatusOK {
		t.Fatalf("GET %d, QUERY %d", get.Code, q.Code)
	}
	gh, qh := comparableHeader(get.Header()), comparableHeader(q.Header())
	for _, k := range []string{hdrXiCache, "Location"} {
		qh.Del(k)
		gh.Del(k)
	}
	gh.Add("Vary", "Content-Type")
	if v := strings.Join(qh.Values("Vary"), ", "); v != strings.Join(gh.Values("Vary"), ", ") {
		t.Errorf("Vary: QUERY %q, GET %q", v, gh.Values("Vary"))
	}
	qh.Del("Vary")
	gh.Del("Vary")
	if diff := cmp.Diff(gh, qh); diff != "" {
		t.Errorf("headers (-GET +QUERY):\n%s", diff)
	}
	if diff := cmp.Diff(get.Body.Bytes(), q.Body.Bytes()); diff != "" {
		t.Errorf("body (-GET +QUERY):\n%s", diff)
	}
}

// TestQueryMethodSigning: xi_sig travels in the content and signs the same
// message as the GET URL; the unsigned policy applies unchanged.
func TestQueryMethodSigning(t *testing.T) {
	t.Parallel()
	keys, err := sign.ParseHexKeyring([]string{"943b421c9eb07c830af81030552c86009268de4e532ba2ee2eab8247c6da0881"}, []string{"520f986b998545b4785e0defbc4f3c1203f22de2374a3d53cb7a7fe9fea309c5"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := sign.NewXIVerifier(sign.XIOptions{Keys: keys, Size: 32})
	if err != nil {
		t.Fatal(err)
	}
	scheme, err := sign.NewXIScheme(32)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := sign.NewSigner(scheme, keys)
	if err != nil {
		t.Fatal(err)
	}
	const wide = "url=http://origin/a.jpg&w=300"
	signedWide, err := signer.SignXIQuery("/", wide)
	if err != nil {
		t.Fatal(err)
	}
	signedVideo, err := signer.SignXIQuery("/resize", "url=http://origin/a.jpg&width=300")
	if err != nil {
		t.Fatal(err)
	}
	sig := signedWide[strings.LastIndex(signedWide, "=")+1:]
	tests := []struct {
		name    string
		policy  router.Policy
		target  string
		ct      string
		content string
		status  int
	}{
		{"signed form under deny", router.PolicyDeny, "/q/", formType, signedWide, http.StatusOK},
		{"signed json under deny", router.PolicyDeny, "/q/", jsonType, `{"url":"http://origin/a.jpg","w":300,"xi_sig":"` + sig + `"}`, http.StatusOK},
		{"signed json, signature first", router.PolicyDeny, "/q/", jsonType, `{"xi_sig":"` + sig + `","url":"http://origin/a.jpg","w":300}`, http.StatusOK},
		{"signed imaginary endpoint", router.PolicyDeny, "/q/resize", formType, signedVideo, http.StatusOK},
		{"signature of another path", router.PolicyDeny, "/q/resize", formType, signedWide, http.StatusForbidden},
		{"tampered", router.PolicyDeny, "/q/", formType, signedWide + "&w=301", http.StatusForbidden},
		{"tampered json", router.PolicyDeny, "/q/", jsonType, `{"url":"http://origin/a.jpg","w":301,"xi_sig":"` + sig + `"}`, http.StatusForbidden},
		{"unsigned under deny", router.PolicyDeny, "/q/", formType, wide, http.StatusForbidden},
		{"unsigned restricted, allowed width", router.PolicyRestricted, "/q/", formType, queryParams, http.StatusOK},
		{"unsigned restricted, other width", router.PolicyRestricted, "/q/", formType, wide, http.StatusForbidden},
		{"unsigned restricted, json", router.PolicyRestricted, "/q/", jsonType, queryJSON, http.StatusOK},
		{"unsigned allow", router.PolicyAllow, "/q/", formType, wide, http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newQueryHarness(t, &query.Options{XIVerifier: v}, tc.policy, nil)
			rec := h.doQuery(t, tc.target, tc.ct, tc.content, nil)
			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			if tc.status != http.StatusOK {
				return
			}
			// The Location is the signed GET URL, which the server serves.
			loc := rec.Header().Get("Location")
			if loc == "" {
				t.Fatal("no Location")
			}
			if got := h.do(t, http.MethodGet, loc, nil); got.Code != http.StatusOK {
				t.Errorf("GET Location %q: %d", loc, got.Code)
			}
		})
	}
}

// TestQueryMethodCache: GET and QUERY for the same parameters share one
// result cache entry (the pinned GET key), in both orders, and QUERY
// answers conditional requests with 304.
func TestQueryMethodCache(t *testing.T) {
	t.Parallel()
	accept := http.Header{"Accept": {"image/webp,*/*"}}
	t.Run("get then query", func(t *testing.T) {
		t.Parallel()
		h := newQueryHarness(t, nil, router.PolicyAllow, nil)
		first := h.do(t, http.MethodGet, "/q/?url=http://origin/a.jpg&w=64&h=48&fit=cover", accept)
		if first.Header().Get(hdrXiCache) != cacheMiss {
			t.Fatalf("GET: %d %q", first.Code, first.Header().Get(hdrXiCache))
		}
		h.store.Wait()
		second := h.doQuery(t, "/q/", jsonType, `{"url":"http://origin/a.jpg","w":64,"h":48,"fit":"cover"}`, accept)
		if second.Code != http.StatusOK || second.Header().Get(hdrXiCache) != cacheHit {
			t.Fatalf("QUERY: %d %q", second.Code, second.Header().Get(hdrXiCache))
		}
		if got := h.keys.last(); got != pinnedNativeKey {
			t.Errorf("QUERY key %q, want the pinned GET key", got)
		}
		if first.Header().Get("ETag") != second.Header().Get("ETag") {
			t.Error("ETag differs")
		}
		cond := accept.Clone()
		cond.Set("If-None-Match", first.Header().Get("ETag"))
		nm := h.doQuery(t, "/q/", formType, "url=http://origin/a.jpg&w=64&h=48&fit=cover", cond)
		if nm.Code != http.StatusNotModified || nm.Body.Len() != 0 || nm.Header().Get("Location") != "" {
			t.Errorf("conditional QUERY: %d, %d bytes, Location %q", nm.Code, nm.Body.Len(), nm.Header().Get("Location"))
		}
		if got := len(h.f.fetched()); got != 1 {
			t.Errorf("fetched %d times, want 1", got)
		}
	})
	t.Run("query then get", func(t *testing.T) {
		t.Parallel()
		h := newQueryHarness(t, nil, router.PolicyAllow, nil)
		first := h.doQuery(t, "/q/", formType, "url=http://origin/a.jpg&w=64&h=48&fit=cover", accept)
		if first.Code != http.StatusOK || first.Header().Get(hdrXiCache) != cacheMiss {
			t.Fatalf("QUERY: %d %q", first.Code, first.Header().Get(hdrXiCache))
		}
		h.store.Wait()
		second := h.do(t, http.MethodGet, "/q/?url=http://origin/a.jpg&w=64&h=48&fit=cover", accept)
		if second.Header().Get(hdrXiCache) != cacheHit {
			t.Fatalf("GET after QUERY: %q", second.Header().Get(hdrXiCache))
		}
		if got := h.keys.last(); got != pinnedNativeKey {
			t.Errorf("GET key %q, want the pinned key", got)
		}
	})
}

// TestQueryMethodHeaders: Accept-Query on GET, HEAD and 405 responses of
// query endpoints only; Allow lists QUERY there; HEAD is unchanged.
func TestQueryMethodHeaders(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, method, target string
		disabled             bool
		status               int
		acceptQuery          bool
		allow                string
	}{
		{"get", http.MethodGet, "/q/?" + queryParams, false, http.StatusOK, true, ""},
		{"head", http.MethodHead, "/q/?" + queryParams, false, http.StatusOK, true, ""},
		{"get error", http.MethodGet, "/q/?w=1", false, http.StatusBadRequest, true, ""},
		{"imgproxy get", http.MethodGet, "/insecure/rs:fit:64:64/plain/http://origin/a.jpg", false, http.StatusOK, false, ""},
		{"post", http.MethodPost, "/q/", false, http.StatusMethodNotAllowed, true, "GET, HEAD, QUERY"},
		{"options", http.MethodOptions, "/q/video", false, http.StatusMethodNotAllowed, true, "GET, HEAD, QUERY"},
		{"imgproxy post", http.MethodPost, "/insecure/x", false, http.StatusMethodNotAllowed, false, "GET, HEAD"},
		{"disabled get", http.MethodGet, "/q/?" + queryParams, true, http.StatusOK, false, ""},
		{"disabled post", http.MethodPost, "/q/", true, http.StatusMethodNotAllowed, false, "GET, HEAD"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newQueryHarness(t, nil, router.PolicyRestricted, func(h *harness) { h.cfg.Query.Enabled = !tc.disabled })
			rec := h.do(t, tc.method, tc.target, nil)
			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d", rec.Code, tc.status)
			}
			if got := rec.Header().Get("Accept-Query") != ""; got != tc.acceptQuery {
				t.Errorf("Accept-Query present = %t, want %t", got, tc.acceptQuery)
			}
			if got := rec.Header().Get("Allow"); got != tc.allow {
				t.Errorf("Allow = %q, want %q", got, tc.allow)
			}
			if rec.Header().Get("Location") != "" || strings.Contains(strings.Join(rec.Header().Values("Vary"), ","), "Content-Type") {
				t.Error("QUERY-only headers on a GET/HEAD response")
			}
		})
	}
}

// TestQueryMethodCORS: a QUERY preflight on a query endpoint is answered
// with QUERY and Content-Type allowed; GET preflights are unchanged.
func TestQueryMethodCORS(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, target, method string
		disabled             bool
		status               int
		methods, headers     string
	}{
		{"query preflight", "/q/", MethodQuery, false, http.StatusNoContent, "GET, HEAD, QUERY", corsAllowed + ", Content-Type"},
		{"get preflight unchanged", "/q/", http.MethodGet, false, http.StatusNoContent, "GET, HEAD", corsAllowed},
		{"query preflight elsewhere", "/insecure/x", MethodQuery, false, http.StatusMethodNotAllowed, "", ""},
		{"query preflight disabled", "/q/", MethodQuery, true, http.StatusMethodNotAllowed, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newQueryHarness(t, nil, router.PolicyRestricted, func(h *harness) {
				h.cfg.CORSOrigins = []string{"https://app.example"}
				h.cfg.Query.Enabled = !tc.disabled
			})
			rec := h.do(t, http.MethodOptions, tc.target, http.Header{
				"Origin": {"https://app.example"}, "Access-Control-Request-Method": {tc.method},
				"Access-Control-Request-Headers": {"content-type"},
			})
			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d", rec.Code, tc.status)
			}
			if got := rec.Header().Get("Access-Control-Allow-Methods"); got != tc.methods {
				t.Errorf("Allow-Methods = %q, want %q", got, tc.methods)
			}
			if got := rec.Header().Get("Access-Control-Allow-Headers"); got != tc.headers {
				t.Errorf("Allow-Headers = %q, want %q", got, tc.headers)
			}
		})
	}
	t.Run("disabled query response as before", func(t *testing.T) {
		t.Parallel()
		h := newQueryHarness(t, nil, router.PolicyRestricted, func(h *harness) {
			h.cfg.CORSOrigins = []string{"*"}
			h.cfg.Query.Enabled = false
		})
		rec := h.doQuery(t, "/q/", formType, queryParams, http.Header{"Origin": {"https://app.example"}})
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Access-Control-Expose-Headers") != corsExposed ||
			rec.Header().Get("Allow") != "GET, HEAD" || rec.Header().Get("Accept-Query") != "" {
			t.Errorf("disabled QUERY: %d %v", rec.Code, rec.Header())
		}
	})
	t.Run("query response exposes location", func(t *testing.T) {
		t.Parallel()
		h := newQueryHarness(t, nil, router.PolicyRestricted, func(h *harness) { h.cfg.CORSOrigins = []string{"*"} })
		rec := h.doQuery(t, "/q/", formType, queryParams, http.Header{"Origin": {"https://app.example"}})
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		if got, want := rec.Header().Get("Access-Control-Expose-Headers"), corsExposed+", Location, Accept-Query"; got != want {
			t.Errorf("Expose-Headers = %q, want %q", got, want)
		}
	})
}

// TestQueryMethodPathPrefix: the Location keeps XI_SERVER_PATH_PREFIX.
func TestQueryMethodPathPrefix(t *testing.T) {
	t.Parallel()
	h := newQueryHarness(t, nil, router.PolicyRestricted, func(h *harness) { h.cfg.PathPrefix = "/img" })
	rec := h.doQuery(t, "/img/q/", formType, queryParams, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/img/q/?"+queryParams {
		t.Errorf("Location = %q", got)
	}
	if rec := h.doQuery(t, "/q/", formType, queryParams, nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("outside the prefix: %d, want 405", rec.Code)
	}
}

// TestQueryMethodPDFPassword: no Location when the query carries a
// document password.
func TestQueryMethodPDFPassword(t *testing.T) {
	t.Parallel()
	h := newQueryHarness(t, nil, router.PolicyAllow, nil)
	if got := h.srv.queryLocation(httptest.NewRequestWithContext(t.Context(), MethodQuery, "/q/", http.NoBody), "url=a&pdp=xyz"); got != "" {
		t.Errorf("Location %q with pdp", got)
	}
	if got := h.srv.queryLocation(httptest.NewRequestWithContext(t.Context(), MethodQuery, "/q/", http.NoBody), "url=a&pdf%5Fpassword=xyz"); got != "" {
		t.Errorf("Location %q with an escaped pdf_password", got)
	}
}
