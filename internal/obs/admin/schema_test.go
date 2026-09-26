package admin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/config"
)

// TestConfigSchemaRoute serves the embedded config schema (the bytes of
// `config schema`) with its media type and a strong ETag that answers
// If-None-Match with 304.
func TestConfigSchemaRoute(t *testing.T) {
	t.Parallel()

	schema := config.Schema()
	mux := NewMux(Options{ConfigSchema: schema})

	rec := serve(t, mux, http.MethodGet, ConfigSchemaPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/schema+json" {
		t.Errorf("Content-Type %q, want application/schema+json", ct)
	}
	if !bytes.Equal(rec.Body.Bytes(), schema) {
		t.Error("body differs from config.Schema()")
	}
	etag := rec.Header().Get("ETag")
	if len(etag) < 3 || etag[0] != '"' || etag[len(etag)-1] != '"' {
		t.Fatalf("ETag %q is not a strong entity tag", etag)
	}
	// The same bytes always carry the same tag.
	if again := serve(t, NewMux(Options{ConfigSchema: config.Schema()}), http.MethodGet, ConfigSchemaPath); again.Header().Get("ETag") != etag {
		t.Errorf("ETag changed between muxes: %q vs %q", etag, again.Header().Get("ETag"))
	}

	for _, tc := range []struct {
		name, ifNoneMatch string
		want              int
	}{
		{"match", etag, http.StatusNotModified},
		{"list with match", `"other", ` + etag, http.StatusNotModified},
		{"any", "*", http.StatusNotModified},
		{"stale", `"stale"`, http.StatusOK},
		{"weak match", "W/" + etag, http.StatusNotModified}, // If-None-Match uses the weak comparison (RFC 9110 §13.1.2)
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, ConfigSchemaPath, http.NoBody)
			req.Header.Set("If-None-Match", tc.ifNoneMatch)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status %d, want %d", rec.Code, tc.want)
			}
			if tc.want == http.StatusNotModified && rec.Body.Len() != 0 {
				t.Errorf("304 with a %d-byte body", rec.Body.Len())
			}
		})
	}

	t.Run("head", func(t *testing.T) {
		t.Parallel()
		rec := serve(t, mux, http.MethodHead, ConfigSchemaPath)
		if rec.Code != http.StatusOK || rec.Body.Len() != 0 || rec.Header().Get("ETag") != etag {
			t.Errorf("HEAD: status %d, %d-byte body, ETag %q", rec.Code, rec.Body.Len(), rec.Header().Get("ETag"))
		}
	})

	t.Run("unregistered without a schema", func(t *testing.T) {
		t.Parallel()
		if rec := serve(t, NewMux(Options{}), http.MethodGet, ConfigSchemaPath); rec.Code != http.StatusNotFound {
			t.Errorf("status %d, want 404", rec.Code)
		}
	})
}
