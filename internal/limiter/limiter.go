// Package limiter admits image-processing jobs through the two-semaphore
// design of docs/ARCHITECTURE.md §11 (copied from imgproxy workers/workers.go):
//
//   - a queue semaphore sized workers+queue, taken with TryAcquire so a full
//     queue fails fast (errs.KindOverloaded, 429 + Retry-After);
//   - a worker semaphore sized workers, waited on for at most the queue
//     timeout (errs.KindUnavailable, 503 + Retry-After);
//   - an optional memory-weighted semaphore: each job declares its estimated
//     peak bytes and a job larger than the whole budget is rejected up front
//     (errs.KindLimitExceeded, 422) instead of deadlocking. The estimate
//     comes from the planner, which needs the probed header, so a job that
//     was admitted with Acquire(ctx, 0) charges it with Reserve once its plan
//     exists and before any pixel is decoded.
//
// Every successful Acquire or Reserve returns a release func that is
// idempotent and returns all its permits, whether or not the request
// context was cancelled.
package limiter

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync/atomic"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// MaxRetryAfter caps the Retry-After hint.
const MaxRetryAfter = 60 * time.Second

// errQueueTimeout is the cause attached to the worker-wait context, so a
// timeout of our own is told apart from the caller's deadline.
var errQueueTimeout = errors.New("limiter: queue timeout")

// Config sizes the limiter. The zero value is invalid; internal/config fills
// it from XI_PROCESSING_WORKERS, XI_PROCESSING_QUEUE_SIZE,
// XI_PROCESSING_QUEUE_TIMEOUT and XI_PROCESSING_MEMORY_BUDGET.
type Config struct {
	Workers      int           // concurrent jobs, ≥ 1
	QueueSize    int           // jobs allowed to wait for a worker, ≥ 0
	QueueTimeout time.Duration // max wait for a worker (and memory); 0 = bounded by ctx only
	MemoryBudget int64         // bytes for weighted admission; 0 = off
}

// Validate reports every invalid field.
func (c Config) Validate() error {
	var errList []error
	if c.Workers < 1 {
		errList = append(errList, fmt.Errorf("limiter: workers must be >= 1, got %d", c.Workers))
	}
	if c.QueueSize < 0 {
		errList = append(errList, fmt.Errorf("limiter: queue size must be >= 0, got %d", c.QueueSize))
	}
	if c.QueueTimeout < 0 {
		errList = append(errList, fmt.Errorf("limiter: queue timeout must be >= 0, got %s", c.QueueTimeout))
	}
	if c.MemoryBudget < 0 {
		errList = append(errList, fmt.Errorf("limiter: memory budget must be >= 0, got %d", c.MemoryBudget))
	}
	return errors.Join(errList...)
}

// Reason says why a request was not admitted. It is the {reason} label of
// xi_overload_total.
type Reason uint8

// Rejection reasons.
const (
	ReasonQueueFull      Reason = iota + 1 // queue semaphore full → 429
	ReasonQueueTimeout                     // no worker (or memory) within QueueTimeout → 503
	ReasonMemoryTooLarge                   // estimate above the whole budget → 422
	ReasonCanceled                         // caller's context done while waiting
	ReasonQueueCost                        // queued cost over its bound → 429 (the video pool, WP-121c)
)

// String returns the snake_case metric label.
func (r Reason) String() string {
	switch r {
	case ReasonQueueFull:
		return "queue_full"
	case ReasonQueueTimeout:
		return "queue_timeout"
	case ReasonMemoryTooLarge:
		return "memory_too_large"
	case ReasonCanceled:
		return "canceled"
	case ReasonQueueCost:
		return "queue_cost"
	default:
		return "unknown"
	}
}

// Metrics receives admission events. Implementations must be safe for
// concurrent use and cheap: they run on every request. Gauges are reported
// as deltas so concurrent updates never leave a stale value behind.
type Metrics interface {
	OnQueued()                     // passed the queue, now waiting for a worker
	OnAdmitted(wait time.Duration) // got a worker; wait is time spent queued
	OnRejected(reason Reason)      // not admitted
	AddQueueDepth(delta int64)     // xi_queue_depth: jobs waiting for a worker
	AddInFlight(delta int64)       // xi_workers_busy: admitted jobs
}

// NopMetrics discards every event. It is the default.
type NopMetrics struct{}

// OnQueued implements Metrics.
func (NopMetrics) OnQueued() {}

// OnAdmitted implements Metrics.
func (NopMetrics) OnAdmitted(time.Duration) {}

// OnRejected implements Metrics.
func (NopMetrics) OnRejected(Reason) {}

// AddQueueDepth implements Metrics.
func (NopMetrics) AddQueueDepth(int64) {}

// AddInFlight implements Metrics.
func (NopMetrics) AddInFlight(int64) {}

// Option configures New.
type Option func(*Limiter)

// WithMetrics installs m; nil keeps NopMetrics.
func WithMetrics(m Metrics) Option {
	return func(l *Limiter) {
		if m != nil {
			l.metrics = m
		}
	}
}

