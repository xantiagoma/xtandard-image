// Package fetch downloads originals from configured sources
// (docs/ARCHITECTURE.md §7). It coalesces concurrent fetches of one key,
// keeps recently fetched originals in a byte-bounded in-memory LRU of
// refcounted Buffers (optionally backed by a shared cache.Store, store.go), enforces the source byte limit while streaming,
// bounds every attempt with a timeout and retries transient failures once.
//
// The package is pure Go: originals land in Buffers from an injected
// Allocator (vips.NewBuffer in production), so it never imports vips.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

// Defaults applied by New to zero Config fields (docs/CONFIG.md).
const (
	DefaultTimeout      = 10 * time.Second // XI_FETCH_TIMEOUT
	DefaultMaxBytes     = 50 << 20         // XI_LIMITS_MAX_SRC_BYTES
	DefaultRetryBackoff = 100 * time.Millisecond
)

// Config is the fetcher's slice of the application config; app fills it
// from XI_FETCH_*, XI_LIMITS_MAX_SRC_BYTES and XI_CACHE_ORIGIN_*.
type Config struct {
	// Timeout bounds each download attempt, body included (XI_FETCH_TIMEOUT).
	Timeout time.Duration
	// MaxBytes is the largest original accepted (XI_LIMITS_MAX_SRC_BYTES).
	MaxBytes int64
	// CacheBytes bounds the origin LRU (XI_CACHE_ORIGIN_MEMORY_BYTES);
	// 0 disables it.
	CacheBytes int64
	// CacheTTL is how long a cached original is served without asking the
	// origin (XI_CACHE_ORIGIN_TTL); after it, entries with an ETag or
	// Last-Modified are revalidated conditionally. 0 disables the cache.
	CacheTTL time.Duration
	// NegativeTTL is how long a not-found is remembered
	// (XI_CACHE_ORIGIN_NEGATIVE_TTL); 0 disables negative caching.
	NegativeTTL time.Duration
	// Retry enables exactly one retry of a transient failure (upstream 5xx,
	// connection error, attempt timeout) after a jittered backoff
	// (XI_FETCH_RETRY).
	Retry bool
	// RetryBackoff is the base delay before the retry; the actual delay is
	// uniformly jittered in [RetryBackoff/2, 3·RetryBackoff/2).
	RetryBackoff time.Duration
}

func (c *Config) setDefaults() {
	if c.Timeout <= 0 {
		c.Timeout = DefaultTimeout
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = DefaultMaxBytes
	}
	if c.RetryBackoff <= 0 {
		c.RetryBackoff = DefaultRetryBackoff
	}
}

// Sources is what the fetcher needs from the source registry;
// *source.Registry implements it.
type Sources interface {
	Source(name string) (source.Source, bool)
	ResolveRedirect(from source.Key, location string) (source.Key, error)
}

// Result is a fetched original. The caller owns one reference to Buffer and
// must call Release exactly when done with the bytes.
type Result struct {
	Buffer Buffer
	// Object is the origin's metadata; Body is always nil.
	Object source.Object
	// FromCache reports that the bytes came from the origin cache (a fresh
	// hit or a 304 revalidation) rather than a new download.
	FromCache bool

	released atomic.Bool
}

// Bytes returns the original's bytes; valid until Release.
func (r *Result) Bytes() []byte { return r.Buffer.Bytes() }

// Release drops the caller's reference. Extra calls are no-ops.
func (r *Result) Release() {
	if r.released.CompareAndSwap(false, true) {
		r.Buffer.Release()
	}
}

// Option configures New.
type Option func(*Fetcher)

// WithMetrics installs m; nil keeps NopMetrics.
func WithMetrics(m Metrics) Option {
	return func(f *Fetcher) {
		if m != nil {
			f.metrics = m
		}
	}
}

// Fetcher fetches originals. It is safe for concurrent use.
type Fetcher struct {
	sources Sources
	alloc   Allocator
	metrics Metrics
	cache   *originCache
	flights flightGroup
	cfg     Config

	store    cache.Store // optional shared origin store (WithStore)
	storeTTL time.Duration

	// FetchLarge (large.go, WithSpool): nil spool fetches like Fetch.
	spool   Spooler
	spooled *spoolCache
	large   flightGroup
}

// New returns a Fetcher over sources that stores originals in buffers from
// alloc. Zero Timeout, MaxBytes and RetryBackoff take the package defaults.
func New(sources Sources, alloc Allocator, cfg Config, opts ...Option) (*Fetcher, error) {
	if sources == nil || alloc == nil {
		return nil, errors.New("fetch: New needs sources and an allocator")
	}
	if cfg.CacheBytes < 0 || cfg.CacheTTL < 0 || cfg.NegativeTTL < 0 {
		return nil, errors.New("fetch: negative cache size or ttl")
	}
	cfg.setDefaults()
	f := &Fetcher{
		sources: sources,
		alloc:   alloc,
		metrics: NopMetrics{},
		cache:   newOriginCache(cfg.CacheBytes, cfg.CacheTTL, cfg.NegativeTTL),
		cfg:     cfg,
	}
	for _, o := range opts {
		o(f)
	}
	return f, nil
}

