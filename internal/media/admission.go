package media

import (
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/limiter"
)

// Cost-weighted admission of the video pool (WP-121c, ADR 0012 §4).
//
// A job's cost is video.EstimateCost of its plan, in cost units
// (milliseconds of one core's work on the reference build), so the pool
// must probe a source before it can admit it. Every job goes through:
//
//  1. a place in the pool: the slots semaphore (MaxJobs + Queue), taken
//     with TryAcquire, so a full pool fails fast (429 + Retry-After). The
//     place is held from arrival to the end of the job, probe included,
//     so the jobs that hold an opened source are bounded;
//  2. the probe (open the source, read its headers, plan): under the
//     probes semaphore (Workers), a few at a time. The probe reads the
//     container's headers and lets FFmpeg's stream-info analysis decode a
//     few packets; no frame of the cut is decoded before admission;
//  3. admission: one of MaxJobs running jobs and cost units of Capacity
//     (two x/sync semaphores, taken in that order). A job that cannot
//     start at once queues, unless the cost already queued plus its own
//     is over QueueCost (429; a job always queues behind nothing); it
//     waits for at most QueueTimeout from its arrival, probe included
//     (503 + Retry-After). A cost above Capacity is clamped to it: the
//     job runs alone instead of being refused.
//
// Fairness: semaphore.Weighted is FIFO. Acquire queues behind every
// earlier waiter, TryAcquire fails while anyone waits, and a release
// wakes waiters from the front only, stopping at the first that does not
// fit (x/sync v0.23.0 semaphore.go, notifyWaiters), so a large job at the
// head is never overtaken by small ones behind it and cannot starve. The
// price is head-of-line blocking: small jobs that arrive after a waiting
// large one wait for it to start.
//
// Prefetches (tryEnter, tryProbe, tryAdmit) never wait: they start only
// when nobody is queued and a job slot and the segment's cost are free
// at that moment.

// WorkerShare is the capacity one worker stands for by default: ten
// seconds of one core's work in cost units. Capacity defaults to
// Workers × WorkerShare and QueueCost to Queue × WorkerShare.
const WorkerShare = 10_000

// Load is the pool's occupancy.
type Load struct {
	// Jobs is the jobs holding a place: probing, queued or running.
	Jobs int64
	// Queued is the probed jobs waiting for a run slot and capacity;
	// Running the admitted ones.
	Queued, Running int64
	// CapacityUsed is the cost units the running jobs hold; QueuedCost
	// the units the queued ones wait for.
	CapacityUsed, QueuedCost int64
}

// Stats is the pool's sizes and occupancy.
type Stats struct {
	Load
	// InFlight is Jobs: every job in the pool holds its source (and, once
	// probed, an opened demuxer) until it ends.
	InFlight int64
	// Capacity, MaxJobs and QueueCost are the pool's bounds (cost units,
	// jobs, cost units); Slots is MaxJobs + Queue.
	Capacity, MaxJobs, QueueCost, Slots int64
}

// errQueueTimeout is the cause of the wait context of a job that did not
// start within QueueTimeout.
var errQueueTimeout = errors.New("media: queue timeout")

// errBusy is a prefetch that did not find the pool idle.
var errBusy = errors.New("media: the pool is busy")

// poolConfig sizes a pool. Every field is final (defaults applied).
type poolConfig struct {
	MaxJobs, Queue, Probes int64
	Capacity, QueueCost    int64
	QueueTimeout           time.Duration
}

// pool is the cost-weighted admission. It is safe for concurrent use.
type pool struct {
	cfg     poolConfig
	slots   *semaphore.Weighted // MaxJobs + Queue
	probes  *semaphore.Weighted // Probes
	jobs    *semaphore.Weighted // MaxJobs
	units   *semaphore.Weighted // Capacity
	metrics Metrics

	mu   sync.Mutex
	load Load // guarded by mu

	avgHold atomic.Int64 // EWMA of the run time of jobs, ns; 0 = no sample yet
}

func newPool(cfg poolConfig, m Metrics) *pool {
	return &pool{
		cfg:     cfg,
		slots:   semaphore.NewWeighted(cfg.MaxJobs + cfg.Queue),
		probes:  semaphore.NewWeighted(cfg.Probes),
		jobs:    semaphore.NewWeighted(cfg.MaxJobs),
		units:   semaphore.NewWeighted(cfg.Capacity),
		metrics: m,
	}
}

