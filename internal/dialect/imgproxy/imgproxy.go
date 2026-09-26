// Copyright 2017 Sergey Alexandrovich (imgproxy), Apache License 2.0.
// Portions of the grammar handling (path redenormalisation, source URL
// decoding, option argument rules) are reimplemented from imgproxy
// handlers/path.go and options/parser/*.go. Modifications copyright
// xtandard-image contributors, MIT. See NOTICE.

// Package imgproxy parses the imgproxy URL dialect (docs/URL-DIALECTS.md §3):
//
//	/%signature/%options.../plain/%source_url[@%extension]
//	/%signature/%options.../%base64url_source_url[.%extension]  (may be split with "/")
//	/%signature/%options.../enc/%aes_cbc_source[.%extension]    (may be split with "/")
//
// Options are "name" + separator + args joined by the separator (":" by
// default); a path segment that is exactly "-" starts a chained pipeline.
// Pipeline-scoped options (resize, crop, effects, watermark, ...) apply to
// the pipeline they appear in; request-scoped ones (format, quality,
// metadata, delivery, security limits) apply to the whole request wherever
// they appear, as in imgproxy (options.Options.Main).
//
// Semantics follow imgproxy v4 (options/parser) option by option: later
// occurrences override earlier ones argument by argument, empty arguments
// leave the previous value, and the source extension overrides format.
// Deliberate differences, all stricter or safer:
//
//   - A malformed argument is a 400 naming the field (imgproxy: 404).
//   - A non-empty boolean that strconv.ParseBool rejects is a 400 (imgproxy
//     logs and treats it as false); an empty boolean is false in both.
//   - NaN and infinite floats are rejected.
//   - Unknown options are recorded in Request.Ignored unless Strict is set
//     (ADR 0002); imgproxy always rejects them.
//   - Options scheduled for a later milestone are parsed into the request
//     and also recorded in Ignored until the milestone ships.
//   - A decoded source containing ASCII control characters is rejected.
//   - BaseURL is prepended to relative sources only (imgproxy prepends it
//     to anything not already starting with it).
//   - "sp" is accepted as an alias of skip_processing because
//     imgproxy-js-core emits it.
package imgproxy

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/security/crypt"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Name is the dialect identifier.
const Name = "imgproxy"

// DefaultArgumentsSeparator separates an option name from its arguments.
const DefaultArgumentsSeparator = ":"

// URLReplacement rewrites source URLs before use
// (XI_DIALECTS_IMGPROXY_URL_REPLACEMENTS, imgproxy IMGPROXY_URL_REPLACEMENTS).
type URLReplacement struct {
	Pattern     *regexp.Regexp
	Replacement string // regexp.ReplaceAllString syntax
}

// Options configures the parser. The zero value is a working unsigned,
// non-strict parser with ":" as the argument separator.
type Options struct {
	// Verifier checks the signature segment against the path after it. nil
	// means signing is off (every URL is sign.Unsigned, but a signature
	// segment that looks like an option is still rejected).
	Verifier sign.Verifier
	// Cipher decrypts enc/ sources. nil rejects them.
	Cipher *crypt.Cipher
	// ArgumentsSeparator separates option arguments; "" means ":".
	ArgumentsSeparator string
	// Strict rejects unknown options with 400 (XI_DIALECTS_STRICT).
	Strict bool
	// AllowSecurityOptions permits max_src_resolution, max_src_file_size,
	// max_animation_frames, max_animation_frame_resolution and
	// max_result_dimension in URLs (XI_SECURITY_ALLOW_SECURITY_OPTIONS);
	// otherwise they are 403.
	AllowSecurityOptions bool
	// BaseURL is prepended to relative sources (no scheme) that do not
	// already start with it (IMGPROXY_BASE_URL).
	BaseURL string
	// URLReplacements are applied, in order, before BaseURL.
	URLReplacements []URLReplacement
	// Base64URLIncludesFilename drops the last "/"-separated segment of a
	// base64 or enc/ source when there is more than one
	// (IMGPROXY_BASE64_URL_INCLUDES_FILENAME).
	Base64URLIncludesFilename bool
	// Now is the clock for expires; nil means time.Now.
	Now func() time.Time
}

