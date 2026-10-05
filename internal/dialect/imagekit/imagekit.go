// Package imagekit parses the ImageKit URL dialect (docs/URL-DIALECTS.md
// §7, https://imagekit.io/docs/transformations), mounted at
// /imagekit/<mount>/ or host-bound:
//
//	/tr:w-400,h-300,fo-auto/folder/a.jpg
//	/folder/tr:w-400/a.jpg
//	/folder/a.jpg?tr=w-400,h-300:rt-90&ik-t=1767225600&ik-s=<hex>
//
// The path after the mount (and after Options.EndpointPath, the part of
// the ImageKit URL endpoint that lives in the path) is the file path,
// resolved relative to the mount's bound source. Transformations come
// either from a "tr:" path segment, which may appear anywhere before the
// file name, or from the tr query parameter; when both are present the
// path wins and the query is recorded as ignored (400 in strict mode), as
// the SDKs never emit both and ImageKit documents neither order. A
// transformation string is a ":"-separated chain of steps (each one
// chained pipeline) of ","-separated key-value parameters ("w-300",
// "e-sharpen-10", "l-image,i-logo.png,…,l-end").
//
// Mapping (see step.go and layer.go for every parameter):
//
//   - c-maintain_ratio (the default when a size is given) is imgproxy fill
//     with enlarge; c-force, c-at_max, c-at_max_enlarge and c-at_least are
//     force, fit, fit+enlarge and outside.
//   - cm-pad_resize is fit+extend (fo- places the image), cm-extract a
//     pre-resize crop (fo-, x/y top-left or xc/yc centre), cm-pad_extract
//     the crop extended to the requested size.
//   - fo-auto is smart gravity; fo-face and fo-<object> are face and object
//     gravity, with z- zooming on them (WP-72).
//   - l-image overlays become basic watermarks from the mount's source and
//     l-text overlays text watermarks, both taking effect in M3.
//   - n-<name> named transformations expand the preset <name>.
//   - <video>/ik-thumbnail.jpg is a thumbnail of the video <video> (a JPEG
//     unless f- says otherwise); so-<seconds> picks the frame (imgproxy
//     video_thumbnail_second), else the configured selection applies.
//   - <gif>/ik-gif-video.mp4 is the animated image <gif> as an MP4 video
//     (WP-76; H.264 in the gpl image, AV1 in full), like f-mp4.
//   - A file with a video extension (mp4, webm, mov, …) and <video>/
//     ik-video.mp4 are video transformations (KindVideo, video.go): w-, h-,
//     ar-, c-, cm-, fo-, x-, y-, bg-, so-, eo-, du-, f-, vc-, ac-, q-, fps-,
//     rt-, dpr-; layers wait for WP-124 and ik-master manifests for WP-126.
//
// Signing: ik-s = hex(HMAC-SHA1(private key, url-relative-to-endpoint ‖
// ik-t)) as in imagekit-nodejs (sign.ImageKitMessage). An ik-t in the past
// is 404 (errs.KindExpired). Unknown parameters are recorded in
// Request.Ignored, and in strict mode unknown and unsupported parameters
// are 400.
//
// Other query parameters (the SDK's queryParameters, for example ?v=2) are
// kept as the cache buster, so they vary the result cache key as they vary
// ImageKit's CDN cache.
package imagekit

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/preset"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Name is the dialect identifier.
const Name = "imagekit"

// Query parameters with a fixed meaning.
const (
	paramTr         = "tr"
	paramAttachment = "ik-attachment"
	// videoThumbnail is the path suffix that asks for a video's thumbnail
	// (<video>/ik-thumbnail.jpg; so- picks the frame).
	videoThumbnail = "ik-thumbnail.jpg"
	// gifVideo is the path suffix that asks for an animated image as an
	// MP4 video (<gif>/ik-gif-video.mp4).
	gifVideo = "ik-gif-video.mp4"
	// videoMP4 is the path suffix that asks for a video as MP4
	// (<video>/ik-video.mp4, WP-125).
	videoMP4 = "ik-video.mp4"
	// videoMaster starts the adaptive-streaming manifest suffixes
	// (ik-master.m3u8, ik-master.mpd; WP-126).
	videoMaster = "ik-master."
)

