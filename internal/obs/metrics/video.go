package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// videoMetrics are the xi_video_transform_* metrics (WP-121, WP-121c):
// the video transformation pool, separate from the image pool's
// xi_queue_depth and xi_workers_busy, and its cost-weighted admission.
type videoMetrics struct {
	duration     *prometheus.HistogramVec
	bytes        *prometheus.CounterVec
	coalesced    prometheus.Counter
	queue        prometheus.Gauge
	busy         prometheus.Gauge
	overload     *prometheus.CounterVec
	cost         prometheus.Histogram
	capacity     prometheus.Gauge
	capacityUsed prometheus.Gauge
	queuedCost   prometheus.Gauge
}

// videoCostBuckets span an audio extraction (tens of units) to a clip
// costing hours of work (cost units are milliseconds of one core).
func videoCostBuckets() []float64 {
	return prometheus.ExponentialBuckets(10, 4, 11) // 10 … ~10.5M
}

// videoDurationBuckets span short clips to the default 2 min transcode
// timeout.
func videoDurationBuckets() []float64 {
	return []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 20, 30, 60, 120, 300}
}

func newVideoMetrics() *videoMetrics {
	return &videoMetrics{
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: Namespace,
			Name:      "video_transform_duration_seconds",
			Help:      "Video transformation time from admission to the muxed file, by result (ok or the errs kind).",
			Buckets:   videoDurationBuckets(),
		}, []string{"result"}),
		bytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "video_transform_bytes_total",
			Help:      "Bytes read from video sources (in) and written as transformed files (out).",
		}, []string{"direction"}),
		coalesced: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "video_transform_coalesced_total",
			Help:      "Video transformations answered by joining an identical one in flight.",
		}),
		queue: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "video_transform_queue_depth",
			Help:      "Video transformations holding a place in their pool (probing, waiting or running).",
		}),
		busy: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "video_transform_workers_busy",
			Help:      "Video transformations running (admitted).",
		}),
		overload: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "video_transform_overload_total",
			Help:      "Video transformations rejected by their pool's admission by reason (queue_full, queue_cost, wait_timeout).",
		}, []string{"reason"}),
		cost: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: Namespace,
			Name:      "video_transform_job_cost",
			Help:      "Estimated cost of probed video transformations in cost units (about a millisecond of one core's work), before clamping to the capacity.",
			Buckets:   videoCostBuckets(),
		}),
		capacity: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "video_transform_capacity",
			Help:      "Cost units the video pool's running transformations may hold together.",
		}),
		capacityUsed: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "video_transform_capacity_used",
			Help:      "Cost units held by the running video transformations.",
		}),
		queuedCost: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "video_transform_queued_cost",
			Help:      "Cost units of the video transformations waiting for admission.",
		}),
	}
}

func (v *videoMetrics) collectors() []prometheus.Collector {
	return []prometheus.Collector{
		v.duration, v.bytes, v.coalesced, v.queue, v.busy, v.overload,
		v.cost, v.capacity, v.capacityUsed, v.queuedCost,
	}
}

// VideoObserver is the hook set of the video transformation subsystem
// (internal/media and its limiter); app adapts it.
type VideoObserver interface {
	// Transcoded observes one transcode: result is "ok" or an errs kind
	// name (a bounded set); in and out are bytes.
	Transcoded(result string, d time.Duration, in, out int64)
	Coalesced()
	QueueDepth(n int)
	WorkersBusy(n int)
	// Rejected counts an admission rejection; reason is
	// OverloadQueueFull, OverloadQueueCost or OverloadWaitTimeout,
	// anything else is "other".
	Rejected(reason string)
	// JobCost observes a job's estimated cost in cost units; Capacity,
	// CapacityUsed and QueuedCost set the pool's gauges (cost units).
	JobCost(units float64)
	Capacity(units int64)
	CapacityUsed(units int64)
	QueuedCost(units int64)
}

// Video returns the VideoObserver backed by m.
func (m *Metrics) Video() VideoObserver { return videoHooks{v: m.video} }

type videoHooks struct{ v *videoMetrics }

func (h videoHooks) Transcoded(result string, d time.Duration, in, out int64) {
	h.v.duration.WithLabelValues(result).Observe(d.Seconds())
	h.v.bytes.WithLabelValues("in").Add(float64(in))
	h.v.bytes.WithLabelValues("out").Add(float64(out))
}

func (h videoHooks) Coalesced()        { h.v.coalesced.Inc() }
func (h videoHooks) QueueDepth(n int)  { h.v.queue.Set(float64(n)) }
func (h videoHooks) WorkersBusy(n int) { h.v.busy.Set(float64(n)) }

func (h videoHooks) JobCost(units float64)    { h.v.cost.Observe(units) }
func (h videoHooks) Capacity(units int64)     { h.v.capacity.Set(float64(units)) }
func (h videoHooks) CapacityUsed(units int64) { h.v.capacityUsed.Set(float64(units)) }
func (h videoHooks) QueuedCost(units int64)   { h.v.queuedCost.Set(float64(units)) }

func (h videoHooks) Rejected(reason string) {
	switch reason {
	case OverloadQueueFull, OverloadQueueCost, OverloadWaitTimeout:
	default:
		reason = "other"
	}
	h.v.overload.WithLabelValues(reason).Inc()
}
