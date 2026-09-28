package playground

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

func fetchAll(ctx context.Context, t *testing.T, s *Store, path string) ([]byte, error) {
	t.Helper()
	obj, err := s.Fetch(ctx, source.Key{Source: source.PlaygroundSourceName, Path: path}, source.FetchOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = obj.Body.Close() }()
	return io.ReadAll(obj.Body)
}

func TestStoreUploads(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	s := NewStore(StoreOptions{Uploads: true, MaxBytes: 16, Now: func() time.Time { return now }})
	u, err := s.Put([]byte("\x89PNG\r\n\x1a\nabc"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u.Source, "playground://uploads/") || u.Size != 11 || !u.Expires.Equal(now.Add(UploadTTL)) {
		t.Errorf("upload = %+v", u)
	}
	path := strings.TrimPrefix(u.Source, "playground://")
	ctx := withRender(t.Context())
	if b, err := fetchAll(ctx, t, s, path); err != nil || string(b) != "\x89PNG\r\n\x1a\nabc" {
		t.Errorf("render fetch = %q, %v", b, err)
	}
	// Outside api/render the store answers not found, like a missing file.
	if _, err := fetchAll(t.Context(), t, s, path); !errors.Is(err, errs.Match(errs.KindNotFound)) {
		t.Errorf("public fetch error = %v, want not found", err)
	}
	if _, err := s.Put(make([]byte, 17)); !errors.Is(err, errs.Match(errs.KindSourceTooLarge)) {
		t.Errorf("oversize put error = %v", err)
	}
	if _, err := s.Put(nil); err == nil {
		t.Error("empty put succeeded")
	}
	now = now.Add(UploadTTL)
	if _, err := fetchAll(ctx, t, s, path); !errors.Is(err, errs.Match(errs.KindNotFound)) {
		t.Errorf("expired fetch error = %v, want not found", err)
	}
	if NewStore(StoreOptions{MaxBytes: 16}).UploadsEnabled() {
		t.Error("uploads enabled by default")
	}
}

func TestStoreEvictsOldest(t *testing.T) {
	t.Parallel()
	s := NewStore(StoreOptions{Uploads: true, MaxBytes: 4})
	var first string
	for i := range maxUploads + 1 {
		u, err := s.Put([]byte("abcd"))
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = strings.TrimPrefix(u.Source, "playground://")
		}
	}
	if _, err := fetchAll(withRender(t.Context()), t, s, first); err == nil {
		t.Error("the oldest upload survived the cap")
	}
}

// TestSamplesMatchFixtures keeps the embedded samples byte-identical to
// the repository fixtures they copy.
func TestSamplesMatchFixtures(t *testing.T) {
	t.Parallel()
	copies := map[string]string{
		"photo.jpg": "jpeg/baseline.jpg", "alpha.png": "png/rgba8.png", "animated.gif": "gif/animated.gif",
		"logo.svg": "svg/logo.svg", "raw.dng": "dng/landscape.dng", "document.pdf": "pdf/pages.pdf",
		"video.mp4": "video/clip-av1.mp4", "faces.jpg": "detect/apollo11-crew.jpg",
		"people-dogs.jpg": "detect/astronaut-dogs.jpg", "cat.jpg": "detect/cat-laptop.jpg",
	}
	s := NewStore(StoreOptions{})
	for _, sm := range Samples() {
		fixture, ok := copies[sm.Name]
		if !ok {
			t.Errorf("sample %s is not a known fixture copy", sm.Name)
			continue
		}
		want, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", fixture))
		if err != nil {
			t.Fatal(err)
		}
		got, err := fetchAll(withRender(t.Context()), t, s, "samples/"+sm.Name)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("sample %s differs from testdata/fixtures/%s (err %v)", sm.Name, fixture, err)
		}
	}
	if _, err := fetchAll(withRender(t.Context()), t, s, "samples/nope.jpg"); err == nil {
		t.Error("unknown sample fetched")
	}
}