// Fetch returns the original addressed by key. Concurrent calls for one key
// share a single backend fetch and a single Buffer; a caller whose ctx ends
// stops waiting but does not abort the shared fetch, which runs on a
// detached context bounded by the configured timeout.
//
// The fetcher owns opt.IfNoneMatch and opt.IfModifiedSince (it sets them to
// revalidate cached entries) and binds a nil opt.CheckRedirect to the
// registry's redirect policy for key. opt.MaxBytes, when positive and below
// the configured limit, is enforced for this caller only.
func (f *Fetcher) Fetch(ctx context.Context, key source.Key, opt source.FetchOptions) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("fetch %s: %w", source.RedactKey(key), context.Cause(ctx))
	}
	src, ok := f.sources.Source(key.Source)
	if !ok {
		return nil, errs.Newf(errs.KindForbidden, "fetch %s: unknown source", source.RedactKey(key))
	}
	ck := cacheKey(key)
	limit := f.cfg.MaxBytes
	if opt.MaxBytes > 0 {
		limit = min(limit, opt.MaxBytes)
	}

	buf, meta, hit, notFound := f.cache.lookup(ck, time.Now())
	switch {
	case hit && notFound != nil:
		f.metrics.OnCache(CacheNegativeHit)
		return nil, notFound
	case hit:
		f.metrics.OnCache(CacheHit)
		return f.result(buf, &meta, true, limit)
	}
	f.metrics.OnCache(CacheMiss)

	c, leader := f.flights.join(ck)
	if leader {
		// Owner: the flight. It exits when the fetch ends, which the
		// per-attempt timeout bounds; finish wakes every waiter.
		go f.run(context.WithoutCancel(ctx), ck, c, src, key, opt)
	} else {
		f.metrics.OnCoalesced()
	}

	select {
	case <-c.done:
	case <-ctx.Done():
		select {
		case <-c.done: // finished too; prefer the result
		default:
			f.flights.leave(c)
			return nil, fmt.Errorf("fetch %s: %w", source.RedactKey(key), context.Cause(ctx))
		}
	}
	if c.err != nil {
		f.flights.leave(c)
		return nil, c.err
	}
	c.buf.Retain()
	f.flights.leave(c)
	return f.result(c.buf, &c.meta, c.fromCache, limit)
}

// result wraps a buffer reference the caller already owns, enforcing the
// caller's byte limit.
func (f *Fetcher) result(buf Buffer, meta *source.Object, fromCache bool, limit int64) (*Result, error) {
	if n := cache.BodyLen(buf); n > limit {
		buf.Release()
		return nil, tooLarge(n, limit)
	}
	return &Result{Buffer: buf, Object: *meta, FromCache: fromCache}, nil
}

// Purge drops every cached original, spooled ones included. Buffers
// still held by callers stay valid until their own Release.
func (f *Fetcher) Purge() {
	f.cache.purge()
	if f.spooled != nil {
		f.spooled.reclaim()
	}
}

// CacheUsage reports the bytes and entries held by the origin cache.
func (f *Fetcher) CacheUsage() (bytes int64, entries int) { return f.cache.usage() }

// run is the flight leader: it fills c and publishes it.
func (f *Fetcher) run(ctx context.Context, ck string, c *call, src source.Source, key source.Key, opt source.FetchOptions) {
	defer f.flights.finish(ck, c)
	c.buf, c.meta, c.fromCache, c.err = f.load(ctx, ck, src, key, opt)
}

