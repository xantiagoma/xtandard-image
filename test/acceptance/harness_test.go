//go:build acceptance

package acceptance

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// requestTimeout bounds one request; the slowest case (drain) takes ~5 s.
const requestTimeout = 60 * time.Second

// env returns the variable or skips the test when it is unset.
func env(t *testing.T, name string) string {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		t.Skipf("%s is not set; run scripts/acceptance.sh", name)
	}
	return v
}

// base returns "http://<host:port>" for the endpoint variable.
func base(t *testing.T, name string) string {
	t.Helper()
	return "http://" + strings.TrimPrefix(env(t, name), "http://")
}

// response is a fully read HTTP response.
type response struct {
	Status int
	Header http.Header
	Body   []byte
}

var client = &http.Client{Timeout: requestTimeout}

// fetch sends a request with the given method and headers and reads the
// body; a Host header becomes the request's Host (host-bound mounts). Safe
// to call from any goroutine.
func fetch(ctx context.Context, method, url string, header http.Header) (*response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, http.NoBody)
	if err != nil {
		return nil, err
	}
	for k, vs := range header {
		if http.CanonicalHeaderKey(k) == "Host" {
			req.Host = vs[0]
			continue
		}
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return &response{Status: res.StatusCode, Header: res.Header, Body: body}, nil
}

// do is fetch that fails the test on a transport error; call it from the
// test goroutine only.
func do(ctx context.Context, t *testing.T, method, url string, header http.Header) *response {
	t.Helper()
	r, err := fetch(ctx, method, url, header)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return r
}

func get(t *testing.T, url string, header http.Header) *response {
	t.Helper()
	return do(t.Context(), t, http.MethodGet, url, header)
}

// wantStatus fails when the status differs, printing the body of errors.
func (r *response) wantStatus(t *testing.T, want int) {
	t.Helper()
	if r.Status != want {
		t.Fatalf("status %d, want %d; body %q", r.Status, want, truncate(r.Body))
	}
}

func truncate(b []byte) []byte {
	if len(b) > 200 {
		return b[:200]
	}
	return b
}

// keys are the imgproxy signing and encryption keys of the main app.
type keys struct {
	key, salt, enc []byte
}

func loadKeys(t *testing.T) keys {
	t.Helper()
	dec := func(name string) []byte {
		b, err := hex.DecodeString(env(t, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return b
	}
	return keys{key: dec("XI_ACC_SIGNING_KEY"), salt: dec("XI_ACC_SIGNING_SALT"), enc: dec("XI_ACC_ENCRYPTION_KEY")}
}

// sign returns "/<signature><path>" for an imgproxy path that starts with
// "/": base64url-nopad(HMAC-SHA256(key, salt || path)), full 32 bytes
// (https://docs.imgproxy.net/usage/signing_url).
func (k keys) sign(path string) string {
	mac := hmac.New(sha256.New, k.key)
	mac.Write(k.salt)
	mac.Write([]byte(path))
	return "/" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)) + path
}

// encrypt returns an imgproxy enc/ token: base64url-nopad(IV || AES-CBC(
// PKCS#7(plaintext))) with a random IV
// (https://docs.imgproxy.net/usage/encrypting_source_url).
func (k keys) encrypt(t *testing.T, plaintext string) string {
	t.Helper()
	block, err := aes.NewCipher(k.enc)
	if err != nil {
		t.Fatalf("aes: %v", err)
	}
	pad := aes.BlockSize - len(plaintext)%aes.BlockSize
	data := append([]byte(plaintext), bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, aes.BlockSize+len(data))
	iv := out[:aes.BlockSize]
	if _, err := rand.Read(iv); err != nil {
		t.Fatalf("iv: %v", err)
	}
	//nolint:gosec // G407 false positive: iv was just filled by crypto/rand
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out[aes.BlockSize:], data)
	return base64.RawURLEncoding.EncodeToString(out)
}

// fixture reads a file of testdata/fixtures on the host.
func fixture(t *testing.T, rel string) []byte {
	t.Helper()
	root := os.Getenv("XI_ACC_FIXTURES")
	if root == "" {
		root = filepath.Join("..", "..", "testdata", "fixtures")
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		t.Fatalf("fixtures: %v", err)
	}
	defer dir.Close()
	b, err := dir.ReadFile(filepath.FromSlash(rel))
	if err != nil {
		t.Fatalf("fixture %s: %v", rel, err)
	}
	return b
}