// update changes the load under the lock and reports it.
func (p *pool) update(f func(*Load)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f(&p.load)
	p.metrics.Load(p.load)
}

// stats is a snapshot.
func (p *pool) stats() Stats {
	p.mu.Lock()
	l := p.load
	p.mu.Unlock()
	return Stats{
		Load: l, InFlight: l.Jobs,
		Capacity: p.cfg.Capacity, MaxJobs: p.cfg.MaxJobs, QueueCost: p.cfg.QueueCost,
		Slots: p.cfg.MaxJobs + p.cfg.Queue,
	}
}

// clamp is cost as charged: whole units in [1, Capacity].
func (p *pool) clamp(cost float64) int64 {
	if math.IsNaN(cost) || cost < 1 {
		return 1
	}
	if cost >= float64(p.cfg.Capacity) {
		return p.cfg.Capacity
	}
	return int64(math.Ceil(cost))
}

// waitContext bounds a job's wait (probe and queue) by QueueTimeout.
func (p *pool) waitContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if p.cfg.QueueTimeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeoutCause(ctx, p.cfg.QueueTimeout, errQueueTimeout)
}

// ticket is one job's place in the pool, used by one goroutine at a time.
type ticket struct {
	p       *pool
	cost    int64 // units held while running
	running bool
	start   time.Time
	left    bool
}

// enter takes a place for a job: *limiter.OverloadError (KindOverloaded,
// 429) when the pool is full.
func (p *pool) enter(ctx context.Context) (*ticket, error) {
	if ctx.Err() != nil {
		return nil, p.canceled(ctx)
	}
	if !p.slots.TryAcquire(1) {
		return nil, p.reject(limiter.ReasonQueueFull, errs.New(errs.KindOverloaded, "media: video pool full"))
	}
	p.update(func(l *Load) { l.Jobs++ })
	return &ticket{p: p}, nil
}

// tryEnter takes a place for a prefetch only when the pool looks idle:
// nobody queued, a run slot and some capacity free. ok is false when
// nothing was taken.
func (p *pool) tryEnter() (t *ticket, ok bool) {
	p.mu.Lock()
	idle := p.load.Queued == 0 && p.load.Running < p.cfg.MaxJobs && p.load.CapacityUsed < p.cfg.Capacity
	p.mu.Unlock()
	if !idle || !p.slots.TryAcquire(1) {
		return nil, false
	}
	p.update(func(l *Load) { l.Jobs++ })
	return &ticket{p: p}, true
}

// probe runs fn (open and plan) under the probe bound, waiting for it on
// wait (the job's wait context, see waitContext).
func (t *ticket) probe(ctx, wait context.Context, fn func() error) error {
	p := t.p
	if !p.probes.TryAcquire(1) {
		if err := p.probes.Acquire(wait, 1); err != nil {
			return p.waitError(ctx)
		}
	}
	defer p.probes.Release(1)
	return fn()
}

// tryProbe runs fn under the probe bound if a probe slot is free (errBusy
// otherwise).
func (t *ticket) tryProbe(fn func() error) error {
	if !t.p.probes.TryAcquire(1) {
		return errBusy
	}
	defer t.p.probes.Release(1)
	return fn()
}

// admit charges cost (EstimateCost's estimate, clamped) and takes a run
// slot, queueing for them within wait. Errors: *limiter.OverloadError
// with KindOverloaded (429, queued cost over QueueCost) or
// KindUnavailable (503, wait done), KindCanceled / KindTimeout when ctx
// ends first. On error the ticket holds no run slot and no capacity.
func (t *ticket) admit(ctx, wait context.Context, estimate float64) error {
	p := t.p
	cost := p.clamp(estimate)
	p.metrics.JobCost(estimate)
	if p.tryRun(cost) {
		t.started(cost)
		return nil
	}
	p.mu.Lock()
	if queued := p.load.QueuedCost; queued > 0 && queued+cost > p.cfg.QueueCost {
		p.mu.Unlock()
		return p.reject(limiter.ReasonQueueCost, errs.Newf(errs.KindOverloaded,
			"media: queued cost %d + %d over %d", queued, cost, p.cfg.QueueCost))
	}
	p.load.Queued++
	p.load.QueuedCost += cost
	p.metrics.Load(p.load)
	p.mu.Unlock()

	err := p.wait(wait, cost)
	p.update(func(l *Load) {
		l.Queued--
		l.QueuedCost -= cost
	})
	if err != nil {
		return p.waitError(ctx)
	}
	t.started(cost)
	return nil
}

