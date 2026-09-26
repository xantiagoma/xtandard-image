// Package awssih parses the URL dialect of AWS "Dynamic Image
// Transformation for Amazon CloudFront", formerly Serverless Image Handler
// (SIH; docs/URL-DIALECTS.md §13, reference
// .refs/aws-image-handler/source/image-handler, v8.x). SIH has three
// request types, chosen from the path after the mount:
//
//   - Default: the path is base64 (standard or URL-safe, padding
//     optional) of a JSON object {bucket, key, edits, outputFormat, effort,
//     headers}. bucket selects a named source through Options.Buckets
//     (SIH's SOURCE_BUCKETS allowlist; absent means the mount's source),
//     key is the object key relative to it, and edits are sharp operations
//     applied in key order. This package implements it completely.
//   - Thumbor: any other path is a thumbor URL without the signature
//     segment. It is forwarded to Options.Thumbor (the thumbor dialect)
//     with "/unsafe" prepended, after an "s3:<bucket>" segment has been
//     resolved to a source; SIH's own signature still applies.
//   - Custom: with Options.Rewrite set (SIH's REWRITE_MATCH_PATTERN and
//     REWRITE_SUBSTITUTION), non-JSON paths are rewritten first and then
//     forwarded like thumbor paths.
//
// With Options.QueryParams, SIH's query-param mapper also applies to every
// request type: format, fit, width, height, rotate, flip, flop and
// grayscale/greyscale are merged into the edits the way SIH merges them
// ({...edits, ...queryEdits}: a width, height or fit parameter replaces the
// whole resize edit).
//
// Signing: the signature query parameter is hex(HMAC-SHA256(secret,
// path[?query])) as built by sign.AWSSIHMessage over the decoded path after
// the mount; expires=YYYYMMDDTHHmmssZ limits the URL's lifetime and sets
// Delivery.Expires. Both are checked before the path is decoded, as in
// SIH.
//
// Deliberate differences from SIH are listed in docs/URL-DIALECTS.md §13:
// expired URLs use the common 404 (SIH: 400), invalid arguments that sharp
// would reject at processing time (SIH: 500) are 400 here, AUTO_WEBP is the
// server's Accept negotiation, and the S3 Object Lambda "ol-" parameters
// are not recognised.
package awssih

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Name is the dialect identifier.
const Name = "awssih"

// Reasons recorded in Request.Ignored.
const (
	reasonUnknown      = "unknown option"
	reasonUnsupported  = "not supported"
	reasonApproximated = "approximated"
	reasonM3           = "takes effect in M3"
	reasonHeader       = "response header not supported"
	reasonHeaderDenied = "header denied"
	reasonPartial      = "partially applied"
	reasonQueryOff     = "query parameter mapping disabled"
	reasonTopLevel     = "unknown request field"
)

// maxQuoted bounds attacker-controlled names copied into Ignored.
const maxQuoted = 64

// paramExpires is SIH's expiry parameter.
const paramExpires = "expires"

// expiresLayout is SIH's YYYYMMDDTHHmmssZ (dayjs strict parsing).
const expiresLayout = "20060102T150405Z"

// ErrBucketNotAllowed is wrapped by the 403 for a bucket that is not in
// Options.Buckets (SIH ImageBucket::CannotAccessBucket).
var ErrBucketNotAllowed = errors.New("awssih: bucket not allowed")

func errBucket() error {
	return &errs.Error{Kind: errs.KindForbidden, Public: "bucket not allowed", Err: ErrBucketNotAllowed}
}

// Options configures the parser. The zero value is an unsigned, non-strict
// parser that serves only base64 JSON requests from the mount's source.
type Options struct {
	// Buckets maps SIH bucket names to named sources (SOURCE_BUCKETS). A
	// request or overlay naming a bucket that is not listed is a 403. A
	// request without a bucket uses the mount's source.
	Buckets map[string]string
	// Verifier checks the signature parameter (sign.NewAWSSIHVerifier);
	// nil means signing is off.
	Verifier sign.Verifier
	// Strict rejects unknown and unsupported edits with 400
	// (XI_DIALECTS_STRICT).
	Strict bool
	// QueryParams enables SIH's query-param mapper (format, fit, width,
	// height, rotate, flip, flop, grayscale, greyscale).
	QueryParams bool
	// Thumbor handles non-JSON paths (SIH's thumbor request type); nil
	// makes them a 400. It must accept "/unsafe/" URLs.
	Thumbor dialect.Dialect
	// Rewrite, when set, rewrites non-JSON paths before they are forwarded
	// to Thumbor (SIH's custom request type).
	Rewrite *Rewrite
	// Now is the clock for expires; nil means time.Now.
	Now func() time.Time
}

