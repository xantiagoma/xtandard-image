// Package gcssrc is the Google Cloud Storage source backend
// (docs/ARCHITECTURE.md §6, source type "gcs").
//
// Every fetch reads the object's metadata first, answers conditional
// requests and the size limit from it, then streams the body pinned to the
// generation it just described, so the metadata and the bytes always belong
// to the same object version.
package gcssrc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

// ErrInvalidConfig reports a Config New rejects.
var ErrInvalidConfig = errors.New("invalid gcs source config")

// Config configures one gcs source (docs/CONFIG.md XI_SOURCES_<NAME>_*).
// Bucket allowlists and the prefix jail are enforced by the source.Registry;
// the Key a backend receives already includes the prefix.
type Config struct {
	// Name is the registered source name.
	Name string
	// CredentialsJSON is a Google credentials document
	// (XI_SOURCES_<NAME>_CREDENTIALS_JSON). Accepted types: service_account,
	// authorized_user, impersonated_service_account, external_account.
	CredentialsJSON []byte
	// CredentialsFile is a path to such a document. At most one of
	// CredentialsJSON and CredentialsFile may be set; with neither,
	// Application Default Credentials are used.
	CredentialsFile string
	// Endpoint overrides the JSON API endpoint, for emulators such as
	// fake-gcs-server ("http://fake-gcs:4443/storage/v1/"). Without
	// credentials an Endpoint implies unauthenticated requests. Reads then
	// use the JSON API too, since emulators and private endpoints do not
	// serve the XML API's host-based routing. The storage library also
	// honours STORAGE_EMULATOR_HOST on its own.
	Endpoint string
	// WithoutAuthentication sends unauthenticated requests (public buckets,
	// emulators).
	WithoutAuthentication bool
	// HTTPClient replaces the transport entirely (tests, custom proxies). It
	// must carry its own authentication when the bucket needs it.
	HTTPClient *http.Client
	// UserAgent is sent with every request when set.
	UserAgent string
}

// Source is a gcs source.Source. It is safe for concurrent use.
type Source struct {
	name   string
	client *storage.Client
}

var _ source.Source = (*Source)(nil)

// New builds a gcs source. It does no network I/O; credentials are resolved
// lazily by the client on first use (ctx only bounds credential discovery).
func New(ctx context.Context, cfg *Config) (*Source, error) {
	opts, err := clientOptions(cfg)
	if err != nil {
		return nil, err
	}
	c, err := storage.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("gcs source %s: new client: %w", cfg.Name, err)
	}
	return &Source{name: cfg.Name, client: c}, nil
}

func clientOptions(cfg *Config) ([]option.ClientOption, error) {
	if cfg.Name == "" {
		return nil, fmt.Errorf("%w: empty name", ErrInvalidConfig)
	}
	if len(cfg.CredentialsJSON) > 0 && cfg.CredentialsFile != "" {
		return nil, fmt.Errorf("%w: %s: credentials json and file are exclusive", ErrInvalidConfig, cfg.Name)
	}
	var opts []option.ClientOption
	hasCreds := true
	switch {
	case len(cfg.CredentialsJSON) > 0:
		ct, err := credType(cfg.CredentialsJSON)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrInvalidConfig, cfg.Name, err)
		}
		opts = append(opts, option.WithAuthCredentialsJSON(ct, cfg.CredentialsJSON))
	case cfg.CredentialsFile != "":
		b, err := readCredFile(cfg.CredentialsFile)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrInvalidConfig, cfg.Name, err)
		}
		ct, err := credType(b)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrInvalidConfig, cfg.Name, err)
		}
		opts = append(opts, option.WithAuthCredentialsJSON(ct, b))
	default:
		hasCreds = false
	}
	if cfg.Endpoint != "" {
		opts = append(opts, option.WithEndpoint(cfg.Endpoint), storage.WithJSONReads())
	}
	if cfg.WithoutAuthentication || (cfg.Endpoint != "" && !hasCreds) {
		if hasCreds {
			return nil, fmt.Errorf("%w: %s: credentials given with authentication disabled", ErrInvalidConfig, cfg.Name)
		}
		opts = append(opts, option.WithoutAuthentication())
	}
	if cfg.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(cfg.HTTPClient))
	}
	if cfg.UserAgent != "" {
		opts = append(opts, option.WithUserAgent(cfg.UserAgent))
	}
	return opts, nil
}

// credType returns the option type of a credentials document, rejecting
// unknown types so an unexpected document is never loaded.
func credType(b []byte) (option.CredentialsType, error) {
	var doc struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return "", fmt.Errorf("parse credentials: %w", err)
	}
	switch ct := option.CredentialsType(doc.Type); ct {
	case option.ServiceAccount, option.AuthorizedUser, option.ImpersonatedServiceAccount, option.ExternalAccount:
		return ct, nil
	default:
		return "", fmt.Errorf("unsupported credentials type %q", doc.Type)
	}
}

// readCredFile reads an operator-configured credentials file.
func readCredFile(name string) ([]byte, error) {
	b, err := os.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read credentials file: %w", err)
	}
	return b, nil
}

// Name returns the registered source name.
func (s *Source) Name() string { return s.name }

// Type returns source.TypeGCS.
func (*Source) Type() string { return source.TypeGCS }

// Close releases the client's resources.
func (s *Source) Close() error { return s.client.Close() }

