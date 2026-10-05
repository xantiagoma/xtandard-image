// Package media is the video transformation subsystem (WP-121, ADR 0012):
// a transform.VideoOp applied to a fetched video, under limits and an
// admission pool of its own, separate from the image limiter, so a few
// long transcodes never starve image requests (and a deployment can run
// them in a dedicated role, XI_ROLE=video).
//
// Service.Transform coalesces identical requests (one transcode per key at
// a time, shared by everyone waiting for it), admits the work through its
// own limiter (XI_VIDEO_TRANSFORM_WORKERS, _QUEUE, _QUEUE_TIMEOUT: 429 or
// 503 when full), bounds it by XI_VIDEO_TRANSFORM_TIMEOUT, and runs the
// Engine: probe, plan (internal/video.PlanTranscode) and transcode
// (internal/video/ffmpeg in production). The package is pure Go; tests use
// a fake Engine.
package media

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/limiter"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
)

// Buffer is reference-counted bytes (a vips.Buffer source, an
// ffmpeg.Output result): the shape of fetch.Buffer and cache.Buffer.
type Buffer interface {
	Bytes() []byte
	Retain()
	Release()
}

// Engine opens sources for transcoding: ffmpeg.OpenTranscode in
// production (app adapts it).
type Engine interface {
	// Open probes src (retaining it until the Job is closed) and checks
	// lim; threads is the decoder and encoder threads.
	Open(ctx context.Context, src Buffer, lim video.Limits, threads int) (Job, error)
}

// Job is one opened source.
type Job interface {
	Input() video.TranscodeInput
	// Run transcodes once; pixels[i] are the RGBA pixels of image overlay
	// p.Video.Overlays[i] (nil for a timecode), read during the call. The
	// caller releases the result.
	Run(ctx context.Context, p *video.TranscodePlan, pixels []Buffer) (Buffer, error)
	Close()
}

// OverlayRenderer renders image and text overlays (WP-124):
// internal/pipeline/exec.Executor.RenderOverlay in production (app adapts
// it), the image watermark code.
type OverlayRenderer interface {
	// Render draws o for a w × h output frame. img is its fetched image
	// (nil for text and the configured watermark). ok is false when
	// nothing would be drawn.
	Render(ctx context.Context, o *transform.VideoOverlay, img Buffer, w, h int) (l Layer, ok bool, err error)
}

// Layer is a rendered overlay: straight 8-bit RGBA pixels (C memory,
// Width × Height × 4 bytes, one reference owned by the caller) to draw at
// X, Y inside the frame.
type Layer struct {
	Pixels              Buffer
	X, Y, Width, Height int
}

// Metrics receives transcode observations. Implementations must be safe
// for concurrent use and must not block.
type Metrics interface {
	// Transcoded observes one transcode: result is "ok" or the errs kind
	// of its failure; in and out are the source and output bytes (out 0 on
	// failure).
	Transcoded(result string, d time.Duration, in, out int64)
	// Coalesced counts a request answered by joining an identical
	// transcode in flight.
	Coalesced()
}

type nopMetrics struct{}

func (nopMetrics) Transcoded(string, time.Duration, int64, int64) {}
func (nopMetrics) Coalesced()                                     {}

// Config sizes and bounds the subsystem (XI_VIDEO_TRANSFORM_*).
type Config struct {
	// Workers is the transcodes run at once; Queue how many may wait;
	// QueueTimeout how long one waits for a worker.
	Workers      int
	Queue        int
	QueueTimeout time.Duration
	// Timeout bounds one transcode (wall time, from admission).
	Timeout time.Duration
	// Input limits the source (bytes, duration, frame pixels).
	Input video.Limits
	// Transcode holds the encoder defaults, threads and output limits.
	Transcode video.TranscodeSettings
}

// Validate reports every invalid field.
func (c *Config) Validate() error {
	var list []error
	if c.Workers < 1 {
		list = append(list, fmt.Errorf("media: workers must be >= 1, got %d", c.Workers))
	}
	if c.Queue < 0 {
		list = append(list, fmt.Errorf("media: queue must be >= 0, got %d", c.Queue))
	}
	if c.QueueTimeout < 0 || c.Timeout <= 0 {
		list = append(list, fmt.Errorf("media: queue timeout must be >= 0 and timeout > 0, got %s and %s", c.QueueTimeout, c.Timeout))
	}
	return errors.Join(list...)
}