// Parser is the SIH dialect. It is safe for concurrent use.
type Parser struct {
	o Options
}

var _ dialect.Dialect = (*Parser)(nil)

// New returns a Parser; nil means the zero Options.
func New(opts *Options) (*Parser, error) {
	var o Options
	if opts != nil {
		o = *opts
	}
	buckets := make(map[string]string, len(o.Buckets))
	for b, s := range o.Buckets {
		if b == "" || s == "" {
			return nil, errors.New("awssih: bucket and source names must be non-empty")
		}
		buckets[b] = s
	}
	o.Buckets = buckets
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Parser{o: o}, nil
}

// Name implements dialect.Dialect.
func (*Parser) Name() string { return Name }

// Verifier implements dialect.Dialect.
func (p *Parser) Verifier() sign.Verifier { return p.o.Verifier }

// param is one decoded query parameter.
type param struct{ key, value string }

// Parse implements dialect.Dialect. It uses in.Path, in.RawQuery and
// in.Mount.Source.
func (p *Parser) Parse(in *dialect.Input) (*dialect.Result, error) {
	escaped := in.Path
	if escaped == "" || escaped[0] != '/' {
		escaped = "/" + escaped
	}
	decoded, err := url.PathUnescape(escaped)
	if err != nil {
		return nil, dialect.InvalidURL("invalid path escaping")
	}
	params, err := parseQuery(in.RawQuery)
	if err != nil {
		return nil, err
	}
	sigResult, err := p.verify(decoded, in.RawQuery)
	if err != nil {
		return nil, err
	}
	expires, err := p.expires(params)
	if err != nil {
		return nil, err
	}

	var res *dialect.Result
	if body, ok := decodeRequest(decoded[1:]); ok {
		res, err = p.parseJSON(body, in.Mount.Source, params)
	} else {
		res, err = p.forward(in, escaped, params)
	}
	if err != nil {
		return nil, err
	}
	res.Signature = sigResult
	res.Request.Delivery.Expires = expires
	return res, nil
}

// parseQuery decodes rawQuery; for a repeated key the last value wins, as
// in API Gateway's queryStringParameters.
func parseQuery(rawQuery string) ([]param, error) {
	var params []param
	index := map[string]int{}
	for part := range strings.SplitSeq(rawQuery, "&") {
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		key, err := url.QueryUnescape(k)
		if err != nil {
			return nil, dialect.InvalidURL("invalid query escaping")
		}
		value, err := url.QueryUnescape(v)
		if err != nil {
			return nil, dialect.InvalidURL("invalid query escaping")
		}
		if i, ok := index[key]; ok {
			params[i].value = value
			continue
		}
		index[key] = len(params)
		params = append(params, param{key, value})
	}
	return params, nil
}

func (p *Parser) verify(path, rawQuery string) (sign.Result, error) {
	sig, msg, err := sign.AWSSIHMessage(path, rawQuery)
	if err != nil {
		return sign.Unsigned, err
	}
	if p.o.Verifier == nil {
		return sign.Unsigned, nil
	}
	return p.o.Verifier.Verify(sig, msg)
}

// expires checks SIH's expires parameter.
func (p *Parser) expires(params []param) (time.Time, error) {
	for _, kv := range params {
		if kv.key != paramExpires {
			continue
		}
		t, err := time.Parse(expiresLayout, kv.value)
		if err != nil || len(kv.value) != len(expiresLayout) {
			return time.Time{}, dialect.InvalidArgument(paramExpires, kv.value, "a UTC time YYYYMMDDTHHmmssZ")
		}
		if t.Before(p.o.Now()) {
			return time.Time{}, dialect.Expired()
		}
		return t, nil
	}
	return time.Time{}, nil
}

// decodeRequest decodes SIH's base64 request path the way Node's
// Buffer.from(s, "base64") does (standard and URL-safe alphabets, padding
// optional, anything after the first "=" ignored) and reports whether it
// is a JSON object. Other characters make the path a non-JSON (thumbor)
// path.
func decodeRequest(s string) ([]byte, bool) {
	if i := strings.IndexByte(s, '='); i >= 0 {
		s = s[:i]
	}
	if s == "" || len(s) > maxRequestLen {
		return nil, false
	}
	buf := make([]byte, len(s))
	for i := range len(s) {
		c := s[i]
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9':
			buf[i] = c
		case c == '+' || c == '-':
			buf[i] = '+'
		case c == '/' || c == '_':
			buf[i] = '/'
		default:
			return nil, false
		}
	}
	if len(buf)%4 == 1 { // Node drops a dangling sextet
		buf = buf[:len(buf)-1]
	}
	out, err := base64.RawStdEncoding.AppendDecode(nil, buf)
	if err != nil {
		return nil, false
	}
	if kind(out) != kindObject || !json.Valid(out) {
		return nil, false
	}
	return out, true
}

