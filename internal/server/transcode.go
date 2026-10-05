package server

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/source"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
)

// Video transformations (WP-121, ADR 0012): a transform.KindVideo request
// transcodes a video source (Deps.Video, internal/media) instead of
// processing an image. The lifecycle is the image one with these
// differences:
//
//   - the budget is Config.Video.Budget (fetch + queue + transcode
//     timeouts) instead of RequestTimeout, and the write deadline is
//     extended to match (http.ResponseController);
//   - the output (container and codecs) is resolved from the URL before the
//     fetch, so an output this image cannot write is a 422 that fetches
//     nothing, and the ETag and cache key know it;
//   - there is no probe, negotiation or fallback image; admission is the
//     video pool's, inside Deps.Video, which also coalesces identical
//     transcodes;
//   - the images of image overlays (WP-124) are fetched like watermark
//     images, after the cache lookup and only when a transcode runs;
//   - the response supports byte ranges (Accept-Ranges, Range → 206,
//     If-Range, 416) through http.ServeContent, so players can seek;
//   - a HEAD that misses the result cache is answered from the fetch, like
//     an image HEAD: every header but Content-Length, without transcoding.
//
// Results go through the result cache like images. Its memory tier holds
// whole files: large outputs belong in the disk or S3 tiers (CONFIG.md).

// VideoTransformer runs video transformations; internal/media.Service
// (adapted by app) implements it.
type VideoTransformer interface {
	// Resolve picks the output of op on this build before anything is
	// fetched: errs.KindUnsupported when it cannot be written.
	Resolve(op *transform.VideoOp) (video.TranscodeOutput, error)
	// Transform applies op to src. overlays[i] is the fetched image of
	// image overlay op.Overlays[i] (nil for the others; nil when there are
	// none). Calls with the same key share one transcode. The caller
	// releases the result's body.
	Transform(ctx context.Context, key string, src fetch.Buffer, op *transform.VideoOp, overlays []fetch.Buffer) (*VideoResult, error)
}

// VideoResult is a transcoded file.
type VideoResult struct {
	Body          cache.Buffer
	Output        video.TranscodeOutput
	Width, Height int
	// Shared reports a result another request's transcode produced.
	Shared bool
}

// VideoConfig configures video transformations (XI_VIDEO_TRANSFORM_*).
type VideoConfig struct {
	// Budget is a video request's whole deadline: the fetch, the wait for
	// a video worker and the transcode.
	Budget time.Duration
	// MaxSrcBytes caps a video source at fetch
	// (XI_VIDEO_TRANSFORM_MAX_INPUT_BYTES); 0 is the image limit.
	MaxSrcBytes int64
	// AllowUnsigned leaves unsigned video transformations to the mount's
	// unsigned policy (XI_VIDEO_TRANSFORM_UNSIGNED_POLICY=allow); false
	// (the default) answers them 403 whatever that policy is (ADR 0012 §8).
	AllowUnsigned bool
}

// errVideoUnsigned is the 403 of an unsigned video transformation while
// XI_VIDEO_TRANSFORM_UNSIGNED_POLICY is deny.
func errVideoUnsigned() error {
	return &errs.Error{
		Kind: errs.KindForbidden, Public: "unsigned request not allowed",
		Msg: "server: unsigned video transformation (video.transform.unsigned_policy deny)",
	}
}

// Role is what an instance serves (XI_ROLE).
type Role uint8

// Roles. The zero value serves everything.
const (
	RoleAll   Role = iota
	RoleImage      // no video transformations
	RoleVideo      // video transformations only
)

// Images reports whether r serves image (and info and raw) requests.
func (r Role) Images() bool { return r != RoleVideo }

// Videos reports whether r serves video transformations.
func (r Role) Videos() bool { return r != RoleImage }

// errNotServed is the 404 of a request this instance's role (or its
// configuration) does not serve: a load balancer routes it elsewhere.
func errNotServed(msg string) error {
	return &errs.Error{Kind: errs.KindNotFound, Public: "not served by this instance", Msg: msg}
}

