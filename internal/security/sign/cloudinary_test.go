package sign_test

import (
	"crypto/sha1" //nolint:gosec // reference implementation of Cloudinary's SHA-1 signature
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

// cloudinarySecret is the api_secret the corpus generator
// (internal/dialect/cloudinary/testdata/urlgen/generate.ts) signs with.
const cloudinarySecret = "a676b67565c6767a6767d6767f676fe1" //nolint:gosec // public test secret of the corpus

// cloudinaryVectors were produced by the cloudinary Node SDK 2.11.0
// (cloudinary.v2.url with sign_url; see the dialect's corpus.json, entries
// sig_*). msg is the SDK's to_sign: transformations and public ID without
// the version segment.
var cloudinaryVectors = []struct {
	name, msg, algorithm, sig string
}{
	{"sha1_basic", "c_fill,h_200,w_300/sample.jpg", "sha1", "Bh1c0rqb"},
	{"sha1_version_removed", "c_scale,w_100/folder/sample.jpg", "sha1", "XjKdrQzI"},
	{"sha1_no_tx", "folder/sample.jpg", "sha1", "Mh0W0keP"},
	{"sha1_url_safe_underscore", "c_fill,w_300/e_grayscale/f_webp/sample.png", "sha1", "8Z_FnhqD"},
	{"sha1_fetch", "w_300/https://example.com/a%20b.jpg%3Fx%3D1", "sha1", "MRUGV-qq"},
	{"sha1_unicode", "w_100/folder/caf%C3%A9.jpg", "sha1", "CEleCxpI"},
	{"sha1_private", "w_100/secret/photo.jpg", "sha1", "iXuySGRt"},
	{"sha1_private_cdn", "w_100/sample.jpg", "sha1", "WsGl8cHM"},
	{"sha256_short", "c_scale,w_300/sample.jpg", "sha256", "i46GHAbs"},
	{"sha256_long", "c_scale,w_300/sample.jpg", "long", "i46GHAbs1Ed66mEYIrj83I6Y10E7Nu43"},
	{"sha256_long_chain", "c_fill,w_300/e_blur:300/folder/sample.jpg", "long", "RqauDDM-ekMKLY4XWtVkLInFGVKsiE27"},
}

// cloudinaryReference is the Node SDK's signature (cloudinary-npm
// lib/utils/index.js url + compute_hash, MIT): base64 of the digest of
// to_sign ‖ api_secret (cloudinarySecret), truncated to n characters, "/" → "_", "+" → "-".
func cloudinaryReference(msg, algorithm string) string {
	n := sign.CloudinaryShortLen
	var sum []byte
	switch algorithm {
	case "sha1":
		s := sha1.Sum([]byte(msg + cloudinarySecret)) //nolint:gosec // reference implementation
		sum = s[:]
	case "sha256":
		s := sha256.Sum256([]byte(msg + cloudinarySecret))
		sum = s[:]
	case "long":
		s := sha256.Sum256([]byte(msg + cloudinarySecret))
		sum, n = s[:], sign.CloudinaryLongLen
	}
	b := base64.StdEncoding.EncodeToString(sum)[:n]
	return strings.NewReplacer("/", "_", "+", "-").Replace(b)
}

func cloudinaryKeys() *sign.Keyring {
	return sign.NewKeyring(sign.NewKey([]byte(cloudinarySecret), nil))
}

func cloudinaryScheme(algorithm string) sign.Scheme {
	return sign.NewCloudinaryScheme(algorithm == "long", algorithm == "sha256")
}

func TestCloudinaryVectors(t *testing.T) {
	t.Parallel()
	for _, v := range cloudinaryVectors {
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()
			if got := cloudinaryReference(v.msg, v.algorithm); got != v.sig {
				t.Errorf("reference = %q, want SDK %q", got, v.sig)
			}
			s, err := sign.NewSigner(cloudinaryScheme(v.algorithm), cloudinaryKeys())
			if err != nil {
				t.Fatal(err)
			}
			seg, err := s.SignCloudinary(v.msg)
			if err != nil {
				t.Fatal(err)
			}
			if want := "s--" + v.sig + "--"; seg != want {
				t.Errorf("SignCloudinary = %q, want %q", seg, want)
			}
			ver, err := sign.NewCloudinaryVerifier(sign.CloudinaryOptions{Keys: cloudinaryKeys(), ShortSHA256: v.algorithm == "sha256"})
			if err != nil {
				t.Fatal(err)
			}
			if res, err := ver.Verify(v.sig, v.msg); err != nil || res != sign.Verified {
				t.Errorf("Verify = %v, %v", res, err)
			}
		})
	}
}

