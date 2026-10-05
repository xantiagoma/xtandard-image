package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/media/fmp4"
	"github.com/xantiagoma/xtandard-image/internal/media/manifest"
	"github.com/xantiagoma/xtandard-image/internal/obs/logging"
	"github.com/xantiagoma/xtandard-image/internal/source"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
)

// HLS and DASH (WP-126, ADR 0012 §6).
//
// A KindVideo request whose VideoOp has a Stream is answered with a
// manifest (an HLS master playlist or a DASH MPD) instead of a file. Its
// URL is a dialect's, signed as usual; everything it lists lives under
//
//	<path prefix>/_stream/<ticket>/<part>
//
// where the ticket (internal/security/ticket) seals the stream's source
// and options with an expiry, so a player fetches the media playlists,
// init segments and media segments without a signing key, and nothing but
// that stream's parts. The parts are internal/media/manifest's.
//
// Everything a manifest says comes from the probe alone, cached under the
// stream's key (the source is fetched and probed once). A media segment
// is transcoded on request (internal/media: one segment of one
// rendition, or the whole audio track, cut into its segments), cached in
// the stream cache (memory, Deps.StreamCache) and the result cache
// (persistent tiers), and the next Config.Stream.Prefetch segments of the
// rendition are transcoded ahead on idle video workers. Coalescing,
// admission, limits and timeouts are the video pool's.

// StreamService runs HLS and DASH: internal/media.Service, adapted by
// app.
type StreamService interface {
	// ResolveStream picks a stream's codecs on this build before anything
	// is fetched: errs.KindUnsupported when it cannot be written.
	ResolveStream(op *transform.VideoOp) (video.TranscodeOutput, error)
	// Probe reads what src holds (in the video pool).
	Probe(ctx context.Context, src fetch.Buffer) (video.TranscodeInput, error)
	// Layout lays op out over a probed source.
	Layout(in *video.TranscodeInput, op *transform.VideoOp) (*video.StreamLayout, error)
	// Stream transcodes part of the stream op of src into fragmented MP4
	// (calls with the same key share one transcode). The caller releases
	// the body.
	Stream(ctx context.Context, key string, src fetch.Buffer, op *transform.VideoOp, part StreamPart) (*VideoResult, error)
	// Prefetch runs Stream in the background on an idle worker, if any,
	// and hands the result (or nil) to done, which owns its body. It
	// reports whether it started.
	Prefetch(key string, src fetch.Buffer, op *transform.VideoOp, part StreamPart, done func(*VideoResult)) bool
}

// StreamPart is what one stream transcode writes: a video segment of
// rendition Track, or the audio track (Track "").
type StreamPart struct {
	Track   string
	Segment int
}

// TicketSealer seals and opens the tickets of child URLs
// (internal/security/ticket.Sealer).
type TicketSealer interface {
	Seal(payload []byte, exp time.Time) string
	// Open fails with errs.KindForbidden for a ticket that does not
	// authenticate and errs.KindExpired past its expiry.
	Open(tok string, now time.Time) ([]byte, time.Time, error)
}

// StreamConfig configures HLS and DASH (XI_VIDEO_TRANSFORM_STREAM_TTL,
// XI_VIDEO_TRANSFORM_PREFETCH_SEGMENTS).
type StreamConfig struct {
	// TTL is how long a manifest's child URLs work.
	TTL time.Duration
	// Prefetch is how many segments after a requested one are transcoded
	// ahead.
	Prefetch int
}

// StreamPath is the path of child URLs, under the path prefix.
const StreamPath = "/_stream/"

// streamCacheTTL bounds the stream cache entries' lifetime when the
// result cache sets none.
const streamCacheTTL = time.Hour

// ticketPayload is what a ticket seals: the stream's request.
type ticketPayload struct {
	Source transform.SourceSpec `json:"s"`
	Video  transform.VideoOp    `json:"v"`
}