// videoTransform serves a KindVideo request.
func (s *Server) videoTransform(w http.ResponseWriter, r *http.Request, info *requestInfo, parsed *routed) {
	budget := s.cfg.Video.Budget
	if budget <= 0 {
		budget = s.cfg.RequestTimeout
	}
	ctx, cancel := context.WithTimeoutCause(r.Context(), budget, errRequestTimeout)
	defer cancel()
	// The response is written after the transcode: the server-wide write
	// timeout (sized for images) would cut it.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(budget + s.cfg.WriteTimeout))

	res, err := s.videoProcess(ctx, r, info, parsed)
	if info.cache != "" && s.cfg.DebugHeaders {
		w.Header().Set(hdrXiCache, info.cache)
	}
	if err != nil {
		s.writeError(w, r, classify(ctx, r, err))
		return
	}
	if res.out != nil {
		defer res.out.Body.Release()
	}
	t := s.stage(info, stageRespond)
	s.respondVideo(w, r, res)
	t.done()
	if body, ok := res.out.bodyBuffer(); ok && r.Method == http.MethodGet {
		s.cacheStore(ctx, r, res, body, w.Header())
	}
}

// videoProcess runs the stages of a video transformation (see above).
func (s *Server) videoProcess(ctx context.Context, r *http.Request, info *requestInfo, parsed *routed) (*imageResult, error) {
	if !s.cfg.Role.Videos() || s.deps.Video == nil {
		return nil, errNotServed("server: video transformations are not served by this instance")
	}
	req := parsed.Request
	signed := parsed.Signature.Signed()
	if !signed && !s.cfg.Video.AllowUnsigned {
		return nil, errVideoUnsigned()
	}
	if e := req.Delivery.Expires; !e.IsZero() && !s.now().Before(e) {
		return nil, errs.New(errs.KindExpired, "server: url expired")
	}

	t := s.stage(info, stageNormalize)
	req.ApplyDefaults(s.cfg.Defaults)
	err := req.Validate()
	var out video.TranscodeOutput
	var vary negotiate.VarySet
	if err == nil {
		vary = s.videoContainer(r, req.Video)
		out, err = s.deps.Video.Resolve(req.Video)
	}
	t.done()
	if err != nil {
		return nil, err
	}

	t = s.stage(info, stageResolve)
	key, err := s.resolve(ctx, req.Source)
	t.done()
	if err != nil {
		return nil, err
	}
	info.source = key.Source
	info.format = out.Container.String()
	lim := s.limits(&req.Limits)
	j := &job{req: req, key: key, lim: lim, signed: signed, hashsum: req.Source.Hashsum, video: out.Key()}

	if res, ok := s.cacheLookup(ctx, r, info, j); ok {
		return res, nil
	}

	t = s.stage(info, stageFetch)
	maxBytes := lim.maxSrcBytes
	if v := s.cfg.Video.MaxSrcBytes; v > 0 && (maxBytes <= 0 || v > maxBytes) {
		maxBytes = v
	}
	fr, err := s.fetch(ctx, key, source.FetchOptions{MaxBytes: maxBytes, Header: r.Header}, j.hashsum)
	t.done()
	if err != nil {
		return nil, err
	}
	defer fr.Release()

	res := newResult(j, fr)
	res.decision.Vary = vary
	res.cacheKey = j.cacheKey
	res.ext = strings.TrimPrefix(out.Ext, ".")
	res.mediaType = out.ContentType
	res.etag = etag(req.CanonicalKey()+"\x00video:"+out.Key(), transform.FormatUnset, 0, &fr.Object, fr.Bytes(), s.cfg.ETagBuster)
	if res.notModified = notModified(r, res.etag, fr.Object.LastModified); res.notModified {
		return res, nil
	}
	if r.Method == http.MethodHead {
		return res, nil
	}

	if err := ctx.Err(); err != nil {
		return nil, errs.Wrap(errs.KindOf(err), context.Cause(ctx))
	}
	overlays, err := s.fetchOverlays(ctx, req.Video, lim.maxSrcBytes)
	if err != nil {
		return nil, err
	}
	defer releaseBuffers(overlays)
	t = s.stage(info, stageProcess)
	vr, err := s.transcode(ctx, s.videoKey(r, j), fr.Buffer, req.Video, overlays)
	t.done()
	if err != nil {
		return nil, err
	}
	res.out = &ProcessOutput{
		Body: vr.Body, ContentType: vr.Output.ContentType,
		Width: vr.Width, Height: vr.Height, VideoCodec: videoCodecs(vr.Output),
	}
	info.width, info.height = vr.Width, vr.Height
	return res, nil
}

