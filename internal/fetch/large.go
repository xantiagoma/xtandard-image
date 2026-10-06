package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

// Large originals (WP-121b): video sources of hundreds of megabytes.
// FetchLarge reads an original larger than the spool's threshold into a
// spool file instead of memory, and keeps that file for a while so the
// segments of an HLS or DASH stream (each its own transcode) and repeated
// transformations of the same source share one download and one copy.

// Spooler creates spool files for large originals (internal/spool, which
// app adapts).
type Spooler interface {
	// Spills reports whether an original of n bytes goes to a file.
	Spills(n int64) bool
	// Threshold is the size above which originals are spooled (> 0).
	Threshold() int64
	// Create opens a spool file reserving reserve bytes of the spool
	// budget (0 when the length is unknown). A full budget is an error
	// of kind errs.KindUnavailable.
	Create(reserve int64) (SpoolWriter, error)
	// SetReclaimer installs the function the spool calls when its budget
	// is full: the fetcher drops the spooled originals it keeps.
	SetReclaimer(func() bool)
}

// SpoolWriter fills one spool file; it ends with one Finish or Abort.
type SpoolWriter interface {
	io.Writer
	// Finish turns the n bytes written into a read-only buffer with one
	// reference (a *cache.FileBuffer).
	Finish(n int64) (Buffer, error)
	Abort()
	// Written is the bytes written so far.
	Written() int64
}

// WithSpool enables FetchLarge's files: originals larger than sp's
// threshold are spooled, and a spooled original is shared for ttl
// (XI_VIDEO_TRANSFORM_SPOOL_TTL): reused without asking the origin for
// ttl after its download or revalidation, kept while it is used at least
// once per ttl (then revalidated, conditionally when the origin sent an
// ETag or Last-Modified), and dropped after ttl without use, or at once
// when the spool's budget is full. ttl ≤ 0 shares nothing.
func WithSpool(sp Spooler, ttl time.Duration) Option {
	return func(f *Fetcher) {
		if sp == nil || sp.Threshold() <= 0 {
			return
		}
		f.spool = sp
		f.spooled = &spoolCache{ttl: ttl, m: make(map[string]*spoolEntry)}
		sp.SetReclaimer(f.spooled.reclaim)
	}
}

// FetchLarge is Fetch for a source that may be large (video): an
// original larger than the spool's threshold is read into a spool file
// (its Buffer is a *cache.FileBuffer: read it with ReadAt, not Bytes) and
// shared with later calls as WithSpool describes; a smaller one is
// fetched and cached exactly as by Fetch. Concurrent calls for one key
// share one download. Without WithSpool it is Fetch.
//
// Errors are Fetch's, plus errs.KindUnavailable when the spool budget is
// full.
func (f *Fetcher) FetchLarge(ctx context.Context, key source.Key, opt source.FetchOptions) (*Result, error) {
	if f.spool == nil {
		return f.Fetch(ctx, key, opt)
	}
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
	now := time.Now()
	buf, meta, hit, notFound := f.cache.lookup(ck, now)
	switch {
	case hit && notFound != nil:
		f.metrics.OnCache(CacheNegativeHit)
		return nil, notFound
	case hit:
		f.metrics.OnCache(CacheHit)
		return f.result(buf, &meta, true, limit)
	}
	if buf, meta, ok := f.spooled.fresh(ck, now); ok {
		f.metrics.OnCache(CacheSpoolHit)
		return f.result(buf, &meta, true, limit)
	}
	f.metrics.OnCache(CacheMiss)

	c, leader := f.large.join(ck)
	if leader {
		// Owner: the flight, bounded by the per-attempt timeout.
		go func() {
			defer f.large.finish(ck, c)
			c.buf, c.meta, c.fromCache, c.err = f.loadLarge(context.WithoutCancel(ctx), ck, src, key, opt)
		}()
	} else {
		f.metrics.OnCoalesced()
	}
	select {
	case <-c.done:
	case <-ctx.Done():
		select {
		case <-c.done:
		default:
			f.large.leave(c)
			return nil, fmt.Errorf("fetch %s: %w", source.RedactKey(key), context.Cause(ctx))
		}
	}
	if c.err != nil {
		f.large.leave(c)
		return nil, c.err
	}
	c.buf.Retain()
	f.large.leave(c)
	return f.result(c.buf, &c.meta, c.fromCache, limit)
}

