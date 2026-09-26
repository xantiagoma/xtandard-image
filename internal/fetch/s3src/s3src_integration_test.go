//go:build integration

// Integration tests against the S3-compatible RustFS service of compose.test.yml
// (`task test:integration`). They read the endpoint from the environment and
// skip when it is unset (go-standards §8):
//
//	XI_TEST_S3_ENDPOINT           e.g. http://rustfs:9000 (in compose) or http://127.0.0.1:19000
//	XI_TEST_S3_ACCESS_KEY_ID      default xitestaccess
//	XI_TEST_S3_SECRET_ACCESS_KEY  default xitestsecret
package s3src

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
	"github.com/xantiagoma/xtandard-image/internal/transform" //nolint:depguard // test only: source.Registry.Resolve takes a transform.SourceSpec
)

const (
	itBucket    = "xi-s3src-allowed"
	itOther     = "xi-s3src-other"
	itVersioned = "xi-s3src-versioned"
	itPrefix    = "public/"
)

type s3Store struct {
	endpoint, id, secret string
}

func s3Env(t *testing.T) s3Store {
	t.Helper()
	m := s3Store{
		endpoint: os.Getenv("XI_TEST_S3_ENDPOINT"),
		id:       os.Getenv("XI_TEST_S3_ACCESS_KEY_ID"),
		secret:   os.Getenv("XI_TEST_S3_SECRET_ACCESS_KEY"),
	}
	if m.endpoint == "" {
		t.Skip("XI_TEST_S3_ENDPOINT not set; run `task test:integration`")
	}
	if m.id == "" {
		m.id, m.secret = "xitestaccess", "xitestsecret"
	}
	return m
}

// seed creates the buckets and objects the tests read; it is idempotent.
func seed(t *testing.T, m s3Store) (big []byte, firstVersion string) {
	t.Helper()
	ctx := t.Context()
	c := s3.New(s3.Options{
		BaseEndpoint:               aws.String(m.endpoint),
		Region:                     DefaultRegion,
		UsePathStyle:               true,
		Credentials:                credentials.NewStaticCredentialsProvider(m.id, m.secret, ""),
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	for _, b := range []string{itBucket, itOther, itVersioned} {
		_, err := c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(b)})
		if _, owned := errors.AsType[*types.BucketAlreadyOwnedByYou](err); err != nil && !owned {
			t.Fatalf("CreateBucket %s: %v", b, err)
		}
	}
	big = bytes.Repeat([]byte("0123456789abcdef"), 4096) // 64 KiB
	_, err := c.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket:                  aws.String(itVersioned),
		VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled},
	})
	if err != nil {
		t.Fatalf("PutBucketVersioning: %v", err)
	}
	put := func(bucket, key string, body []byte) string {
		t.Helper()
		out, err := c.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader(body),
			ContentType: aws.String("image/jpeg"), CacheControl: aws.String("public, max-age=600"),
		})
		if err != nil {
			t.Fatalf("PutObject %s/%s: %v", bucket, key, err)
		}
		return aws.ToString(out.VersionId)
	}
	put(itBucket, itPrefix+"a.jpg", []byte("allowed-a"))
	put(itBucket, itPrefix+"big.bin", big)
	put(itBucket, "private/secret.jpg", []byte("secret"))
	put(itOther, itPrefix+"a.jpg", []byte("other-a"))
	firstVersion = put(itVersioned, "v.txt", []byte("version-1"))
	put(itVersioned, "v.txt", []byte("version-2"))
	return big, firstVersion
}

