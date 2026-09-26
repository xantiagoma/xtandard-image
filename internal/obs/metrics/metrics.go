// Package metrics owns the Prometheus metric set of ARCHITECTURE §13 on a
// private registry (go-standards §5). There is no default registry and no
// package-level state: every New returns an independent *Metrics, so calling
// it twice (tests, reloads) never produces a duplicate registration.
//
// Labels are bounded by construction: dialect, format, stage, source name,
// backend type, cache store and overload reason come from fixed sets or from
// configuration, never from request data such as URLs or hosts.
package metrics

import (
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Namespace prefixes every metric name.
const Namespace = "xi"

// Cache results used as the "result" label of xi_cache_requests_total.
const (
	CacheHit  = "hit"
	CacheMiss = "miss"
)

// Overload reasons used as the "reason" label of xi_overload_total.
const (
	OverloadQueueFull    = "queue_full"
	OverloadWaitTimeout  = "wait_timeout"
	OverloadMemoryBudget = "memory_budget"
)

// VipsStats is one snapshot of libvips' tracked allocations.
type VipsStats struct {
	MemBytes          int64 // vips_tracked_get_mem
	MemHighwaterBytes int64 // vips_tracked_get_mem_highwater
	Allocs            int64 // vips_tracked_get_allocs
}

// VipsCollector reports libvips memory statistics. internal/vips implements
// it; metrics reads it on every scrape, so Stats must be cheap and safe for
// concurrent use.
type VipsCollector interface {
	Stats() VipsStats
}

// Options configures New.
type Options struct {
	// Version and Commit label xi_build_info.
	Version string
	Commit  string
	// Vips, when non-nil, backs the xi_vips_* gauges. When nil the gauges
	// are not registered (pure-Go builds, tests).
	Vips VipsCollector
	// DurationBuckets overrides the request/stage/fetch histogram buckets.
	// Nil uses DefaultDurationBuckets().
	DurationBuckets []float64
}

// DefaultDurationBuckets returns the default histogram buckets, spanning
// cache hits (sub-millisecond) to the default 20 s request budget.
func DefaultDurationBuckets() []float64 {
	return []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 20}
}

// Metrics is the process metric set. Its methods are safe for concurrent use.
type Metrics struct {
	reg *prometheus.Registry

	requests        *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
	inFlight        prometheus.Gauge
	stageDuration   *prometheus.HistogramVec
	panics          prometheus.Counter

	fetchDuration  *prometheus.HistogramVec
	sourceBytes    *prometheus.CounterVec
	fetchCoalesced prometheus.Counter

	cacheRequests      *prometheus.CounterVec
	cacheEvictions     *prometheus.CounterVec
	cacheBytes         *prometheus.GaugeVec
	cacheEntries       *prometheus.GaugeVec
	cacheWritesDropped prometheus.Counter
	cacheSkipped       *prometheus.CounterVec

	queueDepth    prometheus.Gauge
	workersBusy   prometheus.Gauge
	semaphoreWait prometheus.Histogram
	overload      *prometheus.CounterVec
}

// New builds the metric set on a fresh private registry, including the Go
// runtime and process collectors.
func New(opts Options) (*Metrics, error) {
	buckets := opts.DurationBuckets
	if buckets == nil {
		buckets = DefaultDurationBuckets()
	}

	m := &Metrics{
		reg: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "requests_total",
			Help:      "Image requests by dialect, HTTP status and output format.",
		}, []string{"dialect", "status", "format"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: Namespace,
			Name:      "request_duration_seconds",
			Help:      "End-to-end image request latency by dialect, HTTP status and output format.",
			Buckets:   buckets,
		}, []string{"dialect", "status", "format"}),
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "requests_in_flight",
			Help:      "Image requests currently being served.",
		}),
		stageDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: Namespace,
			Name:      "stage_duration_seconds",
			Help:      "Duration of each request lifecycle stage (ARCHITECTURE §2).",
			Buckets:   buckets,
		}, []string{"stage"}),
		panics: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "panics_total",
			Help:      "Handler panics recovered by the recover middleware.",
		}),
		fetchDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: Namespace,
			Name:      "source_fetch_duration_seconds",
			Help:      "Source fetch latency by configured source name and backend type.",
			Buckets:   buckets,
		}, []string{"source", "type"}),
		sourceBytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "source_bytes_total",
			Help:      "Bytes downloaded from sources by backend type.",
		}, []string{"type"}),
		fetchCoalesced: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "fetch_coalesced_total",
			Help:      "Fetches served by joining an identical in-flight fetch.",
		}),
		cacheRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "cache_requests_total",
			Help:      "Cache lookups by store and result (hit or miss).",
		}, []string{"store", "result"}),
		cacheEvictions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "cache_evictions_total",
			Help:      "Entries evicted from a bounded cache store to make room.",
		}, []string{"store"}),
		cacheBytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "cache_bytes",
			Help:      "Bytes held by a bounded cache store.",
		}, []string{"store"}),
		cacheEntries: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "cache_entries",
			Help:      "Entries held by a bounded cache store.",
		}, []string{"store"}),
		cacheWritesDropped: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "cache_writes_dropped_total",
			Help:      "Result cache writes dropped because the write queue was full.",
		}),
		cacheSkipped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "cache_skipped_total",
			Help:      "Cache entries a store skipped by store and reason (too_large on write, corrupt on read).",
		}, []string{"store", "reason"}),
		queueDepth: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "queue_depth",
			Help:      "Jobs admitted to the processing queue (waiting or running).",
		}),
		workersBusy: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "workers_busy",
			Help:      "Processing worker slots currently held.",
		}),
		semaphoreWait: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: Namespace,
			Name:      "semaphore_wait_seconds",
			Help:      "Time spent waiting for a processing worker slot.",
			Buckets:   buckets,
		}),
		overload: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "overload_total",
			Help:      "Requests rejected by admission control by reason.",
		}, []string{"reason"}),
	}

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: Namespace,
		Name:      "build_info",
		Help:      "Build metadata; the value is always 1.",
	}, []string{"version", "commit", "goversion"})
	buildInfo.WithLabelValues(orUnknown(opts.Version), orUnknown(opts.Commit), runtime.Version()).Set(1)

	cs := []prometheus.Collector{
		m.requests, m.requestDuration, m.inFlight, m.stageDuration, m.panics,
		m.fetchDuration, m.sourceBytes, m.fetchCoalesced,
		m.cacheRequests, m.cacheEvictions, m.cacheBytes, m.cacheEntries, m.cacheWritesDropped, m.cacheSkipped,
		m.queueDepth, m.workersBusy, m.semaphoreWait, m.overload,
		buildInfo,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	}
	if opts.Vips != nil {
		cs = append(cs, vipsGauges(opts.Vips)...)
	}

	var errs []error
	for _, c := range cs {
		if err := m.reg.Register(c); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("register metrics: %w", err)
	}
	return m, nil
}

