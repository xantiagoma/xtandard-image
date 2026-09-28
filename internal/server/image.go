package server

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/codec/container"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/security/svg"
	"github.com/xantiagoma/xtandard-image/internal/source"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// image serves every non-fixed GET/HEAD path: the request lifecycle of
// ARCHITECTURE §2 under one request budget. Every failure goes through
// writeError; nothing below writes an error itself.
func (s *Server) image(w http.ResponseWriter, r *http.Request) {
	info := infoOf(w)
	ctx, cancel := context.WithTimeoutCause(r.Context(), s.cfg.RequestTimeout, errRequestTimeout)
	defer cancel()

	res, err := s.process(ctx, r, info)
	if info.cache != "" && s.cfg.DebugHeaders {
		w.Header().Set(hdrXiCache, info.cache)
	}
	if err != nil {
		err = classify(ctx, r, err)
		// An eligible stage 9–12 failure of a request that asks for it
		// (cloudflare onerror=redirect, wsrv default=) is a 302, ahead of
		// the fallback image (redirect.go).
		if s.errorRedirect(w, r, info, err) {
			return
		}
		// WP-34c hook: an eligible stage 9–12 failure is answered with the
		// fallback image when one is configured (special.go).
		if res, err = s.fallback(ctx, r, info, err); err != nil {
			s.writeError(w, r, err)
			return
		}
		w = s.fallbackWriter(w)
	}
	// A response the result cache may keep: its body becomes a refcounted
	// cache.Buffer adopting the encoder's blob (no copy), so the async
	// write can hold it past this handler.
	var body cache.Buffer
	if res.cacheKey != "" && res.out != nil && !res.notModified && r.Method == http.MethodGet {
		blob := res.out.Body
		body = cache.NewBuffer(blob.Bytes(), blob.Release)
		res.out.Body = body
	}
	if res.out != nil {
		defer res.out.Body.Release()
	}

	t := s.stage(info, stageRespond)
	if res.req.Kind == transform.KindInfo {
		s.respondInfo(w, r, res)
	} else {
		s.respond(w, r, res)
	}
	t.done()
	if body != nil {
		s.cacheStore(ctx, r, res, body, w.Header())
	}
}

// process runs stages 2–13. Everything it acquires (source buffer,
// admission permit, sanitised SVG) is released before it returns; only the
// encoded output outlives it.
//
// Conditional requests and HEAD never reach the processor (ARCHITECTURE
// §9.1): the ETag depends only on the canonical request, the negotiated
// format and the source validator, so it is known right after the fetch
// (which revalidates cached originals with the origin itself). When the URL
// fixes the output format the 304 is answered before admission; otherwise
// after the header probe and negotiation. HEAD stops after negotiation.
func (s *Server) process(ctx context.Context, r *http.Request, info *requestInfo) (*imageResult, error) {
	// 2–4. Route, parse (presets are expanded by the dialect), authenticate
	// and apply the unsigned policy.
	t := s.stage(info, stageRoute)
	rr, ok := s.stripPathPrefix(r)
	if !ok {
		t.done()
		return nil, &errs.Error{Kind: errs.KindNotFound, Public: "not found", Msg: "server: outside path prefix"}
	}
	parsed, mount, err := s.deps.Router.Route(rr)
	if mount != nil {
		info.dialect = mount.Dialect
	}
	t.done()
	if err != nil {
		return nil, err
	}
	req := parsed.Request
	signed := parsed.Signature != sign.Unsigned
	// Defence in depth: dialects reject expired URLs at authentication.
	if e := req.Delivery.Expires; !e.IsZero() && !s.now().Before(e) {
		return nil, errs.New(errs.KindExpired, "server: url expired")
	}
	switch req.Kind {
	case transform.KindInfo:
		return s.inspect(ctx, r, req, info)
	case transform.KindRaw:
		return s.raw(ctx, r, info, req, signed) // special.go: no admission, no decode
	default:
	}

	// 6. Normalise: client hints become concrete values, then defaults,
	// then validation, so the canonical key only holds effective values.
	t = s.stage(info, stageNormalize)
	hints := s.resolveHints(r, req, mount, signed)
	req.ApplyDefaults(s.cfg.Defaults)
	err = req.Validate()
	lim := s.limits(&req.Limits)
	if err == nil {
		err = checkResultDimension(req, lim.maxResultDimension)
	}
	t.done()
	if err != nil {
		return nil, err
	}

	// 5. Resolve the source against the allowlist.
	t = s.stage(info, stageResolve)
	key, err := s.resolve(ctx, req.Source)
	t.done()
	if err != nil {
		return nil, err
	}
	info.source = key.Source

	j := &job{req: req, key: key, lim: lim, hints: hints, signed: signed, hashsum: req.Source.Hashsum}
	// 8. Result cache: a hit skips fetch, admission, probe and processing.
	if res, ok := s.cacheLookup(ctx, r, info, j); ok {
		return res, nil
	}
	info.job = j // failures from here on may be answered with the fallback
	res, err := s.produce(ctx, r, info, j)
	if err != nil {
		return nil, err
	}
	res.cacheKey = j.cacheKey
	return res, nil
}

