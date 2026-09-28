package crypt

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// TestSharedPasswordVectors checks the pdf_password vectors of
// testdata/security/vectors.json (WP-115): the token is the enc/ format
// over the password, which the TypeScript package's encryptPdfPassword
// produces too, and the digest pins PasswordDigest.
func TestSharedPasswordVectors(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "security", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		PDFPassword []struct{ Name, Key, IV, Password, Token, Digest string } `json:"pdf_password"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.PDFPassword) < 3 {
		t.Fatalf("%d pdf_password vectors", len(v.PDFPassword))
	}
	for _, tc := range v.PDFPassword {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			c := mustNew(t, tc.Key)
			iv, err := hex.DecodeString(tc.IV)
			if err != nil {
				t.Fatal(err)
			}
			if got := seal(c.blocks[0], iv, []byte(tc.Password)); got != tc.Token {
				t.Errorf("seal = %s, want %s", got, tc.Token)
			}
			got, err := c.DecryptPassword(tc.Token)
			if err != nil || string(got) != tc.Password {
				t.Errorf("DecryptPassword = %q, %v; want %q", got, err, tc.Password)
			}
			if d := c.PasswordDigest([]byte(tc.Password)); d != tc.Digest {
				t.Errorf("PasswordDigest = %s, want %s", d, tc.Digest)
			}
		})
	}
}

func TestDecryptPasswordErrors(t *testing.T) {
	t.Parallel()
	c := mustNew(t, imgproxyKey)
	tok, err := c.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", "!!", "AAAA", strings.Repeat("A", 43), tok[:len(tok)-1], strings.Repeat("A", MaxTokenLen+1)} {
		got, err := c.DecryptPassword(token)
		e, ok := errors.AsType[*errs.Error](err)
		if got != nil || !ok || e.Kind != errs.KindUnauthorized || !errors.Is(err, ErrInvalidToken) ||
			e.PublicMessage() != passwordPublicMessage {
			t.Errorf("DecryptPassword(%.20q) = %q, %v", token, got, err)
		}
		if err != nil && strings.Contains(err.Error(), token) && token != "" {
			t.Errorf("error %q echoes the token", err)
		}
	}
	// Decrypt keeps its own message.
	if _, err := c.Decrypt("AAAA"); publicOf(err) != publicMessage {
		t.Errorf("Decrypt public message %q", publicOf(err))
	}
}

func TestPasswordDigest(t *testing.T) {
	t.Parallel()
	a := mustNew(t, imgproxyKey, key128)
	b := mustNew(t, imgproxyKey)
	rotated := mustNew(t, key128, imgproxyKey)
	pw := []byte("secret")
	if a.PasswordDigest(pw) != b.PasswordDigest(pw) {
		t.Error("the digest depends on keys other than the first")
	}
	if a.PasswordDigest(pw) == rotated.PasswordDigest(pw) {
		t.Error("the digest ignores the first key")
	}
	if a.PasswordDigest(pw) == a.PasswordDigest([]byte("secreT")) {
		t.Error("the digest ignores the password")
	}
	var nilCipher *Cipher
	if nilCipher.PasswordDigest(pw) != "" {
		t.Error("nil cipher digest")
	}
}

func publicOf(err error) string {
	if e, ok := errors.AsType[*errs.Error](err); ok {
		return e.PublicMessage()
	}
	return ""
}
