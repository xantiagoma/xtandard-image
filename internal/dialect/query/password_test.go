package query_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/query"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/crypt"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// TestPDFPassword covers the query dialect's pdf_password (WP-115) in
// native mode, endpoint mode and o=, signed with xi_sig or imaginary's
// sign, and refused on unsigned URLs before decryption.
func TestPDFPassword(t *testing.T) {
	t.Parallel()
	keys, err := sign.ParseHexKeyring([]string{xiKey}, []string{xiSalt})
	if err != nil {
		t.Fatal(err)
	}
	v, err := sign.NewXIVerifier(sign.XIOptions{Keys: keys, Size: 32})
	if err != nil {
		t.Fatal(err)
	}
	scheme, err := sign.NewXIScheme(32)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := sign.NewSigner(scheme, keys)
	if err != nil {
		t.Fatal(err)
	}
	c, err := crypt.New("000102030405060708090a0b0c0d0e0f")
	if err != nil {
		t.Fatal(err)
	}
	p, err := query.New(&query.Options{XIVerifier: v, Cipher: c})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := c.Encrypt([]byte("open sesame"))
	if err != nil {
		t.Fatal(err)
	}
	src := "url=" + url.QueryEscape("s3://docs/a.pdf")
	signed := func(path, q string) string {
		s, err := signer.SignXIQuery(path, q)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	hs := "sha256:" + strings.Repeat("ab", 32)
	for _, tc := range []struct {
		name, path, query string
		kind              errs.Kind // 0: success
		hashsum           bool
	}{
		{"native", "/", signed("/", src+"&w=300&pdf_password="+tok), 0, false},
		{"native short", "/", signed("/", src+"&pdp="+tok), 0, false},
		{"o=", "/", signed("/", src+"&o="+url.QueryEscape("pdp:"+tok+"/hs:"+hs)), 0, true},
		{"endpoint", "/resize", signed("/resize", src+"&width=300&pdf_password="+tok), 0, false},
		{"unsigned", "/", src + "&pdf_password=" + tok, errs.KindForbidden, false},
		{"unsigned garbage", "/", src + "&pdf_password=AAAA", errs.KindForbidden, false},
		{"unsigned endpoint", "/resize", src + "&width=3&pdp=" + tok, errs.KindForbidden, false},
		{"bad token", "/", signed("/", src+"&pdf_password=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"), errs.KindUnauthorized, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := p.Parse(&dialect.Input{Path: tc.path, RawQuery: tc.query})
			if tc.kind != 0 {
				if errs.KindOf(err) != tc.kind {
					t.Fatalf("err = %v (kind %s), want %s", err, errs.KindOf(err), tc.kind)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			s := res.Request.Source
			if s.Password == nil || string(s.Password.Secret) != "open sesame" || s.Password.Digest != c.PasswordDigest([]byte("open sesame")) {
				t.Errorf("password %+v", s.Password)
			}
			if s.Raw != "s3://docs/a.pdf" || s.Encoding != transform.SourcePlain {
				t.Errorf("source %+v", s)
			}
			if tc.hashsum != (s.Hashsum != nil) {
				t.Errorf("hashsum %+v, want set: %t", s.Hashsum, tc.hashsum)
			}
		})
	}
}