// load returns an original with one reference owned by the caller: from the
// cache if another flight filled it meanwhile, via a conditional request if
// a stale entry has validators, or by a full download.
func (f *Fetcher) load(ctx context.Context, ck string, src source.Source, key source.Key, opt source.FetchOptions) (Buffer, source.Object, bool, error) {
	if buf, meta, hit, notFound := f.cache.lookup(ck, time.Now()); hit {
		if notFound != nil {
			return nil, source.Object{}, false, notFound
		}
		return buf, meta, true, nil
	}

	opt.MaxBytes = f.cfg.MaxBytes
	opt.IfNoneMatch, opt.IfModifiedSince = "", time.Time{}
	if opt.CheckRedirect == nil {
		opt.CheckRedirect = func(location string) error {
			_, err := f.sources.ResolveRedirect(key, location)
			return err
		}
	}
	staleBuf, staleMeta, stale := f.cache.stale(ck)
	if stale {
		opt.IfNoneMatch, opt.IfModifiedSince = staleMeta.ETag, staleMeta.LastModified
	} else if f.store != nil {
		if buf, meta, ok := f.storeGet(ctx, ck); ok {
			f.metrics.OnCache(CacheStoreHit)
			f.cache.put(ck, buf, &meta, time.Now())
			return buf, meta, true, nil
		}
	}

	obj, buf, err := f.fetchWithRetry(ctx, src, key, opt, f.readMemory)
	switch {
	case err != nil:
		if stale {
			staleBuf.Release()
		}
		if errors.Is(err, errs.Match(errs.KindNotFound)) {
			f.cache.putNotFound(ck, err, time.Now())
		}
		return nil, source.Object{}, false, err
	case obj.NotModified:
		if !stale {
			return nil, source.Object{}, false, errs.Newf(errs.KindUpstreamError,
				"fetch %s: 304 Not Modified to an unconditional request", source.RedactKey(key))
		}
		f.metrics.OnCache(CacheRevalidated)
		f.cache.refresh(ck, staleBuf, &staleMeta, time.Now())
		return staleBuf, staleMeta, true, nil
	}
	if stale {
		staleBuf.Release()
	}
	meta := *obj
	meta.Body = nil
	meta.Size = cache.BodyLen(buf)
	f.cache.put(ck, buf, &meta, time.Now())
	if f.store != nil {
		f.storePut(ctx, ck, buf, &meta)
	}
	return buf, meta, false, nil
}

// fetchWithRetry runs one attempt and, when enabled, retries a transient
// failure exactly once after a jittered backoff.
func (f *Fetcher) fetchWithRetry(ctx context.Context, src source.Source, key source.Key, opt source.FetchOptions, read readFunc) (*source.Object, Buffer, error) {
	obj, buf, err := f.attempt(ctx, src, key, opt, read)
	if err == nil || !f.cfg.Retry || !transient(err) {
		return obj, buf, err
	}
	f.metrics.OnRetry(src.Name())
	half := int64(f.cfg.RetryBackoff / 2)
	t := time.NewTimer(time.Duration(half + rand.Int64N(2*half+1))) //nolint:gosec // G404: backoff jitter needs no cryptographic randomness
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
		return nil, nil, err
	}
	return f.attempt(ctx, src, key, opt, read)
}

// readFunc reads a body of size bytes (-1 unknown) up to maxBytes.
type readFunc func(r io.Reader, size, maxBytes int64) (Buffer, error)

// readMemory is Fetch's readFunc: the body into a Buffer from the
// allocator.
func (f *Fetcher) readMemory(r io.Reader, size, maxBytes int64) (Buffer, error) {
	return readBody(f.alloc, r, size, maxBytes)
}

// attempt performs one download bounded by the configured timeout.
func (f *Fetcher) attempt(ctx context.Context, src source.Source, key source.Key, opt source.FetchOptions, read readFunc) (_ *source.Object, _ Buffer, err error) {
	timeout := errs.Newf(errs.KindUpstreamTimeout, "fetch %s: no response within %s", source.RedactKey(key), f.cfg.Timeout)
	actx, cancel := context.WithTimeoutCause(ctx, f.cfg.Timeout, timeout)
	defer cancel()

	start := time.Now()
	var n int64
	outcome := "ok"
	defer func() {
		if err != nil {
			outcome = errs.KindOf(err).String()
		}
		f.metrics.ObserveFetch(src.Name(), outcome, time.Since(start), n)
	}()

	obj, err := src.Fetch(actx, key, opt)
	if err != nil {
		return nil, nil, classify(actx, timeout, err)
	}
	if obj.NotModified {
		outcome = "not_modified"
		if obj.Body != nil {
			_ = obj.Body.Close() // nothing to read; a close error changes nothing
		}
		return obj, nil, nil
	}
	if obj.Body == nil {
		return nil, nil, errs.Newf(errs.KindInternal, "fetch %s: source returned no body", source.RedactKey(key))
	}
	defer func() { _ = obj.Body.Close() }() // read-only body: the read error is what matters
	buf, err := read(obj.Body, obj.Size, f.cfg.MaxBytes)
	if err != nil {
		return nil, nil, classify(actx, timeout, err)
	}
	n = cache.BodyLen(buf)
	return obj, buf, nil
}

// classify attributes err to the attempt timeout when it fired, and gives
// errors without a kind KindUpstreamError.
func classify(actx context.Context, timeout, err error) error {
	if errors.Is(context.Cause(actx), timeout) {
		return errors.Join(timeout, err)
	}
	if _, ok := errors.AsType[*errs.Error](err); ok {
		return err
	}
	return errs.Wrap(errs.KindUpstreamError, err)
}

// transient reports whether a retry may succeed.
func transient(err error) bool {
	switch errs.KindOf(err) {
	case errs.KindUpstreamError, errs.KindUpstreamTimeout:
		return true
	default:
		return false
	}
}

// cacheKey is the canonical coalescing and cache key of an original.
func cacheKey(k source.Key) string {
	return k.Source + "\x00" + k.Bucket + "\x00" + k.Path + "\x00" + k.Version
}
