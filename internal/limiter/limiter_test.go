package limiter

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// recorder is a race-safe Metrics fake.
type recorder struct {
	mu         sync.Mutex
	queued     int
	admitted   []time.Duration
	rejected   []Reason
	queueDepth int64
	inFlight   int64
}

func (r *recorder) OnQueued() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queued++
}

func (r *recorder) OnAdmitted(wait time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.admitted = append(r.admitted, wait)
}

func (r *recorder) OnRejected(reason Reason) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rejected = append(r.rejected, reason)
}

func (r *recorder) AddQueueDepth(d int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queueDepth += d
}

func (r *recorder) AddInFlight(d int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inFlight += d
}

func (r *recorder) rejections() []Reason {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Reason(nil), r.rejected...)
}

func newLimiter(t *testing.T, cfg Config) (*Limiter, *recorder) {
	t.Helper()
	rec := &recorder{}
	l, err := New(cfg, WithMetrics(rec))
	if err != nil {
		t.Fatalf("New(%+v): %v", cfg, err)
	}
	return l, rec
}

// assertIdle proves no permit leaked: every semaphore can be taken whole,
// the counters are zero and the metric gauges balanced out.
func assertIdle(t *testing.T, l *Limiter, rec *recorder) {
	t.Helper()
	st := l.Stats()
	if !l.queue.TryAcquire(int64(st.Capacity)) {
		t.Fatal("queue semaphore has leaked permits")
	}
	l.queue.Release(int64(st.Capacity))
	if !l.workers.TryAcquire(int64(st.Workers)) {
		t.Fatal("worker semaphore has leaked permits")
	}
	l.workers.Release(int64(st.Workers))
	if l.memory != nil {
		if !l.memory.TryAcquire(l.cfg.MemoryBudget) {
			t.Fatal("memory semaphore has leaked weight")
		}
		l.memory.Release(l.cfg.MemoryBudget)
	}
	if st.Waiting != 0 || st.InFlight != 0 {
		t.Fatalf("Stats() = %+v, want no waiting or in-flight jobs", st)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.queueDepth != 0 || rec.inFlight != 0 {
		t.Fatalf("gauges queue_depth=%d in_flight=%d, want 0", rec.queueDepth, rec.inFlight)
	}
}

func wantKind(t *testing.T, err error, kind errs.Kind, status int) {
	t.Helper()
	if err == nil {
		t.Fatalf("err = nil, want kind %v", kind)
	}
	if got := errs.KindOf(err); got != kind {
		t.Fatalf("KindOf(%v) = %v, want %v", err, got, kind)
	}
	if got := errs.Status(errs.KindOf(err)); got != status {
		t.Fatalf("status = %d, want %d", got, status)
	}
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"minimal", Config{Workers: 1}, false},
		{"full", Config{Workers: 4, QueueSize: 64, QueueTimeout: 5 * time.Second, MemoryBudget: 1 << 30}, false},
		{"zero workers", Config{}, true},
		{"negative queue", Config{Workers: 1, QueueSize: -1}, true},
		{"negative timeout", Config{Workers: 1, QueueTimeout: -time.Second}, true},
		{"negative budget", Config{Workers: 1, MemoryBudget: -1}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() = %v, wantErr %v", err, tc.wantErr)
			}
			if _, nerr := New(tc.cfg); (nerr != nil) != tc.wantErr {
				t.Fatalf("New() = %v, wantErr %v", nerr, tc.wantErr)
			}
		})
	}
}

