package metrics

import "time"

// LimiterObserver is the hook set the admission limiter (internal/limiter,
// ARCHITECTURE §11) reports to. It is declared here, next to its
// implementation, so metrics never imports limiter; limiter declares a
// structurally identical interface of its own and app passes
// (*Metrics).Limiter() to it.
//
// All methods are called on the request path and must not block.
type LimiterObserver interface {
	// QueueDepth reports jobs holding a queue permit (waiting or running).
	QueueDepth(n int)
	// WorkersBusy reports worker slots currently held.
	WorkersBusy(n int)
	// Waited reports how long one job waited for a worker slot.
	Waited(d time.Duration)
	// Rejected counts one job refused by admission. reason must be one of
	// the Overload* constants; anything else is recorded as "other" so the
	// label stays bounded.
	Rejected(reason string)
}

// Limiter returns the LimiterObserver backed by m.
func (m *Metrics) Limiter() LimiterObserver { return limiterHooks{m: m} }

type limiterHooks struct{ m *Metrics }

func (h limiterHooks) QueueDepth(n int)       { h.m.queueDepth.Set(float64(n)) }
func (h limiterHooks) WorkersBusy(n int)      { h.m.workersBusy.Set(float64(n)) }
func (h limiterHooks) Waited(d time.Duration) { h.m.semaphoreWait.Observe(d.Seconds()) }

func (h limiterHooks) Rejected(reason string) {
	switch reason {
	case OverloadQueueFull, OverloadWaitTimeout, OverloadMemoryBudget:
	default:
		reason = "other"
	}
	h.m.overload.WithLabelValues(reason).Inc()
}
