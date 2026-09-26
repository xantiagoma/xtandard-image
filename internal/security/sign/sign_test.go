package sign_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

func wantErr(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("err = %v, want %v", err, target)
	}
	if k := errs.KindOf(err); k != errs.KindUnauthorized {
		t.Fatalf("KindOf(err) = %v, want unauthorized", k)
	}
	if s := errs.Status(errs.KindOf(err)); s != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", s)
	}
}

func TestImgproxyUpstreamVectors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		keys *sign.Keyring
		size int
		sig  string
		path string
	}{
		{"docs signing_url", hexKeyring(t, docsKey, docsSalt), 32, docsSig, docsPath},
		{"examples signature", hexKeyring(t, exampleKey, exampleSalt), 32, exampleSig, examplePath},
		{"examples signature-truncated", hexKeyring(t, exampleKey, exampleSalt), 8, exampleSig8, examplePath},
		// imgproxy security/signature_test.go (raw keys; path without "/").
		{"TestVerifySignature", rawKeyring("test-key", "test-salt"), 32, "oWaL7QoW5TsgbuiS9-5-DI8S3Ibbo1gdB2SteJh3a20", "asd"},
		{"TestVerifySignatureTruncated", rawKeyring("test-key", "test-salt"), 8, "oWaL7QoW5Ts", "asd"},
		{"leading slash is equivalent", rawKeyring("test-key", "test-salt"), 32, "oWaL7QoW5TsgbuiS9-5-DI8S3Ibbo1gdB2SteJh3a20", "/asd"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := imgproxyVerifier(t, sign.ImgproxyOptions{Keys: tc.keys, Size: tc.size})
			res, err := v.Verify(tc.sig, tc.path)
			if err != nil || res != sign.Verified {
				t.Fatalf("Verify = %v, %v; want verified", res, err)
			}
			if got := mustSign(t, signer(t, tc.size, tc.keys), tc.path); got != tc.sig {
				t.Fatalf("Sign = %q, want %q", got, tc.sig)
			}
		})
	}
}

// Ported from imgproxy security/signature_test.go (Apache-2.0).
func TestImgproxyUpstreamSuite(t *testing.T) {
	t.Parallel()
	one := rawKeyring("test-key", "test-salt")
	two := rawKeyring("test-key", "test-salt", "test-key2", "test-salt2")
	tests := []struct {
		name    string
		keys    *sign.Keyring
		size    int
		trusted []string
		sig     string
		path    string
		want    sign.Result
		err     error
	}{
		{"Invalid (truncated sig, full size)", one, 32, nil, "oWaL7QoW5Ts", "asd", sign.Unsigned, sign.ErrMalformedSignature},
		{"MultiplePairs second", two, 32, nil, "jYz1UZ7j1BCdSzH3pZhaYf0iuz0vusoOTdqJsUT6WXI", "asd", sign.Verified, nil},
		{"MultiplePairs first", two, 32, nil, "oWaL7QoW5TsgbuiS9-5-DI8S3Ibbo1gdB2SteJh3a20", "asd", sign.Verified, nil},
		{"MultiplePairs none", two, 32, nil, "dtLwhdnPPis", "asd", sign.Unsigned, sign.ErrMalformedSignature},
		{"EmptyPath", one, 32, nil, "oWaL7QoW5TsgbuiS9-5-DI8S3Ibbo1gdB2SteJh3a20", "", sign.Unsigned, sign.ErrSignatureMismatch},
		{"Trusted", one, 32, []string{"truested"}, "truested", "asd", sign.Trusted, nil},
		{"Untrusted", one, 32, []string{"truested"}, "untrusted", "asd", sign.Unsigned, sign.ErrMalformedSignature},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := imgproxyVerifier(t, sign.ImgproxyOptions{Keys: tc.keys, Size: tc.size, Trusted: tc.trusted})
			res, err := v.Verify(tc.sig, tc.path)
			if res != tc.want {
				t.Fatalf("result = %v, want %v", res, tc.want)
			}
			if tc.err == nil {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			wantErr(t, err, tc.err)
		})
	}
}

func TestImgproxySigningOff(t *testing.T) {
	t.Parallel()
	v := imgproxyVerifier(t, sign.ImgproxyOptions{})
	for _, sig := range []string{"insecure", "unsafe", "_", "anything", "", docsSig} {
		res, err := v.Verify(sig, docsPath)
		if err != nil || res != sign.Unsigned {
			t.Errorf("Verify(%q) = %v, %v; want unsigned", sig, res, err)
		}
	}
	// imgproxy newMalformedSignatureError: an option where the signature goes.
	_, err := v.Verify("rs:fill:300:400", "/g:sm/plain/x")
	wantErr(t, err, sign.ErrOptionAsSignature)
	se, ok := errors.AsType[*errs.Error](err)
	if !ok || !strings.Contains(se.PublicMessage(), "processing option") {
		t.Fatalf("public message = %v", err)
	}
}