// maxRequestLen bounds the base64 request (URLs are limited far below).
const maxRequestLen = 64 << 10

// Top-level members of the request JSON (lib/interfaces.ts
// DefaultImageRequest).
const (
	fieldBucket       = "bucket"
	fieldKey          = "key"
	fieldEdits        = "edits"
	fieldOutputFormat = "outputFormat"
	fieldEffort       = "effort"
	fieldHeaders      = "headers"
)

// parseJSON builds the request of a Default (base64 JSON) request.
func (p *Parser) parseJSON(body []byte, mountSource string, params []param) (*dialect.Result, error) {
	if depth(body) > maxDepth {
		return nil, dialect.InvalidURL("request json nested too deeply")
	}
	top, err := objectFields(body)
	if err != nil {
		return nil, dialect.InvalidURL("invalid request json")
	}
	r := &transform.Request{Dialect: Name, Pipelines: make([]transform.Pipeline, 1)}
	b := newBuilder(p, r)

	var bucket *string
	if v, ok := top.lookup(fieldBucket); ok && kind(v) != kindNull {
		s, ok := asString(v)
		if !ok {
			return nil, invalidJSON(fieldBucket, "a string")
		}
		bucket = &s
	}
	src, ok := p.requestSource(bucket, mountSource)
	if !ok {
		return nil, errBucket()
	}
	keyRaw, _ := top.lookup(fieldKey)
	key, ok := asString(keyRaw)
	if !ok || key == "" {
		return nil, invalidJSON(fieldKey, "a non-empty string")
	}
	spec, err := sourceSpec(fieldKey, src, key)
	if err != nil {
		return nil, err
	}
	r.Source = spec

	var edits fields
	if v, ok := top.lookup(fieldEdits); ok && kind(v) != kindNull {
		if edits, err = objectFields(v); err != nil {
			return nil, invalidJSON(fieldEdits, "an object")
		}
	}
	if edits, err = p.mergeQueryEdits(b, edits, params); err != nil {
		return nil, err
	}
	for _, e := range edits {
		if err := b.applyEdit(e.key, e.raw); err != nil {
			return nil, err
		}
	}
	b.finishPipelines()

	if v, ok := top.lookup(fieldOutputFormat); ok && kind(v) != kindNull {
		s, ok := asString(v)
		if !ok {
			return nil, invalidJSON(fieldOutputFormat, formatExpected)
		}
		if b.outFormat, err = parseFormat(fieldOutputFormat, s); err != nil {
			return nil, err
		}
	}
	effort, _ := top.lookup(fieldEffort)
	b.finishFormat(effort)
	if v, ok := top.lookup(fieldHeaders); ok {
		if err := b.applyHeaders(v); err != nil {
			return nil, err
		}
	}
	for _, f := range top {
		switch f.key {
		case fieldBucket, fieldKey, fieldEdits, fieldOutputFormat, fieldEffort, fieldHeaders:
		default:
			b.ignore(truncate(f.key), reasonTopLevel)
		}
	}
	return &dialect.Result{Request: r, SourceString: r.Source.Raw}, nil
}

// requestSource resolves the request bucket: listed buckets map to their
// source, no bucket means the mount's source.
func (p *Parser) requestSource(bucket *string, mountSource string) (string, bool) {
	if bucket == nil {
		return mountSource, mountSource != ""
	}
	s, ok := p.o.Buckets[*bucket]
	return s, ok
}

// overlaySource resolves an overlay bucket; SIH requires it to be listed.
func (p *Parser) overlaySource(bucket *string) (string, bool) {
	if bucket == nil {
		return "", false
	}
	s, ok := p.o.Buckets[*bucket]
	return s, ok
}

// sourceSpec returns the relative source for key in the named source. The
// key is an object key, not a URL: characters the source resolver would
// read as URL syntax (%, ?, #, and : which could look like a scheme) are
// escaped, so the key always stays relative to the bucket's source.
func sourceSpec(field, source, key string) (transform.SourceSpec, error) {
	if !imgproxy.PlausibleSource(key) || strings.Trim(key, "/") == "" {
		return transform.SourceSpec{}, invalidJSON(field, "an object key")
	}
	raw := keyEscaper.Replace(key)
	return transform.SourceSpec{Raw: raw, Encoding: transform.SourceRelative, Mount: source}, nil
}

var keyEscaper = strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23", ":", "%3A")

// finishPipelines moves the canvas extension after a post-resize extract:
// sharp extends after extracting.
func (b *builder) finishPipelines() {
	if b.post == nil {
		return
	}
	b.post.Padding, b.pre.Padding = b.pre.Padding, nil
}
