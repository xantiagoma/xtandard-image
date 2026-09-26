package errs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"testing"
)

// statusError is the interface server.writeError resolves (go-standards §6).
type statusError interface {
	error
	HTTPStatus() int
	PublicMessage() string
}

var _ statusError = (*Error)(nil)

// TestStatusTable pins every row of docs/ARCHITECTURE.md §12.
func TestStatusTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind   Kind
		name   string
		status int
		public string
	}{
		{KindInvalidURL, "invalid_url", http.StatusBadRequest, "invalid url"},
		{KindUnauthorized, "unauthorized", http.StatusForbidden, "invalid signature"},
		{KindForbidden, "forbidden", http.StatusForbidden, "forbidden"},
		{KindExpired, "expired", http.StatusNotFound, "url expired"},
		{KindNotFound, "not_found", http.StatusNotFound, "source image not found"},
		{KindSourceTooLarge, "source_too_large", http.StatusUnprocessableEntity, "source image too large"},
		{KindLimitExceeded, "limit_exceeded", http.StatusUnprocessableEntity, "image exceeds processing limits"},
		{KindUnsupported, "unsupported", http.StatusUnprocessableEntity, "unsupported image format"},
		{KindCorrupt, "corrupt", http.StatusUnprocessableEntity, "image cannot be decoded"},
		{KindHashMismatch, "hash_mismatch", http.StatusUnprocessableEntity, "source hash mismatch"},
		{KindNotAcceptable, "not_acceptable", http.StatusNotAcceptable, "no acceptable image format"},
		{KindUpstreamError, "upstream_error", http.StatusBadGateway, "upstream error"},
		{KindUpstreamTimeout, "upstream_timeout", http.StatusGatewayTimeout, "upstream timeout"},
		{KindOverloaded, "overloaded", http.StatusTooManyRequests, "too many requests"},
		{KindUnavailable, "unavailable", http.StatusServiceUnavailable, "service unavailable"},
		{KindTimeout, "timeout", http.StatusGatewayTimeout, "processing timeout"},
		{KindCanceled, "canceled", StatusClientClosedRequest, "request canceled"},
		{KindInternal, "internal", http.StatusInternalServerError, "internal server error"},
		{KindUnknown, "unknown", http.StatusInternalServerError, "internal server error"},
		{Kind(200), "Kind(200)", http.StatusInternalServerError, "internal server error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.kind.String(); got != tc.name {
				t.Errorf("String() = %q, want %q", got, tc.name)
			}
			if got := Status(tc.kind); got != tc.status {
				t.Errorf("Status() = %d, want %d", got, tc.status)
			}
			if got := PublicMessage(tc.kind); got != tc.public {
				t.Errorf("PublicMessage() = %q, want %q", got, tc.public)
			}

			// The same answers must come out of a deeply wrapped error.
			err := fmt.Errorf("outer: %w", fmt.Errorf("middle: %w", New(tc.kind, "secret /etc/passwd detail")))
			se, ok := errors.AsType[statusError](err)
			if !ok {
				t.Fatalf("errors.AsType[statusError] found nothing in %v", err)
			}
			if se.HTTPStatus() != tc.status || se.PublicMessage() != tc.public {
				t.Errorf("resolved (%d, %q), want (%d, %q)", se.HTTPStatus(), se.PublicMessage(), tc.status, tc.public)
			}
			if KindOf(err) != tc.kind {
				t.Errorf("KindOf() = %v, want %v", KindOf(err), tc.kind)
			}
			if !errors.Is(err, Match(tc.kind)) {
				t.Errorf("errors.Is(err, Match(%v)) = false", tc.kind)
			}
		})
	}
}

// TestTableComplete guards against a new Kind being added without a row.
func TestTableComplete(t *testing.T) {
	t.Parallel()
	seen := map[string]Kind{}
	for k := KindUnknown; k <= KindInternal; k++ {
		name := k.String()
		if name == "" || strings.HasPrefix(name, "Kind(") {
			t.Errorf("kind %d has no table row", k)
		}
		if prev, dup := seen[name]; dup {
			t.Errorf("kinds %d and %d share name %q", prev, k, name)
		}
		seen[name] = k
		if Status(k) == 0 || PublicMessage(k) == "" {
			t.Errorf("kind %v has empty status or public message", k)
		}
	}
	if got := (KindInternal + 1).String(); !strings.HasPrefix(got, "Kind(") {
		t.Errorf("kind after KindInternal is %q; extend the tests for new kinds", got)
	}
}

