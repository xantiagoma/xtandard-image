// Package s3src is the s3 source backend (docs/ARCHITECTURE.md §6): it
// fetches objects from AWS S3 or any S3-compatible store (RustFS, Cloudflare
// R2, DigitalOcean Spaces, Backblaze B2) through aws-sdk-go-v2.
//
// Keys arrive already allowlisted and jailed by source.Registry: Key.Bucket is
// an allowed bucket and Key.Path carries the prefix jail. The backend only
// maps S3 responses onto source.Object and errs kinds.
package s3src

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

// DefaultRegion is the signing region used when an endpoint is configured
// without a region (self-hosted stores such as RustFS accept it; R2 wants
// "auto", which callers set).
const DefaultRegion = "us-east-1"

// DefaultRoleSessionName names assumed-role sessions in CloudTrail.
const DefaultRoleSessionName = "xtandard-image"

// ErrInvalidConfig reports a Config that New rejects.
var ErrInvalidConfig = errors.New("s3src: invalid config")

// ErrThrottled marks a fetch the store rejected for rate (SlowDown, 503,
// 429) after the SDK's own retries: the error is errs.KindUpstreamError and
// the caller may retry later (for example by not negative-caching it).
var ErrThrottled = errors.New("s3src: throttled by upstream")

// Config is one s3 source (docs/CONFIG.md XI_SOURCES_<NAME>_*). Bucket
// allowlist and prefix jail are enforced by source.Registry, not here.
type Config struct {
	// Name is the registered source name.
	Name string
	// Endpoint is the S3-compatible base URL (https://<acct>.r2.cloudflarestorage.com,
	// http://rustfs:9000, ...). Empty means AWS S3 resolved from Region.
	Endpoint string
	// Region is the signing region; empty falls back to the SDK chain
	// (AWS_REGION, shared config) and then, with an Endpoint, DefaultRegion.
	Region string
	// ForcePathStyle addresses objects as <endpoint>/<bucket>/<key> (RustFS,
	// B2, most self-hosted stores) instead of virtual-hosted style.
	ForcePathStyle bool
	// AccessKeyID and SecretAccessKey select static credentials. Both empty
	// means the SDK default chain: AWS_* env, shared files, web identity
	// (IRSA / EKS pod identity), ECS task role, EC2 instance role.
	AccessKeyID     string
	SecretAccessKey string
	// RoleARN, when set, assumes that role (via STS) on top of the base
	// credentials above.
	RoleARN string
	// RoleSessionName defaults to DefaultRoleSessionName.
	RoleSessionName string
	// MaxAttempts bounds SDK attempts per fetch, retries included; 0 keeps
	// the SDK default (3). Retries honour ctx.
	MaxAttempts int
	// HTTPClient overrides the SDK's default transport; nil keeps it.
	HTTPClient *http.Client
}

// Validate reports every problem with c.
func (c *Config) Validate() error {
	var problems []error
	bad := func(format string, args ...any) {
		problems = append(problems, fmt.Errorf("%w: source %q: %s", ErrInvalidConfig, c.Name, fmt.Sprintf(format, args...)))
	}
	if c.Name == "" {
		bad("empty name")
	}
	if c.Endpoint != "" {
		u, err := url.Parse(c.Endpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			bad("endpoint must be an absolute http(s) url without userinfo, query or fragment")
		}
	}
	if (c.AccessKeyID == "") != (c.SecretAccessKey == "") {
		bad("access key id and secret access key must be set together")
	}
	if c.MaxAttempts < 0 {
		bad("negative max attempts")
	}
	return errors.Join(problems...)
}

// Source fetches objects from one S3-compatible store. It is safe for
// concurrent use.
type Source struct {
	name   string
	client *s3.Client
}

var _ source.Source = (*Source)(nil)

