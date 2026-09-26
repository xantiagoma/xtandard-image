package server

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/source"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Static header values (go-standards §2: pre-built, no per-request
// formatting).
const (
	// svgCSP neutralises anything an SVG could still do when opened as a
	// document (ARCHITECTURE §9.1).
	svgCSP = "default-src 'none'; style-src 'unsafe-inline'; sandbox"
	// noStore is the Cache-Control of responses that must never be reused.
	noStore = "no-store"
	// noCache is the Cache-Control of a success whose lifetime is zero.
	noCache = "no-cache"
	// fallbackStem names a download when neither the URL nor the source
	// path gives one (imgproxy parity).
	fallbackStem = "image"
	// maxFilenameBytes caps the Content-Disposition file name.
	maxFilenameBytes = 200
	// maxDebugHeaderBytes caps the X-Xi-Ignored value.
	maxDebugHeaderBytes = 1024
)

// imageResult is what stage 14 writes: an encoded image, or a 304 / HEAD
// answer that never ran the processor, plus what the headers derive from.
type imageResult struct {
	// out is the encoded image; nil for 304 and HEAD.
	out *ProcessOutput
	// notModified answers 304: the client's validators match.
	notModified bool
	// etag is computed from the request and the source, never the output
	// bytes, so it is known before processing.
	etag string
	// format is the negotiated output format.
	format   transform.Format
	decision negotiate.Decision
	hints    negotiate.HintResult
	req      *transform.Request
	key      source.Key
	object   source.Object // origin metadata (Body nil)
	// signed reports a verified or trusted signature (immutable eligibility).
	signed bool
	// srcLen is the original's size in bytes; meta is zero until the probe.
	srcLen int
	meta   SourceMeta
	// ext is the sniffed file extension (no dot) of an unprocessed raw or
	// skip-processing body; "" derives it from format (WP-34c).
	ext string
	// cacheKey is the result-cache key under which a successful GET is
	// stored after it was written; "" when it must not be stored (cache
	// off, bypass, fallback, raw, info).
	cacheKey string
	// stored is the cached entry's header of a result-cache hit (cache.go);
	// nil for a response produced now.
	stored http.Header
}

// contentType is the Content-Type of the body.
func (res *imageResult) contentType() string {
	if res.out != nil && res.out.ContentType != "" {
		return res.out.ContentType
	}
	return negotiate.ContentType(res.format)
}

// varyHeader is the Vary value: the negotiation's and the client hints'
// request headers, or the stored value of a cache hit.
func (res *imageResult) varyHeader() string {
	if res.stored != nil {
		return res.stored.Get("Vary")
	}
	return (res.decision.Vary | res.hints.Vary).Header()
}

// contentDPR is the Content-DPR value ("" when none applies).
func (res *imageResult) contentDPR() string {
	if res.stored != nil {
		return res.stored.Get("Content-Dpr")
	}
	if dpr := res.hints.ContentDPR; dpr != 0 && dpr != 1 {
		return strconv.FormatFloat(dpr, 'f', -1, 64)
	}
	return ""
}

// respond is stage 14 (ARCHITECTURE §9.1). A 304 carries the headers a 200
// would (ETag, Cache-Control, Last-Modified, Vary, …; RFC 9110 §15.4.5) but
// no representation headers. HEAD gets every header of the GET except
// Content-Length, which is only known after encoding and is omitted. A 200
// writes the encoded image zero-copy in one Write.
func (s *Server) respond(w http.ResponseWriter, r *http.Request, res *imageResult) {
	h := w.Header()
	s.setCacheHeaders(h, res)
	if res.notModified {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	ct := res.contentType()
	h.Set("Content-Type", ct)
	h.Set("X-Content-Type-Options", "nosniff")
	ext := formatExt(res.format)
	if res.ext != "" {
		ext = "." + res.ext
	}
	h.Set("Content-Disposition", contentDispositionExt(res.req.Delivery.Filename, res.key.Path, ext, attachment(&res.req.Delivery)))
	if res.format == transform.FormatSVG || ct == "image/svg+xml" {
		h.Set("Content-Security-Policy", svgCSP)
	}
	if s.cfg.DebugHeaders {
		setDebugHeaders(h, res)
	}
	if res.out == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	body := res.out.Body.Bytes()
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body) //nolint:gosec // G705: encoded image bytes with an explicit image Content-Type and nosniff
	}
}

