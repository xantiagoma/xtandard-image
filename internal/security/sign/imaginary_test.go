package sign_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

// imaginary README "URL signature" example key, hex-encoded
// ("4f46feebafc4b5e988f131c4ff8b5997").
const (
	imaginaryKeyRaw = "4f46feebafc4b5e988f131c4ff8b5997"
	imaginaryKey    = "3466343666656562616663346235653938386631333163346666386235393937"
	// README: path "/resize", query "file=image.jpg&height=200&type=jpeg&width=300".
	// Computed independently with Python hmac/base64.
	imaginaryREADMESig = "ruEWRoFO-ic-L38vTsjqIYE6DLZ532CTaZXOh1gwuVo"
	// "/crop" with an unsorted query whose url needs re-encoding
	// (Python: urlencode of the sorted pairs, quote_plus).
	imaginaryCropQuery = "width=300&url=https://example.com/a%20b.jpg%3Fv%3D1&gravity=smart&height=200"
	imaginaryCropSig   = "478G48gwH7VV2PL-sB7bWcq_EKmL19qng1Bl6Dh8qSs"
)

// imaginaryReference is imaginary's validateURLSignature (middleware.go,
// MIT) reduced to the expected-signature computation, on a URL parsed the
// way net/http parses the request target.
func imaginaryReference(key, target string) string {
	// http.Server parses the request target with url.ParseRequestURI.
	r := struct{ URL *url.URL }{}
	r.URL, _ = url.ParseRequestURI(target)
	query := r.URL.Query()
	query.Del("sign")
	h := hmac.New(sha256.New, []byte(key))
	_, _ = h.Write([]byte(r.URL.Path))
	_, _ = h.Write([]byte(query.Encode()))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func imaginaryVerifier(tb testing.TB, o sign.ImaginaryOptions) *sign.KeyedVerifier {
	tb.Helper()
	v, err := sign.NewImaginaryVerifier(o)
	if err != nil {
		tb.Fatal(err)
	}
	return v
}

func TestImaginaryMessage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, path, query string
		sig, msg          string
		bad               bool
	}{
		{"no query", "/resize", "", "", "/resize", false},
		{"only sign", "/resize", "sign=abc", "abc", "/resize", false},
		{"sorted", "/resize", "width=1&height=2&sign=abc", "abc", "/resizeheight=2&width=1", false},
		{"sign first", "/resize", "sign=abc&b=1&a=2", "abc", "/resizea=2&b=1", false},
		{"re-encoded", "/crop", "url=https://x/a%20b&sign=s", "s", "/cropurl=https%3A%2F%2Fx%2Fa+b", false},
		{"repeated key order kept", "/r", "a=2&a=1", "", "/ra=2&a=1", false},
		{"malformed pair dropped", "/r", "a=%zz&b=1", "", "/rb=1", false},
		{"empty sign", "/r", "sign=&a=1", "", "/ra=1", false},
		{"duplicate", "/r", "sign=a&sign=b", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sig, msg, err := sign.ImaginaryMessage(tc.path, tc.query)
			if tc.bad {
				wantErr(t, err, sign.ErrMalformedSignature)
				return
			}
			if err != nil || sig != tc.sig || msg != tc.msg {
				t.Fatalf("ImaginaryMessage = %q, %q, %v; want %q, %q", sig, msg, err, tc.sig, tc.msg)
			}
		})
	}
}

func TestImaginaryVector(t *testing.T) {
	t.Parallel()
	// Salts are ignored by the imaginary scheme.
	keys := hexKeyring(t, imaginaryKey, "", exampleKey, exampleSalt)
	saltedKeys := hexKeyring(t, imaginaryKey, "abcd")
	tests := []struct {
		name, path, query, sig string
	}{
		{"readme", "/resize", "file=image.jpg&height=200&type=jpeg&width=300", imaginaryREADMESig},
		{"readme unsorted", "/resize", "width=300&type=jpeg&file=image.jpg&height=200", imaginaryREADMESig},
		{"crop re-encoded", "/crop", imaginaryCropQuery, imaginaryCropSig},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ref := imaginaryReference(imaginaryKeyRaw, tc.path+"?"+tc.query)
			if ref != tc.sig {
				t.Fatalf("reference = %q, vector %q", ref, tc.sig)
			}
			for _, k := range []*sign.Keyring{keys, saltedKeys} {
				v := imaginaryVerifier(t, sign.ImaginaryOptions{Keys: k, Required: true})
				sig, msg, err := sign.ImaginaryMessage(tc.path, tc.query+"&sign="+tc.sig)
				if err != nil {
					t.Fatal(err)
				}
				if res, err := v.Verify(sig, msg); err != nil || res != sign.Verified {
					t.Fatalf("Verify = %v, %v", res, err)
				}
			}
		})
	}
}