// ticketJSON is the encoding of ticket payloads: zero fields omitted (a
// ticket carries only what the URL set), durations in nanoseconds.
func ticketJSON() jsonv2.Options {
	return jsonv2.JoinOptions(jsonv2.OmitZeroStructFields(true), json.FormatDurationAsNano(true))
}

// streamRequest is the request a stream's keys and tickets are made of:
// its source and video options, nothing request-specific.
func streamRequest(spec transform.SourceSpec, op *transform.VideoOp) *transform.Request {
	spec.Password = nil
	v := *op
	return &transform.Request{Kind: transform.KindVideo, Source: spec, Video: &v}
}

// streamJob is one master or child request of a stream.
type streamJob struct {
	req       *transform.Request
	canonical string
	key       source.Key
	out       video.TranscodeOutput
	layout    *video.StreamLayout
	tok       string
	exp       time.Time
	// src is the fetched source once a part needed it; release drops it.
	src *fetch.Result
}

func (j *streamJob) release() {
	if j.src != nil {
		j.src.Release()
	}
}

// streamChildPath returns the part of r's path after StreamPath (and the
// path prefix); ok is false for any other path.
func (s *Server) streamChildPath(r *http.Request) (string, bool) {
	rr, ok := s.stripPathPrefix(r)
	if !ok {
		return "", false
	}
	return strings.CutPrefix(rr.URL.Path, StreamPath)
}

// streamBudget is a stream request's context: the video budget, with the
// write deadline extended to match.
func (s *Server) streamBudget(w http.ResponseWriter, r *http.Request) (context.Context, context.CancelFunc) {
	budget := s.cfg.Video.Budget
	if budget <= 0 {
		budget = s.cfg.RequestTimeout
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(budget + s.cfg.WriteTimeout))
	return context.WithTimeoutCause(r.Context(), budget, errRequestTimeout)
}

// streamServed fails unless this instance serves streams.
func (s *Server) streamServed() error {
	if !s.cfg.Role.Videos() || s.deps.Stream == nil || s.deps.Tickets == nil {
		return errNotServed("server: streams are not served by this instance")
	}
	return nil
}

// streamMaster answers a stream's dialect URL with its manifest.
func (s *Server) streamMaster(w http.ResponseWriter, r *http.Request, info *requestInfo, parsed *routed) {
	ctx, cancel := s.streamBudget(w, r)
	defer cancel()
	j, err := s.streamMasterJob(ctx, r, info, parsed)
	if err == nil {
		defer j.release()
		err = s.respondManifest(w, r, j, j.req.Video.Stream.Protocol == transform.StreamDASH)
	}
	if err != nil {
		s.writeError(w, r, classify(ctx, r, err))
	}
}

// streamMasterJob checks a stream's dialect request like a video
// transformation, lays the stream out and seals its ticket.
func (s *Server) streamMasterJob(ctx context.Context, r *http.Request, info *requestInfo, parsed *routed) (*streamJob, error) {
	if err := s.streamServed(); err != nil {
		return nil, err
	}
	req := parsed.Request
	if !parsed.Signature.Signed() && !s.cfg.Video.AllowUnsigned {
		return nil, errVideoUnsigned()
	}
	if e := req.Delivery.Expires; !e.IsZero() && !s.now().Before(e) {
		return nil, errs.New(errs.KindExpired, "server: url expired")
	}
	t := s.stage(info, stageNormalize)
	req.ApplyDefaults(s.cfg.Defaults)
	err := req.Validate()
	t.done()
	if err != nil {
		return nil, err
	}
	j := &streamJob{req: streamRequest(req.Source, req.Video)}
	if err := s.streamPrepare(ctx, r, info, j); err != nil {
		j.release()
		return nil, err
	}
	// Zero fields omitted: a ticket carries only what the URL set.
	payload, err := jsonv2.Marshal(ticketPayload{Source: j.req.Source, Video: *j.req.Video}, ticketJSON())
	if err != nil {
		j.release()
		return nil, errs.Wrapf(errs.KindInternal, err, "server: stream ticket")
	}
	// The expiry moves in steps of a quarter of the TTL, so the manifest
	// (and the URLs it lists) stays the same for a while: caches keep it.
	now := s.now()
	ttl := max(s.cfg.Stream.TTL, time.Minute)
	j.exp = now.Truncate(ttl / 4).Add(ttl)
	if e := req.Delivery.Expires; !e.IsZero() && e.Before(j.exp) {
		j.exp = e
	}
	j.tok = s.deps.Tickets.Seal(payload, j.exp)
	return j, nil
}