// Result is a transcoded file.
type Result struct {
	// Body is the file; the caller owns one reference.
	Body   Buffer
	Output video.TranscodeOutput
	// Width and Height are the video stream's (0 for audio only);
	// Duration is the expected length in seconds (0 when unknown).
	Width, Height int
	Duration      float64
	// Shared reports a result another request's transcode produced.
	Shared bool
}

// Service runs video transformations. It is safe for concurrent use.
type Service struct {
	cfg     Config
	caps    video.TranscodeCaps
	engine  Engine
	lim     *limiter.Limiter
	metrics Metrics
	render  OverlayRenderer // nil: image and text overlays are unsupported
	flights group
	limOpts []limiter.Option // New only
}

// Option configures New.
type Option func(*Service)

// WithMetrics installs m (nil keeps none).
func WithMetrics(m Metrics) Option {
	return func(s *Service) {
		if m != nil {
			s.metrics = m
		}
	}
}

// WithOverlayRenderer renders image and text overlays with r; without
// one, a request with any is errs.KindUnsupported.
func WithOverlayRenderer(r OverlayRenderer) Option {
	return func(s *Service) { s.render = r }
}

// WithLimiterMetrics reports the pool's queue and workers to m.
func WithLimiterMetrics(m limiter.Metrics) Option {
	return func(s *Service) { s.limOpts = append(s.limOpts, limiter.WithMetrics(m)) }
}

// New validates cfg and builds the service over engine for a build that
// can write caps.
func New(cfg *Config, caps video.TranscodeCaps, engine Engine, opts ...Option) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if engine == nil {
		return nil, errors.New("media: nil engine")
	}
	s := &Service{cfg: *cfg, caps: caps, engine: engine, metrics: nopMetrics{}}
	for _, o := range opts {
		o(s)
	}
	lim, err := limiter.New(limiter.Config{
		Workers: cfg.Workers, QueueSize: cfg.Queue, QueueTimeout: cfg.QueueTimeout,
	}, s.limOpts...)
	if err != nil {
		return nil, err
	}
	s.lim = lim
	s.limOpts = nil
	return s, nil
}

// Caps is what this build can write.
func (s *Service) Caps() video.TranscodeCaps { return s.caps }

// Stats is the pool's admission snapshot.
func (s *Service) Stats() limiter.Stats { return s.lim.Stats() }

// Resolve picks the output of op on this build before anything is
// fetched (video.TranscodeCaps.Resolve): errs.KindUnsupported when it
// cannot be written, or has image or text overlays and no renderer.
func (s *Service) Resolve(op *transform.VideoOp) (video.TranscodeOutput, error) {
	out, err := s.caps.Resolve(op)
	if err != nil {
		return out, err
	}
	if s.render == nil {
		for i := range op.Overlays {
			if op.Overlays[i].Kind() != transform.VideoOverlayTimecode {
				return video.TranscodeOutput{}, errs.WithPublic(errs.New(errs.KindUnsupported, "media: no overlay renderer"),
					"video overlays are not supported by this image")
			}
		}
	}
	return out, nil
}

// errTimeout is the cause of a transcode that ran out of
// XI_VIDEO_TRANSFORM_TIMEOUT.
var errTimeout = errors.New("media: transcode timeout")

// Transform applies op to src. overlays[i] is the fetched image of image
// overlay op.Overlays[i] (nil for the others; nil or shorter when there
// are none). Requests with the same key (the result cache key: the
// canonical request, the source and the build) share one transcode: the
// first starts it, later ones wait for it, and it is cancelled only when
// every one of them has gone. src and the overlays are retained while the
// transcode needs them. The caller releases Result.Body.
//
// Errors: the errs kinds of admission (KindOverloaded 429,
// KindUnavailable 503), of the limits and the plan (KindLimitExceeded,
// KindUnsupported 422), of the source (KindCorrupt), and KindTimeout when
// the transcode outlives XI_VIDEO_TRANSFORM_TIMEOUT.
func (s *Service) Transform(ctx context.Context, key string, src Buffer, op *transform.VideoOp, overlays []Buffer) (*Result, error) {
	out, err := s.Resolve(op)
	if err != nil {
		return nil, err
	}
	if len(overlays) > len(op.Overlays) {
		return nil, errs.Newf(errs.KindInternal, "media: %d overlay images for %d overlays", len(overlays), len(op.Overlays))
	}
	// The transcode may outlive this call (others wait for it): it holds
	// its own references to the source and the overlay images, dropped
	// when it ends. A call that joins a transcode in flight drops them at
	// once.
	in := append([]Buffer{src}, overlays...)
	retain(in)
	res, shared, err := s.flights.do(ctx, key, func(ctx context.Context) (*Result, error) {
		defer release(in)
		return s.run(ctx, src, op, overlays, out)
	})
	if shared {
		release(in)
		s.metrics.Coalesced()
	}
	if err != nil {
		return nil, err
	}
	res.Shared = shared
	return res, nil
}

