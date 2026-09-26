package app

import (
	"context"
	"log/slog"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/cache/memory"
	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/obs/logging"
)

// buildResultCache builds the result cache of XI_CACHE_RESULT_STORES: the
// implemented tiers in configuration order, composed with cache.Tiered and
// wrapped in a cache.AsyncStore so writes never block a response. It
// returns nil when the cache is off or no listed tier is implemented yet
// (disk, redis and s3 arrive with WP-53/54/55 and are skipped with a
// warning).
//
// The original cache (ARCHITECTURE §7, cache.NSOrigin) stays fetch's
// in-process LRU: fetch exposes no Store hook yet. TODO(WP-53/54): back it
// with a shared cache.Store for multi-replica setups.
func buildResultCache(ctx context.Context, cfg *config.Config, m cache.Metrics, logger *slog.Logger) (*cache.AsyncStore, error) {
	rc := &cfg.Cache.Result
	if !rc.Enabled() {
		return nil, nil
	}
	var tiers []cache.Store
	for _, kind := range rc.Stores {
		switch kind {
		case config.StoreMemory:
			s, err := memory.New(memory.Options{MaxBytes: int64(rc.MemoryBytes), Metrics: m})
			if err != nil {
				return nil, err
			}
			tiers = append(tiers, s)
		default:
			logger.LogAttrs(ctx, slog.LevelWarn, "config warning",
				slog.String("warning", "result cache store "+string(kind)+" is not implemented yet; it is skipped"))
		}
	}
	if len(tiers) == 0 {
		return nil, nil
	}
	store := tiers[0]
	if len(tiers) > 1 {
		t, err := cache.NewTiered(tiers...)
		if err != nil {
			return nil, err
		}
		store = t
	}
	return cache.NewAsync(store, cache.AsyncOptions{
		Queue:   rc.WriteQueue,
		Metrics: m,
		OnError: func(ns cache.Namespace, _ string, err error) {
			logger.LogAttrs(ctx, slog.LevelWarn, "result cache write failed",
				slog.String("namespace", string(ns)), logging.Err(err))
		},
	})
}
