package translate

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/playground"
)

var update = flag.Bool("update", false, "rewrite testdata/roundtrip.json")

func testMounts() []playground.Mount {
	return []playground.Mount{
		{ID: "imgproxy", Dialect: "imgproxy", Prefix: "/", Scheme: playground.SchemeImgproxy, Grammar: playground.GrammarImgproxy},
		{ID: "query", Dialect: "query", Prefix: "/q/", Scheme: playground.SchemeXI, Grammar: playground.GrammarQuery},
		{ID: "imgix", Dialect: "imgix", Prefix: "/imgix/", Source: "local", Scheme: playground.SchemeImgix, Grammar: playground.GrammarImgix},
		{ID: "thumbor", Dialect: "thumbor", Prefix: "/thumbor/", Scheme: playground.SchemeThumbor, Grammar: playground.GrammarThumbor},
		{ID: "bunny", Dialect: "bunny", Prefix: "/bunny/", Source: "local", Scheme: playground.SchemeNone, Grammar: playground.GrammarBunny},
		{ID: "imagekit", Dialect: "imagekit", Prefix: "/imagekit/", Source: "local", Scheme: playground.SchemeNone, Grammar: playground.GrammarImagekit},
		{ID: "awssih", Dialect: "awssih", Prefix: "/sih/", Source: "local", Scheme: playground.SchemeNone, Grammar: playground.GrammarAWSSIH},
		{ID: "cloudinary", Dialect: "cloudinary", Prefix: "/cloudinary/", Source: "local", Scheme: playground.SchemeNone, Grammar: playground.GrammarCloudinary},
	}
}

