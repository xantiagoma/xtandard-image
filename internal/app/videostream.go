package app

import (
	"context"
	"crypto/rand"
	"log/slog"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/cache/memory"
	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/media"
	"github.com/xantiagoma/xtandard-image/internal/security/ticket"
	"github.com/xantiagoma/xtandard-image/internal/server"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
)

// HLS and DASH (WP-126, ADR 0012 §6): the server's stream dependencies
// over internal/media.

// streamSettings maps XI_VIDEO_TRANSFORM_ABR_LADDER and _SEGMENT_DURATION
// (validated by config).
func streamSettings(cfg *config.Config) video.StreamSettings {
	t := &cfg.Video.Transform
	s := video.StreamSettings{SegmentDuration: t.SegmentDuration, AudioChunk: t.AudioChunk}
	for _, item := range t.ABRLadder {
		if h, b, err := config.ParseABRRung(item); err == nil {
			s.Ladder = append(s.Ladder, video.Rung{Height: h, Bitrate: b})
		}
	}
	return s
}

// streamServerConfig maps XI_VIDEO_TRANSFORM_STREAM_TTL and
// _PREFETCH_SEGMENTS.
func streamServerConfig(cfg *config.Config) server.StreamConfig {
	t := &cfg.Video.Transform
	return server.StreamConfig{TTL: t.StreamTTL, Prefetch: t.PrefetchSegments}
}

// buildTickets returns the sealer of the child URLs' tickets: the
// configured stream keys, else a key derived from the first signing key
// (ticket.New derives its own keys from it, for this purpose only), else a
// random key of this process (config warns at startup).
func buildTickets(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*ticket.Sealer, error) {
	var secrets [][]byte
	for _, k := range cfg.Video.Transform.StreamKeys {
		secrets = append(secrets, k)
	}
	if len(secrets) == 0 && len(cfg.Signing.Keys) > 0 && len(cfg.Signing.Keys[0]) >= ticket.MinSecret {
		secrets = append(secrets, cfg.Signing.Keys[0])
	}
	if len(secrets) == 0 {
		b := make([]byte, 32)
		_, _ = rand.Read(b) // never fails (crypto/rand)
		secrets = append(secrets, b)
		logger.LogAttrs(ctx, slog.LevelWarn, "stream tickets use a random key of this process")
	}
	return ticket.New(secrets...)
}

// buildStreamCache returns the in-memory stream cache
// (XI_VIDEO_TRANSFORM_STREAM_CACHE_BYTES), nil when off.
func buildStreamCache(cfg *config.Config, m cache.Metrics) (cache.Store, error) {
	n := int64(cfg.Video.Transform.StreamCacheBytes)
	if n <= 0 {
		return nil, nil
	}
	return memory.New(memory.Options{MaxBytes: n, Name: "stream", Metrics: m})
}

// mediaStreamer adapts media.Service to server.StreamService.
type mediaStreamer struct{ s *media.Service }

func (m mediaStreamer) ResolveStream(op *transform.VideoOp) (video.TranscodeOutput, error) {
	return m.s.ResolveStream(op)
}

func (m mediaStreamer) Probe(ctx context.Context, src fetch.Buffer) (video.TranscodeInput, error) {
	return m.s.Probe(ctx, src)
}

func (m mediaStreamer) Layout(in *video.TranscodeInput, op *transform.VideoOp) (*video.StreamLayout, error) {
	return m.s.Layout(in, op)
}

func (m mediaStreamer) Stream(ctx context.Context, key string, src fetch.Buffer, op *transform.VideoOp, part server.StreamPart) (*server.VideoResult, error) {
	r, err := m.s.Stream(ctx, key, src, op, mediaPart(part))
	if err != nil {
		return nil, err
	}
	return videoResult(r), nil
}

func (m mediaStreamer) Prefetch(key string, src fetch.Buffer, op *transform.VideoOp, part server.StreamPart, done func(*server.VideoResult)) bool {
	return m.s.Prefetch(key, src, op, mediaPart(part), func(r *media.Result) {
		if r == nil {
			done(nil)
			return
		}
		done(videoResult(r))
	})
}

// videoResult is the server's view of a media result.
func videoResult(r *media.Result) *server.VideoResult {
	return &server.VideoResult{Body: r.Body, Output: r.Output, Width: r.Width, Height: r.Height, Shared: r.Shared}
}

// mediaPart is the media view of a stream part (its own slice of the
// overlay images, which the server may clear after the call).
func mediaPart(p server.StreamPart) media.StreamPart {
	mp := media.StreamPart{Track: p.Track, Segment: p.Segment}
	if p.CopyAudio != nil {
		mp.CopyAudio = p.CopyAudio
	}
	if len(p.Overlays) > 0 {
		mp.Overlays = make([]media.Buffer, len(p.Overlays))
		for i, b := range p.Overlays {
			if b != nil {
				mp.Overlays[i] = b
			}
		}
	}
	return mp
}
