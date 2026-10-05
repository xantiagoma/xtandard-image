package media

import (
	"context"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
)

// HLS and DASH (WP-126, ADR 0012 §6). A stream's manifests come from the
// probe alone (Probe, then Layout); its parts are transcoded on request
// (Stream): one video segment of one rendition, or the whole audio track,
// which the caller cuts into the init segment and its media segments
// (internal/media/fmp4). Stream coalesces and admits like Transform;
// Prefetch transcodes the segments a player will ask for next on a worker
// that is idle, never queueing.

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

// Probe opens src on a worker of the pool and returns what it holds, for
// Layout. The input limits apply (errs.KindLimitExceeded).
func (s *Service) Probe(ctx context.Context, src Buffer) (video.TranscodeInput, error) {
	release, err := s.lim.Acquire(ctx, 0)
	if err != nil {
		return video.TranscodeInput{}, err
	}
	defer release()
	job, err := s.engine.Open(ctx, src, s.cfg.Input, 1)
	if err != nil {
		return video.TranscodeInput{}, err
	}
	defer job.Close()
	return job.Input(), nil
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
// not have.
func (s *Service) Stream(ctx context.Context, key string, src Buffer, op *transform.VideoOp, part StreamPart) (*Result, error) {
	src.Retain()
	res, shared, err := s.flights.do(ctx, key, func(ctx context.Context) (*Result, error) {
		defer src.Release()
		return s.admitted(ctx, src, func(ctx context.Context) (*Result, error) {
			return s.streamPart(ctx, src, op, part)
		})
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

// Prefetch transcodes part (as Stream does for key) in the background when
// a worker is idle and nobody waits for one, and no transcode for key is
// in flight: it never queues, so it never delays a request or takes a
// place in the queue. done receives the result (the callee owns one
// reference; nil when the transcode failed). It reports whether the
// prefetch started. Close waits for the prefetches.
func (s *Service) Prefetch(key string, src Buffer, op *transform.VideoOp, part StreamPart, done func(*Result)) bool {
	select {
	case <-s.done:
		return false
	default:
	}
	if s.flights.busy(key) {
		return false
	}
	release, ok := s.lim.TryAcquire()
	if !ok {
		return false
	}
	src.Retain() // this prefetch's
	s.prefetches.Go(func() {
		defer src.Release()
		defer release()
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
		res, shared, err := s.flights.do(ctx, key, func(ctx context.Context) (*Result, error) {
			defer src.Release()
			return s.timed(ctx, src, func(ctx context.Context) (*Result, error) {
				return s.streamPart(ctx, src, op, part)
			})
		})
		if shared {
			src.Release()
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

// streamPart opens src, lays the stream out and transcodes part.
func (s *Service) streamPart(ctx context.Context, src Buffer, op *transform.VideoOp, part StreamPart) (*Result, error) {
	job, err := s.engine.Open(ctx, src, s.cfg.Input, s.cfg.Transcode.Threads)
	if err != nil {
		return nil, err
	}
	defer job.Close()
	in := job.Input()
	l, err := s.Layout(&in, op)
	if err != nil {
		return nil, err
	}
	var p *video.TranscodePlan
	r := &Result{Output: l.Output, Duration: l.Duration}
	if part.Audio() {
		if l.Audio == nil {
			return nil, errs.New(errs.KindNotFound, "media: the stream has no audio")
		}
		p, err = video.PlanAudioTrack(&in, op, l, &s.cfg.Transcode)
		r.Output.Video = video.CodecNone
	} else {
		rd, ok := l.Rendition(part.Track)
		if !ok {
			return nil, errs.Newf(errs.KindNotFound, "media: the stream has no rendition %q", part.Track)
		}
		p, err = video.PlanSegment(&in, op, l, rd, part.Segment, &s.cfg.Transcode)
		r.Output.Audio = video.AudioNone
		r.Width, r.Height = rd.Width, rd.Height
	}
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, errs.Wrap(errs.KindOf(err), context.Cause(ctx))
	}
	body, err := job.Run(ctx, p, nil)
	if err != nil {
		return nil, err
	}
	r.Body = body
	return r, nil
}
