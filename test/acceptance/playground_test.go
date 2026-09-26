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
		ID      string `json:"id"`
		Scheme  string `json:"scheme"`
		Keyed   bool   `json:"keyed"`
		Grammar string `json:"grammar"`
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
	for _, m := range meta.Mounts {
		if m.Keyed {
			mounts[m.ID] = m.Scheme
		}
	}
	if meta.Signing.Mode != "optional" || mounts["imgproxy"] != "imgproxy" || mounts["query"] != "xi_sig" ||
		len(meta.Catalogues) != 2 || len(meta.Catalogues[0].Options) < 50 || len(meta.Sources) != 3 ||
		len(meta.Presets) != 1 || meta.Presets[0] != "thumb" {
		t.Errorf("meta: %s", truncate(metaRes.Body))
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

	rendered := get(t, admin+"/_playground/api/render?u="+url.QueryEscape(s.URL), http.Header{"Accept": {"image/webp"}})
	rendered.wantStatus(t, http.StatusOK)
	if !strings.HasPrefix(rendered.Header.Get("Server-Timing"), "xi;") {
		t.Errorf("render Server-Timing %q", rendered.Header.Get("Server-Timing"))
	}
	// Accept is forwarded, so the in-process render negotiates WebP.
	wantImage(t, rendered, "image/webp", props{Format: "webp", Width: 64, Height: 48})
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
