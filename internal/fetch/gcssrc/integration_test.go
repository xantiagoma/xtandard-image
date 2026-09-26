//go:build integration

package gcssrc

import (
	"io"
	"os"
	"strconv"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

// TestIntegrationFakeGCS runs against the fake-gcs-server service of
// compose.test.yml:
//
//	docker compose -f compose.test.yml up -d fake-gcs
//	XI_TEST_GCS_ENDPOINT=http://localhost:4443/storage/v1/ go test -tags integration ./internal/fetch/gcssrc
func TestIntegrationFakeGCS(t *testing.T) {
	t.Parallel()
	endpoint, ok := os.LookupEnv("XI_TEST_GCS_ENDPOINT")
	if !ok || endpoint == "" {
		t.Skip("XI_TEST_GCS_ENDPOINT not set")
	}
	ctx := t.Context()

	// Seed a fresh bucket through the SDK so the test owns its data.
	admin, err := storage.NewClient(ctx, option.WithEndpoint(endpoint), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	bucket := "xi-it-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := admin.Bucket(bucket).Create(ctx, "xi-test", nil); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	w := admin.Bucket(bucket).Object("jail/a.png").NewWriter(ctx)
	w.ChunkSize = 0 // single request upload
	w.ContentType = "image/png"
	w.CacheControl = "public, max-age=300"
	if _, err := io.WriteString(w, "png-bytes"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("upload: %v", err)
	}

	s, err := New(ctx, &Config{Name: "gcs", Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	k := source.Key{Source: "gcs", Bucket: bucket, Path: "jail/a.png"}

	t.Run("get", func(t *testing.T) {
		t.Parallel()
		o, err := s.Fetch(t.Context(), k, source.FetchOptions{MaxBytes: 1 << 20})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = o.Body.Close() }()
		b, err := io.ReadAll(o.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != "png-bytes" || o.Size != 9 || o.ContentType != "image/png" ||
			o.CacheControl != "public, max-age=300" || o.ETag == "" || o.LastModified.IsZero() {
			t.Errorf("got body %q, object %+v", b, *o)
		}

		c, err := s.Fetch(t.Context(), k, source.FetchOptions{IfNoneMatch: o.ETag})
		if err != nil {
			t.Fatal(err)
		}
		if !c.NotModified || c.Body != nil {
			t.Errorf("conditional fetch: %+v, want NotModified without body", *c)
		}
	})
	t.Run("errors", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name string
			key  source.Key
			opt  source.FetchOptions
			want errs.Kind
		}{
			{"missing object", source.Key{Source: "gcs", Bucket: bucket, Path: "jail/none.png"}, source.FetchOptions{}, errs.KindNotFound},
			{"missing bucket", source.Key{Source: "gcs", Bucket: bucket + "-none", Path: "a.png"}, source.FetchOptions{}, errs.KindNotFound},
			{"oversize", k, source.FetchOptions{MaxBytes: 8}, errs.KindSourceTooLarge},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				o, err := s.Fetch(t.Context(), tc.key, tc.opt)
				if err == nil {
					_ = o.Body.Close()
					t.Fatal("no error")
				}
				if got := errs.KindOf(err); got != tc.want {
					t.Errorf("kind = %v, want %v (%v)", got, tc.want, err)
				}
			})
		}
	})
}
