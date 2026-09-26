package apidoc

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/playground"
)

// TestContractPlayground validates real api/meta, api/sign and error
// bodies against the playground schemas.
func TestContractPlayground(t *testing.T) {
	t.Parallel()
	meta := &playground.Meta{
		Version: "dev", PublicAddr: ":8080",
		Signing:    playground.Signing{Mode: playground.SigningOff, UnsignedPolicy: "restricted"},
		Catalogues: Catalogues(),
		Mounts: []playground.Mount{{
			ID: "imgproxy", Dialect: "imgproxy", Prefix: "/", UnsignedPolicy: "allow",
			Scheme: playground.SchemeImgproxy, Grammar: playground.GrammarImgproxy, ArgumentsSeparator: ":",
		}},
		Presets: []string{"thumb"},
		Sources: []playground.Source{{Name: "local", Type: "fs"}},
		Formats: []string{"jpeg", "png", "webp"},
	}
	h, err := playground.New(playground.Config{Path: playground.DefaultPath}, &playground.Deps{
		Meta: meta, Image: http.NotFoundHandler(), Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path, body string) []byte {
		req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		b, _ := io.ReadAll(rec.Body)
		return b
	}
	for _, tc := range []struct {
		schema string
		body   []byte
	}{
		{schemaPlaygroundMeta, do(http.MethodGet, "/_playground/api/meta", "")},
		{schemaPlaygroundSignResponse, do(http.MethodPost, "/_playground/api/sign", `{"mount":"imgproxy","path":"/rs:fit:300:0/plain/https://example.com/a.jpg"}`)},
		{schemaPlaygroundError, do(http.MethodPost, "/_playground/api/sign", `{"mount":"nope","path":"/x"}`)},
	} {
		if err := validateJSON(t, schemaValidator(t, tc.schema), tc.body); err != nil {
			t.Errorf("%s: %v\n%s", tc.schema, err, tc.body)
		}
	}
	if err := validateJSON(t, schemaValidator(t, schemaPlaygroundSignRequest), []byte(`{"mount":"imgproxy","path":"/plain/a.jpg"}`)); err != nil {
		t.Error(err)
	}
}

// TestCataloguesCoverImgproxy checks that the playground catalogue carries
// every processing option that is not removed, with its aliases.
func TestCataloguesCoverImgproxy(t *testing.T) {
	t.Parallel()
	cats := Catalogues()
	if len(cats) != 2 || cats[0].Grammar != playground.GrammarImgproxy || cats[1].Grammar != playground.GrammarQuery {
		t.Fatalf("catalogues %+v", cats)
	}
	got := map[string][]string{}
	for _, o := range cats[0].Options {
		got[o.Name] = o.Aliases
		if len(o.Forms) == 0 {
			t.Errorf("%s has no forms", o.Name)
		}
	}
	for _, o := range imgproxy.ProcessingOptions() {
		aliases, ok := got[o.Name]
		switch {
		case o.Removed && ok:
			t.Errorf("removed option %s is in the catalogue", o.Name)
		case !o.Removed && !ok:
			t.Errorf("option %s is missing", o.Name)
		case !o.Removed && strings.Join(aliases, ",") != strings.Join(o.Aliases, ","):
			t.Errorf("%s aliases %v, want %v", o.Name, aliases, o.Aliases)
		}
	}
	var width *playground.Option
	for i, o := range cats[1].Options {
		if o.Name == "url" || o.Name == "xi_sig" || o.Name == "sign" {
			t.Errorf("query catalogue lists %s", o.Name)
		}
		if o.Name == "width" {
			width = &cats[1].Options[i]
		}
	}
	if width == nil || strings.Join(width.Aliases, ",") != "w" {
		t.Errorf("query width option %+v, want alias w", width)
	}
}
