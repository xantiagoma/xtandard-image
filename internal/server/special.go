package server

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/obs/logging"
	"github.com/xantiagoma/xtandard-image/internal/source"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Special paths (ARCHITECTURE §2, WP-34c): raw and skip-processing
// streaming and the fallback image. Content-Disposition, ETag, 304 and HEAD
// are the WP-34b rules of response.go, applied to these results unchanged.
//
// Admission. raw and skip_processing both stream the source bytes after
// stage 9 without taking a processing permit: they do no decode and no
// libvips work, and the bytes are already bounded by the source size limit,
// so holding a worker slot would only let cheap passthroughs starve real
// processing. When skip_processing matches only after the probe (a format
// the magic-byte sniffer maps to no output format but the prober does, or
// SVG passthrough decided by negotiation), the permit is already held and is
// released as usual when produce returns.

// job is one source to produce (stages 9–13): the request's own source,
// or the fallback image in its place.
type job struct {
	req    *transform.Request
	key    source.Key
	lim    requestLimits
	hints  negotiate.HintResult
	signed bool // verified or trusted signature (immutable eligibility)
	// hashsum is the digest the fetched bytes must have (the request's
	// source hashsum, hashsum.go); nil for the fallback image, which is
	// another file.
	hashsum *transform.Hashsum
	// password opens a password-protected PDF source (documentPassword);
	// nil for the fallback image, which is another file.
	password []byte
	// cacheKey is the result-cache key when the lookup missed and the
	// response may be stored (cache.go); process copies it to the result,
	// never the fallback.
	cacheKey string
}

// newResult starts the result of j from its fetched source: what the
// response headers (ETag, Cache-Control, Last-Modified, debug headers)
// derive from.
func newResult(j *job, fr *fetch.Result) *imageResult {
	res := &imageResult{req: j.req, key: j.key, object: fr.Object, hints: j.hints, signed: j.signed, srcLen: len(fr.Bytes())}
	res.object.Body = nil
	return res
}

// fallbackHeader marks a fallback response when debug headers are on.
const fallbackHeader = "X-Xi-Fallback"

// raw serves transform.KindRaw: resolve (allowlist), fetch (SSRF policy,
// size limit, conditional request) and stream the bytes. There is no
// admission, probe, negotiation or processing, and no fallback. Processing
// options are ignored, like imgproxy's raw; request security options still
// adjust the size limit.
func (s *Server) raw(ctx context.Context, r *http.Request, info *requestInfo, req *transform.Request, signed bool) (*imageResult, error) {
	lim := s.limits(&req.Limits)

	t := s.stage(info, stageResolve)
	key, err := s.resolve(ctx, req.Source)
	t.done()
	if err != nil {
		return nil, err
	}
	info.source = key.Source

	t = s.stage(info, stageFetch)
	fr, err := s.fetch(ctx, key, source.FetchOptions{MaxBytes: lim.maxSrcBytes, Header: r.Header}, req.Source.Hashsum)
	t.done()
	if err != nil {
		return nil, err
	}
	defer fr.Release()
	res := newResult(&job{req: req, key: key, lim: lim, signed: signed}, fr)
	if err := s.passthrough(r, info, res, fr.Buffer, false, nil); err != nil {
		return nil, err
	}
	return res, nil
}

// skipProcessing streams fr into res when its sniffed format is listed in
// XI_FORMATS_SKIP_PROCESSING or the request's skip_processing. ok reports
// that the request was handled: res is complete, or err is set.
func (s *Server) skipProcessing(r *http.Request, info *requestInfo, res *imageResult, fr *fetch.Result) (ok bool, err error) {
	req := res.req
	if len(s.cfg.Negotiate.SkipProcessing) == 0 && len(req.Delivery.SkipProcessing) == 0 {
		return false, nil
	}
	sn, known := s.deps.Sniffer.Sniff(fr.Bytes())
	if !known || sn.Format == transform.FormatUnset ||
		!slices.Contains(s.cfg.Negotiate.SkipProcessing, sn.Format) && !slices.Contains(req.Delivery.SkipProcessing, sn.Format) {
		return false, nil
	}
	return true, s.passthrough(r, info, res, fr.Buffer, false, nil)
}

// passthrough completes res as an unprocessed response of buf. The
// Content-Type comes from the magic-byte sniff only; anything that is not a
// recognised image type is refused (422 Unsupported). An SVG document is
// sanitised first unless clean says buf already is. d is the negotiated
// decision when negotiation chose the passthrough (it keeps its Vary); nil
// means raw or skip_processing, which depend on the source alone.
//
// The ETag is the ARCHITECTURE §9.1 formula over the sniffed format with no
// quality delta, and client-hint Vary / Content-DPR are dropped: the bytes
// depend on neither. A matching conditional request is answered 304 before
// the body is retained or sanitised; otherwise res.out holds its own
// reference to the body.
func (s *Server) passthrough(r *http.Request, info *requestInfo, res *imageResult, buf fetch.Buffer, clean bool, d *negotiate.Decision) error {
	sn, ok := s.deps.Sniffer.Sniff(buf.Bytes())
	if !ok || !strings.HasPrefix(sn.ContentType, "image/") {
		return errs.New(errs.KindUnsupported, "server: unprocessed source is not a recognised image")
	}
	res.decision = negotiate.Decision{Format: sn.Format, Passthrough: true, Reason: negotiate.ReasonSkipProcessing}
	if d != nil {
		res.decision = *d
	}
	res.format, res.ext = sn.Format, sn.Ext
	res.hints = negotiate.HintResult{}
	res.etag = etag(res.req.CanonicalKey(), sn.Format, 0, &res.object, buf.Bytes(), s.cfg.ETagBuster)

	info.format = sn.Format.String()
	if sn.Format == transform.FormatUnset {
		info.format = sn.Ext
	}
	if res.notModified = notModified(r, res.etag, res.object.LastModified); res.notModified {
		return nil
	}

	body := buf
	if clean {
		body.Retain()
	} else {
		var (
			sanitised bool
			err       error
		)
		if body, sanitised, err = s.sanitize(buf); err != nil {
			return err
		}
		if sanitised != (sn.Format == transform.FormatSVG) {
			// The sniffer and the SVG detector disagree: never label
			// sanitised markup as something else, or serve unsanitised SVG.
			body.Release()
			return errs.New(errs.KindUnsupported, "server: sniffer and svg detector disagree")
		}
	}
	res.out = &ProcessOutput{Body: body, ContentType: sn.ContentType, Format: sn.Format}
	return nil
}

