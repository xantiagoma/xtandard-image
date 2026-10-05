package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// videoMetrics are the xi_video_transform_* metrics (WP-121): the video
// transformation pool, separate from the image pool's xi_queue_depth and
// xi_workers_busy.
type videoMetrics struct {
	duration  *prometheus.HistogramVec
	bytes     *prometheus.CounterVec
	coalesced prometheus.Counter
	queue     prometheus.Gauge
	busy      prometheus.Gauge
	overload  *prometheus.CounterVec
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
			Help:      "Video transformations admitted to their pool (waiting or running).",
		}),
		busy: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "video_transform_workers_busy",
			Help:      "Video transformation worker slots currently held.",
		}),
		overload: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "video_transform_overload_total",
			Help:      "Video transformations rejected by their pool's admission by reason.",
		}, []string{"reason"}),
	}
}

func (v *videoMetrics) collectors() []prometheus.Collector {
	return []prometheus.Collector{v.duration, v.bytes, v.coalesced, v.queue, v.busy, v.overload}
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
	// Rejected counts an admission rejection; reason is one of the
	// Overload* constants, anything else is "other".
	Rejected(reason string)
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

func (h videoHooks) Rejected(reason string) {
	switch reason {
	case OverloadQueueFull, OverloadWaitTimeout:
	default:
		reason = "other"
	}
	h.v.overload.WithLabelValues(reason).Inc()
}
