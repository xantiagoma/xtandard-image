package playground

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/security/crypt"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

// passwordFixture is newFixture with api/sign able to encrypt passwords.
func passwordFixture(t *testing.T, withKey bool) (*fixture, *crypt.Cipher) {
	t.Helper()
	f := newFixture(t)
	c, err := crypt.New("1eb5b0e971ad7f45324c1bb15c947cb207c43152fa5c6c7f35c4f36e0c18e0f1")
	if err != nil {
		t.Fatal(err)
	}
	deps := &Deps{Meta: testMeta(), Signers: f.signers, Image: f.image, Logger: slog.New(slog.DiscardHandler)}
	if withKey {
		deps.EncryptPassword = c.Encrypt
	}
	if f.h, err = New(Config{Path: DefaultPath}, deps); err != nil {
		t.Fatal(err)
	}
	return f, c
}

// TestSignPDFPassword: api/sign encrypts the password into the URL (a pdp
// segment first on imgproxy mounts, a pdf_password parameter on the query
// mount), signs the result, and never echoes the plaintext (WP-115).
func TestSignPDFPassword(t *testing.T) {
	t.Parallel()
	f, c := passwordFixture(t, true)
	ipv, err := sign.NewImgproxyVerifier(sign.ImgproxyOptions{Keys: f.ring, Size: 32, Required: true})
	if err != nil {
		t.Fatal(err)
	}
	xiv, err := sign.NewXIVerifier(sign.XIOptions{Keys: f.ring, Size: 32, Required: true})
	if err != nil {
		t.Fatal(err)
	}
	const password = "open sesame"
	signed := func(t *testing.T, body string) SignResponse {
		t.Helper()
		res, b := f.do(t, post(t, body))
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status %d: %s", res.StatusCode, b)
		}
		if strings.Contains(string(b), "sesame") {
			t.Fatalf("response echoes the password: %s", b)
		}
		var out SignResponse
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	decrypts := func(t *testing.T, token string) {
		t.Helper()
		pw, err := c.DecryptPassword(token)
		if err != nil || string(pw) != password {
			t.Errorf("token decrypts to %q, %v", pw, err)
		}
	}

	t.Run("imgproxy", func(t *testing.T) {
		t.Parallel()
		out := signed(t, `{"mount":"imgproxy","path":"/rs:fit:300:0/plain/s3://docs/a.pdf","pdf_password":"`+password+`"}`)
		sig, path, _ := strings.Cut(strings.TrimPrefix(out.URL, "/img/"), "/")
		path = "/" + path
		token, rest, ok := strings.Cut(strings.TrimPrefix(path, "/pdp:"), "/")
		if !ok || !strings.HasPrefix(path, "/pdp:") || rest != "rs:fit:300:0/plain/s3://docs/a.pdf" {
			t.Fatalf("url %q", out.URL)
		}
		decrypts(t, token)
		if r, err := ipv.Verify(sig, path); err != nil || r != sign.Verified {
			t.Errorf("Verify = %v, %v", r, err)
		}
	})
	t.Run("query", func(t *testing.T) {
		t.Parallel()
		out := signed(t, `{"mount":"query","path":"/","query":"url=s3%3A%2F%2Fdocs%2Fa.pdf&w=300","pdf_password":"`+password+`"}`)
		path, query, _ := strings.Cut(strings.TrimPrefix(out.URL, "/img/q"), "?")
		q, err := url.ParseQuery(query)
		if err != nil {
			t.Fatal(err)
		}
		decrypts(t, q.Get("pdf_password"))
		sig, msg, err := sign.XIMessage(path, query)
		if err != nil {
			t.Fatal(err)
		}
		if r, err := xiv.Verify(sig, msg); err != nil || r != sign.Verified {
			t.Errorf("Verify = %v, %v", r, err)
		}
	})
}

func TestSignPDFPasswordRejects(t *testing.T) {
	t.Parallel()
	with, _ := passwordFixture(t, true)
	without, _ := passwordFixture(t, false)
	for _, tc := range []struct {
		name, body, msg string
		f               *fixture
	}{
		{"no encryption key", `{"mount":"imgproxy","path":"/plain/a.pdf","pdf_password":"x"}`, "pdf_password needs XI_ENCRYPTION_KEYS", without},
		{"unkeyed mount", `{"mount":"imgproxy-open","path":"/plain/a.pdf","pdf_password":"x"}`, "pdf_password needs a signed url", with},
		{"other dialect", `{"mount":"thumbor","path":"/a.pdf","pdf_password":"x"}`, "pdf_password is an imgproxy and query dialect option", with},
		{"wsrv", `{"mount":"wsrv","path":"/","query":"url=a.pdf","pdf_password":"x"}`, "pdf_password is an imgproxy and query dialect option", with},
		{"control character", `{"mount":"imgproxy","path":"/plain/a.pdf","pdf_password":"a\nb"}`, "pdf_password must be text without control characters", with},
		{"too long", `{"mount":"imgproxy","path":"/plain/a.pdf","pdf_password":"` + strings.Repeat("x", maxPDFPassword+1) + `"}`, "pdf_password is longer than 1024 bytes", with},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, b := tc.f.do(t, post(t, tc.body))
			if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), tc.msg) {
				t.Errorf("status %d %s, want 400 %q", res.StatusCode, b, tc.msg)
			}
		})
	}
}
