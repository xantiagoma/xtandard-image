package crypt

import (
	"bytes"
	"errors"
	"testing"
	"unicode/utf8"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// FuzzDecrypt feeds attacker-controlled tokens to a multi-key Cipher. Decrypt
// must never panic, must fail only with KindUnauthorized, and anything it
// accepts must be a plausible URL that re-encrypts and decrypts to itself.
// Seeds: f.Add below plus testdata/fuzz/FuzzDecrypt.
func FuzzDecrypt(f *testing.F) {
	for _, v := range vectors {
		f.Add(v.encoded)
	}
	f.Add("")
	f.Add("=")
	f.Add("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	f.Add("MDEyMzQ1Njc4OWFiY2RlZj206ZwwVKiBydg5ip7OI2oVBFCdcMXXGze1K8CjwiQuFTiJkTDmRJeJccTs0wH06w==")
	f.Add("MDEyMzQ1Njc4OWFiY2RlZj206ZwwVKiBydg5ip7OI2oVBFCdcMXXGze1K8CjwiQuFTiJkTDmRJeJccTs0wH06")

	c, err := New(otherKey, key128, key192, imgproxyKey)
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, token string) {
		got, err := c.Decrypt(token)
		if err != nil {
			if got != nil {
				t.Fatalf("Decrypt returned %q with error %v", got, err)
			}
			if !errors.Is(err, errs.Match(errs.KindUnauthorized)) || !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("Decrypt error %v is not an unauthorized ErrInvalidToken", err)
			}
			return
		}
		if !utf8.Valid(got) {
			t.Fatalf("Decrypt accepted invalid UTF-8 %q", got)
		}
		tok, err := c.Encrypt(got)
		if err != nil {
			t.Fatalf("Encrypt(%q) after Decrypt: %v", got, err)
		}
		again, err := c.Decrypt(tok)
		if err != nil || !bytes.Equal(again, got) {
			t.Fatalf("re-encrypt round trip = %q, %v; want %q", again, err, got)
		}
	})
}

// FuzzRoundTrip checks Encrypt/Decrypt symmetry for arbitrary plaintexts:
// whatever Encrypt accepts, Decrypt returns unchanged.
func FuzzRoundTrip(f *testing.F) {
	for _, v := range vectors {
		f.Add(v.plaintext)
	}
	f.Add("")
	f.Add("\x00")
	f.Add("https://例え.jp/画像.png")

	c, err := New(key128, imgproxyKey)
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, p string) {
		tok, err := c.Encrypt([]byte(p))
		if err != nil {
			if !errors.Is(err, ErrInvalidPlaintext) || plausible([]byte(p)) {
				t.Fatalf("Encrypt(%q): %v", p, err)
			}
			return
		}
		got, err := c.Decrypt(tok)
		if err != nil || string(got) != p {
			t.Fatalf("round trip = %q, %v; want %q", got, err, p)
		}
	})
}
