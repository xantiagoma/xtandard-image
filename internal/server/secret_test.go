package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/source"
)

// TestSecret covers XI_SECURITY_SECRET: with a secret every route but the
// probes needs "Authorization: Bearer <secret>" (imgproxy IMGPROXY_SECRET),
// and nothing reaches the fetcher without it.
func TestSecret(t *testing.T) {
	t.Parallel()
	bearer := func(v string) http.Header { return http.Header{"Authorization": {v}} }
	tests := []struct {
		name   string
		secret string
		path   string
		hdr    http.Header
		want   int
	}{
		{"no secret configured", "", okPath, nil, http.StatusOK},
		{"right secret", "s3cr3t", okPath, bearer("Bearer s3cr3t"), http.StatusOK},
		{"missing", "s3cr3t", okPath, nil, http.StatusForbidden},
		{"wrong", "s3cr3t", okPath, bearer("Bearer nope"), http.StatusForbidden},
		{"no scheme", "s3cr3t", okPath, bearer("s3cr3t"), http.StatusForbidden},
		{"prefix of the secret", "s3cr3t", okPath, bearer("Bearer s3cr"), http.StatusForbidden},
		{"healthz needs none", "s3cr3t", "/healthz", nil, http.StatusOK},
		{"readyz needs none", "s3cr3t", "/readyz", nil, http.StatusServiceUnavailable}, // starting
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, func(h *harness) { h.cfg.Secret = tc.secret })
			rec := h.do(t, http.MethodGet, tc.path, tc.hdr)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == http.StatusForbidden && h.f.calls.Load() != 0 {
				t.Errorf("rejected request fetched the source %d times", h.f.calls.Load())
			}
			h.checkReleased(t)
		})
	}
}

// TestSecretPlaygroundRender: the playground's in-process renders
// (api/render) carry no Authorization header and are not refused.
func TestSecretPlaygroundRender(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(h *harness) { h.cfg.Secret = "s3cr3t" })
	req, err := http.NewRequestWithContext(source.WithPlaygroundRequest(t.Context()), http.MethodGet, okPath, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("playground render = %d, want 200", rec.Code)
	}
}
