package sign_test

import (
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

// ImageKit vectors. The "dummy-key" ones are the published expectations
// of imagekit-nodejs tests/custom-tests/url-generation/signing.test.ts
// (Apache-2.0), URL endpoint https://ik.imagekit.io/demo/; the expiring
// one comes from the same SDK via
// internal/dialect/imagekit/testdata/sdk/generate.ts.
const (
	ikDummyKey = "dummy-key"
	ikHindi    = "sdk-testing-files/%E0%A4%B9%E0%A4%BF%E0%A4%A8%E0%A5%8D%E0%A4%A6%E0%A5%80.png"
	ikTextTr   = "l-text,ie-4KS54KS%2F4KSo4KWN4KSm4KWA,co-red,fs-32,ff-sdk-testing-files@@Poppins-Regular_Q15GrYWmL.ttf,l-end"
	ikTestKey  = "private_xtandard_test_key"
)

var ikVectors = []struct {
	name, key, path, query, sig string
}{
	{"plain", ikDummyKey, "/sdk-testing-files/future-search.png", "", "32dbbbfc5f945c0403c71b54c38e76896ef2d6b0"},
	{"unicode", ikDummyKey, "/" + ikHindi, "", "3fff2f31da1f45e007adcdbe95f88c8c330e743c"},
	{"text_overlay_query", ikDummyKey, "/" + ikHindi, "tr=" + ikTextTr, "ac9f24a03080102555e492185533c1ae6bd93fa7"},
	{"text_overlay_path", ikDummyKey, "/tr:" + ikTextTr + "/" + ikHindi, "", "69f2ecbb7364bbbad24616e1f7f1bac5a560fc71"},
	{"query_parameters", ikDummyKey, "/sdk-testing-files/future-search.png", "version=1.0&cache=false", "f2e5a1b8b6a0b03fd63789dfc6413a94acef9fd8"},
	{"transform_and_query", ikDummyKey, "/sdk-testing-files/future-search.png", "version=2.0&tr=w-300,h-200", "601d97a7834b7554f4dabf0d3fc3a219ceeb6b31"},
	{"expiring", ikTestKey, "/folder/photo.jpg", "tr=w-300&ik-t=1815920000", "9b2c14134d4e825d00d18d46fe814ad0f5ce87f5"},
}

func ikVerifier(tb testing.TB, key string, required bool) *sign.KeyedVerifier {
	tb.Helper()
	v, err := sign.NewImageKitVerifier(sign.ImageKitOptions{
		Keys:     sign.NewKeyring(sign.NewKey([]byte(key), nil)),
		Trusted:  []string{"trusted"},
		Required: required,
	})
	if err != nil {
		tb.Fatal(err)
	}
	return v
}

func TestImageKitMessage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, path, query string
		sig, expires, msg string
		bad               bool
	}{
		{"no query", "/a.jpg", "", "", "", "a.jpg9999999999", false},
		{"no leading slash", "a.jpg", "", "", "", "a.jpg9999999999", false},
		{"signature only", "/a.jpg", "ik-s=abc", "abc", "", "a.jpg9999999999", false},
		{"expiry", "/a.jpg", "tr=w-1&ik-t=123&ik-s=abc", "abc", "123", "a.jpg?tr=w-1123", false},
		{"order and encoding kept", "/a", "b=%20&ik-s=x&a=1", "x", "", "a?b=%20&a=19999999999", false},
		{"empty segments dropped", "/a", "&&b=1&&", "", "", "a?b=19999999999", false},
		{"duplicate ik-s", "/a", "ik-s=a&ik-s=b", "", "", "", true},
		{"duplicate ik-t", "/a", "ik-t=1&ik-t=2", "", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sig, expires, msg, err := sign.ImageKitMessage(tc.path, tc.query)
			if tc.bad {
				wantErr(t, err, sign.ErrMalformedSignature)
				return
			}
			if err != nil || sig != tc.sig || expires != tc.expires || msg != tc.msg {
				t.Fatalf("ImageKitMessage = %q, %q, %q, %v; want %q, %q, %q",
					sig, expires, msg, err, tc.sig, tc.expires, tc.msg)
			}
		})
	}
}

// TestImageKitVectors verifies the SDK's signatures, and that the signer
// reproduces them.
func TestImageKitVectors(t *testing.T) {
	t.Parallel()
	for _, vec := range ikVectors {
		t.Run(vec.name, func(t *testing.T) {
			t.Parallel()
			query := vec.query
			if query != "" {
				query += "&"
			}
			query += sign.ImageKitSignatureParam + "=" + vec.sig
			sig, _, msg, err := sign.ImageKitMessage(vec.path, query)
			if err != nil {
				t.Fatal(err)
			}
			res, err := ikVerifier(t, vec.key, true).Verify(sig, msg)
			if err != nil || res != sign.Verified {
				t.Fatalf("Verify = %v, %v", res, err)
			}
			// Upper-case hex is accepted too.
			if res, err := ikVerifier(t, vec.key, true).Verify(strings.ToUpper(sig), msg); err != nil || res != sign.Verified {
				t.Fatalf("Verify(upper) = %v, %v", res, err)
			}

			s, err := sign.NewSigner(sign.NewImageKitScheme(), sign.NewKeyring(sign.NewKey([]byte(vec.key), nil)))
			if err != nil {
				t.Fatal(err)
			}
			base, expires := vec.query, int64(0)
			if b, ts, ok := strings.Cut(vec.query, "&ik-t="); ok {
				base = b
				for _, c := range ts {
					expires = expires*10 + int64(c-'0')
				}
			}
			got, err := s.SignImageKitQuery(vec.path, base, expires)
			if err != nil {
				t.Fatal(err)
			}
			if got != query {
				t.Errorf("SignImageKitQuery = %q, want %q", got, query)
			}
		})
	}
}

