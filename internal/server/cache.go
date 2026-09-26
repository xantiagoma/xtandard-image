package server

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/obs/logging"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Result cache (ARCHITECTURE §2 stage 8, §9.2; WP-52).
//
// The lookup runs after the source resolved and before the fetch: the key
// (cache.ResultKey) is the canonical request, the resolved source
// identity, the output format selector (the URL's format, else the Accept
// weights the negotiation reads), the client-hint inputs, the ETag buster
// and the build version. None of it needs the source bytes, so a hit skips
// fetch, admission, probe and processing. Source validators are not part
// of the key: a result is served for XI_CACHE_RESULT_TTL without
// contacting the origin (a per-request ttl or expires shortens it).
//
// A hit is answered through respond like a miss: the stored entry carries
// the headers derived from the source and the output (ETag, Content-Type,
// Last-Modified, Vary, Content-DPR, debug dimensions), and the ones derived
// from the request (Cache-Control, Content-Disposition, Cache-Tag, Link)
// are recomputed, so a hit's headers equal the miss's. 304 and HEAD are
// answered from the entry. Cache errors are logged and treated as misses.
//
// Stored: 200 responses of a GET for KindProcess requests, produced from
// the request's own source (never a fallback, a 304 or a HEAD), whose
// Cache-Control allows storing (no no-store or private) and whose TTL is
// positive. bypass_cache (with XI_CACHE_ALLOW_BYPASS) skips both the lookup
// and the store. Errors are never stored: the fetcher already caches
// not-found originals for XI_CACHE_ORIGIN_NEGATIVE_TTL. Writes are
// asynchronous (cache.AsyncStore) and never delay the response.

// X-Xi-Cache values (debug header and access log "cache" attribute).
const (
	cacheHit    = "HIT"
	cacheMiss   = "MISS"
	cacheBypass = "BYPASS"
)

// Stored metadata headers that are not response headers: they rebuild the
// imageResult of a hit and are never written as they are.
const (
	hdrFormat        = "X-Xi-Format"               // output format name ("" for unwritable passthrough formats)
	hdrExt           = "X-Xi-Ext"                  // sniffed extension of an unprocessed body
	hdrSourceCC      = "X-Xi-Source-Cache-Control" // origin Cache-Control (XI_CACHE_HEADERS_PASSTHROUGH)
	hdrSourceExpires = "X-Xi-Source-Expires"       // origin Expires (XI_CACHE_HEADERS_PASSTHROUGH)
	hdrXiCache       = "X-Xi-Cache"
)

// ResultCacheConfig configures the result cache (XI_CACHE_RESULT_*,
// XI_CACHE_ALLOW_BYPASS). It is used only when Deps.ResultCache is set.
type ResultCacheConfig struct {
	// TTL is how long a result is served from the cache
	// (XI_CACHE_RESULT_TTL); a per-request ttl or expires shortens it.
	TTL time.Duration
	// AllowBypass honours the bypass_cache option (XI_CACHE_ALLOW_BYPASS).
	AllowBypass bool
	// Build is mixed into every key so a new release never serves an older
	// encoder's bytes from a persistent tier.
	Build string
}

// resultKey derives the cache key of j (cache.ResultKey).
func (s *Server) resultKey(r *http.Request, j *job) string {
	k := cache.ResultKey{
		Canonical:    j.req.CanonicalKey(),
		Source:       j.key.Source + "\x00" + j.key.Bucket + "\x00" + j.key.Path + "\x00" + j.key.Version,
		QualityDelta: j.hints.QualityDelta,
		Hints:        j.hints.Vary.Header() + "|" + strconv.FormatFloat(j.hints.ContentDPR, 'g', -1, 64),
		Buster:       s.cfg.ETagBuster,
		Build:        s.cfg.ResultCache.Build,
	}
	if d, ok := fixedDecision(j.req, &s.cfg.Negotiate); ok {
		k.Format = "f:" + d.Format.String()
	} else {
		k.Format = "a:" + negotiate.ParseAcceptHeader(r.Header).Key()
	}
	return k.String()
}

// cacheLookup is stage 8. It returns a complete result on a hit; on a miss
// (or a bypass) it records the status and, when the response may be
// stored, the key in j.
func (s *Server) cacheLookup(ctx context.Context, r *http.Request, info *requestInfo, j *job) (*imageResult, bool) {
	if s.deps.ResultCache == nil || j.req.Kind != transform.KindProcess {
		return nil, false
	}
	if j.req.Delivery.BypassCache && s.cfg.ResultCache.AllowBypass {
		info.cache = cacheBypass
		return nil, false
	}
	t := s.stage(info, stageCache)
	key := s.resultKey(r, j)
	e, ok, err := s.deps.ResultCache.Get(ctx, cache.NSResult, key)
	t.done()
	if err != nil {
		s.deps.Logger.LogAttrs(ctx, slog.LevelWarn, "result cache lookup failed", logging.Err(err))
	}
	info.cache = cacheMiss
	j.cacheKey = key
	if !ok {
		return nil, false
	}
	res, err := s.resultFromEntry(r, info, j, &e)
	if err != nil {
		e.Release()
		s.deps.Logger.LogAttrs(ctx, slog.LevelWarn, "result cache entry unusable", logging.Err(err))
		return nil, false
	}
	info.cache = cacheHit
	return res, true
}