// licences are the SPDX identifiers the gallery may list: permissive
// only (the files are linked, never vendored; WP-100).
var licences = map[string]bool{
	"MIT": true, "MIT-CMU": true, "CC0-1.0": true, "CC-BY-4.0": true, "CC-BY-3.0": true, "Apache-2.0": true,
	"BSD-2-Clause": true, "BSD-3-Clause": true, "IJG": true, "ImageMagick": true,
	"LicenseRef-public-domain": true, "LicenseRef-Kodak-unrestricted": true,
}

// largeBytes is the size from which a gallery file must be tagged "large".
const largeBytes = 20 << 20

func TestGallery(t *testing.T) {
	t.Parallel()
	g, err := Gallery(func(u string) bool { return strings.Contains(u, "cgohlke") })
	if err != nil {
		t.Fatal(err)
	}
	formats := map[string]bool{}
	urls := map[string]bool{}
	for _, s := range g {
		formats[s.Format] = true
		if !licences[s.Licence] || s.Source == "" || s.Name == "" || s.Variant == "" || s.Bytes <= 0 || !strings.HasPrefix(s.URL, "https://") {
			t.Errorf("incomplete entry %+v", s)
		}
		if urls[s.URL] {
			t.Errorf("%s listed twice", s.URL)
		}
		urls[s.URL] = true
		if large := slices.Contains(s.Tags, "large"); large != (s.Bytes > largeBytes) {
			t.Errorf("%s: %d bytes, tagged large = %v", s.URL, s.Bytes, large)
		}
		for _, tag := range s.Tags {
			if tag == "" || strings.ToLower(tag) != tag || strings.ContainsRune(tag, ' ') {
				t.Errorf("%s: tag %q is not a lower-case keyword", s.URL, tag)
			}
		}
		if strings.HasPrefix(s.Licence, "CC-BY") && !strings.Contains(s.Source, "by ") && !strings.Contains(s.Source, "Foundation") {
			t.Errorf("%s: CC-BY entry without attribution: %q", s.URL, s.Source)
		}
		if s.Allowed != strings.Contains(s.URL, "cgohlke") {
			t.Errorf("%s: allowed = %v", s.URL, s.Allowed)
		}
	}
	for _, f := range []string{"jpeg", "png", "gif", "webp", "avif", "heic", "jxl", "tiff", "bmp", "ico", "svg", "pdf", "raw", "mp4", "webm", "jxr", "tga", "pcx", "dds", "qoi", "pnm", "xbm", "xpm"} {
		if !formats[f] {
			t.Errorf("no %s sample in the gallery", f)
		}
	}
	// The variant corpus (WP-100): camera RAW per brand family and the
	// colour types photographers and designers produce.
	tagged := func(tag string) int {
		n := 0
		for _, s := range g {
			if slices.Contains(s.Tags, tag) {
				n++
			}
		}
		return n
	}
	for _, tag := range []string{"cr3", "nef", "arw", "raf", "orf", "rw2", "pef", "dng", "iiq", "x3f", "cmyk", "lab", "grey", "1-bit", "palette", "16-bit", "32-bit-float", "alpha", "animated", "multi-page", "orientation", "icc", "wide-gamut", "hdr", "gain-map", "gap"} {
		if tagged(tag) == 0 {
			t.Errorf("no gallery sample tagged %q", tag)
		}
	}
}

