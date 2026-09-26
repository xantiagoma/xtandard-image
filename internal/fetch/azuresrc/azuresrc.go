// Package azuresrc is the Azure Blob Storage source backend
// (docs/ARCHITECTURE.md §6, source type "azure"). It streams one blob per
// Fetch with DownloadStream, honouring conditional headers and blob version
// ids, and classifies every failure with an errs.Kind.
//
// Key.Bucket is the container and Key.Path the blob name (prefix jail already
// applied by the source.Registry). The endpoint override targets Azurite or
// sovereign clouds.
package azuresrc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

// ErrInvalidConfig reports a Config that New rejects.
var ErrInvalidConfig = errors.New("azuresrc: invalid config")

// Config configures one azure source (docs/CONFIG.md XI_SOURCES_<NAME>_*).
//
// Credentials are chosen in this order: ConnectionString, AccountKey (shared
// key), SASToken, otherwise DefaultAzureCredential (environment, workload
// identity, managed identity, Azure CLI).
type Config struct {
	Name string // source name

	// Account is the storage account name. Required unless ConnectionString
	// or Endpoint is set.
	Account string
	// AccountKey is the base64 shared key.
	AccountKey string
	// ConnectionString is a full storage connection string; it carries its
	// own account, credentials and endpoint, and wins over every other field.
	ConnectionString string
	// SASToken is an account or container SAS query string, with or without
	// the leading "?".
	SASToken string
	// Endpoint overrides the service URL, default
	// "https://<account>.blob.core.windows.net/". Azurite uses
	// "http://127.0.0.1:10000/devstoreaccount1".
	Endpoint string

	// Prefix is the configured key prefix jail. The Registry already applies
	// it; Fetch re-checks it as defence in depth and refuses keys outside it
	// with errs.KindForbidden.
	Prefix string

	// MaxRetries is the SDK retry count per Fetch: 0 keeps the SDK default
	// (3), a negative value disables retries.
	MaxRetries int32
	// Transport sends the HTTP requests (an *http.Client satisfies it); nil
	// uses the SDK default client.
	Transport policy.Transporter
}

// Source fetches blobs from one storage account. It is safe for concurrent
// use.
type Source struct {
	name   string
	prefix string // "" or "dir/.../"
	svc    *service.Client
}

var _ source.Source = (*Source)(nil)

// New validates cfg and builds the service client. It performs no I/O:
// credentials are first exercised by Fetch.
//
//nolint:gocritic // hugeParam: called once per source at startup; a value keeps the ARCHITECTURE §6 New(ctx, cfg) shape.
func New(_ context.Context, cfg Config) (source.Source, error) {
	if cfg.Name == "" {
		return nil, fmt.Errorf("%w: empty source name", ErrInvalidConfig)
	}
	opts := &azblob.ClientOptions{
		Retry:     policy.RetryOptions{MaxRetries: cfg.MaxRetries},
		Telemetry: policy.TelemetryOptions{ApplicationID: "xtandard-image"},
		Transport: cfg.Transport,
	}
	c, err := newClient(&cfg, opts)
	if err != nil {
		return nil, fmt.Errorf("%w: source %q: %w", ErrInvalidConfig, cfg.Name, err)
	}
	return &Source{name: cfg.Name, prefix: dirPrefix(cfg.Prefix), svc: c.ServiceClient()}, nil
}

func newClient(cfg *Config, opts *azblob.ClientOptions) (*azblob.Client, error) {
	if cfg.ConnectionString != "" {
		return azblob.NewClientFromConnectionString(cfg.ConnectionString, opts)
	}
	endpoint := cfg.Endpoint
	switch {
	case endpoint != "":
	case cfg.Account != "":
		endpoint = "https://" + cfg.Account + ".blob.core.windows.net/"
	default:
		return nil, errors.New("account, endpoint or connection string required")
	}
	switch {
	case cfg.AccountKey != "":
		if cfg.Account == "" {
			return nil, errors.New("account key needs an account name")
		}
		cred, err := azblob.NewSharedKeyCredential(cfg.Account, cfg.AccountKey)
		if err != nil {
			return nil, err
		}
		return azblob.NewClientWithSharedKeyCredential(endpoint, cred, opts)
	case cfg.SASToken != "":
		if strings.Contains(endpoint, "?") {
			return nil, errors.New("endpoint must not carry a query when a SAS token is set")
		}
		return azblob.NewClientWithNoCredential(endpoint+"?"+strings.TrimPrefix(cfg.SASToken, "?"), opts)
	default:
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, err
		}
		return azblob.NewClient(endpoint, cred, opts)
	}
}

// Name implements source.Source.
func (s *Source) Name() string { return s.name }

// Type implements source.Source.
func (*Source) Type() string { return source.TypeAzure }