func vipsGauges(v VipsCollector) []prometheus.Collector {
	return []prometheus.Collector{
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "vips_mem_bytes",
			Help:      "Bytes currently allocated by libvips (vips_tracked_get_mem).",
		}, func() float64 { return float64(v.Stats().MemBytes) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "vips_mem_highwater_bytes",
			Help:      "Peak bytes allocated by libvips (vips_tracked_get_mem_highwater).",
		}, func() float64 { return float64(v.Stats().MemHighwaterBytes) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "vips_allocs",
			Help:      "Live libvips allocations (vips_tracked_get_allocs).",
		}, func() float64 { return float64(v.Stats().Allocs) }),
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// Registry returns the private registry, for tests and extra collectors.
func (m *Metrics) Registry() *prometheus.Registry { return m.reg }

// Handler serves the registry in the Prometheus exposition format. Mount it
// on the admin listener only.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{
		Registry:          m.reg, // counts promhttp errors on our registry
		EnableOpenMetrics: true,
	})
}

// ObserveRequest records one finished image request.
func (m *Metrics) ObserveRequest(dialect string, status int, format string, d time.Duration) {
	s := strconv.Itoa(status)
	m.requests.WithLabelValues(dialect, s, format).Inc()
	m.requestDuration.WithLabelValues(dialect, s, format).Observe(d.Seconds())
}

// RequestStarted increments the in-flight gauge and returns the matching
// decrement, to be deferred by the caller.
func (m *Metrics) RequestStarted() (done func()) {
	m.inFlight.Inc()
	return m.inFlight.Dec
}

// ObserveStage records the duration of one lifecycle stage.
func (m *Metrics) ObserveStage(stage string, d time.Duration) {
	m.stageDuration.WithLabelValues(stage).Observe(d.Seconds())
}

// IncPanic counts one recovered handler panic.
func (m *Metrics) IncPanic() { m.panics.Inc() }

// ObserveFetch records one source fetch by configured source name and
// backend type (http, s3, gcs, ...), with the number of bytes downloaded.
func (m *Metrics) ObserveFetch(source, backend string, d time.Duration, bytes int64) {
	m.fetchDuration.WithLabelValues(source, backend).Observe(d.Seconds())
	if bytes > 0 {
		m.sourceBytes.WithLabelValues(backend).Add(float64(bytes))
	}
}

// IncFetchCoalesced counts a fetch that joined an in-flight identical fetch.
func (m *Metrics) IncFetchCoalesced() { m.fetchCoalesced.Inc() }

// ObserveCache records one lookup in cache store (memory, disk, redis, s3,
// origin).
func (m *Metrics) ObserveCache(store string, hit bool) {
	result := CacheMiss
	if hit {
		result = CacheHit
	}
	m.cacheRequests.WithLabelValues(store, result).Inc()
}

// ObserveCacheEvictions counts n entries evicted from store.
func (m *Metrics) ObserveCacheEvictions(store string, n int) {
	m.cacheEvictions.WithLabelValues(store).Add(float64(n))
}

// SetCacheSize reports the bytes and entries currently held by store.
func (m *Metrics) SetCacheSize(store string, bytes int64, entries int) {
	m.cacheBytes.WithLabelValues(store).Set(float64(bytes))
	m.cacheEntries.WithLabelValues(store).Set(float64(entries))
}

// IncCacheWriteDropped counts one result cache write dropped by a full
// write queue.
func (m *Metrics) IncCacheWriteDropped() { m.cacheWritesDropped.Inc() }

// ObserveCacheSkip counts one entry store skipped for reason (a bounded
// set: cache.SkipTooLarge, cache.SkipCorrupt).
func (m *Metrics) ObserveCacheSkip(store, reason string) {
	m.cacheSkipped.WithLabelValues(store, reason).Inc()
}