// Fetch streams key.Bucket/key.Path (at generation key.Version when set).
//
// With opt.IfNoneMatch (or, when absent, opt.IfModifiedSince) matching the
// object it returns an Object with NotModified set and a nil Body. An object
// larger than opt.MaxBytes fails with errs.KindSourceTooLarge before the body
// is opened, and the body itself fails the same way should it grow past the
// limit (decompressive transcoding of gzip-encoded objects).
func (s *Source) Fetch(ctx context.Context, key source.Key, opt source.FetchOptions) (*source.Object, error) {
	if key.Bucket == "" || key.Path == "" {
		return nil, errs.Newf(errs.KindInvalidURL, "gcs source %s: empty bucket or object", s.name)
	}
	obj := s.client.Bucket(key.Bucket).Object(key.Path)
	if key.Version != "" {
		gen, err := strconv.ParseInt(key.Version, 10, 64)
		if err != nil || gen <= 0 {
			return nil, errs.Newf(errs.KindInvalidURL, "gcs source %s: bad generation %q", s.name, key.Version)
		}
		obj = obj.Generation(gen)
	}
	attrs, err := obj.Attrs(ctx)
	if err != nil {
		return nil, s.classify(ctx.Err(), "attrs", key, err)
	}
	out := &source.Object{
		Size:         attrs.Size,
		ContentType:  attrs.ContentType,
		ETag:         quoteETag(attrs.Etag),
		LastModified: attrs.Updated.UTC().Truncate(time.Second),
		CacheControl: attrs.CacheControl,
	}
	if notModified(attrs, opt) {
		out.NotModified = true
		return out, nil
	}
	if opt.MaxBytes > 0 && attrs.Size > opt.MaxBytes {
		return nil, errs.Newf(errs.KindSourceTooLarge, "gcs source %s: object %d bytes over limit %d", s.name, attrs.Size, opt.MaxBytes)
	}
	r, err := obj.Generation(attrs.Generation).NewReader(ctx)
	if err != nil {
		return nil, s.classify(ctx.Err(), "read", key, err)
	}
	if r.Attrs.Decompressed {
		out.Size = -1 // transcoded: the stored size is the gzip size
	}
	out.Body = &body{r: r, src: s, key: key, left: opt.MaxBytes, limit: opt.MaxBytes}
	return out, nil
}

// notModified evaluates the conditional request against the object
// (RFC 9110 §13.2.2: If-None-Match takes precedence over If-Modified-Since).
func notModified(attrs *storage.ObjectAttrs, opt source.FetchOptions) bool {
	if opt.IfNoneMatch != "" {
		return etagMatches(opt.IfNoneMatch, attrs.Etag)
	}
	if !opt.IfModifiedSince.IsZero() && !attrs.Updated.IsZero() {
		return !attrs.Updated.Truncate(time.Second).After(opt.IfModifiedSince)
	}
	return false
}

// etagMatches reports whether an If-None-Match list names etag, using the
// weak comparison If-None-Match requires ("*" matches any object).
func etagMatches(list, etag string) bool {
	if etag == "" {
		return false
	}
	for tag := range strings.SplitSeq(list, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "*" {
			return true
		}
		tag = strings.TrimPrefix(tag, "W/")
		if strings.Trim(tag, `"`) == etag {
			return true
		}
	}
	return false
}

// quoteETag renders a gcs etag as an HTTP entity tag.
func quoteETag(etag string) string {
	if etag == "" {
		return ""
	}
	return `"` + etag + `"`
}

// classify gives a storage error its errs.Kind; ctxErr is the fetch
// context's Err() when known.
func (s *Source) classify(ctxErr error, op string, key source.Key, err error) error {
	loc := source.RedactKey(key)
	if ge, ok := errors.AsType[*googleapi.Error](err); ok {
		switch ge.Code {
		case http.StatusNotFound:
			return errs.Wrapf(errs.KindNotFound, err, "gcs %s %s", op, loc)
		case http.StatusUnauthorized, http.StatusForbidden:
			return errs.Wrapf(errs.KindForbidden, err, "gcs %s %s", op, loc)
		case http.StatusRequestTimeout, http.StatusGatewayTimeout:
			return errs.Wrapf(errs.KindUpstreamTimeout, err, "gcs %s %s", op, loc)
		default:
			return errs.Wrapf(errs.KindUpstreamError, err, "gcs %s %s", op, loc)
		}
	}
	switch {
	case errors.Is(err, storage.ErrObjectNotExist), errors.Is(err, storage.ErrBucketNotExist):
		return errs.Wrapf(errs.KindNotFound, err, "gcs %s %s", op, loc)
	case errors.Is(err, context.Canceled) || errors.Is(ctxErr, context.Canceled):
		return errs.Wrapf(errs.KindCanceled, err, "gcs %s %s", op, loc)
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctxErr, context.DeadlineExceeded):
		return errs.Wrapf(errs.KindUpstreamTimeout, err, "gcs %s %s", op, loc)
	default:
		return errs.Wrapf(errs.KindUpstreamError, err, "gcs %s %s", op, loc)
	}
}

// body streams the object and enforces the byte limit while reading.
type body struct {
	r     io.ReadCloser
	src   *Source
	key   source.Key
	limit int64 // 0 = unlimited
	left  int64 // bytes still allowed when limit > 0
}

func (b *body) Read(p []byte) (int, error) {
	if b.limit > 0 {
		if b.left <= 0 {
			// Probe one byte: EOF exactly at the limit is fine.
			var one [1]byte
			n, err := b.r.Read(one[:])
			if n > 0 {
				return 0, errs.Newf(errs.KindSourceTooLarge, "gcs source %s: body over limit %d", b.src.name, b.limit)
			}
			return 0, b.readErr(err)
		}
		if int64(len(p)) > b.left {
			p = p[:b.left]
		}
	}
	n, err := b.r.Read(p)
	b.left -= int64(n)
	return n, b.readErr(err)
}

func (b *body) readErr(err error) error {
	if err == nil || errors.Is(err, io.EOF) {
		return err
	}
	return b.src.classify(nil, "read", b.key, err)
}

func (b *body) Close() error { return b.r.Close() }
