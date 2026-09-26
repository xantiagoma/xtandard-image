package sign_test

import (
	"crypto/md5" //nolint:gosec // G501: reference implementation of the imgix scheme
	"encoding/hex"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

// imgixToken is the secure URL token of the imgix-blueprint examples.
const imgixToken = "FOO123bar"

func imgixKeyring(tokens ...string) *sign.Keyring {
	keys := make([]sign.Key, 0, len(tokens))
	for _, t := range tokens {
		keys = append(keys, sign.NewKey([]byte(t), nil))
	}
	return sign.NewKeyring(keys...)
}

func imgixVerifier(tb testing.TB, o sign.ImgixOptions) *sign.KeyedVerifier {
	tb.Helper()
	v, err := sign.NewImgixVerifier(o)
	if err != nil {
		tb.Fatal(err)
	}
	return v
}

// TestImgixBlueprintVectors checks every "Securing URLs" example of
// imgix-blueprint README.md (the vectors all imgix SDKs test against).
func TestImgixBlueprintVectors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, path, query, sig string
	}{
		{"simple path", "/users/1.png", "", "6797c24146142d5b40bde3141fd3600c"},
		{"fully qualified", "/http%3A%2F%2Favatars.com%2Fjohn-smith.png", "", "493a52f008c91416351f8b33d4883135"},
		{"simple path with params", "/users/1.png", "w=400&h=300", "c7b86f666a832434dd38577e38cf86d1"},
		{"simple path alphabetized", "/users/1.png", "h=300&w=400", "1a4e48641614d1109c6a7af51be23d18"},
		{"fully qualified with params", "/http%3A%2F%2Favatars.com%2Fjohn-smith.png", "w=400&h=300", "61ea1cc7add87653bb0695fe25f2b534"},
		{"fully qualified alphabetized", "/http%3A%2F%2Favatars.com%2Fjohn-smith.png", "h=300&w=400", "a201fe1a3caef4944dcb40f6ce99e746"},
		// @imgix/js-core 3.8.0 buildURL with txt64 (blueprint parameter encoding example).
		{"js-core txt64", "/users/1.png", "txt64=dGhpcy9zZWVtc-KApiBwcmV0dHkgc2tldGNoeSEg8J-YgQ", "90f614fddc8ae5b9d5eb2f69b25daed4"},
	}
	// Rotation: the matching token is the second one.
	keys := imgixKeyring("other-token", imgixToken)
	v := imgixVerifier(t, sign.ImgixOptions{Keys: keys, Required: true})
	s, err := sign.NewSigner(sign.NewImgixScheme(), imgixKeyring(imgixToken))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base := imgixToken + tc.path
			if tc.query != "" {
				base += "?" + tc.query
			}
			ref := md5.Sum([]byte(base)) //nolint:gosec // G401: reference imgix signature
			if hex.EncodeToString(ref[:]) != tc.sig {
				t.Fatalf("reference = %x, vector %s", ref, tc.sig)
			}
			q := "s=" + tc.sig
			if tc.query != "" {
				q = tc.query + "&" + q
			}
			sig, msg, err := sign.ImgixMessage(tc.path, q)
			if err != nil {
				t.Fatal(err)
			}
			if res, err := v.Verify(sig, msg); err != nil || res != sign.Verified {
				t.Fatalf("Verify = %v, %v", res, err)
			}
			// Upper-case hex is accepted.
			if res, err := v.Verify(strings.ToUpper(sig), msg); err != nil || res != sign.Verified {
				t.Fatalf("Verify upper = %v, %v", res, err)
			}
			got, err := s.SignImgixQuery(tc.path, tc.query)
			if err != nil || got != q {
				t.Fatalf("SignImgixQuery = %q, %v; want %q", got, err, q)
			}
		})
	}
}

func TestImgixMessage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, path, query string
		sig, msg          string
		bad               bool
	}{
		{"no query", "/a.jpg", "", "", "/a.jpg", false},
		{"only s", "/a.jpg", "s=abc", "abc", "/a.jpg", false},
		{"s last", "/a.jpg", "w=1&h=2&s=abc", "abc", "/a.jpg?w=1&h=2", false},
		{"s first", "/a.jpg", "s=abc&w=1", "abc", "/a.jpg?w=1", false},
		{"encoding kept", "/a%20b.jpg", "txt=a%2Fb+c&s=x", "x", "/a%20b.jpg?txt=a%2Fb+c", false},
		{"empty segments", "/a", "&w=1&&s=x&", "x", "/a?w=1", false},
		{"s64 is not s", "/a", "s64=eA&w=1", "", "/a?s64=eA&w=1", false},
		{"escaped s is not s", "/a", "%73=x", "", "/a?%73=x", false},
		{"duplicate", "/a", "s=a&s=b", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sig, msg, err := sign.ImgixMessage(tc.path, tc.query)
			if tc.bad {
				wantErr(t, err, sign.ErrMalformedSignature)
				return
			}
			if err != nil || sig != tc.sig || msg != tc.msg {
				t.Fatalf("ImgixMessage = %q, %q, %v; want %q, %q", sig, msg, err, tc.sig, tc.msg)
			}
		})
	}
}

