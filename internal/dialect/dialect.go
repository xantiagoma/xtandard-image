// Package dialect defines the contract between URL dialect parsers and the
// server (docs/ARCHITECTURE.md §5, docs/URL-DIALECTS.md).
//
// A Dialect turns the part of a request URL after its mount prefix into a
// transform.Request. Parsing is pure: no I/O, no clock other than an injected
// one, no global state. Each dialect also authenticates the URL with its own
// signature scheme (package security/sign), because only the dialect knows
// which bytes are signed; the outcome is reported in Result.Signature and the
// server applies XI_SECURITY_UNSIGNED_POLICY to unsigned requests.
//
// Errors returned by Parse are *errs.Error values: errs.KindInvalidURL (400)
// for malformed URLs and option arguments, errs.KindUnauthorized (403) for
// signature and encrypted-source failures, errs.KindForbidden (403) for
// options the configuration does not allow, and errs.KindExpired (404) for
// expired URLs. Argument errors additionally wrap an *ArgumentError naming
// the offending field.
package dialect

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Dialect parses one URL grammar. Implementations are safe for concurrent
// use.
type Dialect interface {
	// Name is the dialect identifier used in config, logs and metrics
	// ("imgproxy", "imgix", ...).
	Name() string
	// Parse turns the part of the URL after the mount into a request and
	// verifies its signature. It must not do I/O.
	Parse(in *Input) (*Result, error)
	// Verifier returns the signature scheme Parse applies, or nil if the
	// dialect is unsigned. Parse already calls it; the accessor exists for
	// the sign CLI, the playground and tests.
	Verifier() sign.Verifier
}

// Input is the request as seen by a dialect.
type Input struct {
	Path     string // escaped path after the mount prefix (r.URL.EscapedPath() minus the prefix)
	Query    url.Values
	RawQuery string
	Host     string
	Header   http.Header // Accept and client hints; never used for cache-key decisions except negotiation
	Mount    Mount
}

// Mount is the dialect binding that matched the request.
type Mount struct {
	Name   string // mount name for logs; "" for the default mount
	Prefix string // path prefix that was stripped from Input.Path
	Source string // named source relative keys resolve against; "" = none
}

// Result is a parsed, authenticated URL.
type Result struct {
	// Request holds what the URL says, with presets expanded in place by
	// the dialect (URL order decides overrides); the server applies
	// defaults, then validates.
	Request *transform.Request
	// SourceString is the source reference after all URL-level decoding
	// (percent-decoding, base64, decryption, base URL, replacements). It
	// equals Request.Source.Raw and is what the source resolver maps to a
	// source.Ref.
	SourceString string
	// Signature is the outcome of signature verification.
	Signature sign.Result
	// PresetVideo is, for an unsigned video transformation that names at
	// least one video preset, the VideoOp the same URL produces with every
	// option but its preset references left out (the default preset
	// included; no format or extension). The restricted unsigned video
	// policy (XI_VIDEO_TRANSFORM_UNSIGNED_POLICY) compares it with
	// Request.Video. nil otherwise, which that policy refuses.
	PresetVideo *transform.VideoOp
}

// Sentinel causes wrapped by dialect errors; match them with errors.Is.
var (
	// ErrMalformedURL: the path does not follow the dialect grammar.
	ErrMalformedURL = errors.New("dialect: malformed url")
	// ErrUnknownOption: an option name the dialect does not know (strict
	// mode only; otherwise it is recorded in Request.Ignored).
	ErrUnknownOption = errors.New("dialect: unknown option")
	// ErrOptionNotAllowed: a known option the configuration forbids.
	ErrOptionNotAllowed = errors.New("dialect: option not allowed")
	// ErrExpired: the URL carries an expiry in the past.
	ErrExpired = errors.New("dialect: url expired")
)

// maxQuoted bounds attacker-controlled text copied into internal error
// messages.
const maxQuoted = 64

// ArgumentError reports an invalid option argument. Field is a stable,
// client-safe name such as "width" or "gravity.x_offset".
type ArgumentError struct {
	Field    string
	Value    string // the rejected input, truncated; internal only
	Expected string // client-safe description of valid values
}

func (e *ArgumentError) Error() string {
	return "dialect: invalid " + e.Field + " " + strconv.Quote(e.Value) + " (expected " + e.Expected + ")"
}

func truncate(s string) string {
	if len(s) > maxQuoted {
		return s[:maxQuoted]
	}
	return s
}

// InvalidArgument returns a 400 error for an option argument. The public
// message names field and the expectation but never echoes value.
func InvalidArgument(field, value, expected string) error {
	return &errs.Error{
		Kind:   errs.KindInvalidURL,
		Public: "invalid " + field + ": expected " + expected,
		Err:    &ArgumentError{Field: field, Value: truncate(value), Expected: expected},
	}
}

// InvalidURL returns a 400 error for a grammar violation. reason must be a
// static, client-safe phrase.
func InvalidURL(reason string) error {
	return &errs.Error{
		Kind:   errs.KindInvalidURL,
		Msg:    reason,
		Public: "invalid url: " + reason,
		Err:    ErrMalformedURL,
	}
}

// UnknownOption returns the strict-mode 400 error for an unknown option.
func UnknownOption(name string) error {
	return &errs.Error{
		Kind:   errs.KindInvalidURL,
		Msg:    "option " + strconv.Quote(truncate(name)),
		Public: "invalid url: unknown option",
		Err:    ErrUnknownOption,
	}
}

// OptionNotAllowed returns a 403 error for an option the configuration
// forbids. name is the canonical option name (client-safe).
func OptionNotAllowed(name string) error {
	return &errs.Error{
		Kind:   errs.KindForbidden,
		Msg:    "option " + name,
		Public: "option not allowed: " + name,
		Err:    ErrOptionNotAllowed,
	}
}

// Expired returns the error for a URL whose expiry has passed.
func Expired() error {
	return &errs.Error{Kind: errs.KindExpired, Err: ErrExpired}
}
