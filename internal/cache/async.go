package cache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/semaphore"
)

// Defaults of AsyncOptions (docs/CONFIG.md XI_CACHE_RESULT_WRITE_QUEUE).
const (
	DefaultWriteQueue   = 64
	DefaultWriteTimeout = 30 * time.Second
)

// AsyncOptions configures NewAsync.
type AsyncOptions struct {
	// Queue is the most writes in flight at once
	// (XI_CACHE_RESULT_WRITE_QUEUE); a write beyond it is dropped. ≤ 0
	// uses DefaultWriteQueue.
	Queue int
	// Timeout bounds each write, which runs detached from the request
	// context (go-standards §3). ≤ 0 uses DefaultWriteTimeout.
	Timeout time.Duration
	// Metrics counts dropped writes; nil discards.
	Metrics Metrics
	// OnError receives write failures (the app logs them); nil ignores
	// them. It runs on the writing goroutine.
	OnError func(ns Namespace, key string, err error)
}

// AsyncStore wraps a Store so that Set never blocks the caller: it takes
// a reference to the body and writes on a new goroutine, with at most
// Queue writes in flight (an x/sync semaphore, TryAcquire; no channel
// worker pool). When the queue is full the write is dropped, counted in
// xi_cache_writes_dropped_total, and Set returns ErrDropped. Get and
// Delete go straight to the wrapped store.
//
// Every write goroutine is owned by the AsyncStore: Wait blocks until the
// writes started so far finish, and Close stops new writes, waits, then
// closes the wrapped store.
type AsyncStore struct {
	store   Store
	sem     *semaphore.Weighted
	timeout time.Duration
	metrics Metrics
	onError func(Namespace, string, error)

	wg     sync.WaitGroup
	mu     sync.RWMutex // guards closed against wg.Go racing Close's wg.Wait
	closed bool
	once   sync.Once
	drops  atomic.Int64
}

// NewAsync returns an AsyncStore over store.
func NewAsync(store Store, opts AsyncOptions) (*AsyncStore, error) {
	if store == nil {
		return nil, errors.New("cache: async store needs a store")
	}
	if opts.Queue <= 0 {
		opts.Queue = DefaultWriteQueue
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultWriteTimeout
	}
	if opts.Metrics == nil {
		opts.Metrics = NopMetrics{}
	}
	return &AsyncStore{
		store:   store,
		sem:     semaphore.NewWeighted(int64(opts.Queue)),
		timeout: opts.Timeout,
		metrics: opts.Metrics,
		onError: opts.OnError,
	}, nil
}

// Get implements Store.
func (a *AsyncStore) Get(ctx context.Context, ns Namespace, key string) (Entry, bool, error) {
	return a.store.Get(ctx, ns, key)
}

// Set implements Store without blocking: it returns nil once the write is
// queued, ErrDropped when the queue is full and ErrClosed after Close. The
// write keeps its own reference to e.Body until it finishes and runs under
// context.WithoutCancel(ctx) bounded by the write timeout, so it outlives
// the request that produced the entry.
func (a *AsyncStore) Set(ctx context.Context, ns Namespace, key string, e *Entry, ttl time.Duration) error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.closed {
		return ErrClosed
	}
	if !a.sem.TryAcquire(1) {
		a.drops.Add(1)
		a.metrics.IncCacheWriteDropped()
		return ErrDropped
	}
	ent := *e // the caller may reuse e once Set returns
	if ent.Body != nil {
		ent.Body.Retain()
	}
	wctx := context.WithoutCancel(ctx)
	a.wg.Go(func() {
		defer a.sem.Release(1)
		defer ent.Release()
		ctx, cancel := context.WithTimeout(wctx, a.timeout)
		defer cancel()
		if err := a.store.Set(ctx, ns, key, &ent, ttl); err != nil && a.onError != nil {
			a.onError(ns, key, err)
		}
	})
	return nil
}

// Delete implements Store (synchronously).
func (a *AsyncStore) Delete(ctx context.Context, ns Namespace, key string) error {
	return a.store.Delete(ctx, ns, key)
}

// Wait blocks until every write queued so far has finished. Call it
// when no Set runs concurrently (tests, after the listeners drained):
// sync.WaitGroup forbids a Wait racing the first Go of a new batch.
func (a *AsyncStore) Wait() { a.wg.Wait() }

// Dropped reports how many writes were dropped because the queue was full.
func (a *AsyncStore) Dropped() int64 { return a.drops.Load() }

// Close implements Store: new writes fail with ErrClosed, queued writes
// finish, then the wrapped store is closed. It is idempotent.
func (a *AsyncStore) Close() error {
	var err error
	a.once.Do(func() {
		a.mu.Lock()
		a.closed = true
		a.mu.Unlock()
		a.wg.Wait()
		err = a.store.Close()
	})
	return err
}
