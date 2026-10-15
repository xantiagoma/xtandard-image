package media

import (
	"context"
	"errors"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/media/fmp4"

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
	// CopyAudio is, for a video segment of an MPEG-TS stream (WP-131), the
	// audio of the same segment as the audio track's CMAF init segment
	// followed by its fragment: its packets are copied into the segment.
	// nil for a muted stream or a source without audio. It is retained
	// while the transcode needs it.
	CopyAudio Buffer
	// Overlays[i] is the fetched image of image overlay i of the stream's
	// VideoOp (nil for the others; nil or shorter when there are none),
	// drawn on video segments (WP-131). Retained while the transcode needs
	// them.
	Overlays []Buffer
}

// buffers lists what a transcode of p holds: the source, the copied
// audio and the overlay images.
func (p StreamPart) buffers(src Buffer) []Buffer {
	return append([]Buffer{src, p.CopyAudio}, p.Overlays...)
}

// Audio reports the audio track.
func (p StreamPart) Audio() bool { return p.Track == "" }

// ResolveStream picks a stream's codecs on this build before anything is
// fetched (video.TranscodeCaps.ResolveStream): errs.KindUnsupported when
// it cannot be written.
func (s *Service) ResolveStream(op *transform.VideoOp) (video.TranscodeOutput, error) {
	out, err := s.caps.ResolveStream(op)
	if err != nil {
		return out, err
	}
	if err := s.canRender(op); err != nil {
		return video.TranscodeOutput{}, err
	}
	return out, nil
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
	if len(part.Overlays) > len(op.Overlays) {
		return nil, errs.Newf(errs.KindInternal, "media: %d overlay images for %d overlays", len(part.Overlays), len(op.Overlays))
	}
	in := part.buffers(src)
	for {
		retain(in)
		res, shared, err := s.flights.do(ctx, key, func(ctx context.Context) (*Result, error) {
			defer release(in)
			return s.run(ctx, src, s.streamPlan(op, part), s.transcode(op, part.Overlays))
		})
		if shared {
			release(in)
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
	if len(part.Overlays) > len(op.Overlays) {
		return false
	}
	in := part.buffers(src)
	retain(in) // this prefetch's
	s.prefetches.Go(func() {
		defer release(in)
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
		retain(in) // the flight's, if this call starts it
		// The ticket belongs to the flight's function when this call
		// starts the flight (it leaves when the transcode ends), and is
		// given back here when the call joins another one.
		res, shared, err := s.flights.do(ctx, key, func(ctx context.Context) (*Result, error) {
			defer release(in)
			defer t.leave()
			return s.execute(ctx, t, src, s.streamPlan(op, part), s.transcode(op, part.Overlays), true)
		})
		if shared {
			release(in)
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
			if err == nil && p.TS != nil {
				err = copyAudio(p.TS, part.CopyAudio)
			} else {
				r.Output.Audio = video.AudioNone
			}
			r.Width, r.Height = rd.Width, rd.Height
		}
		if err != nil {
			return nil, Result{}, err
		}
		return p, r, nil
	}
}

// copyAudio gives an MPEG-TS segment the packets of its audio (the audio
// track's init segment and the segment's fragment).
func copyAudio(ts *video.TSPlan, audio Buffer) error {
	if ts.Audio == nil {
		return nil
	}
	if audio == nil {
		return errs.New(errs.KindInternal, "media: an mpeg-ts segment without its audio")
	}
	f, err := fmp4.Parse(audio.Bytes())
	if err != nil || len(f.Fragments) != 1 || int(f.Timescale) != ts.Audio.SampleRate {
		return errs.Newf(errs.KindInternal, "media: the audio of an mpeg-ts segment: %v", err)
	}
	samples, err := f.Samples(0)
	if err != nil {
		return errs.Wrapf(errs.KindInternal, err, "media: the audio of an mpeg-ts segment")
	}
	pk := make([]video.AudioPacket, len(samples))
	for i, s := range samples {
		pk[i] = video.AudioPacket{Data: s.Data, PTS: int64(s.DecodeTime), Duration: int64(s.Duration)} //nolint:gosec // G115: 48 kHz ticks of a stream
	}
	a := *ts.Audio
	a.Packets = pk
	ts.Audio = &a
	return nil
}
