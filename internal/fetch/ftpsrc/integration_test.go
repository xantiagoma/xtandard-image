//go:build integration

package ftpsrc

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

// Integration tests run against the ftp and ftps services of
// compose.test.yml:
//
//	XI_TEST_FTP_ADDR=127.0.0.1:2121   plain FTP
//	XI_TEST_FTPS_ADDR=127.0.0.1:2122  explicit TLS required
//
// Both serve testdata/fixtures read-only under /home/xi/fixtures.

// fixture reads the repository copy of a served file.
func fixture(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "fixtures", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func integrationConfig(t *testing.T, env string) Config {
	t.Helper()
	addr, ok := os.LookupEnv(env)
	if !ok || addr == "" {
		t.Skipf("%s not set; start compose.test.yml", env)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("%s: %v", env, err)
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("%s: %v", env, err)
	}
	return Config{
		Name: "ftp-it", Host: host, Port: p, User: "xi", Password: "xipass",
		Root: "/home/xi", PoolSize: 2, DialTimeout: 5 * time.Second,
	}
}

func tlsConfig(t *testing.T) *tls.Config {
	t.Helper()
	pem, err := os.ReadFile(filepath.Join("testdata", "ftps.crt"))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		t.Fatal("bad test certificate")
	}
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
}

func itKey(p string) source.Key { return source.Key{Source: "ftp-it", Path: p} }

func TestIntegration(t *testing.T) {
	t.Parallel()
	for _, mode := range []struct {
		name string
		env  string
		tls  bool
	}{
		{"plain", "XI_TEST_FTP_ADDR", false},
		{"explicit tls", "XI_TEST_FTPS_ADDR", true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			cfg := integrationConfig(t, mode.env)
			if mode.tls {
				cfg.TLS, cfg.TLSConfig = true, tlsConfig(t)
			}
			s, err := New(&cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			runIntegration(t, s)
		})
	}
}

func runIntegration(t *testing.T, s *Source) {
	t.Helper()
	t.Run("get and nested path", func(t *testing.T) {
		t.Parallel()
		for _, rel := range []string{"jpeg/baseline.jpg", "png/rgba8.png"} {
			obj, err := s.Fetch(t.Context(), itKey("fixtures/"+rel), source.FetchOptions{})
			if err != nil {
				t.Fatalf("%s: %v", rel, err)
			}
			want := fixture(t, rel)
			if got := readAll(t, obj); !bytes.Equal(got, want) {
				t.Fatalf("%s: got %d bytes, want %d", rel, len(got), len(want))
			}
			if obj.Size != int64(len(want)) || obj.LastModified.IsZero() || obj.ETag == "" {
				t.Fatalf("%s: Size %d LastModified %v ETag %q", rel, obj.Size, obj.LastModified, obj.ETag)
			}
		}
	})
	t.Run("404", func(t *testing.T) {
		t.Parallel()
		_, err := s.Fetch(t.Context(), itKey("fixtures/jpeg/nope.jpg"), source.FetchOptions{})
		wantKind(t, err, errs.KindNotFound)
	})
	t.Run("directory is not found", func(t *testing.T) {
		t.Parallel()
		_, err := s.Fetch(t.Context(), itKey("fixtures/jpeg"), source.FetchOptions{})
		wantKind(t, err, errs.KindNotFound)
	})
	t.Run("oversize", func(t *testing.T) {
		t.Parallel()
		_, err := s.Fetch(t.Context(), itKey("fixtures/jpeg/baseline.jpg"), source.FetchOptions{MaxBytes: 1000})
		wantKind(t, err, errs.KindSourceTooLarge)
	})
	t.Run("if-modified-since 304", func(t *testing.T) {
		t.Parallel()
		k := itKey("fixtures/png/palette.png")
		obj, err := s.Fetch(t.Context(), k, source.FetchOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_ = readAll(t, obj)
		again, err := s.Fetch(t.Context(), k, source.FetchOptions{IfModifiedSince: obj.LastModified})
		if err != nil {
			t.Fatal(err)
		}
		if !again.NotModified || again.Body != nil {
			t.Fatalf("IfModifiedSince=%v: NotModified=%v", obj.LastModified, again.NotModified)
		}
		older, err := s.Fetch(t.Context(), k, source.FetchOptions{IfModifiedSince: obj.LastModified.Add(-time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if older.NotModified {
			t.Fatal("older IfModifiedSince answered NotModified")
		}
		_ = readAll(t, older)
	})
}

func TestIntegrationWrongPassword(t *testing.T) {
	t.Parallel()
	cfg := integrationConfig(t, "XI_TEST_FTP_ADDR")
	cfg.Password = "wrong"
	s, err := New(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.Fetch(t.Context(), itKey("fixtures/jpeg/baseline.jpg"), source.FetchOptions{})
	wantKind(t, err, errs.KindForbidden)
}

// TestIntegrationTLSRequired: the ftps service refuses plain logins.
func TestIntegrationTLSRequired(t *testing.T) {
	t.Parallel()
	s, err := New(new(integrationConfig(t, "XI_TEST_FTPS_ADDR")))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.Fetch(t.Context(), itKey("fixtures/jpeg/baseline.jpg"), source.FetchOptions{})
	wantKind(t, err, errs.KindForbidden) // 530 Non-anonymous sessions must use encryption
}

// TestIntegrationUntrustedCertificate: with the system roots the
// self-signed test certificate is rejected.
func TestIntegrationUntrustedCertificate(t *testing.T) {
	t.Parallel()
	cfg := integrationConfig(t, "XI_TEST_FTPS_ADDR")
	cfg.TLS = true
	s, err := New(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.Fetch(t.Context(), itKey("fixtures/jpeg/baseline.jpg"), source.FetchOptions{})
	wantKind(t, err, errs.KindUpstreamError)
}
