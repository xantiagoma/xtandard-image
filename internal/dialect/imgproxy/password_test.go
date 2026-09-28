package imgproxy_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/crypt"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// examplePasswordToken is the catalogue example: "secret" under encKey with
// IV "0123456789abcdef" (testdata/security/vectors.json, pdf_password).
const examplePasswordToken = "MDEyMzQ1Njc4OWFiY2RlZlsN0ZQuFO2sitmy5C87M2o" //nolint:gosec // G101: public test vector (testdata/security/vectors.json)

const pdfSource = "/plain/s3://docs/contract.pdf"

func publicOf(err error) string {
	if e, ok := errors.AsType[*errs.Error](err); ok {
		return e.PublicMessage()
	}
	return ""
}

// TestPDFPasswordSigned parses pdf_password on verified URLs: the password
// is decrypted into Source.Password, its keyed digest is set, and nothing
// else of the request mentions it.
func TestPDFPasswordSigned(t *testing.T) {
	t.Parallel()
	f := getFixture(t)
	p := f.parsers["signed"]
	for _, tc := range []struct {
		name, path, password string
		info                 bool
	}{
		{"canonical", "/SIG/pdf_password:ENC[secret]/rs:fit:300:0" + pdfSource, "secret", false},
		{"alias", "/SIG/pdp:ENC[open sesame]" + pdfSource, "open sesame", false},
		{"non-ascii", "/SIG/pdp:ENC[pässwörd €]" + pdfSource, "pässwörd €", false},
		{"catalogue example", "/SIG/pdp:" + examplePasswordToken + pdfSource, "secret", false},
		{"later wins", "/SIG/pdp:ENC[first]/pdp:ENC[second]" + pdfSource, "second", false},
		{"empty removes", "/SIG/pdp:ENC[first]/pdp:" + pdfSource, "", false},
		{"in a chained pipeline", "/SIG/rs:fit:10:10/-/pdp:ENC[secret]" + pdfSource, "secret", false},
		{"info", "/SIG/s:1/pdp:ENC[secret]" + pdfSource, "secret", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := f.expand(t, tc.path)
			var src transform.SourceSpec
			if tc.info {
				r, err := p.ParseInfoPath(path)
				if err != nil {
					t.Fatalf("ParseInfoPath: %v", err)
				}
				src = r.Request.Source
			} else {
				r, err := p.ParsePath(path)
				if err != nil {
					t.Fatalf("ParsePath: %v", err)
				}
				src = r.Request.Source
			}
			if got := string(src.Password.Bytes()); got != tc.password {
				t.Errorf("password %q, want %q", got, tc.password)
			}
			switch want := f.cipher.PasswordDigest([]byte(tc.password)); {
			case tc.password == "" && src.Password != nil:
				t.Errorf("password %+v, want none", src.Password)
			case tc.password != "" && src.Password.Digest != want:
				t.Errorf("digest %q, want %q", src.Password.Digest, want)
			}
			if src.Raw != "s3://docs/contract.pdf" {
				t.Errorf("source %q", src.Raw)
			}
		})
	}
}

// TestPDFPasswordRefused: the option needs a verified signature and an
// encryption key, and a bad token is 403 with a message that echoes
// nothing. An unsigned URL is refused whatever its token, so the
// decryption is never reachable without a signature.
func TestPDFPasswordRefused(t *testing.T) {
	t.Parallel()
	f := getFixture(t)
	noCipher, err := imgproxy.New(&imgproxy.Options{Verifier: f.parsers["signed"].Verifier()})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		p      *imgproxy.Parser
		path   string
		kind   errs.Kind
		public string
	}{
		{"unsigned", f.parsers["enc"], "/unsafe/pdp:ENC[secret]" + pdfSource, errs.KindForbidden, "pdf_password requires a signed url"},
		{"unsigned garbage token", f.parsers["enc"], "/unsafe/pdp:AAAA" + pdfSource, errs.KindForbidden, "pdf_password requires a signed url"},
		{"unsigned empty token", f.parsers["enc"], "/unsafe/pdp:" + pdfSource, errs.KindForbidden, "pdf_password requires a signed url"},
		{"unsigned on a signing parser", f.parsers["signed"], "/unsafe/pdp:ENC[secret]" + pdfSource, errs.KindForbidden, "pdf_password requires a signed url"},
		{"trusted signature", f.parsers["signed"], "/" + trustedSig + "/pdp:ENC[secret]" + pdfSource, errs.KindForbidden, "pdf_password requires a signed url"},
		{"no encryption key", noCipher, "/SIG/pdp:ENC[secret]" + pdfSource, errs.KindForbidden, "pdf_password is not enabled"},
		{"bad token", f.parsers["signed"], "/SIG/pdp:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" + pdfSource, errs.KindUnauthorized, "invalid encrypted document password"},
		{"not base64", f.parsers["signed"], "/SIG/pdp:%21%21" + pdfSource, errs.KindUnauthorized, "invalid encrypted document password"},
		{"too long", f.parsers["signed"], "/SIG/pdp:ENC[" + strings.Repeat("x", transform.MaxPasswordLen+1) + "]" + pdfSource, errs.KindInvalidURL, "invalid pdf_password: expected a password of at most 1024 bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := f.expand(t, tc.path)
			res, err := tc.p.ParsePath(path)
			if err == nil {
				t.Fatalf("ParsePath succeeded: %+v", res.Request.Source)
			}
			if errs.KindOf(err) != tc.kind || publicOf(err) != tc.public {
				t.Errorf("error %v (kind %s, public %q), want %s %q", err, errs.KindOf(err), publicOf(err), tc.kind, tc.public)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Errorf("error %q echoes the password", err)
			}
		})
	}
}