func newTranslator(t *testing.T) *Translator {
	t.Helper()
	tr, err := New(Options{Mounts: testMounts()})
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// roundTripCases are imgproxy option paths; each is translated twice, from
// an http source and from a key on the mounts' source (xi://local/...).
var roundTripCases = []struct {
	name string
	opts string
	// exact lists the dialects that must reproduce the transform with no
	// unsupported or approximated field.
	exact []string
}{
	{"fit width", "rs:fit:300:0", []string{"imgproxy", "query", "cloudinary", "imagekit", "cloudflare", "thumbor", "bunny", "awssih"}},
	{"fill with smart gravity", "rs:fill:300:200:1/g:sm", []string{"imgproxy", "query", "imgix", "cloudinary", "imagekit", "cloudflare", "wsrv"}},
	{"fill with face gravity", "rs:fill:300:200:1/g:obj:face", []string{"imgproxy", "query", "imgix", "cloudinary", "imagekit", "cloudflare"}},
	{"fill with object classes", "rs:fill:300:200:1/g:obj:dog", []string{"imgproxy", "query", "imgix", "cloudinary", "imagekit"}},
	{"focus point", "rs:fill:300:200:1/g:fp:0.3:0.7", []string{"imgproxy", "query", "imgix", "cloudinary", "cloudflare", "wsrv"}},
	{"compass gravity", "rs:fill:300:200:1/g:nowe", []string{"imgproxy", "query", "imgix", "cloudinary", "imagekit"}},
	{"format and quality", "f:webp/q:70", []string{"imgproxy", "query", "imgix", "cloudinary", "imagekit", "cloudflare", "wsrv", "thumbor", "bunny"}},
	{"blur and sharpen", "bl:2/sh:1", []string{"imgproxy", "query", "imgix", "cloudinary", "cloudflare", "wsrv"}},
	{"rotate and flip", "rot:90/fl:1:0", []string{"imgproxy", "query", "imgix", "cloudinary", "imagekit", "cloudflare", "wsrv", "bunny", "awssih"}},
	{"background and padding", "bg:ff0000/pd:10", []string{"imgproxy", "query", "imgix"}},
	{"colour adjustments", "br:51/co:1.2/sa:0.8", []string{"imgproxy", "query", "imgix"}},
	{"pixelate", "pix:8", []string{"imgproxy", "query", "imgix", "cloudinary"}},
	{"blur faces", "bd:5:face", []string{"imgproxy", "query", "imgix", "cloudinary"}},
	{"text watermark", "wm:0.8:soea:10:10/wmt:" + b64("Hello"), []string{"imgproxy", "query"}},
	{"page", "pg:1", []string{"imgproxy", "query", "imgix", "cloudinary", "imagekit", "wsrv"}},
	{"video second", "vts:1.5", []string{"imgproxy", "query", "cloudinary", "imagekit"}},
	{"filename", "fn:photo.jpg/att:1", []string{"imgproxy", "query", "imgix"}},
	{"chained pipelines", "rs:fit:500:500/-/bl:2", []string{"imgproxy", "query"}},
	{"duotone", "dt:1:000000:ffffff", []string{"imgproxy", "query", "imgix"}},
}

// roundTripResult is one golden row.
type roundTripResult struct {
	Case    string                     `json:"case"`
	Source  string                     `json:"source"`
	Targets map[string]roundTripTarget `json:"targets"`
}

type roundTripTarget struct {
	URL          string   `json:"url,omitempty"`
	Unsupported  []string `json:"unsupported,omitempty"`
	Approximated []string `json:"approximated,omitempty"`
	Error        string   `json:"error,omitempty"`
}

// TestRoundTrip emits every case in every dialect, parses each URL back
// with that dialect's parser (Translate does) and checks that the
// dialects listed as exact reproduce the canonical request. The whole
// matrix is the golden file testdata/roundtrip.json (docs: translate
// coverage per dialect).
func TestRoundTrip(t *testing.T) {
	t.Parallel()
	tr := newTranslator(t)
	rows := make([]roundTripResult, 0, 2*len(roundTripCases))
	for _, tc := range roundTripCases {
		for _, src := range []string{"https://example.com/photos/a.jpg", "xi://local/photos/a.jpg"} {
			res, err := tr.Translate(&playground.TranslateRequest{Mount: "imgproxy", Path: "/" + tc.opts + "/plain/" + src})
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			row := roundTripResult{Case: tc.name, Source: src, Targets: map[string]roundTripTarget{}}
			for _, tg := range res.Targets {
				row.Targets[tg.Dialect] = roundTripTarget{
					URL: withQueryTest(tg.Path, tg.Query), Unsupported: tg.Unsupported, Approximated: tg.Approximated, Error: tg.Error,
				}
				if strings.Contains(tg.Error, "does not parse") {
					t.Errorf("%s (%s): %s: %s", tc.name, src, tg.Dialect, tg.Error)
				}
				if !slices.Contains(tc.exact, tg.Dialect) || tg.Error != "" {
					continue
				}
				if len(tg.Unsupported)+len(tg.Approximated) > 0 {
					t.Errorf("%s (%s): %s %s?%s: unsupported %v, approximated %v",
						tc.name, src, tg.Dialect, tg.Path, tg.Query, tg.Unsupported, tg.Approximated)
				}
			}
			rows = append(rows, row)
		}
	}
	got, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	golden := filepath.Join("testdata", "roundtrip.json")
	if *update {
		if err := os.WriteFile(golden, got, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	if !bytes.Equal(want, got) {
		t.Errorf("round-trip matrix changed (-want +got, run with -update if intended):\n%s", cmp.Diff(string(want), string(got)))
	}
}

func withQueryTest(p, q string) string {
	if q == "" {
		return p
	}
	return p + "?" + q
}

// TestKeyedDialects covers the dialects that address images by path on
// their mount's source (ImageKit, Bunny, AWS SIH): a URL under the base_url
// of that http source is written as a key and round-trips exactly; any
// other absolute URL, or a dialect not bound to a source, gets a reason.
func TestKeyedDialects(t *testing.T) {
	t.Parallel()
	keyed := []string{"imagekit", "bunny", "awssih"}
	mounts := []playground.Mount{
		{ID: "imgproxy", Dialect: "imgproxy", Prefix: "/", Scheme: playground.SchemeImgproxy, Grammar: playground.GrammarImgproxy},
		{ID: "imagekit", Dialect: "imagekit", Prefix: "/imagekit/", Source: "cdn", Grammar: playground.GrammarImagekit},
		{ID: "bunny", Dialect: "bunny", Prefix: "/bunny/", Source: "cdn", Grammar: playground.GrammarBunny},
		{ID: "awssih", Dialect: "awssih", Prefix: "/sih/", Source: "web", Grammar: playground.GrammarAWSSIH},
	}
	tr, err := New(Options{Mounts: mounts, SourceBases: map[string]string{"cdn": "https://cdn.example.com/media/", "web": "ftp://x/"}})
	if err != nil {
		t.Fatal(err)
	}
	unbound, err := New(Options{Mounts: mounts[:1]})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, src string
		tr        *Translator
		// want maps a dialect to its URL, or to "!<text>" for an error
		// containing text.
		want map[string]string
	}{
		{"under base_url", "https://cdn.example.com/media/photos/a%20b.jpg", tr, map[string]string{
			"imagekit": "/tr:w-300,c-at_max/photos/a%20b.jpg",
			"bunny":    "/photos/a%20b.jpg?width=300",
			"awssih":   "!\"web\", and this source is not a key on it",
		}},
		{"outside base_url", "https://other.example.com/media/a.jpg", tr, map[string]string{
			"imagekit": "!nor a URL under its base_url: pick a key on it (xi://cdn/<key>)",
			"bunny":    "!nor a URL under its base_url",
		}},
		{"key on the source", "xi://cdn/photos/a.jpg", tr, map[string]string{
			"imagekit": "/tr:w-300,c-at_max/photos/a.jpg",
		}},
		{"not bound", "https://cdn.example.com/media/a.jpg", unbound, map[string]string{
			"imagekit": "!no mount here binds it to one",
			"bunny":    "!no mount here binds it to one",
			"awssih":   "!no mount here binds it to one",
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := tc.tr.Translate(&playground.TranslateRequest{Mount: "imgproxy", Path: "/rs:fit:300:0/plain/" + tc.src})
			if err != nil {
				t.Fatal(err)
			}
			for _, tg := range res.Targets {
				want, ok := tc.want[tg.Dialect]
				if !ok {
					continue
				}
				if msg, isErr := strings.CutPrefix(want, "!"); isErr {
					if !strings.Contains(tg.Error, msg) || tg.Path != "" {
						t.Errorf("%s: error %q, path %q; want an error containing %q", tg.Dialect, tg.Error, tg.Path, msg)
					}
					continue
				}
				if got := withQueryTest(tg.Path, tg.Query); got != want || tg.Error != "" {
					t.Errorf("%s: %q (error %q), want %q", tg.Dialect, got, tg.Error, want)
				}
				if slices.Contains(keyed, tg.Dialect) && len(tg.Unsupported)+len(tg.Approximated) > 0 {
					t.Errorf("%s: unsupported %v, approximated %v", tg.Dialect, tg.Unsupported, tg.Approximated)
				}
			}
		})
	}
}
