package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/cache/disk"
	"github.com/xantiagoma/xtandard-image/internal/cache/memory"
	"github.com/xantiagoma/xtandard-image/internal/cache/redis"
	"github.com/xantiagoma/xtandard-image/internal/cache/s3cache"
	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/fetch/s3src"
	"github.com/xantiagoma/xtandard-image/internal/obs/logging"
)

// caches are the result cache (XI_CACHE_RESULT_STORES) and the shared
// origin stores (XI_CACHE_ORIGIN_STORES). Each is its tiers composed with
// cache.Tiered and wrapped in a cache.AsyncStore so writes never block a
// response or a fetch; nil when off. A backend listed by both (disk,
// redis, s3) is one instance: the namespaces (res/, orig/) keep the
// entries apart, and a disk budget is shared.
type caches struct {
	result   *cache.AsyncStore
	origin   *cache.AsyncStore
	backends []cache.Store
}

// Close drains both write queues, then closes every backend once.
func (c *caches) Close() error {
	var errs []error
	for _, a := range []*cache.AsyncStore{c.result, c.origin} {
		if a != nil {
			errs = append(errs, a.Close())
		}
	}
	for _, b := range c.backends {
		errs = append(errs, b.Close())
	}
	return errors.Join(errs...)
}

// buildCaches builds every configured backend once and composes the two
// caches from them. On error the backends built so far are closed.
func buildCaches(ctx context.Context, cfg *config.Config, m cache.Metrics, logger *slog.Logger) (_ *caches, err error) {
	c := &caches{}
	defer func() {
		if err != nil {
			_ = c.Close() // the build error is what matters
		}
	}()
	byKind := map[config.StoreKind]cache.Store{}
	for _, kind := range cfg.Cache.SharedStores() {
		b, err := newBackend(ctx, kind, cfg, m)
		if err != nil {
			return nil, fmt.Errorf("%s store: %w", kind, err)
		}
		byKind[kind] = b
		c.backends = append(c.backends, b)
	}
	compose := func(kinds []config.StoreKind, queue int, what string) (*cache.AsyncStore, error) {
		if len(kinds) == 0 {
			return nil, nil
		}
		tiers := make([]cache.Store, 0, len(kinds))
		for _, k := range kinds {
			tiers = append(tiers, shared{byKind[k]})
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
			Queue:   queue,
			Metrics: m,
			OnError: func(ns cache.Namespace, _ string, err error) {
				logger.LogAttrs(ctx, slog.LevelWarn, "cache write failed",
					slog.String("cache", what), slog.String("namespace", string(ns)), logging.Err(err))
			},
		})
	}
	rc := &cfg.Cache.Result
	if c.result, err = compose(rc.Stores, rc.WriteQueue, "result"); err != nil {
		return nil, err
	}
	if c.origin, err = compose(cfg.Cache.Origin.Stores, rc.WriteQueue, "origin"); err != nil {
		return nil, err
	}
	return c, nil
}

// newBackend builds one store from its XI_CACHE_RESULT_* settings.
func newBackend(ctx context.Context, kind config.StoreKind, cfg *config.Config, m cache.Metrics) (cache.Store, error) {
	rc := &cfg.Cache.Result
	switch kind {
	case config.StoreMemory:
		return memory.New(memory.Options{MaxBytes: int64(rc.MemoryBytes), Metrics: m})
	case config.StoreDisk:
		return disk.New(disk.Options{Dir: rc.Disk.Path, MaxBytes: int64(rc.Disk.Bytes), Metrics: m})
	case config.StoreRedis:
		return redis.New(&redis.Options{
			URL: rc.Redis.URL.Reveal(), Prefix: rc.Redis.Prefix, MaxValueBytes: int(rc.Redis.MaxValueBytes), Metrics: m,
		})
	case config.StoreS3:
		name := rc.S3.Source
		src, ok := cfg.Sources[name]
		if !ok || src.Type != config.SourceS3 {
			return nil, fmt.Errorf("source %q is not an s3 source", name)
		}
		client, err := s3src.NewClient(ctx, s3Config(name, &src))
		if err != nil {
			return nil, err
		}
		return s3cache.New(&s3cache.Options{Client: client, Bucket: rc.S3.Bucket, Prefix: rc.S3.Prefix, Metrics: m})
	default:
		return nil, fmt.Errorf("unknown store kind %q", kind)
	}
}

// shared is a backend used as a tier: closing the composition (Tiered,
// AsyncStore) leaves it open, because caches.Close closes each backend
// once after both write queues drained.
type shared struct{ cache.Store }

func (shared) Close() error { return nil }
