package app

import (
	"context"
	"log/slog"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/obs/metrics"
	"github.com/xantiagoma/xtandard-image/internal/spool"
)

// Spool files (WP-121b, ADR 0012 §5): large video sources and transcode
// outputs live in files under XI_VIDEO_TRANSFORM_SPOOL_DIR instead of
// memory.

// buildSpool returns the video spool, or nil when this instance does not
// serve video transformations or XI_VIDEO_TRANSFORM_SPOOL_THRESHOLD is 0.
// An unwritable directory fails startup.
func buildSpool(ctx context.Context, cfg *config.Config, met *metrics.Metrics, logger *slog.Logger) (*spool.Spool, error) {
	t := &cfg.Video.Transform
	if !videoTransformServed(cfg) || t.SpoolThreshold <= 0 {
		return nil, nil
	}
	var opts []spool.Option
	if met != nil {
		opts = append(opts, spool.WithMetrics(met.Spool()))
	}
	sp, err := spool.New(spool.Config{
		Dir: t.SpoolPath(), Threshold: int64(t.SpoolThreshold), MaxBytes: int64(t.SpoolMaxBytes),
	}, opts...)
	if err != nil {
		return nil, err
	}
	logger.LogAttrs(ctx, slog.LevelInfo, "video spool",
		slog.String("dir", sp.Dir()), slog.Int64("threshold", int64(t.SpoolThreshold)),
		slog.Int64("max_bytes", int64(t.SpoolMaxBytes)), slog.Int("stale_removed", sp.Removed()))
	return sp, nil
}

// fetchSpooler adapts *spool.Spool to fetch.Spooler: originals are input
// files.
type fetchSpooler struct{ s *spool.Spool }

func (a fetchSpooler) Spills(n int64) bool        { return a.s.Spills(n) }
func (a fetchSpooler) Threshold() int64           { return a.s.Threshold() }
func (a fetchSpooler) SetReclaimer(f func() bool) { a.s.SetReclaimer(f) }

func (a fetchSpooler) Create(reserve int64) (fetch.SpoolWriter, error) {
	w, err := a.s.Create(spool.KindInput, reserve)
	if err != nil {
		return nil, err
	}
	return spoolWriter{w}, nil
}

// spoolWriter is a *spool.Writer as a fetch.SpoolWriter.
type spoolWriter struct{ *spool.Writer }

func (w spoolWriter) Finish(n int64) (fetch.Buffer, error) {
	b, err := w.Writer.Finish(n)
	if err != nil {
		return nil, err
	}
	return b, nil
}
