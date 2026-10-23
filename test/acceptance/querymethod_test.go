//go:build acceptance

package acceptance

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const (
	formType    = "application/x-www-form-urlencoded"
	acceptQuery = "application/x-www-form-urlencoded, application/json"
)

// query sends an HTTP QUERY request (RFC 10008, WP-147) with content of
// type ctype.
func query(t *testing.T, method, u, ctype, content string) *response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, u, strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, u, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return &response{Status: res.StatusCode, Header: res.Header, Body: body}
}

// xiSign appends xi_sig to rawQuery for path (the path after the mount):
// base64url-nopad(HMAC-SHA256(key, salt || path "?" query)).
func (k keys) xiSign(path, rawQuery string) string {
	mac := hmac.New(sha256.New, k.key)
	mac.Write(k.salt)
	mac.Write([]byte(path + "?" + rawQuery))
	return rawQuery + "&xi_sig=" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// TestQueryMethod: the query dialect over HTTP QUERY on the release image.
// A signed QUERY renders like its GET URL (same ETag) and answers with
// that URL as Location; the restricted policy applies to unsigned
// content; unsupported content is 415 and other endpoints 405.
func TestQueryMethod(t *testing.T) {
	t.Parallel()
	app := base(t, "XI_ACC_APP")
	k := loadKeys(t)
	// w=300 is not in the restricted allowlist: only the signature admits it.
	signed := k.xiSign("/", "url="+url.QueryEscape(jpegSrc)+"&w=300&f=webp")

	r := query(t, "QUERY", app+"/q/", formType, signed)
	r.wantStatus(t, 200)
	if ct := r.Header.Get("Content-Type"); ct != "image/webp" {
		t.Errorf("Content-Type %q", ct)
	}
	if got := r.Header.Get("Accept-Query"); got != acceptQuery {
		t.Errorf("Accept-Query %q", got)
	}
	if v := strings.Join(r.Header.Values("Vary"), ","); !strings.Contains(v, "Content-Type") {
		t.Errorf("Vary %q", v)
	}
	loc := r.Header.Get("Location")
	if loc != "/q/?"+signed {
		t.Fatalf("Location %q", loc)
	}
	g := get(t, app+loc, nil)
	g.wantStatus(t, 200)
	if g.Header.Get("ETag") != r.Header.Get("ETag") {
		t.Errorf("GET ETag %q, QUERY ETag %q", g.Header.Get("ETag"), r.Header.Get("ETag"))
	}

	for _, tc := range []struct {
		name, method, path, ctype, content string
		status                             int
		allow                              string
	}{
		{"json restricted allowed", "QUERY", "/q/", "application/json", `{"url":"` + jpegSrc + `","w":64}`, 200, ""},
		{"form restricted refused", "QUERY", "/q/", formType, "url=" + url.QueryEscape(jpegSrc) + "&w=300", 403, ""},
		{"tampered", "QUERY", "/q/", formType, signed + "&w=301", 403, ""},
		{"unsupported media type", "QUERY", "/q/", "text/plain", signed, 415, ""},
		{"url query too", "QUERY", "/q/?w=1", formType, signed, 400, ""},
		{"post on the query dialect", "POST", "/q/", formType, signed, 405, "GET, HEAD, QUERY"},
		{"query on imgproxy", "QUERY", "/unsafe" + fit64 + "/plain/" + jpegSrc, formType, "x=1", 405, "GET, HEAD"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := query(t, tc.method, app+tc.path, tc.ctype, tc.content)
			r.wantStatus(t, tc.status)
			if tc.allow != "" && r.Header.Get("Allow") != tc.allow {
				t.Errorf("Allow %q, want %q", r.Header.Get("Allow"), tc.allow)
			}
		})
	}
}