// loadLarge is load for FetchLarge: the memory cache, the spooled
// originals, a conditional request for a stale one, else a download that
// spools a large body.
func (f *Fetcher) loadLarge(ctx context.Context, ck string, src source.Source, key source.Key, opt source.FetchOptions) (Buffer, source.Object, bool, error) {
	now := time.Now()
	if buf, meta, hit, notFound := f.cache.lookup(ck, now); hit {
		if notFound != nil {
			return nil, source.Object{}, false, notFound
		}
		return buf, meta, true, nil
	}
	if buf, meta, ok := f.spooled.fresh(ck, now); ok {
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
	staleBuf, staleMeta, spooledStale := f.spooled.stale(ck)
	stale := spooledStale
	if !stale {
		staleBuf, staleMeta, stale = f.cache.stale(ck)
	}
	if stale {
		opt.IfNoneMatch, opt.IfModifiedSince = staleMeta.ETag, staleMeta.LastModified
	}

	obj, buf, err := f.fetchWithRetry(ctx, src, key, opt, f.readLarge)
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
		if spooledStale {
			f.spooled.put(ck, staleBuf, &staleMeta, time.Now())
		} else {
			f.cache.refresh(ck, staleBuf, &staleMeta, time.Now())
		}
		return staleBuf, staleMeta, true, nil
	}
	if stale {
		staleBuf.Release()
	}
	meta := *obj
	meta.Body = nil
	meta.Size = cache.BodyLen(buf)
	if cache.IsFileBody(buf) {
		f.spooled.put(ck, buf, &meta, time.Now())
		return buf, meta, false, nil
	}
	f.cache.put(ck, buf, &meta, time.Now())
	if f.store != nil {
		f.storePut(ctx, ck, buf, &meta)
	}
	return buf, meta, false, nil
}

// readLarge reads a body like readBody, into a spool file when it is
// larger than the spool's threshold (declared, or found while reading a
// body of unknown length).
func (f *Fetcher) readLarge(r io.Reader, size, maxBytes int64) (Buffer, error) {
	switch {
	case size > maxBytes:
		return nil, tooLarge(size, maxBytes)
	case size == 0:
		return nil, errs.New(errs.KindCorrupt, "empty source body")
	case size > 0 && !f.spool.Spills(size):
		return readKnown(f.alloc, r, int(size))
	case size > 0:
		w, err := f.spool.Create(size)
		if err != nil {
			return nil, err
		}
		return spoolBody(w, r, size, size)
	}
	// Unknown length: memory up to the threshold, then a file.
	threshold := f.spool.Threshold()
	head, n, full, err := readPrefix(f.alloc, r, threshold+1)
	if err != nil {
		return nil, err
	}
	if !full {
		return exactBuffer(f.alloc, head, n)
	}
	w, err := f.spool.Create(0)
	if err != nil {
		head.Release()
		return nil, err
	}
	_, err = w.Write(head.Bytes()[:n])
	head.Release()
	if err != nil {
		w.Abort()
		return nil, err
	}
	return spoolBody(w, r, -1, maxBytes-int64(n))
}

