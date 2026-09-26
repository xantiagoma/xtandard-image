package app

import (
	"sync/atomic"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/limiter"
	"github.com/xantiagoma/xtandard-image/internal/obs/metrics"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// vipsStats backs the xi_vips_* gauges with libvips' tracked counters.
type vipsStats struct{}

// Stats implements metrics.VipsCollector.
func (vipsStats) Stats() metrics.VipsStats {
	s := vips.ReadMemStats()
	return metrics.VipsStats{MemBytes: s.Mem, MemHighwaterBytes: s.MemHighwater, Allocs: s.Allocs}
}

// limiterMetrics adapts the limiter's delta-based hooks to the absolute
// gauges of metrics.LimiterObserver: xi_queue_depth counts jobs holding a
// queue permit (waiting or running), xi_workers_busy the running ones.
type limiterMetrics struct {
	obs      metrics.LimiterObserver
	waiting  atomic.Int64
	inFlight atomic.Int64
}

func (m *limiterMetrics) OnQueued() {}

func (m *limiterMetrics) OnAdmitted(wait time.Duration) { m.obs.Waited(wait) }

func (m *limiterMetrics) OnRejected(r limiter.Reason) {
	switch r {
	case limiter.ReasonQueueFull:
		m.obs.Rejected(metrics.OverloadQueueFull)
	case limiter.ReasonQueueTimeout:
		m.obs.Rejected(metrics.OverloadWaitTimeout)
	case limiter.ReasonMemoryTooLarge:
		m.obs.Rejected(metrics.OverloadMemoryBudget)
	default: // canceled by the client or the request deadline: not an overload
	}
}

func (m *limiterMetrics) AddQueueDepth(delta int64) {
	w := m.waiting.Add(delta)
	m.obs.QueueDepth(int(w + m.inFlight.Load()))
}

func (m *limiterMetrics) AddInFlight(delta int64) {
	n := m.inFlight.Add(delta)
	m.obs.WorkersBusy(int(n))
	m.obs.QueueDepth(int(n + m.waiting.Load()))
}
