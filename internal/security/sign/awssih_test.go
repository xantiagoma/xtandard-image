package sign_test

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

// awssihVector is one entry of testdata/awssih/vectors.json, produced by
// generate.ts from SIH's own validateRequestSignature code.
type awssihVector struct {
	Name      string `json:"name"`
	Secret    string `json:"secret"`
	Path      string `json:"path"`
	Query     string `json:"query"`
	Message   string `json:"message"`
	Signature string `json:"signature"`
}

func loadAWSSIHVectors(t *testing.T) []awssihVector {
	t.Helper()
	b, err := os.ReadFile("testdata/awssih/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vs []awssihVector
	if err := json.Unmarshal(b, &vs); err != nil {
		t.Fatal(err)
	}
	if len(vs) == 0 {
		t.Fatal("no vectors")
	}
	return vs
}

func awssihKeys(secrets ...string) *sign.Keyring {
	keys := make([]sign.Key, 0, len(secrets))
	for _, s := range secrets {
		keys = append(keys, sign.NewKey([]byte(s), nil))
	}
	return sign.NewKeyring(keys...)
}

func TestAWSSIHVectors(t *testing.T) {
	t.Parallel()
	for _, v := range loadAWSSIHVectors(t) {
		t.Run(v.Name, func(t *testing.T) {
			t.Parallel()
			_, msg, err := sign.AWSSIHMessage(v.Path, v.Query)
			if err != nil {
				t.Fatal(err)
			}
			if msg != v.Message {
				t.Errorf("message = %q, want %q", msg, v.Message)
			}
			signer, err := sign.NewSigner(sign.NewAWSSIHScheme(), awssihKeys(v.Secret))
			if err != nil {
				t.Fatal(err)
			}
			got, err := signer.Sign(msg)
			if err != nil {
				t.Fatal(err)
			}
			if got != v.Signature {
				t.Errorf("signature = %s, want %s", got, v.Signature)
			}
			ver, err := sign.NewAWSSIHVerifier(sign.AWSSIHOptions{Keys: awssihKeys("other", v.Secret), Required: true})
			if err != nil {
				t.Fatal(err)
			}
			if res, err := ver.Verify(v.Signature, msg); err != nil || res != sign.Verified {
				t.Errorf("Verify = %v, %v", res, err)
			}
			q, err := signer.SignAWSSIHQuery(v.Path, v.Query)
			if err != nil {
				t.Fatal(err)
			}
			sig, msg2, err := sign.AWSSIHMessage(v.Path, q)
			if err != nil || sig != v.Signature || msg2 != msg {
				t.Errorf("SignAWSSIHQuery round trip: %q → sig %q msg %q err %v", q, sig, msg2, err)
			}
		})
	}
}

func TestAWSSIHMessage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, path, query string
		sig, msg          string
		bad               bool
	}{
		{"no query", "/abc", "", "", "/abc", false},
		{"only signature", "/abc", "signature=deadbeef", "deadbeef", "/abc", false},
		{"last signature wins", "/abc", "signature=a&signature=b", "b", "/abc", false},
		{"empty segments", "/abc", "&&b=2&&a=1&", "", "/abc?a=1&b=2", false},
		{"value with comma sorts by key,value", "/p", "a=2,x&a,=1", "", "/p?a,=1&a=2,x", false},
		{"escaped signature key", "/p", "sig%6Eature=x&w=1", "x", "/p?w=1", false},
		{"bad escape", "/p", "a=%zz", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sig, msg, err := sign.AWSSIHMessage(tc.path, tc.query)
			if tc.bad {
				if errs.KindOf(err) != errs.KindInvalidURL {
					t.Fatalf("err = %v, want invalid url", err)
				}
				return
			}
			if err != nil || sig != tc.sig || msg != tc.msg {
				t.Errorf("= %q, %q, %v; want %q, %q", sig, msg, err, tc.sig, tc.msg)
			}
		})
	}
}

func TestAWSSIHVerify(t *testing.T) {
	t.Parallel()
	vs := loadAWSSIHVectors(t)
	v := vs[0]
	req, err := sign.NewAWSSIHVerifier(sign.AWSSIHOptions{Keys: awssihKeys(v.Secret), Required: true, Trusted: []string{"trusted"}})
	if err != nil {
		t.Fatal(err)
	}
	opt, err := sign.NewAWSSIHVerifier(sign.AWSSIHOptions{Keys: awssihKeys(v.Secret)})
	if err != nil {
		t.Fatal(err)
	}
	off, err := sign.NewAWSSIHVerifier(sign.AWSSIHOptions{})
	if err != nil {
		t.Fatal(err)
	}
	last := v.Signature[len(v.Signature)-1]
	flipped := v.Signature[:len(v.Signature)-1] + string("0123456789abcdef"[(strings.IndexByte("0123456789abcdef", last)+1)%16])
	tests := []struct {
		name string
		v    *sign.KeyedVerifier
		sig  string
		want sign.Result
		err  error
	}{
		{"valid", req, v.Signature, sign.Verified, nil},
		{"uppercase hex rejected like SIH", req, strings.ToUpper(v.Signature), sign.Unsigned, sign.ErrMalformedSignature},
		{"last char differs", req, flipped, sign.Unsigned, sign.ErrSignatureMismatch},
		{"short", req, "invalid", sign.Unsigned, sign.ErrMalformedSignature},
		{"missing required", req, "", sign.Unsigned, sign.ErrMissingSignature},
		{"trusted", req, "trusted", sign.Trusted, nil},
		{"missing optional", opt, "", sign.Unsigned, nil},
		{"wrong optional", opt, flipped, sign.Unsigned, sign.ErrSignatureMismatch},
		{"signing off", off, "whatever", sign.Unsigned, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := tc.v.Verify(tc.sig, v.Message)
			if got != tc.want || !errors.Is(err, tc.err) || (tc.err == nil) != (err == nil) {
				t.Fatalf("Verify = %v, %v; want %v, %v", got, err, tc.want, tc.err)
			}
			if err != nil && errs.KindOf(err) != errs.KindUnauthorized {
				t.Errorf("kind = %v", errs.KindOf(err))
			}
		})
	}
}

func FuzzAWSSIHMessage(f *testing.F) {
	for _, s := range []string{"", "signature=a", "b=1&a=2&signature=x", "%F0%9F%98%80=1&%EF%BD%81=2", "a=%zz", "a,b=c,d&a=b"} {
		f.Add("/p", s)
	}
	f.Fuzz(func(t *testing.T, path, query string) {
		sig, msg, err := sign.AWSSIHMessage(path, query)
		if err != nil {
			if errs.KindOf(err) != errs.KindInvalidURL {
				t.Fatalf("kind %v", errs.KindOf(err))
			}
			return
		}
		if !strings.HasPrefix(msg, path) {
			t.Fatalf("message %q does not start with path %q", msg, path)
		}
		// Moving the signature elsewhere in the query never changes the
		// message.
		if sig != "" && !strings.ContainsAny(sig, "&%+") {
			_, msg2, err := sign.AWSSIHMessage(path, "signature="+sig+"&"+query)
			if err != nil || msg2 != msg {
				t.Fatalf("message depends on signature position: %q vs %q (%v)", msg, msg2, err)
			}
		}
	})
}