// resultFromEntry rebuilds the imageResult a miss produced from its
// stored entry. It takes over e's body reference (released at once for a
// 304).
func (s *Server) resultFromEntry(r *http.Request, info *requestInfo, j *job, e *cache.Entry) (*imageResult, error) {
	h := e.Header
	etag := h.Get("ETag")
	if e.Status != http.StatusOK || etag == "" || e.Body == nil {
		return nil, errs.Newf(errs.KindInternal, "server: cached entry has status %d and etag %q", e.Status, etag)
	}
	var f transform.Format
	if err := f.UnmarshalText([]byte(h.Get(hdrFormat))); err != nil {
		return nil, errs.Wrapf(errs.KindInternal, err, "server: cached entry format")
	}
	res := &imageResult{
		req: j.req, key: j.key, signed: j.signed,
		etag: etag, format: f, ext: h.Get(hdrExt),
		decision: negotiate.Decision{Format: f},
		stored:   h,
	}
	res.object.LastModified = parseHTTPTime(h.Get("Last-Modified"))
	res.object.CacheControl = h.Get(hdrSourceCC)
	res.object.Expires = parseHTTPTime(h.Get(hdrSourceExpires))
	res.srcLen = atoiHeader(h, "X-Xi-Origin-Content-Length")
	res.meta.Width, res.meta.Height = atoiHeader(h, "X-Xi-Origin-Width"), atoiHeader(h, "X-Xi-Origin-Height")

	info.format = f.String()
	if f == transform.FormatUnset {
		info.format = res.ext
	}
	if res.notModified = notModified(r, etag, res.object.LastModified); res.notModified {
		e.Release()
		return res, nil
	}
	res.out = &ProcessOutput{
		Body:        e.Body,
		ContentType: h.Get("Content-Type"),
		Format:      f,
		Width:       atoiHeader(h, "X-Xi-Result-Width"),
		Height:      atoiHeader(h, "X-Xi-Result-Height"),
		Quality:     atoiHeader(h, "X-Xi-Quality"),
	}
	info.width, info.height = res.out.Width, res.out.Height
	return res, nil
}

// cacheStore queues res for the result cache after respond wrote it. body
// is the response body with the caller's reference (the store takes its
// own). header is what respond wrote; its Cache-Control decides whether
// the response may be stored.
func (s *Server) cacheStore(ctx context.Context, r *http.Request, res *imageResult, body cache.Buffer, header http.Header) {
	if res.cacheKey == "" || res.stored != nil || res.out == nil || res.notModified || r.Method != http.MethodGet ||
		!storable(header.Get("Cache-Control")) {
		return
	}
	ttl := s.resultTTL(&res.req.Delivery, s.now())
	if ttl <= 0 {
		return
	}
	e := cache.Entry{Body: body, Status: http.StatusOK, Header: s.entryHeader(res), StoredAt: s.now()}
	if err := s.deps.ResultCache.Set(ctx, cache.NSResult, res.cacheKey, &e, ttl); err != nil {
		s.deps.Logger.LogAttrs(ctx, slog.LevelDebug, "result cache write not queued", logging.Err(err))
	}
}

// resultTTL is XI_CACHE_RESULT_TTL shortened by the request's ttl and
// expires, like max-age (§9.1).
func (s *Server) resultTTL(d *transform.Delivery, now time.Time) time.Duration {
	ttl := s.cfg.ResultCache.TTL
	if d.TTL > 0 {
		ttl = min(ttl, d.TTL)
	}
	if !d.Expires.IsZero() {
		ttl = min(ttl, d.Expires.Sub(now))
	}
	return ttl
}

// entryHeader is the stored header subset of res (cache.Entry.Header).
func (s *Server) entryHeader(res *imageResult) http.Header {
	h := make(http.Header, 12)
	h.Set("Content-Type", res.contentType())
	h.Set("ETag", res.etag)
	if lm := res.object.LastModified; !lm.IsZero() {
		h.Set("Last-Modified", lm.UTC().Format(http.TimeFormat))
	}
	if v := res.varyHeader(); v != "" {
		h.Set("Vary", v)
	}
	if v := res.contentDPR(); v != "" {
		h.Set("Content-Dpr", v)
	}
	h.Set(hdrFormat, res.format.String())
	if res.ext != "" {
		h.Set(hdrExt, res.ext)
	}
	if cc := res.object.CacheControl; cc != "" {
		h.Set(hdrSourceCC, cc)
	}
	if exp := res.object.Expires; !exp.IsZero() {
		h.Set(hdrSourceExpires, exp.UTC().Format(http.TimeFormat))
	}
	h.Set("X-Xi-Origin-Content-Length", strconv.Itoa(res.srcLen))
	if res.meta.Width > 0 {
		h.Set("X-Xi-Origin-Width", strconv.Itoa(res.meta.Width))
		h.Set("X-Xi-Origin-Height", strconv.Itoa(res.meta.Height))
	}
	if res.out.Width > 0 {
		h.Set("X-Xi-Result-Width", strconv.Itoa(res.out.Width))
		h.Set("X-Xi-Result-Height", strconv.Itoa(res.out.Height))
	}
	if res.out.Quality > 0 {
		h.Set("X-Xi-Quality", strconv.Itoa(res.out.Quality))
	}
	return h
}

// storable reports whether a response with this Cache-Control may be kept
// by a shared cache (RFC 9111 §3): not with no-store or private.
func storable(cc string) bool {
	for d := range strings.SplitSeq(cc, ",") {
		name, _, _ := strings.Cut(strings.TrimSpace(d), "=")
		if strings.EqualFold(name, "no-store") || strings.EqualFold(name, "private") {
			return false
		}
	}
	return true
}

func parseHTTPTime(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	t, err := http.ParseTime(v)
	if err != nil {
		return time.Time{}
	}
	return t
}

func atoiHeader(h http.Header, name string) int {
	n, err := strconv.Atoi(h.Get(name))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