// Fetch implements source.Source. The returned Body streams from the
// service; a positive opt.MaxBytes is enforced up front from Content-Length
// and again while reading. Key.Version is a blob version id.
func (s *Source) Fetch(ctx context.Context, key source.Key, opt source.FetchOptions) (*source.Object, error) {
	if key.Bucket == "" || key.Path == "" {
		return nil, errs.Newf(errs.KindForbidden, "azure source %q: empty container or blob name", s.name)
	}
	if s.prefix != "" && !strings.HasPrefix(key.Path, s.prefix) {
		return nil, errs.Newf(errs.KindForbidden, "azure source %q: key outside prefix jail", s.name)
	}
	bc := s.svc.NewContainerClient(key.Bucket).NewBlobClient(key.Path)
	if key.Version != "" {
		v, err := bc.WithVersionID(key.Version)
		if err != nil {
			return nil, errs.Wrapf(errs.KindInvalidURL, err, "azure source %q: bad version id", s.name)
		}
		bc = v
	}

	var mac blob.ModifiedAccessConditions
	if opt.IfNoneMatch != "" {
		et := azcore.ETag(opt.IfNoneMatch)
		mac.IfNoneMatch = &et
	}
	if !opt.IfModifiedSince.IsZero() {
		t := opt.IfModifiedSince.UTC()
		mac.IfModifiedSince = &t
	}
	var raw *http.Response
	resp, err := bc.DownloadStream(policy.WithCaptureResponse(ctx, &raw), &blob.DownloadStreamOptions{
		AccessConditions: &blob.AccessConditions{ModifiedAccessConditions: &mac},
	})
	if err != nil {
		return nil, classify(ctx, s.name, err)
	}
	// The SDK turns a 304 into a success with a nil Body.
	if resp.Body == nil || (raw != nil && raw.StatusCode == http.StatusNotModified) {
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		return notModified(raw, opt), nil
	}

	obj := &source.Object{
		Body:         resp.Body,
		Size:         -1,
		ContentType:  deref(resp.ContentType),
		ETag:         string(deref(resp.ETag)),
		CacheControl: deref(resp.CacheControl),
	}
	if resp.LastModified != nil {
		obj.LastModified = resp.LastModified.UTC()
	}
	if resp.ContentLength != nil && *resp.ContentLength >= 0 {
		obj.Size = *resp.ContentLength
	}
	if opt.MaxBytes > 0 {
		if obj.Size > opt.MaxBytes {
			_ = resp.Body.Close()
			return nil, errs.Newf(errs.KindSourceTooLarge, "azure source %q: blob is %d bytes, limit %d", s.name, obj.Size, opt.MaxBytes)
		}
		obj.Body = &limitedBody{rc: resp.Body, left: opt.MaxBytes}
	}
	return obj, nil
}

// notModified builds the 304 result, taking validators from the raw
// response when the SDK captured it.
func notModified(raw *http.Response, opt source.FetchOptions) *source.Object {
	obj := &source.Object{Size: -1, NotModified: true, ETag: opt.IfNoneMatch}
	if raw == nil {
		return obj
	}
	if et := raw.Header.Get("ETag"); et != "" {
		obj.ETag = et
	}
	obj.CacheControl = raw.Header.Get("Cache-Control")
	if lm, err := http.ParseTime(raw.Header.Get("Last-Modified")); err == nil {
		obj.LastModified = lm.UTC()
	}
	return obj
}

// classify maps an SDK or transport error to an errs.Kind.
func classify(ctx context.Context, name string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return errs.Wrapf(errs.KindUpstreamTimeout, err, "azure source %q: fetch deadline", name)
		}
		return errs.Wrapf(errs.KindCanceled, err, "azure source %q: fetch canceled", name)
	}
	if re, ok := errors.AsType[*azcore.ResponseError](err); ok {
		return errs.Wrapf(kindForResponse(re), err, "azure source %q: status %d %s", name, re.StatusCode, re.ErrorCode)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errs.Wrapf(errs.KindUpstreamTimeout, err, "azure source %q", name)
	}
	if ne, ok := errors.AsType[net.Error](err); ok && ne.Timeout() {
		return errs.Wrapf(errs.KindUpstreamTimeout, err, "azure source %q", name)
	}
	return errs.Wrapf(errs.KindUpstreamError, err, "azure source %q", name)
}

func kindForResponse(re *azcore.ResponseError) errs.Kind {
	switch {
	case bloberror.HasCode(re, bloberror.BlobNotFound, bloberror.ContainerNotFound, bloberror.ResourceNotFound):
		return errs.KindNotFound
	case bloberror.HasCode(re, bloberror.AuthorizationFailure, bloberror.AuthenticationFailed,
		bloberror.AuthorizationPermissionMismatch, bloberror.AuthorizationResourceTypeMismatch,
		bloberror.InsufficientAccountPermissions):
		return errs.KindForbidden
	}
	switch re.StatusCode {
	case http.StatusNotFound:
		return errs.KindNotFound
	case http.StatusUnauthorized, http.StatusForbidden:
		return errs.KindForbidden
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return errs.KindUpstreamTimeout
	default:
		return errs.KindUpstreamError
	}
}

// limitedBody fails the read that would exceed left bytes with
// errs.KindSourceTooLarge instead of silently truncating.
type limitedBody struct {
	rc   io.ReadCloser
	left int64
}

func (l *limitedBody) Read(p []byte) (int, error) {
	if l.left < 0 {
		return 0, errs.New(errs.KindSourceTooLarge, "azure blob exceeds size limit")
	}
	// Read at most one byte past the limit: an exact-size body still sees
	// EOF, a longer one trips the check below.
	if int64(len(p)) > l.left+1 {
		p = p[:l.left+1]
	}
	n, err := l.rc.Read(p)
	l.left -= int64(n)
	if l.left < 0 {
		return n - 1, errs.New(errs.KindSourceTooLarge, "azure blob exceeds size limit")
	}
	return n, err
}

func (l *limitedBody) Close() error { return l.rc.Close() }

func dirPrefix(p string) string {
	p = strings.TrimLeft(p, "/")
	if p == "" || strings.HasSuffix(p, "/") {
		return p
	}
	return p + "/"
}

func deref[T any](p *T) T {
	if p == nil {
		var z T
		return z
	}
	return *p
}