// fallback answers an eligible failure with the fallback image: the
// request's own fallback_image_url when XI_FALLBACK_ALLOW_REQUEST_URL is on
// and it passes the allowlist, else XI_FALLBACK_SOURCE. The fallback is
// produced with the same transform request. Any failure while producing it
// returns cause, the original error; the fallback never falls back.
//
// The fallback is always a full response, never a 304: a 304 would refresh
// a cached fallback with the success Cache-Control of setCacheHeaders and
// let it outlive XI_FALLBACK_TTL (fallbackWriter only rewrites a 200).
func (s *Server) fallback(ctx context.Context, r *http.Request, info *requestInfo, cause error) (*imageResult, error) {
	j := info.job
	if j == nil || !fallbackEligible(errs.KindOf(cause)) {
		return nil, cause
	}
	info.job = nil // loop protection
	key, ok := s.fallbackSource(r.Context(), j.req)
	if !ok {
		return nil, cause
	}
	fj := *j
	fj.key, fj.hashsum, fj.password = key, nil, nil
	res, err := s.produce(ctx, unconditional(r), info, &fj)
	if err != nil {
		s.deps.Logger.LogAttrs(r.Context(), slog.LevelWarn, "fallback image failed",
			slog.String("fallback_kind", errs.KindOf(err).String()), logging.Err(err))
		return nil, cause
	}
	s.deps.Logger.LogAttrs(r.Context(), slog.LevelWarn, "serving fallback image",
		slog.String("kind", errs.KindOf(cause).String()), logging.Err(cause))
	return res, nil
}

// unconditional returns r without its If-None-Match / If-Modified-Since
// headers (a copy only when it has any).
func unconditional(r *http.Request) *http.Request {
	if r.Header.Get("If-None-Match") == "" && r.Header.Get("If-Modified-Since") == "" {
		return r
	}
	r2 := r.Clone(r.Context())
	r2.Header.Del("If-None-Match")
	r2.Header.Del("If-Modified-Since")
	return r2
}

// fallbackSource picks the fallback image of req; ok is false when there
// is none.
func (s *Server) fallbackSource(ctx context.Context, req *transform.Request) (source.Key, bool) {
	if spec := req.Delivery.Fallback; spec != nil && spec.Raw != "" && s.cfg.Fallback.AllowRequestURL {
		key, err := s.resolve(ctx, *spec)
		if err == nil {
			return key, true
		}
		s.deps.Logger.LogAttrs(ctx, slog.LevelWarn, "request fallback image not allowed", logging.Err(err))
	}
	if s.fallbackKey != nil {
		return *s.fallbackKey, true
	}
	return source.Key{}, false
}

// fallbackEligible lists the failures of stages 9–12 a fallback image
// replaces (ARCHITECTURE §12): the source is missing, refused by its origin
// or the SSRF policy, unreachable, not a usable image, or over a limit.
// Client errors, overload, our own timeout and internal errors are never
// masked.
func fallbackEligible(k errs.Kind) bool {
	switch k {
	case errs.KindNotFound, errs.KindForbidden, errs.KindUpstreamError, errs.KindUpstreamTimeout,
		errs.KindUnsupported, errs.KindCorrupt, errs.KindSourceTooLarge, errs.KindLimitExceeded:
		return true
	default:
		return false
	}
}

// fallbackWriter turns the 200 that respond writes into XI_FALLBACK_STATUS
// with the XI_FALLBACK_TTL Cache-Control. Wrapping the writer keeps respond
// (and the WP-34b header logic in it: ETag, Vary, Content-Disposition)
// unaware of fallbacks; any other status passes through unchanged.
func (s *Server) fallbackWriter(w http.ResponseWriter) http.ResponseWriter {
	cc := noStore
	if ttl := s.cfg.Fallback.TTL; ttl > 0 {
		cc = "public, max-age=" + strconv.FormatInt(int64(ttl.Seconds()), 10)
	}
	return &fallbackWriter{
		ResponseWriter: w,
		status:         cmp.Or(s.cfg.Fallback.Status, http.StatusOK),
		cacheControl:   cc,
		debug:          s.cfg.DebugHeaders,
	}
}

type fallbackWriter struct {
	http.ResponseWriter
	status       int
	cacheControl string
	debug        bool
	wrote        bool
}

func (w *fallbackWriter) WriteHeader(code int) {
	if !w.wrote {
		w.wrote = true
		if code == http.StatusOK {
			h := w.Header()
			h.Set("Cache-Control", w.cacheControl)
			// The fallback has exactly its own lifetime (imgproxy parity).
			h.Del("Expires")
			h.Del("Last-Modified")
			if w.debug {
				h.Set(fallbackHeader, "1")
			}
			code = w.status
		}
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *fallbackWriter) Write(p []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func (w *fallbackWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