// OverloadError wraps a 429 or 503 rejection with the Retry-After hint.
// The wrapped error is an *errs.Error, so errs.KindOf and the server's
// status mapping see through it.
type OverloadError struct {
	Reason     Reason
	RetryAfter time.Duration // whole seconds, in [1s, MaxRetryAfter]
	Err        error
}

// Error implements error.
func (e *OverloadError) Error() string { return e.Err.Error() }

// Unwrap returns the classified cause.
func (e *OverloadError) Unwrap() error { return e.Err }

// Stats is a point-in-time snapshot, for /readyz and debugging.
type Stats struct {
	Workers  int   // worker semaphore size
	Capacity int   // queue semaphore size (workers + queue)
	Waiting  int64 // waiting for a worker or memory
	InFlight int64 // admitted
}

// Limiter bounds concurrent processing. Create it with New; it is safe for
// concurrent use.
type Limiter struct {
	queue   *semaphore.Weighted
	workers *semaphore.Weighted
	memory  *semaphore.Weighted // nil when MemoryBudget is 0
	metrics Metrics
	cfg     Config

	waiting  atomic.Int64
	inFlight atomic.Int64
	avgHold  atomic.Int64 // EWMA of permit hold time, ns; 0 = no sample yet
}

// New validates cfg and returns a Limiter.
func New(cfg Config, opts ...Option) (*Limiter, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	l := &Limiter{
		queue:   semaphore.NewWeighted(int64(cfg.Workers) + int64(cfg.QueueSize)),
		workers: semaphore.NewWeighted(int64(cfg.Workers)),
		metrics: NopMetrics{},
		cfg:     cfg,
	}
	if cfg.MemoryBudget > 0 {
		l.memory = semaphore.NewWeighted(cfg.MemoryBudget)
	}
	for _, o := range opts {
		o(l)
	}
	return l, nil
}

// Acquire admits one job whose estimated peak memory is cost bytes (ignored
// when no memory budget is set; ≤ 0 means unknown). On success it returns a
// release func that must be called when the job ends; calling it more than
// once is a no-op. On failure no permit is held.
//
// Errors: KindLimitExceeded (cost above the budget), *OverloadError wrapping
// KindOverloaded (queue full) or KindUnavailable (queue timeout), and
// KindCanceled / KindTimeout when ctx ends first.
func (l *Limiter) Acquire(ctx context.Context, cost int64) (release func(), err error) {
	if l.memory == nil || cost < 0 {
		cost = 0
	}
	if cost > l.cfg.MemoryBudget {
		l.metrics.OnRejected(ReasonMemoryTooLarge)
		return nil, errs.Newf(errs.KindLimitExceeded,
			"limiter: estimated memory %d bytes exceeds budget %d", cost, l.cfg.MemoryBudget)
	}
	if ctx.Err() != nil {
		return nil, l.canceled(ctx)
	}
	if !l.queue.TryAcquire(1) {
		l.metrics.OnRejected(ReasonQueueFull)
		return nil, &OverloadError{
			Reason:     ReasonQueueFull,
			RetryAfter: l.RetryAfter(),
			Err:        errs.New(errs.KindOverloaded, "limiter: queue full"),
		}
	}

	l.waiting.Add(1)
	l.metrics.AddQueueDepth(1)
	l.metrics.OnQueued()
	start := time.Now()

	err = l.wait(ctx, cost)

	l.waiting.Add(-1)
	l.metrics.AddQueueDepth(-1)
	if err != nil {
		l.queue.Release(1)
		return nil, l.waitError(ctx)
	}

	l.inFlight.Add(1)
	l.metrics.AddInFlight(1)
	now := time.Now()
	l.metrics.OnAdmitted(now.Sub(start))
	p := &permit{l: l, cost: cost, start: now}
	return p.release, nil
}

// TryAcquire admits one job only when a worker is free at once and no
// other job waits for one, without queueing: for work that may be
// skipped, such as prefetching (WP-126), which must never delay or
// displace a request. ok is false when nothing was admitted.
func (l *Limiter) TryAcquire() (release func(), ok bool) {
	if !l.queue.TryAcquire(1) {
		return nil, false
	}
	if !l.workers.TryAcquire(1) {
		l.queue.Release(1)
		return nil, false
	}
	l.inFlight.Add(1)
	l.metrics.AddInFlight(1)
	p := &permit{l: l, start: time.Now()}
	return p.release, true
}

// noRelease is Reserve's release when nothing was reserved: a package-level
// func value, so the common case (no memory budget) does not allocate.
var noRelease = func() {}

