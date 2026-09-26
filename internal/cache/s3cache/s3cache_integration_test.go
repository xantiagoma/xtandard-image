//go:build integration

// Integration tests against the RustFS service of compose.test.yml
// (`task test:integration`), with the client built by s3src.NewClient as
// the app does for XI_CACHE_RESULT_S3_SOURCE. They skip when the endpoint
// is unset (go-standards §8):
//
//	XI_TEST_S3_ENDPOINT           e.g. http://rustfs:9000
//	XI_TEST_S3_ACCESS_KEY_ID      default xitestaccess
//	XI_TEST_S3_SECRET_ACCESS_KEY  default xitestsecret
package s3cache

import (
	"bytes"
	"crypto/rand"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/cache/cachetest"
	"github.com/xantiagoma/xtandard-image/internal/fetch/s3src"
)

const itBucket = "xi-s3cache"

type skipRec struct {
	cache.NopMetrics
	mu    sync.Mutex
	skips map[string]int
}

func (m *skipRec) ObserveCacheSkip(_, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.skips == nil {
		m.skips = map[string]int{}
	}
	m.skips[reason]++
}

func (m *skipRec) skipped(reason string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.skips[reason]
}

// client builds the S3 client of a named source pointing at RustFS and
// makes sure the bucket exists.
func client(t *testing.T) *s3.Client {
	t.Helper()
	endpoint := os.Getenv("XI_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("XI_TEST_S3_ENDPOINT not set; run `task test:integration`")
	}
	id, secret := os.Getenv("XI_TEST_S3_ACCESS_KEY_ID"), os.Getenv("XI_TEST_S3_SECRET_ACCESS_KEY")
	if id == "" {
		id, secret = "xitestaccess", "xitestsecret"
	}
	c, err := s3src.NewClient(t.Context(), &s3src.Config{
		Name: "cache", Endpoint: endpoint, ForcePathStyle: true, AccessKeyID: id, SecretAccessKey: secret,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.CreateBucket(t.Context(), &s3.CreateBucketInput{Bucket: aws.String(itBucket)})
	if _, owned := errors.AsType[*types.BucketAlreadyOwnedByYou](err); err != nil && !owned {
		if _, exists := errors.AsType[*types.BucketAlreadyExists](err); !exists {
			t.Fatalf("CreateBucket: %v", err)
		}
	}
	return c
}

// newStore returns a store under a prefix of its own.
func newStore(t *testing.T, c *s3.Client, opts *Options) *Store {
	t.Helper()
	opts.Client, opts.Bucket, opts.Prefix = c, itBucket, "cache-"+rand.Text()+"/"
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() }) // idempotent
	return s
}

func TestIntegrationS3Conformance(t *testing.T) {
	t.Parallel()
	c := client(t)
	cachetest.Run(t, func(t *testing.T, now func() time.Time) cache.Store {
		t.Helper()
		return newStore(t, c, &Options{Now: now})
	})
}

// TestIntegrationS3Object checks what lands in the bucket: the body as the
// object, Content-Type and Expires as standard headers, the metadata in
// x-amz-meta-xi-meta.
func TestIntegrationS3Object(t *testing.T) {
	t.Parallel()
	c := client(t)
	clock := cachetest.NewClock()
	s := newStore(t, c, &Options{Now: clock.Now})
	body := bytes.Repeat([]byte("s3"), 40000)
	cachetest.Set(t, s, cache.NSResult, "v1/obj", body, http.Header{"Content-Type": {"image/avif"}}, time.Hour)

	out, err := c.HeadObject(t.Context(), &s3.HeadObjectInput{Bucket: aws.String(itBucket), Key: aws.String(s.objectKey("res/v1/obj"))})
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}
	if got := aws.ToInt64(out.ContentLength); got != int64(len(body)) {
		t.Errorf("object size %d, want %d", got, len(body))
	}
	if got := aws.ToString(out.ContentType); got != "image/avif" {
		t.Errorf("Content-Type %q", got)
	}
	if exp, err := http.ParseTime(aws.ToString(out.ExpiresString)); err != nil || !exp.Equal(clock.Now().Add(time.Hour).Truncate(time.Second)) {
		t.Errorf("Expires %q (%v), want %s", aws.ToString(out.ExpiresString), err, clock.Now().Add(time.Hour))
	}
	if out.Metadata[metaKey] == "" {
		t.Errorf("metadata %v lacks %s", out.Metadata, metaKey)
	}
}

func TestIntegrationS3Skips(t *testing.T) {
	t.Parallel()
	c := client(t)
	m := &skipRec{}
	s := newStore(t, c, &Options{Metrics: m})

	// A header subset too large for S3 user metadata is skipped.
	huge := http.Header{"X-Xi-Ignored": {strings.Repeat("option,", 400)}}
	cachetest.Set(t, s, cache.NSResult, "v1/huge", []byte("x"), huge, time.Hour)
	if _, _, ok := cachetest.Get(t, s, cache.NSResult, "v1/huge"); ok {
		t.Error("entry with oversized metadata stored")
	}
	if got := m.skipped(cache.SkipTooLarge); got != 1 {
		t.Errorf("too_large skips %d, want 1", got)
	}

	// A foreign object (no metadata) under the prefix is a corrupt miss.
	_, err := c.PutObject(t.Context(), &s3.PutObjectInput{
		Bucket: aws.String(itBucket), Key: aws.String(s.objectKey("res/v1/foreign")), Body: strings.NewReader("hello"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := cachetest.Get(t, s, cache.NSResult, "v1/foreign"); ok {
		t.Error("foreign object served")
	}
	if got := m.skipped(cache.SkipCorrupt); got != 1 {
		t.Errorf("corrupt skips %d, want 1", got)
	}
}

func TestIntegrationS3MissingBucketIsError(t *testing.T) {
	t.Parallel()
	c := client(t)
	s, err := New(&Options{Client: c, Bucket: "xi-no-such-bucket"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Get(t.Context(), cache.NSResult, "v1/x"); ok || err == nil {
		t.Errorf("Get on a missing bucket: ok=%v err=%v, want an error", ok, err)
	}
}
