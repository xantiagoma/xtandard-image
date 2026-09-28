package apidoc

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgix"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/playground"
	"github.com/xantiagoma/xtandard-image/internal/playground/translate"
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
		Samples: playground.Samples(), Uploads: true, MaxUploadBytes: 1 << 20, Classes: []string{"face", "dog"},
	}
	gallery, err := playground.Gallery(func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	meta.Gallery = gallery
	if meta.Recipes, err = playground.Recipes(); err != nil {
		t.Fatal(err)
	}
	meta.Watermark = true
	tr, err := translate.New(translate.Options{Mounts: meta.Mounts})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.jpg"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := playground.New(playground.Config{Path: playground.DefaultPath}, &playground.Deps{
		Meta: meta, Image: http.NotFoundHandler(), Logger: slog.New(slog.DiscardHandler),
		Store:      playground.NewStore(playground.StoreOptions{Uploads: true, MaxBytes: 1 << 20}),
		FSRoots:    map[string]playground.FSRoot{"local": {Root: root}},
		Translator: tr,
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
		{schemaPlaygroundTranslateRes, do(http.MethodPost, "/_playground/api/translate", `{"mount":"imgproxy","path":"/rs:fill:300:200/g:sm/plain/https://example.com/a.jpg"}`)},
		{schemaPlaygroundError, do(http.MethodPost, "/_playground/api/translate", `{"mount":"imgproxy","path":"/rs:nope/plain/a"}`)},
		{schemaPlaygroundUpload, do(http.MethodPost, "/_playground/api/upload", "GIF89a")},
		{schemaPlaygroundList, do(http.MethodGet, "/_playground/api/sources/local/list", "")},
		{schemaPlaygroundError, do(http.MethodGet, "/_playground/api/sources/nope/list", "")},
	} {
		if err := validateJSON(t, schemaValidator(t, tc.schema), tc.body); err != nil {
			t.Errorf("%s: %v\n%s", tc.schema, err, tc.body)
		}
	}
	if err := validateJSON(t, schemaValidator(t, schemaPlaygroundSignRequest), []byte(`{"mount":"imgproxy","path":"/plain/a.jpg"}`)); err != nil {
		t.Error(err)
	}
	if err := validateJSON(t, schemaValidator(t, schemaPlaygroundTranslateReq), []byte(`{"mount":"imgproxy","path":"/plain/a.jpg"}`)); err != nil {
		t.Error(err)
	}
}

// TestCataloguesCoverImgproxy checks that the playground catalogue carries
// every processing option that is not removed, with its aliases.
func TestCataloguesCoverImgproxy(t *testing.T) {
	t.Parallel()
	cats := Catalogues()
	if len(cats) != 10 || cats[0].Grammar != playground.GrammarImgproxy || cats[1].Grammar != playground.GrammarQuery ||
		cats[2].Grammar != playground.GrammarImgix {
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

// TestInfoOptionsCoverImgproxy: the Info panel's catalogue lists every
// /info option with its aliases, forms, default, security flag and a panel
// group.
func TestInfoOptionsCoverImgproxy(t *testing.T) {
	t.Parallel()
	got := map[string]playground.Option{}
	for _, o := range InfoOptions() {
		got[o.Name] = o
	}
	want := imgproxy.InfoOptions()
	for i := range want {
		o := &want[i]
		p, ok := got[o.Name]
		switch {
		case !ok:
			t.Errorf("info option %s is missing", o.Name)
		case strings.Join(p.Aliases, ",") != strings.Join(o.Aliases, ","):
			t.Errorf("%s aliases %v, want %v", o.Name, p.Aliases, o.Aliases)
		case len(p.Forms) != len(o.Forms):
			t.Errorf("%s has %d forms, want %d", o.Name, len(p.Forms), len(o.Forms))
		case p.Group == "" || p.Group == groupOther:
			t.Errorf("%s has no Info panel group (%q)", o.Name, p.Group)
		case p.Default != o.Default || p.Security != o.Security:
			t.Errorf("%s default %q security %v, want %q %v", o.Name, p.Default, p.Security, o.Default, o.Security)
		}
	}
	if len(got) != len(want) {
		t.Errorf("%d info options, want %d", len(got), len(want))
	}
}

// TestImgixCatalogueKnown checks that the imgix parser knows every name and
// alias of the imgix catalogue (and so of getImgixImage): none is recorded
// as unknown or unsupported.
func TestImgixCatalogueKnown(t *testing.T) {
	t.Parallel()
	p, err := imgix.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range imgixCatalogue().Options {
		sample := "1"
		if e := o.Forms[0].Args[0].Enum; len(e) > 0 {
			sample = e[0]
		}
		for _, name := range append([]string{o.Name}, o.Aliases...) {
			res, err := p.Parse(&dialect.Input{Path: "/a.jpg", RawQuery: name + "=" + sample})
			if err != nil {
				continue // a known name with an invalid sample value
			}
			for _, ig := range res.Request.Ignored {
				if ig.Reason == "unknown option" || ig.Reason == "unsupported" {
					t.Errorf("%s: recorded as %s", name, ig.Reason)
				}
			}
		}
	}
}