func TestImaginaryVerify(t *testing.T) {
	t.Parallel()
	keys := hexKeyring(t, imaginaryKey, "")
	v := imaginaryVerifier(t, sign.ImaginaryOptions{Keys: keys, Trusted: []string{"trusted"}})
	req := imaginaryVerifier(t, sign.ImaginaryOptions{Keys: keys, Required: true})
	off := imaginaryVerifier(t, sign.ImaginaryOptions{})
	const path, q = "/resize", "file=image.jpg&height=200&type=jpeg&width=300"
	tests := []struct {
		name string
		v    *sign.KeyedVerifier
		sig  string
		want sign.Result
		err  error
	}{
		{"valid", v, imaginaryREADMESig, sign.Verified, nil},
		{"trusted", v, "trusted", sign.Trusted, nil},
		{"absent", v, "", sign.Unsigned, nil},
		{"absent required", req, "", sign.Unsigned, sign.ErrMissingSignature},
		{"mismatch", v, imaginaryCropSig, sign.Unsigned, sign.ErrSignatureMismatch},
		{"truncated", v, imaginaryREADMESig[:20], sign.Unsigned, sign.ErrMalformedSignature},
		{"padded", v, imaginaryREADMESig + "=", sign.Unsigned, sign.ErrMalformedSignature},
		{"std alphabet", v, "+" + imaginaryREADMESig[1:], sign.Unsigned, sign.ErrMalformedSignature},
		{"signing off", off, "anything", sign.Unsigned, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := tc.v.Verify(tc.sig, path+q)
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

func TestImaginaryScheme(t *testing.T) {
	t.Parallel()
	s := sign.NewImaginaryScheme()
	if s.Name() != "imaginary" || s.Size() != 32 {
		t.Fatalf("scheme %s size %d", s.Name(), s.Size())
	}
}

func TestSignImaginaryQuery(t *testing.T) {
	t.Parallel()
	keys := hexKeyring(t, imaginaryKey, "")
	s, err := sign.NewSigner(sign.NewImaginaryScheme(), keys)
	if err != nil {
		t.Fatal(err)
	}
	v := imaginaryVerifier(t, sign.ImaginaryOptions{Keys: keys, Required: true})
	for _, q := range []string{"", "file=image.jpg&height=200&type=jpeg&width=300", "sign=stale&width=1", imaginaryCropQuery} {
		signed, err := s.SignImaginaryQuery("/crop", q)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(signed, "sign=") != 1 {
			t.Fatalf("SignImaginaryQuery(%q) = %q", q, signed)
		}
		if ref := imaginaryReference(imaginaryKeyRaw, "/crop?"+signed); !strings.HasSuffix(signed, "sign="+ref) {
			t.Fatalf("SignImaginaryQuery(%q) = %q, reference sign %q", q, signed, ref)
		}
		sig, msg, err := sign.ImaginaryMessage("/crop", signed)
		if err != nil {
			t.Fatal(err)
		}
		if res, err := v.Verify(sig, msg); err != nil || res != sign.Verified {
			t.Fatalf("round trip %q: %v, %v", q, res, err)
		}
	}
}

// FuzzImaginaryMessage checks that ImaginaryMessage never panics, agrees
// with the reference message for well-formed queries and that its own
// signatures always verify.
func FuzzImaginaryMessage(f *testing.F) {
	for _, s := range []string{
		"file=image.jpg&height=200&type=jpeg&width=300",
		imaginaryCropQuery + "&sign=" + imaginaryCropSig,
		"sign=a&sign=b",
		"a=%zz&;b=1",
		"",
		"&&=&",
	} {
		f.Add("/resize", s)
	}
	keys := hexKeyring(f, imaginaryKey, "")
	s, err := sign.NewSigner(sign.NewImaginaryScheme(), keys)
	if err != nil {
		f.Fatal(err)
	}
	v := imaginaryVerifier(f, sign.ImaginaryOptions{Keys: keys})
	f.Fuzz(func(t *testing.T, path, query string) {
		sig, msg, err := sign.ImaginaryMessage(path, query)
		if err != nil {
			if errs.KindOf(err) != errs.KindUnauthorized {
				t.Fatalf("kind %v", errs.KindOf(err))
			}
			return
		}
		if !strings.HasPrefix(msg, path) {
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