func TestPlaceholders(t *testing.T) {
	t.Parallel()
	keys := hexKeyring(t, docsKey, docsSalt)
	for _, required := range []bool{false, true} {
		v := imgproxyVerifier(t, sign.ImgproxyOptions{Keys: keys, Required: required})
		for _, sig := range []string{"insecure", "unsafe", "_", ""} {
			res, err := v.Verify(sig, docsPath)
			if res != sign.Unsigned {
				t.Errorf("required=%v Verify(%q) result = %v", required, sig, res)
			}
			if required {
				wantErr(t, err, sign.ErrMissingSignature)
			} else if err != nil {
				t.Errorf("required=%v Verify(%q) err = %v", required, sig, err)
			}
		}
		// A real but wrong signature is rejected regardless of Required.
		_, err := v.Verify(exampleSig, docsPath)
		wantErr(t, err, sign.ErrSignatureMismatch)
	}
}

func TestKeyRotation(t *testing.T) {
	t.Parallel()
	oldKeys := hexKeyring(t, docsKey, docsSalt)
	newKeys := hexKeyring(t, exampleKey, exampleSalt)
	both := hexKeyring(t, exampleKey, exampleSalt, docsKey, docsSalt)

	oldSig := mustSign(t, signer(t, 32, oldKeys), docsPath)
	newSig := mustSign(t, signer(t, 32, newKeys), docsPath)
	if got := mustSign(t, signer(t, 32, both), docsPath); got != newSig {
		t.Fatalf("signer must use the first (primary) key")
	}

	tests := []struct {
		name string
		keys *sign.Keyring
		sig  string
		ok   bool
	}{
		{"old on old", oldKeys, oldSig, true},
		{"new on old", oldKeys, newSig, false},
		{"old during rotation", both, oldSig, true},
		{"new during rotation", both, newSig, true},
		{"old after rotation", newKeys, oldSig, false},
		{"new after rotation", newKeys, newSig, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := imgproxyVerifier(t, sign.ImgproxyOptions{Keys: tc.keys})
			res, err := v.Verify(tc.sig, docsPath)
			if tc.ok != (err == nil) || tc.ok != (res == sign.Verified) {
				t.Fatalf("Verify = %v, %v; want ok=%v", res, err, tc.ok)
			}
		})
	}
}

func TestSignatureSizeTruncation(t *testing.T) {
	t.Parallel()
	keys := hexKeyring(t, exampleKey, exampleSalt)
	full := mustSign(t, signer(t, 32, keys), examplePath)
	for size := sign.MinSignatureSize; size <= sign.MaxSignatureSize; size++ {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			t.Parallel()
			sig := mustSign(t, signer(t, size, keys), examplePath)
			if want := (size*8 + 5) / 6; len(sig) != want {
				t.Fatalf("len(sig) = %d, want %d", len(sig), want)
			}
			// A truncated signature is a prefix of the full one's bytes;
			// base64 prefixes agree on every complete 6-bit group.
			if n := size * 8 / 6; sig[:n] != full[:n] {
				t.Fatalf("sig %q is not a prefix of %q", sig, full)
			}
			v := imgproxyVerifier(t, sign.ImgproxyOptions{Keys: keys, Size: size})
			if res, err := v.Verify(sig, examplePath); err != nil || res != sign.Verified {
				t.Fatalf("Verify(own size) = %v, %v", res, err)
			}
			if size != 32 {
				_, err := v.Verify(full, examplePath)
				wantErr(t, err, sign.ErrMalformedSignature)
			}
		})
	}
	for _, size := range []int{-1, 0, 33, 64} {
		if _, err := sign.NewImgproxyScheme(size); err == nil {
			t.Errorf("NewImgproxyScheme(%d) accepted", size)
		}
		if _, err := sign.NewImgproxyVerifier(sign.ImgproxyOptions{Keys: keys, Size: size}); err == nil {
			t.Errorf("NewImgproxyVerifier(size %d) accepted", size)
		}
	}
}