// TestCloudinaryReferenceRandom cross-checks the scheme against the
// reference on messages with characters that exercise the URL-safe
// alphabet.
func TestCloudinaryReferenceRandom(t *testing.T) {
	t.Parallel()
	for _, algorithm := range []string{"sha1", "sha256", "long"} {
		s, err := sign.NewSigner(cloudinaryScheme(algorithm), cloudinaryKeys())
		if err != nil {
			t.Fatal(err)
		}
		for i := range 200 {
			msg := strings.Repeat("w_1/", i%7) + "id" + strings.Repeat("x", i) + ".jpg"
			got, err := s.Sign(msg)
			if err != nil {
				t.Fatal(err)
			}
			if want := cloudinaryReference(msg, algorithm); got != want {
				t.Fatalf("%s %q: %q, want %q", algorithm, msg, got, want)
			}
		}
	}
}

func TestCloudinarySignature(t *testing.T) {
	t.Parallel()
	tests := []struct {
		seg, sig string
		ok       bool
	}{
		{"s--Bh1c0rqb--", "Bh1c0rqb", true},
		{"s--a--", "a", true},
		{"s----", "", false},
		{"s--abc", "", false},
		{"sabc--", "", false},
		{"w_100", "", false},
	}
	for _, tc := range tests {
		sig, ok := sign.CloudinarySignature(tc.seg)
		if sig != tc.sig || ok != tc.ok {
			t.Errorf("CloudinarySignature(%q) = %q, %v; want %q, %v", tc.seg, sig, ok, tc.sig, tc.ok)
		}
	}
}

