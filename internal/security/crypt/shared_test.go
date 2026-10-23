package crypt

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestSharedVectors checks the encryption vectors of
// testdata/security/vectors.json, which the TypeScript package
// (clients/typescript) checks too.
func TestSharedVectors(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "security", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Encryption []struct{ Name, Key, IV, Plaintext, Token string } `json:"encryption"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Encryption) == 0 {
		t.Fatal("no encryption vectors")
	}
	for _, tc := range v.Encryption {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			c := mustNew(t, tc.Key)
			iv, err := hex.DecodeString(tc.IV)
			if err != nil {
				t.Fatal(err)
			}
			if got := seal(c.blocks[0], iv, []byte(tc.Plaintext)); got != tc.Token {
				t.Errorf("seal = %s, want %s", got, tc.Token)
			}
			got, err := c.Decrypt(tc.Token)
			if err != nil || string(got) != tc.Plaintext {
				t.Errorf("Decrypt = %q, %v; want %q", got, err, tc.Plaintext)
			}
		})
	}
}
