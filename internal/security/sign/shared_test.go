package sign_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

// sharedVectors is testdata/security/vectors.json at the repository root:
// the signing vectors the TypeScript package (clients/typescript) checks
// too, so both implementations agree byte for byte.
type sharedVectors struct {
	Imgproxy []struct {
		Name, Key, Salt, Path, Signature string
		Size                             int
	} `json:"imgproxy"`
	XISig []struct {
		Name, Key, Salt, Path, Query string
		SignedQuery                  string `json:"signed_query"`
		Size                         int
	} `json:"xi_sig"`
}

func loadSharedVectors(t *testing.T) sharedVectors {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "security", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v sharedVectors
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Imgproxy) == 0 || len(v.XISig) == 0 {
		t.Fatal("no shared vectors")
	}
	return v
}

func TestSharedVectors(t *testing.T) {
	t.Parallel()
	v := loadSharedVectors(t)
	for _, tc := range v.Imgproxy {
		t.Run("imgproxy/"+tc.Name, func(t *testing.T) {
			t.Parallel()
			keys := hexKeyring(t, tc.Key, tc.Salt)
			if got := mustSign(t, signer(t, tc.Size, keys), tc.Path); got != tc.Signature {
				t.Errorf("Sign = %q, want %q", got, tc.Signature)
			}
			res, err := imgproxyVerifier(t, sign.ImgproxyOptions{Keys: keys, Size: tc.Size}).Verify(tc.Signature, tc.Path)
			if err != nil || res != sign.Verified {
				t.Errorf("Verify = %v, %v", res, err)
			}
		})
	}
	for _, tc := range v.XISig {
		t.Run("xi_sig/"+tc.Name, func(t *testing.T) {
			t.Parallel()
			keys := hexKeyring(t, tc.Key, tc.Salt)
			scheme, err := sign.NewXIScheme(tc.Size)
			if err != nil {
				t.Fatal(err)
			}
			s, err := sign.NewSigner(scheme, keys)
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.SignXIQuery(tc.Path, tc.Query)
			if err != nil || got != tc.SignedQuery {
				t.Fatalf("SignXIQuery = %q, %v; want %q", got, err, tc.SignedQuery)
			}
			ver, err := sign.NewXIVerifier(sign.XIOptions{Keys: keys, Size: tc.Size, Required: true})
			if err != nil {
				t.Fatal(err)
			}
			sig, msg, err := sign.XIMessage(tc.Path, tc.SignedQuery)
			if err != nil {
				t.Fatal(err)
			}
			if res, err := ver.Verify(sig, msg); err != nil || res != sign.Verified {
				t.Errorf("Verify = %v, %v", res, err)
			}
		})
	}
}