func TestCloudinaryVerifier(t *testing.T) {
	t.Parallel()
	const msg = "c_fill,h_200,w_300/sample.jpg"
	newV := func(o sign.CloudinaryOptions) *sign.CloudinaryVerifier {
		t.Helper()
		v, err := sign.NewCloudinaryVerifier(o)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	keys := cloudinaryKeys()
	long := cloudinaryReference(msg, "long")
	tests := []struct {
		name string
		o    sign.CloudinaryOptions
		sig  string
		want sign.Result
		err  error
	}{
		{"off", sign.CloudinaryOptions{}, "garbage", sign.Unsigned, nil},
		{"short", sign.CloudinaryOptions{Keys: keys}, "Bh1c0rqb", sign.Verified, nil},
		{"long", sign.CloudinaryOptions{Keys: keys}, long, sign.Verified, nil},
		{"short_sha256_rejects_sha1", sign.CloudinaryOptions{Keys: keys, ShortSHA256: true}, "Bh1c0rqb", sign.Unsigned, sign.ErrSignatureMismatch},
		{"long_only_rejects_short", sign.CloudinaryOptions{Keys: keys, LongOnly: true}, "Bh1c0rqb", sign.Unsigned, sign.ErrMalformedSignature},
		{"long_only_accepts_long", sign.CloudinaryOptions{Keys: keys, LongOnly: true}, long, sign.Verified, nil},
		{"mismatch", sign.CloudinaryOptions{Keys: keys}, "Bh1c0rqc", sign.Unsigned, sign.ErrSignatureMismatch},
		{"bad_length", sign.CloudinaryOptions{Keys: keys}, "Bh1c0rq", sign.Unsigned, sign.ErrMalformedSignature},
		{"bad_alphabet", sign.CloudinaryOptions{Keys: keys}, "Bh1c0r+b", sign.Unsigned, sign.ErrMalformedSignature},
		{"absent", sign.CloudinaryOptions{Keys: keys}, "", sign.Unsigned, nil},
		{"absent_required", sign.CloudinaryOptions{Keys: keys, Required: true}, "", sign.Unsigned, sign.ErrMissingSignature},
		{"trusted", sign.CloudinaryOptions{Keys: keys, Trusted: []string{"trusted1"}}, "trusted1", sign.Trusted, nil},
		{"rotation", sign.CloudinaryOptions{Keys: sign.NewKeyring(sign.NewKey([]byte("old"), nil), sign.NewKey([]byte(cloudinarySecret), nil))}, "Bh1c0rqb", sign.Verified, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := newV(tc.o)
			res, err := v.Verify(tc.sig, msg)
			if res != tc.want || !errors.Is(err, tc.err) {
				t.Fatalf("Verify = %v, %v; want %v, %v", res, err, tc.want, tc.err)
			}
			if err != nil && errs.KindOf(err) != errs.KindUnauthorized {
				t.Errorf("kind %v", errs.KindOf(err))
			}
		})
	}
	if _, err := sign.NewCloudinaryVerifier(sign.CloudinaryOptions{Required: true}); err == nil {
		t.Error("required without keys accepted")
	}
	if !newV(sign.CloudinaryOptions{Keys: keys}).Enabled() || newV(sign.CloudinaryOptions{}).Enabled() {
		t.Error("Enabled")
	}
}

// FuzzCloudinaryVerify: Verify never panics, fails only with
// KindUnauthorized, and accepts what the signer produces.
func FuzzCloudinaryVerify(f *testing.F) {
	for _, v := range cloudinaryVectors {
		f.Add(v.sig, v.msg)
	}
	for _, s := range [][2]string{{"", "x"}, {"s--x--", ""}, {"====", "/"}, {strings.Repeat("A", 300), "x"}, {"\x00\xff", "\x00"}} {
		f.Add(s[0], s[1])
	}
	v, err := sign.NewCloudinaryVerifier(sign.CloudinaryOptions{Keys: cloudinaryKeys()})
	if err != nil {
		f.Fatal(err)
	}
	short, err := sign.NewSigner(cloudinaryScheme("sha1"), cloudinaryKeys())
	if err != nil {
		f.Fatal(err)
	}
	long, err := sign.NewSigner(cloudinaryScheme("long"), cloudinaryKeys())
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, sig, msg string) {
		res, err := v.Verify(sig, msg)
		if err != nil && (res != sign.Unsigned || errs.KindOf(err) != errs.KindUnauthorized) {
			t.Fatalf("Verify(%q, %q) = %v, %v", sig, msg, res, err)
		}
		for _, s := range []*sign.Signer{short, long} {
			good, err := s.Sign(msg)
			if err != nil {
				t.Fatal(err)
			}
			if res, err := v.Verify(good, msg); err != nil || res != sign.Verified {
				t.Fatalf("round trip %q: %v, %v", msg, res, err)
			}
		}
	})
}

func BenchmarkCloudinaryVerify(b *testing.B) {
	v, err := sign.NewCloudinaryVerifier(sign.CloudinaryOptions{Keys: cloudinaryKeys()})
	if err != nil {
		b.Fatal(err)
	}
	const msg = "c_fill,h_200,w_300/sample.jpg"
	long := cloudinaryReference(msg, "long")
	for _, c := range []struct{ name, sig string }{{"short", "Bh1c0rqb"}, {"long", long}} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, _ = v.Verify(c.sig, msg)
			}
		})
	}
}
