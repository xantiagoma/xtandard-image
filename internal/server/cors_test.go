package server

import (
	"net/http"
	"slices"
	"testing"
)

func TestCORS(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, method, origin, reqMethod string
		origins                         []string
		status                          int
		allow                           string
		varyOrigin                      bool
	}{
		{"off", http.MethodGet, "https://a.example", "", nil, http.StatusOK, "", false},
		{"listed origin", http.MethodGet, "https://a.example", "", []string{"https://a.example"}, http.StatusOK, "https://a.example", true},
		{"other origin", http.MethodGet, "https://b.example", "", []string{"https://a.example"}, http.StatusOK, "", false},
		{"any origin", http.MethodGet, "https://b.example", "", []string{"*"}, http.StatusOK, "*", false},
		{"no origin header", http.MethodGet, "", "", []string{"*"}, http.StatusOK, "", false},
		{"preflight", http.MethodOptions, "https://a.example", "GET", []string{"https://a.example"}, http.StatusNoContent, "https://a.example", true},
		{"preflight of a POST", http.MethodOptions, "https://a.example", "POST", []string{"*"}, http.StatusMethodNotAllowed, "*", false},
		{"preflight off", http.MethodOptions, "https://a.example", "GET", nil, http.StatusMethodNotAllowed, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, func(h *harness) { h.cfg.CORSOrigins = tc.origins })
			hdr := http.Header{}
			if tc.origin != "" {
				hdr.Set("Origin", tc.origin)
			}
			if tc.reqMethod != "" {
				hdr.Set("Access-Control-Request-Method", tc.reqMethod)
			}
			rec := h.do(t, tc.method, okPath, hdr)
			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != tc.allow {
				t.Errorf("Access-Control-Allow-Origin %q, want %q", got, tc.allow)
			}
			if got := slices.Contains(rec.Header().Values("Vary"), "Origin"); got != tc.varyOrigin {
				t.Errorf("Vary %q", rec.Header().Values("Vary"))
			}
			if tc.status == http.StatusNoContent && rec.Header().Get("Access-Control-Allow-Headers") == "" {
				t.Error("preflight without allowed headers")
			}
			if tc.allow != "" && tc.method == http.MethodGet && rec.Header().Get("Access-Control-Expose-Headers") == "" {
				t.Error("no exposed headers")
			}
		})
	}
}