func TestQueueFullIs429(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		l, rec := newLimiter(t, Config{Workers: 1, QueueSize: 1, QueueTimeout: 5 * time.Second})

		release, err := l.Acquire(t.Context(), 0)
		if err != nil {
			t.Fatalf("first Acquire: %v", err)
		}
		waiterErr := make(chan error, 1)
		go func() {
			r, err := l.Acquire(t.Context(), 0)
			if err == nil {
				r()
			}
			waiterErr <- err
		}()
		synctest.Wait()
		if got := l.Stats(); got.Waiting != 1 || got.InFlight != 1 {
			t.Fatalf("Stats() = %+v, want 1 waiting, 1 in flight", got)
		}

		_, err = l.Acquire(t.Context(), 0)
		wantKind(t, err, errs.KindOverloaded, http.StatusTooManyRequests)
		oe, ok := errors.AsType[*OverloadError](err)
		if !ok || oe.Reason != ReasonQueueFull {
			t.Fatalf("err = %#v, want *OverloadError{Reason: queue_full}", err)
		}
		if oe.RetryAfter < time.Second || oe.RetryAfter%time.Second != 0 {
			t.Fatalf("RetryAfter = %s, want whole seconds >= 1s", oe.RetryAfter)
		}

		release()
		if err := <-waiterErr; err != nil {
			t.Fatalf("queued Acquire: %v", err)
		}
		if diff := cmp.Diff([]Reason{ReasonQueueFull}, rec.rejections()); diff != "" {
			t.Fatalf("rejections (-want +got):\n%s", diff)
		}
		assertIdle(t, l, rec)
	})
}

func TestQueueTimeoutIs503(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		const timeout = 5 * time.Second
		l, rec := newLimiter(t, Config{Workers: 1, QueueSize: 1, QueueTimeout: timeout})

		release, err := l.Acquire(t.Context(), 0)
		if err != nil {
			t.Fatalf("first Acquire: %v", err)
		}
		start := time.Now()
		_, err = l.Acquire(t.Context(), 0)
		if waited := time.Since(start); waited != timeout {
			t.Fatalf("waited %s, want exactly %s", waited, timeout)
		}
		wantKind(t, err, errs.KindUnavailable, http.StatusServiceUnavailable)
		oe, ok := errors.AsType[*OverloadError](err)
		if !ok || oe.Reason != ReasonQueueTimeout {
			t.Fatalf("err = %#v, want *OverloadError{Reason: queue_timeout}", err)
		}
		if !errors.Is(err, errQueueTimeout) {
			t.Fatalf("err = %v, want errQueueTimeout in chain", err)
		}
		// Nothing finished yet, so the queue timeout is the estimate.
		if oe.RetryAfter != timeout {
			t.Fatalf("RetryAfter = %s, want %s", oe.RetryAfter, timeout)
		}

		release()
		if diff := cmp.Diff([]Reason{ReasonQueueTimeout}, rec.rejections()); diff != "" {
			t.Fatalf("rejections (-want +got):\n%s", diff)
		}
		assertIdle(t, l, rec)
	})
}

func TestCancelWhileWaitingReturnsPermit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		kind errs.Kind
		stop func(cancel context.CancelFunc)
		ctx  func(parent context.Context) (context.Context, context.CancelFunc)
	}{
		{
			name: "client disconnect",
			kind: errs.KindCanceled,
			ctx:  context.WithCancel,
			stop: func(cancel context.CancelFunc) { cancel() },
		},
		{
			name: "request deadline before queue timeout",
			kind: errs.KindTimeout,
			ctx: func(p context.Context) (context.Context, context.CancelFunc) {
				return context.WithTimeout(p, time.Second)
			},
			stop: func(context.CancelFunc) { synctest.Sleep(time.Second) },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				l, rec := newLimiter(t, Config{Workers: 1, QueueSize: 4, QueueTimeout: time.Minute})
				release, err := l.Acquire(t.Context(), 0)
				if err != nil {
					t.Fatalf("first Acquire: %v", err)
				}

				ctx, cancel := tc.ctx(t.Context())
				defer cancel()
				errc := make(chan error, 1)
				go func() {
					_, err := l.Acquire(ctx, 0)
					errc <- err
				}()
				synctest.Wait()
				if got := l.Stats().Waiting; got != 1 {
					t.Fatalf("Waiting = %d, want 1", got)
				}

				tc.stop(cancel)
				err = <-errc
				wantKind(t, err, tc.kind, errs.Status(tc.kind))
				if _, ok := errors.AsType[*OverloadError](err); ok {
					t.Fatalf("err = %v, want no Retry-After wrapper for a caller-side stop", err)
				}

				release()
				if diff := cmp.Diff([]Reason{ReasonCanceled}, rec.rejections()); diff != "" {
					t.Fatalf("rejections (-want +got):\n%s", diff)
				}
				assertIdle(t, l, rec)
			})
		})
	}
}