// setCacheHeaders writes the validators and caching headers shared by 200,
// HEAD and 304 answers.
func (s *Server) setCacheHeaders(h http.Header, res *imageResult) {
	h.Set("ETag", res.etag)
	h.Set("Cache-Control", s.cacheControl(res, s.now()))
	if lm := res.object.LastModified; !lm.IsZero() {
		h.Set("Last-Modified", lm.UTC().Format(http.TimeFormat))
	}
	if v := res.varyHeader(); v != "" {
		h.Set("Vary", v)
	}
	s.setHintHeaders(h, res)
	if s.cfg.CanonicalHeader {
		if u := res.key.Path; strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
			h.Set("Link", "<"+u+`>; rel="canonical"`)
		}
	}
	if name := s.cfg.CacheTagsHeader; name != "" && len(res.req.Delivery.CacheTags) > 0 {
		if v := cacheTags(res.req.Delivery.CacheTags); v != "" {
			h.Set(name, v)
		}
	}
}

// cacheControl is the success Cache-Control of ARCHITECTURE §9.1:
//
//   - a per-request TTL (clamped by XI_CACHE_HEADERS_TTL) or an `expires`
//     timestamp shortens max-age; expiring URLs never get
//     stale-while-revalidate or immutable, which would outlive the expiry;
//   - otherwise, with XI_CACHE_HEADERS_PASSTHROUGH, the source's
//     Cache-Control is copied, or its Expires turned into max-age;
//   - otherwise public, max-age=TTL, stale-while-revalidate=TTL/10, plus
//     immutable for cloudinary fl_immutable_cache or a signed URL that carries
//     a cache buster or a content hash.
func (s *Server) cacheControl(res *imageResult, now time.Time) string {
	d := &res.req.Delivery
	ttl := s.cfg.Cache.TTL
	explicit := d.TTL > 0 || !d.Expires.IsZero()
	if d.TTL > 0 {
		ttl = min(ttl, d.TTL)
	}
	expiring := !d.Expires.IsZero()
	if expiring {
		ttl = min(ttl, d.Expires.Sub(now))
	}
	if !explicit && s.cfg.Cache.Passthrough {
		if v := passthroughCacheControl(&res.object, now); v != "" {
			return v
		}
	}
	secs := int64(ttl / time.Second)
	if secs <= 0 {
		return noCache
	}
	b := make([]byte, 0, 96)
	b = append(b, "public, max-age="...)
	b = strconv.AppendInt(b, secs, 10)
	if swr := secs / 10; swr > 0 && !expiring {
		b = append(b, ", stale-while-revalidate="...)
		b = strconv.AppendInt(b, swr, 10)
	}
	if !expiring && (d.Immutable || (res.signed && (d.CacheBuster != "" || res.req.Source.Hashsum != nil))) {
		b = append(b, ", immutable"...)
	}
	return string(b)
}

// passthroughCacheControl returns the origin's Cache-Control, else a
// max-age derived from its Expires, else "".
func passthroughCacheControl(obj *source.Object, now time.Time) string {
	if obj.CacheControl != "" {
		return obj.CacheControl
	}
	if obj.Expires.IsZero() {
		return ""
	}
	secs := int64(obj.Expires.Sub(now) / time.Second)
	if secs <= 0 {
		return noCache
	}
	return "public, max-age=" + strconv.FormatInt(secs, 10)
}

// errorCacheControl is the Cache-Control column of ARCHITECTURE §12:
// failures of the source or the image (which retrying will not fix) are
// cached for XI_CACHE_HEADERS_ERROR_TTL; request, policy, overload and
// server failures are never stored.
func (s *Server) errorCacheControl(kind errs.Kind, status int) string {
	switch kind {
	case errs.KindNotFound, errs.KindSourceTooLarge, errs.KindLimitExceeded, errs.KindUnsupported,
		errs.KindCorrupt, errs.KindUpstreamError, errs.KindUpstreamTimeout:
		secs := int64(s.cfg.Cache.ErrorTTL / time.Second)
		// 501 (not implemented yet) wraps KindUnsupported but changes with
		// the next release.
		if secs <= 0 || status == http.StatusNotImplemented {
			return noStore
		}
		return "public, max-age=" + strconv.FormatInt(secs, 10)
	default:
		return noStore
	}
}

// notModified evaluates the client's conditional headers against the
// representation that would be sent (RFC 9110 §13.2.2): If-None-Match wins;
// If-Modified-Since is only consulted without it and only when the source
// has a Last-Modified.
func notModified(r *http.Request, etag string, lastModified time.Time) bool {
	if inm := r.Header.Values("If-None-Match"); len(inm) > 0 {
		for _, v := range inm {
			if etagListMatch(v, etag) {
				return true
			}
		}
		return false
	}
	ims := r.Header.Get("If-Modified-Since")
	if ims == "" || lastModified.IsZero() {
		return false
	}
	t, err := http.ParseTime(ims)
	if err != nil {
		return false
	}
	return !lastModified.Truncate(time.Second).After(t)
}

