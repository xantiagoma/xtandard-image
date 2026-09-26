package azuresrc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

// fakeTransport answers every request with fn and records the requests.
type fakeTransport struct {
	mu   sync.Mutex
	reqs []*http.Request
	fn   handler
}

// handler builds the fake response to one request.
type handler func(*http.Request) (*http.Response, error)

func (f *fakeTransport) Do(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, r)
	f.mu.Unlock()
	return f.fn(r)
}

func (f *fakeTransport) last(t *testing.T) *http.Request {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) == 0 {
		t.Fatal("no request sent")
	}
	return f.reqs[len(f.reqs)-1]
}

func respond(status int, hdr map[string]string, body string) handler {
	return func(r *http.Request) (*http.Response, error) {
		h := http.Header{}
		for k, v := range hdr {
			h.Set(k, v)
		}
		return &http.Response{
			StatusCode:    status,
			Header:        h,
			Body:          io.NopCloser(strings.NewReader(body)),
			ContentLength: int64(len(body)),
			Request:       r,
		}, nil
	}
}

func storageError(status int, code string) handler {
	body := `<?xml version="1.0" encoding="utf-8"?><Error><Code>` + code + `</Code><Message>m</Message></Error>`
	return respond(status, map[string]string{"x-ms-error-code": code, "Content-Type": "application/xml"}, body)
}

// testKey is base64("not-a-real-key-not-a-real-key-00").
const testKey = "bm90LWEtcmVhbC1rZXktbm90LWEtcmVhbC1rZXktMDA="

func newTestSource(t *testing.T, ft *fakeTransport, prefix string) source.Source {
	t.Helper()
	s, err := New(t.Context(), Config{
		Name:       "blobs",
		Account:    "acct",
		AccountKey: testKey,
		Prefix:     prefix,
		MaxRetries: -1,
		Transport:  ft,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestFetchOK(t *testing.T) {
	t.Parallel()
	lm := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	body := "\x89PNG image bytes"
	ft := &fakeTransport{fn: respond(http.StatusOK, map[string]string{
		"Content-Type":   "image/png",
		"Etag":           `"0x8DEADBEEF"`,
		"Last-Modified":  lm.Format(http.TimeFormat),
		"Cache-Control":  "public, max-age=60",
		"Content-Length": strconv.Itoa(len(body)),
	}, body)}
	s := newTestSource(t, ft, "")
	obj, err := s.Fetch(t.Context(), source.Key{Source: "blobs", Bucket: "imgs", Path: "a dir/cat+1.png"}, source.FetchOptions{MaxBytes: 1 << 20})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got, err := io.ReadAll(obj.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := obj.Body.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if string(got) != body {
		t.Errorf("body = %q, want %q", got, body)
	}
	want := source.Object{Size: int64(len(body)), ContentType: "image/png", ETag: `"0x8DEADBEEF"`, LastModified: lm, CacheControl: "public, max-age=60"}
	if diff := cmp.Diff(want, *obj, cmpopts.IgnoreFields(source.Object{}, "Body")); diff != "" {
		t.Errorf("object (-want +got):\n%s", diff)
	}
	r := ft.last(t)
	if r.Method != http.MethodGet {
		t.Errorf("method = %s", r.Method)
	}
	if got, want := r.URL.Host, "acct.blob.core.windows.net"; got != want {
		t.Errorf("host = %q, want %q", got, want)
	}
	if got, want := r.URL.EscapedPath(), "/imgs/a%20dir%2Fcat+1.png"; got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
	if !strings.HasPrefix(r.Header.Get("Authorization"), "SharedKey acct:") {
		t.Errorf("authorization = %q, want shared key", r.Header.Get("Authorization"))
	}
	if r.Header.Get("If-None-Match") != "" || r.Header.Get("If-Modified-Since") != "" {
		t.Errorf("unexpected conditional headers %v", r.Header)
	}
}

func TestFetchConditionalAndVersion(t *testing.T) {
	t.Parallel()
	ims := time.Date(2026, 8, 1, 10, 30, 0, 0, time.FixedZone("x", 3600))
	ft := &fakeTransport{fn: respond(http.StatusNotModified, map[string]string{
		"Etag":          `"0x1"`,
		"Last-Modified": "Sat, 01 Aug 2026 09:00:00 GMT",
		"Cache-Control": "max-age=5",
	}, "")}
	s := newTestSource(t, ft, "")
	obj, err := s.Fetch(t.Context(),
		source.Key{Source: "blobs", Bucket: "imgs", Path: "cat.png", Version: "2026-08-01T09:00:00.0000000Z"},
		source.FetchOptions{IfNoneMatch: `"0x1"`, IfModifiedSince: ims})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	want := source.Object{
		Size: -1, NotModified: true, ETag: `"0x1"`, CacheControl: "max-age=5",
		LastModified: time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC),
	}
	if diff := cmp.Diff(want, *obj); diff != "" {
		t.Errorf("object (-want +got):\n%s", diff)
	}
	r := ft.last(t)
	if got := r.Header.Get("If-None-Match"); got != `"0x1"` {
		t.Errorf("If-None-Match = %q", got)
	}
	if got, want := r.Header.Get("If-Modified-Since"), ims.UTC().Format(http.TimeFormat); got != want {
		t.Errorf("If-Modified-Since = %q, want %q", got, want)
	}
	if got := r.URL.Query().Get("versionid"); got != "2026-08-01T09:00:00.0000000Z" {
		t.Errorf("versionid = %q", got)
	}
}

func TestFetchStatusMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		fn   handler
		want errs.Kind
	}{
		{"blob not found", storageError(http.StatusNotFound, "BlobNotFound"), errs.KindNotFound},
		{"container not found", storageError(http.StatusNotFound, "ContainerNotFound"), errs.KindNotFound},
		{"bare 404", respond(http.StatusNotFound, nil, ""), errs.KindNotFound},
		{"authorization failure", storageError(http.StatusForbidden, "AuthorizationFailure"), errs.KindForbidden},
		{"auth failed", storageError(http.StatusForbidden, "AuthenticationFailed"), errs.KindForbidden},
		{"permission mismatch", storageError(http.StatusForbidden, "AuthorizationPermissionMismatch"), errs.KindForbidden},
		{"bare 403", respond(http.StatusForbidden, nil, ""), errs.KindForbidden},
		{"401", respond(http.StatusUnauthorized, nil, ""), errs.KindForbidden},
		{"server busy", storageError(http.StatusServiceUnavailable, "ServerBusy"), errs.KindUpstreamError},
		{"internal", storageError(http.StatusInternalServerError, "InternalError"), errs.KindUpstreamError},
		{"operation timed out", storageError(http.StatusInternalServerError, "OperationTimedOut"), errs.KindUpstreamError},
		{"gateway timeout", respond(http.StatusGatewayTimeout, nil, ""), errs.KindUpstreamTimeout},
		{"precondition", storageError(http.StatusPreconditionFailed, "ConditionNotMet"), errs.KindUpstreamError},
		{"connection refused", func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial tcp: connection refused")
		}, errs.KindUpstreamError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newTestSource(t, &fakeTransport{fn: tc.fn}, "")
			obj, err := s.Fetch(t.Context(), source.Key{Source: "blobs", Bucket: "imgs", Path: "cat.png"}, source.FetchOptions{})
			if err == nil {
				_ = obj.Body.Close()
				t.Fatal("Fetch succeeded, want error")
			}
			if got := errs.KindOf(err); got != tc.want {
				t.Errorf("kind = %v, want %v (err %v)", got, tc.want, err)
			}
		})
	}
}

