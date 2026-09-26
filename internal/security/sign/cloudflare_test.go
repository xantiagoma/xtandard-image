package sign_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

// Cloudflare Images "Serve private images" example URL
// (developers.cloudflare.com/images/optimization/hosted-images/serve-private-images/)
// signed with the key "IMAGES_SIGNING_KEY" (the docs name the secret but
// not its value). The vectors were produced by running the documented
// generateSignedUrl Worker code under Bun with a fixed expiry and checked
// with `openssl dgst -sha256 -hmac`.
const (
	cfKey       = "IMAGES_SIGNING_KEY"
	cfDocsPath  = "/cheeW4oKsx5ljh8e8BoL2A/bc27a117-9509-446b-8c69-c81bfeac0a01/mobile"
	cfDocsQuery = "exp=1631289275"
	cfDocsSig   = "9d550a7a3b988037f87952bdc324cfb198e41ffcaa6f1a6c235a4bd347261d35"
	cfFlexPath  = "/cheeW4oKsx5ljh8e8BoL2A/bc27a117-9509-446b-8c69-c81bfeac0a01/w=400,sharpen=3"
	cfFlexQuery = "exp=1893456000"
	cfFlexSig   = "e31c150800e48c45bdba48f49fab95930b5c81bf8651d488e21aaaf5c6dcbf1e"
)

func cfKeyring(keys ...string) *sign.Keyring {
	ks := make([]sign.Key, 0, len(keys))
	for _, k := range keys {
		ks = append(ks, sign.NewKey([]byte(k), nil))
	}
	return sign.NewKeyring(ks...)
}

func cfVerifier(tb testing.TB, o sign.CloudflareOptions) *sign.KeyedVerifier {
	tb.Helper()
	v, err := sign.NewCloudflareVerifier(o)
	if err != nil {
		tb.Fatal(err)
	}
	return v
}

// cfReference is the documented Worker's computation for a URL whose query
// is already in URLSearchParams serialisation.
func cfReference(key, path, query string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(path + "?" + query))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestCloudflareMessage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, path, query string
		sig, msg          string
		bad               bool
	}{
		{"docs", cfDocsPath, cfDocsQuery + "&sig=abc", "abc", cfDocsPath + "?" + cfDocsQuery, false},
		{"sig first", "/a", "sig=abc&exp=1", "abc", "/a?exp=1", false},
		{"no query", "/a", "", "", "/a?", false},
		{"only sig", "/a", "sig=abc", "abc", "/a?", false},
		{"order kept", "/a", "b=2&exp=1&a=1&sig=s", "s", "/a?b=2&exp=1&a=1", false},
		{"empty segments", "/a", "&exp=1&&sig=s&", "s", "/a?exp=1", false},
		{"escaped kept", "/a", "x=%2F&sig=s", "s", "/a?x=%2F", false},
		{"sig-like key", "/a", "sigx=1&sig=s", "s", "/a?sigx=1", false},
		{"duplicate", "/a", "sig=a&sig=b", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sig, msg, err := sign.CloudflareMessage(tc.path, tc.query)
			if tc.bad {
				wantErr(t, err, sign.ErrMalformedSignature)
				return
			}
			if err != nil || sig != tc.sig || msg != tc.msg {
				t.Fatalf("CloudflareMessage = %q, %q, %v; want %q, %q", sig, msg, err, tc.sig, tc.msg)
			}
		})
	}
}

// TestCloudflareDocsVector verifies the signature of the Cloudflare docs
// example URL.
func TestCloudflareDocsVector(t *testing.T) {
	t.Parallel()
	v := cfVerifier(t, sign.CloudflareOptions{Keys: cfKeyring("other-key", cfKey), Required: true})
	for _, tc := range []struct{ path, query, sig string }{
		{cfDocsPath, cfDocsQuery, cfDocsSig},
		{cfFlexPath, cfFlexQuery, cfFlexSig},
	} {
		if ref := cfReference(cfKey, tc.path, tc.query); ref != tc.sig {
			t.Fatalf("reference %q, vector %q", ref, tc.sig)
		}
		for _, sig := range []string{tc.sig, strings.ToUpper(tc.sig)} {
			s, msg, err := sign.CloudflareMessage(tc.path, tc.query+"&sig="+sig)
			if err != nil {
				t.Fatal(err)
			}
			if res, err := v.Verify(s, msg); err != nil || res != sign.Verified {
				t.Fatalf("Verify(%s) = %v, %v", tc.path, res, err)
			}
		}
	}
}

