package fetch

import (
	"container/list"
	"sync"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/source"
)

// negativeEntrySize is the byte charge of a negative entry on top of its
// key, so a flood of distinct missing keys is bounded by the same budget.
const negativeEntrySize = 64

// entry is one cached original, or one cached not-found (buf == nil,
// notFound != nil).
type entry struct {
	key      string
	buf      Buffer // the cache's own reference
	meta     source.Object
	notFound error
	size     int64
	expires  time.Time
}

func (e *entry) hasValidators() bool {
	return e.buf != nil && (e.meta.ETag != "" || !e.meta.LastModified.IsZero())
}

// originCache is an in-memory LRU of originals bounded by total bytes, with
// a TTL per entry and short-lived negative entries for not-found keys. It
// holds one Buffer reference per entry and drops it on eviction.
type originCache struct {
	mu     sync.Mutex
	max    int64
	used   int64
	ttl    time.Duration
	negTTL time.Duration
	ll     list.List // front = most recently used; values are *entry
	items  map[string]*list.Element
}

func newOriginCache(maxBytes int64, ttl, negTTL time.Duration) *originCache {
	return &originCache{max: maxBytes, ttl: ttl, negTTL: negTTL, items: make(map[string]*list.Element)}
}

func (c *originCache) enabled() bool { return c.max > 0 && c.ttl > 0 }

// lookup returns a fresh entry for key. For a positive hit the returned
// buffer carries a new reference owned by the caller. An expired negative
// entry, or an expired positive entry that cannot be revalidated, is
// removed.
func (c *originCache) lookup(key string, now time.Time) (buf Buffer, meta source.Object, hit bool, notFound error) {
	var drop Buffer
	defer func() {
		if drop != nil {
			drop.Release()
		}
	}()
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, source.Object{}, false, nil
	}
	e := entryOf(el)
	if now.After(e.expires) {
		if !e.hasValidators() {
			drop = c.removeLocked(el)
		}
		return nil, source.Object{}, false, nil
	}
	c.ll.MoveToFront(el)
	if e.notFound != nil {
		return nil, source.Object{}, true, e.notFound
	}
	e.buf.Retain()
	return e.buf, e.meta, true, nil
}

// stale returns an expired entry that carries validators (ETag or
// Last-Modified) so the caller can revalidate it, with a new reference
// owned by the caller.
func (c *originCache) stale(key string) (buf Buffer, meta source.Object, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, found := c.items[key]
	if !found {
		return nil, source.Object{}, false
	}
	e := entryOf(el)
	if !e.hasValidators() {
		return nil, source.Object{}, false
	}
	e.buf.Retain()
	return e.buf, e.meta, true
}

// put stores buf (taking its own reference) under key, replacing any
// previous entry and evicting least recently used entries until the total
// fits. Originals larger than the whole budget are not cached.
func (c *originCache) put(key string, buf Buffer, meta *source.Object, now time.Time) {
	size := int64(len(buf.Bytes()))
	if !c.enabled() || size > c.max {
		return
	}
	e := &entry{key: key, buf: buf, meta: *meta, size: size, expires: now.Add(c.ttl)}
	e.meta.Body = nil
	buf.Retain()
	c.insert(e)
}

// putNotFound caches err for key for the negative TTL.
func (c *originCache) putNotFound(key string, err error, now time.Time) {
	if !c.enabled() || c.negTTL <= 0 {
		return
	}
	c.insert(&entry{key: key, notFound: err, size: int64(len(key)) + negativeEntrySize, expires: now.Add(c.negTTL)})
}

// refresh extends the TTL of key after a 304, provided the entry still
// holds buf; otherwise (evicted or replaced meanwhile) it stores buf anew.
func (c *originCache) refresh(key string, buf Buffer, meta *source.Object, now time.Time) {
	c.mu.Lock()
	if el, ok := c.items[key]; ok {
		if e := entryOf(el); e.buf == buf {
			e.expires = now.Add(c.ttl)
			c.ll.MoveToFront(el)
			c.mu.Unlock()
			return
		}
	}
	c.mu.Unlock()
	c.put(key, buf, meta, now)
}

func (c *originCache) insert(e *entry) {
	var drop []Buffer
	c.mu.Lock()
	if el, ok := c.items[e.key]; ok {
		drop = append(drop, c.removeLocked(el))
	}
	c.items[e.key] = c.ll.PushFront(e)
	c.used += e.size
	for c.used > c.max {
		drop = append(drop, c.removeLocked(c.ll.Back()))
	}
	c.mu.Unlock()
	releaseAll(drop)
}

// entryOf returns the entry stored in el; every element holds one.
func entryOf(el *list.Element) *entry {
	e, _ := el.Value.(*entry)
	return e
}

// removeLocked unlinks el and returns its buffer for the caller to release
// (outside the lock when convenient; nil for negative entries).
func (c *originCache) removeLocked(el *list.Element) Buffer {
	e := entryOf(el)
	c.ll.Remove(el)
	delete(c.items, e.key)
	c.used -= e.size
	return e.buf
}

// purge drops every entry and its reference.
func (c *originCache) purge() {
	c.mu.Lock()
	drop := make([]Buffer, 0, c.ll.Len())
	for c.ll.Len() > 0 {
		drop = append(drop, c.removeLocked(c.ll.Back()))
	}
	c.mu.Unlock()
	releaseAll(drop)
}

// usage reports the bytes and entries held (for tests and gauges).
func (c *originCache) usage() (bytes int64, entries int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used, c.ll.Len()
}

func releaseAll(bufs []Buffer) {
	for _, b := range bufs {
		if b != nil {
			b.Release()
		}
	}
}