func newAPIFixture(t *testing.T, tr Translator) http.Handler {
	t.Helper()
	root := t.TempDir()
	for _, p := range []string{"photos/a.jpg", "photos/sub/b.png", "top.gif", ".hidden"} {
		full := filepath.Join(root, "jail", p)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h, err := New(Config{Path: DefaultPath}, &Deps{
		Meta: testMeta(), Image: &fakeImage{}, Logger: slog.New(slog.DiscardHandler),
		Store:      NewStore(StoreOptions{Uploads: true, MaxBytes: 32}),
		FSRoots:    map[string]FSRoot{"local": {Root: root, Prefix: "jail/"}},
		Translator: tr,
		Signers:    newFixture(t).signers,
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func serve(t *testing.T, h http.Handler, req *http.Request) (int, []byte) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func TestUploadEndpoint(t *testing.T) {
	t.Parallel()
	h := newAPIFixture(t, nil)
	up := func(body string, header ...string) *http.Request {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/_playground/api/upload", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/octet-stream")
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		return req
	}
	code, body := serve(t, h, up("GIF89a-some-bytes"))
	var u Upload
	if code != http.StatusCreated || json.Unmarshal(body, &u) != nil || !strings.HasPrefix(u.Source, "playground://uploads/") {
		t.Fatalf("upload = %d %s", code, body)
	}
	if code, _ := serve(t, h, up(strings.Repeat("x", 33))); code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize upload = %d, want 413", code)
	}
	if code, _ := serve(t, h, up("abc", "Sec-Fetch-Site", "cross-site")); code != http.StatusForbidden {
		t.Errorf("cross-site upload = %d, want 403", code)
	}
	// Disabled: the route does not exist.
	off, err := New(Config{Path: DefaultPath}, &Deps{
		Meta: testMeta(), Image: &fakeImage{}, Logger: slog.New(slog.DiscardHandler),
		Store: NewStore(StoreOptions{MaxBytes: 32}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := serve(t, off, up("abc")); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
		t.Errorf("disabled upload = %d, want 404/405", code)
	}
}

func TestListEndpoint(t *testing.T) {
	t.Parallel()
	h := newAPIFixture(t, nil)
	list := func(q string) (int, ListResponse) {
		code, body := serve(t, h, get(t, "/_playground/api/sources/local/list"+q))
		var r ListResponse
		_ = json.Unmarshal(body, &r)
		return code, r
	}
	code, r := list("")
	want := []ListEntry{{Name: "photos", Key: "photos/", Dir: true}, {Name: "top.gif", Key: "top.gif", Size: 1}}
	if code != http.StatusOK || !cmp.Equal(r.Entries, want) {
		t.Errorf("root = %d %+v", code, r)
	}
	code, r = list("?prefix=photos/")
	want = []ListEntry{{Name: "sub", Key: "photos/sub/", Dir: true}, {Name: "a.jpg", Key: "photos/a.jpg", Size: 1}}
	if code != http.StatusOK || r.Prefix != "photos" || !cmp.Equal(r.Entries, want) {
		t.Errorf("photos = %d %+v", code, r)
	}
	for _, bad := range []string{"?prefix=..", "?prefix=photos/../..", "?prefix=a%00b", "?prefix=./photos"} {
		if code, _ := list(bad); code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", bad, code)
		}
	}
	if code, _ := list("?prefix=nope"); code != http.StatusNotFound {
		t.Errorf("missing dir = %d, want 404", code)
	}
	if code, _ := serve(t, h, get(t, "/_playground/api/sources/other/list")); code != http.StatusNotFound {
		t.Errorf("unknown source = %d, want 404", code)
	}
}

// fakeTranslator returns fixed targets.
type fakeTranslator struct{ err error }

func (f fakeTranslator) Translate(*TranslateRequest) (*Translation, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &Translation{
		Canonical: map[string]any{"Kind": "process"},
		Targets: []TranslateTarget{
			{Dialect: "imgproxy", Mount: "imgproxy", Path: "/rs:fit:10:0/plain/https://e.com/a.jpg"},
			{Dialect: "cloudflare", Path: "/cdn-cgi/image/width=10/https://e.com/a.jpg"},
			{Dialect: "bunny", Error: "no source"},
			{Dialect: "simple:ipx", Mount: "simple:ipx", Path: "/w_10/a.jpg"},
		},
	}, nil
}

func TestTranslateEndpoint(t *testing.T) {
	t.Parallel()
	h := newAPIFixture(t, fakeTranslator{})
	tr := func(body string) (int, []byte) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/_playground/api/translate", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		return serve(t, h, req)
	}
	code, body := tr(`{"mount":"imgproxy","path":"/rs:fit:10:0/plain/https://e.com/a.jpg"}`)
	var res TranslateResponse
	if code != http.StatusOK || json.Unmarshal(body, &res) != nil || len(res.Targets) != 4 {
		t.Fatalf("translate = %d %s", code, body)
	}
	if ip := res.Targets[0]; !ip.Signed || !strings.HasPrefix(ip.URL, "/img/") || strings.Contains(ip.URL, "insecure") {
		t.Errorf("imgproxy target not signed: %+v", ip)
	}
	if cf := res.Targets[1]; cf.Signed || cf.URL != "/img/cdn-cgi/image/width=10/https://e.com/a.jpg" {
		t.Errorf("unmounted target = %+v", cf)
	}
	if ipx := res.Targets[3]; ipx.URL != "/img/_ipx/w_10/a.jpg" ||
		!cmp.Equal(ipx.Alternates, []TranslateAlternate{{Prefix: "/simple/ipx/", URL: "/img/simple/ipx/w_10/a.jpg"}}) {
		t.Errorf("target on a mount with two prefixes = %+v", ipx)
	}
	if ip := res.Targets[0]; len(ip.Alternates) != 0 {
		t.Errorf("single-prefix mount has alternates: %+v", ip.Alternates)
	}
	for _, bad := range []string{`{}`, `{"mount":"nope","path":"/"}`, `{"mount":"imgproxy","path":"/a b"}`, `{"mount":"imgproxy","request":{}}`} {
		if code, _ := tr(bad); code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", bad, code)
		}
	}
	failing := newAPIFixture(t, fakeTranslator{err: &errs.Error{Kind: errs.KindInvalidURL, Public: "invalid width: expected integer"}})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/_playground/api/translate",
		strings.NewReader(`{"mount":"imgproxy","path":"/w:x/plain/a"}`))
	req.Header.Set("Content-Type", "application/json")
	if code, body := serve(t, failing, req); code != http.StatusBadRequest || !strings.Contains(string(body), "invalid width") {
		t.Errorf("parse error = %d %s", code, body)
	}
}

