package crypt

import (
	"bytes"
	"crypto/aes"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

const (
	// imgproxyKey is the key in .refs/imgproxy/examples/encrypted_source_url.{go,js}.
	imgproxyKey = "1eb5b0e971ad7f45324c1bb15c947cb207c43152fa5c6c7f35c4f36e0c18e0f1"
	key128      = "000102030405060708090a0b0c0d0e0f"
	key192      = "000102030405060708090a0b0c0d0e0f1011121314151617"
	otherKey    = "ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554433221100"
)

// vectors were produced outside this package by imgproxy's reference
// encoders with the random IV replaced by a fixed one:
//   - aes-256: .refs/imgproxy/examples/encrypted_source_url.go (Go) and
//     encrypted_source_url.js (Node/OpenSSL via bun) both print this token
//     for IV "0123456789abcdef".
//   - aes-128/192: the same JS encoder (crypto.createCipheriv), exercising a
//     block-aligned plaintext (a full padding block) and a short one.
var vectors = []struct {
	name, key, iv, plaintext, encoded string
}{
	{
		name:      "aes256_imgproxy_example",
		key:       imgproxyKey,
		iv:        "0123456789abcdef",
		plaintext: "http://img.example.com/pretty/image.jpg",
		encoded:   "MDEyMzQ1Njc4OWFiY2RlZj206ZwwVKiBydg5ip7OI2oVBFCdcMXXGze1K8CjwiQuFTiJkTDmRJeJccTs0wH06w",
	},
	{
		name:      "aes128_block_aligned",
		key:       key128,
		iv:        "fedcba9876543210",
		plaintext: "https://example.com/abcdefghijkl",
		encoded:   "ZmVkY2JhOTg3NjU0MzIxMCx6VmHChBajo8LAQxnT97Rexo6Vxh_GWH1Fa7gST9LDB4olaWLqjxQvsrisG5xtfg",
	},
	{
		name:      "aes192_short",
		key:       key192,
		iv:        "ivivivivivivivi!",
		plaintext: "s3://bucket/key.png",
		encoded:   "aXZpdml2aXZpdml2aXZpIWPwY9qCmnnmLnNVeCIjMP1UiOg7cNRyAGHg19Rprjpx",
	},
}

func mustNew(tb testing.TB, keys ...string) *Cipher {
	tb.Helper()
	c, err := New(keys...)
	if err != nil {
		tb.Fatalf("New: %v", err)
	}
	return c
}

func TestReferenceVectors(t *testing.T) {
	t.Parallel()
	for _, v := range vectors {
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()
			c := mustNew(t, v.key)

			got, err := c.Decrypt(v.encoded)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if string(got) != v.plaintext {
				t.Errorf("Decrypt = %q, want %q", got, v.plaintext)
			}
			// Padded base64 is tolerated.
			padded := v.encoded + strings.Repeat("=", (4-len(v.encoded)%4)%4)
			if got, err := c.Decrypt(padded); err != nil || string(got) != v.plaintext {
				t.Errorf("Decrypt(padded) = %q, %v", got, err)
			}
			if tok := seal(c.blocks[0], []byte(v.iv), []byte(v.plaintext)); tok != v.encoded {
				t.Errorf("seal = %s, want %s", tok, v.encoded)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	plaintexts := []string{
		"",
		"a",
		"http://img.example.com/pretty/image.jpg",
		"https://example.com/abcdefghijkl", // exactly two blocks
		"https://例え.jp/画像.png?x=1&y=ü",
		strings.Repeat("x", 4096),
	}
	for _, key := range []string{key128, key192, imgproxyKey} {
		c := mustNew(t, key)
		for _, p := range plaintexts {
			t.Run(fmt.Sprintf("%d/%d", len(key)/2, len(p)), func(t *testing.T) {
				t.Parallel()
				tok, err := c.Encrypt([]byte(p))
				if err != nil {
					t.Fatalf("Encrypt: %v", err)
				}
				if strings.ContainsAny(tok, "=+/") {
					t.Errorf("token %q is not unpadded base64url", tok)
				}
				got, err := c.Decrypt(tok)
				if err != nil {
					t.Fatalf("Decrypt: %v", err)
				}
				if string(got) != p {
					t.Errorf("round trip = %q, want %q", got, p)
				}
			})
		}
	}
}

func TestEncryptUsesRandomIV(t *testing.T) {
	t.Parallel()
	c := mustNew(t, imgproxyKey)
	a, err := c.Encrypt([]byte("http://a/b.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Encrypt([]byte("http://a/b.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Errorf("two encryptions produced the same token %q", a)
	}
}

func TestKeyRotation(t *testing.T) {
	t.Parallel()
	oldC := mustNew(t, imgproxyKey)
	rotated := mustNew(t, otherKey, imgproxyKey) // new primary, old still accepted

	oldTok, err := oldC.Encrypt([]byte("http://old/token.png"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := rotated.Decrypt(oldTok); err != nil || string(got) != "http://old/token.png" {
		t.Errorf("rotated.Decrypt(old token) = %q, %v", got, err)
	}
	newTok, err := rotated.Encrypt([]byte("http://new/token.png"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oldC.Decrypt(newTok); err == nil {
		t.Error("old cipher decrypted a token made with the new primary key")
	}
	// Every reference vector still decrypts when its key is not first.
	for _, v := range vectors {
		c := mustNew(t, otherKey, key128, key192, imgproxyKey)
		if got, err := c.Decrypt(v.encoded); err != nil || string(got) != v.plaintext {
			t.Errorf("%s: multi-key Decrypt = %q, %v", v.name, got, err)
		}
	}
}

// TestRotationManyTokens checks that a wrong first key never yields garbage:
// with padding alone about 1 in 256 tokens would decrypt under the wrong key.
func TestRotationManyTokens(t *testing.T) {
	t.Parallel()
	primary := mustNew(t, imgproxyKey)
	rotated := mustNew(t, otherKey, imgproxyKey)
	for i := range 4000 {
		p := fmt.Sprintf("https://cdn.example.com/%d.jpg", i)
		tok, err := primary.Encrypt([]byte(p))
		if err != nil {
			t.Fatal(err)
		}
		got, err := rotated.Decrypt(tok)
		if err != nil || string(got) != p {
			t.Fatalf("token %d: Decrypt = %q, %v", i, got, err)
		}
	}
}

func TestDecryptFailures(t *testing.T) {
	t.Parallel()
	c := mustNew(t, imgproxyKey)
	good := vectors[0].encoded
	raw, err := base64.RawURLEncoding.DecodeString(good)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString

	// Flip a bit in the second-to-last block: this corrupts the last
	// block's padding without touching the IV.
	badPad := bytes.Clone(raw)
	badPad[len(badPad)-aes.BlockSize-1] ^= 0x80

	// A structurally valid token encrypting bytes with invalid padding.
	var zeroBlock [aes.BlockSize]byte
	badPadding := bytes.Clone(raw[:2*aes.BlockSize])
	c.blocks[0].Encrypt(badPadding[aes.BlockSize:], zeroBlock[:]) // decrypts to the IV, whose last byte is not a valid pad

	// A valid pad whose plaintext holds a control character.
	ctrl := seal(c.blocks[0], raw[:aes.BlockSize], []byte("http://a/\x00.jpg"))

	tests := []struct {
		name, token string
		cipher      *Cipher
	}{
		{"empty", "", c},
		{"not_base64", "!!!!", c},
		{"std_alphabet", strings.ReplaceAll(strings.ReplaceAll(good, "-", "+"), "_", "/") + "+/", c},
		{"iv_only", enc(raw[:aes.BlockSize]), c},
		{"truncated_block", good[:len(good)-4], c},
		{"not_block_multiple", enc(raw[:len(raw)-1]), c},
		{"truncated_to_iv_plus_partial", enc(raw[:aes.BlockSize+5]), c},
		{"bit_flip", enc(badPad), c},
		{"invalid_padding", enc(badPadding), c},
		{"control_char_plaintext", ctrl, c},
		{"wrong_key", good, mustNew(t, otherKey)},
		{"wrong_key_size_128", good, mustNew(t, key128)},
		{"too_long", strings.Repeat("A", MaxTokenLen+1), c},
		{"nil_cipher", good, nil},
		{"zero_cipher", good, &Cipher{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := tc.cipher.Decrypt(tc.token)
			if err == nil {
				t.Fatalf("Decrypt = %q, want error", got)
			}
			checkUnauthorized(t, err)
		})
	}
}

func checkUnauthorized(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, errs.Match(errs.KindUnauthorized)) {
		t.Errorf("kind = %v, want unauthorized", errs.KindOf(err))
	}
	if !errors.Is(err, ErrInvalidToken) {
		t.Errorf("error %v does not wrap ErrInvalidToken", err)
	}
	e, ok := errors.AsType[*errs.Error](err)
	if !ok {
		t.Fatalf("no *errs.Error in %v", err)
	}
	if e.HTTPStatus() != 403 || e.PublicMessage() != publicMessage {
		t.Errorf("public = (%d, %q), want (403, %q)", e.HTTPStatus(), e.PublicMessage(), publicMessage)
	}
}

func TestUnpad(t *testing.T) {
	t.Parallel()
	block := func(fill byte, tail ...byte) []byte {
		b := bytes.Repeat([]byte{fill}, aes.BlockSize)
		copy(b[aes.BlockSize-len(tail):], tail)
		return b
	}
	tests := []struct {
		name  string
		in    []byte
		wantN int
		ok    bool
	}{
		{"pad1", block('a', 1), 15, true},
		{"pad2", block('a', 2, 2), 14, true},
		{"pad16", bytes.Repeat([]byte{16}, 16), 0, true},
		{"pad16_two_blocks", append(block('a'), bytes.Repeat([]byte{16}, 16)...), 16, true},
		{"pad0", block('a', 0), 0, false},
		{"pad17", bytes.Repeat([]byte{17}, 16), 0, false},
		{"pad255", block('a', 255), 0, false},
		{"pad2_mismatch", block('a', 3, 2), 0, false},
		{"pad3_mismatch_far", block('a', 1, 3, 3), 0, false},
		{"pad16_mismatch_first", append([]byte{15}, bytes.Repeat([]byte{16}, 15)...), 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			n, ok := unpad(tc.in)
			if n != tc.wantN || ok != tc.ok {
				t.Errorf("unpad = (%d, %v), want (%d, %v)", n, ok, tc.wantN, tc.ok)
			}
		})
	}
}

func TestNewErrors(t *testing.T) {
	t.Parallel()
	const notHexKey = "zz5b0e971ad7f45324c1bb15c947cb20"
	tests := []struct {
		name string
		keys []string
		want error
	}{
		{"none", nil, ErrNoKeys},
		{"not_hex", []string{notHexKey}, ErrInvalidKey},
		{"odd_hex", []string{"abc"}, ErrInvalidKey},
		{"short", []string{"00112233"}, ErrInvalidKey},
		{"33_bytes", []string{imgproxyKey + "00"}, ErrInvalidKey},
		{"second_bad", []string{imgproxyKey, "00"}, ErrInvalidKey},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, err := New(tc.keys...)
			if c != nil || !errors.Is(err, tc.want) {
				t.Fatalf("New = (%v, %v), want error %v", c, err, tc.want)
			}
			for _, k := range tc.keys {
				if len(k) > 4 && strings.Contains(err.Error(), k[:4]) {
					t.Errorf("error %q leaks key material", err)
				}
			}
		})
	}
	if c, err := New(" " + key128 + "\n"); err != nil || c == nil {
		t.Errorf("New with surrounding whitespace = (%v, %v)", c, err)
	}
}

func TestEncryptErrors(t *testing.T) {
	t.Parallel()
	c := mustNew(t, key128)
	for _, p := range []string{"a\x00b", "tab\there", "del\x7f", "\xff\xfe"} {
		if _, err := c.Encrypt([]byte(p)); !errors.Is(err, ErrInvalidPlaintext) {
			t.Errorf("Encrypt(%q) error = %v, want ErrInvalidPlaintext", p, err)
		}
	}
	var nilC *Cipher
	if _, err := nilC.Encrypt([]byte("x")); !errors.Is(err, ErrNoKeys) {
		t.Errorf("nil Encrypt error = %v, want ErrNoKeys", err)
	}
}

func TestNoKeyMaterialInOutput(t *testing.T) {
	t.Parallel()
	c := mustNew(t, imgproxyKey, key128)
	var logBuf bytes.Buffer
	slog.New(slog.NewJSONHandler(&logBuf, nil)).Info("cipher", slog.Any("crypt", c))
	outputs := []string{
		fmt.Sprintf("%v", c),
		fmt.Sprintf("%+v", c),
		fmt.Sprintf("%#v", c),
		fmt.Sprint(c),
		logBuf.String(),
	}
	for _, out := range outputs {
		if !strings.Contains(out, "2") {
			t.Errorf("output %q does not report the key count", out)
		}
		for _, k := range []string{imgproxyKey, key128} {
			if strings.Contains(out, k[:8]) {
				t.Errorf("output %q leaks key material", out)
			}
		}
		if strings.Contains(out, "enc") || strings.Contains(out, "rounds") {
			t.Errorf("output %q exposes the key schedule", out)
		}
	}
}