// streamChild serves a child URL: a manifest, a playlist or a segment.
func (s *Server) streamChild(w http.ResponseWriter, r *http.Request, info *requestInfo, rest string) {
	info.dialect = "stream"
	ctx, cancel := s.streamBudget(w, r)
	defer cancel()
	if err := s.serveStreamChild(ctx, w, r, info, rest); err != nil {
		s.writeError(w, r, classify(ctx, r, err))
	}
}

// errNoPart is the 404 of a child path that names nothing.
func errNoPart() error {
	return &errs.Error{Kind: errs.KindNotFound, Public: "not found", Msg: "server: no such stream part"}
}

// serveStreamChild writes the part rest names, or returns an error before
// writing anything.
func (s *Server) serveStreamChild(ctx context.Context, w http.ResponseWriter, r *http.Request, info *requestInfo, rest string) error {
	if err := s.streamServed(); err != nil {
		return err
	}
	tok, path, ok := strings.Cut(rest, "/")
	if !ok || tok == "" {
		return errNoPart()
	}
	payload, exp, err := s.deps.Tickets.Open(tok, s.now())
	if err != nil {
		return err
	}
	var tp ticketPayload
	if err := jsonv2.Unmarshal(payload, &tp, ticketJSON()); err != nil || tp.Video.Stream == nil {
		return &errs.Error{Kind: errs.KindForbidden, Public: "invalid stream url", Msg: "server: unreadable stream ticket"}
	}
	part, err := manifest.ParsePart(path)
	if err != nil {
		return errNoPart()
	}
	j := &streamJob{req: streamRequest(tp.Source, &tp.Video), tok: tok, exp: exp}
	defer j.release()
	if err := j.req.Video.Validate(); err != nil {
		return err
	}
	if err := s.streamPrepare(ctx, r, info, j); err != nil {
		return err
	}
	switch part.Kind {
	case manifest.PartMaster, manifest.PartMPD:
		return s.respondManifest(w, r, j, part.Kind == manifest.PartMPD)
	case manifest.PartPlaylist:
		pl, ok := manifest.HLSMedia(j.layout, part.Track)
		if !ok {
			return errNoPart()
		}
		info.format = "m3u8"
		s.respondStreamFile(w, r, j, pl, manifest.ContentTypeHLS, "")
		return nil
	default:
		body, ct, err := s.streamSegment(ctx, r, info, j, part)
		if err != nil {
			return err
		}
		defer body.Release()
		info.format = "m4s"
		if s.cfg.DebugHeaders && info.cache != "" {
			w.Header().Set(hdrXiCache, info.cache)
		}
		s.respondStreamFile(w, r, j, body.Bytes(), ct, s.streamKey(j, part.Path()))
		return nil
	}
}

// streamPrepare resolves j's output and source and lays the stream out.
func (s *Server) streamPrepare(ctx context.Context, r *http.Request, info *requestInfo, j *streamJob) error {
	var err error
	if j.out, err = s.deps.Stream.ResolveStream(j.req.Video); err != nil {
		return err
	}
	t := s.stage(info, stageResolve)
	j.key, err = s.resolve(ctx, j.req.Source)
	t.done()
	if err != nil {
		return err
	}
	info.source = j.key.Source
	j.canonical = j.req.CanonicalKey()
	j.layout, err = s.streamLayout(ctx, r, info, j)
	return err
}

