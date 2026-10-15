// Package media is the video transformation subsystem (WP-121, ADR 0012):
// a transform.VideoOp applied to a fetched video, under limits and an
// admission pool of its own, separate from the image limiter, so a few
// long transcodes never starve image requests (and a deployment can run
// them in a dedicated role, XI_ROLE=video).
//
// Service.Transform coalesces identical requests (one transcode per key at
// a time, shared by everyone waiting for it), probes and plans the job
// (Engine.Open, internal/video.PlanTranscode), admits it by its estimated
// cost through a pool of its own (admission.go: XI_VIDEO_TRANSFORM_CAPACITY,
// _MAX_JOBS, _QUEUE, _QUEUE_COST, _QUEUE_TIMEOUT: 429 or 503 when full),
// bounds it by XI_VIDEO_TRANSFORM_TIMEOUT and transcodes
// (internal/video/ffmpeg in production). The package is pure Go; tests use
// a fake Engine.
package media

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/limiter"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
)

// Buffer is reference-counted bytes (a vips.Buffer source, an
// ffmpeg.Output result): the shape of fetch.Buffer and cache.Buffer. A
// large source or output is a *cache.FileBuffer in a spool file
// (WP-121b): the engine reads it with ReadAt, and Bytes is not called on
// it here.
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
// internal/pipeline/exec.Executor.RenderOverlayScaled in production (app
// adapts it), the image watermark code.
type OverlayRenderer interface {
	// Render draws o for output frame f. img is its fetched image (nil
	// for text and the configured watermark). ok is false when nothing
	// would be drawn.
	Render(ctx context.Context, o *transform.VideoOverlay, img Buffer, f Frame) (l Layer, ok bool, err error)
}

// Frame is the output frame an overlay is rendered for: Width × Height,
// laid out as on a RefWidth × RefHeight frame and scaled to it (a
// stream's rendition, laid out on the largest, WP-131); for a file the
// two are the same.
type Frame struct {
	Width, Height, RefWidth, RefHeight int
}

// Layer is a rendered overlay: straight 8-bit RGBA pixels (C memory,
// Width × Height × 4 bytes, one reference owned by the caller) to draw at
// X, Y inside the frame.
type Layer struct {
	Pixels              Buffer
	X, Y, Width, Height int
}

// Metrics receives transcode and admission observations. Implementations
// must be safe for concurrent use and must not block.
type Metrics interface {
	// Transcoded observes one transcode: result is "ok" or the errs kind
	// of its failure; in and out are the source and output bytes (out 0 on
	// failure).
	Transcoded(result string, d time.Duration, in, out int64)
	// Coalesced counts a request answered by joining an identical
	// transcode in flight.
	Coalesced()
	// JobCost observes a probed job's estimated cost in cost units
	// (before clamping to the capacity).
	JobCost(units float64)
	// Rejected counts a job the pool did not admit.
	Rejected(r limiter.Reason)
	// Load reports the pool's occupancy after every change.
	Load(l Load)
}

type nopMetrics struct{}

func (nopMetrics) Transcoded(string, time.Duration, int64, int64) {}
func (nopMetrics) Coalesced()                                     {}
func (nopMetrics) JobCost(float64)                                {}
func (nopMetrics) Rejected(limiter.Reason)                        {}
func (nopMetrics) Load(Load)                                      {}