// produce runs stages 9–13 of one job: the requested source, or the
// fallback image in its place (special.go).
func (s *Server) produce(ctx context.Context, r *http.Request, info *requestInfo, j *job) (*imageResult, error) {
	req, lim, hints := j.req, &j.lim, j.hints

	// 9. Fetch, outside the processing semaphore so slow origins never hold
	// a worker slot.
	t := s.stage(info, stageFetch)
	fr, err := s.fetch(ctx, j.key, source.FetchOptions{MaxBytes: lim.maxSrcBytes, Header: r.Header}, j.hashsum)
	t.done()
	if err != nil {
		return nil, err
	}
	defer fr.Release()

	res := newResult(j, fr)

	// skip_processing (XI_FORMATS_SKIP_PROCESSING or the request's list):
	// a listed sniffed format is streamed without admission or decode.
	if ok, err := s.skipProcessing(r, info, res, fr); ok {
		if err != nil {
			return nil, err
		}
		return res, nil
	}

	canonical := req.CanonicalKey()
	decide := func(d negotiate.Decision) bool {
		res.decision, res.format = d, d.Format
		key := canonical
		if bk := d.BestKey(); bk != "" {
			// format=best: the candidate lists (from Accept) decide the
			// output, not d.Format alone.
			key += "\x00best:" + bk
		}
		res.etag = etag(key, d.Format, hints.QualityDelta, &fr.Object, fr.Bytes(), s.cfg.ETagBuster)
		res.notModified = notModified(r, res.etag, fr.Object.LastModified)
		if res.notModified {
			info.format = d.Format.String()
		}
		return res.notModified
	}
	if d, ok := fixedDecision(req, &s.cfg.Negotiate); ok && decide(d) {
		return res, nil
	}

	// 9b. The request's own watermark images, fetched like the source and
	// still outside the processing semaphore (each has its fetch span).
	marks, err := s.fetchWatermarks(ctx, req, lim.maxSrcBytes)
	if err != nil {
		return nil, err
	}
	defer releaseWatermarks(marks)

	// 10. Admission: fail fast when the queue is full.
	t = s.stage(info, stageAdmission)
	// The planner's memory estimate needs the probe below: the executor
	// charges it (limiter.Reserve through exec.Config.Admit) once the plan
	// exists, before decoding.
	release, err := s.deps.Admitter.Acquire(ctx, 0)
	t.done()
	if err != nil {
		return nil, err
	}
	defer release()

	// 11. Probe: sanitise SVG, read the header, enforce input limits.
	t = s.stage(info, stageProbe)
	src, meta, err := s.probe(ctx, req, fr.Buffer, &lim.probe, lim.maxSrcBytes)
	t.done()
	if err != nil {
		return nil, err
	}
	defer src.Release()
	defer meta.closeSession()

	// 7. Negotiate the output format (needs the source meta).
	t = s.stage(info, stageNegotiate)
	d, err := negotiate.Decide(req, negotiate.ParseAcceptHeader(r.Header), &s.cfg.Negotiate, negotiate.SourceMeta{
		Format:     meta.Format,
		HasAlpha:   meta.HasAlpha,
		IsAnimated: meta.IsAnimated,
		FrameCount: meta.Frames,
		// Until the planner (WP-25) reports whether any raster operation
		// applies, SVG is always rasterised, never passed through.
		NeedsRaster: true,
	})
	t.done()
	if err != nil {
		return nil, err
	}
	res.meta = meta
	if d.Passthrough {
		// A skip-processing format only the prober recognised, or SVG
		// passthrough: stream the (sanitised) source. The negotiated
		// decision keeps its Vary (SVG passthrough depends on Accept).
		if err := s.passthrough(r, info, res, src, true, &d); err != nil {
			return nil, err
		}
		return res, nil
	}
	if decide(d) {
		return res, nil
	}
	if r.Method == http.MethodHead {
		info.format = d.Format.String()
		return res, nil
	}

	// 12–13. Plan, execute, encode.
	if err := ctx.Err(); err != nil {
		return nil, errs.Wrap(errs.KindOf(err), context.Cause(ctx))
	}
	t = s.stage(info, stageProcess)
	out, err := s.run(ctx, &ProcessInput{
		Request:      req,
		Source:       src,
		Meta:         meta,
		Decision:     d,
		QualityDelta: hints.QualityDelta,
		Watermarks:   marks,
	})
	t.done()
	if err != nil {
		return nil, err
	}
	info.format = out.Format.String()
	info.width, info.height = out.Width, out.Height
	res.out = out
	res.format = out.Format // format=best chose among the candidates
	return res, nil
}

