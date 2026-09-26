// Package redis is the Redis-protocol cache.Store (docs/ARCHITECTURE.md
// §9.2, XI_CACHE_RESULT_REDIS_*), built on github.com/redis/go-redis/v9
// (ADR 0004). It speaks RESP to Redis, Valkey (the BSD-licensed fork the
// integration tests run against), KeyDB or a managed service; nothing it
// uses is newer than Redis 2.6.12 (GET, SET PX, DEL).
//
// An entry is one string value at <prefix><namespace><key>: a
// cache.AppendFrame frame (magic, metadata length, JSON cache.Meta with
// status, header subset, times, size and CRC-32C) followed by the body.
// Set uses SET … PX <ttl>, so the server expires entries itself; Get also
// checks the expiry in the metadata so every tier agrees on it. Values
// larger than MaxValueBytes (1 MiB by default) are not stored and are
// counted (xi_cache_skipped_total{reason="too_large"}): big results belong
// in the disk or s3 tier, not in Redis memory.
//
// Connection, TLS and auth come from the URL (redis.ParseURL):
// redis://[user:password@]host:port/db, rediss:// for TLS, plus query
// options such as dial_timeout or pool_size.
package redis

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/xantiagoma/xtandard-image/internal/cache"
)

// Name is the default store label in metrics.
const Name = "redis"

// Defaults of Options.
const (
	DefaultMaxValueBytes = 1 << 20
	DefaultPrefix        = "xi:"
)

// Options configures New.
type Options struct {
	// URL is the server URL (redis:// or rediss://); it may carry a
	// password. Ignored when Client is set.
	URL string
	// Client, when set, is used instead of dialling URL (tests); Close
	// closes it.
	Client *goredis.Client
	// Prefix namespaces every key so several deployments can share one
	// server; "" uses DefaultPrefix.
	Prefix string
	// MaxValueBytes bounds a stored value (frame plus body); larger
	// entries are skipped. ≤ 0 uses DefaultMaxValueBytes.
	MaxValueBytes int
	// Name labels the metrics; "" uses Name.
	Name string
	// Metrics receives lookups and skips; nil discards.
	Metrics cache.Metrics
	// Now is the clock for the metadata TTL check; nil uses time.Now.
	Now func() time.Time
}

// Store is the Redis cache. It is safe for concurrent use.
type Store struct {
	client   *goredis.Client
	prefix   string
	maxValue int
	name     string
	metrics  cache.Metrics
	now      func() time.Time
	closed   atomic.Bool
}

// New returns a store for opts. It does not contact the server: an
// unreachable server makes every lookup a miss (logged by the caller).
func New(opts *Options) (*Store, error) {
	client := opts.Client
	if client == nil {
		if opts.URL == "" {
			return nil, errors.New("redis cache: needs a URL")
		}
		o, err := goredis.ParseURL(opts.URL)
		if err != nil {
			// The URL may hold a password: never echo it.
			return nil, errors.New("redis cache: invalid URL")
		}
		client = goredis.NewClient(o)
	}
	s := &Store{
		client:   client,
		prefix:   cmp.Or(opts.Prefix, DefaultPrefix),
		maxValue: opts.MaxValueBytes,
		name:     cmp.Or(opts.Name, Name),
		metrics:  opts.Metrics,
		now:      opts.Now,
	}
	if s.maxValue <= 0 {
		s.maxValue = DefaultMaxValueBytes
	}
	if s.metrics == nil {
		s.metrics = cache.NopMetrics{}
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

func (s *Store) redisKey(full string) string { return s.prefix + full }

// Get implements cache.Store. A missing key, an expired entry or an
// undecodable value is a miss (the last one counted as corrupt); a
// connection failure is returned for the caller to log.
func (s *Store) Get(ctx context.Context, ns cache.Namespace, key string) (cache.Entry, bool, error) {
	if s.closed.Load() {
		return cache.Entry{}, false, cache.ErrClosed
	}
	full := string(ns) + key
	e, ok, err := s.get(ctx, full)
	s.metrics.ObserveCache(s.name, ok)
	return e, ok, err
}

func (s *Store) get(ctx context.Context, full string) (cache.Entry, bool, error) {
	v, err := s.client.Get(ctx, s.redisKey(full)).Bytes()
	switch {
	case errors.Is(err, goredis.Nil):
		return cache.Entry{}, false, nil
	case err != nil:
		return cache.Entry{}, false, fmt.Errorf("redis cache: get: %w", err)
	}
	m, body, ok := cache.ParseEntry(v, full)
	if !ok {
		s.metrics.ObserveCacheSkip(s.name, cache.SkipCorrupt)
		return cache.Entry{}, false, nil
	}
	if m.Expired(s.now()) {
		return cache.Entry{}, false, nil
	}
	// The body aliases the reply buffer, which nothing else holds.
	return m.Entry(cache.NewBuffer(body, nil)), true, nil
}

// Set implements cache.Store with SET … PX ttl (no expiry for ttl ≤ 0).
// A value over MaxValueBytes is skipped and counted.
func (s *Store) Set(ctx context.Context, ns cache.Namespace, key string, e *cache.Entry, ttl time.Duration) error {
	if e.Body == nil {
		return errors.New("redis cache: entry without body")
	}
	if s.closed.Load() {
		return cache.ErrClosed
	}
	body := e.Body.Bytes()
	if len(body) > s.maxValue {
		s.metrics.ObserveCacheSkip(s.name, cache.SkipTooLarge)
		return nil
	}
	full := string(ns) + key
	m := cache.NewMeta(full, e, s.now(), ttl)
	v, err := cache.AppendFrame(make([]byte, 0, 512+len(body)), &m)
	if err != nil {
		return err
	}
	if len(v)+len(body) > s.maxValue {
		s.metrics.ObserveCacheSkip(s.name, cache.SkipTooLarge)
		return nil
	}
	v = append(v, body...)
	// SET key value PX <ms> (ms ≥ 1); plain SET for no expiry.
	args := []any{"set", s.redisKey(full), v}
	if ttl > 0 {
		args = append(args, "px", max(ttl.Milliseconds(), 1))
	}
	if err := s.client.Do(ctx, args...).Err(); err != nil {
		return fmt.Errorf("redis cache: set: %w", err)
	}
	return nil
}

// Delete implements cache.Store.
func (s *Store) Delete(ctx context.Context, ns cache.Namespace, key string) error {
	if s.closed.Load() {
		return cache.ErrClosed
	}
	if err := s.client.Del(ctx, s.redisKey(string(ns)+key)).Err(); err != nil {
		return fmt.Errorf("redis cache: delete: %w", err)
	}
	return nil
}

// Close implements cache.Store: it closes the connection pool.
func (s *Store) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	if err := s.client.Close(); err != nil {
		return fmt.Errorf("redis cache: close: %w", err)
	}
	return nil
}