// streamLayout lays j's stream out from its probe: cached, or read from
// the fetched source and cached.
func (s *Server) streamLayout(ctx context.Context, r *http.Request, info *requestInfo, j *streamJob) (*video.StreamLayout, error) {
	key := s.streamKey(j, "probe")
	var in video.TranscodeInput
	if b, ok := s.streamGet(ctx, key); ok {
		err := json.Unmarshal(b.Bytes(), &in)
		b.Release()
		if err == nil {
			return s.deps.Stream.Layout(&in, j.req.Video)
		}
	}
	src, err := s.streamSource(ctx, r, info, j)
	if err != nil {
		return nil, err
	}
	t := s.stage(info, stageProcess)
	in, err = s.deps.Stream.Probe(ctx, src)
	t.done()
	if err != nil {
		return nil, err
	}
	if b, err := json.Marshal(in); err == nil {
		s.streamPut(ctx, key, cache.NewBuffer(b, nil), "application/json")
	}
	return s.deps.Stream.Layout(&in, j.req.Video)
}

// streamSource fetches j's source once.
func (s *Server) streamSource(ctx context.Context, r *http.Request, info *requestInfo, j *streamJob) (fetch.Buffer, error) {
	if j.src != nil {
		return j.src.Buffer, nil
	}
	lim := s.limits(&j.req.Limits)
	maxBytes := lim.maxSrcBytes
	if v := s.cfg.Video.MaxSrcBytes; v > 0 && (maxBytes <= 0 || v > maxBytes) {
		maxBytes = v
	}
	t := s.stage(info, stageFetch)
	fr, err := s.fetch(ctx, j.key, source.FetchOptions{MaxBytes: maxBytes, Header: r.Header}, j.req.Source.Hashsum)
	t.done()
	if err != nil {
		return nil, err
	}
	j.src = fr
	return fr.Buffer, nil
}

// streamKey is the cache and coalescing key of part of j's stream: the
// result key of its request with the stream's codecs and the part.
func (s *Server) streamKey(j *streamJob, part string) string {
	k := cache.ResultKey{
		Canonical: j.canonical,
		Source:    j.key.Source + "\x00" + j.key.Bucket + "\x00" + j.key.Path + "\x00" + j.key.Version,
		Format:    j.out.StreamKey() + ":" + part,
		Buster:    s.cfg.ETagBuster,
		Build:     s.cfg.ResultCache.Build,
	}
	return k.String()
}

// streamGet looks key up in the stream cache, then the result cache
// (copying a hit into the stream cache). The caller releases the body.
func (s *Server) streamGet(ctx context.Context, key string) (cache.Buffer, bool) {
	for i, c := range []cache.Store{s.deps.StreamCache, s.deps.ResultCache} {
		if c == nil {
			continue
		}
		e, ok, err := c.Get(ctx, cache.NSResult, key)
		if err != nil {
			s.deps.Logger.LogAttrs(ctx, slog.LevelWarn, "stream cache lookup failed", logging.Err(err))
		}
		if !ok || e.Body == nil {
			continue
		}
		if i > 0 && s.deps.StreamCache != nil {
			_ = s.deps.StreamCache.Set(ctx, cache.NSResult, key, &e, streamCacheTTL)
		}
		return e.Body, true
	}
	return nil, false
}

// streamHas reports whether the stream cache holds key.
func (s *Server) streamHas(ctx context.Context, key string) bool {
	if s.deps.StreamCache == nil {
		return false
	}
	e, ok, _ := s.deps.StreamCache.Get(ctx, cache.NSResult, key)
	if ok {
		e.Release()
	}
	return ok
}

// streamPut stores body under key in both caches; the caller keeps its
// reference.
func (s *Server) streamPut(ctx context.Context, key string, body cache.Buffer, contentType string) {
	e := cache.Entry{Body: body, Status: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, StoredAt: s.now()}
	if c := s.deps.StreamCache; c != nil {
		ttl := s.cfg.ResultCache.TTL
		if ttl <= 0 {
			ttl = streamCacheTTL
		}
		_ = c.Set(ctx, cache.NSResult, key, &e, ttl)
	}
	if c := s.deps.ResultCache; c != nil && s.cfg.ResultCache.TTL > 0 {
		if err := c.Set(ctx, cache.NSResult, key, &e, s.cfg.ResultCache.TTL); err != nil {
			s.deps.Logger.LogAttrs(ctx, slog.LevelDebug, "result cache write not queued", logging.Err(err))
		}
	}
}