// etagListMatch reports whether the If-None-Match field value list matches
// etag with the weak comparison RFC 9110 §13.1.2 prescribes. Malformed
// members end the scan without matching.
func etagListMatch(list, etag string) bool {
	for {
		list = strings.TrimLeft(list, " \t,")
		if list == "" {
			return false
		}
		if list[0] == '*' {
			return true
		}
		list = strings.TrimPrefix(list, "W/")
		if list == "" || list[0] != '"' {
			return false
		}
		end := strings.IndexByte(list[1:], '"')
		if end < 0 {
			return false
		}
		if list[:end+2] == etag {
			return true
		}
		list = list[end+2:]
	}
}

// etagLen is the number of base64url characters kept from the digest
// (ARCHITECTURE §9.1).
const etagLen = 22

// etag derives the strong ETag of ARCHITECTURE §9.1 without looking at the
// output bytes, so it is known before processing:
//
//	"xi1-" + base64url(sha256(F(canonicalKey) F(outputFormat) F(qualityDelta) F(sourceValidator) F(buster)))[:22]
//
// where F(x) = decimal byte length of x, ":", x. sourceValidator is "e" + the
// origin ETag, else "m" + its Last-Modified as an HTTP date, else "h" + the
// raw SHA-256 of the source bytes. qualityDelta (Save-Data) changes the
// output without appearing in the canonical key, so it is mixed in too.
func etag(canonicalKey string, format transform.Format, qualityDelta int, obj *source.Object, src []byte, buster string) string {
	d := sha256.New()
	field := func(b []byte) {
		var n [20]byte
		d.Write(strconv.AppendInt(n[:0], int64(len(b)), 10))
		d.Write([]byte{':'})
		d.Write(b)
	}
	field([]byte(canonicalKey))
	field([]byte(format.String()))
	field(strconv.AppendInt(nil, int64(qualityDelta), 10))
	switch {
	case obj != nil && obj.ETag != "":
		field([]byte("e" + obj.ETag))
	case obj != nil && !obj.LastModified.IsZero():
		field([]byte("m" + obj.LastModified.UTC().Format(http.TimeFormat)))
	default:
		sum := sha256.Sum256(src)
		field(append([]byte("h"), sum[:]...))
	}
	field([]byte(buster))

	var sum [sha256.Size]byte
	enc := base64.RawURLEncoding.EncodeToString(d.Sum(sum[:0]))
	b := make([]byte, 0, 2+4+etagLen)
	b = append(b, `"xi1-`...)
	b = append(b, enc[:etagLen]...)
	b = append(b, '"')
	return string(b)
}

// attachment reports whether the image is served as a download. Without
// the option it is inline (imgproxy's IMGPROXY_RETURN_ATTACHMENT has no
// XI_ equivalent).
func attachment(d *transform.Delivery) bool { return d.Attachment != nil && *d.Attachment }

// contentDisposition renders `inline|attachment; filename="…"` (RFC 6266).
// The stem is the filename option, else the source path's base name without
// its extension, else "image"; the extension is the output format's. The
// quoted filename is a printable-ASCII fallback; when it differs from the
// real name, filename* carries the UTF-8 name (RFC 8187).
func contentDisposition(filename, sourcePath string, f transform.Format, attach bool) string {
	return contentDispositionExt(filename, sourcePath, formatExt(f), attach)
}

// contentDispositionExt is contentDisposition with the extension given
// directly (".psd", or "" for none): unprocessed raw and skip-processing
// bodies may be in a format no encoder writes (WP-34c).
func contentDispositionExt(filename, sourcePath, ext string, attach bool) string {
	var stem string
	if filename != "" {
		stem = sanitizeFilename(filename)
		// A filename that already ends in the output extension keeps it.
		if e := stemExt(stem); e != "" && sameExt(e, ext) {
			stem = stem[:len(stem)-len(e)]
		}
	} else {
		stem = sanitizeFilename(sourceBase(sourcePath))
		if e := stemExt(stem); e != "" && e != stem {
			stem = stem[:len(stem)-len(e)]
		}
	}
	stem = strings.TrimRight(stem, ". ")
	if stem == "" {
		stem = fallbackStem
	}
	name := stem + ext

	var b strings.Builder
	b.Grow(len(name)*2 + 32)
	if attach {
		b.WriteString("attachment")
	} else {
		b.WriteString("inline")
	}
	b.WriteString(`; filename="`)
	plain := true
	for _, r := range name {
		if r == ' ' || (r > 0x20 && r < 0x7f && r != '"' && r != '\\' && r != '%') {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
		plain = false
	}
	b.WriteByte('"')
	if !plain {
		b.WriteString("; filename*=UTF-8''")
		for i := range len(name) {
			c := name[i]
			if isAttrChar(c) {
				b.WriteByte(c)
				continue
			}
			b.WriteByte('%')
			b.WriteByte("0123456789ABCDEF"[c>>4])
			b.WriteByte("0123456789ABCDEF"[c&0xf])
		}
	}
	return b.String()
}

// sourceBase is the last path segment of a source path or URL, without
// query or fragment, percent-decoded for URLs.
func sourceBase(p string) string {
	isURL := strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://")
	if isURL {
		if i := strings.IndexAny(p, "?#"); i >= 0 {
			p = p[:i]
		}
		if u, err := url.Parse(p); err == nil {
			if u.Path == "" {
				return ""
			}
			p = u.Path
		}
	}
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		p = p[i+1:]
	}
	return p
}