// Parser is the imgproxy dialect. It is safe for concurrent use.
type Parser struct {
	o        Options
	sep      string
	verifier sign.Verifier
}

var _ dialect.Dialect = (*Parser)(nil)

// New validates o and returns a Parser; nil means the zero Options. The
// parser keeps a copy of o.
func New(opts *Options) (*Parser, error) {
	var o Options
	if opts != nil {
		o = *opts
	}
	sep := o.ArgumentsSeparator
	if sep == "" {
		sep = DefaultArgumentsSeparator
	}
	if strings.ContainsAny(sep, "/%?#") || sep == "-" {
		return nil, errors.New("imgproxy: arguments separator must not contain '/', '%', '?' or '#' or be '-'")
	}
	for i, r := range o.URLReplacements {
		if r.Pattern == nil {
			return nil, errors.New("imgproxy: url replacement " + strconv.Itoa(i) + " has no pattern")
		}
	}
	v := o.Verifier
	if v == nil {
		off, err := sign.NewImgproxyVerifier(sign.ImgproxyOptions{Size: sign.DefaultSignatureSize})
		if err != nil {
			return nil, err
		}
		v = off
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Parser{o: o, sep: sep, verifier: v}, nil
}

// Name implements dialect.Dialect.
func (*Parser) Name() string { return Name }

// Verifier implements dialect.Dialect.
func (p *Parser) Verifier() sign.Verifier { return p.verifier }

// Parse implements dialect.Dialect. Only in.Path is used.
func (p *Parser) Parse(in *dialect.Input) (*dialect.Result, error) {
	return p.ParsePath(in.Path)
}

// ParsePath parses an escaped request path such as
// "/unsafe/rs:fit:300:300/plain/https://example.com/a.jpg".
func (p *Parser) ParsePath(path string) (*dialect.Result, error) {
	path, _, _ = strings.Cut(path, "?")
	path = strings.TrimPrefix(path, "/")
	signature, rest, _ := strings.Cut(path, "/")
	if signature == "" || rest == "" {
		return nil, dialect.InvalidURL("missing signature or path")
	}
	rest = redenormalizePath(rest)
	if rest == "" {
		return nil, dialect.InvalidURL("missing path")
	}
	// imgproxy signs "/" + the redenormalised path; sign adds the slash.
	sigResult, err := p.verifier.Verify(signature, rest)
	if err != nil {
		return nil, err
	}

	st := newState(p)
	if err := st.parse(rest); err != nil {
		return nil, err
	}
	return &dialect.Result{Request: st.r, SourceString: st.r.Source.Raw, Signature: sigResult}, nil
}

// redenormalizePath undoes normalisation applied by some browsers and
// proxies (imgproxy handlers.RedenormalizePath): "%3A" in the options part
// becomes ":", and a collapsed "scheme:/" in a plain source becomes
// "scheme://" ("local:///" for local). A leading "/" is removed.
func redenormalizePath(path string) string {
	var options, plainURL string
	var hasPlain bool
	if after, ok := strings.CutPrefix(path, "plain/"); ok {
		plainURL, hasPlain = after, true
	} else {
		options, plainURL, hasPlain = strings.Cut(path, "/plain/")
	}
	if strings.Contains(options, "%3A") {
		options = strings.ReplaceAll(options, "%3A", ":")
	}
	if !hasPlain {
		return strings.TrimPrefix(options, "/")
	}
	if i := collapsedScheme(plainURL); i > 0 {
		sep := "://"
		if plainURL[:i] == "local" {
			sep = ":///"
		}
		plainURL = plainURL[:i] + sep + plainURL[i+2:]
	}
	return strings.TrimPrefix(options+"/plain/"+plainURL, "/")
}

// collapsedScheme returns the index of the ":" in a leading "scheme:/x"
// (x not "/"), or -1. It matches imgproxy's `^(\S+)\:/([^/])` including
// the greedy \S+: the last such ":/" before the first whitespace wins.
func collapsedScheme(s string) int {
	found := -1
	for i := 1; i+2 < len(s); i++ {
		if isRE2Space(s[i-1]) {
			break
		}
		if s[i] == ':' && s[i+1] == '/' && s[i+2] != '/' {
			found = i
		}
	}
	return found
}

// isRE2Space reports whether c is in RE2's \s class.
func isRE2Space(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r'
}

// state is the per-request parse state.
type state struct {
	p *Parser
	r *transform.Request
	// cur indexes the pipeline options currently apply to.
	cur int
	// bgAlpha holds background_alpha per pipeline (-1 = unset); it is
	// applied to Background once all options are known.
	bgAlpha []float64
	// args is scratch space for splitting option arguments.
	args []string
}

func newState(p *Parser) *state {
	return &state{
		p:       p,
		r:       &transform.Request{Dialect: Name, Pipelines: make([]transform.Pipeline, 1)},
		bgAlpha: []float64{-1},
		args:    make([]string, 0, 8),
	}
}

func (st *state) pipeline() *transform.Pipeline { return &st.r.Pipelines[st.cur] }

// parse consumes the options and the source of rest (the path after the
// signature, without a leading slash).
func (st *state) parse(rest string) error {
	if first, _, _ := strings.Cut(rest, "/"); isResizingType(first) {
		return dialect.InvalidURL("deprecated basic url format")
	}
	sawOption, chained := false, false
	for rest != "" {
		seg, tail, _ := strings.Cut(rest, "/")
		if seg == "-" {
			if !sawOption {
				return dialect.InvalidURL("empty pipeline")
			}
			if len(st.r.Pipelines) >= transform.MaxPipelines {
				return dialect.InvalidURL("too many chained pipelines")
			}
			st.r.Pipelines = append(st.r.Pipelines, transform.Pipeline{})
			st.bgAlpha = append(st.bgAlpha, -1)
			st.cur++
			sawOption, chained = false, true
			rest = tail
			continue
		}
		name, argStr, isOption := strings.Cut(seg, st.p.sep)
		if !isOption {
			break
		}
		st.args = splitArgs(st.args[:0], argStr, st.p.sep)
		if err := st.apply(name, st.args); err != nil {
			return err
		}
		sawOption = true
		rest = tail
	}
	if chained && !sawOption {
		return dialect.InvalidURL("empty pipeline")
	}

	ext, err := st.decodeSource(rest)
	if err != nil {
		return err
	}
	if ext != "" && st.r.Kind != transform.KindRaw {
		if err := st.format("extension", ext); err != nil {
			return err
		}
	}
	return st.finish()
}

// splitArgs appends the sep-separated fields of s to dst.
func splitArgs(dst []string, s, sep string) []string {
	for {
		a, b, ok := strings.Cut(s, sep)
		dst = append(dst, a)
		if !ok {
			return dst
		}
		s = b
	}
}

// finish applies cross-option rules once every option is known.
func (st *state) finish() error {
	for i := range st.r.Pipelines {
		pl := &st.r.Pipelines[i]
		if a := st.bgAlpha[i]; a >= 0 && pl.Background != nil {
			c := *pl.Background
			c.A = uint8(a*255 + 0.5)
			pl.Background = &c
		}
		// imgproxy applies a watermark only when its opacity is > 0.
		if len(pl.Watermarks) > 0 && pl.Watermarks[0].Opacity == 0 {
			pl.Watermarks = nil
		}
	}
	if e := st.r.Delivery.Expires; !e.IsZero() && e.Before(st.p.o.Now()) {
		return dialect.Expired()
	}
	return nil
}

// ignore records an option that is parsed but not applied.
func (st *state) ignore(name, reason string) {
	for _, ig := range st.r.Ignored {
		if ig.Option == name && ig.Reason == reason {
			return
		}
	}
	st.r.Ignored = append(st.r.Ignored, transform.Ignored{Option: name, Reason: reason})
}