// fixedDecision returns the negotiation outcome when the URL alone decides
// it: an explicit concrete format and no skip-processing list that could
// turn the request into a passthrough depending on the source format. It
// mirrors negotiate.Decide's explicit branch, so the ETag computed from it
// equals the one computed after the probe.
func fixedDecision(req *transform.Request, cfg *negotiate.Config) (negotiate.Decision, bool) {
	f := req.Output.Format
	if req.Kind != transform.KindProcess || f == transform.FormatUnset || f == transform.FormatBest ||
		len(cfg.SkipProcessing) > 0 || len(req.Delivery.SkipProcessing) > 0 {
		return negotiate.Decision{}, false
	}
	return negotiate.Decision{Format: f, Reason: negotiate.ReasonExplicit}, true
}

// stripPathPrefix removes XI_SERVER_PATH_PREFIX; ok is false when the path
// is outside it.
func (s *Server) stripPathPrefix(r *http.Request) (*http.Request, bool) {
	p := s.cfg.PathPrefix
	if p == "" {
		return r, true
	}
	p = strings.TrimSuffix(p, "/")
	path, ok := strings.CutPrefix(r.URL.Path, p)
	if !ok || (path != "" && path[0] != '/') {
		return nil, false
	}
	raw, _ := strings.CutPrefix(r.URL.RawPath, p)
	r2 := new(http.Request)
	*r2 = *r
	r2.URL = new(url.URL)
	*r2.URL = *r.URL
	r2.URL.Path, r2.URL.RawPath = path, raw
	return r2, true
}

// fetch is stage 9: it fetches key and, when want is set, checks the bytes
// against the request's hashsum (hashsum.go) before anything else sees
// them.
func (s *Server) fetch(ctx context.Context, key source.Key, opt source.FetchOptions, want *transform.Hashsum) (*fetch.Result, error) {
	ctx, span := s.deps.Tracer.Start(ctx, "fetch", trace.WithAttributes(attribute.String("xi.source", key.Source)))
	defer span.End()
	fr, err := s.deps.Fetcher.Fetch(ctx, key, opt)
	if err != nil {
		span.SetStatus(codes.Error, errs.KindOf(err).String())
		return nil, err
	}
	if fr == nil || fr.Buffer == nil {
		return nil, errs.New(errs.KindInternal, "server: fetcher returned no buffer")
	}
	span.SetAttributes(attribute.Int("xi.source_bytes", len(fr.Bytes())))
	if err := verifyHashsum(want, fr.Bytes()); err != nil {
		fr.Release()
		span.SetStatus(codes.Error, errs.KindOf(err).String())
		return nil, err
	}
	return fr, nil
}

func (s *Server) run(ctx context.Context, in *ProcessInput) (*ProcessOutput, error) {
	ctx, span := s.deps.Tracer.Start(ctx, "process", trace.WithAttributes(attribute.String("xi.format", in.Decision.Format.String())))
	defer span.End()
	out, err := s.deps.Processor.Process(ctx, in)
	if err != nil {
		span.SetStatus(codes.Error, errs.KindOf(err).String())
		return nil, err
	}
	if out == nil || out.Body == nil {
		return nil, errs.New(errs.KindInternal, "server: processor returned no output")
	}
	if len(out.Skipped) > 0 {
		s.deps.Logger.LogAttrs(ctx, slog.LevelDebug, "options not applied",
			slog.String("skipped", strings.Join(out.Skipped, ",")))
	}
	if out.RawPath != "" {
		s.deps.Metrics.ObserveRawDecode(out.RawPath)
		span.SetAttributes(attribute.String("xi.raw_path", out.RawPath))
	}
	if out.VideoFrame != "" {
		span.SetAttributes(attribute.String("xi.video_frame", out.VideoFrame))
	}
	return out, nil
}

