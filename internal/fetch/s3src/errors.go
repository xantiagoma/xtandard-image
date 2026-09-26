package s3src

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

// throttledPublic is the client-safe message of a throttled fetch.
const throttledPublic = "upstream busy, retry later"

// notModified turns the SDK's 304 error (GetObject with If-None-Match or
// If-Modified-Since) into an Object carrying the validators S3 sent back.
func notModified(err error) (*source.Object, bool) {
	re, ok := errors.AsType[*smithyhttp.ResponseError](err)
	if !ok || re.HTTPStatusCode() != http.StatusNotModified {
		return nil, false
	}
	obj := &source.Object{Size: -1, NotModified: true}
	if re.Response != nil && re.Response.Response != nil {
		h := re.Response.Header
		obj.ETag = h.Get("ETag")
		obj.CacheControl = h.Get("Cache-Control")
		obj.LastModified = parseHTTPTime(h.Get("Last-Modified"))
		obj.Expires = parseHTTPTime(h.Get("Expires"))
	}
	return obj, true
}

// classify maps a GetObject error to an errs kind (ARCHITECTURE §12):
// missing key/bucket/version → NotFound, AccessDenied → Forbidden, rate
// limiting → UpstreamError wrapping ErrThrottled, deadline → UpstreamTimeout,
// caller gone → Canceled, anything else → UpstreamError.
func classify(ctx context.Context, err error, key source.Key) error {
	where := "s3src: get " + source.RedactKey(key)
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return errs.Wrapf(errs.KindUpstreamTimeout, err, "%s", where)
		}
		return errs.Wrapf(errs.KindCanceled, err, "%s", where)
	}
	status := 0
	if re, ok := errors.AsType[*smithyhttp.ResponseError](err); ok {
		status = re.HTTPStatusCode()
	}
	code := ""
	if ae, ok := errors.AsType[smithy.APIError](err); ok {
		code = ae.ErrorCode()
	}
	switch {
	case isNotFound(code, status):
		return errs.Wrapf(errs.KindNotFound, err, "%s", where)
	case code == "AccessDenied" || code == "AllAccessDisabled":
		return errs.Wrapf(errs.KindForbidden, err, "%s", where)
	case isThrottle(code, status):
		return errs.WithPublic(errs.Wrapf(errs.KindUpstreamError, fmt.Errorf("%w: %w", ErrThrottled, err), "%s", where), throttledPublic)
	case isTimeout(err):
		return errs.Wrapf(errs.KindUpstreamTimeout, err, "%s", where)
	default:
		return errs.Wrapf(errs.KindUpstreamError, err, "%s", where)
	}
}

func isNotFound(code string, status int) bool {
	switch code {
	case "NoSuchKey", "NotFound", "NoSuchBucket", "NoSuchVersion":
		return true
	default:
		return status == http.StatusNotFound
	}
}

func isThrottle(code string, status int) bool {
	switch code {
	case "SlowDown", "Throttling", "ThrottlingException", "ThrottledException", "RequestThrottled",
		"RequestThrottledException", "RequestLimitExceeded", "TooManyRequests", "TooManyRequestsException",
		"ServiceUnavailable":
		return true
	default:
		return status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable
	}
}

func isTimeout(err error) bool {
	ne, ok := errors.AsType[net.Error](err)
	return ok && ne.Timeout()
}