// TestModulesServed: every module app.js imports is served next to it.
func TestModulesServed(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, page := f.do(t, get(t, "/_playground/"))
	m := regexp.MustCompile(`assets/([0-9a-f]{12})/app\.js`).FindSubmatch(page)
	if m == nil {
		t.Fatal("no versioned app.js")
	}
	app, err := fs.ReadFile(assets, "assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	imports := regexp.MustCompile(`from '\./([a-z0-9-]+\.js)'`).FindAllSubmatch(app, -1)
	if len(imports) < 5 {
		t.Fatalf("app.js imports %d modules", len(imports))
	}
	for _, im := range imports {
		res, body := f.do(t, get(t, "/_playground/assets/"+string(m[1])+"/"+string(im[1])))
		if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "text/javascript; charset=utf-8" || len(body) == 0 {
			t.Errorf("%s: %d %q", im[1], res.StatusCode, res.Header.Get("Content-Type"))
		}
	}
}

// renderProbe records whether the image handler saw the render mark.
type renderProbe struct{ marked bool }

func (p *renderProbe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.marked = isRender(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

func TestRenderMarksContext(t *testing.T) {
	t.Parallel()
	probe := &renderProbe{}
	h, err := New(Config{Path: DefaultPath}, &Deps{Meta: testMeta(), Image: probe, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	serve(t, h, get(t, "/_playground/api/render?u=/a"))
	if !probe.marked {
		t.Error("api/render did not mark its request for the playground store")
	}
}