// spoolBody copies the rest of r into w (what w already holds plus
// exactly want more bytes when want ≥ 0, at most limit more otherwise)
// and finishes it.
func spoolBody(w SpoolWriter, r io.Reader, want, limit int64) (Buffer, error) {
	n, err := io.Copy(w, io.LimitReader(r, limit+1))
	switch {
	case err != nil:
		w.Abort()
		if _, ok := errors.AsType[*errs.Error](err); ok {
			return nil, err
		}
		return nil, readError(err)
	case want >= 0 && n != want:
		w.Abort()
		if n > want {
			return nil, errs.Newf(errs.KindUpstreamError, "source body longer than its declared %d bytes", want)
		}
		return nil, errs.Newf(errs.KindUpstreamError, "source body shorter than its declared %d bytes", want)
	case n > limit:
		w.Abort()
		return nil, tooLarge(-1, limit)
	}
	return w.Finish(w.Written())
}

// spoolCache keeps the spooled originals of FetchLarge (see WithSpool).
// It holds one reference to each.
type spoolCache struct {
	ttl time.Duration
	mu  sync.Mutex
	m   map[string]*spoolEntry
}

type spoolEntry struct {
	buf     Buffer
	meta    source.Object
	fetched time.Time // download or last revalidation
	used    time.Time
	timer   *time.Timer
}

// fresh returns key's original when it was fetched less than ttl ago,
// with a reference for the caller. A nil cache holds nothing.
func (c *spoolCache) fresh(key string, now time.Time) (Buffer, source.Object, bool) {
	if c == nil {
		return nil, source.Object{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || now.Sub(e.fetched) >= c.ttl {
		return nil, source.Object{}, false
	}
	e.used = now
	e.buf.Retain()
	return e.buf, e.meta, true
}

// stale returns key's original when it is past ttl but can be
// revalidated, with a reference for the caller.
func (c *spoolCache) stale(key string) (Buffer, source.Object, bool) {
	if c == nil {
		return nil, source.Object{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || (e.meta.ETag == "" && e.meta.LastModified.IsZero()) {
		return nil, source.Object{}, false
	}
	e.buf.Retain()
	return e.buf, e.meta, true
}

// put keeps buf (taking its own reference) for key, fetched or
// revalidated at now, replacing another original.
func (c *spoolCache) put(key string, buf Buffer, meta *source.Object, now time.Time) {
	if c == nil || c.ttl <= 0 {
		return
	}
	var drop Buffer
	c.mu.Lock()
	e, ok := c.m[key]
	switch {
	case ok && e.buf == buf:
		e.fetched, e.used = now, now
		c.mu.Unlock()
		return
	case ok:
		drop = e.buf
		e.timer.Stop()
	}
	buf.Retain()
	e = &spoolEntry{buf: buf, meta: *meta, fetched: now, used: now}
	e.meta.Body = nil
	c.m[key] = e
	e.timer = time.AfterFunc(c.ttl, func() { c.expire(key, e) })
	c.mu.Unlock()
	if drop != nil {
		drop.Release()
	}
}

// expire drops e when it went ttl without use, else waits for that.
func (c *spoolCache) expire(key string, e *spoolEntry) {
	c.mu.Lock()
	if c.m[key] != e {
		c.mu.Unlock()
		return
	}
	if idle := time.Since(e.used); idle < c.ttl {
		e.timer.Reset(c.ttl - idle)
		c.mu.Unlock()
		return
	}
	delete(c.m, key)
	c.mu.Unlock()
	e.buf.Release()
}

// reclaim drops every kept original (the spool's budget is full): a file
// no request reads any more is closed at once, returning its bytes.
func (c *spoolCache) reclaim() bool {
	c.mu.Lock()
	drop := make([]Buffer, 0, len(c.m))
	for k, e := range c.m {
		e.timer.Stop()
		drop = append(drop, e.buf)
		delete(c.m, k)
	}
	c.mu.Unlock()
	releaseAll(drop)
	return len(drop) > 0
}

// usage reports the originals kept.
func (c *spoolCache) usage() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

// SpooledUsage reports how many spooled originals FetchLarge keeps.
func (f *Fetcher) SpooledUsage() int { return f.spooled.usage() }
