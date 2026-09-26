package app

import (
	"sync/atomic"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/limiter"
	"github.com/xantiagoma/xtandard-image/internal/obs/metrics"
	"github.com/xantiagoma/xtandard-image/internal/server"
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

// fetchMetrics adapts fetch.Metrics to xi_source_fetch_duration_seconds,
// xi_source_bytes_total, xi_fetch_coalesced_total and
// xi_cache_requests_total{store="origin"}. types maps a source name to its
// backend type (the {type} label).
type fetchMetrics struct {
	m     *metrics.Metrics
	types map[string]string
}

func (f *fetchMetrics) ObserveFetch(src, _ string, d time.Duration, bytes int64) {
	f.m.ObserveFetch(src, f.types[src], d, bytes)
}

func (f *fetchMetrics) OnCache(r fetch.CacheResult) {
	f.m.ObserveCache("origin", r != fetch.CacheMiss)
}

func (f *fetchMetrics) OnCoalesced() { f.m.IncFetchCoalesced() }

func (f *fetchMetrics) OnRetry(string) {}

// vipsAllocator is the production fetch.Allocator: originals land in C
// memory libvips owns. It never returns a typed nil.
func vipsAllocator(n int) (fetch.Buffer, error) {
	b, err := vips.NewBuffer(n)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// vipsBuffer returns buf as a *vips.Buffer with one extra reference owned
// by the caller, copying Go memory (tests, GoAllocator) into C memory so
// libvips never holds a Go pointer.
func vipsBuffer(buf fetch.Buffer) (*vips.Buffer, error) {
	if vb, ok := buf.(*vips.Buffer); ok {
		vb.Retain()
		return vb, nil
	}
	data := buf.Bytes()
	vb, err := vips.NewBuffer(len(data))
	if err != nil {
		return nil, err
	}
	copy(vb.Bytes(), data)
	return vb, nil
}

// codecSniffer adapts codec.Sniff to server.Sniffer: the Content-Type of
// raw and skip-processing responses comes from magic bytes only.
type codecSniffer struct{}

// Sniff implements server.Sniffer.
func (codecSniffer) Sniff(data []byte) (server.Sniffed, bool) {
	f, ok := codec.Sniff(data)
	if !ok {
		return server.Sniffed{}, false
	}
	out, _ := f.Output()
	return server.Sniffed{Format: out, ContentType: f.MIME(), Ext: f.Ext()}, true
}