// videoContainer resolves an automatic container (Cloudinary f_auto,
// ImageKit f-auto): WebM when the client names video/webm in Accept and
// this build can write the request as WebM, otherwise the request's MP4.
// It returns the Vary members the choice depends on.
func (s *Server) videoContainer(r *http.Request, op *transform.VideoOp) negotiate.VarySet {
	if !op.AutoContainer {
		return 0
	}
	webm := *op
	webm.Container = transform.VideoContainerWebM
	if webm.Container.AllowsVideo(op.VideoCodec) && webm.Container.AllowsAudio(op.AudioCodec) &&
		negotiate.ParseAcceptHeader(r.Header).Explicit("video", "webm") > 0 {
		if _, err := s.deps.Video.Resolve(&webm); err == nil {
			op.Container = transform.VideoContainerWebM
		}
	}
	return negotiate.VaryAccept
}

// transcode runs Deps.Video.Transform in its span.
func (s *Server) transcode(ctx context.Context, key string, src fetch.Buffer, op *transform.VideoOp, overlays []fetch.Buffer) (*VideoResult, error) {
	ctx, span := s.deps.Tracer.Start(ctx, "transcode")
	defer span.End()
	vr, err := s.deps.Video.Transform(ctx, key, src, op, overlays)
	if err != nil {
		span.SetStatus(codes.Error, errs.KindOf(err).String())
		return nil, err
	}
	if vr == nil || vr.Body == nil {
		return nil, errs.New(errs.KindInternal, "server: video transformer returned no output")
	}
	span.SetAttributes(attribute.String("xi.video_output", vr.Output.Key()),
		attribute.Int("xi.result_bytes", len(vr.Body.Bytes())), attribute.Bool("xi.coalesced", vr.Shared))
	return vr, nil
}

// videoKey identifies a transcode for coalescing: the result cache key,
// computed whether the cache is on or not.
func (s *Server) videoKey(r *http.Request, j *job) string {
	if j.cacheKey != "" {
		return j.cacheKey
	}
	return s.resultKey(r, j)
}

// videoCodecs is the X-Xi-Video-Codec value: "av1+aac", "vp9", "opus".
func videoCodecs(o video.TranscodeOutput) string {
	v, a := o.Video.String(), o.Audio.String()
	switch {
	case v != "" && a != "":
		return v + "+" + a
	case v != "":
		return v
	default:
		return a
	}
}

// respondVideo writes a video result: the image response's caching
// headers, then the body through http.ServeContent, which answers Range
// (206, multipart for several ranges), If-Range and unsatisfiable ranges
// (416) on the file's bytes.
func (s *Server) respondVideo(w http.ResponseWriter, r *http.Request, res *imageResult) {
	h := w.Header()
	s.setCacheHeaders(h, res)
	if res.notModified {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", res.contentType())
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Accept-Ranges", "bytes")
	h.Set("Content-Disposition", contentDispositionExt(res.req.Delivery.Filename, res.key.Path, "."+res.ext,
		attachment(&res.req.Delivery)))
	if s.cfg.DebugHeaders {
		setDebugHeaders(h, res)
	}
	if res.out == nil { // HEAD before the transcode
		w.WriteHeader(http.StatusOK)
		return
	}
	// ServeContent sets Content-Length, Content-Range and the status;
	// the ETag above is its If-Range validator.
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(res.out.Body.Bytes()))
}

// bodyBuffer is the output's body as a cache buffer (a transcoded file
// or a cache hit's entry); ok is false without one.
func (o *ProcessOutput) bodyBuffer() (cache.Buffer, bool) {
	if o == nil || o.Body == nil {
		return nil, false
	}
	b, ok := o.Body.(cache.Buffer)
	return b, ok
}