// tryAdmit is admit for a prefetch: it never queues (errBusy when the run
// slot or the cost is not free at once, or anyone waits).
func (t *ticket) tryAdmit(estimate float64) error {
	cost := t.p.clamp(estimate)
	if !t.p.tryRun(cost) {
		return errBusy
	}
	t.p.metrics.JobCost(estimate)
	t.started(cost)
	return nil
}

// tryRun takes a run slot and cost units without waiting (and fails while
// anyone waits for either, keeping the FIFO order).
func (p *pool) tryRun(cost int64) bool {
	if !p.jobs.TryAcquire(1) {
		return false
	}
	if !p.units.TryAcquire(cost) {
		p.jobs.Release(1)
		return false
	}
	return true
}

// wait takes a run slot, then cost units. On error it holds neither.
func (p *pool) wait(ctx context.Context, cost int64) error {
	if err := p.jobs.Acquire(ctx, 1); err != nil {
		return err
	}
	if err := p.units.Acquire(ctx, cost); err != nil {
		p.jobs.Release(1)
		return err
	}
	return nil
}

func (t *ticket) started(cost int64) {
	t.cost, t.running, t.start = cost, true, time.Now()
	t.p.update(func(l *Load) {
		l.Running++
		l.CapacityUsed += cost
	})
}

// leave gives back everything the ticket holds; calling it again does
// nothing.
func (t *ticket) leave() {
	if t.left {
		return
	}
	t.left = true
	p := t.p
	if t.running {
		p.observeHold(time.Since(t.start))
		p.units.Release(t.cost)
		p.jobs.Release(1)
	}
	p.slots.Release(1)
	p.update(func(l *Load) {
		l.Jobs--
		if t.running {
			l.Running--
			l.CapacityUsed -= t.cost
		}
	})
}

// reject reports a 429 or 503.
func (p *pool) reject(r limiter.Reason, err error) error {
	p.metrics.Rejected(r)
	return &limiter.OverloadError{Reason: r, RetryAfter: p.retryAfter(), Err: err}
}

// waitError classifies a failed wait: the caller's context wins,
// otherwise it was the queue timeout.
func (p *pool) waitError(ctx context.Context) error {
	if ctx.Err() != nil {
		return p.canceled(ctx)
	}
	return p.reject(limiter.ReasonQueueTimeout, errs.WithPublic(
		errs.Wrapf(errs.KindUnavailable, errQueueTimeout, "media: not admitted within %s", p.cfg.QueueTimeout),
		"the video service is busy, retry later"))
}

// canceled reports ctx ending before admission.
func (p *pool) canceled(ctx context.Context) error {
	p.metrics.Rejected(limiter.ReasonCanceled)
	return errs.Wrapf(errs.KindOf(ctx.Err()), context.Cause(ctx), "media: wait for the video pool")
}

// retryAfter estimates when a rejected client should retry, as the image
// limiter does: one average job per round of queued jobs ahead of it,
// the queue timeout before any job finished; whole seconds in
// [1s, limiter.MaxRetryAfter].
func (p *pool) retryAfter() time.Duration {
	hold := time.Duration(p.avgHold.Load())
	if hold <= 0 {
		hold = p.cfg.QueueTimeout
	}
	p.mu.Lock()
	queued := p.load.Queued
	p.mu.Unlock()
	rounds := float64(queued/p.cfg.MaxJobs + 1)
	secs := math.Ceil(hold.Seconds() * rounds)
	secs = min(max(secs, 1), limiter.MaxRetryAfter.Seconds())
	return time.Duration(secs) * time.Second
}

// observeHold folds d into the run-time EWMA (alpha 1/8).
func (p *pool) observeHold(d time.Duration) {
	sample := max(int64(d), 1)
	for {
		old := p.avgHold.Load()
		next := sample
		if old != 0 {
			next = old + (sample-old)/8
		}
		if p.avgHold.CompareAndSwap(old, next) {
			return
		}
	}
}