// probe is stage 11. A designer container (OpenRaster, Krita, Procreate,
// Sketch, SVGZ, Paint.NET) is replaced by the image it carries, and an SVG
// document (magic-byte sniff) is sanitised into a new buffer before
// anything parses it, so neither the probe nor librsvg ever sees scripts,
// external references or entities (ARCHITECTURE §10).
// The returned buffer holds one reference owned by the caller.
func (s *Server) probe(ctx context.Context, req *transform.Request, buf fetch.Buffer, lim *ProbeLimits, maxSrcBytes int64) (fetch.Buffer, SourceMeta, error) {
	ctx, span := s.deps.Tracer.Start(ctx, "probe")
	defer span.End()

	src, cont, sanitised, err := s.prepare(buf, maxSrcBytes)
	if err != nil {
		span.SetStatus(codes.Error, errs.KindOf(err).String())
		return nil, SourceMeta{}, err
	}
	meta, err := s.deps.Prober.Probe(ctx, src, *lim)
	meta.Container = cont
	if err == nil && meta.Format == transform.FormatSVG && !sanitised {
		// The sniffer and the prober disagree: never hand an unsanitised
		// SVG to librsvg.
		err = errs.New(errs.KindUnsupported, "server: prober found svg the sniffer missed")
	}
	if err == nil {
		// A video sprite or animation is checked as what is assembled.
		videoComposite(req, &meta, s.cfg.Limits.MaxVideoCompositeFrames)
		err = checkMeta(&meta, lim)
	}
	if err != nil {
		meta.closeSession()
		src.Release()
		span.SetStatus(codes.Error, errs.KindOf(err).String())
		return nil, SourceMeta{}, err
	}
	span.SetAttributes(attribute.String("xi.source_format", meta.Format.String()))
	if cont != nil {
		span.SetAttributes(attribute.String("xi.container", cont.Kind.String()))
	}
	return src, meta, nil
}

// prepare is what every parser of a source sees first: a designer
// container replaced by the image it carries (unwrap), then an SVG
// document sanitised (sanitize). The returned buffer holds one reference
// owned by the caller; cont is nil unless a container was unwrapped.
func (s *Server) prepare(buf fetch.Buffer, maxSrcBytes int64) (_ fetch.Buffer, cont *container.Source, sanitised bool, _ error) {
	inner, cont, err := s.unwrap(buf, maxSrcBytes)
	if err != nil {
		return nil, nil, false, err
	}
	defer inner.Release()
	src, sanitised, err := s.sanitize(inner)
	if err != nil {
		return nil, nil, false, err
	}
	return src, cont, sanitised, nil
}

// sanitize returns buf with one extra reference, or a sanitised copy when
// buf is an SVG document.
func (s *Server) sanitize(buf fetch.Buffer) (fetch.Buffer, bool, error) {
	data := buf.Bytes()
	if !svg.IsSVG(data) {
		buf.Retain()
		return buf, false, nil
	}
	// Cold path, bounded by XI_LIMITS_MAX_SVG_BYTES: sanitise into Go
	// memory, then copy into an allocator buffer (C memory in production).
	var clean bytes.Buffer
	clean.Grow(len(data))
	if err := svg.Sanitize(&clean, bytes.NewReader(data), s.cfg.SVG); err != nil {
		return nil, false, svgError(err)
	}
	out, err := s.deps.Allocator(clean.Len())
	if err != nil {
		return nil, false, errs.Wrapf(errs.KindInternal, err, "server: allocate sanitised svg")
	}
	copy(out.Bytes(), clean.Bytes())
	return out, true, nil
}

// svgError classifies a sanitiser failure: limits are 422 LimitExceeded,
// everything else (malformed, DTD, not an svg root) 422 Corrupt.
func svgError(err error) error {
	if errs.KindOf(err) != errs.KindInternal {
		return err
	}
	switch {
	case errorsIsAny(err, svg.ErrTooLarge, svg.ErrTooDeep, svg.ErrTooManyElements):
		return errs.Wrapf(errs.KindLimitExceeded, err, "server: sanitise svg")
	default:
		return errs.Wrapf(errs.KindCorrupt, err, "server: sanitise svg")
	}
}

