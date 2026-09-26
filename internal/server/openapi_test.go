package server

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// TestAdminDocsNotPublic checks that the admin-only documents (ADR 0008
// decision 3, ADR 0007 decision 6) are not routes on the public listener:
// they reach the dialect catch-all like any image URL.
func TestAdminDocsNotPublic(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(h *harness) { h.router.err = dialect.InvalidURL("not an image url") })
	for _, p := range []string{"/openapi.json", "/docs", "/config.schema.json"} {
		rec := h.do(t, http.MethodGet, p, nil)
		if rec.Code != http.StatusBadRequest || strings.Contains(rec.Body.String(), "openapi") {
			t.Errorf("public %s = %d %q, want the dialect's 400", p, rec.Code, rec.Body.String())
		}
	}
	for _, p := range Patterns() {
		if strings.Contains(p, "openapi") || strings.Contains(p, "docs") || strings.Contains(p, "schema") {
			t.Errorf("public pattern %q serves a document", p)
		}
	}
	if !slices.Contains(Patterns(), PatternImage) {
		t.Errorf("Patterns() = %v lacks the image catch-all", Patterns())
	}
}

// TestErrorJSONIsErrorResponse checks the wire body against the exported
// type the OpenAPI document reflects.
func TestErrorJSONIsErrorResponse(t *testing.T) {
	t.Parallel()
	for _, k := range errs.Kinds() {
		var got ErrorResponse
		dec := json.NewDecoder(strings.NewReader(string(errorJSON(k, errs.PublicMessage(k), "req-1"))))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&got); err != nil {
			t.Fatalf("%v: %v", k, err)
		}
		if diff := cmp.Diff(NewErrorResponse(k, errs.PublicMessage(k), "req-1"), got); diff != "" {
			t.Errorf("%v (-want +got):\n%s", k, diff)
		}
	}
}
