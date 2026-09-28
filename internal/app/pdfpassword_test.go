//go:build cgo

package app

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/security/crypt"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
)

// syncBuffer is a log sink the server's goroutines write concurrently.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestPDFPassword runs WP-115 end to end: password-protected PDFs render
// with the right encrypted password on a signed URL (imgproxy, /info and
// the query dialect), a wrong one is 422 `wrong document password`, none is
// the existing 422, an unsigned URL is 403, a source's default password
// applies, the result cache keys on the password without serving it to
// anyone else, and neither the password nor its token ever appears in the
// logs (every level), the response headers or bodies.
func TestPDFPassword(t *testing.T) {
	t.Parallel()
	const (
		signKey  = "943b421c9eb07c830af81030552c86009268de4e532ba2ee2eab8247c6da0881"
		signSalt = "520f986b998545b4785e0defbc4f3c1203f22de2374a3d53cb7a7fe9fea309c5"
		encKey   = "1eb5b0e971ad7f45324c1bb15c947cb207c43152fa5c6c7f35c4f36e0c18e0f1"
		password = "secret"
		aes128PW = "open sesame"
		wrongPW  = "hunter2-wrong"
	)
	fixtures := filepath.Join(golden.ModuleRoot(t), "testdata", "fixtures")
	// The vault source holds the AES-128 fixture under another name and
	// has its password as the default.
	vaultRoot := t.TempDir()
	data, err := os.ReadFile(filepath.Join(fixtures, "pdf", "password-aes128.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vaultRoot, "locked.pdf"), data, 0o600); err != nil { //nolint:gosec // G703: the test's own temp dir
		t.Fatal(err)
	}
	cfg, _, err := config.Load(config.LoadOptions{Environ: []string{
		"XI_SERVER_ADDR=127.0.0.1:0",
		"XI_ADMIN_ADDR=127.0.0.2:0",
		"XI_SERVER_SHUTDOWN_DELAY=0s",
		"XI_SECURITY_UNSIGNED_POLICY=allow",
		"XI_OBSERVABILITY_DEBUG_HEADERS=true",
		"XI_SIGNING_KEYS=" + signKey,
		"XI_SIGNING_SALTS=" + signSalt,
		"XI_SIGNING_REQUIRED=false", // unsigned URLs follow the (allow) policy
		"XI_DIALECTS_QUERY_ENABLED=true",
		"XI_ENCRYPTION_KEYS=" + encKey,
		"XI_CACHE_RESULT_STORES=memory",
		"XI_CACHE_RESULT_MEMORY_BYTES=8MiB",
		"XI_SOURCES=docs,vault",
		"XI_SOURCES_DOCS_TYPE=fs",
		"XI_SOURCES_DOCS_ROOT=" + fixtures,
		"XI_SOURCES_VAULT_TYPE=fs",
		"XI_SOURCES_VAULT_ROOT=" + vaultRoot,
		"XI_SOURCES_VAULT_PDF_PASSWORD=" + aes128PW,
		"XI_DIALECTS_IMGPROXY_SOURCE=docs",
	}, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}

	logs := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	var store *cache.AsyncStore
	a, stop := startLogged(t, &cfg, hooks{resultCache: func(s *cache.AsyncStore) { store = s }}, logger)
	if store == nil {
		t.Fatal("result cache not built")
	}
	base := "http://" + a.public.String()

	ring, err := sign.ParseHexKeyring([]string{signKey}, []string{signSalt})
	if err != nil {
		t.Fatal(err)
	}
	ipScheme, err := sign.NewImgproxyScheme(32)
	if err != nil {
		t.Fatal(err)
	}
	ipSigner, err := sign.NewSigner(ipScheme, ring)
	if err != nil {
		t.Fatal(err)
	}
	xiScheme, err := sign.NewXIScheme(32)
	if err != nil {
		t.Fatal(err)
	}
	xiSigner, err := sign.NewSigner(xiScheme, ring)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := crypt.New(encKey)
	if err != nil {
		t.Fatal(err)
	}
	var tokens []string
	token := func(pw string) string {
		tok, err := enc.Encrypt([]byte(pw))
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, tok)
		return tok
	}
	signed := func(path string) string {
		p, err := ipSigner.SignImgproxyPath(path)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	secrets := []string{password, aes128PW, wrongPW}
	check := func(t *testing.T, r response, status int, bodyHas string) {
		t.Helper()
		if r.status != status || !strings.Contains(string(r.body), bodyHas) {
			t.Fatalf("status %d %q %.200s, want %d with %q", r.status, r.ctype, r.body, status, bodyHas)
		}
		for name, vals := range r.header {
			for _, v := range vals {
				for _, s := range append(secrets, tokens...) {
					if strings.Contains(v, s) {
						t.Errorf("header %s: %q reveals %q", name, v, s)
					}
				}
			}
		}
		for _, s := range secrets {
			if bytes.Contains(r.body, []byte(s)) {
				t.Errorf("body reveals %q", s)
			}
		}
	}

	// The steps run in order (the cache ones depend on it), so they are not
	// subtests.
	step := func(name string, fn func()) {
		t.Logf("step: %s", name)
		fn()
	}
	const pdf = "/rs:fit:100:0/plain/pdf/password.pdf"
	right := signed("/pdp:" + token(password) + pdf)
	step("right password", func() {
		r := get(t, base+right)
		check(t, r, http.StatusOK, "")
		if r.ctype != "image/jpeg" || r.header.Get("X-Xi-Cache") != "MISS" {
			t.Fatalf("first render: %q cache %q", r.ctype, r.header.Get("X-Xi-Cache"))
		}
		checkJPEG(t, r, 100, 100)
	})
	store.Wait()
	step("same password, another token: cache hit", func() {
		r := get(t, base+signed("/pdp:"+token(password)+pdf))
		check(t, r, http.StatusOK, "")
		if r.header.Get("X-Xi-Cache") != "HIT" {
			t.Errorf("X-Xi-Cache %q, want HIT", r.header.Get("X-Xi-Cache"))
		}
	})
	step("wrong password: never the cached render", func() {
		r := get(t, base+signed("/pdp:"+token(wrongPW)+pdf))
		check(t, r, http.StatusUnprocessableEntity, "wrong document password")
	})
	step("no password: never the cached render", func() {
		r := get(t, base+signed(pdf))
		check(t, r, http.StatusUnprocessableEntity, "password-protected documents are not supported")
		r = get(t, base+"/insecure"+pdf)
		check(t, r, http.StatusUnprocessableEntity, "password-protected documents are not supported")
	})
	step("unsigned", func() {
		check(t, get(t, base+"/insecure/pdp:"+token(password)+pdf), http.StatusForbidden, "pdf_password requires a signed url")
		check(t, get(t, base+"/insecure/pdp:garbage"+pdf), http.StatusForbidden, "pdf_password requires a signed url")
	})
	step("aes-128 fixture, page 2", func() {
		r := get(t, base+signed("/pdp:"+token(aes128PW)+"/pg:2/rs:fit:64:0/f:png/plain/pdf/password-aes128.pdf"))
		check(t, r, http.StatusOK, "")
		if r.ctype != "image/png" {
			t.Errorf("content type %q", r.ctype)
		}
	})
	step("info", func() {
		r := get(t, base+"/info"+signed("/pn:1/pdp:"+token(password)+"/plain/pdf/password.pdf"))
		check(t, r, http.StatusOK, "pages_number")
		r = get(t, base+"/info"+signed("/pdp:"+token(wrongPW)+"/plain/pdf/password.pdf"))
		check(t, r, http.StatusUnprocessableEntity, "wrong document password")
	})
	step("query dialect", func() {
		q, err := xiSigner.SignXIQuery("/", "url="+url.QueryEscape("xi://docs/pdf/password.pdf")+"&w=80&pdf_password="+token(password))
		if err != nil {
			t.Fatal(err)
		}
		check(t, get(t, base+"/q/?"+q), http.StatusOK, "")
		check(t, get(t, base+"/q/?url=xi://docs/pdf/password.pdf&w=80&pdf_password="+token(password)), http.StatusForbidden, "pdf_password requires a signed url")
	})
	step("source default password", func() {
		r := get(t, base+"/insecure/rs:fit:90:0/plain/xi://vault/locked.pdf")
		check(t, r, http.StatusOK, "")
		// The request's own password wins over the default.
		r = get(t, base+signed("/pdp:"+token(wrongPW)+"/rs:fit:90:0/plain/xi://vault/locked.pdf"))
		check(t, r, http.StatusUnprocessableEntity, "wrong document password")
	})

	stop() // flush every log line
	out := logs.String()
	if !strings.Contains(out, `"msg":"request served"`) || !strings.Contains(out, "pdp:REDACTED") {
		t.Fatalf("access log lines missing or not redacted:\n%.2000s", out)
	}
	for _, s := range append(secrets, tokens...) {
		if strings.Contains(out, s) {
			i := strings.Index(out, s)
			t.Errorf("logs reveal %q: ...%s...", s, out[max(0, i-200):min(len(out), i+100)])
		}
	}
}
