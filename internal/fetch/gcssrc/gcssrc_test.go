package gcssrc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fsouza/fake-gcs-server/fakestorage"
	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

const (
	testBucket = "imgs"
	testGen    = 1234
)

var testUpdated = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// newFake starts an in-process fake-gcs-server and a Source pointed at it.
func newFake(t *testing.T) *Source {
	t.Helper()
	srv, err := fakestorage.NewServerWithOptions(fakestorage.Options{
		Scheme: "http",
		Host:   "127.0.0.1",
		Writer: io.Discard,
		InitialObjects: []fakestorage.Object{
			{
				BucketName: testBucket, Name: "photos/a.jpg", ContentType: "image/jpeg",
				CacheControl: "public, max-age=60", Generation: testGen, Updated: testUpdated,
				Content: []byte("jpeg-bytes"),
			},
			{
				BucketName: testBucket, Name: "big.bin", ContentType: "application/octet-stream",
				Content: bytes.Repeat([]byte("x"), 1000),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Stop)
	s, err := New(t.Context(), &Config{Name: "assets", Endpoint: srv.URL() + "/storage/v1/"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func key(p string) source.Key { return source.Key{Source: "assets", Bucket: testBucket, Path: p} }

func readAll(t *testing.T, o *source.Object) string {
	t.Helper()
	if o.Body == nil {
		t.Fatal("nil body")
	}
	defer func() { _ = o.Body.Close() }()
	b, err := io.ReadAll(o.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func TestFetch(t *testing.T) {
	t.Parallel()
	s := newFake(t)
	if s.Name() != "assets" || s.Type() != source.TypeGCS {
		t.Fatalf("Name/Type = %q/%q", s.Name(), s.Type())
	}

	o, err := s.Fetch(t.Context(), key("photos/a.jpg"), source.FetchOptions{MaxBytes: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, o); got != "jpeg-bytes" {
		t.Errorf("body = %q", got)
	}
	if o.ETag == "" || !strings.HasPrefix(o.ETag, `"`) {
		t.Errorf("ETag = %q, want a quoted entity tag", o.ETag)
	}
	o.Body, o.ETag = nil, ""
	want := source.Object{
		Size: 10, ContentType: "image/jpeg", CacheControl: "public, max-age=60", LastModified: testUpdated,
	}
	if diff := cmp.Diff(want, *o); diff != "" {
		t.Errorf("object (-want +got):\n%s", diff)
	}
}

func TestFetchGeneration(t *testing.T) {
	t.Parallel()
	s := newFake(t)
	k := key("photos/a.jpg")
	k.Version = "1234"
	o, err := s.Fetch(t.Context(), k, source.FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, o); got != "jpeg-bytes" {
		t.Errorf("body = %q", got)
	}
}

func TestFetchErrors(t *testing.T) {
	t.Parallel()
	s := newFake(t)
	tests := []struct {
		name string
		key  source.Key
		opt  source.FetchOptions
		want errs.Kind
	}{
		{"missing object", key("photos/none.jpg"), source.FetchOptions{}, errs.KindNotFound},
		{"missing bucket", source.Key{Source: "assets", Bucket: "nope", Path: "a.jpg"}, source.FetchOptions{}, errs.KindNotFound},
		{"missing generation", source.Key{Source: "assets", Bucket: testBucket, Path: "photos/a.jpg", Version: "99"}, source.FetchOptions{}, errs.KindNotFound},
		{"bad generation", source.Key{Source: "assets", Bucket: testBucket, Path: "photos/a.jpg", Version: "x"}, source.FetchOptions{}, errs.KindInvalidURL},
		{"empty bucket", source.Key{Source: "assets", Path: "a.jpg"}, source.FetchOptions{}, errs.KindInvalidURL},
		{"oversize", key("big.bin"), source.FetchOptions{MaxBytes: 999}, errs.KindSourceTooLarge},
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
}

func TestFetchLimitExact(t *testing.T) {
	t.Parallel()
	s := newFake(t)
	o, err := s.Fetch(t.Context(), key("big.bin"), source.FetchOptions{MaxBytes: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, o); len(got) != 1000 {
		t.Errorf("read %d bytes, want 1000", len(got))
	}
}

func TestFetchConditional(t *testing.T) {
	t.Parallel()
	s := newFake(t)
	first, err := s.Fetch(t.Context(), key("photos/a.jpg"), source.FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = first.Body.Close()
	etag := first.ETag
	tests := []struct {
		name string
		opt  source.FetchOptions
		want bool
	}{
		{"etag match", source.FetchOptions{IfNoneMatch: etag}, true},
		{"weak etag in list", source.FetchOptions{IfNoneMatch: `"other", W/` + etag}, true},
		{"star", source.FetchOptions{IfNoneMatch: "*"}, true},
		{"etag mismatch", source.FetchOptions{IfNoneMatch: `"other"`}, false},
		{"etag wins over date", source.FetchOptions{IfNoneMatch: `"other"`, IfModifiedSince: testUpdated}, false},
		{"not modified since", source.FetchOptions{IfModifiedSince: testUpdated}, true},
		{"modified since", source.FetchOptions{IfModifiedSince: testUpdated.Add(-time.Second)}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o, err := s.Fetch(t.Context(), key("photos/a.jpg"), tc.opt)
			if err != nil {
				t.Fatal(err)
			}
			if o.NotModified != tc.want {
				t.Errorf("NotModified = %v, want %v", o.NotModified, tc.want)
			}
			switch {
			case o.NotModified && o.Body != nil:
				t.Error("304 with a body")
			case !o.NotModified:
				if got := readAll(t, o); got != "jpeg-bytes" {
					t.Errorf("body = %q", got)
				}
			}
			if o.ETag != etag {
				t.Errorf("ETag = %q, want %q", o.ETag, etag)
			}
		})
	}
}

// TestFetchStatus maps HTTP failures of the JSON API onto kinds.
func TestFetchStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status int
		want   errs.Kind
	}{
		{http.StatusUnauthorized, errs.KindForbidden},
		{http.StatusForbidden, errs.KindForbidden},
		{http.StatusNotFound, errs.KindNotFound},
		{http.StatusBadRequest, errs.KindUpstreamError},
	}
	for _, tc := range tests {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			t.Parallel()
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, `{"error":{"code":`+strconv.Itoa(tc.status)+`,"message":"denied"}}`)
			}))
			t.Cleanup(ts.Close)
			s, err := New(t.Context(), &Config{Name: "assets", Endpoint: ts.URL + "/storage/v1/"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			_, err = s.Fetch(t.Context(), key("a.jpg"), source.FetchOptions{})
			if got := errs.KindOf(err); got != tc.want {
				t.Errorf("kind = %v, want %v (%v)", got, tc.want, err)
			}
		})
	}
}

func TestFetchCanceled(t *testing.T) {
	t.Parallel()
	s := newFake(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := s.Fetch(ctx, key("photos/a.jpg"), source.FetchOptions{})
	if got := errs.KindOf(err); got != errs.KindCanceled {
		t.Errorf("kind = %v, want canceled (%v)", got, err)
	}
}

// TestBodyLimit covers the streaming limit, which only bites when the stored
// size under-reports the body (decompressive transcoding).
func TestBodyLimit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
		limit   int64
		want    errs.Kind // KindUnknown = no error
	}{
		{"unlimited", "abcdef", 0, errs.KindUnknown},
		{"under", "abcdef", 7, errs.KindUnknown},
		{"exact", "abcdef", 6, errs.KindUnknown},
		{"over", "abcdef", 5, errs.KindSourceTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := &body{
				r:   io.NopCloser(iotestOneByte{strings.NewReader(tc.content)}),
				src: &Source{name: "t"}, limit: tc.limit, left: tc.limit,
			}
			got, err := io.ReadAll(b)
			if k := errs.KindOf(err); k != tc.want {
				t.Fatalf("kind = %v, want %v (%v)", k, tc.want, err)
			}
			if err == nil && string(got) != tc.content {
				t.Errorf("body = %q", got)
			}
			if tc.limit > 0 && int64(len(got)) > tc.limit {
				t.Errorf("read %d bytes past limit %d", len(got), tc.limit)
			}
		})
	}
}

// iotestOneByte returns one byte per Read, exercising partial reads.
type iotestOneByte struct{ r io.Reader }

func (o iotestOneByte) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return o.r.Read(p[:1])
}

func TestNewConfig(t *testing.T) {
	t.Parallel()
	sa := []byte(`{"type":"service_account"}`)
	tests := []struct {
		name string
		cfg  Config
	}{
		{"no name", Config{}},
		{"json and file", Config{Name: "g", CredentialsJSON: sa, CredentialsFile: "/x.json"}},
		{"bad json", Config{Name: "g", CredentialsJSON: []byte("{")}},
		{"unknown type", Config{Name: "g", CredentialsJSON: []byte(`{"type":"gdch_service_account"}`)}},
		{"missing file", Config{Name: "g", CredentialsFile: "/nonexistent/creds.json"}},
		{"creds without auth", Config{Name: "g", CredentialsJSON: sa, WithoutAuthentication: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(t.Context(), &tc.cfg)
			if !errors.Is(err, ErrInvalidConfig) {
				t.Errorf("err = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestETagMatches(t *testing.T) {
	t.Parallel()
	tests := []struct {
		list, etag string
		want       bool
	}{
		{`"abc"`, "abc", true},
		{`W/"abc"`, "abc", true},
		{` "x" , "abc" `, "abc", true},
		{`"abcd"`, "abc", false},
		{`*`, "abc", true},
		{`"abc"`, "", false},
	}
	for _, tc := range tests {
		if got := etagMatches(tc.list, tc.etag); got != tc.want {
			t.Errorf("etagMatches(%q, %q) = %v, want %v", tc.list, tc.etag, got, tc.want)
		}
	}
}
