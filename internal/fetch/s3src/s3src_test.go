package s3src

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

var (
	lastMod = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	expires = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
)

// newFake starts an httptest S3 endpoint served by h and returns a
// path-style Source pointed at it with static credentials and no retries.
func newFake(t *testing.T, h http.HandlerFunc) *Source {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return mustNew(t, &Config{
		Name:            "assets",
		Endpoint:        srv.URL,
		Region:          "us-east-1",
		ForcePathStyle:  true,
		AccessKeyID:     "AKIDTEST",
		SecretAccessKey: "secret",
		MaxAttempts:     1,
	})
}

func mustNew(t *testing.T, cfg *Config) *Source {
	t.Helper()
	s, err := New(t.Context(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	src, ok := s.(*Source)
	if !ok {
		t.Fatalf("New returned %T", s)
	}
	return src
}

func s3Error(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>`+code+
		`</Code><Message>test</Message><RequestId>REQ1</RequestId></Error>`)
}

func readAll(t *testing.T, obj *source.Object) (string, error) {
	t.Helper()
	defer func() {
		if err := obj.Body.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()
	b, err := io.ReadAll(obj.Body)
	return string(b), err
}

func TestFetchOK(t *testing.T) {
	t.Parallel()
	const body = "\xff\xd8\xffjpeg-bytes"
	type seen struct {
		Path, Version, IfNoneMatch, IfModifiedSince, ChecksumMode string
		Signed                                                    bool
	}
	got := make(chan seen, 1)
	s := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		got <- seen{
			Path:            r.URL.EscapedPath(),
			Version:         r.URL.Query().Get("versionId"),
			IfNoneMatch:     r.Header.Get("If-None-Match"),
			IfModifiedSince: r.Header.Get("If-Modified-Since"),
			ChecksumMode:    r.Header.Get("X-Amz-Checksum-Mode"),
			Signed:          strings.Contains(r.Header.Get("Authorization"), "Credential=AKIDTEST/"),
		}
		h := w.Header()
		h.Set("ETag", `"abc123"`)
		h.Set("Last-Modified", lastMod.Format(http.TimeFormat))
		h.Set("Content-Type", "image/jpeg")
		h.Set("Cache-Control", "max-age=60")
		h.Set("Expires", expires.Format(http.TimeFormat))
		h.Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = io.WriteString(w, body)
	})
	obj, err := s.Fetch(t.Context(),
		source.Key{Source: "assets", Bucket: "photos", Path: "dir/a b+c.jpg", Version: "v1"},
		source.FetchOptions{MaxBytes: 1 << 20, IfNoneMatch: `"old"`, IfModifiedSince: lastMod.Add(-time.Hour)})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	data, err := readAll(t, obj)
	if err != nil || data != body {
		t.Fatalf("body = %q, %v; want %q", data, err, body)
	}
	obj.Body = nil
	want := &source.Object{
		Size: int64(len(body)), ContentType: "image/jpeg", ETag: `"abc123"`,
		LastModified: lastMod, CacheControl: "max-age=60", Expires: expires,
	}
	if diff := cmp.Diff(want, obj); diff != "" {
		t.Errorf("object (-want +got):\n%s", diff)
	}
	wantSeen := seen{
		Path: "/photos/dir/a%20b%2Bc.jpg", Version: "v1", IfNoneMatch: `"old"`,
		IfModifiedSince: lastMod.Add(-time.Hour).Format(http.TimeFormat), Signed: true,
	}
	if diff := cmp.Diff(wantSeen, <-got); diff != "" {
		t.Errorf("request (-want +got):\n%s", diff)
	}
}

func TestFetchNotModified(t *testing.T) {
	t.Parallel()
	s := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != `"abc123"` {
			t.Errorf("If-None-Match = %q", r.Header.Get("If-None-Match"))
		}
		w.Header().Set("ETag", `"abc123"`)
		w.Header().Set("Last-Modified", lastMod.Format(http.TimeFormat))
		w.Header().Set("Cache-Control", "max-age=60")
		w.WriteHeader(http.StatusNotModified)
	})
	obj, err := s.Fetch(t.Context(), source.Key{Bucket: "photos", Path: "a.jpg"},
		source.FetchOptions{IfNoneMatch: `"abc123"`})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	want := &source.Object{Size: -1, NotModified: true, ETag: `"abc123"`, LastModified: lastMod, CacheControl: "max-age=60"}
	if diff := cmp.Diff(want, obj); diff != "" {
		t.Errorf("object (-want +got):\n%s", diff)
	}
}

func TestFetchErrorMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		status    int
		code      string
		want      errs.Kind
		throttled bool
	}{
		{"no such key", http.StatusNotFound, "NoSuchKey", errs.KindNotFound, false},
		{"no such bucket", http.StatusNotFound, "NoSuchBucket", errs.KindNotFound, false},
		{"no such version", http.StatusNotFound, "NoSuchVersion", errs.KindNotFound, false},
		{"bare 404", http.StatusNotFound, "Whatever", errs.KindNotFound, false},
		{"access denied", http.StatusForbidden, "AccessDenied", errs.KindForbidden, false},
		{"bad signature is ours", http.StatusForbidden, "SignatureDoesNotMatch", errs.KindUpstreamError, false},
		{"slow down", http.StatusServiceUnavailable, "SlowDown", errs.KindUpstreamError, true},
		{"too many requests", http.StatusTooManyRequests, "TooManyRequests", errs.KindUpstreamError, true},
		{"throttled 400", http.StatusBadRequest, "RequestLimitExceeded", errs.KindUpstreamError, true},
		{"internal", http.StatusInternalServerError, "InternalError", errs.KindUpstreamError, false},
		{"bad request", http.StatusBadRequest, "InvalidArgument", errs.KindUpstreamError, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newFake(t, func(w http.ResponseWriter, _ *http.Request) { s3Error(w, tc.status, tc.code) })
			obj, err := s.Fetch(t.Context(), source.Key{Bucket: "photos", Path: "a.jpg"}, source.FetchOptions{})
			if err == nil {
				_ = obj.Body.Close()
				t.Fatal("Fetch succeeded")
			}
			if got := errs.KindOf(err); got != tc.want {
				t.Errorf("kind = %v, want %v (%v)", got, tc.want, err)
			}
			if got := errors.Is(err, ErrThrottled); got != tc.throttled {
				t.Errorf("throttled = %v, want %v", got, tc.throttled)
			}
			if e, ok := errors.AsType[*errs.Error](err); tc.throttled && (!ok || e.PublicMessage() != throttledPublic) {
				t.Errorf("public message of %v is not the retry hint", err)
			}
		})
	}
}

func TestFetchMaxBytes(t *testing.T) {
	t.Parallel()
	const body = "0123456789"
	tests := []struct {
		name     string
		max      int64
		chunked  bool // omit Content-Length so only the limited reader sees the size
		wantKind errs.Kind
		readKind errs.Kind
	}{
		{name: "declared too large", max: 9, wantKind: errs.KindSourceTooLarge},
		{name: "declared exactly max", max: 10},
		{name: "unlimited", max: 0},
		{name: "streamed too large", max: 9, chunked: true, readKind: errs.KindSourceTooLarge},
		{name: "streamed exactly max", max: 10, chunked: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newFake(t, func(w http.ResponseWriter, _ *http.Request) {
				if !tc.chunked {
					w.Header().Set("Content-Length", strconv.Itoa(len(body)))
				}
				w.WriteHeader(http.StatusOK)
				if f, ok := w.(http.Flusher); ok && tc.chunked {
					f.Flush() // commit chunked encoding before the body
				}
				_, _ = io.WriteString(w, body)
			})
			obj, err := s.Fetch(t.Context(), source.Key{Bucket: "b", Path: "k"}, source.FetchOptions{MaxBytes: tc.max})
			if tc.wantKind != errs.KindUnknown {
				if got := errs.KindOf(err); got != tc.wantKind {
					t.Fatalf("Fetch kind = %v, want %v (%v)", got, tc.wantKind, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if tc.chunked && obj.Size != -1 {
				t.Errorf("Size = %d, want -1", obj.Size)
			}
			data, err := readAll(t, obj)
			if tc.readKind != errs.KindUnknown {
				if got := errs.KindOf(err); got != tc.readKind {
					t.Fatalf("read kind = %v, want %v (%v)", got, tc.readKind, err)
				}
				if int64(len(data)) > tc.max {
					t.Errorf("read %d bytes past the %d limit", len(data), tc.max)
				}
				return
			}
			if err != nil || data != body {
				t.Errorf("body = %q, %v", data, err)
			}
		})
	}
}

func TestFetchContext(t *testing.T) {
	t.Parallel()
	t.Run("deadline", func(t *testing.T) {
		t.Parallel()
		s := newFake(t, func(http.ResponseWriter, *http.Request) { t.Error("request sent after deadline") })
		ctx, cancel := context.WithDeadline(t.Context(), time.Unix(0, 0))
		defer cancel()
		_, err := s.Fetch(ctx, source.Key{Bucket: "b", Path: "k"}, source.FetchOptions{})
		if got := errs.KindOf(err); got != errs.KindUpstreamTimeout {
			t.Errorf("kind = %v, want upstream_timeout (%v)", got, err)
		}
	})
	t.Run("canceled mid-request", func(t *testing.T) {
		t.Parallel()
		arrived := make(chan struct{})
		s := newFake(t, func(_ http.ResponseWriter, r *http.Request) {
			close(arrived)
			<-r.Context().Done()
		})
		ctx, cancel := context.WithCancel(t.Context())
		go func() {
			<-arrived
			cancel()
		}()
		_, err := s.Fetch(ctx, source.Key{Bucket: "b", Path: "k"}, source.FetchOptions{})
		if got := errs.KindOf(err); got != errs.KindCanceled {
			t.Errorf("kind = %v, want canceled (%v)", got, err)
		}
	})
}

func TestFetchIncompleteKey(t *testing.T) {
	t.Parallel()
	s := newFake(t, func(http.ResponseWriter, *http.Request) { t.Error("request sent for an incomplete key") })
	for _, k := range []source.Key{{Path: "k"}, {Bucket: "b"}} {
		if _, err := s.Fetch(t.Context(), k, source.FetchOptions{}); errs.KindOf(err) != errs.KindInternal {
			t.Errorf("Fetch(%+v) = %v, want internal", k, err)
		}
	}
}

// TestVirtualHostedStyle dials every connection to the fake server so the
// Host header shows how the SDK addressed the bucket.
func TestVirtualHostedStyle(t *testing.T) {
	t.Parallel()
	type seen struct{ Host, Path string }
	got := make(chan seen, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- seen{r.Host, r.URL.Path}
		_, _ = io.WriteString(w, "x")
	}))
	t.Cleanup(srv.Close)
	addr := srv.Listener.Addr().String()
	dialer := &net.Dialer{}
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, addr)
		},
	}}
	s := mustNew(t, &Config{
		Name: "r2", Endpoint: "http://store.test", Region: "auto",
		AccessKeyID: "AKIDTEST", SecretAccessKey: "secret", MaxAttempts: 1, HTTPClient: client,
	})
	obj, err := s.Fetch(t.Context(), source.Key{Bucket: "photos", Path: "a.jpg"}, source.FetchOptions{})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if _, err := readAll(t, obj); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(seen{"photos.store.test", "/a.jpg"}, <-got); diff != "" {
		t.Errorf("request (-want +got):\n%s", diff)
	}
}

// TestAssumeRole points STS at the fake server (AWS_ENDPOINT_URL_STS) and
// checks the S3 request is signed with the assumed-role credentials.
// Not parallel: it sets environment variables.
func TestAssumeRole(t *testing.T) {
	var s3Auth, s3Token, stsForm string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost { // STS
			if err := r.ParseForm(); err != nil {
				t.Errorf("ParseForm: %v", err)
			}
			stsForm = r.PostForm.Get("Action") + " " + r.PostForm.Get("RoleArn") + " " + r.PostForm.Get("RoleSessionName")
			w.Header().Set("Content-Type", "text/xml")
			_, _ = io.WriteString(w, `<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><AssumeRoleResult>`+
				`<Credentials><AccessKeyId>ASIAROLE</AccessKeyId><SecretAccessKey>s</SecretAccessKey>`+
				`<SessionToken>tok</SessionToken><Expiration>2099-01-01T00:00:00Z</Expiration></Credentials>`+
				`<AssumedRoleUser><Arn>arn:aws:sts::1:assumed-role/r/x</Arn><AssumedRoleId>AROA:x</AssumedRoleId></AssumedRoleUser>`+
				`</AssumeRoleResult><ResponseMetadata><RequestId>1</RequestId></ResponseMetadata></AssumeRoleResponse>`)
			return
		}
		s3Auth, s3Token = r.Header.Get("Authorization"), r.Header.Get("X-Amz-Security-Token")
		_, _ = io.WriteString(w, "x")
	}))
	t.Cleanup(srv.Close)
	t.Setenv("AWS_ENDPOINT_URL_STS", srv.URL)
	s := mustNew(t, &Config{
		Name: "assets", Endpoint: srv.URL, Region: "us-east-1", ForcePathStyle: true,
		AccessKeyID: "AKIDBASE", SecretAccessKey: "secret", RoleARN: "arn:aws:iam::1:role/r", MaxAttempts: 1,
	})
	obj, err := s.Fetch(t.Context(), source.Key{Bucket: "b", Path: "k"}, source.FetchOptions{})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if _, err := readAll(t, obj); err != nil {
		t.Fatal(err)
	}
	if want := "AssumeRole arn:aws:iam::1:role/r " + DefaultRoleSessionName; stsForm != want {
		t.Errorf("sts request = %q, want %q", stsForm, want)
	}
	if !strings.Contains(s3Auth, "Credential=ASIAROLE/") || s3Token != "tok" {
		t.Errorf("s3 request not signed with the role: auth=%q token=%q", s3Auth, s3Token)
	}
}

// TestNewRegion covers region fallbacks. Not parallel: it isolates the SDK
// from the host's AWS environment.
func TestNewRegion(t *testing.T) {
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_CONFIG_FILE", t.TempDir()+"/none")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", t.TempDir()+"/none")
	if _, err := New(t.Context(), &Config{Name: "aws"}); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("no region, no endpoint: err = %v, want ErrInvalidConfig", err)
	}
	s := mustNew(t, &Config{Name: "minio", Endpoint: "http://minio:9000"})
	if got := s.client.Options().Region; got != DefaultRegion {
		t.Errorf("region = %q, want %q", got, DefaultRegion)
	}
	if s.Name() != "minio" || s.Type() != source.TypeS3 {
		t.Errorf("Name/Type = %q/%q", s.Name(), s.Type())
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cfg  Config
		ok   bool
	}{
		{"minimal", Config{Name: "a"}, true},
		{"full", Config{Name: "a", Endpoint: "https://x.r2.cloudflarestorage.com", AccessKeyID: "k", SecretAccessKey: "s"}, true},
		{"no name", Config{}, false},
		{"relative endpoint", Config{Name: "a", Endpoint: "minio:9000"}, false},
		{"ftp endpoint", Config{Name: "a", Endpoint: "ftp://minio"}, false},
		{"userinfo endpoint", Config{Name: "a", Endpoint: "http://user@minio"}, false},
		{"half credentials", Config{Name: "a", AccessKeyID: "k"}, false},
		{"negative attempts", Config{Name: "a", MaxAttempts: -1}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.cfg.Validate()
			if (err == nil) != tc.ok || (err != nil && !errors.Is(err, ErrInvalidConfig)) {
				t.Errorf("Validate() = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}