func TestKindOf(t *testing.T) {
	t.Parallel()

	notFound := Wrap(KindNotFound, fs.ErrNotExist)
	tests := []struct {
		name string
		err  error
		want Kind
	}{
		{"nil", nil, KindUnknown},
		{"foreign", errors.New("boom"), KindInternal},
		{"foreign wrapped", fmt.Errorf("x: %w", fs.ErrNotExist), KindInternal},
		{"canceled", fmt.Errorf("fetch: %w", context.Canceled), KindCanceled},
		{"deadline", fmt.Errorf("resize: %w", context.DeadlineExceeded), KindTimeout},
		{"errs wins over context", Wrap(KindUpstreamTimeout, context.DeadlineExceeded), KindUpstreamTimeout},
		{"wrapped once", notFound, KindNotFound},
		{"fmt chain", fmt.Errorf("a: %w", fmt.Errorf("b: %w", notFound)), KindNotFound},
		{"outermost kind wins", Wrap(KindCorrupt, New(KindInternal, "vips")), KindCorrupt},
		{"join first errs error", errors.Join(errors.New("plain"), notFound, New(KindForbidden, "x")), KindNotFound},
		{"join inside fmt", fmt.Errorf("cleanup: %w", errors.Join(errors.New("plain"), notFound)), KindNotFound},
		{"multi %w", fmt.Errorf("%w; %w", errors.New("plain"), New(KindExpired, "t")), KindExpired},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := KindOf(tc.err); got != tc.want {
				t.Errorf("KindOf(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestMatchThroughJoin(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("validate: %w", errors.Join(
		New(KindInvalidURL, "bad width"),
		New(KindForbidden, "option not allowed"),
	))
	for _, k := range []Kind{KindInvalidURL, KindForbidden} {
		if !errors.Is(err, Match(k)) {
			t.Errorf("errors.Is(err, Match(%v)) = false, want true", k)
		}
	}
	if errors.Is(err, Match(KindNotFound)) {
		t.Error("errors.Is(err, Match(KindNotFound)) = true, want false")
	}
	if errors.Is(errors.New("plain"), Match(KindInternal)) {
		t.Error("foreign error matched KindInternal")
	}
	if got, want := Match(KindExpired).Error(), "errs: kind expired"; got != want {
		t.Errorf("Match().Error() = %q, want %q", got, want)
	}
}

func TestCausePreserved(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("fetch: %w", Wrapf(KindNotFound, fs.ErrNotExist, "open %s", "/data/a.jpg"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Error("errors.Is(err, fs.ErrNotExist) = false through Wrapf")
	}
	e, ok := errors.AsType[*Error](err)
	if !ok {
		t.Fatal("errors.AsType[*Error] = false")
	}
	if !errors.Is(e.Unwrap(), fs.ErrNotExist) {
		t.Errorf("Unwrap() = %v, want fs.ErrNotExist", e.Unwrap())
	}
}

func TestErrorText(t *testing.T) {
	t.Parallel()
	cause := errors.New("connection refused")
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"New", New(KindInvalidURL, "bad base64"), "bad base64"},
		{"Newf", Newf(KindLimitExceeded, "width %d over %d", 9000, 8192), "width 9000 over 8192"},
		{"Wrap", Wrap(KindUpstreamError, cause), "connection refused"},
		{"Wrapf", Wrapf(KindUpstreamError, cause, "get %s", "origin.test"), "get origin.test: connection refused"},
		{"bare", &Error{Kind: KindCorrupt}, "corrupt"},
		{"WithPublic keeps internal text", WithPublic(New(KindInvalidURL, "internal"), "unknown option"), "internal"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.err.Error(); got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPublicMessageNeverLeaks(t *testing.T) {
	t.Parallel()
	const internalText = "dial tcp 10.0.0.7:443: i/o timeout"
	err := Wrapf(KindUpstreamTimeout, errors.New(internalText), "get %s", "internal.corp")
	e, ok := errors.AsType[*Error](err)
	if !ok {
		t.Fatal("errors.AsType[*Error] = false")
	}
	if pm := e.PublicMessage(); strings.Contains(pm, "10.0.0.7") || strings.Contains(pm, "internal.corp") {
		t.Errorf("PublicMessage() = %q leaks internal text", pm)
	}
	if got, want := e.PublicMessage(), "upstream timeout"; got != want {
		t.Errorf("PublicMessage() = %q, want %q", got, want)
	}
}

func TestWithPublic(t *testing.T) {
	t.Parallel()

	t.Run("overrides message keeps kind", func(t *testing.T) {
		t.Parallel()
		err := fmt.Errorf("parse: %w", WithPublic(New(KindInvalidURL, "opt foo at 3"), `unknown option "foo"`))
		se, ok := errors.AsType[statusError](err)
		if !ok {
			t.Fatal("errors.AsType[statusError] = false")
		}
		if se.HTTPStatus() != http.StatusBadRequest || se.PublicMessage() != `unknown option "foo"` {
			t.Errorf("got (%d, %q)", se.HTTPStatus(), se.PublicMessage())
		}
		if KindOf(err) != KindInvalidURL {
			t.Errorf("KindOf() = %v, want invalid_url", KindOf(err))
		}
	})
	t.Run("foreign error becomes internal", func(t *testing.T) {
		t.Parallel()
		err := WithPublic(errors.New("boom"), "try again")
		if KindOf(err) != KindInternal {
			t.Errorf("KindOf() = %v, want internal", KindOf(err))
		}
	})
}

func TestNilPassthrough(t *testing.T) {
	t.Parallel()
	if err := Wrap(KindInternal, nil); err != nil {
		t.Errorf("Wrap(nil) = %v, want nil", err)
	}
	if err := Wrapf(KindInternal, nil, "x %d", 1); err != nil {
		t.Errorf("Wrapf(nil) = %v, want nil", err)
	}
	if err := WithPublic(nil, "x"); err != nil {
		t.Errorf("WithPublic(nil) = %v, want nil", err)
	}
}