func TestAcquireWithDoneContext(t *testing.T) {
	t.Parallel()
	l, rec := newLimiter(t, Config{Workers: 1})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := l.Acquire(ctx, 0)
	wantKind(t, err, errs.KindCanceled, errs.StatusClientClosedRequest)
	assertIdle(t, l, rec)
}

func TestReleaseAfterCancelIsIdempotent(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		l, rec := newLimiter(t, Config{Workers: 2, QueueSize: 2, QueueTimeout: time.Second, MemoryBudget: 100})
		ctx, cancel := context.WithCancel(t.Context())
		release, err := l.Acquire(ctx, 40)
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		cancel() // the job's context dies mid-processing
		release()
		release() // a second call must not over-release (semaphore would panic)
		assertIdle(t, l, rec)
	})
}

func TestMemoryOversizeIs422(t *testing.T) {
	t.Parallel()
	l, rec := newLimiter(t, Config{Workers: 2, QueueSize: 2, QueueTimeout: time.Second, MemoryBudget: 100})

	_, err := l.Acquire(t.Context(), 101)
	wantKind(t, err, errs.KindLimitExceeded, http.StatusUnprocessableEntity)
	if _, ok := errors.AsType[*OverloadError](err); ok {
		t.Fatalf("err = %v, 422 carries no Retry-After", err)
	}

	// Exactly the budget is admissible; without a budget the cost is ignored.
	release, err := l.Acquire(t.Context(), 100)
	if err != nil {
		t.Fatalf("Acquire(budget): %v", err)
	}
	release()
	unbounded, _ := newLimiter(t, Config{Workers: 1})
	release, err = unbounded.Acquire(t.Context(), 1<<40)
	if err != nil {
		t.Fatalf("Acquire without budget: %v", err)
	}
	release()

	if diff := cmp.Diff([]Reason{ReasonMemoryTooLarge}, rec.rejections()); diff != "" {
		t.Fatalf("rejections (-want +got):\n%s", diff)
	}
	assertIdle(t, l, rec)
}

func TestMemoryWeightedAdmission(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		const timeout = 5 * time.Second
		l, rec := newLimiter(t, Config{Workers: 4, QueueSize: 4, QueueTimeout: timeout, MemoryBudget: 100})

		big, err := l.Acquire(t.Context(), 60)
		if err != nil {
			t.Fatalf("Acquire(60): %v", err)
		}

		// A second 60-byte job fits no worker-wise but not memory-wise: it
		// waits, then times out with 503 and gives its worker back.
		start := time.Now()
		_, err = l.Acquire(t.Context(), 60)
		if waited := time.Since(start); waited != timeout {
			t.Fatalf("waited %s, want %s", waited, timeout)
		}
		wantKind(t, err, errs.KindUnavailable, http.StatusServiceUnavailable)

		// A 40-byte job fits beside it.
		small, err := l.Acquire(t.Context(), 40)
		if err != nil {
			t.Fatalf("Acquire(40): %v", err)
		}

		// A queued 60-byte job is admitted once the big one releases.
		admitted := make(chan error, 1)
		go func() {
			r, err := l.Acquire(t.Context(), 60)
			if err == nil {
				r()
			}
			admitted <- err
		}()
		synctest.Wait()
		if got := l.Stats(); got.Waiting != 1 || got.InFlight != 2 {
			t.Fatalf("Stats() = %+v, want 1 waiting on memory, 2 in flight", got)
		}
		big()
		if err := <-admitted; err != nil {
			t.Fatalf("queued Acquire(60): %v", err)
		}
		small()

		if diff := cmp.Diff([]Reason{ReasonQueueTimeout}, rec.rejections()); diff != "" {
			t.Fatalf("rejections (-want +got):\n%s", diff)
		}
		assertIdle(t, l, rec)
	})
}