func TestTrustedBypass(t *testing.T) {
	t.Parallel()
	keys := hexKeyring(t, docsKey, docsSalt)
	trusted := []string{"partner-cdn-token", "Zm9vYmFy"}
	v := imgproxyVerifier(t, sign.ImgproxyOptions{Keys: keys, Trusted: trusted, Required: true})
	xi, err := sign.NewXIVerifier(sign.XIOptions{Keys: keys, Size: 32, Trusted: trusted})
	if err != nil {
		t.Fatal(err)
	}
	for _, ver := range []sign.Verifier{v, xi} {
		for _, sig := range trusted {
			// Any message: the trusted list bypasses the MAC.
			if res, err := ver.Verify(sig, "/anything/at/all"); err != nil || res != sign.Trusted {
				t.Errorf("Verify(%q) = %v, %v; want trusted", sig, res, err)
			}
		}
		for _, sig := range []string{"partner-cdn-toke", "partner-cdn-token2", "PARTNER-CDN-TOKEN", "Zm9vYmFy "} {
			if res, err := ver.Verify(sig, "/x"); err == nil || res != sign.Unsigned {
				t.Errorf("near-miss Verify(%q) = %v, %v; want error", sig, res, err)
			}
		}
	}
	// Trusted signatures do nothing while signing is off (imgproxy parity).
	off := imgproxyVerifier(t, sign.ImgproxyOptions{Trusted: trusted})
	if res, err := off.Verify(trusted[0], "/x"); err != nil || res != sign.Unsigned {
		t.Errorf("signing off: Verify(trusted) = %v, %v", res, err)
	}
	if _, err := sign.NewImgproxyVerifier(sign.ImgproxyOptions{Keys: keys, Size: 32, Trusted: []string{""}}); err == nil {
		t.Errorf("empty trusted signature accepted")
	}
}

func TestTamperRejected(t *testing.T) {
	t.Parallel()
	keys := hexKeyring(t, docsKey, docsSalt)
	v := imgproxyVerifier(t, sign.ImgproxyOptions{Keys: keys})
	flip := func(s string, i int) string {
		b := []byte(s)
		if b[i] == 'A' {
			b[i] = 'B'
		} else {
			b[i] = 'A'
		}
		return string(b)
	}
	cases := map[string][2]string{
		"first char":    {flip(docsSig, 0), docsPath},
		"last char":     {flip(docsSig, len(docsSig)-1), docsPath},
		"path changed":  {docsSig, docsPath + "x"},
		"path prefix":   {docsSig, docsPath[:len(docsPath)-1]},
		"padded":        {docsSig + "=", docsPath},
		"std alphabet":  {strings.ReplaceAll(docsSig, "_", "/"), docsPath},
		"too long":      {strings.Repeat("A", 1000), docsPath},
		"option in sig": {"rs:fill:300:400", docsPath},
	}
	for name, c := range cases {
		res, err := v.Verify(c[0], c[1])
		if err == nil || res != sign.Unsigned || errs.KindOf(err) != errs.KindUnauthorized {
			t.Errorf("%s: Verify = %v, %v; want unauthorized", name, res, err)
		}
	}
}

func TestNewVerifierValidation(t *testing.T) {
	t.Parallel()
	if _, err := sign.NewImgproxyVerifier(sign.ImgproxyOptions{Size: 32, Required: true}); err == nil {
		t.Error("Required without keys accepted")
	}
	if _, err := sign.NewVerifier(nil, sign.Options{}); err == nil {
		t.Error("nil scheme accepted")
	}
	if _, err := sign.NewSigner(sign.Scheme(nil), rawKeyring("k", "s")); err == nil {
		t.Error("NewSigner nil scheme accepted")
	}
	scheme, _ := sign.NewImgproxyScheme(32)
	if _, err := sign.NewSigner(scheme, sign.NewKeyring()); err == nil {
		t.Error("NewSigner empty keyring accepted")
	}
}

func TestParseHexKeyring(t *testing.T) {
	t.Parallel()
	const secretHex = "deadbeefcafebabe0123456789abcdef"
	tests := []struct {
		name  string
		keys  []string
		salts []string
		n     int
		ok    bool
	}{
		{"empty", nil, nil, 0, true},
		{"one", []string{docsKey}, []string{docsSalt}, 1, true},
		{"two", []string{docsKey, exampleKey}, []string{docsSalt, exampleSalt}, 2, true},
		{"whitespace", []string{" " + docsKey + "\n"}, []string{"\t" + docsSalt}, 1, true},
		{"uppercase", []string{strings.ToUpper(exampleKey)}, []string{exampleSalt}, 1, true},
		{"empty salt", []string{docsKey}, []string{""}, 1, true},
		{"count mismatch", []string{docsKey}, nil, 0, false},
		{"empty key", []string{""}, []string{docsSalt}, 0, false},
		{"odd key", []string{secretHex + "a"}, []string{docsSalt}, 0, false},
		{"bad key char", []string{secretHex[:30] + "zz"}, []string{docsSalt}, 0, false},
		{"bad salt", []string{docsKey}, []string{secretHex[:30] + "gg"}, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, err := sign.ParseHexKeyring(tc.keys, tc.salts)
			if tc.ok != (err == nil) {
				t.Fatalf("err = %v, want ok=%v", err, tc.ok)
			}
			if err != nil {
				for _, s := range append(slicesConcat(tc.keys, tc.salts), secretHex[:16], "zz", "gg", "'z'", "'g'") {
					if len(strings.TrimSpace(s)) >= 2 && strings.Contains(err.Error(), strings.TrimSpace(s)) {
						t.Fatalf("error %q leaks input %q", err, s)
					}
				}
				return
			}
			if r.Len() != tc.n {
				t.Fatalf("Len = %d, want %d", r.Len(), tc.n)
			}
		})
	}
}