// New builds the S3 client for cfg (not retained). It loads the SDK's shared configuration
// (which may read AWS_* variables and ~/.aws files) but makes no network
// call: credentials are resolved lazily on the first fetch.
func New(ctx context.Context, cfg *Config) (source.Source, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: nil config", ErrInvalidConfig)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	opts := []func(*config.LoadOptions) error{
		// R2, B2 and some self-hosted stores reject the CRC checksums newer
		// SDKs send by default; only send or validate them when an operation
		// requires it.
		config.WithRequestChecksumCalculation(aws.RequestChecksumCalculationWhenRequired),
		config.WithResponseChecksumValidation(aws.ResponseChecksumValidationWhenRequired),
	}
	if cfg.Region != "" {
		opts = append(opts, config.WithRegion(cfg.Region))
	}
	if cfg.AccessKeyID != "" {
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")))
	}
	if cfg.MaxAttempts > 0 {
		opts = append(opts, config.WithRetryMaxAttempts(cfg.MaxAttempts))
	}
	if cfg.HTTPClient != nil {
		opts = append(opts, config.WithHTTPClient(cfg.HTTPClient))
	}
	awsCfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("s3src: source %q: load aws config: %w", cfg.Name, err)
	}
	if awsCfg.Region == "" {
		if cfg.Endpoint == "" {
			return nil, fmt.Errorf("%w: source %q: no region configured or discoverable", ErrInvalidConfig, cfg.Name)
		}
		awsCfg.Region = DefaultRegion
	}
	if cfg.RoleARN != "" {
		session := cfg.RoleSessionName
		if session == "" {
			session = DefaultRoleSessionName
		}
		// STS uses its regional AWS endpoint, never the S3 Endpoint.
		stsClient := sts.NewFromConfig(awsCfg)
		awsCfg.Credentials = aws.NewCredentialsCache(stscreds.NewAssumeRoleProvider(stsClient, cfg.RoleARN,
			func(o *stscreds.AssumeRoleOptions) { o.RoleSessionName = session }))
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.ForcePathStyle
	})
	return &Source{name: cfg.Name, client: client}, nil
}

// Name implements source.Source.
func (s *Source) Name() string { return s.name }

// Type implements source.Source.
func (*Source) Type() string { return source.TypeS3 }

// Fetch implements source.Source with one GetObject. The body streams from
// the store; opt.MaxBytes (> 0) is enforced on Content-Length up front and on
// the bytes actually read. opt.Header and opt.CheckRedirect do not apply.
func (s *Source) Fetch(ctx context.Context, key source.Key, opt source.FetchOptions) (*source.Object, error) {
	if key.Bucket == "" || key.Path == "" {
		return nil, errs.Newf(errs.KindInternal, "s3src: incomplete key %s", source.RedactKey(key))
	}
	in := &s3.GetObjectInput{Bucket: aws.String(key.Bucket), Key: aws.String(key.Path)}
	if key.Version != "" {
		in.VersionId = aws.String(key.Version)
	}
	if opt.IfNoneMatch != "" {
		in.IfNoneMatch = aws.String(opt.IfNoneMatch)
	}
	if !opt.IfModifiedSince.IsZero() {
		in.IfModifiedSince = aws.Time(opt.IfModifiedSince)
	}
	out, err := s.client.GetObject(ctx, in)
	if err != nil {
		if obj, ok := notModified(err); ok {
			return obj, nil
		}
		return nil, classify(ctx, err, key)
	}
	size := aws.ToInt64(out.ContentLength)
	if out.ContentLength == nil {
		size = -1
	}
	if opt.MaxBytes > 0 && size > opt.MaxBytes {
		_ = out.Body.Close() // the error below is what matters
		return nil, tooLarge(key, opt.MaxBytes)
	}
	body := out.Body
	if opt.MaxBytes > 0 {
		body = &limitedBody{rc: out.Body, left: opt.MaxBytes, key: key, limit: opt.MaxBytes}
	}
	return &source.Object{
		Body:         body,
		Size:         size,
		ContentType:  aws.ToString(out.ContentType),
		ETag:         aws.ToString(out.ETag),
		LastModified: aws.ToTime(out.LastModified),
		CacheControl: aws.ToString(out.CacheControl),
		Expires:      parseHTTPTime(aws.ToString(out.ExpiresString)),
	}, nil
}

func parseHTTPTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := http.ParseTime(s)
	if err != nil {
		return time.Time{} // an unparsable Expires is treated as absent (RFC 9111 §5.3)
	}
	return t
}

func tooLarge(key source.Key, limit int64) error {
	return errs.Newf(errs.KindSourceTooLarge, "s3src: %s exceeds %d bytes", source.RedactKey(key), limit)
}

// limitedBody streams rc and fails with KindSourceTooLarge once more than
// limit bytes arrive, so a store that lies about (or omits) Content-Length
// cannot push past the limit.
type limitedBody struct {
	rc    io.ReadCloser
	left  int64
	limit int64
	key   source.Key
	over  bool
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.over {
		return 0, tooLarge(b.key, b.limit)
	}
	// Allow one byte past the limit so the overflow is observed instead of
	// silently truncating an object that is exactly one byte too big.
	if int64(len(p)) > b.left+1 {
		p = p[:b.left+1]
	}
	n, err := b.rc.Read(p)
	if int64(n) > b.left {
		b.over = true
		n = int(b.left)
		b.left = 0
		return n, tooLarge(b.key, b.limit)
	}
	b.left -= int64(n)
	return n, err
}

func (b *limitedBody) Close() error { return b.rc.Close() }
