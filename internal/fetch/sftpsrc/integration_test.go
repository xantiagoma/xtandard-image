//go:build integration

package sftpsrc

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

// Integration tests run against the sftp service of compose.test.yml
// (OpenSSH, user xi chrooted to its home, testdata/fixtures mounted at
// /fixtures, host key pinned from testdata):
//
//	XI_TEST_SFTP_ADDR=127.0.0.1:2222

// fixture reads the repository copy of a served file.
func fixture(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "fixtures", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func integrationConfig(t *testing.T) Config {
	t.Helper()
	addr, ok := os.LookupEnv("XI_TEST_SFTP_ADDR")
	if !ok || addr == "" {
		t.Skip("XI_TEST_SFTP_ADDR not set; start compose.test.yml")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return Config{
		Name: "sftp-it", Host: host, Port: p, User: "xi", Password: "xipass",
		HostKey: testdataFile(t, "ssh_host_ed25519_key.pub"), PoolSize: 2, DialTimeout: 10 * time.Second,
	}
}

func itKey(p string) source.Key { return source.Key{Source: "sftp-it", Path: p} }

func newIntegrationSource(t *testing.T, cfg *Config) *Source {
	t.Helper()
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestIntegration(t *testing.T) {
	t.Parallel()
	s := newIntegrationSource(t, new(integrationConfig(t)))

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

func TestIntegrationAuth(t *testing.T) {
	t.Parallel()
	base := integrationConfig(t)
	tests := []struct {
		name   string
		mutate func(*Config)
		kind   errs.Kind // zero: the fetch succeeds
		is     error
	}{
		{name: "private key", mutate: func(c *Config) {
			c.Password, c.PrivateKey = "", testdataFile(t, "id_ed25519")
		}},
		{name: "wrong password", mutate: func(c *Config) { c.Password = "wrong" }, kind: errs.KindForbidden},
		{name: "unknown host key", mutate: func(c *Config) {
			// Any other ed25519 key: the server's real key must not be accepted.
			c.HostKey = testdataFile(t, "id_ed25519.pub")
		}, kind: errs.KindUpstreamError, is: ErrHostKeyMismatch},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := base
			tc.mutate(&cfg)
			s := newIntegrationSource(t, &cfg)
			obj, err := s.Fetch(t.Context(), itKey("fixtures/jpeg/baseline.jpg"), source.FetchOptions{})
			if tc.kind == errs.KindUnknown {
				if err != nil {
					t.Fatal(err)
				}
				if got := readAll(t, obj); !bytes.Equal(got, fixture(t, "jpeg/baseline.jpg")) {
					t.Fatal("body differs from fixture")
				}
				return
			}
			wantKind(t, err, tc.kind)
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Fatalf("err = %v, want it to wrap %v", err, tc.is)
			}
		})
	}
}