func slicesConcat(a, b []string) []string { return append(append([]string{}, a...), b...) }

func TestRedaction(t *testing.T) {
	t.Parallel()
	const secret, salt = "super-secret-key", "super-secret-salt"
	k := sign.NewKey([]byte(secret), []byte(salt))
	r := sign.NewKeyring(k)
	var logBuf bytes.Buffer
	slog.New(slog.NewJSONHandler(&logBuf, nil)).LogAttrs(t.Context(), slog.LevelInfo, "keys",
		slog.Any("key", k), slog.Any("keyring", r))
	outputs := []string{
		fmt.Sprintf("%v %+v %#v %s %x %q", k, k, k, k, k, k),
		fmt.Sprintf("%v %+v %#v %s %x %q", r, r, r, r, r, r),
		fmt.Sprintf("%v", []sign.Key{k}),
		logBuf.String(),
	}
	for _, out := range outputs {
		for _, leak := range []string{secret, salt, "7375706572", "super"} {
			if strings.Contains(out, leak) {
				t.Errorf("output leaks %q: %s", leak, out)
			}
		}
	}
}

func TestDestroy(t *testing.T) {
	t.Parallel()
	secret := []byte("caller-owned-secret")
	keys := sign.NewKeyring(sign.NewKey(secret, []byte("salt")))
	clear(secret) // caller zeroing its copy must not affect the keyring
	s := signer(t, 32, keys)
	sig := mustSign(t, s, "/p")
	v := imgproxyVerifier(t, sign.ImgproxyOptions{Keys: keys})
	if res, err := v.Verify(sig, "/p"); err != nil || res != sign.Verified {
		t.Fatalf("before Destroy: %v, %v", res, err)
	}
	keys.Destroy()
	// After Destroy the verifier must fail closed, never fall back to
	// Unsigned (a zeroed key would be a known key).
	res, err := v.Verify(sig, "/p")
	if !errors.Is(err, sign.ErrKeysDestroyed) || res != sign.Unsigned {
		t.Fatalf("after Destroy: %v, %v", res, err)
	}
	if _, err := s.Sign("/p"); !errors.Is(err, sign.ErrKeysDestroyed) {
		t.Fatalf("Sign after Destroy: %v", err)
	}
	if _, err := sign.NewImgproxyVerifier(sign.ImgproxyOptions{Keys: keys, Size: 32}); err == nil {
		t.Fatal("NewImgproxyVerifier accepted a destroyed keyring")
	}
	var nilRing *sign.Keyring
	nilRing.Destroy()
	if nilRing.Len() != 0 {
		t.Fatal("nil keyring Len != 0")
	}
}

func TestResultString(t *testing.T) {
	t.Parallel()
	for r, want := range map[sign.Result]string{
		sign.Unsigned: "unsigned", sign.Verified: "verified", sign.Trusted: "trusted", sign.Result(9): "Result(9)",
	} {
		if r.String() != want {
			t.Errorf("%d.String() = %q, want %q", r, r.String(), want)
		}
		if r.Signed() != (r == sign.Verified || r == sign.Trusted) {
			t.Errorf("%v.Signed() wrong", r)
		}
	}
}

func TestSchemeNames(t *testing.T) {
	t.Parallel()
	ip, _ := sign.NewImgproxyScheme(32)
	xi, _ := sign.NewXIScheme(32)
	if ip.Name() != "imgproxy" || xi.Name() != "xi_sig" {
		t.Fatalf("names = %q, %q", ip.Name(), xi.Name())
	}
	v := imgproxyVerifier(t, sign.ImgproxyOptions{Keys: rawKeyring("k", "s")})
	if v.Scheme().Name() != "imgproxy" {
		t.Fatalf("verifier scheme = %q", v.Scheme().Name())
	}
}