// retain and release take and drop a reference to every non-nil buffer.
func retain(bs []Buffer) {
	for _, b := range bs {
		if b != nil {
			b.Retain()
		}
	}
}

func release(bs []Buffer) {
	for _, b := range bs {
		if b != nil {
			b.Release()
		}
	}
}

// run is one transcode: admission, open, plan, overlays, transcode.
func (s *Service) run(ctx context.Context, src Buffer, op *transform.VideoOp, overlays []Buffer, out video.TranscodeOutput) (*Result, error) {
	release, err := s.lim.Acquire(ctx, 0)
	if err != nil {
		return nil, err
	}
	defer release()
	start := time.Now()
	ctx, cancel := context.WithTimeoutCause(ctx, s.cfg.Timeout, errTimeout)
	defer cancel()
	res, err := s.transcode(ctx, src, op, overlays, out)
	result := "ok"
	var n int64
	if err != nil {
		if ctx.Err() != nil && errors.Is(context.Cause(ctx), errTimeout) {
			err = errs.WithPublic(errs.Wrapf(errs.KindTimeout, err, "media: transcode over %s", s.cfg.Timeout),
				"the video transformation took too long")
		}
		result = errs.KindOf(err).String()
	} else {
		n = int64(len(res.Body.Bytes()))
	}
	s.metrics.Transcoded(result, time.Since(start), int64(len(src.Bytes())), n)
	return res, err
}

func (s *Service) transcode(ctx context.Context, src Buffer, op *transform.VideoOp, overlays []Buffer, out video.TranscodeOutput) (*Result, error) {
	job, err := s.engine.Open(ctx, src, s.cfg.Input, s.cfg.Transcode.Threads)
	if err != nil {
		return nil, err
	}
	defer job.Close()
	in := job.Input()
	p, err := video.PlanTranscode(&in, op, out, s.caps, &s.cfg.Transcode)
	if err != nil {
		return nil, err
	}
	var pixels []Buffer
	if p.Video != nil && len(p.Video.Overlays) > 0 {
		if pixels, err = s.renderOverlays(ctx, p.Video, op, overlays); err != nil {
			return nil, err
		}
		defer release(pixels)
	}
	if err := ctx.Err(); err != nil {
		return nil, errs.Wrap(errs.KindOf(err), context.Cause(ctx))
	}
	body, err := job.Run(ctx, p, pixels)
	if err != nil {
		return nil, err
	}
	r := &Result{Body: body, Output: out, Duration: p.Duration}
	if p.Audio == nil { // the source had no audio: none was written
		r.Output.Audio = video.AudioNone
	}
	if p.Video != nil {
		r.Width, r.Height = p.Video.Width, p.Video.Height
	}
	return r, nil
}

// renderOverlays renders the image and text overlays of v for its output
// size and places them; one that draws nothing is removed from v. It
// returns the pixels of each remaining overlay (nil for a timecode); the
// caller releases them.
func (s *Service) renderOverlays(ctx context.Context, v *video.VideoStreamPlan, op *transform.VideoOp, imgs []Buffer) ([]Buffer, error) {
	kept := v.Overlays[:0:0]
	var pixels []Buffer
	for _, o := range v.Overlays {
		if err := ctx.Err(); err != nil {
			release(pixels)
			return nil, errs.Wrap(errs.KindOf(err), context.Cause(ctx))
		}
		if !o.Image() {
			kept, pixels = append(kept, o), append(pixels, nil)
			continue
		}
		if s.render == nil {
			release(pixels)
			return nil, errs.New(errs.KindUnsupported, "media: no overlay renderer")
		}
		var img Buffer
		if o.Index < len(imgs) {
			img = imgs[o.Index]
		}
		l, ok, err := s.render.Render(ctx, &op.Overlays[o.Index], img, v.Width, v.Height)
		if err != nil {
			release(pixels)
			return nil, err
		}
		if !ok {
			continue
		}
		o.Place(l.X, l.Y, l.Width, l.Height)
		kept, pixels = append(kept, o), append(pixels, l.Pixels)
	}
	v.Overlays = kept
	if err := v.CheckOverlays(); err != nil {
		release(pixels)
		return nil, err
	}
	return pixels, nil
}
