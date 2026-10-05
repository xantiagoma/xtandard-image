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
	// Run transcodes once; the caller releases the result.
	Run(ctx context.Context, p *video.TranscodePlan) (Buffer, error)
	Close()
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

// WithLimiterMetrics reports the pool's queue and workers to m.
func WithLimiterMetrics(m limiter.Metrics) Option {
	return func(s *Service) { s.limOpts = append(s.limOpts, limiter.WithMetrics(m)) }
}

// New validates cfg and builds the service over engine for a build that
// can write caps.
func New(cfg Config, caps video.TranscodeCaps, engine Engine, opts ...Option) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if engine == nil {
		return nil, errors.New("media: nil engine")
	}
	s := &Service{cfg: cfg, caps: caps, engine: engine, metrics: nopMetrics{}}
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
// cannot be written.
func (s *Service) Resolve(op *transform.VideoOp) (video.TranscodeOutput, error) {
	return s.caps.Resolve(op)
}

// errTimeout is the cause of a transcode that ran out of
// XI_VIDEO_TRANSFORM_TIMEOUT.
var errTimeout = errors.New("media: transcode timeout")

// Transform applies op to src. Requests with the same key (the result
// cache key: the canonical request, the source and the build) share one
// transcode: the first starts it, later ones wait for it, and it is
// cancelled only when every one of them has gone. src is retained while
// the transcode needs it. The caller releases Result.Body.
//
// Errors: the errs kinds of admission (KindOverloaded 429,
// KindUnavailable 503), of the limits and the plan (KindLimitExceeded,
// KindUnsupported 422), of the source (KindCorrupt), and KindTimeout when
// the transcode outlives XI_VIDEO_TRANSFORM_TIMEOUT.
func (s *Service) Transform(ctx context.Context, key string, src Buffer, op *transform.VideoOp) (*Result, error) {
	out, err := s.Resolve(op)
	if err != nil {
		return nil, err
	}
	// The transcode may outlive this call (others wait for it): it holds
	// its own reference to the source, dropped when it ends. A call that
	// joins a transcode in flight drops it at once.
	src.Retain()
	res, shared, err := s.flights.do(ctx, key, func(ctx context.Context) (*Result, error) {
		defer src.Release()
		return s.run(ctx, src, op, out)
	})
	if shared {
		src.Release()
		s.metrics.Coalesced()
	}
	if err != nil {
		return nil, err
	}
	res.Shared = shared
	return res, nil
}

// run is one transcode: admission, open, plan, transcode.
func (s *Service) run(ctx context.Context, src Buffer, op *transform.VideoOp, out video.TranscodeOutput) (*Result, error) {
	release, err := s.lim.Acquire(ctx, 0)
	if err != nil {
		return nil, err
	}
	defer release()
	start := time.Now()
	ctx, cancel := context.WithTimeoutCause(ctx, s.cfg.Timeout, errTimeout)
	defer cancel()
	res, err := s.transcode(ctx, src, op, out)
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

func (s *Service) transcode(ctx context.Context, src Buffer, op *transform.VideoOp, out video.TranscodeOutput) (*Result, error) {
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
	if err := ctx.Err(); err != nil {
		return nil, errs.Wrap(errs.KindOf(err), context.Cause(ctx))
	}
	body, err := job.Run(ctx, p)
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