// checkMeta enforces the input limits on the probed header, independent of
// whether the prober already did (defence in depth, and fakes in tests):
// the frame count, and one frame against the limits that could admit it
// (transform.SourceLimits.CheckFrame). Which frames are loaded depends on
// the output format and the request, so the totals over frames (imgproxy's
// CheckDimensions, and XI_LIMITS_MAX_ANIMATION_PIXELS) are the planner's,
// before anything is decoded. A frame limit of 1 disables animation (one
// frame is processed, as in imgproxy) instead of rejecting animations. A
// camera RAW passes when one of its decode paths fits
// (transform.SourceLimits.CheckRaw).
func checkMeta(m *SourceMeta, lim *ProbeLimits) error {
	if m.Width <= 0 || m.Height <= 0 {
		return errs.Newf(errs.KindCorrupt, "server: probe returned %dx%d", m.Width, m.Height)
	}
	frames := max(m.Frames, 1)
	if le := transform.CheckFrames(frames, lim.MaxFrames); le != nil {
		return limitExceeded(le)
	}
	src := transform.SourceLimits{MaxSrcPixels: lim.MaxPixels, MaxFramePixels: lim.MaxFramePixels}
	if m.Raw != nil {
		// A camera RAW is judged on what it can be decoded to (its
		// preview or half-size demosaic); the planner checks the path the
		// request takes.
		if le := src.CheckRaw(m.Raw); le != nil {
			return limitExceeded(le)
		}
		return nil
	}
	if le := src.CheckFrame(m.Width, m.Height, frames > 1 && lim.MaxFrames != 1); le != nil {
		return limitExceeded(le)
	}
	return nil
}

// limitExceeded is the 422 of a source limit, with its message for the
// client.
func limitExceeded(le *transform.LimitError) error {
	return errs.WithPublic(errs.Wrapf(errs.KindLimitExceeded, le, "server"), le.Public)
}

// checkResultDimension rejects requests whose explicit target size exceeds
// XI_LIMITS_MAX_RESULT_DIMENSION (stage 6: 422). Sizes derived from the
// source are checked again by the planner.
func checkResultDimension(req *transform.Request, maxDim int) error {
	if maxDim <= 0 {
		return nil
	}
	for i := range req.Pipelines {
		rs := &req.Pipelines[i].Resize
		dpr := max(rs.DPR, 1)
		for _, px := range []int{rs.Width.Px, rs.Height.Px, rs.MinWidth, rs.MinHeight} {
			if float64(px)*dpr > float64(maxDim) {
				return errs.Newf(errs.KindLimitExceeded, "server: requested dimension %d at dpr %g exceeds %d", px, dpr, maxDim)
			}
		}
	}
	return nil
}

// requestLimits are the effective limits of one request.
type requestLimits struct {
	maxSrcBytes        int64
	maxResultDimension int
	probe              ProbeLimits
}

// limits merges the request's security options into the configured
// limits: an override may lower a limit, and raise it only with
// AllowRaising. The dialect already refused the options unless
// XI_SECURITY_ALLOW_SECURITY_OPTIONS is on.
func (s *Server) limits(o *transform.LimitOverrides) requestLimits {
	c := &s.cfg.Limits
	mp := func(v float64) int64 { return int64(v * 1e6) }
	return requestLimits{
		maxSrcBytes:        override(c.MaxSrcBytes, o.MaxSrcFileSize, c.AllowRaising),
		maxResultDimension: override(c.MaxResultDimension, o.MaxResultDimension, c.AllowRaising),
		probe: ProbeLimits{
			MaxPixels:      override(c.MaxSrcPixels, mp(o.MaxSrcResolution), c.AllowRaising),
			MaxFrames:      override(c.MaxAnimationFrames, o.MaxAnimationFrames, c.AllowRaising),
			MaxFramePixels: override(c.MaxAnimationFramePixels, mp(o.MaxAnimationFrameResolution), c.AllowRaising),
		},
	}
}

// override applies a per-request limit v (0 = unset) to the configured
// limit c (0 = unlimited).
func override[T int | int64](c, v T, allowRaising bool) T {
	switch {
	case v <= 0:
		return c
	case allowRaising || c <= 0: // raising, or lowering "unlimited"
		return v
	default:
		return min(c, v)
	}
}

func errorsIsAny(err error, targets ...error) bool {
	for _, t := range targets {
		if errors.Is(err, t) {
			return true
		}
	}
	return false
}

// resolve maps spec to a source key through the allowlist. playground://
// sources (source.PlaygroundSourceName) exist only for the playground's
// own requests: anyone else gets a 404, before the result cache could
// answer from a rendition the playground made.
func (s *Server) resolve(ctx context.Context, spec transform.SourceSpec) (source.Key, error) {
	key, err := s.deps.Resolver.Resolve(spec)
	if err == nil && key.Source == source.PlaygroundSourceName && !source.IsPlaygroundRequest(ctx) {
		return source.Key{}, errs.New(errs.KindNotFound, "playground source outside the playground")
	}
	return key, err
}