// TestReserve covers the two-phase admission of the server: Acquire(ctx, 0)
// for the worker, then Reserve with the planner's estimate.
func TestReserve(t *testing.T) {
	t.Parallel()
	const timeout = 5 * time.Second
	cfg := Config{Workers: 4, QueueSize: 4, QueueTimeout: timeout, MemoryBudget: 100}

	t.Run("oversize is 422", func(t *testing.T) {
		t.Parallel()
		l, rec := newLimiter(t, cfg)
		_, err := l.Reserve(t.Context(), 101)
		wantKind(t, err, errs.KindLimitExceeded, http.StatusUnprocessableEntity)
		if diff := cmp.Diff([]Reason{ReasonMemoryTooLarge}, rec.rejections()); diff != "" {
			t.Fatalf("rejections (-want +got):\n%s", diff)
		}
		assertIdle(t, l, rec)
	})
	t.Run("no budget or no cost", func(t *testing.T) {
		t.Parallel()
		unbounded, rec := newLimiter(t, Config{Workers: 1})
		bounded, brec := newLimiter(t, cfg)
		for _, c := range []struct {
			l    *Limiter
			cost int64
		}{{unbounded, 1 << 40}, {bounded, 0}, {bounded, -1}} {
			release, err := c.l.Reserve(t.Context(), c.cost)
			if err != nil {
				t.Fatalf("Reserve(%d): %v", c.cost, err)
			}
			release()
			release()
		}
		assertIdle(t, unbounded, rec)
		assertIdle(t, bounded, brec)
	})
	t.Run("waits for memory, then times out with 503", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			l, rec := newLimiter(t, cfg)
			held, err := l.Reserve(t.Context(), 60)
			if err != nil {
				t.Fatalf("Reserve(60): %v", err)
			}
			start := time.Now()
			_, err = l.Reserve(t.Context(), 60)
			if waited := time.Since(start); waited != timeout {
				t.Fatalf("waited %s, want %s", waited, timeout)
			}
			wantKind(t, err, errs.KindUnavailable, http.StatusServiceUnavailable)
			if oe, ok := errors.AsType[*OverloadError](err); !ok || oe.Reason != ReasonQueueTimeout {
				t.Fatalf("err = %v, want an *OverloadError for the queue timeout", err)
			}

			// A 40-byte reservation fits beside it; a waiting 60-byte one is
			// granted once the first is released, exactly then.
			small, err := l.Reserve(t.Context(), 40)
			if err != nil {
				t.Fatalf("Reserve(40): %v", err)
			}
			granted := make(chan time.Duration, 1)
			go func() {
				start := time.Now()
				r, err := l.Reserve(t.Context(), 60)
				if err != nil {
					t.Errorf("queued Reserve(60): %v", err)
					granted <- 0
					return
				}
				r()
				granted <- time.Since(start)
			}()
			synctest.Wait()
			synctest.Sleep(time.Second)
			held()
			held() // idempotent
			if d := <-granted; d != time.Second {
				t.Fatalf("queued Reserve granted after %s, want 1s", d)
			}
			small()
			if diff := cmp.Diff([]Reason{ReasonQueueTimeout}, rec.rejections()); diff != "" {
				t.Fatalf("rejections (-want +got):\n%s", diff)
			}
			assertIdle(t, l, rec)
		})
	})
	t.Run("caller canceled", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			l, rec := newLimiter(t, cfg)
			held, err := l.Reserve(t.Context(), 100)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			go func() {
				synctest.Sleep(time.Second)
				cancel()
			}()
			_, err = l.Reserve(ctx, 1)
			if errs.KindOf(err) != errs.KindCanceled {
				t.Fatalf("err = %v, want canceled", err)
			}
			held()
			if diff := cmp.Diff([]Reason{ReasonCanceled}, rec.rejections()); diff != "" {
				t.Fatalf("rejections (-want +got):\n%s", diff)
			}
			assertIdle(t, l, rec)
		})
	})
}