// Reasons recorded in Request.Ignored.
const (
	reasonUnknown        = "unknown option"
	reasonUnsupported    = "not supported"
	reasonNotImplemented = "not implemented"
	reasonM3             = "takes effect in M3"
	reasonExpression     = "arithmetic expressions not supported"
	reasonPathWins       = "path transformation takes precedence"
	reasonNotApplied     = "not applicable here"
	reasonNoPresets      = "named transformations not configured"
	// reasonApproximated: applied with a documented difference.
	reasonApproximated = "approximated"
	// reasonUnsupportedObject: an fo- object the COCO object model does
	// not detect (the smart crop is used).
	reasonUnsupportedObject = "unsupported object (smart crop)"
)

// Options configures the parser. The zero value is a working unsigned,
// non-strict parser.
type Options struct {
	// Verifier checks ik-s (sign.NewImageKitVerifier with the ImageKit
	// private keys). nil means signing is off: ik-s is not checked, ik-t
	// still expires the URL.
	Verifier sign.Verifier
	// EndpointPath is the path part of the ImageKit URL endpoint below the
	// mount prefix, for host-bound mounts whose URLs keep the endpoint id
	// (https://ik.example.com/<id>/…): "/<id>". Requests outside it are
	// 400; it is excluded from the source key and from the signed message.
	EndpointPath string
	// Presets expands n-<name> named transformations. nil records the
	// names in Delivery.Presets unexpanded.
	Presets *preset.Store
	// Strict rejects unknown and unsupported parameters with 400
	// (XI_DIALECTS_STRICT).
	Strict bool
	// Now is the clock for ik-t; nil means time.Now.
	Now func() time.Time
}

// Parser is the ImageKit dialect. It is safe for concurrent use.
type Parser struct {
	o        Options
	endpoint string // "" or "/seg[/seg…]"
	opt      *imgproxy.Parser
}

var _ dialect.Dialect = (*Parser)(nil)

