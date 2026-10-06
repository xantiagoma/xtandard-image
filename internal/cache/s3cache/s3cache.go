// Package s3cache is the S3-compatible cache.Store (docs/ARCHITECTURE.md
// §9.2, XI_CACHE_RESULT_S3_SOURCE / _BUCKET / _PREFIX). It reuses the
// client of a named s3 source (s3src.NewClient), so endpoint, region,
// path style and credentials (static, default chain or assumed role) are
// configured once.
//
// An entry is one object at <prefix><namespace><key>: the body is the
// object, and the cache.Meta JSON (status, header subset, stored and
// expiry times, size, CRC-32C) travels base64url-encoded in the user
// metadata "xi-meta", next to the standard Content-Type and Expires. Get
// checks the expiry in that metadata before reading the body, so TTLs
// hold without any bucket lifecycle rule; an expired object is left in
// place (the next Set overwrites it) — add a lifecycle rule on the prefix
// to reclaim space from keys never requested again. Bodies are streamed:
// Set sends the caller's bytes as the request body without copying, Get
// reads the response straight into one buffer of the recorded size.
package s3cache

import (
	"cmp"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/xantiagoma/xtandard-image/internal/cache"
)

// Name is the default store label in metrics.
const Name = "s3"

// metaKey is the user metadata key (x-amz-meta-xi-meta) holding the
// entry's cache.Meta.
const metaKey = "xi-meta"

// maxMetaValue keeps the metadata under S3's 2 KB user-metadata limit
// (key plus value); an entry with a larger header subset is skipped.
const maxMetaValue = 2048 - len(metaKey) - 64

// Options configures New.
type Options struct {
	// Client is the S3 client (s3src.NewClient of the named source).
	Client *s3.Client
	// Bucket holds the entries; it must exist.
	Bucket string
	// Prefix is prepended verbatim to every object key ("cache/").
	Prefix string
	// Name labels the metrics; "" uses Name.
	Name string
	// Metrics receives lookups and skips; nil discards.
	Metrics cache.Metrics
	// Now is the clock for TTLs; nil uses time.Now.
	Now func() time.Time
}

// Store is the S3 cache. It is safe for concurrent use.
type Store struct {
	client  *s3.Client
	bucket  string
	prefix  string
	name    string
	metrics cache.Metrics
	now     func() time.Time
	closed  atomic.Bool
}

// New returns a store over opts.Client. It makes no request.
func New(opts *Options) (*Store, error) {
	if opts.Client == nil || opts.Bucket == "" {
		return nil, errors.New("s3 cache: needs a client and a bucket")
	}
	s := &Store{
		client: opts.Client, bucket: opts.Bucket, prefix: opts.Prefix,
		name: cmp.Or(opts.Name, Name), metrics: opts.Metrics, now: opts.Now,
	}
	if s.metrics == nil {
		s.metrics = cache.NopMetrics{}
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

func (s *Store) objectKey(full string) string { return s.prefix + full }

// Get implements cache.Store. A missing object, an expired entry or an
// object without valid metadata is a miss (the last one counted as
// corrupt); any other failure is returned for the caller to log.
func (s *Store) Get(ctx context.Context, ns cache.Namespace, key string) (cache.Entry, bool, error) {
	if s.closed.Load() {
		return cache.Entry{}, false, cache.ErrClosed
	}
	full := string(ns) + key
	e, ok, err := s.get(ctx, full)
	s.metrics.ObserveCache(s.name, ok)
	return e, ok, err
}

func (s *Store) get(ctx context.Context, full string) (cache.Entry, bool, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.objectKey(full))})
	if err != nil {
		if notFound(err) {
			return cache.Entry{}, false, nil
		}
		return cache.Entry{}, false, fmt.Errorf("s3 cache: get: %w", err)
	}
	defer func() { _ = out.Body.Close() }() // read-only body: the read error is what matters
	m, err := decodeMeta(out.Metadata[metaKey])
	if err == nil && m.Key != full {
		err = fmt.Errorf("%w: object holds another key", cache.ErrCorrupt)
	}
	if err == nil && out.ContentLength != nil && *out.ContentLength != m.Size {
		err = fmt.Errorf("%w: object has %d bytes, metadata says %d", cache.ErrCorrupt, *out.ContentLength, m.Size)
	}
	if err != nil {
		s.metrics.ObserveCacheSkip(s.name, cache.SkipCorrupt)
		return cache.Entry{}, false, nil
	}
	if m.Expired(s.now()) {
		return cache.Entry{}, false, nil
	}
	body, err := readBody(out.Body, out.ContentLength, m.Size)
	if err != nil {
		return cache.Entry{}, false, fmt.Errorf("s3 cache: read body: %w", err)
	}
	if err := m.Check(body); err != nil {
		s.metrics.ObserveCacheSkip(s.name, cache.SkipCorrupt)
		return cache.Entry{}, false, nil
	}
	return m.Entry(cache.NewBuffer(body, nil)), true, nil
}