func TestImageKitVerifyFailures(t *testing.T) {
	t.Parallel()
	vec := ikVectors[0]
	_, _, msg, err := sign.ImageKitMessage(vec.path, "")
	if err != nil {
		t.Fatal(err)
	}
	v := ikVerifier(t, vec.key, false)
	tests := []struct {
		name, sig, msg string
		want           sign.Result
		target         error
	}{
		{"absent optional", "", msg, sign.Unsigned, nil},
		{"trusted", "trusted", msg, sign.Trusted, nil},
		{"mismatch", strings.Repeat("0", 40), msg, sign.Unsigned, sign.ErrSignatureMismatch},
		{"other message", vec.sig, msg + "x", sign.Unsigned, sign.ErrSignatureMismatch},
		{"not hex", strings.Repeat("z", 40), msg, sign.Unsigned, sign.ErrMalformedSignature},
		{"short", vec.sig[:38], msg, sign.Unsigned, sign.ErrMalformedSignature},
		{"long", vec.sig + "00", msg, sign.Unsigned, sign.ErrMalformedSignature},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := v.Verify(tc.sig, tc.msg)
			if tc.target != nil {
				wantErr(t, err, tc.target)
				return
			}
			if err != nil || res != tc.want {
				t.Fatalf("Verify = %v, %v; want %v", res, err, tc.want)
			}
		})
	}
	_, err = ikVerifier(t, vec.key, true).Verify("", msg)
	wantErr(t, err, sign.ErrMissingSignature)
}

func TestImageKitScheme(t *testing.T) {
	t.Parallel()
	s := sign.NewImageKitScheme()
	if s.Name() != "imagekit" || s.Size() != 20 {
		t.Fatalf("Name %q Size %d", s.Name(), s.Size())
	}
	raw, err := s.Decode(nil, "00ff")
	if err != nil || s.Encode(raw) != "00ff" {
		t.Fatalf("Decode/Encode = %x, %v", raw, err)
	}
}

// FuzzImageKitMessage checks the canonicalisation never panics, never
// keeps ik-s or ik-t in the signed query, and round-trips with Signer.
func FuzzImageKitMessage(f *testing.F) {
	for _, s := range [][2]string{
		{"/a.jpg", ""},
		{"/a.jpg", "tr=w-1&ik-t=123&ik-s=abc"},
		{"/a", "ik-s=a&ik-s=b"},
		{"/a", "&&&"},
		{"", "ik-t"},
		{"/tr:w-1/a", "a=%zz&b==c"},
	} {
		f.Add(s[0], s[1])
	}
	keys := sign.NewKeyring(sign.NewKey([]byte(ikTestKey), nil))
	s, err := sign.NewSigner(sign.NewImageKitScheme(), keys)
	if err != nil {
		f.Fatal(err)
	}
	v, err := sign.NewImageKitVerifier(sign.ImageKitOptions{Keys: keys})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, path, query string) {
		_, expires, msg, err := sign.ImageKitMessage(path, query)
		if err != nil {
			if errs.KindOf(err) != errs.KindUnauthorized {
				t.Fatalf("kind = %v", errs.KindOf(err))
			}
			return
		}
		rel := strings.TrimPrefix(path, "/")
		if !strings.HasPrefix(msg, rel) {
			t.Fatalf("msg %q lacks path %q", msg, rel)
		}
		if expires == "" && !strings.Contains(query, sign.ImageKitExpiresParam) && !strings.HasSuffix(msg, sign.ImageKitNoExpiry) {
			t.Fatalf("msg %q lacks the default expiry", msg)
		}
		signed, err := s.SignImageKitQuery(path, query, 0)
		if err != nil {
			t.Fatal(err)
		}
		sig, _, msg2, err := sign.ImageKitMessage(path, signed)
		if err != nil {
			t.Fatalf("re-parse of %q: %v", signed, err)
		}
		if res, err := v.Verify(sig, msg2); err != nil || res != sign.Verified {
			t.Fatalf("round trip %q: %v, %v", signed, res, err)
		}
	})
}

func BenchmarkImageKitVerify(b *testing.B) {
	vec := ikVectors[len(ikVectors)-1]
	v := ikVerifier(b, vec.key, true)
	query := vec.query + "&ik-s=" + vec.sig
	b.ReportAllocs()
	for b.Loop() {
		sig, _, msg, _ := sign.ImageKitMessage(vec.path, query)
		_, _ = v.Verify(sig, msg)
	}
}