// New returns a Parser; nil means the zero Options.
func New(opts *Options) (*Parser, error) {
	var o Options
	if opts != nil {
		o = *opts
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	ep := strings.Trim(o.EndpointPath, "/")
	if strings.ContainsAny(ep, "?#") || strings.Contains(ep, "//") {
		return nil, errors.New("imagekit: endpoint path must be a plain path")
	}
	if ep != "" {
		ep = "/" + ep
	}
	ip, err := imgproxy.New(&imgproxy.Options{Strict: o.Strict, Now: o.Now, Presets: o.Presets})
	if err != nil {
		return nil, err
	}
	return &Parser{o: o, endpoint: ep, opt: ip}, nil
}

// Name implements dialect.Dialect.
func (*Parser) Name() string { return Name }

// Verifier implements dialect.Dialect.
func (p *Parser) Verifier() sign.Verifier { return p.o.Verifier }

// Parse implements dialect.Dialect. It uses in.Path, in.RawQuery and
// in.Mount.Source (for overlay and default-image paths).
func (p *Parser) Parse(in *dialect.Input) (*dialect.Result, error) {
	return p.parse(in.Path, in.RawQuery, in.Mount.Source)
}

// ParseURL parses the escaped path after the mount prefix and the raw
// query, for example ("/tr:w-300/a.jpg", "") or ("/a.jpg", "tr=w-300").
// Overlay and default-image paths get no mount source.
func (p *Parser) ParseURL(path, rawQuery string) (*dialect.Result, error) {
	return p.parse(path, rawQuery, "")
}

func (p *Parser) parse(path, rawQuery, mountSource string) (*dialect.Result, error) {
	rel, err := p.stripEndpoint(path)
	if err != nil {
		return nil, err
	}
	q, err := parseQuery(rawQuery)
	if err != nil {
		return nil, err
	}
	sigResult, expires, err := p.verify(rel, rawQuery)
	if err != nil {
		return nil, err
	}

	segs := strings.Split(strings.TrimPrefix(rel, "/"), "/")
	trIdx := -1
	for i := range len(segs) - 1 {
		if _, ok := trSegment(segs[i]); ok {
			if trIdx >= 0 {
				return nil, dialect.InvalidURL("multiple tr path segments")
			}
			trIdx = i
		}
	}

	b := p.opt.NewBuilder(Name)
	ps := &parseState{p: p, b: b, mountSource: mountSource}
	var tr string
	switch {
	case trIdx >= 0:
		raw, _ := trSegment(segs[trIdx])
		if tr, err = url.PathUnescape(raw); err != nil {
			return nil, dialect.InvalidURL("invalid path escaping")
		}
		if len(q.tr) > 0 {
			if p.o.Strict {
				return nil, dialect.InvalidURL("tr in both path and query")
			}
			b.Ignore(paramTr, reasonPathWins)
		}
		segs = append(segs[:trIdx], segs[trIdx+1:]...)
	case len(q.tr) == 1:
		tr = q.tr[0]
	case len(q.tr) > 1:
		return nil, dialect.InvalidURL("multiple tr parameters")
	}

	last := segs[len(segs)-1]
	thumb := len(segs) > 1 && last == videoThumbnail
	gif := len(segs) > 1 && last == gifVideo
	ikVideo := len(segs) > 1 && last == videoMP4
	if len(segs) > 1 && strings.HasPrefix(last, videoMaster) {
		return nil, dialect.InvalidURL("adaptive bitrate streaming (ik-master) is not supported yet")
	}
	if thumb || gif || ikVideo {
		segs = segs[:len(segs)-1]
	}
	key, err := sourceKey(segs)
	if err != nil {
		return nil, err
	}
	if ikVideo || (!thumb && !gif && isVideoFile(key)) {
		// A video transformation (video.go).
		if err := ps.video(tr, ikVideo); err != nil {
			return nil, err
		}
		r := b.Request()
		if err := applyQuery(r, &q, expires); err != nil {
			return nil, err
		}
		r.Source = transform.SourceSpec{Raw: key, Encoding: transform.SourceRelative}
		return &dialect.Result{Request: r, SourceString: key, Signature: sigResult}, nil
	}
	if tr != "" {
		if err := ps.transformations(tr); err != nil {
			return nil, err
		}
	}
	r := b.Request()
	if thumb && r.Output.Format == transform.FormatUnset {
		// A video's thumbnail is a JPEG unless f- says otherwise.
		r.Output.Format = transform.FormatJPEG
	}
	if gif {
		r.Output.Format = transform.FormatMP4
	}
	if err := applyQuery(r, &q, expires); err != nil {
		return nil, err
	}
	if err := b.Finish(); err != nil {
		return nil, err
	}
	r.Source = transform.SourceSpec{Raw: key, Encoding: transform.SourceRelative}
	return &dialect.Result{Request: r, SourceString: key, Signature: sigResult}, nil
}

// stripEndpoint removes Options.EndpointPath from path.
func (p *Parser) stripEndpoint(path string) (string, error) {
	if p.endpoint == "" {
		return path, nil
	}
	rest, ok := strings.CutPrefix(path, p.endpoint)
	if !ok || (rest != "" && rest[0] != '/') {
		return "", dialect.InvalidURL("path outside the url endpoint")
	}
	return rest, nil
}

// trSegment reports whether seg is a "tr:" path segment (":" possibly
// percent-encoded) and returns the transformation string after it, still
// escaped.
func trSegment(seg string) (string, bool) {
	if rest, ok := strings.CutPrefix(seg, "tr:"); ok {
		return rest, true
	}
	if len(seg) >= 5 && seg[:2] == "tr" && strings.EqualFold(seg[2:5], "%3a") {
		return seg[5:], true
	}
	return "", false
}

// query holds the query parameters the dialect interprets.
type query struct {
	tr         []string // decoded tr values
	attachment string
	hasAttach  bool
	rest       []string // other raw parameters, in order (cache buster)
}

// parseQuery splits rawQuery; a malformed escape is a 400.
func parseQuery(rawQuery string) (query, error) {
	var q query
	for part := range strings.SplitSeq(rawQuery, "&") {
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		key, err := url.QueryUnescape(k)
		if err != nil {
			return q, dialect.InvalidURL("invalid query escaping")
		}
		switch key {
		case sign.ImageKitSignatureParam, sign.ImageKitExpiresParam:
			continue
		case paramTr, paramAttachment:
			value, err := url.QueryUnescape(v)
			if err != nil {
				return q, dialect.InvalidURL("invalid query escaping")
			}
			if key == paramTr {
				q.tr = append(q.tr, value)
			} else {
				q.attachment, q.hasAttach = value, true
			}
		default:
			q.rest = append(q.rest, part)
		}
	}
	return q, nil
}

// verify checks ik-s and returns the ik-t value as written ("" if absent).
func (p *Parser) verify(rel, rawQuery string) (sign.Result, string, error) {
	sig, expires, msg, err := sign.ImageKitMessage(rel, rawQuery)
	if err != nil {
		return sign.Unsigned, "", err
	}
	if p.o.Verifier == nil {
		return sign.Unsigned, expires, nil
	}
	res, err := p.o.Verifier.Verify(sig, msg)
	return res, expires, err
}

// applyQuery applies ik-t, ik-attachment and the cache buster.
func applyQuery(r *transform.Request, q *query, expires string) error {
	if expires != "" && expires != sign.ImageKitNoExpiry {
		ts, err := strconv.ParseInt(expires, 10, 64)
		if err != nil || ts < 0 {
			return dialect.InvalidArgument(sign.ImageKitExpiresParam, expires, "unix timestamp")
		}
		r.Delivery.Expires = time.Time{}
		if ts > 0 {
			r.Delivery.Expires = time.Unix(ts, 0).UTC()
		}
	}
	if q.hasAttach {
		on, err := imgproxy.ParseBool(paramAttachment, q.attachment)
		if err != nil {
			return err
		}
		r.Delivery.Attachment = &on
	}
	if len(q.rest) > 0 {
		r.Delivery.CacheBuster = strings.Join(q.rest, "&")
	}
	return nil
}

// sourceKey joins the file path segments into an escaped key relative to
// the mount's source. A ":" in the first segment is escaped so the key
// can never read as a scheme ("https:/…").
func sourceKey(segs []string) (string, error) {
	key := strings.Join(segs, "/")
	decoded, err := url.PathUnescape(key)
	if err != nil {
		return "", dialect.InvalidURL("invalid path escaping")
	}
	if strings.Trim(decoded, "/") == "" {
		return "", dialect.InvalidURL("missing source path")
	}
	if !imgproxy.PlausibleSource(decoded) {
		return "", dialect.InvalidURL("invalid source path")
	}
	return escapeScheme(key), nil
}

// relativeKey turns a decoded path (overlay input, default image) into an
// escaped key relative to the mount's source.
func relativeKey(decoded string) string {
	u := url.URL{Path: strings.Trim(decoded, "/")}
	return escapeScheme(u.EscapedPath())
}

// escapeScheme escapes ":" in the first segment of an escaped key.
func escapeScheme(key string) string {
	first, rest, found := strings.Cut(key, "/")
	if !strings.Contains(first, ":") {
		return key
	}
	first = strings.ReplaceAll(first, ":", "%3A")
	if found {
		return first + "/" + rest
	}
	return first
}