// Set implements cache.Store with one PutObject streaming the caller's
// bytes. An entry whose metadata exceeds S3's user-metadata limit is
// skipped and counted (SkipTooLarge).
func (s *Store) Set(ctx context.Context, ns cache.Namespace, key string, e *cache.Entry, ttl time.Duration) error {
	if e.Body == nil {
		return errors.New("s3 cache: entry without body")
	}
	if s.closed.Load() {
		return cache.ErrClosed
	}
	full := string(ns) + key
	m, err := cache.NewMeta(full, e, s.now(), ttl)
	if err != nil {
		return err
	}
	j, err := cache.EncodeMeta(&m)
	if err != nil {
		return err
	}
	meta := base64.RawURLEncoding.EncodeToString(j)
	if len(meta) > maxMetaValue {
		s.metrics.ObserveCacheSkip(s.name, cache.SkipTooLarge)
		return nil
	}
	// A file body (a spooled video, WP-121b) is streamed from its file.
	in := &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(s.objectKey(full)),
		Body:          cache.BodyReader(e.Body),
		ContentLength: aws.Int64(cache.BodyLen(e.Body)),
		Metadata:      map[string]string{metaKey: meta},
	}
	if ct := e.Header.Get("Content-Type"); ct != "" {
		in.ContentType = aws.String(ct)
	}
	if m.ExpiresAt != 0 {
		in.Expires = aws.Time(time.Unix(0, m.ExpiresAt))
	}
	if _, err := s.client.PutObject(ctx, in); err != nil {
		return fmt.Errorf("s3 cache: put: %w", err)
	}
	return nil
}

// Delete implements cache.Store.
func (s *Store) Delete(ctx context.Context, ns cache.Namespace, key string) error {
	if s.closed.Load() {
		return cache.ErrClosed
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.objectKey(string(ns) + key))})
	if err != nil && !notFound(err) {
		return fmt.Errorf("s3 cache: delete: %w", err)
	}
	return nil
}

// Close implements cache.Store. The client belongs to the caller (it is
// shared with the named source) and stays usable.
func (s *Store) Close() error {
	s.closed.Store(true)
	return nil
}

// readBody reads the object into one buffer: of the declared length when
// the store sent one (already checked against the metadata), otherwise
// growing up to one byte past the recorded size so Meta.Check sees an
// overlong body. A short body is left for Meta.Check to reject.
func readBody(r io.Reader, contentLength *int64, size int64) ([]byte, error) {
	if contentLength == nil {
		return io.ReadAll(io.LimitReader(r, size+1))
	}
	body := make([]byte, size)
	n, err := io.ReadFull(r, body)
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return body[:n], nil
	}
	return body, err
}

func decodeMeta(v string) (cache.Meta, error) {
	if v == "" {
		return cache.Meta{}, fmt.Errorf("%w: object without %s metadata", cache.ErrCorrupt, metaKey)
	}
	j, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		return cache.Meta{}, fmt.Errorf("%w: %s metadata: %w", cache.ErrCorrupt, metaKey, err)
	}
	return cache.DecodeMeta(j)
}

// notFound reports a missing key (not a missing bucket, which is a
// configuration error worth logging).
func notFound(err error) bool {
	if ae, ok := errors.AsType[smithy.APIError](err); ok {
		switch ae.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return true
		case "NoSuchBucket":
			return false
		}
	}
	re, ok := errors.AsType[*smithyhttp.ResponseError](err)
	return ok && re.HTTPStatusCode() == http.StatusNotFound
}
