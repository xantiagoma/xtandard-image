package xtandard_test

// testdata/security/vectors.json: the signing, enc/ and pdf_password vectors
// shared with the server and the TypeScript and Python clients.

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	xtandard "github.com/xantiagoma/xtandard-image/clients/go"
)

type vectors struct {
	Imgproxy []struct {
		Name, Key, Salt, Path, Signature string
		Size                             int
	} `json:"imgproxy"`
	XiSig []struct {
		Name, Key, Salt, Path, Query string
		SignedQuery                  string `json:"signed_query"`
		Size                         int
	} `json:"xi_sig"`
	Encryption []struct {
		Name, Key, IV, Plaintext, Token string
	} `json:"encryption"`
	PDFPassword []struct {
		Name, Key, IV, Password, Token string
	} `json:"pdf_password"`
}

// decrypt opens a token with the stdlib, independently of the client.
func decrypt(t *testing.T, token, key string) string {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	k, _ := hex.DecodeString(key)
	block, err := aes.NewCipher(k)
	if err != nil {
		t.Fatal(err)
	}
	data := raw[aes.BlockSize:]
	cipher.NewCBCDecrypter(block, raw[:aes.BlockSize]).CryptBlocks(data, data)
	pad := int(data[len(data)-1])
	return string(data[:len(data)-pad])
}

func TestVectors(t *testing.T) {
	t.Parallel()
	v := loadJSON[vectors](t, "testdata/security/vectors.json")
	for _, c := range v.Imgproxy {
		t.Run("imgproxy/"+c.Name, func(t *testing.T) {
			t.Parallel()
			k, err := xtandard.ParseSigningKey(c.Key, c.Salt, c.Size)
			if err != nil {
				t.Fatal(err)
			}
			if got := xtandard.Sign(c.Path, k); got != c.Signature {
				t.Errorf("Sign = %s, want %s", got, c.Signature)
			}
			p := c.Path
			if !strings.HasPrefix(p, "/") {
				p = "/" + p
			}
			if got, want := xtandard.SignImgproxyPath(c.Path, k), "/"+c.Signature+p; got != want {
				t.Errorf("SignImgproxyPath = %s, want %s", got, want)
			}
			key, _ := hex.DecodeString(c.Key)
			salt, _ := hex.DecodeString(c.Salt)
			raw, err := xtandard.NewSigningKey(key, salt, c.Size)
			if err != nil {
				t.Fatal(err)
			}
			if got := xtandard.Sign(c.Path, raw); got != c.Signature {
				t.Errorf("Sign (raw key) = %s, want %s", got, c.Signature)
			}
		})
	}
	for _, c := range v.XiSig {
		t.Run("xi_sig/"+c.Name, func(t *testing.T) {
			t.Parallel()
			k, err := xtandard.ParseSigningKey(c.Key, c.Salt, c.Size)
			if err != nil {
				t.Fatal(err)
			}
			for _, q := range []string{c.Query, c.SignedQuery} {
				got, err := xtandard.SignXiQuery(c.Path, q, k)
				if err != nil || got != c.SignedQuery {
					t.Errorf("SignXiQuery(%q) = %s, %v; want %s", q, got, err, c.SignedQuery)
				}
			}
			got, err := xtandard.SignXiURL("https://img.example.com"+c.Path+"?"+c.Query, k, "")
			if want := "https://img.example.com" + c.Path + "?" + c.SignedQuery; err != nil || got != want {
				t.Errorf("SignXiURL = %s, %v; want %s", got, err, want)
			}
		})
	}
	for _, c := range v.Encryption {
		t.Run("encryption/"+c.Name, func(t *testing.T) {
			t.Parallel()
			k, err := xtandard.ParseEncryptionKey(c.Key, c.IV)
			if err != nil {
				t.Fatal(err)
			}
			got, err := xtandard.EncryptSource(c.Plaintext, k)
			if err != nil || got != c.Token {
				t.Fatalf("EncryptSource = %s, %v; want %s", got, err, c.Token)
			}
			if p := decrypt(t, got, c.Key); p != c.Plaintext {
				t.Errorf("decrypted %q, want %q", p, c.Plaintext)
			}
		})
	}
	for _, c := range v.PDFPassword {
		t.Run("pdf_password/"+c.Name, func(t *testing.T) {
			t.Parallel()
			k, err := xtandard.ParseEncryptionKey(c.Key, c.IV)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := xtandard.EncryptPDFPassword(c.Password, k); err != nil || got != c.Token {
				t.Errorf("EncryptPDFPassword = %s, %v; want %s", got, err, c.Token)
			}
		})
	}
}

func TestRandomIV(t *testing.T) {
	t.Parallel()
	const key = "1eb5b0e971ad7f45324c1bb15c947cb207c43152fa5c6c7f35c4f36e0c18e0f1"
	k, err := xtandard.ParseEncryptionKey(key, "")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := xtandard.EncryptPDFPassword("s3cret", k)
	b, _ := xtandard.EncryptPDFPassword("s3cret", k)
	if a == b {
		t.Error("two tokens with a random IV are equal")
	}
	if p := decrypt(t, a, key); p != "s3cret" {
		t.Errorf("decrypted %q", p)
	}
}

func TestXiMessage(t *testing.T) {
	t.Parallel()
	msg, sig, err := xtandard.XiMessage("/", "url=x&&xi_sig=s&w=1")
	if err != nil || msg != "/?url=x&w=1" || sig != "s" {
		t.Errorf("XiMessage = %q, %q, %v", msg, sig, err)
	}
	if msg, sig, err := xtandard.XiMessage("/", ""); err != nil || msg != "/" || sig != "" {
		t.Errorf("XiMessage(empty) = %q, %q, %v", msg, sig, err)
	}
	_, _, err = xtandard.XiMessage("/", "xi_sig=a&xi_sig=b")
	wantCode(t, err, "invalid_request")
}

func TestKeysHideSecrets(t *testing.T) {
	t.Parallel()
	sk, err := xtandard.ParseSigningKey(strings.Repeat("ab", 32), strings.Repeat("cd", 32), 32)
	if err != nil {
		t.Fatal(err)
	}
	ek, err := xtandard.ParseEncryptionKey(strings.Repeat("ef", 32), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{fmt.Sprint(sk), fmt.Sprintf("%+v %#v", sk, sk), fmt.Sprint(ek), fmt.Sprintf("%#v", ek), fmt.Sprintf("%v", &sk)} {
		for _, secret := range []string{"ab", "cd", "ef", "171", "205", "239"} {
			if strings.Contains(s, secret) {
				t.Errorf("%q shows key material %q", s, secret)
			}
		}
	}
}