// TestPDFPasswordSeparator: a custom separator that base64url uses ("_")
// splits the token into arguments; they are joined back.
func TestPDFPasswordSeparator(t *testing.T) {
	t.Parallel()
	f := getFixture(t)
	p, err := imgproxy.New(&imgproxy.Options{Verifier: f.parsers["signed"].Verifier(), Cipher: f.cipher, ArgumentsSeparator: "_"})
	if err != nil {
		t.Fatal(err)
	}
	for range 20 { // random IVs: make an underscore in the token likely
		path := f.expand(t, "/SIG/pdp_ENC[secret]"+pdfSource)
		res, err := p.ParsePath(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if got := string(res.Request.Source.Password.Bytes()); got != "secret" {
			t.Fatalf("%s: password %q", path, got)
		}
	}
}

// TestPDFPasswordDigestKeyed: the digest is independent of the token (IV)
// but depends on the password and on the encryption key, and the canonical
// key follows it.
func TestPDFPasswordDigestKeyed(t *testing.T) {
	t.Parallel()
	f := getFixture(t)
	other, err := crypt.New("000102030405060708090a0b0c0d0e0f")
	if err != nil {
		t.Fatal(err)
	}
	a, b := f.cipher.PasswordDigest([]byte("secret")), f.cipher.PasswordDigest([]byte("secret"))
	if a != b || len(a) != 64 {
		t.Fatalf("digest not stable: %q %q", a, b)
	}
	if a == f.cipher.PasswordDigest([]byte("Secret")) || a == other.PasswordDigest([]byte("secret")) {
		t.Error("digest ignores the password or the key")
	}
	key := func(path string) string {
		res, err := f.parsers["signed"].ParsePath(f.expand(t, path))
		if err != nil {
			t.Fatal(err)
		}
		res.Request.ApplyDefaults(transform.StandardDefaults())
		return res.Request.CanonicalKey()
	}
	k1, k2 := key("/SIG/pdp:ENC[secret]"+pdfSource), key("/SIG/pdp:ENC[secret]"+pdfSource)
	if k1 != k2 {
		t.Error("two tokens of one password have different canonical keys")
	}
	if k1 == key("/SIG/pdp:ENC[wrong]"+pdfSource) || k1 == key("/SIG"+pdfSource) {
		t.Error("the canonical key ignores the password")
	}
}

// TestPDFPasswordNeverFormatted: the request never prints the password,
// whatever formats it.
func TestPDFPasswordNeverFormatted(t *testing.T) {
	t.Parallel()
	f := getFixture(t)
	res, err := f.parsers["signed"].ParsePath(f.expand(t, "/SIG/pdp:ENC[hunter2-secret]"+pdfSource))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		fmt.Sprintf("%v", res.Request), fmt.Sprintf("%+v", res.Request), fmt.Sprintf("%#v", res.Request),
		fmt.Sprint(res.Request.Source.Password), fmt.Sprintf("%x", res.Request.Source.Password.Secret),
		mustJSONString(t, res.Request),
	} {
		if strings.Contains(s, "hunter2") || strings.Contains(s, fmt.Sprintf("%x", "hunter2")) {
			t.Errorf("formatted request reveals the password: %s", s)
		}
	}
}

func mustJSONString(t *testing.T, v any) string {
	t.Helper()
	return string(mustJSON(t, v))
}

// FuzzPDFPassword feeds arbitrary pdf_password tokens to a signed URL and
// to the same URL unsigned. The signed parse never panics, fails only with
// a client kind, and anything it accepts is a password whose digest
// matches and that round-trips through the cipher. The unsigned parse
// never reaches the decryption, whatever the token (no padding oracle).
func FuzzPDFPassword(f *testing.F) {
	for _, s := range []string{examplePasswordToken, "", "AAAA", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "a:b", "%zz", "==", "/"} {
		f.Add(s)
	}
	fx := getFixture(f)
	signed, unsigned := fx.parsers["signed"], fx.parsers["enc"]
	f.Fuzz(func(t *testing.T, token string) {
		rest := "/pdp:" + token + "/plain/s3://docs/a.pdf"
		sig, err := fx.signer.Sign(rest)
		if err != nil {
			t.Skip()
		}
		res, err := signed.ParsePath("/" + sig + rest)
		checkFuzz(t, "signed", rest, res, err, transform.KindProcess)
		if err == nil {
			src := res.Request.Source
			switch pw := src.Password.Bytes(); {
			case len(pw) > 0:
				if src.Password.Digest != fx.cipher.PasswordDigest(pw) {
					t.Fatalf("%q: digest does not match the password", token)
				}
				tok, err := fx.cipher.Encrypt(pw)
				if err != nil {
					t.Fatalf("%q: decrypted password does not re-encrypt: %v", token, err)
				}
				again, err := fx.cipher.DecryptPassword(tok)
				if err != nil || !bytes.Equal(again, pw) {
					t.Fatalf("%q: round trip = %v", token, err)
				}
			case src.Password != nil:
				t.Fatalf("%q: empty password set", token)
			}
		}
		if _, err := unsigned.ParsePath("/unsafe" + rest); err != nil && strings.HasPrefix(publicOf(err), "invalid encrypted") {
			t.Fatalf("%q: an unsigned url reached the decryption: %v", token, err)
		}
	})
}
