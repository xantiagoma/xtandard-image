//go:build acceptance

package acceptance

import (
	"strings"
	"testing"
)

// TestPDFPassword covers password-protected PDFs (WP-115): the password
// travels encrypted with the enc/ key (pdp option) on signed URLs only; the
// right one renders, a wrong one is 422 `wrong document password` (never
// the fallback image), none is the existing 422 (masked here by the
// configured fallback image) and an unsigned URL is 403. testdata/fixtures/pdf holds
// password.pdf (AES-256, "secret") and password-aes128.pdf ("open
// sesame"), both qpdf copies of pages.pdf (first page 200x150).
func TestPDFPassword(t *testing.T) {
	t.Parallel()
	app := base(t, "XI_ACC_APP")
	k := loadKeys(t)
	const pdf = "/plain/local:///pdf/password.pdf"
	pdp := func(pw string) string { return "/pdp:" + k.encrypt(t, pw) }
	says := func(msg string) func(*testing.T, *response) {
		return func(t *testing.T, r *response) {
			t.Helper()
			if !strings.Contains(string(r.Body), msg) {
				t.Errorf("body %q, want %q", truncate(r.Body), msg)
			}
			for _, secret := range []string{"secret", "sesame", "wrong-one"} {
				if strings.Contains(string(r.Body), secret) {
					t.Errorf("body reveals %q", secret)
				}
			}
		}
	}
	fellBack := func(t *testing.T, r *response) {
		t.Helper()
		if r.Header.Get("X-Xi-Fallback") == "" {
			t.Errorf("no X-Xi-Fallback header: %v", r.Header)
		}
	}
	page := props{Format: "jpeg", Width: 64, Height: 48}
	runImageCases(t, app, []imageCase{
		{name: "right password", path: k.sign(pdp("secret") + fit64 + pdf), status: 200, ctype: "image/jpeg", props: page},
		{name: "right password aes-128 page 2", path: k.sign(pdp("open sesame") + "/pg:2" + fit64 + "/plain/local:///pdf/password-aes128.pdf@png"), status: 200, ctype: "image/png"},
		{name: "right password info", path: "/info" + k.sign(pdp("secret")+"/pn:1"+pdf), status: 200, ctype: "application/json", check: says(`"pages_number"`)},
		{name: "wrong password", path: k.sign(pdp("wrong-one") + fit64 + pdf), status: 422, check: says("wrong document password")},
		// No password: an unreadable source like any other, so this app's
		// fallback image (XI_FALLBACK_SOURCE) answers; a wrong password never.
		{name: "no password", path: k.sign(fit64 + pdf), status: 200, ctype: "image/png", check: fellBack},
		{name: "unsigned", path: "/unsafe" + pdp("secret") + fit64 + pdf, status: 403, check: says("pdf_password requires a signed url")},
		{name: "unsigned garbage token", path: "/unsafe/pdp:AAAA" + fit64 + pdf, status: 403, check: says("pdf_password requires a signed url")},
	})
}