// sanitizeFilename keeps the last path segment (either separator), drops
// control characters and invalid UTF-8, trims leading dots and surrounding
// spaces and caps the length at a rune boundary.
func sanitizeFilename(s string) string {
	if i := strings.LastIndexAny(s, `/\`); i >= 0 {
		s = s[i+1:]
	}
	var b strings.Builder
	b.Grow(min(len(s), maxFilenameBytes))
	for _, r := range s {
		if r == utf8.RuneError || unicode.IsControl(r) || !unicode.IsPrint(r) && r != ' ' {
			continue
		}
		if b.Len()+utf8.RuneLen(r) > maxFilenameBytes {
			break
		}
		b.WriteRune(r)
	}
	return strings.TrimLeft(strings.TrimSpace(b.String()), ".")
}

// stemExt returns the extension of name including the dot, or "".
func stemExt(name string) string {
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return ""
	}
	return name[i:]
}

// sameExt compares extensions case-insensitively, treating the JPEG and
// TIFF spellings as equal.
func sameExt(a, b string) bool {
	norm := func(e string) string {
		switch e = strings.ToLower(e); e {
		case ".jpeg", ".jpe":
			return ".jpg"
		case ".tif":
			return ".tiff"
		default:
			return e
		}
	}
	return norm(a) == norm(b)
}

// formatExt is the file extension of an output format.
func formatExt(f transform.Format) string {
	switch f {
	case transform.FormatUnset, transform.FormatBest:
		return ""
	case transform.FormatJPEG:
		return ".jpg"
	default:
		return "." + f.String()
	}
}

// isAttrChar reports RFC 8187 attr-char (sent unencoded in filename*).
func isAttrChar(c byte) bool {
	switch {
	case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		return true
	}
	return strings.IndexByte("!#$&+-.^_`|~", c) >= 0
}

// cacheTags joins the cache_tags values with commas, dropping characters
// that could split or break the header.
func cacheTags(tags []string) string {
	var b strings.Builder
	for _, t := range tags {
		t = strings.Map(func(r rune) rune {
			if r <= 0x20 || r >= 0x7f || r == ',' {
				return -1
			}
			return r
		}, t)
		if t == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(t)
	}
	return b.String()
}

// setDebugHeaders writes the XI_OBSERVABILITY_DEBUG_HEADERS headers
// (imgproxy's X-Origin-* / X-Result-* under the X-Xi- prefix): the
// original's size and dimensions, the result's dimensions when it was
// encoded, and the options that were parsed but not applied.
func setDebugHeaders(h http.Header, res *imageResult) {
	h.Set("X-Xi-Origin-Content-Length", strconv.Itoa(res.srcLen))
	if res.meta.Width > 0 {
		h.Set("X-Xi-Origin-Width", strconv.Itoa(res.meta.Width))
		h.Set("X-Xi-Origin-Height", strconv.Itoa(res.meta.Height))
	}
	if res.out != nil && res.out.Width > 0 {
		h.Set("X-Xi-Result-Width", strconv.Itoa(res.out.Width))
		h.Set("X-Xi-Result-Height", strconv.Itoa(res.out.Height))
	}
	if len(res.req.Ignored) > 0 {
		var b strings.Builder
		for _, ig := range res.req.Ignored {
			opt := strings.Map(func(r rune) rune {
				if r < 0x21 || r >= 0x7f || r == ',' {
					return -1
				}
				return r
			}, ig.Option)
			if opt == "" || b.Len()+len(opt)+2 > maxDebugHeaderBytes {
				continue
			}
			if b.Len() > 0 {
				b.WriteString(", ")
			}
			b.WriteString(opt)
		}
		if b.Len() > 0 {
			h.Set("X-Xi-Ignored", b.String())
		}
	}
}
