package sign_test

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

// thumborVectors are signatures from thumbor's own test suite (MIT):
// tests/test_url_composer.py (key "MY-SECURITY-KEY"),
// tests/handlers/test_base_handler.py (key "ACME-SEC") and
// tests/handlers/test_base_handler_with_stored_keys.py (key "MYKEY"). The
// message is the escaped path after "/<signature>/".
var thumborVectors = []struct {
	key, sig, path string
}{
	{"MY-SECURITY-KEY", "G_dykuWBGyEil5JnNh9cBke0Ajo=", "200x300/myserver.com/myimg.jpg"},
	{"ACME-SEC", "5Xr8gyuWE7jL_VB72K0wvzTMm2U=", "animated-one-frame.gif"},
	{"ACME-SEC", "5YRxzS2yxZxj9SZ50SoZ11eIdDI=", "filters:fill(blue)/image.jpg"},
	{"ACME-SEC", "_wIUeSaeHw8dricKG2MGhqu5thk=", "smart/image.jpg"},
	{"ACME-SEC", "gTr2Xr9lbzIa2CT_dL_O0GByeR0=", "animated.gif"},
	{"ACME-SEC", "lc6e3kkm_2Ww7NWho8HPOe-sqLU=", "smart/alabama1_ap620%C3%A9.jpg"},
	{"ACME-SEC", "oGi9XL4nHJKGEKVHjHEp1Jg1KpM=", "meta/animated.gif"},
	{"MYKEY", "Iw7LZGdr-hHj2gQ4ZzksP3llQHY=", "smart/image.jpg%3Fts%3D1"},
	{"MYKEY", "fxOHtHcTZMyuAQ1YPKh9KWg7nO8=", "smart/image.jpg%23something"},
	{"MYKEY", "nty7gpBIRJ3GWtYDLLw6q1PgqTo=", "smart/image.jpg"},
}

func thumborVerifier(tb testing.TB, o sign.ThumborOptions) *sign.KeyedVerifier {
	tb.Helper()
	v, err := sign.NewThumborVerifier(o)
	if err != nil {
		tb.Fatalf("NewThumborVerifier: %v", err)
	}
	return v
}

func TestThumborVectors(t *testing.T) {
	t.Parallel()
	for _, tc := range thumborVectors {
		t.Run(tc.key+"/"+tc.path, func(t *testing.T) {
			t.Parallel()
			keys := rawKeyring(tc.key, "")
			s, err := sign.NewSigner(sign.NewThumborScheme(), keys)
			if err != nil {
				t.Fatal(err)
			}
			if got := mustSign(t, s, tc.path); got != tc.sig {
				t.Fatalf("Sign = %q, want %q", got, tc.sig)
			}
			v := thumborVerifier(t, sign.ThumborOptions{Keys: keys})
			if res, err := v.Verify(tc.sig, tc.path); err != nil || res != sign.Verified {
				t.Fatalf("Verify = %v, %v", res, err)
			}
			full, err := s.SignThumborPath(tc.path)
			if err != nil || full != "/"+tc.sig+"/"+tc.path {
				t.Fatalf("SignThumborPath = %q, %v", full, err)
			}
		})
	}
}

func TestThumborVerify(t *testing.T) {
	t.Parallel()
	const (
		path = "smart/image.jpg"
		good = "_wIUeSaeHw8dricKG2MGhqu5thk="
	)
	keys := rawKeyring("ACME-SEC", "")
	rotated := rawKeyring("OLD", "", "ACME-SEC", "")
	tests := []struct {
		name string
		o    sign.ThumborOptions
		sig  string
		path string
		want sign.Result
		err  error
	}{
		{"verified", sign.ThumborOptions{Keys: keys}, good, path, sign.Verified, nil},
		{"rotation", sign.ThumborOptions{Keys: rotated}, good, path, sign.Verified, nil},
		{"salt ignored", sign.ThumborOptions{Keys: rawKeyring("ACME-SEC", "salt")}, good, path, sign.Verified, nil},
		{"other path", sign.ThumborOptions{Keys: keys}, good, "smart/other.jpg", sign.Unsigned, sign.ErrSignatureMismatch},
		{"leading slash is signed", sign.ThumborOptions{Keys: keys}, good, "/" + path, sign.Unsigned, sign.ErrSignatureMismatch},
		{"unpadded", sign.ThumborOptions{Keys: keys}, good[:27], path, sign.Unsigned, sign.ErrMalformedSignature},
		{"std alphabet", sign.ThumborOptions{Keys: keys}, "/wIUeSaeHw8dricKG2MGhqu5thk=", path, sign.Unsigned, sign.ErrMalformedSignature},
		{"garbage", sign.ThumborOptions{Keys: keys}, "not-a-signature-at-all-xxxx=", path, sign.Unsigned, sign.ErrMalformedSignature},
		{"unsafe", sign.ThumborOptions{Keys: keys}, sign.ThumborUnsafe, path, sign.Unsigned, nil},
		{"unsafe required", sign.ThumborOptions{Keys: keys, Required: true}, sign.ThumborUnsafe, path, sign.Unsigned, sign.ErrMissingSignature},
		{"empty required", sign.ThumborOptions{Keys: keys, Required: true}, "", path, sign.Unsigned, sign.ErrMissingSignature},
		{"signing off", sign.ThumborOptions{}, "anything", path, sign.Unsigned, nil},
		{"trusted", sign.ThumborOptions{Keys: keys, Trusted: []string{"t"}}, "t", path, sign.Trusted, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := thumborVerifier(t, tc.o)
			res, err := v.Verify(tc.sig, tc.path)
			if tc.err != nil {
				wantErr(t, err, tc.err)
				return
			}
			if err != nil || res != tc.want {
				t.Fatalf("Verify = %v, %v; want %v", res, err, tc.want)
			}
		})
	}
}

func TestThumborScheme(t *testing.T) {
	t.Parallel()
	s := sign.NewThumborScheme()
	if s.Name() != "thumbor" || s.Size() != 20 {
		t.Fatalf("Name/Size = %q/%d", s.Name(), s.Size())
	}
	if _, err := sign.NewThumborVerifier(sign.ThumborOptions{Required: true}); err == nil {
		t.Fatal("required without keys accepted")
	}
}

func BenchmarkThumborVerify(b *testing.B) {
	v := thumborVerifier(b, sign.ThumborOptions{Keys: rawKeyring("ACME-SEC", "")})
	b.ReportAllocs()
	for b.Loop() {
		_, _ = v.Verify("_wIUeSaeHw8dricKG2MGhqu5thk=", "smart/image.jpg")
	}
}