func TestFetchContext(t *testing.T) {
	t.Parallel()
	block := func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	}
	t.Run("canceled", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		s := newTestSource(t, &fakeTransport{fn: block}, "")
		_, err := s.Fetch(ctx, source.Key{Source: "blobs", Bucket: "imgs", Path: "cat.png"}, source.FetchOptions{})
		if got := errs.KindOf(err); got != errs.KindCanceled {
			t.Errorf("kind = %v, want canceled (err %v)", got, err)
		}
	})
	t.Run("deadline", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithDeadline(t.Context(), time.Unix(0, 0))
		defer cancel()
		s := newTestSource(t, &fakeTransport{fn: block}, "")
		_, err := s.Fetch(ctx, source.Key{Source: "blobs", Bucket: "imgs", Path: "cat.png"}, source.FetchOptions{})
		if got := errs.KindOf(err); got != errs.KindUpstreamTimeout {
			t.Errorf("kind = %v, want upstream_timeout (err %v)", got, err)
		}
	})
}

func TestFetchMaxBytes(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("x", 100)
	tests := []struct {
		name      string
		length    string // Content-Length header; "" = omitted by the fake (chunked)
		max       int64
		fetchKind errs.Kind // KindUnknown = Fetch succeeds
		readKind  errs.Kind // KindUnknown = read succeeds
	}{
		{name: "under", length: "100", max: 200},
		{name: "exact", length: "100", max: 100},
		{name: "over by length", length: "100", max: 99, fetchKind: errs.KindSourceTooLarge},
		{name: "unlimited", length: "100", max: 0},
		{name: "unknown length exact", max: 100},
		{name: "unknown length over", max: 50, readKind: errs.KindSourceTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fn := func(r *http.Request) (*http.Response, error) {
				h := http.Header{}
				cl := int64(-1)
				if tc.length != "" {
					h.Set("Content-Length", tc.length)
					cl = int64(len(body))
				}
				return &http.Response{
					StatusCode: http.StatusOK, Header: h, ContentLength: cl,
					Body: io.NopCloser(bytes.NewReader([]byte(body))), Request: r,
				}, nil
			}
			s := newTestSource(t, &fakeTransport{fn: fn}, "")
			obj, err := s.Fetch(t.Context(), source.Key{Source: "blobs", Bucket: "imgs", Path: "big.png"}, source.FetchOptions{MaxBytes: tc.max})
			if tc.fetchKind != errs.KindUnknown {
				if got := errs.KindOf(err); got != tc.fetchKind {
					t.Fatalf("Fetch kind = %v, want %v (err %v)", got, tc.fetchKind, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			defer obj.Body.Close()
			got, err := io.ReadAll(obj.Body)
			if tc.readKind != errs.KindUnknown {
				if k := errs.KindOf(err); k != tc.readKind {
					t.Fatalf("read kind = %v, want %v (err %v)", k, tc.readKind, err)
				}
				if int64(len(got)) > tc.max {
					t.Errorf("read %d bytes past limit %d", len(got), tc.max)
				}
				return
			}
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if string(got) != body {
				t.Errorf("body length %d, want %d", len(got), len(body))
			}
		})
	}
}

func TestFetchRejectsBeforeIO(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		prefix string
		key    source.Key
	}{
		{"empty container", "", source.Key{Source: "blobs", Path: "cat.png"}},
		{"empty path", "", source.Key{Source: "blobs", Bucket: "imgs"}},
		{"outside prefix", "jail", source.Key{Source: "blobs", Bucket: "imgs", Path: "jailbreak/cat.png"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ft := &fakeTransport{fn: func(*http.Request) (*http.Response, error) {
				t.Error("unexpected request")
				return nil, errors.New("unexpected")
			}}
			s := newTestSource(t, ft, tc.prefix)
			_, err := s.Fetch(t.Context(), tc.key, source.FetchOptions{})
			if got := errs.KindOf(err); got != errs.KindForbidden {
				t.Errorf("kind = %v, want forbidden (err %v)", got, err)
			}
		})
	}
}

func TestNew(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
		host    string
	}{
		{name: "no name", cfg: Config{Account: "a", AccountKey: testKey}, wantErr: true},
		{name: "nothing", cfg: Config{Name: "x"}, wantErr: true},
		{name: "key without account", cfg: Config{Name: "x", Endpoint: "http://127.0.0.1:10000/dev", AccountKey: testKey}, wantErr: true},
		{name: "bad key", cfg: Config{Name: "x", Account: "a", AccountKey: "%%%"}, wantErr: true},
		{name: "sas with query endpoint", cfg: Config{Name: "x", Endpoint: "https://a.example/?x=1", SASToken: "sv=1"}, wantErr: true},
		{name: "bad connection string", cfg: Config{Name: "x", ConnectionString: "garbage"}, wantErr: true},
		{name: "shared key", cfg: Config{Name: "x", Account: "a", AccountKey: testKey}, host: "a.blob.core.windows.net"},
		{name: "endpoint", cfg: Config{Name: "x", Account: "a", AccountKey: testKey, Endpoint: "http://127.0.0.1:10000/a"}, host: "127.0.0.1:10000"},
		{name: "sas", cfg: Config{Name: "x", Account: "a", SASToken: "?sv=2026&sig=abc"}, host: "a.blob.core.windows.net"}, //nolint:gosec // G101: fake SAS token

		{name: "connection string", cfg: Config{Name: "x", ConnectionString: "DefaultEndpointsProtocol=http;AccountName=devstoreaccount1;AccountKey=" + testKey + ";BlobEndpoint=http://azurite:10000/devstoreaccount1;"}, host: "azurite:10000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ft := &fakeTransport{fn: respond(http.StatusOK, nil, "x")}
			tc.cfg.Transport = ft
			tc.cfg.MaxRetries = -1
			s, err := New(t.Context(), tc.cfg)
			if tc.wantErr {
				if !errors.Is(err, ErrInvalidConfig) {
					t.Fatalf("err = %v, want ErrInvalidConfig", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if s.Name() != "x" || s.Type() != source.TypeAzure {
				t.Errorf("Name/Type = %s/%s", s.Name(), s.Type())
			}
			obj, err := s.Fetch(t.Context(), source.Key{Source: "x", Bucket: "c", Path: "k"}, source.FetchOptions{})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			_ = obj.Body.Close()
			r := ft.last(t)
			if r.URL.Host != tc.host {
				t.Errorf("host = %q, want %q", r.URL.Host, tc.host)
			}
			if tc.cfg.SASToken != "" && r.URL.Query().Get("sig") != "abc" {
				t.Errorf("SAS not sent: %s", r.URL.RawQuery)
			}
		})
	}
}
