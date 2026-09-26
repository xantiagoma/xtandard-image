package sign_test

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

func TestXIMessage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, path, query string
		sig, msg          string
		bad               bool
	}{
		{"no query", "/a.jpg", "", "", "/a.jpg", false},
		{"only sig", "/a.jpg", "xi_sig=abc", "abc", "/a.jpg", false},
		{"sig last", "/a.jpg", "w=1&h=2&xi_sig=abc", "abc", "/a.jpg?w=1&h=2", false},
		{"sig first", "/a.jpg", "xi_sig=abc&w=1&h=2", "abc", "/a.jpg?w=1&h=2", false},
		{"sig middle", "/a.jpg", "w=1&xi_sig=abc&h=2", "abc", "/a.jpg?w=1&h=2", false},
		{"order kept", "/a.jpg", "h=2&w=1", "", "/a.jpg?h=2&w=1", false},
		{"encoding kept", "/a.jpg", "url=https%3A%2F%2Fx&xi_sig=s", "s", "/a.jpg?url=https%3A%2F%2Fx", false},
		{"empty segments", "/a.jpg", "&w=1&&xi_sig=s&", "s", "/a.jpg?w=1", false},
		{"bare key", "/a.jpg", "xi_sig&w=1", "", "/a.jpg?w=1", false},
		{"lookalike keys kept", "/a.jpg", "xi_sig2=1&xxi_sig=2&xi%5Fsig=3", "", "/a.jpg?xi_sig2=1&xxi_sig=2&xi%5Fsig=3", false},
		{"duplicate", "/a.jpg", "xi_sig=a&xi_sig=b", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sig, msg, err := sign.XIMessage(tc.path, tc.query)
			if tc.bad {
				wantErr(t, err, sign.ErrMalformedSignature)
				return
			}
			if err != nil || sig != tc.sig || msg != tc.msg {
				t.Fatalf("XIMessage = %q, %q, %v; want %q, %q", sig, msg, err, tc.sig, tc.msg)
			}
		})
	}
}

func TestXIVector(t *testing.T) {
	t.Parallel()
	keys := hexKeyring(t, exampleKey, exampleSalt)
	v, err := sign.NewXIVerifier(sign.XIOptions{Keys: keys, Size: 32})
	if err != nil {
		t.Fatal(err)
	}
	// The xi_sig message is the imgproxy construction over path?query, so
	// it is checked against an independent HMAC (Python hmac, openssl).
	sig, msg, err := sign.XIMessage(exampleXIPath, exampleXIQ+"&xi_sig="+exampleXISig)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := v.Verify(sig, msg); err != nil || res != sign.Verified {
		t.Fatalf("Verify = %v, %v", res, err)
	}
}

func TestXIRoundTrip(t *testing.T) {
	t.Parallel()
	keys := hexKeyring(t, exampleKey, exampleSalt)
	scheme, err := sign.NewXIScheme(16)
	if err != nil {
		t.Fatal(err)
	}
	s, err := sign.NewSigner(scheme, keys)
	if err != nil {
		t.Fatal(err)
	}
	v, err := sign.NewXIVerifier(sign.XIOptions{Keys: keys, Size: 16})
	if err != nil {
		t.Fatal(err)
	}
	const path = "/next/_next/image"
	for _, q := range []string{"", "url=%2Fa.jpg&w=640&q=75", "xi_sig=stale&w=640"} {
		signed, err := s.SignXIQuery(path, q)
		if err != nil {
			t.Fatal(err)
		}
		sig, msg, err := sign.XIMessage(path, signed)
		if err != nil {
			t.Fatal(err)
		}
		if res, err := v.Verify(sig, msg); err != nil || res != sign.Verified {
			t.Fatalf("query %q signed %q: Verify = %v, %v", q, signed, res, err)
		}
		// Tampering with the query, the path or the parameter order fails.
		for _, tampered := range []struct{ path, query string }{
			{path, signed + "&w=1"},
			{path + "x", signed},
			{path, "w=641&" + signed},
		} {
			sig, msg, err := sign.XIMessage(tampered.path, tampered.query)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := v.Verify(sig, msg); err == nil {
				t.Fatalf("tampered %+v verified", tampered)
			}
		}
	}
	// Absent xi_sig: Unsigned (policy decides) unless required.
	if res, err := v.Verify("", path); err != nil || res != sign.Unsigned {
		t.Fatalf("absent: %v, %v", res, err)
	}
	req, err := sign.NewXIVerifier(sign.XIOptions{Keys: keys, Size: 16, Required: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = req.Verify("", path)
	wantErr(t, err, sign.ErrMissingSignature)
	if _, err := sign.NewXIVerifier(sign.XIOptions{Keys: keys, Size: 0}); err == nil {
		t.Fatal("size 0 accepted")
	}
}

func TestSignImgproxyPath(t *testing.T) {
	t.Parallel()
	s := signer(t, 32, hexKeyring(t, docsKey, docsSalt))
	for _, p := range []string{docsPath, docsPath[1:]} {
		got, err := s.SignImgproxyPath(p)
		if err != nil {
			t.Fatal(err)
		}
		if want := "/" + docsSig + docsPath; got != want {
			t.Fatalf("SignImgproxyPath(%q) = %q, want %q", p, got, want)
		}
	}
}
