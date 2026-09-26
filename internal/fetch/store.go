package fetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

// originKeyVersion prefixes store keys of originals; bump it when the key
// or the stored header set changes.
const originKeyVersion = "v1/"

// WithStore backs the in-memory origin LRU with a shared cache.Store
// under cache.NSOrigin (XI_CACHE_ORIGIN_STORES, ARCHITECTURE §7), so
// replicas and restarts share originals: a flight that misses the LRU
// asks the store before downloading, and every download is written to the
// store for ttl (> 0). Pass an AsyncStore so writes never delay a fetch.
// Store failures are misses. A nil store or ttl ≤ 0 disables it.
//
// A store hit is copied into a Buffer from the fetcher's Allocator (C
// memory in production) and enters the LRU like a download. Store entries
// are not revalidated: they are served until ttl, like the result cache.
func WithStore(s cache.Store, ttl time.Duration) Option {
	return func(f *Fetcher) {
		if s != nil && ttl > 0 {
			f.store, f.storeTTL = s, ttl
		}
	}
}

// storeKey is the store key of the coalescing key ck: "v1/" plus the hex
// SHA-256, safe for every backend's key syntax.
func storeKey(ck string) string {
	sum := sha256.Sum256([]byte(ck))
	return originKeyVersion + hex.EncodeToString(sum[:])
}

// storeGet returns the original under ck from the shared store in a new
// Buffer owned by the caller.
func (f *Fetcher) storeGet(ctx context.Context, ck string) (Buffer, source.Object, bool) {
	ctx, cancel := context.WithTimeout(ctx, f.cfg.Timeout)
	defer cancel()
	e, ok, err := f.store.Get(ctx, cache.NSOrigin, storeKey(ck))
	if err != nil || !ok {
		return nil, source.Object{}, false
	}
	defer e.Release()
	b := e.Body.Bytes()
	if len(b) == 0 || int64(len(b)) > f.cfg.MaxBytes {
		return nil, source.Object{}, false
	}
	buf, err := f.alloc(len(b))
	if err != nil {
		return nil, source.Object{}, false
	}
	copy(buf.Bytes(), b)
	return buf, objectFromHeader(e.Header, int64(len(b))), true
}

// storePut writes a downloaded original to the shared store. The store
// takes its own reference to buf (fetch.Buffer is a cache.Buffer) or
// copies it.
func (f *Fetcher) storePut(ctx context.Context, ck string, buf Buffer, meta *source.Object) {
	e := cache.Entry{Body: buf, Status: http.StatusOK, Header: headerFromObject(meta)}
	_ = f.store.Set(ctx, cache.NSOrigin, storeKey(ck), &e, f.storeTTL) // best effort: a failed write is a later miss
}

// headerFromObject keeps the origin metadata the fetcher returns.
func headerFromObject(o *source.Object) http.Header {
	h := http.Header{}
	set := func(k, v string) {
		if v != "" {
			h[k] = []string{v}
		}
	}
	set("Content-Type", o.ContentType)
	set("Etag", o.ETag)
	set("Cache-Control", o.CacheControl)
	if !o.LastModified.IsZero() {
		set("Last-Modified", o.LastModified.UTC().Format(http.TimeFormat))
	}
	if !o.Expires.IsZero() {
		set("Expires", o.Expires.UTC().Format(http.TimeFormat))
	}
	return h
}

func objectFromHeader(h http.Header, size int64) source.Object {
	o := source.Object{
		Size:         size,
		ContentType:  h.Get("Content-Type"),
		ETag:         h.Get("ETag"),
		CacheControl: h.Get("Cache-Control"),
	}
	if t, err := http.ParseTime(h.Get("Last-Modified")); err == nil {
		o.LastModified = t
	}
	if t, err := http.ParseTime(h.Get("Expires")); err == nil {
		o.Expires = t
	}
	return o
}
