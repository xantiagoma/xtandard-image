package media

import (
	"context"
	"errors"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
)

// HLS and DASH (WP-126, ADR 0012 §6). A stream's manifests come from the
// probe alone (Probe, then Layout); its parts are transcoded on request
// (Stream): one video segment of one rendition, or the whole audio track,
// which the caller cuts into the init segment and its media segments
// (internal/media/fmp4). Stream coalesces and admits like Transform;
// Prefetch transcodes the segments a player will ask for next only when
// the pool is idle enough for them, never queueing.

// StreamPart is what one stream transcode writes.
type StreamPart struct {
	// Track is a video rendition's name ("v0", …), or "" for the audio
	// track (every audio segment at once).
	Track string
	// Segment is the video segment's number.
	Segment int
}

// Audio reports the audio track.
func (p StreamPart) Audio() bool { return p.Track == "" }

// ResolveStream picks a stream's codecs on this build before anything is
// fetched (video.TranscodeCaps.ResolveStream): errs.KindUnsupported when
// it cannot be written.
func (s *Service) ResolveStream(op *transform.VideoOp) (video.TranscodeOutput, error) {
	return s.caps.ResolveStream(op)
}

// Probe opens src under the pool's probe bound (holding a place in the
// pool, as every job does: 429 when it is full, 503 past the queue
// timeout) and returns what it holds, for Layout. The input limits apply
// (errs.KindLimitExceeded).
func (s *Service) Probe(ctx context.Context, src Buffer) (video.TranscodeInput, error) {
	t, err := s.pool.enter(ctx)
	if err != nil {
		return video.TranscodeInput{}, err
	}
	defer t.leave()
	wait, cancel := s.pool.waitContext(ctx)
	defer cancel()
	var in video.TranscodeInput
	var probeErr error
	err = t.probe(ctx, wait, func() error {
		job, err := s.engine.Open(wait, src, s.cfg.Input, 1)
		if err != nil {
			probeErr = err
			return err
		}
		in = job.Input()
		job.Close()
		return nil
	})
	if probeErr != nil && ctx.Err() == nil && errors.Is(context.Cause(wait), errQueueTimeout) {
		return in, s.pool.waitError(ctx)
	}
	return in, err
}

// Layout lays op out as a stream over the probed source in
// (video.PlanStream with the configured ladder and segment duration).
func (s *Service) Layout(in *video.TranscodeInput, op *transform.VideoOp) (*video.StreamLayout, error) {
	out, err := s.caps.ResolveStream(op)
	if err != nil {
		return nil, err
	}
	return video.PlanStream(in, op, out, s.caps, &s.cfg.Transcode, &s.cfg.Stream)
}

// Stream transcodes part of the stream op of src into fragmented MP4: a
// video segment (its init segment and one fragment) or the audio track
// (its init segment and one fragment per segment). Calls with the same key
// share one transcode, admitted and bounded like Transform's. The caller
// releases the result's body. errs.KindNotFound for a part the stream does
// not have. A call that joins a prefetch which then finds the pool busy
// starts the transcode itself.
func (s *Service) Stream(ctx context.Context, key string, src Buffer, op *transform.VideoOp, part StreamPart) (*Result, error) {
	for {
		src.Retain()
		res, shared, err := s.flights.do(ctx, key, func(ctx context.Context) (*Result, error) {
			defer src.Release()
			return s.run(ctx, src, s.streamPlan(op, part), streamRun)
		})
		if shared {
			src.Release()
		}
		if shared && errors.Is(err, errBusy) {
			continue // joined a prefetch that found the pool busy: run it ourselves
		}
		if shared {
			s.metrics.Coalesced()
		}
		if err != nil {
			return nil, err
		}
		res.Shared = shared
		return res, nil
	}
}

// Prefetch transcodes part (as Stream does for key) in the background when
// the pool is idle (nobody queued, a run slot and the part's cost in
// capacity free at once, after its probe) and no transcode for key is in
// flight: it never queues, so it never delays a request. done receives
// the result (the callee owns one reference; nil when the transcode
// failed or the pool was busy). It reports whether the prefetch started.
// Close waits for the prefetches.
func (s *Service) Prefetch(key string, src Buffer, op *transform.VideoOp, part StreamPart, done func(*Result)) bool {
	select {
	case <-s.done:
		return false
	default:
	}
	if s.flights.busy(key) {
		return false
	}
	t, ok := s.pool.tryEnter()
	if !ok {
		return false
	}
	src.Retain() // this prefetch's
	s.prefetches.Go(func() {
		defer src.Release()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		stop := make(chan struct{})
		defer close(stop)
		go func() { // Close cancels the prefetches
			select {
			case <-s.done:
				cancel()
			case <-stop:
			}
		}()
		src.Retain() // the flight's, if this call starts it
		// The ticket belongs to the flight's function when this call
		// starts the flight (it leaves when the transcode ends), and is
		// given back here when the call joins another one.
		res, shared, err := s.flights.do(ctx, key, func(ctx context.Context) (*Result, error) {
			defer src.Release()
			defer t.leave()
			return s.execute(ctx, t, src, s.streamPlan(op, part), streamRun, true)
		})
		if shared {
			src.Release()
			t.leave()
		}
		if err != nil {
			res = nil
		}
		done(res)
	})
	return true
}

// Close stops the prefetches in flight and waits for them. The service
// starts none afterwards; Transform and Stream keep working.
func (s *Service) Close() {
	s.closeOnce.Do(func() { close(s.done) })
	s.prefetches.Wait()
}

// streamPlan lays the stream out over the probed source and plans part.
func (s *Service) streamPlan(op *transform.VideoOp, part StreamPart) planner {
	return func(in *video.TranscodeInput) (*video.TranscodePlan, Result, error) {
		l, err := s.Layout(in, op)
		if err != nil {
			return nil, Result{}, err
		}
		var p *video.TranscodePlan
		r := Result{Output: l.Output, Duration: l.Duration}
		if part.Audio() {
			if l.Audio == nil {
				return nil, Result{}, errs.New(errs.KindNotFound, "media: the stream has no audio")
			}
			p, err = video.PlanAudioTrack(in, op, l, &s.cfg.Transcode)
			r.Output.Video = video.CodecNone
		} else {
			rd, ok := l.Rendition(part.Track)
			if !ok {
				return nil, Result{}, errs.Newf(errs.KindNotFound, "media: the stream has no rendition %q", part.Track)
			}
			p, err = video.PlanSegment(in, op, l, rd, part.Segment, &s.cfg.Transcode)
			r.Output.Audio = video.AudioNone
			r.Width, r.Height = rd.Width, rd.Height
		}
		if err != nil {
			return nil, Result{}, err
		}
		return p, r, nil
	}
}

// streamRun transcodes a stream part.
func streamRun(ctx context.Context, t *task) (*Result, error) { return runTask(ctx, t, nil) }