func TestWorkersNeverExceeded(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		const (
			workers = 3
			jobs    = 50
			hold    = 10 * time.Millisecond
		)
		l, rec := newLimiter(t, Config{Workers: workers, QueueSize: jobs, QueueTimeout: time.Minute})

		var cur, peak, failed atomic.Int64
		var wg sync.WaitGroup
		start := time.Now()
		for range jobs {
			wg.Go(func() {
				release, err := l.Acquire(t.Context(), 0)
				if err != nil {
					failed.Add(1)
					return
				}
				defer release()
				n := cur.Add(1)
				for {
					if p := peak.Load(); n <= p || peak.CompareAndSwap(p, n) {
						break
					}
				}
				timer := time.NewTimer(hold)
				<-timer.C
				cur.Add(-1)
			})
		}
		wg.Wait()

		if failed.Load() != 0 {
			t.Fatalf("%d jobs failed", failed.Load())
		}
		if got := peak.Load(); got != workers {
			t.Fatalf("peak in flight = %d, want exactly %d", got, workers)
		}
		// 50 jobs, 3 at a time, 10ms each: ceil(50/3) = 17 rounds.
		if got, want := time.Since(start), 17*hold; got != want {
			t.Fatalf("elapsed %s, want %s", got, want)
		}

		rec.mu.Lock()
		queued, admitted := rec.queued, len(rec.admitted)
		rec.mu.Unlock()
		if queued != jobs || admitted != jobs {
			t.Fatalf("OnQueued=%d OnAdmitted=%d, want %d each", queued, admitted, jobs)
		}
		if len(rec.rejections()) != 0 {
			t.Fatalf("rejections = %v, want none", rec.rejections())
		}
		assertIdle(t, l, rec)
	})
}

func TestMetricsHooks(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		l, rec := newLimiter(t, Config{Workers: 1, QueueSize: 1, QueueTimeout: 2 * time.Second, MemoryBudget: 10})

		_, err := l.Acquire(t.Context(), 11) // memory_too_large, never queued
		wantKind(t, err, errs.KindLimitExceeded, http.StatusUnprocessableEntity)

		first, err := l.Acquire(t.Context(), 0)
		if err != nil {
			t.Fatal(err)
		}
		_, err = l.Acquire(t.Context(), 0) // queue_timeout after 2s
		wantKind(t, err, errs.KindUnavailable, http.StatusServiceUnavailable)

		go func() {
			r, err := l.Acquire(t.Context(), 0) // waits 3s, then admitted
			if err == nil {
				r()
			}
		}()
		synctest.Wait()
		_, err = l.Acquire(t.Context(), 0) // queue_full
		wantKind(t, err, errs.KindOverloaded, http.StatusTooManyRequests)

		rec.mu.Lock()
		depth, inFlight := rec.queueDepth, rec.inFlight
		rec.mu.Unlock()
		if depth != 1 || inFlight != 1 {
			t.Fatalf("gauges queue_depth=%d in_flight=%d, want 1 and 1", depth, inFlight)
		}

		synctest.Sleep(time.Second)
		first()
		synctest.Wait()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err = l.Acquire(ctx, 0) // canceled
		wantKind(t, err, errs.KindCanceled, errs.StatusClientClosedRequest)

		want := []Reason{ReasonMemoryTooLarge, ReasonQueueTimeout, ReasonQueueFull, ReasonCanceled}
		if diff := cmp.Diff(want, rec.rejections()); diff != "" {
			t.Fatalf("rejections (-want +got):\n%s", diff)
		}
		rec.mu.Lock()
		queued, admitted := rec.queued, append([]time.Duration(nil), rec.admitted...)
		rec.mu.Unlock()
		if queued != 3 {
			t.Fatalf("OnQueued = %d, want 3", queued)
		}
		// first: no wait; the goroutine waited from t=2s to t=3s.
		if diff := cmp.Diff([]time.Duration{0, time.Second}, admitted); diff != "" {
			t.Fatalf("admitted waits (-want +got):\n%s", diff)
		}
		assertIdle(t, l, rec)
	})
}

