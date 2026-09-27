//go:build acceptance

package acceptance

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// playgroundMeta is the part of GET /_playground/api/meta the suite reads
// (internal/playground.Meta, re-declared: the suite imports nothing of the
// server).
type playgroundMeta struct {
	Signing struct {
		Mode string `json:"mode"`
	} `json:"signing"`
	Mounts []struct {
		ID       string   `json:"id"`
		Scheme   string   `json:"scheme"`
		Keyed    bool     `json:"keyed"`
		Grammar  string   `json:"grammar"`
		Prefixes []string `json:"prefixes"`
	} `json:"mounts"`
	Catalogues []struct {
		Grammar string            `json:"grammar"`
		Options []json.RawMessage `json:"options"`
	} `json:"catalogues"`
	Sources []struct {
		Name string `json:"name"`
	} `json:"sources"`
	Presets []string `json:"presets"`
}

type signed struct {
	URL    string `json:"url"`
	Signed bool   `json:"signed"`
	Scheme string `json:"scheme"`
}

func postSign(t *testing.T, admin, body string) signed {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, admin+"/_playground/api/sign", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST api/sign: %v", err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("POST api/sign %s: %d %s", body, res.StatusCode, truncate(b))
	}
	var s signed
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestPlayground is the WP-80 end-to-end smoke test: with
// XI_PLAYGROUND_ENABLED the admin listener serves the page, its assets and
// api/meta (without key material), api/sign signs a URL with the server's
// keys that the public listener then serves as an image, and api/render
// shows the same result in-process; the public listener has no playground.
func TestPlayground(t *testing.T) {
	t.Parallel()
	app, admin := base(t, "XI_ACC_APP"), base(t, "XI_ACC_ADMIN")
	k := loadKeys(t)

	page := get(t, admin+"/_playground/", nil)
	page.wantStatus(t, http.StatusOK)
	if ct := page.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("page Content-Type %q", ct)
	}
	if csp := page.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") || page.Header.Get("X-Frame-Options") != "DENY" {
		t.Errorf("page CSP %q, X-Frame-Options %q", csp, page.Header.Get("X-Frame-Options"))
	}
	asset := regexp.MustCompile(`assets/[0-9a-f]+/app\.js`).Find(page.Body)
	if asset == nil {
		t.Fatalf("page has no app.js: %s", truncate(page.Body))
	}
	js := get(t, admin+"/_playground/"+string(asset), nil)
	js.wantStatus(t, http.StatusOK)
	if !strings.Contains(js.Header.Get("Cache-Control"), "immutable") {
		t.Errorf("app.js Cache-Control %q", js.Header.Get("Cache-Control"))
	}

	if r := get(t, app+"/_playground/", nil); r.Status != http.StatusNotFound {
		t.Errorf("public /_playground/ = %d, want 404 (XI_PLAYGROUND_PUBLIC is off)", r.Status)
	}

	metaRes := get(t, admin+"/_playground/api/meta", nil)
	metaRes.wantStatus(t, http.StatusOK)
	for _, secret := range [][]byte{k.key, k.salt, k.enc} {
		for _, form := range []string{hex.EncodeToString(secret), strings.ToUpper(hex.EncodeToString(secret))} {
			if bytes.Contains(metaRes.Body, []byte(form)) {
				t.Fatal("api/meta contains key material")
			}
		}
	}
	var meta playgroundMeta
	if err := json.Unmarshal(metaRes.Body, &meta); err != nil {
		t.Fatal(err)
	}
	mounts := map[string]string{}
	ids := map[string]bool{}
	for _, m := range meta.Mounts {
		if ids[m.ID] {
			t.Errorf("api/meta lists mount %s twice (WP-96: once, with its prefixes)", m.ID)
		}
		ids[m.ID] = true
		if m.ID == "simple:ipx" && strings.Join(m.Prefixes, ",") != "/_ipx/,/simple/ipx/" {
			t.Errorf("simple:ipx prefixes %v", m.Prefixes)
		}
		if m.Keyed {
			mounts[m.ID] = m.Scheme
		}
	}
	if bytes.Contains(metaRes.Body, []byte(env(t, "XI_ACC_IMGIX_TOKEN"))) {
		t.Fatal("api/meta contains the imgix token")
	}
	if meta.Signing.Mode != "optional" || mounts["imgproxy"] != "imgproxy" || mounts["query"] != "xi_sig" ||
		mounts["imgix"] != "imgix" || len(meta.Catalogues) != 10 || len(meta.Catalogues[0].Options) < 50 || len(meta.Sources) != 3 ||
		len(meta.Presets) != 1 || meta.Presets[0] != "thumb" {
		t.Errorf("meta: %s", truncate(metaRes.Body))
	}

	if !ids["simple:ipx"] {
		t.Error("api/meta has no simple:ipx mount")
	}
	// A mount with several prefixes signs on the one asked for.
	if s := postSign(t, admin, `{"mount":"simple:ipx","path":"/w_64/jpeg/baseline.jpg","prefix":"/simple/ipx/"}`); s.URL != "/simple/ipx/w_64/jpeg/baseline.jpg" {
		t.Errorf("simple:ipx on /simple/ipx/: %+v", s)
	}

	// imgproxy: the server's signature equals the suite's own HMAC.
	path := fit64 + "/plain/" + jpegSrc
	s := postSign(t, admin, `{"mount":"imgproxy","path":"`+path+`"}`)
	if !s.Signed || s.URL != k.sign(path) {
		t.Errorf("imgproxy sign %+v, want %s", s, k.sign(path))
	}
	img := get(t, app+s.URL, nil)
	img.wantStatus(t, http.StatusOK)
	wantImage(t, img, "image/jpeg", props{Format: "jpeg", Width: 64, Height: 48})

	// query: xi_sig lifts the restricted policy (w=65 is not allowlisted).
	q := postSign(t, admin, `{"mount":"query","path":"/","query":"url=`+url.QueryEscape(jpegSrc)+`&w=65&format=png"}`)
	if !q.Signed || !strings.Contains(q.URL, "xi_sig=") {
		t.Fatalf("query sign %+v", q)
	}
	qi := get(t, app+q.URL, nil)
	qi.wantStatus(t, http.StatusOK)
	wantImage(t, qi, "image/png", props{Format: "png", Width: 65})

	// imgix: s with the mount's token lifts the restricted policy.
	x := postSign(t, admin, `{"mount":"imgix","path":"/jpeg/baseline.jpg","query":"w=65&fm=png"}`)
	if want := "/imgix/jpeg/baseline.jpg?" + imgixSign(env(t, "XI_ACC_IMGIX_TOKEN"), "/jpeg/baseline.jpg", "w=65&fm=png"); !x.Signed || x.URL != want {
		t.Errorf("imgix sign %+v, want %s", x, want)
	}
	xi := get(t, app+x.URL, nil)
	xi.wantStatus(t, http.StatusOK)
	wantImage(t, xi, "image/png", props{Format: "png", Width: 65, Height: 49})

	rendered := get(t, admin+"/_playground/api/render?u="+url.QueryEscape(s.URL), http.Header{"Accept": {"image/webp"}})
	rendered.wantStatus(t, http.StatusOK)
	if st := strings.Join(rendered.Header.Values("Server-Timing"), ", "); !strings.Contains(st, "xi;") || !strings.Contains(st, "process;dur=") {
		t.Errorf("render Server-Timing %q", st)
	}
	// Accept is forwarded, so the in-process render negotiates WebP.
	wantImage(t, rendered, "image/webp", props{Format: "webp", Width: 64, Height: 48})

	playgroundV2(t, app, admin, path)
}