// Config sizes and bounds the subsystem (XI_VIDEO_TRANSFORM_*).
type Config struct {
	// Workers sizes the pool: the probes run at once and, when they are
	// 0, the defaults of Capacity (Workers × WorkerShare) and MaxJobs
	// (2 × Workers).
	Workers int
	// Capacity is the cost units the running jobs may hold together
	// (video.EstimateCost); MaxJobs bounds the running jobs whatever
	// their cost.
	Capacity int64
	MaxJobs  int
	// Queue is how many jobs may wait beyond MaxJobs (a place is held
	// from the probe on); QueueCost bounds the cost they wait for
	// (0: Queue × WorkerShare); QueueTimeout how long one waits, probe
	// included.
	Queue        int
	QueueCost    int64
	QueueTimeout time.Duration
	// Timeout bounds one transcode (wall time, from admission).
	Timeout time.Duration
	// Input limits the source (bytes, duration, frame pixels).
	Input video.Limits
	// Transcode holds the encoder defaults, threads and output limits.
	Transcode video.TranscodeSettings
	// Stream holds the ABR ladder and the segment duration of HLS and
	// DASH (WP-126).
	Stream video.StreamSettings
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
	if c.Capacity < 0 || c.MaxJobs < 0 || c.QueueCost < 0 {
		list = append(list, fmt.Errorf("media: capacity, max jobs and queue cost must be >= 0, got %d, %d and %d",
			c.Capacity, c.MaxJobs, c.QueueCost))
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
	// Width and Height are the video stream's or the waveform picture's
	// (0 for audio only);
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
	pool    *pool
	metrics Metrics
	render  OverlayRenderer // nil: image and text overlays are unsupported
	flights group
	// estimate is the cost model (video.EstimateCost; tests replace it).
	estimate func(*video.TranscodeInput, *video.TranscodePlan) float64

	// Background prefetches of stream segments (stream.go): Close stops
	// them (done) and waits for them (prefetches).
	prefetches sync.WaitGroup
	done       chan struct{}
	closeOnce  sync.Once
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

// New validates cfg and builds the service over engine for a build that
// can write caps.
func New(cfg *Config, caps video.TranscodeCaps, engine Engine, opts ...Option) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if engine == nil {
		return nil, errors.New("media: nil engine")
	}
	s := &Service{
		cfg: *cfg, caps: caps, engine: engine, metrics: nopMetrics{}, done: make(chan struct{}),
		estimate: video.EstimateCost,
	}
	for _, o := range opts {
		o(s)
	}
	s.pool = newPool(poolSizes(cfg), s.metrics)
	return s, nil
}

// poolSizes is cfg's pool with the defaults applied.
func poolSizes(cfg *Config) poolConfig {
	w := int64(cfg.Workers)
	p := poolConfig{
		MaxJobs: int64(cfg.MaxJobs), Queue: int64(cfg.Queue), Probes: w,
		Capacity: cfg.Capacity, QueueCost: cfg.QueueCost, QueueTimeout: cfg.QueueTimeout,
	}
	if p.MaxJobs == 0 {
		p.MaxJobs = 2 * w
	}
	if p.Capacity == 0 {
		p.Capacity = w * WorkerShare
	}
	if p.QueueCost == 0 {
		p.QueueCost = max(p.Queue, 1) * WorkerShare
	}
	return p
}

// Caps is what this build can write.
func (s *Service) Caps() video.TranscodeCaps { return s.caps }

// Stats is the pool's sizes and occupancy.
func (s *Service) Stats() Stats { return s.pool.stats() }

// Resolve picks the output of op on this build before anything is
// fetched (video.TranscodeCaps.Resolve): errs.KindUnsupported when it
// cannot be written, or has image or text overlays and no renderer.
func (s *Service) Resolve(op *transform.VideoOp) (video.TranscodeOutput, error) {
	out, err := s.caps.Resolve(op)
	if err != nil {
		return out, err
	}
	if err := s.canRender(op); err != nil {
		return video.TranscodeOutput{}, err
	}
	return out, nil
}

// canRender refuses image and text overlays without a renderer.
func (s *Service) canRender(op *transform.VideoOp) error {
	if s.render != nil {
		return nil
	}
	for i := range op.Overlays {
		if op.Overlays[i].Kind() != transform.VideoOverlayTimecode {
			return errs.WithPublic(errs.New(errs.KindUnsupported, "media: no overlay renderer"),
				"video overlays are not supported by this image")
		}
	}
	return nil
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
		return s.run(ctx, src, s.transformPlan(op, out), s.transcode(op, overlays))
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

// task is a probed job: its opened source, what the probe read, its plan
// and the result it will make (without its Body).
type task struct {
	job  Job
	in   video.TranscodeInput
	plan *video.TranscodePlan
	res  Result
}

// A planner plans a job over what the probe read (the second half of the
// probe); a worker runs an admitted task.
type (
	planner func(in *video.TranscodeInput) (*video.TranscodePlan, Result, error)
	worker  func(ctx context.Context, t *task) (*Result, error)
)

// run is one job in the pool: a place, then execute.
func (s *Service) run(ctx context.Context, src Buffer, plan planner, work worker) (*Result, error) {
	t, err := s.pool.enter(ctx)
	if err != nil {
		return nil, err
	}
	defer t.leave()
	return s.execute(ctx, t, src, plan, work, false)
}

// execute probes and plans the job of t (under the probe bound), admits
// it by its estimated cost, then runs work under the transcode timeout.
// The probe and the wait for admission together have the queue timeout;
// a prefetch (try) never waits for either.
func (s *Service) execute(ctx context.Context, t *ticket, src Buffer, plan planner, work worker, try bool) (*Result, error) {
	wait, cancel := s.pool.waitContext(ctx)
	defer cancel()
	start := time.Now()
	var tk *task
	var probeErr error
	probe := func() error {
		tk, probeErr = s.open(wait, src, plan)
		return probeErr
	}
	var err error
	if try {
		err = t.tryProbe(probe)
	} else {
		err = t.probe(ctx, wait, probe)
	}
	if err != nil {
		if probeErr == nil { // the pool's error
			return nil, err
		}
		if !try && ctx.Err() == nil && errors.Is(context.Cause(wait), errQueueTimeout) {
			return nil, s.pool.waitError(ctx) // the probe outlived the queue timeout
		}
		s.metrics.Transcoded(errs.KindOf(err).String(), time.Since(start), cache.BodyLen(src), 0)
		return nil, err
	}
	defer tk.job.Close()
	cost := s.estimate(&tk.in, tk.plan)
	if try {
		err = t.tryAdmit(cost)
	} else {
		err = t.admit(ctx, wait, cost)
	}
	if err != nil {
		return nil, err
	}
	cancel()
	return s.timed(ctx, src, func(ctx context.Context) (*Result, error) { return work(ctx, tk) })
}

// open probes src (Engine.Open: the container's headers and FFmpeg's
// stream analysis, no decoding of the cut) and plans the job.
func (s *Service) open(ctx context.Context, src Buffer, plan planner) (*task, error) {
	job, err := s.engine.Open(ctx, src, s.cfg.Input, s.cfg.Transcode.Threads)
	if err != nil {
		return nil, err
	}
	t := &task{job: job, in: job.Input()}
	if t.plan, t.res, err = plan(&t.in); err != nil {
		job.Close()
		return nil, err
	}
	return t, nil
}

// timed runs work under XI_VIDEO_TRANSFORM_TIMEOUT (errs.KindTimeout past
// it) and observes it.
func (s *Service) timed(ctx context.Context, src Buffer, work func(context.Context) (*Result, error)) (*Result, error) {
	start := time.Now()
	ctx, cancel := context.WithTimeoutCause(ctx, s.cfg.Timeout, errTimeout)
	defer cancel()
	res, err := work(ctx)
	result := "ok"
	var n int64
	if err != nil {
		if ctx.Err() != nil && errors.Is(context.Cause(ctx), errTimeout) {
			err = errs.WithPublic(errs.Wrapf(errs.KindTimeout, err, "media: transcode over %s", s.cfg.Timeout),
				"the video transformation took too long")
		}
		result = errs.KindOf(err).String()
	} else {
		n = cache.BodyLen(res.Body)
	}
	s.metrics.Transcoded(result, time.Since(start), cache.BodyLen(src), n)
	return res, err
}

// transformPlan plans op for out (PlanTranscode) and the result's shape.
func (s *Service) transformPlan(op *transform.VideoOp, out video.TranscodeOutput) planner {
	return func(in *video.TranscodeInput) (*video.TranscodePlan, Result, error) {
		p, err := video.PlanTranscode(in, op, out, s.caps, &s.cfg.Transcode)
		if err != nil {
			return nil, Result{}, err
		}
		r := Result{Output: out, Duration: p.Duration}
		if p.Audio == nil { // the source had no audio: none is written
			r.Output.Audio = video.AudioNone
		}
		switch {
		case p.Video != nil:
			r.Width, r.Height = p.Video.Width, p.Video.Height
		case p.Waveform != nil:
			r.Width, r.Height = p.Waveform.Width, p.Waveform.Height
		}
		return p, r, nil
	}
}

// transcode renders the overlays of an admitted task and runs it (files,
// and the video segments of streams).
func (s *Service) transcode(op *transform.VideoOp, overlays []Buffer) worker {
	return func(ctx context.Context, t *task) (*Result, error) {
		p := t.plan
		var pixels []Buffer
		if p.Video != nil && len(p.Video.Overlays) > 0 {
			var err error
			if pixels, err = s.renderOverlays(ctx, p.Video, op, overlays); err != nil {
				return nil, err
			}
			defer release(pixels)
		}
		return runTask(ctx, t, pixels)
	}
}

// runTask runs t's plan with the overlay pixels.
func runTask(ctx context.Context, t *task, pixels []Buffer) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, errs.Wrap(errs.KindOf(err), context.Cause(ctx))
	}
	body, err := t.job.Run(ctx, t.plan, pixels)
	if err != nil {
		return nil, err
	}
	r := t.res
	r.Body = body
	return &r, nil
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
		rw, rh := v.OverlayRef()
		l, ok, err := s.render.Render(ctx, &op.Overlays[o.Index], img, Frame{Width: v.Width, Height: v.Height, RefWidth: rw, RefHeight: rh})
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