// Reserve charges cost bytes (a plan's estimated peak memory) against the
// memory budget for a job already admitted by Acquire, waiting for other
// jobs to release theirs for at most QueueTimeout. Without a budget, or
// with cost ≤ 0, it returns at once without allocating. Call it on an
// admitted job only: a job never waits for memory while it has none of
// its own reserved, so the wait cannot deadlock.
//
// Errors: KindLimitExceeded (cost above the whole budget, 422),
// *OverloadError wrapping KindUnavailable (QueueTimeout passed, 503), and
// KindCanceled / KindTimeout when ctx ends first.
func (l *Limiter) Reserve(ctx context.Context, cost int64) (release func(), err error) {
	if l.memory == nil || cost <= 0 {
		return noRelease, nil
	}
	if cost > l.cfg.MemoryBudget {
		l.metrics.OnRejected(ReasonMemoryTooLarge)
		return nil, errs.Newf(errs.KindLimitExceeded,
			"limiter: estimated memory %d bytes exceeds budget %d", cost, l.cfg.MemoryBudget)
	}
	if !l.memory.TryAcquire(cost) {
		waitCtx := ctx
		if l.cfg.QueueTimeout > 0 {
			var cancel context.CancelFunc
			waitCtx, cancel = context.WithTimeoutCause(ctx, l.cfg.QueueTimeout, errQueueTimeout)
			defer cancel()
		}
		if err := l.memory.Acquire(waitCtx, cost); err != nil {
			return nil, l.waitError(ctx)
		}
	}
	r := &reservation{l: l, cost: cost}
	return r.release, nil
}

// reservation is one Reserve.
type reservation struct {
	l    *Limiter
	cost int64
	done atomic.Bool
}

func (r *reservation) release() {
	if r.done.CompareAndSwap(false, true) {
		r.l.memory.Release(r.cost)
	}
}

// wait takes a worker and then the memory weight, both within QueueTimeout.
// On error it holds nothing (the caller still holds the queue permit) and the
// caller classifies the failure with waitError once it no longer counts
// itself as waiting.
func (l *Limiter) wait(ctx context.Context, cost int64) error {
	// Fast path: no timer or derived context when permits are free.
	// TryAcquire fails while others wait, so FIFO order is preserved.
	gotWorker := l.workers.TryAcquire(1)
	if gotWorker && (cost == 0 || l.memory.TryAcquire(cost)) {
		return nil
	}

	waitCtx := ctx
	if l.cfg.QueueTimeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeoutCause(ctx, l.cfg.QueueTimeout, errQueueTimeout)
		defer cancel()
	}
	if !gotWorker {
		if err := l.workers.Acquire(waitCtx, 1); err != nil {
			return err
		}
	}
	if cost > 0 {
		if err := l.memory.Acquire(waitCtx, cost); err != nil {
			l.workers.Release(1)
			return err
		}
	}
	return nil
}

// waitError classifies a failed wait: the caller's context wins, otherwise it
// was our queue timeout.
func (l *Limiter) waitError(ctx context.Context) error {
	if ctx.Err() != nil {
		return l.canceled(ctx)
	}
	l.metrics.OnRejected(ReasonQueueTimeout)
	return &OverloadError{
		Reason:     ReasonQueueTimeout,
		RetryAfter: l.RetryAfter(),
		Err:        errs.Wrapf(errs.KindUnavailable, errQueueTimeout, "limiter: no worker within %s", l.cfg.QueueTimeout),
	}
}

// canceled reports ctx ending before admission: KindCanceled for a client
// disconnect, KindTimeout for the request deadline.
func (l *Limiter) canceled(ctx context.Context) error {
	l.metrics.OnRejected(ReasonCanceled)
	return errs.Wrapf(errs.KindOf(ctx.Err()), context.Cause(ctx), "limiter: wait for worker")
}

// RetryAfter estimates when a rejected client should retry: one average job
// duration per round of queued jobs ahead of it. Before any job finished the
// queue timeout stands in for the average. The result is whole seconds in
// [1s, MaxRetryAfter], ready for the Retry-After header.
func (l *Limiter) RetryAfter() time.Duration {
	hold := time.Duration(l.avgHold.Load())
	if hold <= 0 {
		hold = l.cfg.QueueTimeout
	}
	rounds := float64(l.waiting.Load()/int64(l.cfg.Workers) + 1)
	secs := math.Ceil(hold.Seconds() * rounds)
	secs = min(max(secs, 1), MaxRetryAfter.Seconds())
	return time.Duration(secs) * time.Second
}

// Stats returns current occupancy.
func (l *Limiter) Stats() Stats {
	return Stats{
		Workers:  l.cfg.Workers,
		Capacity: l.cfg.Workers + l.cfg.QueueSize,
		Waiting:  l.waiting.Load(),
		InFlight: l.inFlight.Load(),
	}
}

// observeHold folds d into the hold-time EWMA (alpha 1/8).
func (l *Limiter) observeHold(d time.Duration) {
	sample := max(int64(d), 1)
	for {
		old := l.avgHold.Load()
		next := sample
		if old != 0 {
			next = old + (sample-old)/8
		}
		if l.avgHold.CompareAndSwap(old, next) {
			return
		}
	}
}

// permit is one admitted job.
type permit struct {
	l     *Limiter
	start time.Time
	cost  int64
	done  atomic.Bool
}

func (p *permit) release() {
	if !p.done.CompareAndSwap(false, true) {
		return
	}
	l := p.l
	l.observeHold(time.Since(p.start))
	l.inFlight.Add(-1)
	l.metrics.AddInFlight(-1)
	if p.cost > 0 {
		l.memory.Release(p.cost)
	}
	l.workers.Release(1)
	l.queue.Release(1)
}
