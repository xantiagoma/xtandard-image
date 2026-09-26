//go:build integration

package azuresrc

import (
	"context"
	"io"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

// Azurite's well-known development account (public, documented by Microsoft).
const (
	devAccount = "devstoreaccount1"
	devKey     = "Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw=="
)

// azurite returns the blob endpoint of the compose.test.yml azurite service,
// e.g. http://127.0.0.1:10000/devstoreaccount1, or skips.
func azurite(t *testing.T) string {
	t.Helper()
	ep := os.Getenv("XI_TEST_AZURITE_ENDPOINT")
	if ep == "" {
		t.Skip("XI_TEST_AZURITE_ENDPOINT not set (docker compose -f compose.test.yml up -d azurite)")
	}
	return ep
}

// seed creates a fresh container holding blobs and returns its name.
func seed(ctx context.Context, t *testing.T, endpoint string, blobs map[string]string) string {
	t.Helper()
	cred, err := azblob.NewSharedKeyCredential(devAccount, devKey)
	if err != nil {
		t.Fatal(err)
	}
	c, err := azblob.NewClientWithSharedKeyCredential(endpoint, cred, nil)
	if err != nil {
		t.Fatal(err)
	}
	container := "wp15-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := c.CreateContainer(ctx, container, nil); err != nil {
		t.Fatalf("create container: %v", err)
	}
	t.Cleanup(func() { _, _ = c.DeleteContainer(context.WithoutCancel(ctx), container, nil) })
	ct, cc := "image/png", "public, max-age=300"
	for name, body := range blobs {
		_, err := c.UploadBuffer(ctx, container, name, []byte(body), &azblob.UploadBufferOptions{
			HTTPHeaders: &blob.HTTPHeaders{BlobContentType: &ct, BlobCacheControl: &cc},
		})
		if err != nil {
			t.Fatalf("upload %s: %v", name, err)
		}
	}
	return container
}

func TestAzuriteIntegration(t *testing.T) {
	t.Parallel()
	endpoint := azurite(t)
	ctx := t.Context()
	body := "\x89PNG fake image body for azurite"
	container := seed(ctx, t, endpoint, map[string]string{
		"img/cat.png":     body,
		"img/sub/dog.png": body + body,
		"outside.png":     body,
	})
	newSrc := func(t *testing.T, cfg Config) source.Source {
		t.Helper()
		cfg.Name = "az"
		cfg.Endpoint = endpoint
		cfg.MaxRetries = -1
		s, err := New(t.Context(), cfg)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return s
	}
	key := func(p string) source.Key { return source.Key{Source: "az", Bucket: container, Path: p} }
	s := newSrc(t, Config{Account: devAccount, AccountKey: devKey})

	t.Run("get", func(t *testing.T) {
		t.Parallel()
		obj, err := s.Fetch(t.Context(), key("img/cat.png"), source.FetchOptions{MaxBytes: 1 << 20})
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		defer obj.Body.Close()
		got, err := io.ReadAll(obj.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != body {
			t.Errorf("body = %q", got)
		}
		if obj.Size != int64(len(body)) || obj.ContentType != "image/png" || obj.CacheControl != "public, max-age=300" ||
			obj.ETag == "" || obj.LastModified.IsZero() || obj.NotModified {
			t.Errorf("object metadata = %+v", *obj)
		}
	})

	t.Run("connection string", func(t *testing.T) {
		t.Parallel()
		cs := newSrc(t, Config{ConnectionString: "DefaultEndpointsProtocol=http;AccountName=" + devAccount +
			";AccountKey=" + devKey + ";BlobEndpoint=" + endpoint + ";"})
		obj, err := cs.Fetch(t.Context(), key("img/cat.png"), source.FetchOptions{})
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		_ = obj.Body.Close()
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		for _, k := range []source.Key{key("img/missing.png"), {Source: "az", Bucket: "no-such-container", Path: "x.png"}} {
			_, err := s.Fetch(t.Context(), k, source.FetchOptions{})
			if got := errs.KindOf(err); got != errs.KindNotFound {
				t.Errorf("%s: kind = %v, want not_found (err %v)", source.RedactKey(k), got, err)
			}
		}
	})

	t.Run("bad key forbidden", func(t *testing.T) {
		t.Parallel()
		bad := newSrc(t, Config{Account: devAccount, AccountKey: "d3Jvbmcta2V5LXdyb25nLWtleS13cm9uZy1rZXktMDA="})
		_, err := bad.Fetch(t.Context(), key("img/cat.png"), source.FetchOptions{})
		if got := errs.KindOf(err); got != errs.KindForbidden {
			t.Errorf("kind = %v, want forbidden (err %v)", got, err)
		}
	})

	t.Run("conditional", func(t *testing.T) {
		t.Parallel()
		first, err := s.Fetch(t.Context(), key("img/cat.png"), source.FetchOptions{})
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		_ = first.Body.Close()

		obj, err := s.Fetch(t.Context(), key("img/cat.png"), source.FetchOptions{IfNoneMatch: first.ETag})
		if err != nil {
			t.Fatalf("If-None-Match: %v", err)
		}
		if !obj.NotModified || obj.Body != nil || obj.ETag != first.ETag {
			t.Errorf("If-None-Match: object = %+v, want NotModified with ETag %s", *obj, first.ETag)
		}

		obj, err = s.Fetch(t.Context(), key("img/cat.png"), source.FetchOptions{IfModifiedSince: first.LastModified.Add(time.Hour)})
		if err != nil {
			t.Fatalf("If-Modified-Since: %v", err)
		}
		if !obj.NotModified {
			_ = obj.Body.Close()
			t.Errorf("If-Modified-Since: want NotModified")
		}

		obj, err = s.Fetch(t.Context(), key("img/cat.png"), source.FetchOptions{IfNoneMatch: `"0xSTALE"`})
		if err != nil {
			t.Fatalf("stale ETag: %v", err)
		}
		if obj.NotModified {
			t.Fatal("stale ETag: got NotModified")
		}
		_ = obj.Body.Close()
	})

	t.Run("oversize", func(t *testing.T) {
		t.Parallel()
		_, err := s.Fetch(t.Context(), key("img/sub/dog.png"), source.FetchOptions{MaxBytes: int64(len(body))})
		if got := errs.KindOf(err); got != errs.KindSourceTooLarge {
			t.Errorf("kind = %v, want source_too_large (err %v)", got, err)
		}
	})

	t.Run("prefix", func(t *testing.T) {
		t.Parallel()
		jailed := newSrc(t, Config{Account: devAccount, AccountKey: devKey, Prefix: "/img"})
		obj, err := jailed.Fetch(t.Context(), key("img/sub/dog.png"), source.FetchOptions{})
		if err != nil {
			t.Fatalf("inside prefix: %v", err)
		}
		_ = obj.Body.Close()
		for _, p := range []string{"outside.png", "imgx/cat.png"} {
			_, err := jailed.Fetch(t.Context(), key(p), source.FetchOptions{})
			if got := errs.KindOf(err); got != errs.KindForbidden {
				t.Errorf("%s: kind = %v, want forbidden (err %v)", p, got, err)
			}
		}
	})
}