// subBuffer is b, a slice of parent's bytes, holding a reference to
// parent.
func subBuffer(parent cache.Buffer, b []byte) cache.Buffer {
	parent.Retain()
	return cache.NewBuffer(b, parent.Release)
}

// streamSegment returns an init or media segment of j's stream: cached,
// or transcoded (a video segment with its init segment; the audio track
// with all of its segments), cached and, for video, followed by the
// prefetch of the next segments. The caller releases the body.
func (s *Server) streamSegment(ctx context.Context, r *http.Request, info *requestInfo, j *streamJob, part manifest.Part) (cache.Buffer, string, error) {
	l := j.layout
	n := part.Segment
	if part.Kind == manifest.PartSegment && n >= l.Segments {
		return nil, "", errNoPart()
	}
	if part.Track == manifest.Audio {
		if l.Audio == nil {
			return nil, "", errNoPart()
		}
		b, err := s.streamAudio(ctx, r, info, j, part)
		return b, manifest.ContentTypeM4A, err
	}
	rd, ok := l.Rendition(part.Track)
	if !ok {
		return nil, "", errNoPart()
	}
	info.width, info.height = rd.Width, rd.Height
	if b, ok := s.streamGet(ctx, s.streamKey(j, part.Path())); ok {
		info.cache = cacheHit
		return b, manifest.ContentTypeMP4, nil
	}
	info.cache = cacheMiss
	if part.Kind == manifest.PartInit {
		n = 0 // the init segment comes with segment 0
	}
	src, err := s.streamSource(ctx, r, info, j)
	if err != nil {
		return nil, "", err
	}
	seg := manifest.Part{Kind: manifest.PartSegment, Track: part.Track, Segment: n}
	t := s.stage(info, stageProcess)
	res, err := s.deps.Stream.Stream(ctx, s.streamKey(j, seg.Path()), src, j.req.Video, StreamPart{Track: part.Track, Segment: n})
	t.done()
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Release()
	initSeg, media, err := s.storeSegment(ctx, j, seg, res.Body)
	if err != nil {
		return nil, "", err
	}
	s.prefetch(j, part.Track, n)
	if part.Kind == manifest.PartInit {
		media.Release()
		return initSeg, manifest.ContentTypeMP4, nil
	}
	initSeg.Release()
	return media, manifest.ContentTypeMP4, nil
}

// storeSegment cuts a video segment's transcode into its init segment and
// its media segment and caches both. The caller releases them.
func (s *Server) storeSegment(ctx context.Context, j *streamJob, seg manifest.Part, body cache.Buffer) (initSeg, media cache.Buffer, err error) {
	f, err := fmp4.Parse(body.Bytes())
	if err != nil || len(f.Fragments) != 1 {
		return nil, nil, errs.Newf(errs.KindInternal, "server: segment %s: %d fragments, %v", seg.Path(), len(f.Fragments), err)
	}
	initSeg, media = subBuffer(body, f.Init), subBuffer(body, f.Fragments[0].Data)
	s.streamPut(ctx, s.streamKey(j, manifest.Part{Kind: manifest.PartInit, Track: seg.Track}.Path()), initSeg, manifest.ContentTypeMP4)
	s.streamPut(ctx, s.streamKey(j, seg.Path()), media, manifest.ContentTypeMP4)
	return initSeg, media, nil
}

