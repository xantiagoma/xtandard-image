package sign_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

// FuzzImgproxyVerify feeds attacker-controlled signature segments and paths
// to the imgproxy verifier. It must never panic, only accept signatures
// that decode to the MAC (or are trusted), and always accept its own.
func FuzzImgproxyVerify(f *testing.F) {
	for _, s := range [][2]string{
		{docsSig, docsPath},
		{exampleSig, examplePath},
		{exampleSig8, examplePath},
		{"insecure", "/rs:fit:1:1/plain/x"},
		{"_", ""},
		{"rs:fill:1:1", "/x"},
		{"trusted-sig", "/x"},
		{"====", "/"},
		{strings.Repeat("A", 300), "/x"},
		{"oWaL7QoW5Ts", "asd"},
		{"\x00\xff", "\x00"},
	} {
		f.Add(s[0], s[1])
	}
	keys := hexKeyring(f, exampleKey, exampleSalt, docsKey, docsSalt)
	v := imgproxyVerifier(f, sign.ImgproxyOptions{Keys: keys, Trusted: []string{"trusted-sig"}})
	off := imgproxyVerifier(f, sign.ImgproxyOptions{})
	s := signer(f, 32, keys)
	f.Fuzz(func(t *testing.T, sig, path string) {
		res, err := v.Verify(sig, path)
		switch {
		case err != nil:
			if res != sign.Unsigned || errs.KindOf(err) != errs.KindUnauthorized {
				t.Fatalf("Verify(%q, %q) = %v, %v", sig, path, res, err)
			}
		case res == sign.Trusted:
			if sig != "trusted-sig" {
				t.Fatalf("Verify(%q) trusted", sig)
			}
		case res == sign.Verified:
			got, derr := base64.RawURLEncoding.DecodeString(sig)
			want, _ := base64.RawURLEncoding.DecodeString(mustSign(t, s, path))
			other, _ := base64.RawURLEncoding.DecodeString(
				mustSign(t, signer(t, 32, hexKeyring(t, docsKey, docsSalt)), path))
			if derr != nil || (!bytes.Equal(got, want) && !bytes.Equal(got, other)) {
				t.Fatalf("Verify(%q, %q) accepted a signature that is not the MAC", sig, path)
			}
		default: // Unsigned without error: only placeholders.
			if sig != "" && sig != "insecure" && sig != "unsafe" && sig != "_" {
				t.Fatalf("Verify(%q) unsigned without error", sig)
			}
		}
		if res, err := v.Verify(mustSign(t, s, path), path); err != nil || res != sign.Verified {
			t.Fatalf("own signature for %q rejected: %v, %v", path, res, err)
		}
		res, err = off.Verify(sig, path)
		if (err != nil) != strings.Contains(sig, ":") || res != sign.Unsigned {
			t.Fatalf("signing off: Verify(%q) = %v, %v", sig, res, err)
		}
	})
}

// FuzzParseHexKeyring checks key parsing never panics and never echoes key
// material in errors.
func FuzzParseHexKeyring(f *testing.F) {
	for _, s := range [][2]string{
		{docsKey, docsSalt},
		{exampleKey + "," + docsKey, exampleSalt + "," + docsSalt},
		{"", ""},
		{"abc", "00"},
		{"zz", "00"},
		{"00", "zz"},
		{" 0a0b ", "\t"},
		{"a,b,c", "1"},
		{"deadbeefdeadbeefdeadbeefdeadbeeg", "00"},
	} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, keyList, saltList string) {
		keys, salts := strings.Split(keyList, ","), strings.Split(saltList, ",")
		r, err := sign.ParseHexKeyring(keys, salts)
		if err != nil {
			if r != nil {
				t.Fatal("non-nil keyring with error")
			}
			for _, in := range append(keys, salts...) {
				in = strings.TrimSpace(in)
				if len(in) >= 8 && isHexish(in) && strings.Contains(err.Error(), in) {
					t.Fatalf("error %q leaks %q", err, in)
				}
			}
			return
		}
		if r.Len() != len(keys) {
			t.Fatalf("Len = %d, want %d", r.Len(), len(keys))
		}
		s := signer(t, 32, r)
		v := imgproxyVerifier(t, sign.ImgproxyOptions{Keys: r})
		if res, err := v.Verify(mustSign(t, s, "/p"), "/p"); err != nil || res != sign.Verified {
			t.Fatalf("round trip: %v, %v", res, err)
		}
		r.Destroy()
		if _, err := v.Verify("x", "/p"); !errors.Is(err, sign.ErrKeysDestroyed) {
			t.Fatalf("after Destroy: %v", err)
		}
	})
}

func isHexish(s string) bool {
	return strings.Trim(s, "0123456789abcdefABCDEFghijklmnopqrstuvwxyzGHIJKLMNOPQRSTUVWXYZ") == ""
}

// FuzzXIMessage checks the xi_sig query canonicalisation never panics, never
// keeps an xi_sig parameter in the message, and round-trips with Signer.
func FuzzXIMessage(f *testing.F) {
	for _, s := range [][2]string{
		{"/a.jpg", ""},
		{"/a.jpg", "w=1&xi_sig=abc"},
		{"/a", "xi_sig=a&xi_sig=b"},
		{"/a", "&&&"},
		{"", "xi_sig"},
		{"/a", "a=%zz&b==c"},
		{"/a?b", "c=d"},
	} {
		f.Add(s[0], s[1])
	}
	keys := hexKeyring(f, exampleKey, exampleSalt)
	scheme, err := sign.NewXIScheme(32)
	if err != nil {
		f.Fatal(err)
	}
	s, err := sign.NewSigner(scheme, keys)
	if err != nil {
		f.Fatal(err)
	}
	v, err := sign.NewXIVerifier(sign.XIOptions{Keys: keys, Size: 32})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, path, query string) {
		_, msg, err := sign.XIMessage(path, query)
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
				if k, _, _ := strings.Cut(p, "="); k == sign.XIParam || p == "" {
					t.Fatalf("msg %q keeps %q", msg, p)
				}
			}
		}
		signed, err := s.SignXIQuery(path, query)
		if err != nil {
			t.Fatal(err)
		}
		sig, msg2, err := sign.XIMessage(path, signed)
		if err != nil || msg2 != msg {
			t.Fatalf("re-parse of %q: msg %q (want %q), %v", signed, msg2, msg, err)
		}
		if res, err := v.Verify(sig, msg2); err != nil || res != sign.Verified {
			t.Fatalf("round trip %q: %v, %v", signed, res, err)
		}
	})
}