// postJSONAcc posts body and decodes the JSON answer into out.
func postJSONAcc(t *testing.T, u, ctype string, body []byte, want int, out any) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", ctype)
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", u, err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != want {
		t.Fatalf("POST %s: %d %s", u, res.StatusCode, truncate(b))
	}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatal(err)
	}
}

// playgroundV2 covers WP-94: api/translate writes the URL in every
// dialect (the imgproxy one signed and served by the app), an uploaded
// file and a built-in sample render through api/render and never on the
// public listener.
func playgroundV2(t *testing.T, app, admin, path string) {
	t.Helper()
	var tr struct {
		Targets []struct {
			Dialect, URL, Error string
			Signed              bool
			Unsupported         []string
		} `json:"targets"`
	}
	postJSONAcc(t, admin+"/_playground/api/translate", "application/json",
		[]byte(`{"mount":"imgproxy","path":"`+path+`"}`), http.StatusOK, &tr)
	dialects := map[string]bool{}
	for _, x := range tr.Targets {
		dialects[x.Dialect] = true
	}
	for _, d := range []string{"imgproxy", "query", "imgix", "cloudinary", "imagekit", "cloudflare", "wsrv", "thumbor", "bunny", "awssih"} {
		if !dialects[d] {
			t.Errorf("translate has no %s target", d)
		}
	}
	if ip := tr.Targets[0]; ip.Dialect != "imgproxy" || !ip.Signed {
		t.Errorf("translate imgproxy target %+v", ip)
	} else {
		r := get(t, app+ip.URL, nil)
		r.wantStatus(t, http.StatusOK)
		wantImage(t, r, "image/jpeg", props{Format: "jpeg", Width: 64, Height: 48})
	}

	// Upload a PNG, render it through the playground.
	png := get(t, app+postSign(t, admin, `{"mount":"imgproxy","path":"/rs:fit:32:32/f:png/plain/`+jpegSrc+`"}`).URL, nil)
	png.wantStatus(t, http.StatusOK)
	var up struct {
		Source string `json:"source"`
	}
	postJSONAcc(t, admin+"/_playground/api/upload", "image/png", png.Body, http.StatusCreated, &up)
	if !strings.HasPrefix(up.Source, "playground://uploads/") {
		t.Fatalf("upload source %q", up.Source)
	}
	for _, src := range []string{up.Source, "playground://samples/photo.jpg"} {
		s := postSign(t, admin, `{"mount":"imgproxy","path":"/rs:fit:16:16/f:png/plain/`+src+`"}`)
		r := get(t, admin+"/_playground/api/render?u="+url.QueryEscape(s.URL), nil)
		r.wantStatus(t, http.StatusOK)
		wantImage(t, r, "image/png", props{Format: "png", Width: 16})
		if pub := get(t, app+s.URL, nil); pub.Status != http.StatusNotFound {
			t.Errorf("%s on the public listener: %d, want 404", src, pub.Status)
		}
	}
}

// wantImage checks the Content-Type and decodes the body; a zero Height in
// want is not compared. The acceptance app serves a fallback PNG with 200
// on failures, so the dimensions are what proves the request succeeded.
func wantImage(t *testing.T, r *response, ctype string, want props) {
	t.Helper()
	if got := r.Header.Get("Content-Type"); got != ctype {
		t.Errorf("Content-Type %q, want %q", got, ctype)
	}
	got, err := decodeProps(r.Body)
	if err != nil {
		t.Fatalf("%d-byte body: %v", len(r.Body), err)
	}
	if want.Height == 0 {
		got.Height = 0
	}
	if got != want {
		t.Errorf("image %+v, want %+v", got, want)
	}
}