func TestIntegrationS3(t *testing.T) {
	t.Parallel()
	m := s3Env(t)
	big, firstVersion := seed(t, m)
	src, err := New(t.Context(), &Config{
		Name: "assets", Endpoint: m.endpoint, ForcePathStyle: true,
		AccessKeyID: m.id, SecretAccessKey: m.secret,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	reg, err := source.NewRegistry(source.Options{Sources: []source.Binding{{
		Spec:   source.Spec{Name: "assets", Type: source.TypeS3, Buckets: []string{itBucket}, Bucket: itBucket, Prefix: itPrefix},
		Source: src,
	}}})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	resolve := func(t *testing.T, raw, mount string) (source.Key, error) {
		t.Helper()
		return reg.Resolve(transform.SourceSpec{Raw: raw, Mount: mount})
	}
	fetch := func(t *testing.T, raw string, opt source.FetchOptions) (*source.Object, error) {
		t.Helper()
		k, err := resolve(t, raw, "assets")
		if err != nil {
			t.Fatalf("Resolve(%q): %v", raw, err)
		}
		return src.Fetch(t.Context(), k, opt)
	}

	t.Run("get", func(t *testing.T) {
		t.Parallel()
		for _, raw := range []string{"a.jpg", "s3://" + itBucket + "/public/a.jpg", "xi://assets/a.jpg"} {
			obj, err := fetch(t, raw, source.FetchOptions{MaxBytes: 1 << 20})
			if err != nil {
				t.Fatalf("Fetch(%q): %v", raw, err)
			}
			data, err := readAll(t, obj)
			if err != nil || data != "allowed-a" {
				t.Errorf("Fetch(%q) body = %q, %v", raw, data, err)
			}
			if obj.Size != 9 || obj.ContentType != "image/jpeg" || obj.CacheControl != "public, max-age=600" ||
				obj.ETag == "" || obj.LastModified.IsZero() {
				t.Errorf("Fetch(%q) metadata = %+v", raw, obj)
			}
		}
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		_, err := fetch(t, "missing.jpg", source.FetchOptions{})
		if got := errs.KindOf(err); got != errs.KindNotFound {
			t.Errorf("kind = %v, want not_found (%v)", got, err)
		}
	})

	t.Run("missing bucket", func(t *testing.T) {
		t.Parallel()
		_, err := src.Fetch(t.Context(), source.Key{Source: "assets", Bucket: "xi-s3src-absent", Path: "public/a.jpg"}, source.FetchOptions{})
		if got := errs.KindOf(err); got != errs.KindNotFound {
			t.Errorf("kind = %v, want not_found (%v)", got, err)
		}
	})

	t.Run("bucket not allowed", func(t *testing.T) {
		t.Parallel()
		// The object exists and the credentials can read it: only the
		// registry's bucket allowlist stands in the way.
		_, err := resolve(t, "s3://"+itOther+"/public/a.jpg", "")
		if !errors.Is(err, source.ErrNotAllowed) || errs.KindOf(err) != errs.KindForbidden {
			t.Errorf("Resolve other bucket = %v, want forbidden", err)
		}
	})

	t.Run("prefix jail", func(t *testing.T) {
		t.Parallel()
		for _, raw := range []string{"s3://" + itBucket + "/private/secret.jpg", "xi://assets/../private/secret.jpg", "../private/secret.jpg"} {
			if _, err := resolve(t, raw, "assets"); errs.KindOf(err) != errs.KindForbidden {
				t.Errorf("Resolve(%q) = %v, want forbidden", raw, err)
			}
		}
		k, err := resolve(t, "private/secret.jpg", "assets")
		if err != nil || !strings.HasPrefix(k.Path, itPrefix) {
			t.Fatalf("Resolve relative = %+v, %v; want jailed under %q", k, err, itPrefix)
		}
		if _, err := src.Fetch(t.Context(), k, source.FetchOptions{}); errs.KindOf(err) != errs.KindNotFound {
			t.Errorf("jailed fetch = %v, want not_found", err)
		}
	})

	t.Run("conditional", func(t *testing.T) {
		t.Parallel()
		obj, err := fetch(t, "a.jpg", source.FetchOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := readAll(t, obj); err != nil {
			t.Fatal(err)
		}
		for name, opt := range map[string]source.FetchOptions{
			"if-none-match":     {IfNoneMatch: obj.ETag},
			"if-modified-since": {IfModifiedSince: obj.LastModified},
		} {
			got, err := fetch(t, "a.jpg", opt)
			if err != nil || !got.NotModified || got.Body != nil {
				t.Errorf("%s: %+v, %v; want NotModified", name, got, err)
				continue
			}
			if name == "if-none-match" && got.ETag != obj.ETag {
				t.Errorf("%s: ETag = %q, want %q", name, got.ETag, obj.ETag)
			}
		}
		changed, err := fetch(t, "a.jpg", source.FetchOptions{IfNoneMatch: `"stale"`})
		if err != nil || changed.NotModified {
			t.Fatalf("stale etag: %+v, %v; want full object", changed, err)
		}
		if _, err := readAll(t, changed); err != nil {
			t.Error(err)
		}
	})

	t.Run("version", func(t *testing.T) {
		t.Parallel()
		if firstVersion == "" {
			t.Fatal("store returned no version id; versioning is off")
		}
		for version, want := range map[string]string{firstVersion: "version-1", "": "version-2"} {
			obj, err := src.Fetch(t.Context(), source.Key{Source: "assets", Bucket: itVersioned, Path: "v.txt", Version: version}, source.FetchOptions{})
			if err != nil {
				t.Fatalf("Fetch version %q: %v", version, err)
			}
			if data, err := readAll(t, obj); err != nil || data != want {
				t.Errorf("version %q body = %q, %v; want %q", version, data, err, want)
			}
		}
		_, err := src.Fetch(t.Context(), source.Key{Source: "assets", Bucket: itVersioned, Path: "missing.txt", Version: firstVersion}, source.FetchOptions{})
		if got := errs.KindOf(err); got != errs.KindNotFound {
			t.Errorf("missing versioned key kind = %v, want not_found (%v)", got, err)
		}
	})

	t.Run("oversize", func(t *testing.T) {
		t.Parallel()
		limit := int64(len(big) - 1)
		if _, err := fetch(t, "big.bin", source.FetchOptions{MaxBytes: limit}); errs.KindOf(err) != errs.KindSourceTooLarge {
			t.Errorf("kind = %v, want source_too_large", errs.KindOf(err))
		}
		obj, err := fetch(t, "big.bin", source.FetchOptions{MaxBytes: int64(len(big))})
		if err != nil {
			t.Fatal(err)
		}
		n, err := io.Copy(io.Discard, obj.Body)
		_ = obj.Body.Close()
		if err != nil || n != int64(len(big)) {
			t.Errorf("exact-limit read = %d, %v", n, err)
		}
	})
}
