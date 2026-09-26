package query_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

// imaginaryReference is imaginary's validateURLSignature (middleware.go,
// MIT) reduced to the expected signature, on a URL
// parsed the way net/http parses the request target.
func imaginaryReference(key, target string) string {
	// http.Server parses the request target with url.ParseRequestURI.
	r := struct{ URL *url.URL }{}
	r.URL, _ = url.ParseRequestURI(target)
	q := r.URL.Query()
	q.Del("sign")
	h := hmac.New(sha256.New, []byte(key))
	_, _ = h.Write([]byte(r.URL.Path))
	_, _ = h.Write([]byte(q.Encode()))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func TestSigning(t *testing.T) {
	t.Parallel()
	f := getFixture(t)
	// imaginary's own algorithm (imaginaryReference) over "/resize" +
	// "height=200&type=jpeg&url=https%3A%2F%2Fexample.com%2Fa.jpg&width=300".
	const readmeLike = "width=300&height=200&type=jpeg&url=https://example.com/a.jpg"
	imSig := imaginaryReference(imaginaryKeyRaw, "/resize?"+readmeLike)
	signedIm, err := f.imaginary.SignImaginaryQuery("/resize", readmeLike)
	if err != nil {
		t.Fatal(err)
	}
	if signedIm != "height=200&type=jpeg&url=https%3A%2F%2Fexample.com%2Fa.jpg&width=300&sign="+imSig {
		t.Fatalf("SignImaginaryQuery = %q, reference sign %q", signedIm, imSig)
	}
	nativeQ := "url=https://example.com/a.jpg&w=300&h=200"
	signedNative, err := f.imaginary.SignImaginaryQuery("/", nativeQ)
	if err != nil {
		t.Fatal(err)
	}
	signedXI, err := f.xi.SignXIQuery("/", nativeQ)
	if err != nil {
		t.Fatal(err)
	}
	signedXIEndpoint, err := f.xi.SignXIQuery("/resize", readmeLike)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, cfg, path, query string
		want                   sign.Result
		err                    error
	}{
		{"imaginary endpoint", "signed", "/resize", readmeLike + "&sign=" + imSig, sign.Verified, nil},
		{"imaginary endpoint sign first", "signed", "/resize", "sign=" + imSig + "&" + readmeLike, sign.Verified, nil},
		{"imaginary endpoint re-encoded", "signed", "/resize", signedIm, sign.Verified, nil},
		{"imaginary mount-relative path without slash", "signed", "resize", readmeLike + "&sign=" + imSig, sign.Verified, nil},
		{"imaginary native", "signed", "/", signedNative, sign.Verified, nil},
		{"imaginary wrong path", "signed", "/crop", readmeLike + "&sign=" + imSig, sign.Unsigned, sign.ErrSignatureMismatch},
		{"imaginary tampered", "signed", "/resize", readmeLike + "&width=301&sign=" + imSig, sign.Unsigned, sign.ErrSignatureMismatch},
		{"imaginary malformed", "signed", "/resize", readmeLike + "&sign=!!", sign.Unsigned, sign.ErrMalformedSignature},
		{"imaginary duplicate", "signed", "/resize", readmeLike + "&sign=" + imSig + "&sign=" + imSig, sign.Unsigned, sign.ErrMalformedSignature},
		{"xi native", "signed", "/", signedXI, sign.Verified, nil},
		{"xi endpoint", "signed", "/resize", signedXIEndpoint, sign.Verified, nil},
		{"xi tampered", "signed", "/", signedXI + "&w=1", sign.Unsigned, sign.ErrSignatureMismatch},
		{"both", "signed", "/", signedXI + "&sign=" + imSig, sign.Unsigned, sign.ErrMalformedSignature},
		{"unsigned allowed", "signed", "/", nativeQ, sign.Unsigned, nil},
		{"unsigned required", "required", "/", nativeQ, sign.Unsigned, sign.ErrMissingSignature},
		{"required with xi", "required", "/", signedXI, sign.Verified, nil},
		{"signing off", "default", "/resize", readmeLike + "&sign=anything", sign.Unsigned, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := f.parsers[tc.cfg].Parse(&dialect.Input{Path: tc.path, RawQuery: tc.query})
			if tc.err != nil {
				if !errors.Is(err, tc.err) || errs.KindOf(err) != errs.KindUnauthorized {
					t.Fatalf("err = %v, want %v", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if res.Signature != tc.want {
				t.Fatalf("signature %v, want %v", res.Signature, tc.want)
			}
		})
	}
}