// prefetch transcodes, on idle video workers, the segments of a rendition
// after segment n that the stream cache lacks (at most
// Config.Stream.Prefetch of them), from the source this request fetched.
func (s *Server) prefetch(j *streamJob, track string, n int) {
	if j.src == nil {
		return
	}
	// Detached from the request: the prefetch outlives it.
	ctx := context.Background()
	for i := n + 1; i <= n+s.cfg.Stream.Prefetch && i < j.layout.Segments; i++ {
		seg := manifest.Part{Kind: manifest.PartSegment, Track: track, Segment: i}
		key := s.streamKey(j, seg.Path())
		if s.streamHas(ctx, key) {
			continue
		}
		if !s.deps.Stream.Prefetch(key, j.src.Buffer, j.req.Video, StreamPart{Track: track, Segment: i}, func(res *VideoResult) {
			if res == nil {
				return
			}
			defer res.Body.Release()
			if initSeg, media, err := s.storeSegment(ctx, j, seg, res.Body); err == nil {
				initSeg.Release()
				media.Release()
			}
		}) {
			return // no idle worker: the next ones would not start either
		}
	}
}

// streamAudio returns the audio init segment or segment n, cut from the
// audio track (cached, or transcoded once and cached whole).
func (s *Server) streamAudio(ctx context.Context, r *http.Request, info *requestInfo, j *streamJob, part manifest.Part) (cache.Buffer, error) {
	key := s.streamKey(j, manifest.Audio+"/track")
	track, ok := s.streamGet(ctx, key)
	info.cache = cacheHit
	if !ok {
		info.cache = cacheMiss
		src, err := s.streamSource(ctx, r, info, j)
		if err != nil {
			return nil, err
		}
		t := s.stage(info, stageProcess)
		res, err := s.deps.Stream.Stream(ctx, key, src, j.req.Video, StreamPart{})
		t.done()
		if err != nil {
			return nil, err
		}
		track = res.Body
		s.streamPut(ctx, key, track, manifest.ContentTypeM4A)
	}
	defer track.Release()
	f, err := fmp4.Parse(track.Bytes())
	if err != nil || len(f.Fragments) != j.layout.Segments {
		return nil, errs.Newf(errs.KindInternal, "server: audio track of %d fragments for %d segments: %v", len(f.Fragments), j.layout.Segments, err)
	}
	if part.Kind == manifest.PartInit {
		return subBuffer(track, f.Init), nil
	}
	return subBuffer(track, f.Fragments[part.Segment].Data), nil
}

// respondManifest writes the master playlist or the MPD of j's stream,
// naming its parts under the stream's base.
func (s *Server) respondManifest(w http.ResponseWriter, r *http.Request, j *streamJob, dash bool) error {
	base := strings.TrimSuffix(s.cfg.PathPrefix, "/") + StreamPath + j.tok + "/"
	body, ct := manifest.HLSMaster(j.layout, base), manifest.ContentTypeHLS
	if dash {
		body, ct = manifest.DASH(j.layout, base), manifest.ContentTypeDASH
	}
	s.respondStreamFile(w, r, j, body, ct, "")
	return nil
}

// respondStreamFile writes one file of a stream with Range support. It
// may be cached until the ticket expires (a quarter of the TTL for a
// dialect URL's manifest, whose ticket a cache would otherwise hand out
// close to its end), never longer than the configured TTL. tag is the
// ETag's input; "" hashes body.
func (s *Server) respondStreamFile(w http.ResponseWriter, r *http.Request, j *streamJob, body []byte, contentType, tag string) {
	if tag == "" {
		sum := sha256.Sum256(body)
		tag = string(sum[:])
	}
	sum := sha256.Sum256([]byte(tag))
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Accept-Ranges", "bytes")
	h.Set("ETag", `"`+hex.EncodeToString(sum[:16])+`"`)
	maxAge := min(s.cfg.Cache.TTL, j.exp.Sub(s.now()))
	if strings.HasPrefix(contentType, manifest.ContentTypeHLS) || contentType == manifest.ContentTypeDASH {
		maxAge = min(maxAge, max(s.cfg.Stream.TTL, time.Minute)/4)
	}
	if secs := int64(maxAge / time.Second); secs > 0 {
		h.Set("Cache-Control", "public, max-age="+strconv.FormatInt(secs, 10))
	} else {
		h.Set("Cache-Control", noCache)
	}
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
}