func TestImgixVerify(t *testing.T) {
	t.Parallel()
	const (
		msg = "/users/1.png?w=400&h=300"
		sig = "c7b86f666a832434dd38577e38cf86d1"
	)
	keys := imgixKeyring(imgixToken)
	tests := []struct {
		name string
		o    sign.ImgixOptions
		sig  string
		want sign.Result
		err  error
	}{
		{"off", sign.ImgixOptions{}, "", sign.Unsigned, nil},
		{"off ignores bad", sign.ImgixOptions{}, "zz", sign.Unsigned, nil},
		{"absent optional", sign.ImgixOptions{Keys: keys}, "", sign.Unsigned, nil},
		{"absent required", sign.ImgixOptions{Keys: keys, Required: true}, "", sign.Unsigned, sign.ErrMissingSignature},
		{"valid", sign.ImgixOptions{Keys: keys}, sig, sign.Verified, nil},
		{"trusted", sign.ImgixOptions{Keys: keys, Trusted: []string{"t0k"}}, "t0k", sign.Trusted, nil},
		{"mismatch", sign.ImgixOptions{Keys: keys}, "6797c24146142d5b40bde3141fd3600c", sign.Unsigned, sign.ErrSignatureMismatch},
		{"not hex", sign.ImgixOptions{Keys: keys}, "zz97c24146142d5b40bde3141fd3600c", sign.Unsigned, sign.ErrMalformedSignature},
		{"short", sign.ImgixOptions{Keys: keys}, sig[:30], sign.Unsigned, sign.ErrMalformedSignature},
		{"long", sign.ImgixOptions{Keys: keys}, sig + "00", sign.Unsigned, sign.ErrMalformedSignature},
		{"odd", sign.ImgixOptions{Keys: keys}, sig[:31], sign.Unsigned, sign.ErrMalformedSignature},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := imgixVerifier(t, tc.o)
			res, err := v.Verify(tc.sig, msg)
			if tc.err != nil {
				wantErr(t, err, tc.err)
				return
			}
			if err != nil || res != tc.want {
				t.Fatalf("Verify = %v, %v; want %v", res, err, tc.want)
			}
		})
	}
	if _, err := sign.NewImgixVerifier(sign.ImgixOptions{Required: true}); err == nil {
		t.Error("required without keys accepted")
	}
}

func FuzzImgixMessage(f *testing.F) {
	for _, s := range [][2]string{
		{"/users/1.png", ""},
		{"/users/1.png", "w=400&h=300&s=c7b86f666a832434dd38577e38cf86d1"},
		{"/a", "s=a&s=b"},
		{"/a", "&&&"},
		{"", "s"},
		{"/a", "a=%zz&b==c&s64=x"},
	} {
		f.Add(s[0], s[1])
	}
	keys := imgixKeyring(imgixToken)
	s, err := sign.NewSigner(sign.NewImgixScheme(), keys)
	if err != nil {
		f.Fatal(err)
	}
	v := imgixVerifier(f, sign.ImgixOptions{Keys: keys})
	f.Fuzz(func(t *testing.T, path, query string) {
		_, msg, err := sign.ImgixMessage(path, query)
		if err != nil {
			if errs.KindOf(err) != errs.KindUnauthorized {
				t.Fatalf("kind = %v", errs.KindOf(err))
			}
			return
		}
		if !strings.HasPrefix(msg, path) {
			t.Fatalf("msg %q lacks path %q", msg, path)
		}
		if rest := msg[len(path):]; rest != "" {
			if rest[0] != '?' {
				t.Fatalf("msg %q: query not introduced by ?", msg)
			}
			for p := range strings.SplitSeq(rest[1:], "&") {
				if k, _, _ := strings.Cut(p, "="); p == "" || k == sign.ImgixParam {
					t.Fatalf("msg %q keeps an empty or s segment", msg)
				}
			}
		}
		// Signing then verifying round-trips.
		q, err := s.SignImgixQuery(path, query)
		if err != nil {
			t.Fatal(err)
		}
		sig, msg2, err := sign.ImgixMessage(path, q)
		if err != nil || msg2 != msg {
			t.Fatalf("re-split = %q, %v; want %q", msg2, err, msg)
		}
		if res, err := v.Verify(sig, msg2); err != nil || res != sign.Verified {
			t.Fatalf("Verify = %v, %v", res, err)
		}
	})
}

func BenchmarkImgixVerify(b *testing.B) {
	const q = "w=400&h=300&fit=crop&auto=format&s=c7b86f666a832434dd38577e38cf86d1"
	v := imgixVerifier(b, sign.ImgixOptions{Keys: imgixKeyring(imgixToken)})
	b.ReportAllocs()
	for b.Loop() {
		sig, msg, _ := sign.ImgixMessage("/users/1.png", q)
		_, _ = v.Verify(sig, msg)
	}
}