func TestReasonString(t *testing.T) {
	t.Parallel()
	got := []string{
		ReasonQueueFull.String(), ReasonQueueTimeout.String(),
		ReasonMemoryTooLarge.String(), ReasonCanceled.String(), Reason(0).String(),
	}
	want := []string{"queue_full", "queue_timeout", "memory_too_large", "canceled", "unknown"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("(-want +got):\n%s", diff)
	}
}

func TestRetryAfter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cfg     Config
		avgHold time.Duration
		waiting int64
		want    time.Duration
	}{
		{"no sample uses queue timeout", Config{Workers: 2, QueueTimeout: 5 * time.Second}, 0, 0, 5 * time.Second},
		{"no sample no timeout floors at 1s", Config{Workers: 2}, 0, 0, time.Second},
		{"sub-second rounds up", Config{Workers: 2}, 300 * time.Millisecond, 0, time.Second},
		{"one round per workers waiting", Config{Workers: 2}, 1500 * time.Millisecond, 4, 5 * time.Second},
		{"capped", Config{Workers: 1}, 10 * time.Second, 100, MaxRetryAfter},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l, _ := newLimiter(t, tc.cfg)
			l.avgHold.Store(int64(tc.avgHold))
			l.waiting.Store(tc.waiting)
			if got := l.RetryAfter(); got != tc.want {
				t.Fatalf("RetryAfter() = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestHoldAverage(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		l, _ := newLimiter(t, Config{Workers: 1})
		for _, d := range []time.Duration{8 * time.Second, 16 * time.Second} {
			release, err := l.Acquire(t.Context(), 0)
			if err != nil {
				t.Fatal(err)
			}
			synctest.Sleep(d)
			release()
		}
		// 8s, then 8 + (16-8)/8 = 9s.
		if got := time.Duration(l.avgHold.Load()); got != 9*time.Second {
			t.Fatalf("avgHold = %s, want 9s", got)
		}
	})
}

func TestNilMetricsKeepsNop(t *testing.T) {
	t.Parallel()
	l, err := New(Config{Workers: 1}, WithMetrics(nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := l.metrics.(NopMetrics); !ok {
		t.Fatalf("metrics = %T, want NopMetrics", l.metrics)
	}
	release, err := l.Acquire(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

// TestQueueFillAtLeast fills a 2-worker, 2-queue limiter one job at a time
// and checks the fill threshold (/readyz, XI_SERVER_READY_QUEUE_THRESHOLD)
// at every step, then drains it.
func TestQueueFillAtLeast(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		l, rec := newLimiter(t, Config{Workers: 2, QueueSize: 2, QueueTimeout: time.Minute})
		check := func(held int) {
			t.Helper()
			for _, p := range []int{1, 25, 50, 75, 100} {
				want := held*100 >= p*4
				if got := l.QueueFillAtLeast(p); got != want {
					t.Errorf("held %d/4: QueueFillAtLeast(%d) = %v, want %v", held, p, got, want)
				}
			}
			if !l.QueueFillAtLeast(0) {
				t.Errorf("QueueFillAtLeast(0) = false, want always true")
			}
		}
		check(0)
		var releases []func()
		for range 2 { // admitted
			r, err := l.Acquire(t.Context(), 0)
			if err != nil {
				t.Fatalf("Acquire: %v", err)
			}
			releases = append(releases, r)
			check(len(releases))
		}
		done := make(chan struct{}, 2)
		for i := range 2 { // waiting for a worker
			go func() {
				r, err := l.Acquire(t.Context(), 0)
				if err == nil {
					r()
				}
				done <- struct{}{}
			}()
			synctest.Wait()
			check(3 + i)
		}
		for _, r := range releases {
			r()
		}
		<-done
		<-done
		check(0)
		assertIdle(t, l, rec)
	})
}
