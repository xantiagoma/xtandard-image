package admin

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestOpenAPIServed(t *testing.T) {
	t.Parallel()

	spec := []byte(`{"openapi":"3.1.0"}` + "\n")
	mux := NewMux(&Options{OpenAPI: spec})

	rec := serve(t, mux, http.MethodGet, "/openapi.json")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != string(spec) {
		t.Errorf("body %q, want the document bytes %q", got, spec)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type %q", ct)
	}
	etag := rec.Header().Get("ETag")
	if !strings.HasPrefix(etag, `"`) || strings.HasPrefix(etag, "W/") || len(etag) < 10 {
		t.Fatalf("ETag %q is not a strong entity tag", etag)
	}

	for _, inm := range []string{etag, "W/" + etag, `"x", ` + etag, "*"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/openapi.json", http.NoBody)
		req.Header.Set("If-None-Match", inm)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
			t.Errorf("If-None-Match %s: status %d, body %d bytes; want 304 without body", inm, rec.Code, rec.Body.Len())
		}
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/openapi.json", http.NoBody)
	req.Header.Set("If-None-Match", `"other"`)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("stale If-None-Match: status %d, want 200", rec.Code)
	}

	head := serve(t, mux, http.MethodHead, "/openapi.json")
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("ETag") != etag {
		t.Errorf("HEAD: status %d, %d body bytes, ETag %q", head.Code, head.Body.Len(), head.Header().Get("ETag"))
	}
}

func TestOpenAPIAbsent(t *testing.T) {
	t.Parallel()

	mux := NewMux(&Options{})
	for _, p := range []string{"/openapi.json", "/docs"} {
		if rec := serve(t, mux, http.MethodGet, p); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d without a document, want 404", p, rec.Code)
		}
	}
	ps := Patterns(&Options{})
	if slices.Contains(ps, PatternOpenAPI) || slices.Contains(ps, PatternDocs) {
		t.Errorf("Patterns without a document = %v", ps)
	}
}

func TestDocsDisabled(t *testing.T) {
	t.Parallel()

	mux := NewMux(&Options{OpenAPI: []byte("{}")})
	rec := serve(t, mux, http.MethodGet, "/docs")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 while XI_ADMIN_DOCS is off", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "/openapi.json") || !strings.Contains(body, "XI_ADMIN_DOCS") {
		t.Errorf("body %q does not point at /openapi.json and XI_ADMIN_DOCS", body)
	}
}

func TestDocsEnabled(t *testing.T) {
	t.Parallel()

	mux := NewMux(&Options{OpenAPI: []byte("{}"), Docs: true})
	rec := serve(t, mux, http.MethodGet, "/docs")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`src="` + scalarScript + `"`,
		`integrity="` + scalarIntegrity + `"`,
		`data-url="openapi.json"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src " + scalarScript + ";", "connect-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if strings.Contains(csp, "unsafe-eval") || strings.Contains(csp, "script-src 'unsafe-inline'") {
		t.Errorf("CSP %q allows inline or eval scripts", csp)
	}
	if !strings.HasPrefix(scalarIntegrity, "sha384-") || !strings.Contains(scalarScript, "@1.") {
		t.Errorf("Scalar must be pinned to a version with an SRI hash: %s %s", scalarScript, scalarIntegrity)
	}
}

func TestPatternsMatchMux(t *testing.T) {
	t.Parallel()

	opts := Options{Metrics: http.NotFoundHandler(), OpenAPI: []byte("{}"), Pprof: true}
	mux := NewMux(&opts)
	for _, p := range Patterns(&opts) {
		method, path, _ := strings.Cut(p, " ")
		req := httptest.NewRequestWithContext(t.Context(), method, path, http.NoBody)
		if _, got := mux.Handler(req); got != p {
			t.Errorf("%s routed to %q", p, got)
		}
	}
}