func TestCloudflareVerify(t *testing.T) {
	t.Parallel()
	keys := cfKeyring(cfKey)
	v := cfVerifier(t, sign.CloudflareOptions{Keys: keys, Trusted: []string{"trusted"}})
	req := cfVerifier(t, sign.CloudflareOptions{Keys: keys, Required: true})
	off := cfVerifier(t, sign.CloudflareOptions{})
	msg := cfDocsPath + "?" + cfDocsQuery
	tests := []struct {
		name string
		v    *sign.KeyedVerifier
		sig  string
		want sign.Result
		err  error
	}{
		{"valid", v, cfDocsSig, sign.Verified, nil},
		{"trusted", v, "trusted", sign.Trusted, nil},
		{"absent", v, "", sign.Unsigned, nil},
		{"absent required", req, "", sign.Unsigned, sign.ErrMissingSignature},
		{"mismatch", v, cfFlexSig, sign.Unsigned, sign.ErrSignatureMismatch},
		{"truncated", v, cfDocsSig[:40], sign.Unsigned, sign.ErrMalformedSignature},
		{"odd length", v, cfDocsSig[:63], sign.Unsigned, sign.ErrMalformedSignature},
		{"not hex", v, "zz" + cfDocsSig[2:], sign.Unsigned, sign.ErrMalformedSignature},
		{"base64", v, "nVUKejuYgDf4eVK9wyTPsZjkH_yqbxpsI1pL00cmHTU", sign.Unsigned, sign.ErrMalformedSignature},
		{"signing off", off, "anything", sign.Unsigned, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := tc.v.Verify(tc.sig, msg)
			if tc.err != nil {
				wantErr(t, err, tc.err)
			} else if err != nil {
				t.Fatal(err)
			}
			if res != tc.want {
				t.Fatalf("result = %v, want %v", res, tc.want)
			}
		})
	}
}

func TestCloudflareScheme(t *testing.T) {
	t.Parallel()
	s := sign.NewCloudflareScheme()
	if s.Name() != "cloudflare" || s.Size() != 32 {
		t.Fatalf("scheme %s size %d", s.Name(), s.Size())
	}
	// Salts are ignored.
	salted := sign.NewKeyring(sign.NewKey([]byte(cfKey), []byte("salt")))
	v := cfVerifier(t, sign.CloudflareOptions{Keys: salted, Required: true})
	if res, err := v.Verify(cfDocsSig, cfDocsPath+"?"+cfDocsQuery); err != nil || res != sign.Verified {
		t.Fatalf("salted key: %v, %v", res, err)
	}
}

func TestSignCloudflareQuery(t *testing.T) {
	t.Parallel()
	keys := cfKeyring(cfKey)
	s, err := sign.NewSigner(sign.NewCloudflareScheme(), keys)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := s.SignCloudflareQuery(cfDocsPath, cfDocsQuery)
	if err != nil {
		t.Fatal(err)
	}
	if want := cfDocsQuery + "&sig=" + cfDocsSig; signed != want {
		t.Fatalf("SignCloudflareQuery = %q, want %q", signed, want)
	}
	v := cfVerifier(t, sign.CloudflareOptions{Keys: keys, Required: true})
	for _, q := range []string{"", "sig=stale", "exp=1&sig=stale&x=1"} {
		signed, err := s.SignCloudflareQuery("/h/i/v", q)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(signed, "sig=") != 1 {
			t.Fatalf("SignCloudflareQuery(%q) = %q", q, signed)
		}
		sig, msg, err := sign.CloudflareMessage("/h/i/v", signed)
		if err != nil {
			t.Fatal(err)
		}
		if res, err := v.Verify(sig, msg); err != nil || res != sign.Verified {
			t.Fatalf("round trip %q: %v, %v", q, res, err)
		}
	}
}

// FuzzCloudflareMessage checks that CloudflareMessage never panics, that
// the message always starts with path + "?", and that its own signatures
// always verify.
func FuzzCloudflareMessage(f *testing.F) {
	for _, s := range [][2]string{
		{cfDocsPath, cfDocsQuery + "&sig=" + cfDocsSig},
		{cfFlexPath, "sig=" + cfFlexSig + "&exp=1"},
		{"/a", "sig=a&sig=b"},
		{"", "&&=&"},
		{"/x", "sig=" + strings.Repeat("0", 300)},
	} {
		f.Add(s[0], s[1])
	}
	keys := cfKeyring(cfKey)
	s, err := sign.NewSigner(sign.NewCloudflareScheme(), keys)
	if err != nil {
		f.Fatal(err)
	}
	v := cfVerifier(f, sign.CloudflareOptions{Keys: keys})
	f.Fuzz(func(t *testing.T, path, query string) {
		sig, msg, err := sign.CloudflareMessage(path, query)
		if err != nil {
			if errs.KindOf(err) != errs.KindUnauthorized {
				t.Fatalf("kind %v", errs.KindOf(err))
			}
			return
		}
		if !strings.HasPrefix(msg, path+"?") {
			t.Fatalf("message %q lacks path %q", msg, path)
		}
		if _, err := v.Verify(sig, msg); err != nil && errs.KindOf(err) != errs.KindUnauthorized {
			t.Fatalf("Verify kind %v", errs.KindOf(err))
		}
		own, err := s.Sign(msg)
		if err != nil {
			t.Fatal(err)
		}
		if res, err := v.Verify(own, msg); err != nil || res != sign.Verified {
			t.Fatalf("own signature: %v, %v", res, err)
		}
	})
}

func BenchmarkCloudflareVerify(b *testing.B) {
	v := cfVerifier(b, sign.CloudflareOptions{Keys: cfKeyring(cfKey)})
	const q = cfDocsQuery + "&sig=" + cfDocsSig
	b.ReportAllocs()
	for b.Loop() {
		sig, msg, _ := sign.CloudflareMessage(cfDocsPath, q)
		_, _ = v.Verify(sig, msg)
	}
}
